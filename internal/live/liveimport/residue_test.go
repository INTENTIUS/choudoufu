// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #327: aws_nat_gateway's ForceNew arguments (allocation_id,
// subnet_id) came back null on the FIRST live-plan after a clean migrate,
// because nothing had ever classified them as GitHub issue #275's residue -
// the apply-time write-back that does the classifying never runs during a
// migrate. This file tests Approve's new second write path directly, with a
// provider double standing in for the SDKv2 "preserve whatever the prior
// held" behavior [carriesNoInformation]'s doc comment names as the actual
// mechanism, rather than reaching for a real aws_nat_gateway schema.

// residueSchema is a minimal taggable schema with one Optional argument
// (subnet_id) shaped like a ForceNew argument, and one attribute
// (computed_only) that is neither Required nor Optional.
// [projection.residueCandidates] (unqualified here because this package
// cannot import an unexported name; see its own doc comment) no longer
// excludes computed_only by schema shape alone - a purely Computed
// attribute can be exactly as residue-shaped as an Optional+Computed one,
// which is what corpus-xancloud-iac's aws_nat_gateway.regional_nat_gateway_address
// turned out to be. What still keeps computed_only out of this test's
// residue is [preserveFromPriorProvider] answering it FRESH from both
// reads regardless of prior, which classifyResidue's own two-read
// discriminator correctly reads as "the provider manages this" - the same
// test "tags" already exercises. computed_only stands in for a genuinely
// provider-derived Computed attribute, not for the whole class.
func residueSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":            {Type: cty.String, Computed: true},
		"tags":          {Type: cty.Map(cty.String), Optional: true},
		"subnet_id":     {Type: cty.String, Optional: true},
		"computed_only": {Type: cty.String, Computed: true},
	}}}
}

// preserveFromPriorProvider is a [tofu.MockProvider] whose ReadResource
// reproduces exactly the shape this issue is about: it never reads
// subnet_id from anywhere - it returns whatever PriorState already held,
// null included. tags is always answered fresh (the ordinary case every
// taggable AWS type follows), and computed_only is always answered fresh
// too, so a test can tell "recorded because residue-shaped" apart from
// "recorded because everything gets recorded".
func preserveFromPriorProvider() *tofu.MockProvider {
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		out := map[string]cty.Value{
			"id":            cty.StringVal("ngw-x"),
			"tags":          cty.MapValEmpty(cty.String),
			"subnet_id":     cty.NullVal(cty.String),
			"computed_only": cty.StringVal("fresh-from-remote"),
		}
		prior := r.PriorState
		if prior != cty.NilVal && !prior.IsNull() && prior.Type().HasAttribute("subnet_id") {
			out["subnet_id"] = prior.GetAttr("subnet_id")
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(out)}
	}
	// approveOne's own tag write, accepted as proposed with nothing else
	// changed - the same shape stamp_test.go's capturingProvider uses, so
	// Approve's stamp outcome is OutcomeStamped and a test can assert on it
	// alongside the residue write this file is actually about.
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return p
}

func residueStore(t *testing.T) *projection.RecordStore {
	t.Helper()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return projection.NewRecordEnvelopeStore(store, projection.RecordKeyPrefix("residue-test-estate"))
}

// TestApprove_RecordsResidueForForceNewLikeAttribute is the positive case:
// an Optional argument the provider only ever preserves from whatever prior
// it is given (never truly reads from the remote) gets recorded as residue
// during Approve, from the real applied value Ratify already read - not
// from a null stub.
func TestApprove_RecordsResidueForForceNewLikeAttribute(t *testing.T) {
	addr := mustAddr(t, "aws_nat_gateway.this")
	store := residueStore(t)
	p := preserveFromPriorProvider()

	rat := &Ratification{
		Estate: "residue-test-estate",
		Entries: []Entry{
			{Addr: addr, TypeName: "aws_nat_gateway", Status: StatusVerified},
		},
		eligible: map[string]*eligible{
			addr.String(): {residuable{
				provider: p,
				schema:   residueSchema(),
				typeName: "aws_nat_gateway",
				applied: cty.ObjectVal(map[string]cty.Value{
					"id":            cty.StringVal("ngw-x"),
					"tags":          cty.MapValEmpty(cty.String),
					"subnet_id":     cty.StringVal("subnet-real"),
					"computed_only": cty.StringVal("fresh-from-remote"),
				}),
				identity: cty.NilVal,
			}},
		},
		recordStore: store,
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	if len(rep.Outcomes) != 1 || rep.Outcomes[0].Outcome != OutcomeStamped {
		t.Fatalf("Outcomes = %+v, want one OutcomeStamped", rep.Outcomes)
	}

	attrs, _, _, exists, err := store.GetResidue(context.Background(), addr)
	if err != nil {
		t.Fatalf("store.Get: %s", err)
	}
	if !exists {
		t.Fatalf("no residue was recorded for %s; the FIRST live-plan after this migrate would see subnet_id null and propose a phantom replace, exactly issue #327's bug", addr)
	}
	got, ok := attrs["subnet_id"]
	if !ok {
		t.Fatalf("residue attrs = %v, want subnet_id present", attrs)
	}
	if !got.RawEquals(cty.StringVal("subnet-real")) {
		t.Errorf("residue subnet_id = %#v, want %#v", got, cty.StringVal("subnet-real"))
	}
	if _, ok := attrs["computed_only"]; ok {
		t.Errorf("computed_only was recorded as residue; preserveFromPriorProvider answers it fresh from the remote on both reads, so classifyResidue's discriminator must have judged it provider-managed regardless of it being Computed-only")
	}
	if _, ok := attrs["tags"]; ok {
		t.Errorf("tags was recorded as residue; the provider always answers it fresh in this fixture, so classifyResidue must not have judged it residue-shaped")
	}
}

// nestingSetBlockSchema is aws_autoscaling_group's initial_lifecycle_hook
// shape (GitHub issue #385), reduced to what this migrate-time path needs:
// one NestingSet block ("hooks") with a Required string member and an
// Optional+Computed one, alongside "tags" (always answered fresh, the
// ordinary taggable case) and the identity attribute "id". The point of
// this fixture living HERE rather than only in internal/live/projection's
// own residue_test.go is #327's whole reason for recordResidueFor to
// exist: Approve is a SECOND caller of classifyResidue, reached from a
// migrated state file's real applied value rather than from an apply's
// write-back, and a block-shaped candidate has to survive that path too.
func nestingSetBlockSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"tags": {Type: cty.Map(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"hooks": {
				Nesting: configschema.NestingSet,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"transition":     {Type: cty.String, Required: true},
					"default_result": {Type: cty.String, Optional: true, Computed: true},
				}},
			},
		},
	}}
}

func hooksBlockType() cty.Type {
	return cty.Object(map[string]cty.Type{"transition": cty.String, "default_result": cty.String})
}

// preserveBlockFromPriorProvider is [preserveFromPriorProvider]'s sibling
// for the block-shaped candidate: it never reads "hooks" from anywhere -
// exactly hashicorp/aws's resourceGroupFlatten never sourcing
// initial_lifecycle_hook from DescribeAutoScalingGroups - and answers tags
// fresh on every read, so a test can tell "recorded because residue-shaped"
// apart from "recorded because everything gets recorded".
func preserveBlockFromPriorProvider() *tofu.MockProvider {
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		out := map[string]cty.Value{
			"id":    cty.StringVal("asg-x"),
			"tags":  cty.MapValEmpty(cty.String),
			"hooks": cty.SetValEmpty(hooksBlockType()),
		}
		prior := r.PriorState
		if prior != cty.NilVal && !prior.IsNull() && prior.Type().HasAttribute("hooks") {
			if hooks := prior.GetAttr("hooks"); !hooks.IsNull() {
				out["hooks"] = hooks
			}
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(out)}
	}
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return p
}

// TestApprove_RecordsResidueForANestingSetBlock is GitHub issue #385's own
// crossing, through Approve's migrate-time write path rather than an
// apply's write-back: a NestingSet block the provider's Read never sources
// from the remote at all gets recorded as residue from the migrated state
// file's own real applied value, the same way TestApprove_RecordsResidueForForceNewLikeAttribute
// already proves for a flat attribute. Before commit 6452c3baf6 (#365 slice
// 2) this was excluded outright by residueEligibleBlock's nesting-mode
// filter; this test pins the CURRENT, block-admitting behavior by value so
// a regression there is caught here, at the liveimport call site, and not
// only in internal/live/projection's own unit tests.
func TestApprove_RecordsResidueForANestingSetBlock(t *testing.T) {
	addr := mustAddr(t, "aws_autoscaling_group.this")
	store := residueStore(t)
	p := preserveBlockFromPriorProvider()

	appliedHooks := cty.SetVal([]cty.Value{
		cty.ObjectVal(map[string]cty.Value{"transition": cty.StringVal("autoscaling:EC2_INSTANCE_TERMINATING"), "default_result": cty.StringVal("CONTINUE")}),
		cty.ObjectVal(map[string]cty.Value{"transition": cty.StringVal("autoscaling:EC2_INSTANCE_LAUNCHING"), "default_result": cty.StringVal("")}),
	})

	rat := &Ratification{
		Estate: "residue-test-estate",
		Entries: []Entry{
			{Addr: addr, TypeName: "aws_autoscaling_group", Status: StatusVerified},
		},
		eligible: map[string]*eligible{
			addr.String(): {residuable{
				provider: p,
				schema:   nestingSetBlockSchema(),
				typeName: "aws_autoscaling_group",
				applied: cty.ObjectVal(map[string]cty.Value{
					"id":    cty.StringVal("asg-x"),
					"tags":  cty.MapValEmpty(cty.String),
					"hooks": appliedHooks,
				}),
				identity: cty.NilVal,
			}},
		},
		recordStore: store,
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	if len(rep.Outcomes) != 1 || rep.Outcomes[0].Outcome != OutcomeStamped {
		t.Fatalf("Outcomes = %+v, want one OutcomeStamped", rep.Outcomes)
	}

	attrs, _, _, exists, err := store.GetResidue(context.Background(), addr)
	if err != nil {
		t.Fatalf("store.Get: %s", err)
	}
	if !exists {
		t.Fatalf("no residue was recorded for %s; the FIRST live-plan after this migrate would see an empty hooks set and propose 'must be replaced', exactly issue #385's bug", addr)
	}
	got, ok := attrs["hooks"]
	if !ok {
		t.Fatalf("residue attrs = %v, want hooks present", attrs)
	}
	if !got.RawEquals(appliedHooks) {
		t.Errorf("residue hooks = %#v, want %#v", got, appliedHooks)
	}
	if _, ok := attrs["tags"]; ok {
		t.Errorf("tags was recorded as residue; the provider always answers it fresh in this fixture, so classifyResidue must not have judged it residue-shaped")
	}
}

// TestApprove_SecondRunIsIdempotent is issue #327's other real-world
// requirement: live-import -approve is documented as idempotent over a
// second run against the same state (OutcomeAlreadyStamped). Approve's new
// residue write has to survive that too - a naive Put asserting
// expectedVersion="" would fail every run after the first.
func TestApprove_SecondRunIsIdempotent(t *testing.T) {
	addr := mustAddr(t, "aws_nat_gateway.this")
	store := residueStore(t)

	buildRat := func() *Ratification {
		p := preserveFromPriorProvider()
		return &Ratification{
			Estate:  "residue-test-estate",
			Entries: []Entry{{Addr: addr, TypeName: "aws_nat_gateway", Status: StatusVerified}},
			eligible: map[string]*eligible{
				addr.String(): {residuable{
					provider: p,
					schema:   residueSchema(),
					typeName: "aws_nat_gateway",
					applied: cty.ObjectVal(map[string]cty.Value{
						"id":            cty.StringVal("ngw-x"),
						"tags":          cty.MapValEmpty(cty.String),
						"subnet_id":     cty.StringVal("subnet-real"),
						"computed_only": cty.StringVal("fresh-from-remote"),
					}),
					identity: cty.NilVal,
				}},
			},
			recordStore: store,
		}
	}

	ctx := context.Background()
	if _, diags := buildRat().Approve(ctx); diags.HasErrors() {
		t.Fatalf("first Approve returned errors: %s", diags.Err())
	}
	_, v1, _, exists, err := store.GetResidue(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("first Approve did not record residue: exists=%v err=%v", exists, err)
	}

	if _, diags := buildRat().Approve(ctx); diags.HasErrors() {
		t.Fatalf("second Approve returned errors: %s", diags.Err())
	}
	_, v2, _, exists, err := store.GetResidue(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("second Approve did not record residue: exists=%v err=%v", exists, err)
	}
	if v1 == "" || v2 == "" {
		t.Fatalf("expected non-empty versions from both runs, got %q and %q", v1, v2)
	}
}

// TestApprove_NilResidueStoreIsANoOp confirms the same "a run with no
// record_store declared pays nothing and changes nothing" contract every
// other GitHub issue #275 consumer already holds to.
func TestApprove_NilResidueStoreIsANoOp(t *testing.T) {
	addr := mustAddr(t, "aws_nat_gateway.this")
	p := preserveFromPriorProvider()

	rat := &Ratification{
		Estate:  "residue-test-estate",
		Entries: []Entry{{Addr: addr, TypeName: "aws_nat_gateway", Status: StatusVerified}},
		eligible: map[string]*eligible{
			addr.String(): {residuable{
				provider: p,
				schema:   residueSchema(),
				typeName: "aws_nat_gateway",
				applied: cty.ObjectVal(map[string]cty.Value{
					"id":            cty.StringVal("ngw-x"),
					"tags":          cty.MapValEmpty(cty.String),
					"subnet_id":     cty.StringVal("subnet-real"),
					"computed_only": cty.StringVal("fresh-from-remote"),
				}),
				identity: cty.NilVal,
			}},
		},
		// residueStore left nil, matching a configuration with no
		// record_store block.
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	if len(rep.Outcomes) != 1 || rep.Outcomes[0].Outcome != OutcomeStamped {
		t.Fatalf("Outcomes = %+v, want one OutcomeStamped", rep.Outcomes)
	}
}
