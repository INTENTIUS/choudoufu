// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #220: a sibling's plain, statically-evaluable argument was
// unreachable, because the only path from one resource to another's
// attribute went through [TypeIdentity.IdentityAttrs] - the sibling's cloud
// identity, not its ordinary configuration. These tests are the three
// confirmed corpus sites (one wearing #197's refusal, two wearing #187's)
// plus the boundary the fix has to hold everywhere it does not appear in
// the corpus. See [resolver.siblingLiteralExpr],
// [resolver.comprehensionOverSiblingAttr] and
// [resolver.forEachOverTupleComprehension].

// siblingTestSchemas is the fake schema every test below other than the
// three confirmed sites uses: test_sibling, whose "key" is its whole
// identity, standing next to three plain arguments the schema tells apart
// exactly the way #220's fix has to - literal_val is Optional and never
// Computed, computed_val is Computed only, and optcomp_val is
// Optional+Computed, the legacy-SDK shape the real AWS provider gives
// aws_s3_bucket's own "bucket" argument (see synthesize.go's own doc
// comments). test_domain_source stands in for fastly_tls_subscription's
// confirmed shape (domains, a plain list(string); managed_dns_challenges,
// Computed), so that type's unrelated admission gap - fastly_tls_subscription
// itself has no entry in DefaultTable - cannot be mistaken for a failure of
// the for_each fix under test.
func siblingTestSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"test_sibling": {
			args: map[string]string{
				"key":          "req",
				"literal_val":  "opt",
				"computed_val": "comp",
				"optcomp_val":  "optcomp",
			},
			identity: map[string]string{"key": "req"},
		},
		"test_domain_source": {
			args: map[string]string{
				"domains":                "req",
				"managed_dns_challenges": "comp",
			},
		},
	})
}

// TestSiblingLiteralAttributeResolves is the mastino/global/dns confirmed
// site (#197's own refusal): aws_route53_zone.production's identity is
// zone_id, so name is correctly absent from its IdentityAttrs, but
// mx-datacite's own name argument reads it as a plain literal string
// ("datacite.org", written directly in the zone's block) rather than as an
// identity reference. Before #220 this refused with "Not an identity
// attribute"; the zone_id half of the record still waits on cloud discovery
// (aws_route53_zone is ServerAssigned), so the record as a whole stays
// parent-derived, but the name component specifically must come back a
// plain literal.
func TestSiblingLiteralAttributeResolves(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "sibling-literal-attr"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: fakeProviderSchemas(map[string]fakeType{
		"aws_route53_zone": {
			args: map[string]string{"name": "req"},
		},
	})})
	assertNoErrors(t, diags)

	record := resolutionAt(t, result, `aws_route53_record.mx-datacite`)
	if record.Class != ClassParentDerived {
		t.Fatalf("the record resolved %s; zone_id still waits on the zone's own cloud discovery", record.Class)
	}
	nameParts, ok := record.attrParts("name")
	if !ok {
		t.Fatalf("the record's formula has no name component: %v", record.Formula)
	}
	if len(nameParts) != 1 || nameParts[0].Parent != nil || nameParts[0].Literal != "datacite.org" {
		t.Errorf("the record's name resolved to %v, want a single literal part \"datacite.org\"", nameParts)
	}
}

// TestForEachTupleComprehensionOverSiblingsOwnIdentity is the
// govuk-infrastructure ecr confirmed site (#187's own refusal):
// aws_ecr_lifecycle_policy's for_each is
// toset([for repo in local.X : aws_ecr_repository.Y[repo].name]) - a keyless
// for-expression wrapped in toset(), which forEachOverComprehension never
// recognized (it requires a key => value comprehension) and which
// forEachOverResource refused outright as "computed from another resource's
// attributes", even though aws_ecr_repository's own identity IS "name" and
// every repository name is a literal string built from each.key. Before
// #220 the whole for_each refused; after it, both instances resolve
// concretely.
func TestForEachTupleComprehensionOverSiblingsOwnIdentity(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-tuple-comprehension"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for _, d := range diags {
		if d.Description().Summary == "Non-static for_each expression" {
			t.Fatalf("still refused as non-static: %s", d.Description().Detail)
		}
	}

	for _, repo := range []string{"repo-a", "repo-b"} {
		name := "github/alphagov/govuk/" + repo
		addr := `aws_ecr_lifecycle_policy.ecr_lifecycle_policy["` + name + `"]`
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Fatalf("%s resolved %s; repository is a plain literal read of the sibling's own identity", addr, res.Class)
		}
		if res.ImportID != name {
			t.Errorf("%s resolved to %q, want %q", addr, res.ImportID, name)
		}
	}
}

// TestForEachComprehensionOverSiblingsLiteralArgument is the
// simpleinfra/fastly-tls-subscription confirmed site (#187's own refusal,
// the second of its two shapes): the for_each's key clause reads
// test_domain_source.subscription.domains, set verbatim from the sibling's
// own literal block, while the value clause reaches into
// managed_dns_challenges - a genuinely apply-time, Computed-only attribute.
// Before #220 the whole for_each refused as "Non-static for_each
// expression" on the collection clause; after it, the for_each itself
// expands to both domains, and only the per-instance identity argument that
// genuinely needs the apply-time value (name, built from each.value) still
// refuses - proving the fix moved exactly the refusal the issue named, and
// nothing past it.
func TestForEachComprehensionOverSiblingsLiteralArgument(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-sibling-attr"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: siblingTestSchemas()})

	for _, d := range diags {
		if d.Description().Summary == "Non-static for_each expression" {
			t.Fatalf("the for_each itself still refused as non-static: %s", d.Description().Detail)
		}
	}

	// The residual refusal this fix deliberately leaves in place: each
	// instance's own "name" and "type" arguments read each.value.*, and a
	// keyOnly expansion (this package refuses to guess at
	// managed_dns_challenges) leaves each.value itself unresolvable. Two
	// domains times two components means four such diagnostics - which is
	// only possible if the for_each itself actually expanded to two
	// instances, since a refused for_each produces exactly one diagnostic
	// on the whole expression, not one per key.
	residual := 0
	for _, d := range diags {
		if d.Description().Summary == "Dynamic value in static context" {
			residual++
		}
	}
	if residual != 4 {
		t.Fatalf("got %d \"Dynamic value in static context\" diagnostics, want 4 (name and type, for each of the two domain instances, proving the for_each expanded to both keys): %v", residual, diags)
	}

	// Confirms the for_each's OWN keys, independent of whether the
	// instances' own identity resolved: an unresolved instance is simply
	// absent from the identity result, but [resolver.expansionFor] is
	// memoized per resource regardless, so a second, unrelated resolution
	// error would surface here as a different key set or a
	// "does not exist" diagnostic instead.
	for _, d := range diags {
		if d.Description().Summary == "Reference to a resource instance that does not exist" {
			t.Fatalf("a domain key did not survive the for_each expansion: %s", d.Description().Detail)
		}
	}
}

// TestSiblingLiteralAttributeBoundary is the adversarial boundary #220's fix
// has to hold beyond the three confirmed sites: a plain Optional,
// non-Computed argument resolves: a Computed-only attribute stays refused
// exactly as before, and so does the Optional+Computed shape real providers
// use for a caller-settable value the provider may still override
// (aws_s3_bucket's "bucket"). Getting the last two wrong reads an unknown as
// known and writes a wrong marker - the worst failure this system has.
func TestSiblingLiteralAttributeBoundary(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "sibling-literal-attr-boundary"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: siblingTestSchemas()})

	literal := resolutionAt(t, result, `aws_route53_record.reads_literal`)
	if literal.Class != ClassConcrete {
		t.Fatalf("reads_literal resolved %s; zone_id, name and type are all literal", literal.Class)
	}
	if want := "Z1_hello_TXT"; literal.ImportID != want {
		t.Errorf("reads_literal resolved to %q, want %q", literal.ImportID, want)
	}

	for _, tc := range []struct {
		addr string
		attr string
	}{
		{`aws_route53_record.reads_computed`, "computed_val"},
		{`aws_route53_record.reads_optcomp`, "optcomp_val"},
	} {
		if _, ok := result.Get(mustAddr(t, tc.addr)); ok {
			t.Fatalf("%s resolved; %s is Computed and must stay refused", tc.addr, tc.attr)
		}
		if !hasDiag(diags, "Not an identity attribute", tc.attr) {
			t.Errorf("no \"Not an identity attribute\" diagnostic naming %s:\n%s", tc.attr, renderDiags(diags))
		}
	}
}

// TestSiblingLiteralAttributeChainRefusesAtTheRightLink: a reads b's own
// literal argument, which is itself set from c's Computed attribute. The
// chain must refuse at c - literal_val being schema-safe on b does not make
// whatever expression b actually wrote for it safe, only eligible for the
// same resolution an ordinary identity component already gets.
func TestSiblingLiteralAttributeChainRefusesAtTheRightLink(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "sibling-literal-attr-chain"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: siblingTestSchemas()})

	if !diags.HasErrors() {
		t.Fatalf("expected a refusal; b's literal_val is set from c's Computed attribute")
	}
	if _, ok := result.Get(mustAddr(t, `aws_route53_record.a`)); ok {
		t.Fatalf("aws_route53_record.a resolved; the chain bottoms out at c's Computed computed_val")
	}
	if !hasDiag(diags, "Not an identity attribute", "computed_val") {
		t.Errorf("no diagnostic naming computed_val - the refusal did not land at the right link:\n%s", renderDiags(diags))
	}
}

// TestSiblingLiteralAttributeUnsetVariableRefuses: a sibling's own literal
// argument set from a variable with no value must refuse, not resolve to
// the empty string - the same "error at use time" every other identity
// argument in this package already gives an unset required variable.
func TestSiblingLiteralAttributeUnsetVariableRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "sibling-literal-attr-unset-var"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: siblingTestSchemas()})

	if !diags.HasErrors() {
		t.Fatalf("expected a refusal; var.unset_name has no default and no supplied value")
	}
	if res, ok := result.Get(mustAddr(t, `aws_route53_record.a`)); ok {
		t.Fatalf("aws_route53_record.a resolved to %v; an unset variable must refuse, not resolve to empty", res)
	}
}
