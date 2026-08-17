// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// This file is why [LayerDiscovery] is in [UncheckedLayers] and not in
// [PartiallyCheckedLayers], which is not the obvious answer and was proposed
// the other way round in GitHub issue #261.
//
// [discovery.DeclaredDiagnostics] is exported, takes no provider handle, and
// says in its own doc comment that it exists "so a caller with no provider
// handle, such as an offline check, can still run the part of discovery that
// needs none". It has never had a caller. The projection stage was in exactly
// that state and wiring it was right, so the symmetric move looks right here
// too. It is not, and the reason is not visible from discovery's side.
//
// Of the four diagnostics the declared scan can raise:
//
//   - "No configuration to discover against" is DeclaredDiagnostics' own nil
//     guard. [Analyze] returns before it could fire.
//   - "Resolved resource missing from the configuration" says the resolutions
//     and the configuration came from different runs. [Analyze] computes the
//     resolutions from the configuration it was handed, in the same call.
//     Its own Detail calls it "a bug in whatever assembled them".
//   - "One marker value for two declared addresses" needs two declared
//     addresses escaping to one marker value. markerkey excludes the runes
//     that would make EscapeAddress lossy ("[", "]", the quote, and the
//     backslash), issue #178 escapes a key's own ".", ":" and "@" reversibly,
//     and a block carries count or for_each but never both - so escaping is
//     injective over everything identity resolves, and discovery's own
//     comment already calls the case "impossible in practice within one
//     block". TestDeclaredScanRaisesNothingLintDoesNot is the measurement
//     rather than the argument.
//   - "Address too long to carry an ownership marker" measures
//     RuneCount(EscapeAddress(addr)) against markers.MaxAddressLen, which is
//     the identical quantity [lint.RuleOverlongAddress] measures - in a layer
//     [Analyze] already runs in full. See TestLintCoversTheDeclaredScan.
//
// So the pass computes nothing [Analyze] does not already compute, and moving
// discovery out of the unchecked list on the strength of it would empty that
// list entirely. Twenty-one of discovery's twenty-five refusals still need a
// cloud. The narrow claim has to stay legible, so the stage stays named.
//
// This paragraph used to carry a second reason: that "choudoufu live-check"
// and tools/corpus-gen would both render the emptied list as "Not checked: ."
// followed by a sentence about stages that need a cloud. That was true and is
// no longer - both renderers now drop the sentence when there is nothing to
// name, under test (TestNotCheckedLineIsNotPrintedWhenNothingIsUnchecked in
// internal/command/views). It was a reason not to empty the list by accident,
// never a reason not to empty it; leaving it here would have made a fixed
// defect look like a standing argument. The reason above is the one that
// stands.
//
// If lint's rule is ever narrowed, or discovery's declared scan grows a
// refusal that is genuinely its own, these tests go red and the decision is
// worth taking again.

// overlongFixture is one way a resource block expands, pushed past
// markers.MaxAddressLen, with the site arithmetic pinned.
//
// The three counts are the finding, not decoration. lintSites is what
// [lint.RuleOverlongAddress] reports (once per block, at the count or
// for_each expression's range, or at the block's own DeclRange when there is
// neither). scanSites is what discovery's declared scan would add (once per
// expanded instance, always at the block's DeclRange). dedupedSites is how
// many of those [Analyze]'s existing refusedByLint dedupe would absorb, which
// it can only do where the two subjects coincide.
type overlongFixture struct {
	name         string
	lintSites    int
	scanSites    int
	dedupedSites int
	hcl          string
}

// overlongFixtures are the three ways a resource block expands, each pushed
// past markers.MaxAddressLen. The shapes are separate cases because lint and
// discovery report them at different subjects and different site counts, and
// that difference is half the finding.
func overlongFixtures() []overlongFixture {
	return []overlongFixture{
		{"no expansion", 1, 1, 1, `
resource "aws_vpc" "` + strings.Repeat("x", 1100) + `" {
  cidr_block = "10.0.0.0/16"
}
`},
		{"for_each", 1, 1, 0, `
locals {
  keys = { "` + strings.Repeat("k", 1100) + `" = "10.0.0.0/16" }
}
resource "aws_vpc" "v" {
  for_each   = local.keys
  cidr_block = each.value
}
`},
		{"count", 1, 12, 0, `
resource "aws_vpc" "` + strings.Repeat("y", 1015) + `" {
  count      = 12
  cidr_block = "10.0.0.0/16"
}
`},
	}
}

// TestLintCoversTheDeclaredScan is the load-bearing half of #261's refusal:
// every configuration whose declared scan raises the overlong-address
// diagnostic is a configuration lint has already refused under
// [lint.RuleOverlongAddress].
//
// The external source is lint itself, run over the same fixture in the same
// call. Nothing here consults a rule of this test's own devising: if lint's
// coverage ever became narrower than discovery's, a fixture would raise the
// discovery diagnostic with no lint issue beside it and this fails.
func TestLintCoversTheDeclaredScan(t *testing.T) {
	const overlongSummary = "Address too long to carry an ownership marker"

	for _, tc := range overlongFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFixture(t, tc.hcl)
			load := Load(t.Context(), dir)
			if load.Config == nil {
				t.Fatalf("fixture did not load: %v", load.Diags)
			}

			report := Analyze(t.Context(), load.Config, Context{})
			declared := declaredScanSummaries(t.Context(), load.Config, report)
			if declared[overlongSummary] == 0 {
				t.Fatalf("the declared scan raised no %q for this fixture, so it no longer exercises the case it was written for; summaries seen: %v",
					overlongSummary, declared)
			}

			var lintSites int
			for _, issue := range lint.CheckWith(t.Context(), load.Config, lint.Context{}) {
				if issue.Rule == lint.RuleOverlongAddress {
					lintSites++
				}
			}
			if lintSites == 0 {
				t.Errorf("the declared scan refuses this configuration (%d site(s)) and lint does not; "+
					"discovery would then be seeing something no checked layer sees, and wiring it into Analyze "+
					"(GitHub issue #261) becomes the right call after all",
					declared[overlongSummary])
			}
		})
	}
}

// TestDeclaredScanWouldOnlyDoubleCount is the other half, and it is the
// reason the wiring is not merely redundant but harmful.
//
// A finding's site count is what ranks it in live/corpus-refusals.json.
// [Analyze] already carries a dedupe - refusedByLint - that drops an identity
// diagnostic landing on a construct lint refused, and it keys on the
// diagnostic's source position. Discovery's declared scan reports at the
// resource block's DeclRange, once per expanded instance; lint reports at the
// count or for_each expression's range, once per block. So the dedupe catches
// the unexpanded case and nothing else, and a single overlong count block
// would be counted once by lint and again by every one of its instances.
func TestDeclaredScanWouldOnlyDoubleCount(t *testing.T) {
	for _, tc := range overlongFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFixture(t, tc.hcl)
			load := Load(t.Context(), dir)
			if load.Config == nil {
				t.Fatalf("fixture did not load: %v", load.Diags)
			}
			report := Analyze(t.Context(), load.Config, Context{})

			lintLocs := map[string]bool{}
			var lintSites int
			for _, f := range report.Findings {
				if f.Layer != LayerLint || f.ID != string(lint.RuleOverlongAddress) {
					continue
				}
				for _, site := range f.Sites {
					lintSites++
					if loc := site.location(); loc != "" {
						lintLocs[loc] = true
					}
				}
			}
			if lintSites == 0 {
				t.Fatalf("no lint overlong-address finding; TestLintCoversTheDeclaredScan covers that case and this one assumes it")
			}

			// The sites Analyze WOULD have added, and how many of them its
			// existing dedupe would have caught.
			added, deduped := 0, 0
			for _, diag := range discovery.DeclaredDiagnostics(t.Context(), discovery.Request{
				Config:      load.Config,
				Resolutions: report.Identities,
			}) {
				added++
				if src := diag.Source(); src.Subject != nil {
					site := Site{
						File:   src.Subject.Filename,
						Line:   src.Subject.Start.Line,
						Column: src.Subject.Start.Column,
					}
					if lintLocs[site.location()] {
						deduped++
					}
				}
			}
			t.Logf("lint reports %d site(s); the declared scan would add %d, of which the existing dedupe catches %d",
				lintSites, added, deduped)

			// Pinned rather than asserted as an inequality, because the
			// interesting failure is in either direction. "no expansion" is
			// the shape where the dedupe DOES absorb everything - if it were
			// the only shape, the wiring would cost nothing and #261 would
			// come down to whether a redundant pass is worth its noise. It is
			// not the only shape: for_each escapes the dedupe because lint
			// reports at the for_each expression and discovery at the block,
			// and count escapes it twelve times over for a block lint counts
			// once. If any of these three numbers moves, the ranking cost of
			// the wiring moved with it and the decision is worth retaking.
			if lintSites != tc.lintSites || added != tc.scanSites || deduped != tc.dedupedSites {
				t.Errorf("site arithmetic moved: lint %d (pinned %d), scan %d (pinned %d), deduped %d (pinned %d)",
					lintSites, tc.lintSites, added, tc.scanSites, deduped, tc.dedupedSites)
			}
		})
	}
}

// TestDeclaredScanRaisesNothingLintDoesNot sweeps every configuration
// directory in the repository and asserts that the only thing the declared
// scan raises is the overlong-address diagnostic, in directories lint refuses
// under its own rule.
//
// It is the empirical form of the reasoning above: not "we argued that the
// collision refusal is unreachable" but "over every fixture this repository
// carries, the pass Analyze would have run raised one summary, in one
// directory, and that directory is internal/live/lint/testdata/overlong-address
// - the fixture for the lint rule that already covers it".
//
// Measured at the commit this landed on: 269 directories and 1335 resolutions
// reached the scan. Over the 250-entry corpus (not swept here, because .corpus
// is a gitignored symlink and a bound that needs it is not a bound), the same
// instrumentation counted 221 directories, 3041 resolutions of which 1403 were
// ClassNeedsDiscovery, a longest escaped address of 99 runes against the
// 1024-rune ceiling, and zero diagnostics.
//
// The sweep reuses the identity golden's roots and walker, so it covers the
// same set.
func TestDeclaredScanRaisesNothingLintDoesNot(t *testing.T) {
	root := flocitest.RepoRoot(t)
	dirs := identityGoldenDirs(t, root)
	if len(dirs) < 300 {
		t.Fatalf("found only %d configuration directories under %v; the walk is not reaching the tree it claims to cover",
			len(dirs), identityGoldenRoots)
	}

	const overlongSummary = "Address too long to carry an ownership marker"

	var (
		reached     int
		resolutions int
		raising     int
	)
	for _, dir := range dirs {
		load := Load(t.Context(), dir)
		if load.Config == nil {
			continue
		}
		report := Analyze(t.Context(), load.Config, Context{})
		if len(report.Identities) == 0 {
			continue
		}
		reached++
		resolutions += len(report.Identities)

		raised := declaredScanSummaries(t.Context(), load.Config, report)
		if len(raised) == 0 {
			continue
		}
		raising++
		for summary, n := range raised {
			if summary != overlongSummary {
				t.Errorf("%s: the declared scan raised %q (%d site(s)), which no checked layer computes; "+
					"discovery has something to contribute offline after all (GitHub issue #261)",
					rel(root, dir), summary, n)
			}
		}
		// The covering claim, per directory and against lint itself rather
		// than against a rule of this test's own.
		var lintSites int
		for _, issue := range lint.CheckWith(t.Context(), load.Config, lint.Context{}) {
			if issue.Rule == lint.RuleOverlongAddress {
				lintSites++
			}
		}
		if lintSites == 0 {
			t.Errorf("%s: the declared scan refuses it (%v) and lint does not", rel(root, dir), raised)
		}
	}

	// A zero is also what a dead code path looks like, so the reach is
	// asserted rather than assumed: these are the resolutions the scan was
	// handed, and it indexed every one of them.
	if reached < 100 || resolutions < 500 {
		t.Fatalf("the declared scan ran over only %d directories and %d resolutions; a result from a sweep this "+
			"small is the absence of evidence, not evidence of absence", reached, resolutions)
	}
	t.Logf("declared scan run over %d directories and %d resolutions; %d directories raised anything",
		reached, resolutions, raising)

	// The one directory that raises is lint's own fixture for the rule. If a
	// second appears, somebody wrote a case worth looking at, and the count
	// moving is how they find out.
	if raising != 1 {
		t.Errorf("%d directories raised a declared-scan diagnostic, pinned at 1 (internal/live/lint/testdata/overlong-address); "+
			"read the directory before re-pinning", raising)
	}
}

// declaredScanSummaries runs the provider-free declared scan the way Analyze
// would have to and returns its diagnostics by summary.
//
// ScopeProvider is left zero on purpose, and it is the one field worth
// justifying: discovery's inScope returns true for every block when
// scope.Provider.Type is empty, so one unscoped pass covers a configuration
// using several provider configurations, which is what an offline check
// wants. Estate, Region and Provider are untouched - declaredInstances reads
// Config, Resolutions and ScopeProvider and nothing else.
func declaredScanSummaries(ctx context.Context, cfg *configs.Config, report Report) map[string]int {
	out := map[string]int{}
	for _, diag := range discovery.DeclaredDiagnostics(ctx, discovery.Request{
		Config:      cfg,
		Resolutions: report.Identities,
	}) {
		out[diag.Description().Summary]++
	}
	return out
}

func writeFixture(t *testing.T, hcl string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}
