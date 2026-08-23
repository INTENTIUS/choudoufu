// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #365 slice 2's migrate-time gap, found for real by
// live/e2e/corpus-sumaform-aws's own crossing: this package's stamping
// implementation never consulted identity.SelectionFor, so an instance a
// live block's `strict { markers "record" }` selected was still tag-stamped
// during a migrate, and no located record was ever written for it - the
// resolver then read it ABSENT on the very next stateless plan.
//
// Every claim below is asserted at the record store's own rendered value,
// never at a verdict alone, per HANDOFF.md's safety rule: convergence is
// not evidence an identity is right.

const locatedTestEstate = "located-unit"

// locatedTestSource is a one-resource configuration whose live block selects
// aws_vpc.main into the record rung. aws_vpc is a real admitted type
// (internal/live/identity.DefaultTable) with a plain, non-composite,
// non-sensitive "id" - the same shape this run.sh's own header names for
// aws_instance and aws_ebs_volume - so [identity.SelectedLocatedType] admits
// it without any of this test needing to reach into AWS-specific schema
// data.
const locatedTestSource = `
terraform {
  live {
    estate = "` + locatedTestEstate + `"

    record_store "local" {}

    strict {
      marker_repair = "never"

      markers "record" {
        types = ["aws_vpc"]
      }
    }
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.77.0.0/16"
}
`

// loadLocatedConfig loads locatedTestSource the way the command layer loads
// a real working directory: parsed and built, static evaluator and all, so
// identity.SelectionFor reads the same shape a real run would hand it.
func loadLocatedConfig(t *testing.T) *configs.Config {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(locatedTestSource), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)
	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(t.Context(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("fixture unexpectedly calls module %q", req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config: %s", cfgDiags.Error())
	}
	return cfg
}

// locatedVPCSchema is aws_vpc narrowed to what this test needs: a plain
// server-minted "id" and a tags argument, so the SAME resource could have
// been tag-stamped had the selection not covered it - claim 2 below depends
// on that being true.
func locatedVPCSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":         {Type: cty.String, Computed: true},
		"cidr_block": {Type: cty.String, Required: true},
		"tags":       {Type: cty.Map(cty.String), Optional: true},
	}}}
}

// locatedVPCState is a one-resource tfstate for aws_vpc.main, matching
// locatedTestSource, with liveID as its live object's own "id".
func locatedVPCState(liveID string) *states.State {
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_vpc", Name: "main"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"` + liveID + `","cidr_block":"10.77.0.0/16","tags":null}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)
	return state
}

// locatedTestProvider answers locatedVPCSchema, reads back exactly what it
// is given (no drift), and records whether ApplyResourceChange - a tag
// write - was ever called, which for a selected instance it must not be.
type locatedTestProvider struct {
	*tofu.MockProvider
	applyCount int
}

func newLocatedTestProvider() *locatedTestProvider {
	lp := &locatedTestProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"aws_vpc": locatedVPCSchema()},
		},
	}}
	lp.ConfigureProviderCalled = true
	lp.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: r.PriorState}
	}
	lp.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		lp.applyCount++
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	lp.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	return lp
}

func (p *locatedTestProvider) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return p, nil
}

// TestApprove_SelectedInstanceIsLocatedNotStamped is the positive case this
// package never had: given a live block's `markers "record"` selection
// covering aws_vpc.main, Approve writes no tag and instead seeds the
// estate's record store with the live object's own identity - asserted at
// the store, not from the outcome alone.
func TestApprove_SelectedInstanceIsLocatedNotStamped(t *testing.T) {
	const liveID = "vpc-0123456789abcdef0"
	addr := mustAddr(t, "aws_vpc.main")

	cfg := loadLocatedConfig(t)
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	locatedStore := projection.NewRecordEnvelopeStore(store, projection.RecordKeyPrefix(locatedTestEstate))
	p := newLocatedTestProvider()

	rat, diags := Ratify(context.Background(), Request{
		Estate:      locatedTestEstate,
		Config:      cfg,
		State:       locatedVPCState(liveID),
		Providers:   p,
		RecordStore: locatedStore,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	if got := len(rat.Entries); got != 1 {
		t.Fatalf("got %d entries, want 1", got)
	}
	if entry := rat.Entries[0]; entry.Status != StatusVerified {
		t.Fatalf("status = %s, want %s: %s", entry.Status, StatusVerified, entry.Detail)
	}

	// Ratify never writes. The located namespace must still be empty.
	if _, _, _, exists, err := locatedStore.GetIdentity(context.Background(), addr); err != nil {
		t.Fatalf("reading the located store after Ratify: %s", err)
	} else if exists {
		t.Fatalf("Ratify wrote a located record; Ratify must never write")
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeRecorded {
		t.Errorf("outcome = %s, want %s: %s", out.Outcome, OutcomeRecorded, out.Detail)
	}

	// Claim 1: no tag write was attempted at all.
	if p.applyCount != 0 {
		t.Errorf("ApplyResourceChange was called %d time(s); a markers=record selected instance must never be tag-stamped", p.applyCount)
	}

	// Claim 2: the RENDERED IDENTITY, read straight back out of the store,
	// is the live object's own id - not merely "something was written".
	rec, _, _, exists, err := locatedStore.GetIdentity(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading the located record: %s", err)
	}
	if !exists {
		t.Fatalf("no located record was written for %s", addr)
	}
	if rec.ImportID != liveID {
		t.Errorf("located record ImportID = %q, want %q (the live object's own id)", rec.ImportID, liveID)
	}
	if len(rec.Components) != 0 {
		t.Errorf("located record carries components %v; aws_vpc's identity here is a single string", rec.Components)
	}
}

// TestApprove_UnselectedInstanceIsStampedNotLocated is the mutation check:
// the identical resource, provider and state, with no selection at all,
// must be tag-stamped exactly as it always was - and, since GitHub issue
// #364 unit A2, ALSO carries its identity in the record store, alongside
// the marker rather than instead of it. Without the STAMPED half of this
// assertion, TestApprove_SelectedInstanceIsLocatedNotStamped could pass
// merely because located instances are never stamped for some unrelated
// reason - this proves the selection, not the record write, is what keeps
// the two routes apart.
//
// Before unit A2 this test asserted the OPPOSITE of its record half - that
// no located record was written for a stamped instance - which was true
// under the ruling that only an untaggable instance's identity belonged in
// the store. The 2026-08-23 foundation-order ruling widened that to every
// instance, precisely so a migrated estate's next plan can read a record
// first instead of re-deriving every taggable instance's identity from
// configuration; HANDOFF.md's safety rule is why the assertion below reads
// the RENDERED identity by value rather than trusting the outcome alone.
func TestApprove_UnselectedInstanceIsStampedNotLocated(t *testing.T) {
	const liveID = "vpc-unselected000001"
	addr := mustAddr(t, "aws_vpc.main")

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	locatedStore := projection.NewRecordEnvelopeStore(store, projection.RecordKeyPrefix(locatedTestEstate))
	p := newLocatedTestProvider()

	rat, diags := Ratify(context.Background(), Request{
		Estate: locatedTestEstate,
		// No Config at all: identity.SelectionFor(nil) selects nothing,
		// which is every migration's behavior before this field existed.
		State:       locatedVPCState(liveID),
		Providers:   p,
		RecordStore: locatedStore,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeStamped {
		t.Errorf("outcome = %s, want %s: %s", out.Outcome, OutcomeStamped, out.Detail)
	}
	if p.applyCount != 1 {
		t.Errorf("ApplyResourceChange was called %d time(s), want exactly 1 (the tag write)", p.applyCount)
	}
	if rep.IdentitiesRecorded != 1 {
		t.Errorf("StampReport.IdentitiesRecorded = %d, want 1", rep.IdentitiesRecorded)
	}

	// The RENDERED IDENTITY, read straight back out of the store: the live
	// object's own id, exactly what a located instance's own record would
	// carry for the same object - the marker and the record now agree
	// about which object this is, and this assertion is at the value, not
	// merely "something was written".
	rec, _, _, exists, err := locatedStore.GetIdentity(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading the located record: %s", err)
	}
	if !exists {
		t.Fatalf("no identity record was written for a stamped instance; GitHub issue #364 unit A2 records every instance's identity, not only an untaggable one's")
	}
	if rec.ImportID != liveID {
		t.Errorf("identity record ImportID = %q, want %q (the live object's own id)", rec.ImportID, liveID)
	}
	if len(rec.Components) != 0 {
		t.Errorf("identity record carries components %v; aws_vpc's identity here is a single string", rec.Components)
	}
}

// TestApprove_SelectedInstanceWithNoRecordStoreIsSkippedNotStamped is the
// no-store fallback, mirroring [recordOne]'s own: a selection this run
// cannot honour for lack of anywhere to write it must not silently fall
// back to tag-stamping (a half-applied selection is invisible drift the
// next time a record_store IS configured) and must not leave the resource
// unfindable either. It is reported SKIPPED, exactly like a record-backed
// instance with no record_store.
func TestApprove_SelectedInstanceWithNoRecordStoreIsSkippedNotStamped(t *testing.T) {
	const liveID = "vpc-nostore0000000001"
	addr := mustAddr(t, "aws_vpc.main")

	cfg := loadLocatedConfig(t)
	// A real store, deliberately never handed to Ratify as a LocatedStore:
	// the witness that nothing was written anywhere, not a participant.
	witness, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	witnessStore := projection.NewRecordEnvelopeStore(witness, projection.RecordKeyPrefix(locatedTestEstate))
	p := newLocatedTestProvider()

	rat, diags := Ratify(context.Background(), Request{
		Estate:    locatedTestEstate,
		Config:    cfg,
		State:     locatedVPCState(liveID),
		Providers: p,
		// LocatedStore deliberately omitted.
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeSkipped {
		t.Errorf("outcome = %s, want %s with no record store configured: %s", out.Outcome, OutcomeSkipped, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("ApplyResourceChange was called %d time(s); a selected instance with nowhere to record its identity must not fall back to a tag write", p.applyCount)
	}
	if _, _, _, exists, err := witnessStore.GetIdentity(context.Background(), addr); err != nil {
		t.Fatalf("reading the witness store: %s", err)
	} else if exists {
		t.Errorf("something wrote a located record to a store this run was never given")
	}
}
