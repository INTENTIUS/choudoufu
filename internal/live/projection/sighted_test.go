// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
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

// GitHub issue #596. A per-instance import that comes back "no such object"
// is the same provider answer to two different questions - the object does
// not exist, or the provider cannot import it at the identity it was asked
// with - and build.go used to read it as the first one unconditionally, so a
// plan proposed CREATING a resource a live, tagged object already held.
//
// The two fixtures below are the two situations the issue says have to be
// told apart, and they differ in exactly one field: whether the resolution
// carries the provider's own identity object. See
// [builder.refuseListedButAbsent] for why that field, and nothing else, is
// the discriminator.

const sightedEstate = "sighted-unit"

const sightedType = "aws_cloudwatch_log_group"

// sightedProvider is a provider that lists nothing and imports nothing: the
// wire shape of a provider whose ImportResourceState cannot find an object
// at the identity it is handed, whatever the reason. It serves an identity
// schema for the type, because a resolution can only ever carry an identity
// object for a type the provider serves one for - listclient reads a list
// result's identity through the type's identity schema, and leaves it null
// when there is none.
type sightedProvider struct {
	*tofu.MockProvider

	// imports records every identity this provider was asked about, so a
	// test can report what the run actually did rather than assert on a
	// bare boolean.
	imports []string
}

func newSightedProvider(t *testing.T) *sightedProvider {
	t.Helper()

	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Optional: true, Computed: true},
		"name": {Type: cty.String, Optional: true, Computed: true},
		"arn":  {Type: cty.String, Optional: true, Computed: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}}
	idSchema := &configschema.Object{
		Nesting: configschema.NestingSingle,
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Required: true},
		},
	}

	p := &sightedProvider{}
	p.MockProvider = &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				sightedType: {Block: block, IdentitySchema: idSchema},
				// The fixture directory declares a second, untaggable
				// resource; it needs a schema or the projection refuses the
				// whole build for an unsupported type.
				"aws_s3_bucket_policy": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"id":     {Type: cty.String, Optional: true, Computed: true},
					"bucket": {Type: cty.String, Optional: true, Computed: true},
					"policy": {Type: cty.String, Optional: true, Computed: true},
				}}},
			},
		},
	}
	p.ConfigureProviderCalled = true

	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		asked := r.Target.ID
		if r.Target.IsIdentityBased() {
			asked = "identity:" + r.Target.Identity.GoString()
		}
		p.imports = append(p.imports, r.TypeName+" "+asked)
		// No ImportedResources and no diagnostics: the "there is no such
		// object" answer several AWS types give, and the exact shape
		// importAndRead folds into statusAbsent.
		return providers.ImportResourceStateResponse{}
	}

	return p
}

func (p *sightedProvider) providers() Providers {
	return SingleProvider(awsProvider, p.MockProvider)
}

// sightedResolution is the declared instance both fixtures build, with the
// one field that separates them left to the caller.
func sightedResolution(t *testing.T, id cty.Value) identity.Resolution {
	t.Helper()
	return identity.Resolution{
		Addr:           mustAddr(t, sightedType+".app"),
		Class:          identity.ClassConcrete,
		ImportID:       "/ours/logs",
		IdentityValues: map[string]string{"id": "/ours/logs"},
		Identity:       id,
	}
}

func buildSighted(t *testing.T, id cty.Value) (*Result, string, *sightedProvider) {
	t.Helper()
	cfg := loadConfig(t, "testdata/named")
	p := newSightedProvider(t)
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		sightedResolution(t, id),
	}, p.providers(), Options{Ownership: &Ownership{Estate: sightedEstate}})
	return res, renderDiags(diags), p
}

// renderedOmissions is the projection's own operator-facing rendering of
// what it left out, one line per instance. Asserting on it rather than on a
// Reason value is deliberate: the sentence "The plan will propose creating
// it" is the thing a user acts on, and it is the thing this issue is about.
func renderedOmissions(res *Result) string {
	var b strings.Builder
	for _, om := range res.Omitted {
		b.WriteString(om.String())
		b.WriteString("\n")
	}
	return b.String()
}

// TestListedButAbsent_liveSightingRefusesTheDuplicate is case one: the
// provider's own list call returned this object, alive, carrying this
// estate's marker - which is what a non-null Resolution.Identity means and
// the only thing it can mean (internal/live/discovery sets a claimant
// identity at exactly one site, over listclient results; every other source
// spells cty.NilVal). The import then says nothing is there. Proposing a
// create on the strength of the second answer duplicates the object named by
// the first.
func TestListedButAbsent_liveSightingRefusesTheDuplicate(t *testing.T) {
	res, rendered, p := buildSighted(t, cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("/ours/logs"),
	}))

	if len(p.imports) != 1 {
		t.Fatalf("expected exactly one import attempt, got %d: %v", len(p.imports), p.imports)
	}

	omissions := renderedOmissions(res)
	if strings.Contains(omissions, "The plan will propose creating it") {
		t.Errorf("the plan still proposes creating a duplicate of a live, listed object:\n%s", omissions)
	}
	if !strings.Contains(omissions, string(ReasonListedNotImportable)) {
		t.Errorf("the omission is not classified as %s:\n%s", ReasonListedNotImportable, omissions)
	}

	// The run must stop. An omission alone would leave the instance out of
	// prior state, which is precisely how stock's own graph then plans a
	// create for it.
	if !strings.Contains(rendered, SummaryListedNotImportable) {
		t.Fatalf("no %q diagnostic was produced; the plan would continue and create a duplicate:\n%s", SummaryListedNotImportable, rendered)
	}
	for _, want := range []string{
		"[Error] " + SummaryListedNotImportable,
		"The provider's own list call returned a live " + sightedType,
		"tofu-estate",
		"tofu-address",
		sightedType + ".app",
		`"/ours/logs"`,
		"refuses rather than propose creating a second",
		"not a tag-index lookup",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, rendered)
		}
	}
}

// TestListedButAbsent_tagIndexSightingStillProposesTheRebuild is case two,
// and it is the reason this guard is keyed on the identity object rather
// than on "the estate's sweep listed it".
//
// A resolution the tagging sweep produced carries an import ID and no
// identity object at all - internal/live/discovery's fileTaggingCandidate
// writes cty.NilVal into the claimant, unconditionally. That matters because
// the Resource Groups Tagging API keeps deleted objects queryable
// indefinitely (#578's teardown had to describe all eight ECS clusters
// individually rather than trust the tag sweep), so a tag-index sighting is
// not evidence the object exists. An estate rebuilding a resource it really
// did destroy is in exactly this shape, and it must still get its create.
func TestListedButAbsent_tagIndexSightingStillProposesTheRebuild(t *testing.T) {
	res, rendered, p := buildSighted(t, cty.NilVal)

	if len(p.imports) != 1 {
		t.Fatalf("expected exactly one import attempt, got %d: %v", len(p.imports), p.imports)
	}
	if strings.Contains(rendered, SummaryListedNotImportable) {
		t.Fatalf("a legitimate rebuild was refused on tag-index evidence:\n%s", rendered)
	}
	if strings.Contains(rendered, "[Error]") {
		t.Fatalf("the rebuild produced an error diagnostic:\n%s", rendered)
	}

	omissions := renderedOmissions(res)
	if !strings.Contains(omissions, "The plan will propose creating it") {
		t.Errorf("the rebuild is no longer proposed:\n%s", omissions)
	}
	if !strings.Contains(omissions, string(ReasonAbsent)) {
		t.Errorf("the omission is not classified as %s:\n%s", ReasonAbsent, omissions)
	}
}

// TestListedButAbsent_undeclaredAbsenceIsNotRefused: an instance the estate
// owns and the configuration no longer declares reaches the same branch, and
// "absent" there means the destroy this run would have proposed has already
// happened. Refusing that would fail a plan over good news, and there is no
// duplicate to create because nothing declares the address.
func TestListedButAbsent_undeclaredAbsenceIsNotRefused(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")
	p := newSightedProvider(t)

	r := sightedResolution(t, cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("/gone/logs")}))
	r.Addr = mustAddr(t, sightedType+".removed")
	r.ImportID = "/gone/logs"
	r.IdentityValues = map[string]string{"id": "/gone/logs"}
	r.Undeclared = true

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{r},
		p.providers(), Options{
			Ownership:          &Ownership{Estate: sightedEstate},
			UndeclaredProvider: awsProvider,
		})

	rendered := renderDiags(diags)
	if strings.Contains(rendered, SummaryListedNotImportable) {
		t.Fatalf("an already-destroyed orphan was refused:\n%s", rendered)
	}
	omissions := renderedOmissions(res)
	if !strings.Contains(omissions, string(ReasonAbsent)) {
		t.Errorf("the orphan's absence is not the ordinary ABSENT answer:\n%s", omissions)
	}
}

// TestListedButAbsent_noEstateIsNotRefused: a caller with no estate concept
// at all - internal/live/mv reading one resource, or ReadInstances' narrow
// value read - passes no Ownership. There is no "this estate's marker" for a
// listing to have carried, so there is nothing to contradict and nothing to
// refuse.
func TestListedButAbsent_noEstateIsNotRefused(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")
	p := newSightedProvider(t)

	_, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		sightedResolution(t, cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("/ours/logs")})),
	}, p.providers(), Options{})

	if rendered := renderDiags(diags); strings.Contains(rendered, SummaryListedNotImportable) {
		t.Fatalf("a run with no estate refused an absence it has no marker evidence about:\n%s", rendered)
	}
}

// TestListedButAbsent_costsNoProviderCall is the failure-path cost,
// measured rather than estimated. The issue asks for any existence check to
// be paid only when an import actually fails, and to be proportional to how
// often that happens rather than to estate size. This one is cheaper than
// that: the evidence is a field on the resolution the builder already holds,
// so the refusing path makes exactly the same provider calls the ABSENT path
// it replaces made - one ImportResourceState, no ReadResource (there is no
// object to read), and nothing else.
//
// Recorded as counts rather than as a claim so that a future change which
// starts paying for this shows up here as a number that moved.
func TestListedButAbsent_costsNoProviderCall(t *testing.T) {
	sighted := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("/ours/logs")})

	for _, tc := range []struct {
		name string
		id   cty.Value
	}{
		{"refused (live sighting)", sighted},
		{"ordinary absence (no sighting)", cty.NilVal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, p := buildSighted(t, tc.id)
			if got := len(p.imports); got != 1 {
				t.Errorf("ImportResourceState calls = %d, want 1: %v", got, p.imports)
			}
			if p.ReadResourceCalled {
				t.Errorf("ReadResource was called for an absent object")
			}
			if p.PlanResourceChangeCalled || p.ApplyResourceChangeCalled || p.ReadDataSourceCalled {
				t.Errorf("the guard reached a provider call the ABSENT path did not: plan=%v apply=%v data=%v",
					p.PlanResourceChangeCalled, p.ApplyResourceChangeCalled, p.ReadDataSourceCalled)
			}
		})
	}
}

// TestListedButAbsentRefusalIsRegistered keeps this refusal reachable
// through check.AllRefusals(): a refusal nobody can look up is one an
// operator meets with no documentation behind it.
func TestListedButAbsentRefusalIsRegistered(t *testing.T) {
	r, ok := LookupRefusal(SummaryListedNotImportable)
	if !ok {
		t.Fatalf("%q is not in this package's refusal registry", SummaryListedNotImportable)
	}
	if !strings.Contains(r.What, "#596") {
		t.Errorf("the registry entry does not name the issue it closes: %q", r.What)
	}
}
