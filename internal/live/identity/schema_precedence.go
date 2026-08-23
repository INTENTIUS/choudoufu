// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import "sort"

// This file is ruling 2 of rfc/20260823-foundation-order-ruling.md (issue
// #387), the online half: [resolver.lookupType] calls schemaReproducesRow to
// decide, at RESOLUTION time and with the REAL provider schemas, whether a
// synthesized entry ([SynthesizeTypeIdentity]) says the same thing a hand
// table row ([DefaultTable]) already does. tools/row-gen/schemafirst.go
// makes the identical comparison offline, over live/import-grammar.json's
// identity_schema_required list instead of a real schema, to decide what to
// MEASURE (live/rowgen-convergence.json's schema_reproduces bucket) rather
// than what to prefer - the two never disagree about the RULE, only about
// where the schema's required-attribute evidence comes from.
//
// The rule: strip every separator-literal and cloud-context component from
// each side's Components, leaving the identity-bearing configuration
// argument names; the two entries "reproduce" each other when that set is
// identical and, once "id" is set aside (identity.SynthesizeTypeIdentity's
// own doc comment - it never claims "id" as an identity source, because
// whether a type's id attribute equals its import identity is precisely
// the inference a schema does not carry), the row's own IdentityAttrs claim
// is either empty or the same set. A row that names a Cloud-valued
// component (an ARN assembled from region/account/name) or more than one
// alternative Attrs on one component (an any-of argument) never reproduces,
// because synthesized never builds either shape.

// preferSynthesized decides, for [resolver.lookupType], whether synthesized
// should be used instead of row, and if so, returns the exact entry to use.
// The second return is false when it should not - the row wins, unchanged.
//
// The returned entry is synthesized itself, verbatim, except for one field:
// when row claims "id" as an identity source and synthesized does not -
// the one difference [schemaReproducesRow]'s own IdentityAttrs check
// tolerates, since [SynthesizeTypeIdentity] never claims "id" on principle
// - "id" is added back onto the entry actually used.
//
// Dropping it silently was the shape this ruling's own first schema-backed
// refusal-probe run caught directly: sixty-nine new "Not an identity
// attribute" refusals and several real corpus entries losing resolved
// instances, every one a sibling resource reading a reproduced type's own
// `.id` (aws_s3_bucket among them - IdentityAttrs [id, bucket] on the row,
// [bucket] alone from the schema), which no schema-less instrument
// (internal/live/check's identity golden, refusal-probe's default sweep)
// could ever have seen, because none of their fixtures happens to reference
// it. "Reproduces the row" and "safe to use verbatim in the row's place"
// are different claims for exactly this reason, and this function is what
// makes the second one true whenever the first one is.
func preferSynthesized(row, synthesized TypeIdentity) (TypeIdentity, bool) {
	if !schemaReproducesRow(row, synthesized) {
		return TypeIdentity{}, false
	}
	if containsString(row.IdentityAttrs, "id") && !containsString(synthesized.IdentityAttrs, "id") {
		merged := synthesized
		merged.IdentityAttrs = append(append([]string(nil), synthesized.IdentityAttrs...), "id")
		sort.Strings(merged.IdentityAttrs)
		return merged, true
	}
	return synthesized, true
}

// containsString reports whether s appears anywhere in ss.
func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// schemaReproducesRow reports whether synthesized - an entry
// [SynthesizeTypeIdentity] built from the real provider schemas - says the
// same thing row already does. See this file's own doc comment for the
// rule. Never called with a synthesized entry that failed
// ([SynthesizeTypeIdentity]'s own bool already gates that). Callers deciding
// what to actually USE should call [preferSynthesized] instead, which also
// carries forward the one field this comparison deliberately tolerates a
// difference on.
func schemaReproducesRow(row, synthesized TypeIdentity) bool {
	rowAttrs, ok := identityBearingAttrNames(row.Components)
	if !ok {
		return false
	}
	synthAttrs, ok := identityBearingAttrNames(synthesized.Components)
	if !ok {
		return false
	}
	sort.Strings(rowAttrs)
	sort.Strings(synthAttrs)
	if !equalSortedStrings(rowAttrs, synthAttrs) {
		return false
	}

	claimed := withoutIDAttr(row.IdentityAttrs)
	sort.Strings(claimed)
	want := append([]string(nil), synthesized.IdentityAttrs...)
	sort.Strings(want)
	return len(claimed) == 0 || equalSortedStrings(claimed, want)
}

// identityBearingAttrNames extracts the identity-bearing configuration
// argument names off comps, unsorted (the caller sorts). It refuses the
// whole comparison - the second return is false - the moment it meets a
// component [SynthesizeTypeIdentity] could never have built: a Cloud-valued
// component (the account or region, not a configuration argument at all)
// or one offering more than one alternative Attrs (a fallback chain, a
// judgment call no schema states a preference order for). A plain
// separator literal (no Attrs at all) contributes nothing and is skipped,
// never refused - a schema-derived entry has none, but their absence in row
// is not itself a disagreement.
func identityBearingAttrNames(comps []Component) (names []string, ok bool) {
	for _, c := range comps {
		switch {
		case len(c.Attrs) == 0:
			continue
		case c.Cloud != CloudNone:
			return nil, false
		case len(c.Attrs) != 1:
			return nil, false
		default:
			names = append(names, c.Attrs[0])
		}
	}
	return names, true
}

// withoutIDAttr returns ss with every "id" entry removed. See this file's
// own doc comment for why "id" is set aside rather than compared.
func withoutIDAttr(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "id" {
			out = append(out, s)
		}
	}
	return out
}

// equalSortedStrings compares two already-sorted string slices for exact
// equality.
func equalSortedStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
