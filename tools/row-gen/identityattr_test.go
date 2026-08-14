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
// either follows deriveAssembledIdentityAttr's rule or sits in
// identityAttrEvidence with the raw evidence written down - and the ledger
// holds nothing else. A new ratified row that hand-writes an IdentityAttr
// the rule does not produce fails here until its evidence is recorded; a
// ledger entry whose row was corrected or dropped fails as stale.
func TestExplicitComponentIdentityAttrsAreDerivedOrEvidenced(t *testing.T) {
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
			// The convention the 17 explicit rows all follow: every
			// component, literals and cloud values included, carries the
			// name, because their rendered strings concatenate to form it.
			for i, c := range row.Components {
				if c.IdentityAttr != explicit {
					t.Errorf("%s: component %d carries IdentityAttr %q where the row's other components carry %q", name, i, c.IdentityAttr, explicit)
				}
			}
		}

		want, ok := deriveAssembledIdentityAttr(row.Components)
		entry, inLedger := identityAttrEvidence[name]

		switch {
		case ok && explicit == want:
			derived++
			if inLedger {
				t.Errorf("%s: the rule derives %q and the row agrees; its ledger entry is stale", name, want)
			}
		case ok || explicit != "":
			// The rule and the row disagree - the rule derives something
			// the row does not carry, or the row carries something the rule
			// cannot produce. Either way the evidence must be on file.
			if !inLedger {
				t.Errorf("%s: explicit IdentityAttr %q, rule derives %q (fired: %t) - not derivable and not in identityAttrEvidence", name, explicit, want, ok)
				continue
			}
			evidenced[name] = true
			if entry.attr != explicit {
				t.Errorf("%s: ledger records IdentityAttr %q but the row carries %q", name, entry.attr, explicit)
			}
			if entry.evidence == "" {
				t.Errorf("%s: ledger entry has no evidence", name)
			}
		default:
			if inLedger {
				t.Errorf("%s: in the ledger but carries no explicit IdentityAttr and the rule does not fire; stale entry", name)
			}
		}
	}

	for name := range identityAttrEvidence {
		if _, ok := identity.DefaultTable[name]; !ok {
			t.Errorf("ledger names %s, which the table does not admit; stale entry", name)
		}
	}

	// The measured split when this landed: 12 rows derived, 7 evidenced (5
	// carrying an attr the rule cannot reach, 2 where the rule would derive
	// a value the identity schema contradicts). Logged, not pinned - the
	// exactness of the ledger is the invariant, not the counts.
	t.Logf("derived: %d rows; evidenced: %d rows", derived, len(evidenced))
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
