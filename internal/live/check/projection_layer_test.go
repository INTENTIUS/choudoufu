// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is GitHub issue #262. [Analyze] computes two of projection's
// twenty-seven refusals offline, and until this file existed no input made it
// raise either one - so the [LayerProjection] case in catalog's lookup, the
// finding's registry resolution and its site rendering were exercised by
// nothing at all. The wiring was green because nothing reached it.
//
// That is worse than an ordinary coverage gap here. A layer with no lookup
// case reports every one of its findings as Registered=false, which reddens
// TestCorpusArtifactHasNoUnregisteredRefusals on a corpus run rather than in
// this package - a long way from whoever removed the case.
//
// So these assert on the rendered finding: its layer, its ID, Registered, and
// the site's own fields. Not on Blocked(). A predicate has been green while
// the value underneath it was wrong six times in this repository.

// pairSchema is one synthetic provider type whose identity the resolver can
// derive without a table row.
//
// The shape is the one issue #262 measured in the corpus: two client-named
// identity attributes, which [identity.SynthesizeTypeIdentity] admits and
// marks IdentityObjectOnly because no schema carries the separator that would
// join them into a legacy import-ID string. Such a resolution is CONCRETE
// with an EMPTY ImportID on purpose - 12 of the 1287 concrete resolutions
// that reached projection's importTarget over 250 corpus entries arrive that
// way, and all twelve then built an identity object successfully. The
// identity object is the only import form there is for them, so a schema that
// will not accept one leaves nothing to import by.
//
// identityAttrs is what makes each variant differ, and it is the ONLY thing
// that differs: same fixture directory, same configuration schema, same
// resolution. Whatever moves between the subtests below is caused by the
// identity schema and by nothing else.
func pairSchema(identityAttrs map[string]*configschema.Attribute) map[string]providers.Schema {
	return map[string]providers.Schema{
		"examplecloud_pair": {
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"cluster": {Type: cty.String, Required: true},
					"service": {Type: cty.String, Required: true},
					"tags":    {Type: cty.Map(cty.String), Optional: true},
					"id":      {Type: cty.String, Computed: true},
				},
			},
			IdentitySchema: &configschema.Object{
				Nesting:    configschema.NestingSingle,
				Attributes: identityAttrs,
			},
		},
	}
}

// buildableIdentity is the control: two plain required string attributes,
// exactly the ones the configuration sets. identityFromValues clears both of
// its bars and the projection layer says nothing.
func buildableIdentity() map[string]*configschema.Attribute {
	return map[string]*configschema.Attribute{
		"cluster": {Type: cty.String, Required: true},
		"service": {Type: cty.String, Required: true},
	}
}

const emptyImportFixture = "projection-empty-import"

// TestAnalyzeRaisesEmptyImportIdentity is the positive case the issue asks
// for: a configuration on which [Analyze] actually produces a
// [LayerProjection] finding, end to end from the .tf file.
//
// Two variants, one per bar of projection's identityFromValues
// (internal/live/projection/build.go:442), because clearing one of them is
// not the same as clearing the other:
//
//   - structure: an identity attribute with a nested type. A string per
//     attribute is the only shape the identity table can express, so an
//     identity with structure in it is one the run cannot build rather than
//     one to approximate.
//   - type: an identity attribute whose declared type the configured value
//     does not convert to. The provider says this attribute is a number and
//     the configuration supplies "web".
//
// Both leave importTarget with no identity object, and IdentityObjectOnly
// already left it with no import-ID string, so nothing at all names the
// instance - which is refused rather than approximated.
func TestAnalyzeRaisesEmptyImportIdentity(t *testing.T) {
	variants := []struct {
		name    string
		attrs   map[string]*configschema.Attribute
		because string
	}{
		{
			name: "identity attribute has structure",
			attrs: map[string]*configschema.Attribute{
				"cluster": {Required: true, NestedType: &configschema.Object{
					Nesting: configschema.NestingSingle,
					Attributes: map[string]*configschema.Attribute{
						"tenant": {Type: cty.String, Required: true},
					},
				}},
				"service": {Type: cty.String, Required: true},
			},
			because: "cluster is a nested object in the identity schema",
		},
		{
			name: "identity attribute is not a string",
			attrs: map[string]*configschema.Attribute{
				"cluster": {Type: cty.String, Required: true},
				"service": {Type: cty.Number, Required: true},
			},
			because: `service is a number in the identity schema and the configuration sets it to "web"`,
		},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			report := Dir(t.Context(), filepath.Join("testdata", emptyImportFixture), Context{Schemas: pairSchema(v.attrs)})
			if !report.Readable() {
				t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
			}

			// The population first: this has to be the shape the issue
			// measured, or the finding below would be about something else.
			assertConcreteWithNoImportID(t, report)

			f := projectionFinding(t, report, RefusalEmptyImportIdentity)

			// Registered is the whole point. Deleting catalog.go's
			// LayerProjection case from lookup turns this line red, and
			// nothing else in this package would have noticed.
			if !f.Registered {
				t.Errorf("the finding is not registered, so it resolves in no table: catalog's lookup has no %s case", LayerProjection)
			}

			// Compared against internal/live/projection's own registry rather
			// than against a literal, so this is an assertion about two
			// packages agreeing and not about one of them agreeing with
			// itself.
			want, ok := projection.LookupRefusal(RefusalEmptyImportIdentity)
			if !ok {
				t.Fatalf("projection's registry does not carry %q, so PartiallyCheckedLayers names a refusal that no longer exists", RefusalEmptyImportIdentity)
			}
			if f.Title != want.Summary {
				t.Errorf("finding title is %q, projection's registry says %q", f.Title, want.Summary)
			}
			if f.What != want.What {
				t.Errorf("finding What is %q, projection's registry says %q", f.What, want.What)
			}
			if f.DocsRef != want.DocsRef() {
				t.Errorf("finding DocsRef is %q, projection's registry says %q", f.DocsRef, want.DocsRef())
			}
			if f.RaisedBy != RaisedByProjection {
				t.Errorf("finding RaisedBy is %q, want %q", f.RaisedBy, RaisedByProjection)
			}

			if len(f.Sites) != 1 {
				t.Fatalf("want one site for one refused instance, got %d: %v", len(f.Sites), f.Sites)
			}
			site := f.Sites[0]
			if !strings.Contains(site.Detail, "examplecloud_pair") {
				t.Errorf("the site detail does not name the type it is about (%s):\n%s", v.because, site.Detail)
			}
			if !strings.Contains(site.Detail, "no identity object and no import ID") {
				t.Errorf("the site detail does not say what was missing:\n%s", site.Detail)
			}

			// Recorded rather than asserted-away: projection raises both of
			// its offline diagnostics with tfdiags.Sourceless, so the site
			// carries no position and no address even though
			// EmptyImportIdentityDiagnostics has the resource block's
			// DeclRange in hand. A reader gets the type and the reason and
			// not the instance. This pins today's shape so that giving the
			// diagnostic a range is a visible change here rather than a
			// silent one.
			if site.File != "" || site.Line != 0 || site.Address != "" {
				t.Errorf("the site now carries a position (%s:%d %q); that is an improvement, so update this assertion and say so",
					site.File, site.Line, site.Address)
			}

			// The report has to advertise the stage it just ran, or the
			// caveat every corpus number carries is wrong about what was
			// looked at.
			assertProjectionPartlyChecked(t, report)
		})
	}
}

// TestAnalyzeIsCleanWhenTheIdentitySchemaCanBuildTheObject is the mutation
// check for the two variants above: the same fixture, the same resolution,
// the same empty ImportID - and a plain identity schema.
//
// Without it, those two prove only that something in the run refused, not
// that the identity schema is what refused it. This is the leg that says the
// obstacle was the stated obstacle.
func TestAnalyzeIsCleanWhenTheIdentitySchemaCanBuildTheObject(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("testdata", emptyImportFixture), Context{Schemas: pairSchema(buildableIdentity())})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	// The same population as the refusing variants - CONCRETE, no import ID.
	// The empty ImportID is not the defect and does not refuse on its own.
	assertConcreteWithNoImportID(t, report)

	for _, f := range report.Findings {
		if f.Layer == LayerProjection {
			t.Errorf("a buildable identity object still raised %s/%s: %v", f.Layer, f.ID, f.Sites)
		}
	}
	if report.Blocked() {
		t.Errorf("the control configuration is blocked, so the refusing variants prove nothing about the identity schema: %v", findingIDs(report))
	}
}

// TestAnalyzeCannotReachCyclicParentDerivedIdentities is the other half of
// issue #262, and its answer is that this refusal is not reachable through
// [Analyze] - stated with the reason rather than left looking like a
// configuration refusal nobody got round to testing.
//
// The fixture is the configuration the refusal describes: two resources whose
// identities are each built from the other's. Identity resolution refuses it
// first, with "Circular identity reference", and neither instance ends up in
// [Report.Identities] at all - so the resolutions [Analyze] hands
// [projection.CyclicIdentityDiagnostics] contain no cycle to find.
//
// That is not an accident of this fixture. Every Part carrying a ParentRef is
// built by identity's parentPart, which returns nothing unless r.instance
// already resolved that parent to completion, and identity's classify is the
// only place a Formula is constructed. So a resolution's Formula.Parents can
// only name instances that finished resolving before it did, which orders the
// graph by completion time and leaves orderWork's cyclic list empty for
// anything ResolveWith produced. Through [Analyze] the refusal is defence in
// depth against a bug in identity resolution - which is what its own detail
// text says it is.
func TestAnalyzeCannotReachCyclicParentDerivedIdentities(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("testdata", "projection-identity-cycle"), Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	// The configuration really is the cyclic one, and identity really is what
	// refuses it. Without this the test below would pass over a fixture that
	// had stopped being cyclic.
	if !hasFinding(report, LayerIdentity, "Circular identity reference") {
		t.Fatalf("the fixture no longer produces identity's cycle refusal, so it is no longer the shape this test is about: %v", findingIDs(report))
	}

	if len(report.Identities) != 0 {
		t.Errorf("identity returned %d resolutions for a cycle it refused; the argument below assumes none survive: %v", len(report.Identities), report.Identities)
	}
	if diags := projection.CyclicIdentityDiagnostics(report.Identities); len(diags) != 0 {
		t.Errorf("CyclicIdentityDiagnostics fired over Analyze's own resolutions: %s", diags.Err())
	}
	for _, f := range report.Findings {
		if f.Layer == LayerProjection {
			t.Errorf("unexpected projection finding on the cycle fixture: %s/%s %v", f.Layer, f.ID, f.Sites)
		}
	}
}

// assertConcreteWithNoImportID pins the population the "Empty import
// identity" refusal is decided over: one CONCRETE resolution whose ImportID
// is empty and whose identity values are the two the configuration set.
//
// Asserting the rendered values rather than the class alone is deliberate.
// The class is a predicate; the values are what a marker would be written
// from, and issue #251 is the case where every predicate in the system stayed
// identical while the rendered string moved.
func assertConcreteWithNoImportID(t *testing.T, report Report) {
	t.Helper()

	if len(report.Identities) != 1 {
		t.Fatalf("want one resolution from the fixture, got %d: %v", len(report.Identities), report.Identities)
	}
	res := report.Identities[0]
	if got := res.Addr.String(); got != "examplecloud_pair.one" {
		t.Fatalf("resolved %s, want examplecloud_pair.one", got)
	}
	if res.Class != identity.ClassConcrete {
		t.Fatalf("resolution is %v; EmptyImportIdentityDiagnostics only looks at concrete resolutions, so nothing below is being exercised", res.Class)
	}
	if res.ImportID != "" {
		t.Errorf("ImportID is %q; an IdentityObjectOnly type must carry none, and with one there is always something to import by", res.ImportID)
	}
	want := map[string]string{"cluster": "prod", "service": "web"}
	if len(res.IdentityValues) != len(want) {
		t.Fatalf("identity values are %v, want %v", res.IdentityValues, want)
	}
	for k, v := range want {
		if got := res.IdentityValues[k]; got != v {
			t.Errorf("identity value %q is %q, want %q", k, got, v)
		}
	}
}

// assertProjectionPartlyChecked checks the report says the projection stage
// was partly run and names this refusal as one of the parts.
func assertProjectionPartlyChecked(t *testing.T, report Report) {
	t.Helper()

	for _, p := range report.Partial {
		if p.Layer != LayerProjection {
			continue
		}
		for _, id := range p.Refusals {
			if id == RefusalEmptyImportIdentity {
				return
			}
		}
		t.Errorf("%s is reported as partly checked but does not name %q among the refusals it checked: %v", p.Layer, RefusalEmptyImportIdentity, p.Refusals)
		return
	}
	t.Errorf("the report raised a %s finding and does not report that layer as partly checked: %v", LayerProjection, report.Partial)
}

// projectionFinding returns the one finding under [LayerProjection] with this
// ID, failing if there is not exactly one.
func projectionFinding(t *testing.T, report Report, id string) Finding {
	t.Helper()

	var found []Finding
	for _, f := range report.Findings {
		if f.Layer == LayerProjection && f.ID == id {
			found = append(found, f)
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("no %s/%s finding; Analyze produced %v", LayerProjection, id, findingIDs(report))
	}
	t.Fatalf("%d findings under %s/%s, want one", len(found), LayerProjection, id)
	return Finding{}
}

func hasFinding(report Report, layer Layer, id string) bool {
	for _, f := range report.Findings {
		if f.Layer == layer && f.ID == id {
			return true
		}
	}
	return false
}
