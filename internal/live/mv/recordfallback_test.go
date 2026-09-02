// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// recordFallbackProvider adds the list protocol's method that
// providers.Interface itself does not declare (see
// internal/command/live_mv_test.go's mvProvider, the same shape): the
// stateless list client asks for it by type assertion
// (listclient.asLister), so a *tofu.MockProvider alone - which implements
// providers.Interface but not this - fails that assertion outright rather
// than answering "not listable". This wrapper's ListResourceStream is never
// actually invoked by any test below: every fixture's GetProviderSchemaResponse
// carries no ListResourceTypes entry for recordFallbackType, so
// listclient.ListSchemas reports it unlistable before any List call would be
// made - exactly the real gap these tests stand in for.
type recordFallbackProvider struct {
	*tofu.MockProvider
}

func (recordFallbackProvider) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

// TestFindFallsBackToRecordForANeedsDiscoveryTypeWithNoListSupport and its
// two neighbours are the record-primary consult point ("The order" item 1,
// GitHub issue #364) for the wall the day2-rename-flip unit left named on
// corpus-rds-complete-postgres (aws_db_instance) and corpus-ecs-fargate
// (aws_service_discovery_http_namespace): a provider-assigned, taggable
// type this provider build cannot List used to be unfindable by live-mv no
// matter what. It is not, once an estate has migrated - stamp.go's
// seedIdentityFor already records an identity for every stamped instance,
// and [mover.locateByRecord] is what asks for it.
//
// recordFallbackType ("test_needs_discovery_instance") is a fixture, not a
// real type - deliberately outside identity.DefaultTable, so this file's
// synthetic two-attribute schema is never checked against the real table's
// claims about aws_db_instance (a different guard, schema_check.go's own
// concern, exercised elsewhere). What these tests assert is the WIRING
// (record consulted, verified by a live read, never trusted
// unconditionally), which is independent of any one type's real attributes.

const (
	recordFallbackEstate = "record-fallback-test"
	recordFallbackType   = "test_needs_discovery_instance"
)

// recordFallbackProviderAddr is the provider configuration every fixture
// config below resolves to, matching internal/live/projection's own
// awsProvider test constant.
var recordFallbackProviderAddr = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

func recordFallbackSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":   {Type: cty.String, Computed: true},
				"tags": {Type: cty.Map(cty.String), Optional: true},
			},
		},
	}
}

// recordFallbackObject builds a live object of recordFallbackSchema's
// implied type: an id and, when tags is non-nil, a tags map - which is
// where an ownership marker lives, the only thing locateByIdentity's own
// verification reads.
func recordFallbackObject(id string, tags map[string]string) cty.Value {
	vals := map[string]cty.Value{"id": cty.StringVal(id)}
	if tags == nil {
		vals["tags"] = cty.MapValEmpty(cty.String)
	} else {
		tagVals := make(map[string]cty.Value, len(tags))
		for k, v := range tags {
			tagVals[k] = cty.StringVal(v)
		}
		vals["tags"] = cty.MapVal(tagVals)
	}
	return cty.ObjectVal(vals)
}

// newRecordFallbackProvider is a provider handle that can ImportResourceState
// and ReadResource the objects given (keyed by their "id"), and answers
// GetProviderSchema with NO ListResourceTypes entry for recordFallbackType -
// the "this provider cannot list it" half of the wall these tests exist
// for. It is never asked to List anything; if it were, listclient would see
// an empty Schemas and report not-listable, exactly as the real gap does.
func newRecordFallbackProvider(objects map[string]cty.Value) providers.Interface {
	schema := recordFallbackSchema()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{recordFallbackType: schema},
		},
	}
	p.ConfigureProviderCalled = true

	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		obj, ok := objects[r.Target.ID]
		if !ok {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: obj}},
		}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		idVal := r.PriorState.GetAttr("id")
		if idVal.IsNull() || !idVal.IsKnown() {
			return providers.ReadResourceResponse{NewState: cty.NullVal(schema.Block.ImpliedType())}
		}
		obj, ok := objects[idVal.AsString()]
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(schema.Block.ImpliedType())}
		}
		return providers.ReadResourceResponse{NewState: obj}
	}
	return recordFallbackProvider{p}
}

// recordFallbackLoadConfig is the one-resource fixture every test below
// resolves its anchor address against: a root test_needs_discovery_instance.this with no
// arguments, standing in for the identifier_prefix shape (the object's name
// is not known until create time, so nothing in the block could name it
// even if this test cared to assert one).
func recordFallbackLoadConfig(t *testing.T) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "test_needs_discovery_instance" "this" {
  provider = aws
}
`)
	return loadConfigDir(t, dir)
}

// recordFallbackStore opens a real local record store in a fresh temp
// directory, exactly the on-disk shape live-import and live-mv both talk
// to in the e2e scripts, rather than a hand-rolled in-memory double.
func recordFallbackStore(t *testing.T) *projection.RecordStore {
	t.Helper()
	dir := t.TempDir()
	store, err := staterecord.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("opening the local record store fixture: %s", err)
	}
	return projection.NewRecordEnvelopeStore(store, projection.RecordKeyPrefix(recordFallbackEstate))
}

// TestFindFallsBackToRecordForANeedsDiscoveryTypeWithNoListSupport is the
// "a record-held instance moves" half. The record names the live object by
// its import ID alone (exactly [projection.LocatedRecordFrom]'s shape for a
// single-string identity); the fake provider serves it back tagged for
// req.Old and this estate. find must return that object - read by
// ImportResourceState/ReadResource, never fabricated from the record's own
// fields - and mark the resolution found by identity, not by list.
func TestFindFallsBackToRecordForANeedsDiscoveryTypeWithNoListSupport(t *testing.T) {
	ctx := t.Context()
	newAddr := mustAddr(t, "test_needs_discovery_instance.this")
	oldAddr := mustAddr(t, "test_needs_discovery_instance.old")
	cfg := recordFallbackLoadConfig(t)

	const liveID = "db-source-1234abcd"
	provider := newRecordFallbackProvider(map[string]cty.Value{
		liveID: recordFallbackObject(liveID, map[string]string{
			discovery.TagEstate:  recordFallbackEstate,
			discovery.TagAddress: discovery.EscapeAddress(oldAddr.String()),
		}),
	})

	store := recordFallbackStore(t)
	if _, err := projection.SeedLocatedForInstance(ctx, store, oldAddr, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: liveID}); err != nil {
		t.Fatalf("seeding the record fixture: %s", err)
	}

	m := &mover{
		req: Request{
			Estate: recordFallbackEstate,
			Old:    oldAddr,
			New:    newAddr,
			Config: cfg,
			Resolutions: []identity.Resolution{
				{Addr: newAddr, Class: identity.ClassNeedsDiscovery, Reason: "identifier_prefix, not known until create time"},
			},
			Providers:   projection.SingleProvider(recordFallbackProviderAddr, provider),
			RecordStore: store,
		},
		res: &Result{
			Old:       oldAddr,
			New:       newAddr,
			TypeName:  recordFallbackType,
			Anchor:    newAddr,
			OldMarker: discovery.EscapeAddress(oldAddr.String()),
			NewMarker: discovery.EscapeAddress(newAddr.String()),
		},
		provider: provider,
		schema:   recordFallbackSchema(),
	}

	obj, diags := m.find(ctx)
	if diags.HasErrors() {
		t.Fatalf("find refused a rename a record should have authorized: %s", diags.Err())
	}
	if obj == nil {
		t.Fatal("find returned no object for a record-verified instance")
	}
	got := obj.Value.GetAttr("id")
	if got.IsNull() || got.AsString() != liveID {
		t.Errorf("materialized object has id %#v, want %q read back from the provider - the record's ImportID must be used to READ the object, not stand in for its value", got, liveID)
	}
	if m.res.Path != PathIdentity {
		t.Errorf("Path = %q, want %q: a record-verified instance is found by identity, exactly as a configuration-derived one is", m.res.Path, PathIdentity)
	}
	if m.res.LiveID != liveID {
		t.Errorf("LiveID = %q, want %q", m.res.LiveID, liveID)
	}
}

// TestFindWithNoRecordStillRefusesVerbatim is the no-record boundary: a
// type with no List support and no record for the old address must refuse
// with EXACTLY the pre-existing "No marker search path" wording, whether
// RecordStore is nil (no live block / no record_store configured) or a
// real, empty store. A changed message here would be silently narrowing an
// operator-facing diagnostic that live/LIMITATIONS.md and this stage's own
// e2e scripts quote verbatim.
func TestFindWithNoRecordStillRefusesVerbatim(t *testing.T) {
	const wantSummary = "No marker search path for this resource type"

	for name, store := range map[string]*projection.RecordStore{
		"nil RecordStore":   nil,
		"empty RecordStore": recordFallbackStore(t),
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			newAddr := mustAddr(t, "test_needs_discovery_instance.this")
			oldAddr := mustAddr(t, "test_needs_discovery_instance.old")
			cfg := recordFallbackLoadConfig(t)

			provider := newRecordFallbackProvider(nil)
			m := &mover{
				req: Request{
					Estate: recordFallbackEstate,
					Old:    oldAddr,
					New:    newAddr,
					Config: cfg,
					Resolutions: []identity.Resolution{
						{Addr: newAddr, Class: identity.ClassNeedsDiscovery, Reason: "identifier_prefix, not known until create time"},
					},
					Providers:   projection.SingleProvider(recordFallbackProviderAddr, provider),
					RecordStore: store,
				},
				res: &Result{
					Old:       oldAddr,
					New:       newAddr,
					TypeName:  recordFallbackType,
					Anchor:    newAddr,
					OldMarker: discovery.EscapeAddress(oldAddr.String()),
					NewMarker: discovery.EscapeAddress(newAddr.String()),
				},
				provider: provider,
				schema:   recordFallbackSchema(),
			}

			obj, diags := m.find(ctx)
			if obj != nil {
				t.Errorf("find returned an object with no record and no list support; nothing may be rewritten")
			}
			if !diags.HasErrors() {
				t.Fatal("find accepted a rename with no marker search path at all")
			}
			var found bool
			for _, d := range diags {
				if d.Description().Summary == wantSummary {
					found = true
					detail := d.Description().Detail
					for _, want := range []string{"can only be found by listing the type", "cannot list it", "needs list support"} {
						if !strings.Contains(detail, want) {
							t.Errorf("the refusal detail changed; missing %q:\n%s", want, detail)
						}
					}
				}
			}
			if !found {
				t.Errorf("no %q diagnostic; got %v", wantSummary, diags)
			}
		})
	}
}

// TestFindRecordFoundButStaleRefusesHonestly is the record's own boundary:
// a record exists for the old address, but the live object it points at no
// longer (or never did) carry req.Old's marker for this estate. The record
// is a cache; only the live read is the truth (HANDOFF.md, "a wrong marker
// outranks a missing one"). find must refuse - with locateByIdentity's own
// honest account of what the object actually carries, never the generic
// "No marker search path" text, and never a silent write.
func TestFindRecordFoundButStaleRefusesHonestly(t *testing.T) {
	const wantNotSummary = "No marker search path for this resource type"

	ctx := t.Context()
	newAddr := mustAddr(t, "test_needs_discovery_instance.this")
	oldAddr := mustAddr(t, "test_needs_discovery_instance.old")
	cfg := recordFallbackLoadConfig(t)

	const liveID = "db-stale-9999"
	// The live object's marker names a THIRD address, in the same estate -
	// not req.Old and not req.New - the shape a record can go stale into
	// when the object was renamed again by hand, or the record was seeded
	// from a different migration run.
	otherAddr := mustAddr(t, "test_needs_discovery_instance.unrelated")
	provider := newRecordFallbackProvider(map[string]cty.Value{
		liveID: recordFallbackObject(liveID, map[string]string{
			discovery.TagEstate:  recordFallbackEstate,
			discovery.TagAddress: discovery.EscapeAddress(otherAddr.String()),
		}),
	})

	store := recordFallbackStore(t)
	if _, err := projection.SeedLocatedForInstance(ctx, store, oldAddr, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: liveID}); err != nil {
		t.Fatalf("seeding the stale record fixture: %s", err)
	}

	m := &mover{
		req: Request{
			Estate: recordFallbackEstate,
			Old:    oldAddr,
			New:    newAddr,
			Config: cfg,
			Resolutions: []identity.Resolution{
				{Addr: newAddr, Class: identity.ClassNeedsDiscovery, Reason: "identifier_prefix, not known until create time"},
			},
			Providers:   projection.SingleProvider(recordFallbackProviderAddr, provider),
			RecordStore: store,
		},
		res: &Result{
			Old:       oldAddr,
			New:       newAddr,
			TypeName:  recordFallbackType,
			Anchor:    newAddr,
			OldMarker: discovery.EscapeAddress(oldAddr.String()),
			NewMarker: discovery.EscapeAddress(newAddr.String()),
		},
		provider: provider,
		schema:   recordFallbackSchema(),
	}

	obj, diags := m.find(ctx)
	if obj != nil {
		t.Fatal("find returned an object for a stale record; the live object it points at does not carry req.Old's marker, so nothing may be rewritten")
	}
	if !diags.HasErrors() {
		t.Fatal("find accepted a rename whose record points at a live object carrying a DIFFERENT address's marker")
	}
	for _, d := range diags {
		if d.Description().Summary == wantNotSummary {
			t.Errorf("a stale record produced the generic no-search-path refusal instead of an honest account of what the live object actually carries:\n%s", d.Description().Detail)
		}
	}
}
