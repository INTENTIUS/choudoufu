// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The maintainer's ruling of 2026-10-20 ("option A"): with a record store in
// play, a declared instance of a markerless type whose identity comes from
// its configuration, and which has no current record, is not owned even
// though an object is live at that identity.
//
// The motivating run is corpus-k8s-infra-gcp-gcve's day2_count on floci-gcp:
// live-import recorded every google_project_service, dropping one for_each
// key destroyed it (the record became a tombstone) and left the service
// enabled, because the root leaves disable_on_destroy unset. Putting the key
// back, stock planned one add and choudoufu planned "No changes": the
// object at the declared identity was admitted with nothing asked, and the
// node's resolver imported the same configuration-derived identity.
//
// These tests walk that sequence against the real record store: records as
// a migration writes them, a destroy through the store's own tombstone
// writer, the object left in the cloud, the key declared again.

const (
	gcpProject     = "broadcom-451918"
	gcpKeptService = "compute.googleapis.com"
	gcpGoneService = "essentialcontacts.googleapis.com"
)

var googleProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("google"),
}

// googleServiceSchema is google_project_service as far as these tests need
// it: no labels map, so no marker surface, and an id the provider sets to
// project/service.
func googleServiceSchema() providers.Schema {
	str := func() *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": str(), "project": str(), "service": str(),
	}}}
}

func googleServiceID(service string) string { return gcpProject + "/" + service }

func googleServiceObject(service string) cty.Value {
	return objectFor(googleServiceSchema(), map[string]string{
		"id": googleServiceID(service), "project": gcpProject, "service": service,
	})
}

// googleCloud is a project whose services are enabled or not. A service
// left enabled after its destroy is simply still in the map.
type googleCloud struct {
	enabled map[string]bool
}

func (c *googleCloud) provider() providers.Interface {
	schema := googleServiceSchema()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"google_project_service": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    objectFor(schema, map[string]string{"id": r.Target.ID}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		id := r.PriorState.GetAttr("id").AsString()
		for service, on := range c.enabled {
			if on && googleServiceID(service) == id {
				return providers.ReadResourceResponse{NewState: googleServiceObject(service)}
			}
		}
		return providers.ReadResourceResponse{NewState: cty.NullVal(schema.Block.ImpliedType())}
	}
	return p
}

func serviceAddr(t *testing.T, service string) addrs.AbsResourceInstance {
	t.Helper()
	return mustAddr(t, `google_project_service.project["`+service+`"]`)
}

// serviceResolutions is what identity resolution hands the projection for
// the fixture's declared keys: identities straight out of configuration.
func serviceResolutions(t *testing.T, services ...string) []identity.Resolution {
	t.Helper()
	out := make([]identity.Resolution, 0, len(services))
	for _, s := range services {
		out = append(out, identity.Resolution{
			Addr:     serviceAddr(t, s),
			Class:    identity.ClassConcrete,
			ImportID: googleServiceID(s),
		})
	}
	return out
}

// migrated is the store as live-import leaves it: one identity record per
// instance, derived by the same LocatedRecordFrom every writer uses.
func migrated(t *testing.T, services ...string) *RecordStore {
	t.Helper()
	ctx := context.Background()
	rs := newTestLocatedStore(localHintStore(t), ownershipEstate).rs
	for _, s := range services {
		rec, ok := LocatedRecordFrom("google_project_service", googleServiceSchema(), googleServiceObject(s))
		if !ok {
			t.Fatalf("no record derivable for %s", s)
		}
		if _, err := SeedLocatedForInstance(ctx, rs, serviceAddr(t, s), googleProvider, rec); err != nil {
			t.Fatalf("seeding %s: %s", s, err)
		}
	}
	return rs
}

// destroyedByRecordLeg is the scale-down apply's write-back for the dropped
// key: the address left the final state, so its record becomes a tombstone.
func destroyedByRecordLeg(t *testing.T, rs *RecordStore, service string) {
	t.Helper()
	ctx := context.Background()
	addr := serviceAddr(t, service)
	_, version, _, found, err := rs.GetIdentity(ctx, addr)
	if err != nil || !found {
		t.Fatalf("no record for %s before its destroy (found %v): %v", addr, found, err)
	}
	if err := rs.tombstone(ctx, addr, version, nil); err != nil {
		t.Fatalf("tombstoning %s: %s", addr, err)
	}
	if _, _, _, found, _ := rs.GetIdentity(ctx, addr); found {
		t.Fatalf("%s still has a current record after its destroy", addr)
	}
}

func buildServices(t *testing.T, rs *RecordStore, cloud *googleCloud, services ...string) *Result {
	t.Helper()
	cfg := loadConfig(t, "testdata/unrecorded-markerless")
	res, diags := BuildWith(context.Background(), cfg, serviceResolutions(t, services...),
		SingleProvider(googleProvider, cloud.provider()),
		Options{RecordStore: rs, Ownership: &Ownership{Estate: ownershipEstate}})
	assertNoErrors(t, diags)
	return res
}

// TestUnrecordedMarkerless_reAddedKeyIsNotOwned is day2_count's scale-up.
// Before the ruling the re-added key materialized, and the plan read "No
// changes" where stock planned one add.
func TestUnrecordedMarkerless_reAddedKeyIsNotOwned(t *testing.T) {
	rs := migrated(t, gcpKeptService, gcpGoneService)
	destroyedByRecordLeg(t, rs, gcpGoneService)
	cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}

	cfg := loadConfig(t, "testdata/unrecorded-markerless")
	res, diags := BuildWith(context.Background(), cfg, serviceResolutions(t, gcpKeptService, gcpGoneService),
		SingleProvider(googleProvider, cloud.provider()),
		Options{RecordStore: rs, Ownership: &Ownership{Estate: ownershipEstate}})
	assertNoErrors(t, diags)

	gone := serviceAddr(t, gcpGoneService).String()
	assertMaterialized(t, res, []string{serviceAddr(t, gcpKeptService).String()})
	assertOmitted(t, res, map[string]Reason{gone: ReasonUnowned})
	if !hasDiag(diags, SummaryUnrecordedMarkerless, googleServiceID(gcpGoneService)) {
		t.Errorf("no %q warning names the object left out:\n%s", SummaryUnrecordedMarkerless, renderDiags(diags))
	}

	// The node must not import it either. (b)'s index carries the
	// configuration-derived resolution, which is exactly what planned
	// "No changes" on the emulator.
	n := &NodeResolver{
		RecordStore: rs,
		MarkerIndex: NewMarkerIndex(serviceResolutions(t, gcpGoneService)),
		Config:      cfg,
	}
	target, found, ndiags := n.ResolveResourceIdentity(context.Background(), serviceAddr(t, gcpGoneService), cty.NilVal, googleServiceSchema())
	if ndiags.HasErrors() {
		t.Fatalf("node resolution failed:\n%s", renderDiags(ndiags))
	}
	if found {
		t.Errorf("the node resolver imported %s at %+v; with no record the object is not owned and the plan must propose the create", gone, target)
	}
}

// TestUnrecordedMarkerless_someoneElsesObjectIsNotOwned: no record and no
// tombstone at all, an object somebody else made at the declared identity.
func TestUnrecordedMarkerless_someoneElsesObjectIsNotOwned(t *testing.T) {
	rs := migrated(t, gcpKeptService)
	cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}

	res := buildServices(t, rs, cloud, gcpKeptService, gcpGoneService)
	assertMaterialized(t, res, []string{serviceAddr(t, gcpKeptService).String()})
	assertOmitted(t, res, map[string]Reason{serviceAddr(t, gcpGoneService).String(): ReasonUnowned})
}

// TestUnrecordedMarkerless_recordedInstancesStillBind is the post-migrate
// path: every instance has a record, and every one binds as before.
func TestUnrecordedMarkerless_recordedInstancesStillBind(t *testing.T) {
	rs := migrated(t, gcpKeptService, gcpGoneService)
	cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}

	res := buildServices(t, rs, cloud, gcpKeptService, gcpGoneService)
	assertMaterialized(t, res, []string{serviceAddr(t, gcpKeptService).String(), serviceAddr(t, gcpGoneService).String()})
	if len(res.Omitted) != 0 {
		t.Errorf("a recorded instance was left out: %+v", res.Omitted)
	}

	n := &NodeResolver{RecordStore: rs, Config: loadConfig(t, "testdata/unrecorded-markerless")}
	target, found, _ := n.ResolveResourceIdentity(context.Background(), serviceAddr(t, gcpGoneService), cty.NilVal, googleServiceSchema())
	if !found || target.ID != googleServiceID(gcpGoneService) {
		t.Errorf("the node resolver did not bind the recorded instance: found %v, target %+v", found, target)
	}
}

// TestUnrecordedMarkerless_noObjectPlansCreate: a re-added key whose
// service really was disabled is an ordinary absence.
func TestUnrecordedMarkerless_noObjectPlansCreate(t *testing.T) {
	rs := migrated(t, gcpKeptService, gcpGoneService)
	destroyedByRecordLeg(t, rs, gcpGoneService)
	cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true}}

	res := buildServices(t, rs, cloud, gcpKeptService, gcpGoneService)
	assertOmitted(t, res, map[string]Reason{serviceAddr(t, gcpGoneService).String(): ReasonAbsent})
}

// TestUnrecordedMarkerless_noRecordStoreKeepsTodaysBehaviour: with no record
// store (no live block at all) there is no record to be missing, and the
// object at the declared identity is admitted as before.
func TestUnrecordedMarkerless_noRecordStoreKeepsTodaysBehaviour(t *testing.T) {
	cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}
	cfg := loadConfig(t, "testdata/unrecorded-markerless")
	res, diags := BuildWith(context.Background(), cfg, serviceResolutions(t, gcpKeptService, gcpGoneService),
		SingleProvider(googleProvider, cloud.provider()),
		Options{Ownership: &Ownership{Estate: ownershipEstate}})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{serviceAddr(t, gcpKeptService).String(), serviceAddr(t, gcpGoneService).String()})
}

// TestUnrecordedMarkerless_adoptAdmits: declared_untagged = "adopt" is the
// operator's way to take such an object in on this run.
func TestUnrecordedMarkerless_adoptAdmits(t *testing.T) {
	rs := migrated(t, gcpKeptService)
	cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}
	cfg := loadConfig(t, "testdata/unrecorded-markerless")
	res, diags := BuildWith(context.Background(), cfg, serviceResolutions(t, gcpKeptService, gcpGoneService),
		SingleProvider(googleProvider, cloud.provider()),
		Options{RecordStore: rs, Ownership: &Ownership{Estate: ownershipEstate, Policy: buildPolicy(t, "", "adopt")}})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{serviceAddr(t, gcpKeptService).String(), serviceAddr(t, gcpGoneService).String()})
}

// TestUnrecordedMarkerless_awsParentOwnedIsUnchanged pins what the ruling
// leaves alone: an untaggable AWS type is an attachment identified by its
// tagged parents, and with no record, and even with a tombstone naming the
// live object, it is admitted at its declared identity as before.
func TestUnrecordedMarkerless_awsParentOwnedIsUnchanged(t *testing.T) {
	for name, tombstone := range map[string]bool{"no record": false, "tombstone only": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			addr := mustAddr(t, `aws_s3_bucket_policy.data`)
			cloud := newFakeCloud()
			cloud.put("aws_s3_bucket_policy", "policy-live", map[string]string{
				"id": "policy-live", "bucket": "ownership-unit-data",
			})
			rs := newTestLocatedStore(localHintStore(t), ownershipEstate).rs
			if tombstone {
				if err := SeedTombstoneForInstance(ctx, rs, addr, TombstoneRecord{ImportID: "policy-live"}); err != nil {
					t.Fatalf("seeding the tombstone: %s", err)
				}
			}
			res, diags := BuildWith(ctx, loadConfig(t, "testdata/named"),
				[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "policy-live"}},
				cloud.providers(t),
				Options{RecordStore: rs, Ownership: &Ownership{Estate: ownershipEstate}})
			assertNoErrors(t, diags)
			assertMaterialized(t, res, []string{"aws_s3_bucket_policy.data"})
			if hasDiag(diags, SummaryUnrecordedMarkerless, "") {
				t.Errorf("the AWS attachment was reported as unrecorded:\n%s", renderDiags(diags))
			}

			n := &NodeResolver{
				RecordStore: rs,
				MarkerIndex: NewMarkerIndex([]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "policy-live"}}),
			}
			if _, found, _ := n.ResolveResourceIdentity(ctx, addr, cty.NilVal, fakeSchemas()["aws_s3_bucket_policy"]); !found {
				t.Errorf("the node resolver no longer binds the AWS attachment from its configuration-derived identity")
			}
		})
	}
}

// TestUnrecordedMarkerless_greenfieldAfterTeardown is gcve's greenfield
// stage: the estate was destroyed, stock's destroy left every service
// enabled (disable_on_destroy unset), and the next apply starts from a fresh
// record store. Every live service is unrecorded and must plan its create,
// as stock's apply does; before the ruling all of them were adopted.
func TestUnrecordedMarkerless_greenfieldAfterTeardown(t *testing.T) {
	rs := newTestLocatedStore(localHintStore(t), ownershipEstate).rs
	cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}

	res := buildServices(t, rs, cloud, gcpKeptService, gcpGoneService)
	assertMaterialized(t, res, nil)
	assertOmitted(t, res, map[string]Reason{
		serviceAddr(t, gcpKeptService).String(): ReasonUnowned,
		serviceAddr(t, gcpGoneService).String(): ReasonUnowned,
	})
}

// perTypeFamily stands in for a provider family that knows, per type,
// whether a create at a held identity collides: the hook the google family
// on gcp-lane implements, where enabling a service that is already enabled
// succeeds and a firewall whose name is taken does not. Everything else is
// Kubernetes', by embedding.
type perTypeFamily struct {
	substrate.Substrate
	answers map[string]substrate.Unrecorded
}

func (perTypeFamily) Name() string            { return "google" }
func (perTypeFamily) ProviderTypes() []string { return []string{"google"} }
func (f perTypeFamily) Unrecorded(typeName string) substrate.Unrecorded {
	if a, ok := f.answers[typeName]; ok {
		return a
	}
	return substrate.UnrecordedCreates
}

func withFamily(t *testing.T, f substrate.Substrate) {
	t.Helper()
	saved := substrate.All
	substrate.All = append([]substrate.Substrate{f}, saved...)
	t.Cleanup(func() { substrate.All = saved })
}

// TestUnrecordedMarkerless_theFamilyDecidesPerType: the same unrecorded
// object plans its create where the family says the create cannot collide,
// and stops at the name-held refusal where it says it would.
func TestUnrecordedMarkerless_theFamilyDecidesPerType(t *testing.T) {
	for name, tc := range map[string]struct {
		answer    substrate.Unrecorded
		wantError bool
	}{
		"idempotent create": {answer: substrate.UnrecordedCreates},
		"create collides":   {answer: substrate.UnrecordedNameHeld, wantError: true},
		"parent-owned":      {answer: substrate.UnrecordedParentOwned},
	} {
		t.Run(name, func(t *testing.T) {
			withFamily(t, perTypeFamily{Substrate: substrate.Kubernetes, answers: map[string]substrate.Unrecorded{"google_project_service": tc.answer}})
			rs := migrated(t, gcpKeptService)
			cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}
			cfg := loadConfig(t, "testdata/unrecorded-markerless")
			res, diags := BuildWith(context.Background(), cfg, serviceResolutions(t, gcpKeptService, gcpGoneService),
				SingleProvider(googleProvider, cloud.provider()),
				Options{RecordStore: rs, Ownership: &Ownership{Estate: ownershipEstate}})
			gone := serviceAddr(t, gcpGoneService).String()
			switch tc.answer {
			case substrate.UnrecordedParentOwned:
				assertNoErrors(t, diags)
				assertMaterialized(t, res, []string{serviceAddr(t, gcpKeptService).String(), gone})
			case substrate.UnrecordedNameHeld:
				if errs := errorDiagsWith(diags, SummaryNameHeld); len(errs) != 1 {
					t.Fatalf("want one %q error, got:\n%s", SummaryNameHeld, renderDiags(diags))
				}
				assertOmitted(t, res, map[string]Reason{gone: ReasonUnowned})
			default:
				assertNoErrors(t, diags)
				assertOmitted(t, res, map[string]Reason{gone: ReasonUnowned})
				if !hasDiag(diags, SummaryUnrecordedMarkerless, googleServiceID(gcpGoneService)) {
					t.Errorf("no create warning:\n%s", renderDiags(diags))
				}
			}
		})
	}
}
