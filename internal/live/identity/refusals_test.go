// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"testing"

	"github.com/intentius/choudoufu/internal/live/refusalscan"
)

// TestRefusalsRegistered is GitHub issue #110's lockstep test for this
// package, the counterpart of internal/live/lint's limits_test.go.
//
// It parses this package's own non-test source and collects every diagnostic
// Summary it can produce: the second argument of every resolver.errorf call,
// and every Summary: field in an hcl.Diagnostic literal. That set has to
// equal the registry in refusals.go.
//
// The direction that matters is "a refusal with no registry entry fails".
// Before this existed, nothing could even ask what this package refuses,
// which is why live/LIMITATIONS.md documents almost none of it while
// documenting all sixteen lint rules.
func TestRefusalsRegistered(t *testing.T) {
	summaries := make([]string, 0, len(refusals))
	whats := map[string]string{}
	for _, r := range Refusals() {
		summaries = append(summaries, r.Summary)
		whats[r.Summary] = r.What
	}
	refusalscan.Check(t, refusalscan.Params{
		Dir:        ".",
		SkipFile:   "refusals.go",
		Registered: summaries,
		What:       whats,
		// Two functions take their summary as a variable. resolver.errorf
		// is this package's own diagnostic helper, whose callers pass the
		// literal the scan records; Finding.Diagnostic chooses between the
		// two Summary-prefixed constants in schema_verify.go.
		DynamicSites: []string{"errorf", "Diagnostic"},
	})
}

// TestRefusalsWithOwnDoc pins the four refusals that override where they are
// documented, because an override is the one way a refusal can end up with no
// generated entry and no hand-written one either.
//
// This replaces a ratchet on the count of undocumented refusals. That
// measure meant something while the gap was 27 of 30 and closing it was
// pending work; once live/LIMITATIONS.md is generated from this table there
// are no undocumented refusals to count, and what can still go wrong is a
// row pointing somewhere nobody wrote. internal/live/check's
// TestEveryRefusalDocsRefIsResolvable checks that for every row; this checks
// that the set of rows claiming to be documented elsewhere is the set
// someone decided on.
//
// An audit once defeated the old count by blanking one refusal's DocsRef and
// moving it onto an unrelated one: total unchanged, test green. Pinning
// membership rather than a number is what makes both directions visible, and
// that property is kept here.
func TestRefusalsWithOwnDoc(t *testing.T) {
	elsewhere := map[string]string{
		"Resource type outside the live-markers subset": `live/LIMITATIONS.md, "unadmitted-type"`,
		"Two resources with the same identity":          `live/LIMITATIONS.md, "duplicate-identity"`,
		"for_each key cannot be recorded as a marker":   `live/MARKERS.md, "Ownership semantics"`,
	}

	for _, r := range RefusalsWithOwnDoc() {
		want, ok := elsewhere[r.Summary]
		if !ok {
			t.Errorf("%q now overrides its documentation to %q. An override means no generated entry is written for it, so the target has to be a fuller treatment somebody wrote - if that is what this is, add it to this test's set.", r.Summary, r.Doc)
			continue
		}
		if r.Doc != want {
			t.Errorf("%q points at %q, want %q", r.Summary, r.Doc, want)
		}
		delete(elsewhere, r.Summary)
	}
	for summary, ref := range elsewhere {
		t.Errorf("%q no longer points at %q. Its hand-written entry is the fuller one; losing the override silently downgrades it to the generated one-liner.", summary, ref)
	}
}

// TestDocsRefDerivesFromSummary covers the derivation itself: a row with no
// override is documented under its own Summary.
func TestDocsRefDerivesFromSummary(t *testing.T) {
	for _, r := range Refusals() {
		if r.Doc != "" {
			if got := r.DocsRef(); got != r.Doc {
				t.Errorf("%q: DocsRef() = %q, want the override %q", r.Summary, got, r.Doc)
			}
			continue
		}
		want := fmt.Sprintf("live/LIMITATIONS.md, %q", r.Summary)
		if got := r.DocsRef(); got != want {
			t.Errorf("%q: DocsRef() = %q, want %q", r.Summary, got, want)
		}
	}
}
