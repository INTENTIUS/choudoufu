// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestExplicitComponentIdentityAttrsAreDerivedOrEvidenced is #106 criterion
// 3's ratchet: every explicit Component.IdentityAttr in the ratified table
// either follows deriveAssembledIdentityAttr's rule AND agrees with the
// provider's own identity schema, or sits in identityAttrEvidence with the
// raw evidence written down - and the ledger holds nothing else.
//
// The wire check is not decoration. This test's first version treated
// rule-agreement as correctness, and an adversarial audit defeated it two
// ways with one mutation: aws_sagemaker_user_profile's "arn" was counted a
// success of the rule while the 6.59.0 schema requires [domain_id,
// user_profile_name] with no arn attribute at all, and setting the two
// codeartifact policy rows to the "arn" their own evidence calls wrong,
// ledger entries deleted, passed clean. Now a named attribute the schema
// does not carry needs a ledger entry regardless of what the rule says.
func TestExplicitComponentIdentityAttrsAreDerivedOrEvidenced(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, err := loadSurvey(root + "/live/survey-full.json")
	if err != nil {
		t.Fatal(err)
	}
	wireHas := func(typeName, attr string) (conflicts bool) {
		attrs := survey[typeName].identityAttrs()
		if attrs == nil {
			return false // no identity schema: nothing to contradict
		}
		for _, a := range attrs {
			if a == attr {
				return false
			}
		}
		return true
	}

	derived, evidenced := 0, map[string]bool{}

	for name, row := range identity.DefaultTable {
		explicit := ""
		for _, c := range row.Components {
			if c.IdentityAttr == "" || c.IdentityAttr == identity.SameNameIdentity {
				continue
			}
			if explicit != "" && c.IdentityAttr != explicit {
				t.Errorf("%s: components name two identity attributes, %q and %q - the field is a property of the assembled identity and must be uniform", name, explicit, c.IdentityAttr)
			}
			explicit = c.IdentityAttr
		}
		if explicit != "" {
			// The convention the explicit rows all follow: every component,
			// literals and cloud values included, carries the name, because
			// their rendered strings concatenate to form it.
			for i, c := range row.Components {
				if c.IdentityAttr != explicit {
					t.Errorf("%s: component %d carries IdentityAttr %q where the row's other components carry %q", name, i, c.IdentityAttr, explicit)
				}
			}
		}

		want, ruleFired := deriveAssembledIdentityAttr(row.Components)
		wireConflict := explicit != "" && wireHas(name, explicit)
		clean := !wireConflict && ((explicit == "" && !ruleFired) || (ruleFired && explicit == want))
		entry, inLedger := identityAttrEvidence[name]

		switch {
		case clean && explicit != "":
			derived++
			if inLedger {
				t.Errorf("%s: the rule derives %q, the row agrees and the identity schema carries it; the ledger entry is stale", name, want)
			}
		case clean:
			if inLedger {
				t.Errorf("%s: in the ledger but carries no explicit IdentityAttr and the rule does not fire; stale entry", name)
			}
		default:
			// Some disagreement: rule-vs-row, wire-vs-row, or an explicit
			// attr the rule cannot produce. The evidence must be on file.
			if !inLedger {
				t.Errorf("%s: explicit IdentityAttr %q, rule derives %q (fired: %t), schema conflict: %t - not in identityAttrEvidence", name, explicit, want, ruleFired, wireConflict)
				continue
			}
			evidenced[name] = true
			if entry.attr != explicit {
				t.Errorf("%s: ledger records IdentityAttr %q but the row carries %q", name, entry.attr, explicit)
			}
			if entry.evidence == "" {
				t.Errorf("%s: ledger entry has no evidence", name)
			}
		}
	}

	for name := range identityAttrEvidence {
		if _, ok := identity.DefaultTable[name]; !ok {
			t.Errorf("ledger names %s, which the table does not admit; stale entry", name)
		}
	}

	// The measured split after the audit: 11 rows derived clean, 8
	// evidenced (4 glue + securityhub the rule cannot reach, 2 codeartifact
	// rows where the rule would derive what the schema contradicts, and
	// sagemaker_user_profile where rule and row agree against the schema).
	// Logged, not pinned - the ledger's exactness is the invariant.
	t.Logf("derived clean: %d rows; evidenced: %d rows", derived, len(evidenced))
}

// TestApplyDerivedIdentityAttrs covers the proposal side: the shapes the
// rule fires on, and the shapes it must leave alone.
func TestApplyDerivedIdentityAttrs(t *testing.T) {
	arnRow := []identity.Component{
		{Literal: "arn:aws:sns:"},
		{Cloud: "region"},
		{Literal: ":"},
		{Cloud: "account-id"},
		{Literal: ":"},
		{Attrs: []string{"name"}, IdentityAttr: identity.SameNameIdentity},
	}
	got := applyDerivedIdentityAttrs(arnRow)
	for i, c := range got {
		if c.IdentityAttr != "arn" {
			t.Errorf("arn row component %d: IdentityAttr %q, want \"arn\"", i, c.IdentityAttr)
		}
	}
	if arnRow[0].IdentityAttr != "" {
		t.Error("applyDerivedIdentityAttrs mutated its input")
	}

	urlRow := []identity.Component{
		{Literal: "https://sqs."},
		{Attrs: []string{"name"}, IdentityAttr: identity.SameNameIdentity},
	}
	for i, c := range applyDerivedIdentityAttrs(urlRow) {
		if c.IdentityAttr != "url" {
			t.Errorf("url row component %d: IdentityAttr %q, want \"url\"", i, c.IdentityAttr)
		}
	}

	// A separator-joined composite - every proposal bucketComposite builds
	// today - has no leading literal and must pass through untouched.
	composite := []identity.Component{
		{Attrs: []string{"cluster"}, IdentityAttr: identity.SameNameIdentity},
		{Literal: "/"},
		{Attrs: []string{"name"}, IdentityAttr: identity.SameNameIdentity},
	}
	got = applyDerivedIdentityAttrs(composite)
	for i, c := range got {
		if c.IdentityAttr != composite[i].IdentityAttr {
			t.Errorf("separator composite component %d changed: %q -> %q", i, composite[i].IdentityAttr, c.IdentityAttr)
		}
	}
}
