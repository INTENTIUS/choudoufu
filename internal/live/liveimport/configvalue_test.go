// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"errors"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/markers/markerstest"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #373, and what measuring the fix against real estates turned
// up on the way.
//
// A stamp has no configuration - that is what a migration is - so it invents
// one and the provider reads it back (SDKv2's GetRawConfig, the framework's
// Config) as if an operator had written it. What that invented configuration
// should say about an optional+computed attribute has two answers, and both
// of them are refused by a real provider on a real estate:
//
//   - carry the read-back value across, as this path did before #373, and
//     terraform-provider-aws's NAT gateway CustomizeDiff reads
//     secondary_private_ip_address_count as explicitly set and answers
//     "not supported with connectivity_type = \"public\"" - on
//     corpus-vpc-complete, whose HCL has never mentioned the argument and
//     which stock plans and applies without complaint;
//   - leave it null, the way real HCL does, and hashicorp/aws 6's injected
//     per-resource `region` goes unknown in a plugin-framework plan, whose
//     force-new-if-region-changes check reads unknown-against-known as a
//     change - measured on corpus-overture-tiles' aws_batch_job_queue, which
//     came back "would require replacing it (.region)".
//
// No property of the schema separates those two: both are optional+computed
// scalars, and the difference is entirely in what the provider does with the
// config. So [syntheticConfigs] offers both, least claim first, and
// approveOne applies the first plan that is a clean tags-only change. The
// guards that decide "clean" are the ones that already stood between a plan
// and an apply, so a second attempt widens what CAN be written without
// widening what MAY be.
//
// Nothing in these tests names a resource type. The two providers below are
// stand-ins for the shape of each check; the schema is
// [markerstest.TagsOnlyConfigBlock], shared with the two other packages that
// build the same synthetic configuration.

// gatedSchema is [markerstest.TagsOnlyConfigBlock] as this package's callers
// take it.
func gatedSchema() providers.Schema {
	return providers.Schema{Block: markerstest.TagsOnlyConfigBlock()}
}

// gatedObject is [markerstest.TagsOnlyConfigObject] with its tag map
// replaced, so a test can say what the resource already carries.
func gatedObject(tags cty.Value) cty.Value {
	vals := markerstest.TagsOnlyConfigObject().AsValueMap()
	vals["tags"] = tags
	return cty.ObjectVal(vals)
}

// eligibleFor wires an *eligible for one of the providers below.
func eligibleFor(provider providers.Interface, tags map[string]string) *eligible {
	vals := make(map[string]cty.Value, len(tags))
	for k, v := range tags {
		vals[k] = cty.StringVal(v)
	}
	tagsVal := cty.MapValEmpty(cty.String)
	if len(vals) > 0 {
		tagsVal = cty.MapVal(vals)
	}
	return &eligible{residuable{
		provider: provider,
		schema:   gatedSchema(),
		typeName: "test_gated_gateway",
		applied:  gatedObject(tagsVal),
		identity: cty.NilVal,
	}}
}

// TestConfigValueLeavesOptionalComputedUnset is the mechanism, both claims.
// The claim that asserts most is asserted too, so this fails loudly if the
// two are ever collapsed into one: a test that only pins the new answer
// cannot say whether it differs from the old.
func TestConfigValueLeavesOptionalComputedUnset(t *testing.T) {
	block := gatedSchema().Block
	live := markerstest.TagsOnlyConfigObject()

	most := configValue(block, live, claimEverythingSettable)
	for _, name := range markerstest.TagsOnlyConfigOptionalComputed() {
		if most.GetAttr(name).IsNull() {
			t.Fatalf("premise broken: claimEverythingSettable already nulls %s, so the two claims do not differ and there was nothing to fix", name)
		}
	}

	least := configValue(block, live, claimTagsOnly)
	for _, name := range markerstest.TagsOnlyConfigNulled() {
		if got := least.GetAttr(name); !got.IsNull() {
			t.Errorf("%s = %#v under claimTagsOnly, want null: the provider may compute it, so a configuration that only sets tags says nothing about it", name, got)
		}
	}
	for _, name := range markerstest.TagsOnlyConfigCarried() {
		for claimName, cfg := range map[string]cty.Value{"claimTagsOnly": least, "claimEverythingSettable": most} {
			if got, want := cfg.GetAttr(name), live.GetAttr(name); !got.RawEquals(want) {
				t.Errorf("%s: %s = %#v, want %#v carried across: only a configuration can supply this argument, so nulling it would propose removing it", claimName, name, got, want)
			}
		}
	}
}

// TestConfigValueKeepsTheTagMapWhenTagsIsOptionalComputed is the exemption's
// own test. [markers.TagSurface] admits an optional+computed tags map - it
// refuses only a computed-ONLY one - so without [assertedTagAttr] the
// claimTagsOnly rule would null the very argument the write exists to set,
// ProposedNew would answer the prior tags, and every stamp on such a type
// would plan as a no-op and report success. No hashicorp/aws 6.59.0 type
// declares tags that way today, which is exactly why this needs a test rather
// than a reader noticing.
func TestConfigValueKeepsTheTagMapWhenTagsIsOptionalComputed(t *testing.T) {
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Computed: true},
		"tags": {Type: cty.Map(cty.String), Optional: true, Computed: true},
	}}
	want := cty.MapVal(map[string]cty.Value{discovery.TagEstate: cty.StringVal("acme")})
	live := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("x"), "tags": want})

	if got := configValue(block, live, claimTagsOnly).GetAttr("tags"); !got.RawEquals(want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
}

// TestConfigValueDoesNotMoveTheProposedPlan is the safety half, and the one
// that answers "did this change what gets written". A claim is only allowed
// to alter what the provider reads back as configuration, never what the run
// proposes to do. [objchange.ProposedNew] answers the prior value for a
// Computed attribute whose config is null, so the proposed object comes out
// identical under both claims - and the assertion is that equality, not a
// restatement of an object a reader would have to check by eye.
func TestConfigValueDoesNotMoveTheProposedPlan(t *testing.T) {
	block := gatedSchema().Block
	prior := markerstest.TagsOnlyConfigObject()

	wantTags := map[string]string{
		"Name":               "gw",
		discovery.TagEstate:  "acme",
		discovery.TagAddress: "aws_nat_gateway.this",
	}
	desired, err := withTags(block, prior, wantTags)
	if err != nil {
		t.Fatalf("withTags: %s", err)
	}

	mostProposed := objchange.ProposedNew(block, prior, configValue(block, desired, claimEverythingSettable))
	leastProposed := objchange.ProposedNew(block, prior, configValue(block, desired, claimTagsOnly))

	if !leastProposed.RawEquals(mostProposed) {
		t.Fatalf("the proposed new object moved between the two claims:\nleast: %#v\n most: %#v\na claim must change what the provider reads back and nothing else", leastProposed, mostProposed)
	}
	// And say what it is, so a future reader does not have to trust that two
	// equal answers are the right one.
	for name := range block.Attributes {
		if name == "tags" {
			continue
		}
		if got, want := leastProposed.GetAttr(name), prior.GetAttr(name); !got.RawEquals(want) {
			t.Errorf("proposed %s = %#v, want the prior value %#v", name, got, want)
		}
	}
	gotTags := leastProposed.GetAttr("tags").AsValueMap()
	if len(gotTags) != len(wantTags) {
		t.Fatalf("proposed tags = %v, want exactly %v", gotTags, wantTags)
	}
	for k, want := range wantTags {
		if got, ok := gotTags[k]; !ok || got.AsString() != want {
			t.Errorf("proposed tag %q = %v, want %q", k, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// End to end, through providers that judge the configuration
// ---------------------------------------------------------------------------

// recordingProvider is the shared half of the two providers below: it accepts
// the proposed state and records the tag set an apply would have landed, so a
// test can assert on the write itself rather than on the plan.
type recordingProvider struct {
	*tofu.MockProvider
	planCount   int
	applyCount  int
	appliedTags map[string]string
	refusals    []string
}

// newRecordingProvider builds one whose plans are refused exactly when refuse
// says so, given the raw configuration the write offered.
func newRecordingProvider(refuse func(config cty.Value) string) *recordingProvider {
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	g := &recordingProvider{MockProvider: p}

	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		g.planCount++
		if why := refuse(r.Config); why != "" {
			g.refusals = append(g.refusals, why)
			var resp providers.PlanResourceChangeResponse
			resp.Diagnostics = resp.Diagnostics.Append(errors.New(why))
			return resp
		}
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		g.applyCount++
		tags := map[string]string{}
		if tv := r.PlannedState.GetAttr("tags"); !tv.IsNull() {
			for it := tv.ElementIterator(); it.Next(); {
				k, v := it.Element()
				tags[k.AsString()] = v.AsString()
			}
		}
		g.appliedTags = tags
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return g
}

// refuseWhenSet is resourceNATGatewayCustomizeDiff's shape, reduced to the one
// clause issue #373 turns on: an optional+computed argument found known and
// non-null in the raw config is read as explicitly set, and refused,
// regardless of its value.
func refuseWhenSet(config cty.Value) string {
	conn := config.GetAttr("connectivity_type")
	count := config.GetAttr("secondary_count")
	if !count.IsNull() && count.IsKnown() && !conn.IsNull() && conn.AsString() == "public" {
		return `secondary_count is not supported with connectivity_type = "public"`
	}
	return ""
}

// refuseWhenUnset is the opposite shape, and the one that made the naive
// version of this fix regress corpus-overture-tiles: a provider that INJECTS
// an optional+computed argument reads its absence from the config as a
// change. Stated as a refusal rather than as a replacement because both are
// judged the same way here - see [notATagsOnlyPlan].
func refuseWhenUnset(config cty.Value) string {
	if config.GetAttr("connectivity_type").IsNull() {
		return "the configuration does not say which connectivity_type this is; the provider would have to invent one, and that is a replacement"
	}
	return ""
}

// TestApproveOne_ConfigGateDoesNotRefuseATagsOnlyWrite is issue #373 itself,
// end to end: a fully-populated live read of a resource whose provider gates
// on an optional+computed argument being set, stamped. The gate is untouched;
// what changed is that the first configuration offered no longer claims the
// operator set the argument.
func TestApproveOne_ConfigGateDoesNotRefuseATagsOnlyWrite(t *testing.T) {
	g := newRecordingProvider(refuseWhenSet)
	e := eligibleFor(g, map[string]string{"Name": "gw"})
	addr := mustAddr(t, "test_gated_gateway.this")

	out := approveOne(context.Background(), "acme", addr, e)

	if out.Outcome != OutcomeStamped {
		t.Fatalf("Outcome = %s, want STAMPED (detail: %s)", out.Outcome, out.Detail)
	}
	if len(g.refusals) != 0 {
		t.Fatalf("the provider's config gate fired during a tags-only write: %v", g.refusals)
	}
	if g.planCount != 1 {
		t.Errorf("PlanResourceChange was called %d times, want 1: the first configuration was accepted, so there is nothing to fall back to", g.planCount)
	}
	assertMarkersLanded(t, g, addr)
}

// TestApproveOne_ConfigGateFiresOnTheClaimThatAssertsMost is the other half of
// the reproduction. The same provider is offered only the configuration that
// asserts everything the object holds - the one this path sent before #373 -
// and it refuses, with the diagnostic corpus-vpc-complete's migrate stage
// printed for real. Without this, the test above only proves the gate is
// satisfiable, not that it was ever tripped.
func TestApproveOne_ConfigGateFiresOnTheClaimThatAssertsMost(t *testing.T) {
	block := gatedSchema().Block
	prior := markerstest.TagsOnlyConfigObject()
	desired, err := withTags(block, prior, map[string]string{"Name": "gw", discovery.TagEstate: "acme"})
	if err != nil {
		t.Fatalf("withTags: %s", err)
	}

	if why := refuseWhenSet(configValue(block, desired, claimEverythingSettable)); why == "" {
		t.Fatal("the pre-#373 configuration passed the gate, so this file no longer reproduces issue #373; fix the fixture before trusting the test above")
	}
	if why := refuseWhenSet(configValue(block, desired, claimTagsOnly)); why != "" {
		t.Fatalf("the tags-only configuration is still refused (%s), so the fix does not reach the case it was written for", why)
	}
}

// TestApproveOne_FallsBackWhenTheTagsOnlyClaimIsRefused is the regression the
// naive fix caused, and the reason the write offers two configurations rather
// than swapping one for the other. A provider that reads an omitted
// optional+computed argument as a change refuses the first candidate; the
// second is the configuration this path always sent, and the stamp lands
// exactly as it did before #373.
func TestApproveOne_FallsBackWhenTheTagsOnlyClaimIsRefused(t *testing.T) {
	g := newRecordingProvider(refuseWhenUnset)
	e := eligibleFor(g, map[string]string{"Name": "gw"})
	addr := mustAddr(t, "test_gated_gateway.this")

	out := approveOne(context.Background(), "acme", addr, e)

	if out.Outcome != OutcomeStamped {
		t.Fatalf("Outcome = %s, want STAMPED (detail: %s)", out.Outcome, out.Detail)
	}
	if g.planCount != 2 {
		t.Errorf("PlanResourceChange was called %d times, want 2: the first candidate was refused, so the second had to be offered", g.planCount)
	}
	if len(g.refusals) != 1 {
		t.Errorf("the provider refused %d times, want 1: only the tags-only claim should have been refused", len(g.refusals))
	}
	assertMarkersLanded(t, g, addr)
}

// TestApproveOne_RefusesWhenNoClaimProducesACleanPlan is the floor: a
// provider that refuses both configurations still gets nothing written, and
// the operator reads the refusal for the configuration that asserts most -
// the only one this path used to send at all.
func TestApproveOne_RefusesWhenNoClaimProducesACleanPlan(t *testing.T) {
	g := newRecordingProvider(func(cty.Value) string { return "no, for reasons of its own" })
	e := eligibleFor(g, map[string]string{"Name": "gw"})

	out := approveOne(context.Background(), "acme", mustAddr(t, "test_gated_gateway.this"), e)

	if out.Outcome != OutcomeFailed {
		t.Fatalf("Outcome = %s, want FAILED", out.Outcome)
	}
	if g.applyCount != 0 {
		t.Fatalf("ApplyResourceChange was called %d times after both plans were refused, want 0", g.applyCount)
	}
	if g.planCount != 2 {
		t.Errorf("PlanResourceChange was called %d times, want 2", g.planCount)
	}
}

// assertMarkersLanded is the point of the whole path, and the half of issue
// #373 that would be worth nothing if only the refusal moved: the markers
// were written, by value, and the tag the operator already had is still there
// and unmodified.
func assertMarkersLanded(t *testing.T, g *recordingProvider, addr interface{ String() string }) {
	t.Helper()
	if g.applyCount != 1 {
		t.Fatalf("ApplyResourceChange was called %d times, want 1", g.applyCount)
	}
	want := map[string]string{
		"Name":               "gw",
		discovery.TagEstate:  "acme",
		discovery.TagAddress: discovery.EscapeAddress(addr.String()),
	}
	if len(g.appliedTags) != len(want) {
		t.Fatalf("applied tags = %v, want exactly %v", g.appliedTags, want)
	}
	for k, v := range want {
		if got := g.appliedTags[k]; got != v {
			t.Errorf("applied tag %q = %q, want %q", k, got, v)
		}
	}
}
