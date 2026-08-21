// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file covers GitHub issue #337's second qualification route: a located
// record composed from the import-string grammar the provider's own Import
// section states, for the types whose wire identity schema says nothing.
//
// The subjects here are SHAPES, registered into DocumentedImportIDs under
// names no provider uses, because the rule reads a grammar and a schema and
// never a type name. The one test that does name a real type reads it out of
// the generated roster rather than writing it down.

// withDocumentedGrammar registers grammar under a name for the duration of
// one test and restores what was there.
//
// Mutating the generated map is deliberate and is the only way to test the
// rule rather than the corpus: a fixture built out of whatever aws types
// happen to be in the roster today would change meaning every time the
// provider's documentation does.
func withDocumentedGrammar(t *testing.T, name string, grammar DocumentedImportID) {
	t.Helper()
	prior, had := DocumentedImportIDs[name]
	DocumentedImportIDs[name] = grammar
	t.Cleanup(func() {
		if had {
			DocumentedImportIDs[name] = prior
			return
		}
		delete(DocumentedImportIDs, name)
	})
}

func docStringBlock(names ...string) *configschema.Block {
	b := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
	for _, n := range names {
		b.Attributes[n] = &configschema.Attribute{Type: cty.String, Computed: true}
	}
	return b
}

// TestResolveDocumentedImportIDCorroboratesEveryNameAgainstTheSchema is the
// rule, one shape per case.
//
// The generator reads a documentation page and can check nothing against a
// provider; this is where every name it read is either found in the schema or
// the type is refused. Each refusal case names the wrong record it prevents,
// because a refusal that prevents nothing is a rule nobody should be paying
// for.
func TestResolveDocumentedImportIDCorroboratesEveryNameAgainstTheSchema(t *testing.T) {
	part := func(name string, argument bool) DocumentedImportIDPart {
		return DocumentedImportIDPart{Name: name, Argument: argument}
	}

	cases := []struct {
		name    string
		grammar DocumentedImportID
		block   *configschema.Block
		want    []string
		wantSep string
		why     string
	}{
		{
			name:    "every segment names an attribute the block carries",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("apiid", true), part("routeid", false)}},
			block:   docStringBlock("id", "api_id", "route_id"),
			want:    []string{"api_id", "route_id"},
			wantSep: "/",
			why:     "the strongest shape: nothing is inferred, and `id` is not consulted at all",
		},
		{
			name:    "one segment resolves to nothing and is read as the minted id",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("restapiid", true), part("authorizerid", false)}},
			block:   docStringBlock("id", "rest_api_id"),
			want:    []string{"rest_api_id", "id"},
			wantSep: "/",
			why:     "the population's own shape: the provider sets `id` to the minted leaf and exposes it under no other name",
		},
		{
			name:    "the punctuation in a name is not part of it",
			grammar: DocumentedImportID{Separator: ":", Parts: []DocumentedImportIDPart{part("policystoreid", true), part("policytemplateid", false)}},
			block:   docStringBlock("id", "policy_store_id", "policy_template_id"),
			want:    []string{"policy_store_id", "policy_template_id"},
			wantSep: ":",
			why:     "the page writes REST-API-ID and the schema writes rest_api_id; the reduction is what makes them one name",
		},
		{
			name:    "two segments resolve to nothing",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("catalogid", false), part("tablename", false)}},
			block:   docStringBlock("id"),
			why: "there is no way to say which of the two the minted `id` fills. Filling either is a coin toss " +
				"with a wrong identity on one face, and a wrong identity is invisible until the next run's import.",
		},
		{
			name:    "the unresolved segment is one the page calls a configuration argument",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("domainname", true), part("keyid", false)}},
			block:   docStringBlock("id", "key_id"),
			why: "the page and the schema disagree about a name. Binding `id` into the argument's position would " +
				"put the minted leaf where the parent belongs - the right value in the wrong slot.",
		},
		{
			name:    "the block carries no minted id to fill the gap with",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("apiid", true), part("routeid", false)}},
			block:   docStringBlock("arn", "api_id"),
			why:     "nothing on the applied object could supply the missing segment",
		},
		{
			name:    "the page names `id` itself and still leaves a segment unresolved",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("id", false), part("version", false)}},
			block:   docStringBlock("id"),
			why: "`id` is already this composite's first segment, so there is nothing left over to infer the " +
				"second from. Reusing it would compose a string with one segment repeated and another missing.",
		},
		{
			name:    "a segment names an attribute that is not a string",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("apiid", true), part("routeid", false)}},
			block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id":       {Type: cty.String, Computed: true},
				"api_id":   {Type: cty.List(cty.String), Required: true},
				"route_id": {Type: cty.String, Computed: true},
			}},
			why: "a component with structure in it has no one string to put in a segment - and here it is also " +
				"an argument, so it is refused rather than filled with `id`",
		},
		{
			name:    "two attributes reduce to one name",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("apiid", true), part("routeid", false)}},
			block:   docStringBlock("id", "api_id", "apiid", "route_id"),
			why: "the reduction has lost the difference between api_id and apiid, so the name means neither. " +
				"Picking one would be a coin toss the page cannot break.",
		},
		{
			name:    "a grammar of one segment",
			grammar: DocumentedImportID{Separator: "/", Parts: []DocumentedImportIDPart{part("apiid", true)}},
			block:   docStringBlock("id", "api_id"),
			why:     "one segment is not a composite and the separator would join nothing",
		},
		{
			name:    "a grammar with no separator",
			grammar: DocumentedImportID{Parts: []DocumentedImportIDPart{part("apiid", true), part("routeid", false)}},
			block:   docStringBlock("id", "api_id", "route_id"),
			why:     "there is no character to join the segments with, so no string can be composed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const subject = "documented_grammar_subject"
			withDocumentedGrammar(t, subject, tc.grammar)

			parts, sep, ok := resolveDocumentedImportID(subject, tc.block)
			if ok != (tc.want != nil) {
				t.Fatalf("ok = %v, want %v.\n%s", ok, tc.want != nil, tc.why)
			}
			if !reflect.DeepEqual(parts, tc.want) {
				t.Errorf("parts = %v, want %v.\n%s", parts, tc.want, tc.why)
			}
			if sep != tc.wantSep {
				t.Errorf("separator = %q, want %q.\n%s", sep, tc.wantSep, tc.why)
			}
		})
	}

	t.Run("a type the roster does not describe", func(t *testing.T) {
		if _, _, ok := resolveDocumentedImportID("a_type_no_page_describes", docStringBlock("id", "api_id")); ok {
			t.Error("resolved a grammar for a type no scraped page names. The route would then be inventing " +
				"a composite for every type, which is the flattening issue #105 forbids.")
		}
	})
}

// TestDocumentedImportIDRouteOnlyReachesRefusedTypes is the containment
// property, and it is the reason this route was safe to add at all.
//
// The question an audit asks of any new rule is "what does this newly refuse
// that used to work". The answer here has to be nothing, and the reason is
// structural rather than careful: [LocatedIdentityPlanFor] consults the
// grammar only inside the branch [IDNotProvenWholeTypes] was already
// refusing. This asserts that structure from the outside - a type the
// refusal does not name must reach the identical verdict with the grammar
// present and with it absent.
func TestDocumentedImportIDRouteOnlyReachesRefusedTypes(t *testing.T) {
	proven := aMarkerlessTypeIn(t, false)
	block := docStringBlock("id", "api_id")
	schema := providers.Schema{Block: block}

	before, beforeOK := LocatedIdentityPlanFor(proven, schema)

	// A grammar registered for a type the refusal does not name. If the
	// route were consulted outside the refusal, this would change the plan.
	withDocumentedGrammar(t, proven, DocumentedImportID{
		Separator: "/",
		Parts:     []DocumentedImportIDPart{{Name: "apiid", Argument: true}, {Name: "leafid"}},
	})

	after, afterOK := LocatedIdentityPlanFor(proven, schema)
	if beforeOK != afterOK || !reflect.DeepEqual(before, after) {
		t.Errorf("plan for a type outside IDNotProvenWholeTypes moved when a documented grammar was added: "+
			"(%+v, %v) -> (%+v, %v).\nThe grammar route must be reachable only inside the refusal it replaces, "+
			"or it is changing what already works.", before, beforeOK, after, afterOK)
	}
}

// TestLocatedComposedImportIDIsAllOrNothing pins the composition by VALUE,
// which is the only assertion that can tell a right string from a plausible
// one.
//
// The mutation this is written against is a composition that skipped an
// unreadable segment: the string would come back short, the store would
// accept it, and the next run's import would be handed a different object's
// identity rather than none.
func TestLocatedComposedImportIDIsAllOrNothing(t *testing.T) {
	parts := []string{"rest_api_id", "id"}
	obj := func(parent, id cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"rest_api_id": parent,
			"id":          id,
			"name":        cty.StringVal("example"),
		})
	}

	got, ok := LocatedComposedImportID(obj(cty.StringVal("12345abcde"), cty.StringVal("67890fghij")), parts, "/")
	if !ok {
		t.Fatal("refused an object carrying every segment")
	}
	if want := "12345abcde/67890fghij"; got != want {
		t.Errorf("composed = %q, want %q - the documented import string, segment for segment", got, want)
	}

	refusals := []struct {
		name string
		obj  cty.Value
		sep  string
		why  string
	}{
		{"parent segment null", obj(cty.NullVal(cty.String), cty.StringVal("67890fghij")), "/", "a segment that is not there cannot be composed around"},
		{"parent segment unknown", obj(cty.UnknownVal(cty.String), cty.StringVal("67890fghij")), "/", "a value read from a plan rather than a finished apply"},
		{"parent segment empty", obj(cty.StringVal(""), cty.StringVal("67890fghij")), "/", "an empty segment composes a string with a hole in it that reads as a whole one"},
		{"parent segment sensitive", obj(cty.StringVal("12345abcde").Mark("secret"), cty.StringVal("67890fghij")), "/", "an identity derived from a sensitive value would be written to the store in clear"},
		{"leaf missing", cty.ObjectVal(map[string]cty.Value{"rest_api_id": cty.StringVal("12345abcde")}), "/", "same, from the other end"},
		{"not an object", cty.StringVal("12345abcde/67890fghij"), "/", "fails closed on a value that is not an applied object"},
		{
			"a segment contains the separator",
			obj(cty.StringVal("12345abcde"), cty.StringVal("12345abcde/67890fghij")),
			"/",
			"the provider's importer splits on that character, so it would see three segments where the object " +
				"has two. This is also the backstop for a page whose `id` bullet understates what `id` holds: a " +
				"type whose `id` is already the whole composite refuses here rather than composing it twice.",
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := LocatedComposedImportID(tc.obj, parts, tc.sep); ok {
				t.Errorf("composed %q, want a refusal.\n%s", got, tc.why)
			}
		})
	}

	if _, ok := LocatedComposedImportID(obj(cty.StringVal("a"), cty.StringVal("b")), []string{"id"}, "/"); ok {
		t.Error("composed a single-segment string. One segment is not a composite, and a caller reaching here " +
			"with one has lost track of which of the three record shapes it is writing.")
	}
	if _, ok := LocatedComposedImportID(obj(cty.StringVal("a"), cty.StringVal("b")), parts, ""); ok {
		t.Error("composed with no separator, which concatenates two identities into one unsplittable string")
	}
}

// TestDocumentedImportIDsIsBoundedByTheRefusalItReplaces holds the generated
// roster's own label against what it actually contains.
//
// This is the "a mask wider than its label" check. The roster's stated
// population is types [IDNotProvenWholeTypes] refuses; a member outside that
// set would be a type whose bare `id` the bare-`id` rule already accepts, and
// the grammar would then be a second, competing answer for a type that has
// one - which is how a working population starts recording something else.
func TestDocumentedImportIDsIsBoundedByTheRefusalItReplaces(t *testing.T) {
	if len(DocumentedImportIDs) == 0 {
		t.Fatal("DocumentedImportIDs is empty, so issue #337's second route reaches nothing and every " +
			"assertion that reads it passes vacuously")
	}
	var outside []string
	for name := range DocumentedImportIDs {
		if _, unproven := IDNotProvenWholeTypes[name]; !unproven {
			outside = append(outside, name)
		}
	}
	sort.Strings(outside)
	if len(outside) > 0 {
		t.Errorf("%d type(s) carry a documented grammar the bare-`id` rule does not refuse: %v\n"+
			"For such a type `id` is already proven to be the whole import string, so composing a second "+
			"answer out of its segments is a competing identity for a type that has one.", len(outside), outside)
	}

	for name, g := range DocumentedImportIDs {
		if g.Separator == "" {
			t.Errorf("%s: no separator, so nothing could be composed", name)
		}
		if len(g.Parts) < 2 {
			t.Errorf("%s: %d segment(s); a composite needs at least two", name, len(g.Parts))
		}
		seen := map[string]bool{}
		for _, p := range g.Parts {
			if p.Name != normalizeDocName(p.Name) {
				t.Errorf("%s: segment %q is not in comparison form, so it can never match a schema attribute", name, p.Name)
			}
			if seen[p.Name] {
				t.Errorf("%s: segment %q appears twice; one attribute cannot fill two positions", name, p.Name)
			}
			seen[p.Name] = true
		}
	}
}
