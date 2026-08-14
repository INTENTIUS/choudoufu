// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package passthrough

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
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
	found := summariesInFiles(t, staticEvalSources)
	if len(found) == 0 {
		t.Fatal("scanned the static-evaluation sources and found no diagnostic summaries; the scanner has stopped working")
	}

	registered := map[string]Refusal{}
	for _, r := range Refusals() {
		if _, dup := registered[r.Summary]; dup {
			t.Errorf("registry lists %q twice", r.Summary)
		}
		registered[r.Summary] = r
	}

	for _, s := range found {
		r, ok := registered[s]
		if !ok {
			t.Errorf("internal/configs' static evaluator can produce the refusal %q and this registry does not list it.\n"+
				"A user reaches it through identity resolution with no way to look it up. Add an entry describing the configuration shape that triggers it.", s)
			continue
		}
		if r.Origin != OriginConfigs {
			t.Errorf("%q is raised in %v but registered with origin %q; the origin decides how far this package's completeness claim reaches, so it has to be accurate", s, staticEvalSources, r.Origin)
		}
	}

	inSource := map[string]bool{}
	for _, s := range found {
		inSource[s] = true
	}
	for summary, r := range registered {
		if r.Origin == OriginConfigs && !inSource[summary] {
			t.Errorf("this registry lists %q with origin %q, but no site in %v produces it. Remove the entry, or it was renamed upstream without updating this.", summary, r.Origin, staticEvalSources)
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

// summariesInFiles parses the named files and returns every string literal
// assigned to a Summary field of a diagnostic literal.
func summariesInFiles(t *testing.T, files []string) []string {
	t.Helper()

	seen := map[string]bool{}
	var nonLiteral []string
	fset := token.NewFileSet()
	for _, rel := range files {
		file, err := parser.ParseFile(fset, filepath.Clean(rel), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %s", rel, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Summary" {
				return true
			}
			if s, ok := stringLit(kv.Value); ok {
				seen[s] = true
			} else {
				// Loud, not skipped, for the same reason identity's scanner
				// is: a summary assembled at runtime is exactly the shape
				// that evades this test.
				nonLiteral = append(nonLiteral, fset.Position(kv.Value.Pos()).String())
			}
			return true
		})
	}

	for _, pos := range nonLiteral {
		t.Errorf("%s: the diagnostic's Summary is not a string literal, so this scanner cannot see it and the registry cannot cover it", pos)
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}
