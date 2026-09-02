// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"os"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statefile"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file drives the whole package - Ratify then Approve - over the
// committed fixture testdata/small.tfstate, through a fake provider that
// never touches a network. The fixture carries one resource for every
// ratification verdict this package can produce, plus two that need Approve
// to tell them apart even though Ratify calls both VERIFIED: one already
// carrying this estate's markers (idempotent no-op) and one already carrying
// another estate's (refused).

const testEstate = "acme"

// ---------------------------------------------------------------------------
// Loading the fixture
// ---------------------------------------------------------------------------

func loadFixtureState(t *testing.T) *states.State {
	t.Helper()

	f, err := os.Open("testdata/small.tfstate")
	if err != nil {
		t.Fatalf("opening the fixture: %v", err)
	}
	defer f.Close()

	file, err := statefile.Read(f, encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatalf("reading the fixture as a tfstate: %v", err)
	}
	return file.State
}

// ---------------------------------------------------------------------------
// The fake cloud
// ---------------------------------------------------------------------------

// fakeProviders is the [Providers] seam, backed by one mock provider every
// resource in the fixture shares - the fixture declares a single provider
// configuration, so one mock answers for all of it.
type fakeProviders struct {
	provider providers.Interface
}

func (f *fakeProviders) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return f.provider, nil
}

// fakeCloud is the live object store behind the mock provider: keyed by
// "type/id", it is what ReadResource answers from and what ApplyResourceChange
// overwrites, so a test can assert on what a stamp actually wrote.
type fakeCloud struct {
	objects map[string]cty.Value // "type/id" -> live object, or absent for "does not exist"
	applied []string             // "type/id" of every successful apply, in order
}

func schemas() map[string]providers.Schema {
	strMap := cty.Map(cty.String)
	tagsAttr := &configschema.Attribute{Type: strMap, Optional: true}
	idAttr := &configschema.Attribute{Type: cty.String, Computed: true}

	return map[string]providers.Schema{
		"aws_vpc": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":         idAttr,
			"cidr_block": {Type: cty.String, Required: true},
			"tags":       tagsAttr,
		}}},
		"aws_security_group": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":          idAttr,
			"description": {Type: cty.String, Optional: true},
			"tags":        tagsAttr,
		}}},
		"aws_subnet": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":         idAttr,
			"cidr_block": {Type: cty.String, Required: true},
			"tags":       tagsAttr,
		}}},
		// aws_route deliberately has no tags attribute at all: it is this
		// fixture's UNTAGGABLE row, and real aws_route has none either.
		"aws_route": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":                     idAttr,
			"route_table_id":         {Type: cty.String, Required: true},
			"destination_cidr_block": {Type: cty.String, Required: true},
		}}},
		"aws_eip": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":   idAttr,
			"tags": tagsAttr,
		}}},
		"aws_lb": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":   idAttr,
			"tags": tagsAttr,
		}}},
	}
}

func newFakeCloud() *fakeCloud {
	c := &fakeCloud{objects: make(map[string]cty.Value)}

	c.objects["aws_vpc/vpc-clean"] = cty.ObjectVal(map[string]cty.Value{
		"id":         cty.StringVal("vpc-clean"),
		"cidr_block": cty.StringVal("10.0.0.0/16"),
		"tags":       cty.MapVal(map[string]cty.Value{"Name": cty.StringVal("main")}),
	})
	// Drifted: the live description no longer matches what the state
	// recorded.
	c.objects["aws_security_group/sg-drifted"] = cty.ObjectVal(map[string]cty.Value{
		"id":          cty.StringVal("sg-drifted"),
		"description": cty.StringVal("live has moved on"),
		"tags":        cty.MapValEmpty(cty.String),
	})
	// subnet-gone is deliberately absent: ReadResource for it returns a null
	// NewState, which is what "the live system reports this no longer
	// exists" looks like.
	c.objects["aws_eip/eip-already"] = cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("eip-already"),
		"tags": cty.MapVal(map[string]cty.Value{
			"tofu-estate":  cty.StringVal(testEstate),
			"tofu-address": cty.StringVal("aws_eip.already"),
		}),
	})
	c.objects["aws_lb/lb-conflict"] = cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal("lb-conflict"),
		"tags": cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("other-team")}),
	})
	return c
}

func (c *fakeCloud) provider() providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: schemas(),
		},
	}
	p.ConfigureProviderCalled = true

	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		id := r.PriorState.GetAttr("id").AsString()
		key := r.TypeName + "/" + id
		obj, ok := c.objects[key]
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: obj}
	}

	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}

	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		id := r.PriorState.GetAttr("id").AsString()
		key := r.TypeName + "/" + id
		c.objects[key] = r.PlannedState
		c.applied = append(c.applied, key)
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}

	return p
}

func (c *fakeCloud) tagsOf(t *testing.T, key string) map[string]string {
	t.Helper()
	obj, ok := c.objects[key]
	if !ok {
		t.Fatalf("no live object recorded for %s", key)
	}
	if !obj.Type().HasAttribute("tags") {
		return nil
	}
	out := map[string]string{}
	tagsVal := obj.GetAttr("tags")
	if tagsVal.IsNull() {
		return out
	}
	for it := tagsVal.ElementIterator(); it.Next(); {
		k, v := it.Element()
		out[k.AsString()] = v.AsString()
	}
	return out
}

// ---------------------------------------------------------------------------
// Ratify
// ---------------------------------------------------------------------------

func TestRatify_classifiesEveryStatus(t *testing.T) {
	state := loadFixtureState(t)
	cloud := newFakeCloud()

	rat, diags := Ratify(context.Background(), Request{
		Estate:    testEstate,
		State:     state,
		Providers: &fakeProviders{provider: cloud.provider()},
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}

	got := make(map[string]Entry, len(rat.Entries))
	for _, e := range rat.Entries {
		got[e.Addr.String()] = e
	}

	want := map[string]Status{
		"aws_vpc.main":                    StatusVerified,
		"aws_security_group.main":         StatusDrifted,
		"aws_subnet.gone":                 StatusMissing,
		"aws_route.default":               StatusUntaggable,
		"aws_totally_fictional_type.nope": StatusUnadmittedType,
		"aws_eip.already":                 StatusVerified,
		"aws_lb.conflict":                 StatusVerified,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d (data sources and unrelated resources must be excluded):\n%+v", len(got), len(want), got)
	}
	for addr, wantStatus := range want {
		e, ok := got[addr]
		if !ok {
			t.Errorf("no entry for %s", addr)
			continue
		}
		if e.Status != wantStatus {
			t.Errorf("%s: status = %s, want %s (detail: %s)", addr, e.Status, wantStatus, e.Detail)
		}
		if e.Detail == "" {
			t.Errorf("%s: empty Detail", addr)
		}
	}

	sgDrift := got["aws_security_group.main"]
	if len(sgDrift.Drifted) == 0 {
		t.Errorf("aws_security_group.main is DRIFTED but reports no drifted attributes")
	}

	// Ratify must never write: nothing was applied.
	if len(cloud.applied) != 0 {
		t.Errorf("Ratify applied to %v; it must never write", cloud.applied)
	}
}

func TestRatify_rejectsBadInput(t *testing.T) {
	cloud := newFakeCloud()
	provs := &fakeProviders{provider: cloud.provider()}
	state := loadFixtureState(t)

	if _, diags := Ratify(context.Background(), Request{Estate: "Not Valid", State: state, Providers: provs}); !diags.HasErrors() {
		t.Error("an invalid estate name did not produce an error")
	}
	if _, diags := Ratify(context.Background(), Request{Estate: testEstate, State: nil, Providers: provs}); !diags.HasErrors() {
		t.Error("a nil state did not produce an error")
	}
	if _, diags := Ratify(context.Background(), Request{Estate: testEstate, State: state, Providers: nil}); !diags.HasErrors() {
		t.Error("nil providers did not produce an error")
	}
}

// ---------------------------------------------------------------------------
// Approve
// ---------------------------------------------------------------------------

// TestApprove_stampsOnlyEligibleResources is the ratify-report-first claim
// made concrete: Approve's outcomes line up exactly with what Ratify found,
// and only VERIFIED/DRIFTED resources without a marker conflict receive a
// write.
func TestApprove_stampsOnlyEligibleResources(t *testing.T) {
	state := loadFixtureState(t)
	cloud := newFakeCloud()

	rat, diags := Ratify(context.Background(), Request{
		Estate:    testEstate,
		State:     state,
		Providers: &fakeProviders{provider: cloud.provider()},
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}

	stampRep, stampDiags := rat.Approve(context.Background())
	if stampDiags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", stampDiags.Err())
	}

	outcomes := make(map[string]StampOutcome, len(stampRep.Outcomes))
	for _, o := range stampRep.Outcomes {
		outcomes[o.Addr.String()] = o
	}

	wantOutcome := map[string]Outcome{
		"aws_vpc.main":                    OutcomeStamped,
		"aws_security_group.main":         OutcomeStamped,
		"aws_subnet.gone":                 OutcomeSkipped,
		"aws_route.default":               OutcomeSkipped,
		"aws_totally_fictional_type.nope": OutcomeSkipped,
		"aws_eip.already":                 OutcomeAlreadyStamped,
		"aws_lb.conflict":                 OutcomeFailed,
	}
	for addr, want := range wantOutcome {
		o, ok := outcomes[addr]
		if !ok {
			t.Errorf("no stamp outcome for %s", addr)
			continue
		}
		if o.Outcome != want {
			t.Errorf("%s: outcome = %s, want %s (detail: %s)", addr, o.Outcome, want, o.Detail)
		}
	}

	// The two writes landed, and landed with both marker keys.
	for _, addr := range []string{"aws_vpc/vpc-clean", "aws_security_group/sg-drifted"} {
		tags := cloud.tagsOf(t, addr)
		if tags["tofu-estate"] != testEstate {
			t.Errorf("%s: tofu-estate = %q, want %q", addr, tags["tofu-estate"], testEstate)
		}
	}
	if got := cloud.tagsOf(t, "aws_vpc/vpc-clean")["tofu-address"]; got != "aws_vpc.main" {
		t.Errorf("aws_vpc.main: tofu-address = %q, want aws_vpc.main", got)
	}
	if got := cloud.tagsOf(t, "aws_security_group/sg-drifted")["tofu-address"]; got != "aws_security_group.main" {
		t.Errorf("aws_security_group.main: tofu-address = %q, want aws_security_group.main", got)
	}
	// A tag that was not a marker survives the write untouched.
	if got := cloud.tagsOf(t, "aws_vpc/vpc-clean")["Name"]; got != "main" {
		t.Errorf("aws_vpc.main: Name tag disturbed, got %q", got)
	}

	// Nothing else was applied: the already-stamped, conflicting, missing,
	// untaggable and unadmitted resources never reached ApplyResourceChange.
	if len(cloud.applied) != 2 {
		t.Errorf("applied = %v, want exactly the two eligible resources", cloud.applied)
	}

	// The conflicting resource's marker was left exactly as it was.
	if got := cloud.tagsOf(t, "aws_lb/lb-conflict")["tofu-estate"]; got != "other-team" {
		t.Errorf("aws_lb.conflict: tofu-estate was overwritten to %q", got)
	}
}

// TestApprove_secondRunIsIdempotent proves the estate's own re-run story: a
// second live-import over the same estate does not re-apply resources it
// already stamped.
func TestApprove_secondRunIsIdempotent(t *testing.T) {
	state := loadFixtureState(t)
	cloud := newFakeCloud()
	provs := &fakeProviders{provider: cloud.provider()}

	rat1, diags := Ratify(context.Background(), Request{Estate: testEstate, State: state, Providers: provs})
	if diags.HasErrors() {
		t.Fatalf("first Ratify: %s", diags.Err())
	}
	if _, diags := rat1.Approve(context.Background()); diags.HasErrors() {
		t.Fatalf("first Approve: %s", diags.Err())
	}
	firstApplyCount := len(cloud.applied)

	rat2, diags := Ratify(context.Background(), Request{Estate: testEstate, State: state, Providers: provs})
	if diags.HasErrors() {
		t.Fatalf("second Ratify: %s", diags.Err())
	}
	stampRep2, diags := rat2.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("second Approve: %s", diags.Err())
	}

	if len(cloud.applied) != firstApplyCount {
		t.Errorf("the second Approve applied again: %v", cloud.applied)
	}
	for _, o := range stampRep2.Outcomes {
		switch o.Addr.String() {
		case "aws_vpc.main", "aws_security_group.main", "aws_eip.already":
			if o.Outcome != OutcomeAlreadyStamped {
				t.Errorf("%s on the second run: outcome = %s, want ALREADY_STAMPED (detail: %s)", o.Addr, o.Outcome, o.Detail)
			}
		}
	}
}

// TestApprove_neverWritesWithoutBeingCalled is a compile-time-adjacent
// sanity check as much as anything: Ratify alone must never touch
// ApplyResourceChange, which the fixture-wide test above already checks, but
// this isolates the claim to a single resource so a future regression fails
// fast and close to the cause.
func TestApprove_neverWritesWithoutBeingCalled(t *testing.T) {
	state := loadFixtureState(t)
	cloud := newFakeCloud()

	_, diags := Ratify(context.Background(), Request{
		Estate:    testEstate,
		State:     state,
		Providers: &fakeProviders{provider: cloud.provider()},
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	if got := cloud.tagsOf(t, "aws_vpc/vpc-clean")["tofu-estate"]; got != "" {
		t.Fatalf("Ratify alone wrote tofu-estate = %q; it must write nothing", got)
	}
}
