// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
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
	found := summariesInPackage(t)
	if len(found) == 0 {
		t.Fatal("scanned the package and found no diagnostic summaries; the scanner has stopped working")
	}

	registered := map[string]bool{}
	for _, r := range Refusals() {
		if r.Summary == "" {
			t.Error("registry entry with an empty Summary")
		}
		if r.What == "" {
			t.Errorf("registry entry %q has no What; the whole point is that it describes itself", r.Summary)
		}
		if registered[r.Summary] {
			t.Errorf("registry lists %q twice", r.Summary)
		}
		registered[r.Summary] = true
	}

	for _, s := range found {
		if !registered[s] {
			t.Errorf("this package can produce the refusal %q and refusals.go does not list it.\n"+
				"Add an entry describing what configuration shape triggers it, so it can be documented rather than discovered by an operator.", s)
		}
	}

	inSource := map[string]bool{}
	for _, s := range found {
		inSource[s] = true
	}
	for s := range registered {
		if !inSource[s] {
			t.Errorf("refusals.go lists %q, but no call site in this package produces it. Remove the entry, or the refusal was renamed without updating it.", s)
		}
	}
}

// TestUndocumentedRefusalsAreCounted pins the documentation gap as a number
// rather than a vague impression, so that closing it is visible.
//
// This is deliberately a ratchet on the count and not an assertion that the
// gap is zero: it is 27 of 30 today, closing it is issue #110's second half
// (generating live/LIMITATIONS.md from this registry plus lint's), and the
// useful property in the meantime is that it cannot silently grow.
func TestUndocumentedRefusalsAreCounted(t *testing.T) {
	const ceiling = 27

	undocumented := UndocumentedRefusals()
	if len(undocumented) > ceiling {
		names := make([]string, 0, len(undocumented))
		for _, r := range undocumented {
			names = append(names, r.Summary)
		}
		t.Errorf("%d refusals have no DocsRef, over the ratchet of %d. A new refusal needs a live/ entry, or the ratchet needs raising deliberately.\n%s",
			len(undocumented), ceiling, strings.Join(names, "\n"))
	}
	if len(undocumented) < ceiling {
		t.Errorf("only %d refusals are undocumented, under the ratchet of %d - lower the ratchet in this test to lock the improvement in", len(undocumented), ceiling)
	}
}

// summariesInPackage parses every non-test .go file in this package's
// directory and returns the diagnostic summaries it can produce.
func summariesInPackage(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %s", err)
	}

	seen := map[string]bool{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if filepath.Base(name) == "refusals.go" {
				// The registry itself is data, not call sites.
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					// r.errorf(rng, "Summary", "detail", ...)
					sel, ok := node.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "errorf" || len(node.Args) < 2 {
						return true
					}
					if s, ok := stringLit(node.Args[1]); ok {
						seen[s] = true
					}
				case *ast.KeyValueExpr:
					// hcl.Diagnostic{ Summary: "..." }
					key, ok := node.Key.(*ast.Ident)
					if !ok || key.Name != "Summary" {
						return true
					}
					if s, ok := stringLit(node.Value); ok {
						seen[s] = true
					}
				}
				return true
			})
		}
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
