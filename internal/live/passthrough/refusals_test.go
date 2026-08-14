// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package passthrough

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
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

// refParserSources is internal/addrs' reference parser, reached through
// lang.References before any evaluation happens. Same argument as above: one
// file of a large package.
var refParserSources = []string{"../../addrs/parse_ref.go"}

// hclEvalSources are HCL's own expression-evaluation files, relative to the
// module root, whose diagnostics reach a user through
// identity/resolve.go's evalPure calling expr.Value.
//
// Naming them is what turned this half of the registry from a claim into a
// check. The package doc used to argue that HCL's diagnostic set was "not
// ours to enumerate", so these entries rested on a sweep of the
// configurations that happen to be in the tree. An adversarial audit wrote
// fourteen three-line configurations reaching fourteen unregistered
// refusals, which is what that argument was worth.
//
// The set is enumerable after all, as long as the enumeration is scoped to
// evaluation rather than to the whole library: a parse error never reaches
// here, because a configuration that will not parse fails at load. These six
// files are the evaluation path. An HCL bump adding a diagnostic to one of
// them fails this test, which is the right outcome - somebody should decide
// what it means for a live run.
var hclEvalSources = []string{
	"ops.go",
	"traversal.go",
	"hclsyntax/expression.go",
	"hclsyntax/expression_ops.go",
	"hclsyntax/expression_template.go",
	"hclsyntax/expression_vars.go",
}

// hclModuleDir asks the go tool where HCL is unpacked. It needs the module
// cache, which any checkout that can build has.
func hclModuleDir(t *testing.T) string {
	t.Helper()

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/hashicorp/hcl/v2").Output()
	if err != nil {
		t.Fatalf("locating the hcl module: %s", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatal("the go tool reported no directory for the hcl module")
	}
	return dir
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
	hclDir := hclModuleDir(t)
	hclFiles := make([]string, len(hclEvalSources))
	for i, rel := range hclEvalSources {
		hclFiles[i] = filepath.Join(hclDir, rel)
	}

	// One scan per origin. Each is checked in both directions: every
	// summary the sources raise is registered, and every entry claiming
	// that origin is raised there.
	for _, source := range []struct {
		origin Origin
		files  []string
	}{
		{OriginConfigs, staticEvalSources},
		{OriginAddrs, refParserSources},
		{OriginHCL, hclFiles},
	} {
		t.Run(string(source.origin), func(t *testing.T) {
			var summaries, elsewhere []string
			whats := map[string]string{}
			for _, r := range Refusals() {
				summaries = append(summaries, r.Summary)
				whats[r.Summary] = r.What
				if r.Origin != source.origin {
					elsewhere = append(elsewhere, r.Summary)
				}
			}
			refusalscan.Check(t, refusalscan.Params{
				Files:      source.files,
				Registered: summaries,
				What:       whats,
				// An entry belonging to another origin has no call site
				// here by construction; its own subtest covers it.
				AllowUnproduced: elsewhere,
			})

			// The origin field has to be accurate, because it is what a
			// reader uses to tell whose diagnostic they are looking at.
			raised := map[string]bool{}
			for _, s := range refusalscan.Summaries(t, refusalscan.Params{Files: source.files}) {
				raised[s] = true
			}
			for _, r := range Refusals() {
				switch {
				case r.Origin == source.origin && !raised[r.Summary]:
					t.Errorf("%q is registered with origin %q but no site in those sources produces it", r.Summary, r.Origin)
				case r.Origin != source.origin && raised[r.Summary]:
					t.Errorf("%q is raised in %q's sources but registered with origin %q", r.Summary, source.origin, r.Origin)
				}
			}
		})
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
