// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// These tests run the 2026-10-20 ruling through the real google family
// ([substrate.Google]), whose per-type answer comes from what each type's
// Create sends at the provider pin. unrecorded_markerless_test.go's
// TestUnrecordedMarkerless_theFamilyDecidesPerType proves the projection
// honours an answer; these prove the family gives the right one.

const (
	gcpFirewallName = "allow-ssh"
	gcpFirewallID   = "projects/" + gcpProject + "/global/firewalls/" + gcpFirewallName
)

// googleFirewallSchema is google_compute_firewall as far as these tests
// need it: no labels map, so no marker surface.
func googleFirewallSchema() providers.Schema {
	str := func() *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": str(), "project": str(), "name": str(), "network": str(),
	}}}
}

func googleFirewallObject() cty.Value {
	return objectFor(googleFirewallSchema(), map[string]string{
		"id": gcpFirewallID, "project": gcpProject, "name": gcpFirewallName, "network": "default",
	})
}

// firewallCloud is a project holding one firewall at the declared name,
// made by somebody else or by this estate.
func firewallCloud() providers.Interface {
	schema := googleFirewallSchema()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"google_compute_firewall": schema},
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
		if r.PriorState.GetAttr("id").AsString() == gcpFirewallID {
			return providers.ReadResourceResponse{NewState: googleFirewallObject()}
		}
		return providers.ReadResourceResponse{NewState: cty.NullVal(schema.Block.ImpliedType())}
	}
	return p
}

func buildFirewall(t *testing.T, recorded bool) (*Result, *RecordStore, error) {
	t.Helper()
	ctx := context.Background()
	addr := mustAddr(t, `google_compute_firewall.ssh`)
	rs := newTestLocatedStore(localHintStore(t), ownershipEstate).rs
	if recorded {
		rec, ok := LocatedRecordFrom("google_compute_firewall", googleFirewallSchema(), googleFirewallObject())
		if !ok {
			t.Fatal("no record derivable for the firewall")
		}
		if _, err := SeedLocatedForInstance(ctx, rs, addr, googleProvider, rec); err != nil {
			t.Fatalf("seeding the firewall's record: %s", err)
		}
	}
	res, diags := BuildWith(ctx, loadConfig(t, "testdata/unrecorded-google-firewall"),
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: gcpFirewallID}},
		SingleProvider(googleProvider, firewallCloud()),
		Options{RecordStore: rs, Ownership: &Ownership{Estate: ownershipEstate}})
	return res, rs, diags.Err()
}

// TestUnrecordedGoogle_foreignFirewallIsNameHeld: a firewall somebody else
// made at the declared name, with no record. Google answers a create there
// with 409 alreadyExists, so the plan stops with the name-held refusal
// instead of proposing a create that would fail at apply.
func TestUnrecordedGoogle_foreignFirewallIsNameHeld(t *testing.T) {
	res, _, err := buildFirewall(t, false)
	if err == nil {
		t.Fatalf("no error: the foreign firewall was planned past (materialized %v, omitted %+v)", res.Materialized, res.Omitted)
	}
	if !strings.Contains(err.Error(), SummaryNameHeld) || !strings.Contains(err.Error(), gcpFirewallID) {
		t.Errorf("want the %q refusal naming %s, got:\n%s", SummaryNameHeld, gcpFirewallID, err)
	}
	assertOmitted(t, res, map[string]Reason{"google_compute_firewall.ssh": ReasonUnowned})
}

// TestUnrecordedGoogle_recordedFirewallBinds: the post-migrate path. With
// the record live-import writes, the same firewall binds as before, in the
// projection and at the plan node.
func TestUnrecordedGoogle_recordedFirewallBinds(t *testing.T) {
	res, rs, err := buildFirewall(t, true)
	if err != nil {
		t.Fatalf("a recorded firewall was refused:\n%s", err)
	}
	assertMaterialized(t, res, []string{"google_compute_firewall.ssh"})

	n := &NodeResolver{RecordStore: rs, Config: loadConfig(t, "testdata/unrecorded-google-firewall")}
	target, found, _ := n.ResolveResourceIdentity(context.Background(), mustAddr(t, `google_compute_firewall.ssh`), cty.NilVal, googleFirewallSchema())
	if !found || target.ID != gcpFirewallID {
		t.Errorf("the node resolver did not bind the recorded firewall: found %v, target %+v", found, target)
	}
}

// TestUnrecordedGoogle_serviceReAddedPlansCreate is gcve's day2_count
// scale-up and greenfield-after-teardown through the real family: enabling
// a service that is already enabled succeeds (the provider's Create takes
// it), so the unrecorded service plans its create rather than stopping.
func TestUnrecordedGoogle_serviceReAddedPlansCreate(t *testing.T) {
	t.Run("day2_count scale-up", func(t *testing.T) {
		rs := migrated(t, gcpKeptService, gcpGoneService)
		destroyedByRecordLeg(t, rs, gcpGoneService)
		cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}
		res := buildServices(t, rs, cloud, gcpKeptService, gcpGoneService)
		assertMaterialized(t, res, []string{serviceAddr(t, gcpKeptService).String()})
		assertOmitted(t, res, map[string]Reason{serviceAddr(t, gcpGoneService).String(): ReasonUnowned})
	})
	t.Run("greenfield after teardown", func(t *testing.T) {
		rs := newTestLocatedStore(localHintStore(t), ownershipEstate).rs
		cloud := &googleCloud{enabled: map[string]bool{gcpKeptService: true, gcpGoneService: true}}
		res := buildServices(t, rs, cloud, gcpKeptService, gcpGoneService)
		assertMaterialized(t, res, nil)
		assertOmitted(t, res, map[string]Reason{
			serviceAddr(t, gcpKeptService).String(): ReasonUnowned,
			serviceAddr(t, gcpGoneService).String(): ReasonUnowned,
		})
	})
}

// buildMovedFirewall is [buildFirewall] over a configuration whose moved
// blocks rename google_compute_firewall.ssh-v1 to .ssh_v2 and on to .ssh,
// with the firewall's record seeded at recordedAt (or nowhere when empty).
func buildMovedFirewall(t *testing.T, recordedAt string) (*Result, error) {
	t.Helper()
	ctx := context.Background()
	addr := mustAddr(t, `google_compute_firewall.ssh`)
	rs := newTestLocatedStore(localHintStore(t), ownershipEstate).rs
	if recordedAt != "" {
		rec, ok := LocatedRecordFrom("google_compute_firewall", googleFirewallSchema(), googleFirewallObject())
		if !ok {
			t.Fatal("no record derivable for the firewall")
		}
		if _, err := SeedLocatedForInstance(ctx, rs, mustAddr(t, recordedAt), googleProvider, rec); err != nil {
			t.Fatalf("seeding the firewall's record at %s: %s", recordedAt, err)
		}
	}
	res, diags := BuildWith(ctx, loadConfig(t, "testdata/unrecorded-google-firewall-moved"),
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: gcpFirewallID}},
		SingleProvider(googleProvider, firewallCloud()),
		Options{RecordStore: rs, Ownership: &Ownership{Estate: ownershipEstate}})
	return res, diags.Err()
}

// TestUnrecordedGoogle_movedFirewallKeepsItsRecord is
// corpus-k8s-infra-gcp-gcve's day2_rename: a stock moved block renames a
// markerless firewall whose record sits under the address it was migrated
// at. That record is this instance's, so the firewall binds and the plan
// is the move, as it was before the 2026-10-20 ruling. Both the direct
// moved-from address and the head of a two-statement chain are covered.
func TestUnrecordedGoogle_movedFirewallKeepsItsRecord(t *testing.T) {
	for _, at := range []string{`google_compute_firewall.ssh_v2`, `google_compute_firewall.ssh-v1`} {
		t.Run(at, func(t *testing.T) {
			res, err := buildMovedFirewall(t, at)
			if err != nil {
				t.Fatalf("a firewall recorded at its moved-from address %s was refused:\n%s", at, err)
			}
			assertMaterialized(t, res, []string{"google_compute_firewall.ssh"})
		})
	}
}

// TestUnrecordedGoogle_recordOutsideTheMoveChainStillRefuses keeps option A
// whole beside the fix above: a record at an address no moved block leads
// from, or no record at all, is no claim on the firewall at .ssh.
func TestUnrecordedGoogle_recordOutsideTheMoveChainStillRefuses(t *testing.T) {
	for _, at := range []string{``, `google_compute_firewall.unrelated`} {
		t.Run("recorded at "+at, func(t *testing.T) {
			res, err := buildMovedFirewall(t, at)
			if err == nil || !strings.Contains(err.Error(), SummaryNameHeld) {
				t.Fatalf("want the %q refusal, got %v", SummaryNameHeld, err)
			}
			assertOmitted(t, res, map[string]Reason{"google_compute_firewall.ssh": ReasonUnowned})
		})
	}
}
