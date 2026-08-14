// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package passthrough

import (
	"fmt"
	"testing"

	"github.com/intentius/choudoufu/internal/live/refusalscan"
)

// staticEvalSources are the internal/configs files whose diagnostics reach a
// live-markers user through identity resolution's static evaluation. They are
// named rather than globbed: internal/configs is a large package and almost
// none of it is reachable from here, so a glob would demand registry entries
// for decode errors a configuration has already failed on before this fork's
// live path runs at all.
var staticEvalSources = []string{
	"../../configs/static_scope.go",
	"../../configs/static_evaluator.go",
}

// TestConfigsRefusalsRegistered is the scan half of this package's
// completeness argument, and the reason [OriginConfigs] is the origin whose
// entries cannot silently grow.
//
// It parses the static-evaluation sources and requires every Summary literal
// in them to be registered here. That is the same contract
// internal/live/identity's TestRefusalsRegistered enforces on its own
// package, applied across a package boundary because the diagnostics are
// upstream's and the documentation obligation is ours.
func TestConfigsRefusalsRegistered(t *testing.T) {
	// Only the internal/configs half. The addrs and HCL entries have no
	// call site in these two files by construction, so they are declared
	// unproduced rather than deleted - see this package's doc comment for
	// why their completeness rests on a corpus sweep instead.
	var summaries, elsewhere []string
	whats := map[string]string{}
	for _, r := range Refusals() {
		summaries = append(summaries, r.Summary)
		whats[r.Summary] = r.What
		if r.Origin != OriginConfigs {
			elsewhere = append(elsewhere, r.Summary)
		}
	}
	refusalscan.Check(t, refusalscan.Params{
		Files:           staticEvalSources,
		Registered:      summaries,
		What:            whats,
		AllowUnproduced: elsewhere,
	})

	// The origin field has to be accurate, because it is what decides how
	// far this package's completeness claim reaches.
	inConfigs := map[string]bool{}
	for _, s := range refusalscan.Summaries(t, refusalscan.Params{Files: staticEvalSources}) {
		inConfigs[s] = true
	}
	for _, r := range Refusals() {
		switch {
		case r.Origin == OriginConfigs && !inConfigs[r.Summary]:
			t.Errorf("%q is registered with origin %q but no site in %v produces it", r.Summary, r.Origin, staticEvalSources)
		case r.Origin != OriginConfigs && inConfigs[r.Summary]:
			t.Errorf("%q is raised in %v but registered with origin %q; the origin decides how far the scan-enforced claim reaches, so it has to be accurate", r.Summary, staticEvalSources, r.Origin)
		}
	}
}

// TestEveryRefusalDescribesItself is the shape check the other two registries
// carry too: an entry with no What or no Summary is a row that cannot be
// rendered into a document, which defeats the whole point of the table.
func TestEveryRefusalDescribesItself(t *testing.T) {
	for _, r := range Refusals() {
		if r.Summary == "" {
			t.Error("registry entry with an empty Summary")
		}
		if r.What == "" {
			t.Errorf("registry entry %q has no What; the whole point is that it describes itself", r.Summary)
		}
		switch r.Origin {
		case OriginConfigs, OriginAddrs, OriginHCL:
		default:
			t.Errorf("registry entry %q has origin %q, which is not one of the three declared origins", r.Summary, r.Origin)
		}
	}
}

// TestDocsRefNamesTheRefusalsOwnHeading pins the derivation rather than the
// strings it produces. Whether the heading it names actually exists is
// internal/live/check's TestEveryRefusalDocsRefIsResolvable, which can read
// the document; this only checks that a reference is built at all and is
// built from the Summary.
func TestDocsRefNamesTheRefusalsOwnHeading(t *testing.T) {
	for _, r := range Refusals() {
		want := fmt.Sprintf("live/LIMITATIONS.md, %q", r.Summary)
		if got := r.DocsRef(); got != want {
			t.Errorf("%q: DocsRef() = %q, want %q", r.Summary, got, want)
		}
	}
}

// TestLookupRefusal covers the accessor the combined catalog uses.
func TestLookupRefusal(t *testing.T) {
	if _, ok := LookupRefusal("Unable to compute static value"); !ok {
		t.Error("the largest single blocker in the corpus is not findable by Summary")
	}
	if _, ok := LookupRefusal("no such refusal"); ok {
		t.Error("LookupRefusal invented an entry")
	}
}
