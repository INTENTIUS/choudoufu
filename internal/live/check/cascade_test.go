// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/dataread"
)

// These tests are GitHub issue #184's rescoped fix: identity.ResolveWith
// runs with no [identity.Context.DataResults] here (Analyze is an offline
// instrument; only a real live-plan reads data sources), so an identity
// argument that reads one refuses as "Dynamic value in static context" -
// already reclassified as data-read-eligible by classifyDataSite - and
// every OTHER resource whose identity references that resource's identity
// attribute cascades into identity's own "Unresolvable identity", a
// diagnostic that carries no reference back to the data read that actually
// caused it (internal/live/identity/resolve.go's parentPart). Analyze
// traces that cascade back through [neededByResourceAddr] and
// [parseCascadeDetail] and reclassifies it the same way, transitively.

// TestAnalyzeCascadesEligibleReadThroughDependents: the log stream's
// "Unresolvable identity" is re-homed under the data-read pass, alongside
// the log group's own finding, so both collapse into one eligible-read
// finding with two sites.
func TestAnalyzeCascadesEligibleReadThroughDependents(t *testing.T) {
	report := analyzeFixtureDir(t, "cascade-eligible")

	if len(report.Findings) != 1 {
		t.Fatalf("want exactly one finding, got %d: %+v", len(report.Findings), report.Findings)
	}
	f := report.Findings[0]
	if f.Layer != LayerDataread || f.ID != dataread.SummaryEligibleRead {
		t.Fatalf("finding is %s/%s, want %s/%s", f.Layer, f.ID, LayerDataread, dataread.SummaryEligibleRead)
	}
	if len(f.Sites) != 2 {
		t.Fatalf("want two sites (the log group's own read and the log stream's cascade), got %d: %+v", len(f.Sites), f.Sites)
	}
	var sawCascade bool
	for _, site := range f.Sites {
		if strings.Contains(site.Detail, "depends on the identity of") {
			sawCascade = true
			if !strings.Contains(site.Detail, "own identity depends on a data source") {
				t.Errorf("cascaded site lacks the explanatory tail: %s", site.Detail)
			}
		}
	}
	if !sawCascade {
		t.Errorf("no site carries the cascade wording; sites: %+v", f.Sites)
	}

	if got := ClassifyOnboarding(true, refusalIDs(report.Findings)); got != OnboardingDataReadEligible {
		t.Errorf("classified %q, want %q", got, OnboardingDataReadEligible)
	}
}

// TestAnalyzeCascadesThroughAModuleBoundary: identity.StaticIdentifier's
// NeededBy carries a redundant, differently-spelled module prefix inside a
// child module ("module.child:module.child.type.name.attr" - see
// neededByResourceAddr's doc comment). The recovered address must still
// match the cascade diagnostic's own parent text, or the propagation never
// fires for anything but the root module.
func TestAnalyzeCascadesThroughAModuleBoundary(t *testing.T) {
	report := analyzeFixtureDir(t, "cascade-eligible-module")

	if len(report.Findings) != 1 {
		t.Fatalf("want exactly one finding, got %d: %+v", len(report.Findings), report.Findings)
	}
	f := report.Findings[0]
	if f.Layer != LayerDataread || f.ID != dataread.SummaryEligibleRead {
		t.Fatalf("finding is %s/%s, want %s/%s", f.Layer, f.ID, LayerDataread, dataread.SummaryEligibleRead)
	}
	if len(f.Sites) != 2 {
		t.Fatalf("want two sites, got %d: %+v", len(f.Sites), f.Sites)
	}
}

// TestAnalyzeCascadesPerForEachKey: two for_each keys must not collide in
// eligibleAddrs - each key's log stream cascades from its OWN key's log
// group, not from whichever key happened to be seen first.
func TestAnalyzeCascadesPerForEachKey(t *testing.T) {
	report := analyzeFixtureDir(t, "cascade-eligible-foreach")

	if len(report.Findings) != 1 {
		t.Fatalf("want exactly one finding, got %d: %+v", len(report.Findings), report.Findings)
	}
	f := report.Findings[0]
	if f.Layer != LayerDataread || f.ID != dataread.SummaryEligibleRead {
		t.Fatalf("finding is %s/%s, want %s/%s", f.Layer, f.ID, LayerDataread, dataread.SummaryEligibleRead)
	}
	if len(f.Sites) != 4 {
		t.Fatalf("want four sites (two log groups, two log streams), got %d: %+v", len(f.Sites), f.Sites)
	}
	for _, key := range []string{`"a"`, `"b"`} {
		var found bool
		for _, site := range f.Sites {
			if strings.Contains(site.Detail, "depends on the identity of") && strings.Contains(site.Detail, key) {
				found = true
			}
		}
		if !found {
			t.Errorf("no cascaded site names key %s; sites: %+v", key, f.Sites)
		}
	}
}

// TestAnalyzeDoesNotCascadeAnUnrelatedFailure is the false-"you are fine"
// guard: the log group's identity fails for a reason that has nothing to
// do with a data read, so the log stream's cascade onto it must stay a
// hard "Unresolvable identity" refusal, not read as eligible.
func TestAnalyzeDoesNotCascadeAnUnrelatedFailure(t *testing.T) {
	report := analyzeFixtureDir(t, "cascade-blocked")

	var identityCount, datareadCount int
	for _, f := range report.Findings {
		switch f.Layer {
		case LayerIdentity:
			identityCount += len(f.Sites)
			if f.ID != "Identity argument not set" && f.ID != "Unresolvable identity" {
				t.Errorf("unexpected identity finding %s", f.ID)
			}
		case LayerDataread:
			datareadCount += len(f.Sites)
		}
	}
	if datareadCount != 0 {
		t.Errorf("an unrelated failure was reclassified as data-read-eligible: %+v", report.Findings)
	}
	if identityCount != 2 {
		t.Fatalf("want two identity-layer sites (the group's own failure and the policy's cascade), got %d: %+v", identityCount, report.Findings)
	}

	if got := ClassifyOnboarding(true, refusalIDs(report.Findings)); got != OnboardingLanguageBlocked {
		t.Errorf("classified %q, want %q", got, OnboardingLanguageBlocked)
	}
}

// TestAnalyzeDoesNotReclassifyMultiComponentCascade is GitHub issue #221's
// regression guard: it is the audit's own repro, reproduced with a built
// binary before this fix existed. aws_cloudwatch_log_stream's identity has
// TWO real components (log_group_name, name); log_group_name cascades from
// the log group's eligible data-source read, but "name" has no value at
// all - a second, wholly independent failure resolveInstance's
// first-failing-component short-circuit never reaches, so it raises no
// diagnostic of its own anywhere in this configuration.
//
// Before #221, the cascade fixpoint saw only that the first component
// traced to an eligible read and reclassified the whole instance as "no
// configuration edit is needed" - which is false; plain "tofu validate"
// refuses this configuration. dependentSafeToReclassify must refuse to
// reclassify here, leaving the log stream's cascade a hard "Unresolvable
// identity" refusal so the estate is not told it is fine when it is not.
func TestAnalyzeDoesNotReclassifyMultiComponentCascade(t *testing.T) {
	report := analyzeFixtureDir(t, "cascade-multicomponent-blocked")

	var identityCount, datareadCount int
	for _, f := range report.Findings {
		switch f.Layer {
		case LayerIdentity:
			identityCount += len(f.Sites)
			if f.ID != "Unresolvable identity" {
				t.Errorf("unexpected identity finding %s", f.ID)
			}
		case LayerDataread:
			datareadCount += len(f.Sites)
		}
	}
	// The log group's OWN data-source read is still eligible on its own
	// terms - #221 does not touch the direct case, only the cascade. Only
	// the log stream's cascade onto it must stay held back.
	if datareadCount != 1 {
		t.Errorf("want one data-read-eligible site (the log group's own read), got %d: %+v", datareadCount, report.Findings)
	}
	if identityCount != 1 {
		t.Fatalf("want one identity-layer site (the log stream's cascade, held hard-refused), got %d: %+v", identityCount, report.Findings)
	}

	if got := ClassifyOnboarding(true, refusalIDs(report.Findings)); got != OnboardingLanguageBlocked {
		t.Errorf("classified %q, want %q", got, OnboardingLanguageBlocked)
	}
}

// TestParseCascadeDetail pins the fixed format
// internal/live/identity/resolve.go's parentPart raises "Unresolvable
// identity" with, and that a summary or detail that does not match it -
// including a future wording change identity makes that this package
// cannot see coming - never crashes and never parses partial garbage.
func TestParseCascadeDetail(t *testing.T) {
	cases := []struct {
		name       string
		summary    string
		detail     string
		wantOK     bool
		wantChild  string
		wantParent string
	}{
		{
			name:       "well formed",
			summary:    "Unresolvable identity",
			detail:     `aws_cloudwatch_log_stream.per_zone.log_group_name depends on the identity of aws_cloudwatch_log_group.per_zone, which could not be resolved (see the other error).`,
			wantOK:     true,
			wantChild:  "aws_cloudwatch_log_stream.per_zone.log_group_name",
			wantParent: "aws_cloudwatch_log_group.per_zone",
		},
		{
			name:    "wrong summary",
			summary: "Not an identity attribute",
			detail:  `aws_cloudwatch_log_stream.per_zone.log_group_name depends on the identity of aws_cloudwatch_log_group.per_zone, which could not be resolved (see the other error).`,
			wantOK:  false,
		},
		{
			name:    "right summary, unrecognised detail",
			summary: "Unresolvable identity",
			detail:  "something identity changed to say instead",
			wantOK:  false,
		},
		{
			name:    "empty",
			summary: "Unresolvable identity",
			detail:  "",
			wantOK:  false,
		},
		{
			// A for_each key that legitimately contains the parser's own
			// separator text. strings.Index finds the FIRST occurrence of
			// cascadeMiddle, which is the one inside the key's quoted
			// string, not the real one further right - so this
			// mis-splits. The fail-safe direction matters more than the
			// exact wrong values: ok must still be true (parseCascadeDetail
			// itself never panics or rejects on this shape, since the
			// detail still contains the middle and ends with the fixed
			// suffix), but the recovered child and parent must NOT be the
			// real addresses, so [Analyze]'s cascade fixpoint can never
			// match them against a real eligibleAddrs entry and this stays
			// a hard "Unresolvable identity" refusal - safe, not silent.
			name:       "address contains the parser's own separator text",
			summary:    "Unresolvable identity",
			detail:     `aws_instance.x[" depends on the identity of x"].id depends on the identity of aws_instance.y, which could not be resolved (see the other error).`,
			wantOK:     true,
			wantChild:  `aws_instance.x["`,
			wantParent: `x"].id depends on the identity of aws_instance.y`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			child, parent, ok := parseCascadeDetail(tc.summary, tc.detail)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if child != tc.wantChild {
				t.Errorf("child = %q, want %q", child, tc.wantChild)
			}
			if parent != tc.wantParent {
				t.Errorf("parent = %q, want %q", parent, tc.wantParent)
			}
			// The fail-safe assertion this case exists for: a mis-split
			// address must never coincide with a real
			// addrs.AbsResourceInstance.String() shape that the fixpoint
			// could accidentally match against eligibleAddrs. Both halves
			// here carry a stray '"' or a run-on sentence fragment, which
			// [addrs.ParseAbsResourceInstanceStr] refuses - proving the
			// mismatch cannot silently resolve to a real instance.
			if tc.name == "address contains the parser's own separator text" {
				if _, diags := addrs.ParseAbsResourceInstanceStr(child); !diags.HasErrors() {
					t.Errorf("mis-split child %q parsed as a real resource-instance address; the safe-direction guarantee depends on it NOT doing so", child)
				}
			}
		})
	}
}

// TestNeededByResourceAddr pins the three shapes
// [neededByResourceAddr] must recover identically to a cascade
// diagnostic's own parent text: root module, nested module (the
// differently-spelled, redundant module prefix), and a for_each key.
func TestNeededByResourceAddr(t *testing.T) {
	cases := []struct {
		name     string
		neededBy string
		want     string
	}{
		{
			name:     "root",
			neededBy: `aws_cloudwatch_log_group.per_zone.name`,
			want:     `aws_cloudwatch_log_group.per_zone`,
		},
		{
			name:     "nested module, doubled prefix",
			neededBy: `module.child:module.child.aws_cloudwatch_log_group.per_zone.name`,
			want:     `module.child.aws_cloudwatch_log_group.per_zone`,
		},
		{
			name:     "for_each key",
			neededBy: `aws_cloudwatch_log_group.per_zone["a"].name`,
			want:     `aws_cloudwatch_log_group.per_zone["a"]`,
		},
		{
			name:     "no attribute segment",
			neededBy: `aws_cloudwatch_log_group`,
			want:     "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := neededByResourceAddr(tc.neededBy); got != tc.want {
				t.Errorf("neededByResourceAddr(%q) = %q, want %q", tc.neededBy, got, tc.want)
			}
		})
	}
}

// TestAnalyzeCascadeSweepsEveryEligibleFixture is a coarse sweep over every
// "eligible" cascade fixture: no [LayerIdentity] finding may survive
// [Analyze] on any of them.
//
// Correction (#221's audit): an earlier version of this comment claimed the
// test "cross-checks against what identity.ResolveWith actually emits
// today" - implying an oracle independent of the rest of this file. It does
// not. It calls analyzeFixtureDir, the exact same [Analyze] entry point
// TestAnalyzeCascadesEligibleReadThroughDependents and its two siblings
// above already call on these same three fixtures, and those three already
// assert the identical fact with exact finding and site counts - this test
// is fully redundant with them. It is not, however, a self-agreement
// ratchet in the sense that matters (a test that checks a derivation
// against a rule the code itself computed): Analyze calls
// identity.ResolveWith for real, so this does exercise the true resolver
// transitively. It simply adds no coverage the three tests above lack.
func TestAnalyzeCascadeSweepsEveryEligibleFixture(t *testing.T) {
	for _, dir := range []string{"cascade-eligible", "cascade-eligible-module", "cascade-eligible-foreach"} {
		t.Run(dir, func(t *testing.T) {
			report := analyzeFixtureDir(t, dir)
			for _, f := range report.Findings {
				if f.Layer == LayerIdentity {
					t.Errorf("%s: an identity-layer finding survived the cascade fix: %s (%d sites)", dir, f.ID, len(f.Sites))
				}
			}
		})
	}
}
