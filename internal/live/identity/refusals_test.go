// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
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
	var nonLiteral []string
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if filepath.Base(name) == "refusals.go" {
				// The registry itself is data, not call sites.
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.GenDecl:
					// Package-level `const SummaryX = "..."`. A summary that
					// reaches tfdiags.Sourceless through a variable is
					// invisible to the two literal cases below - which is how
					// schema_verify.go's two escaped this registry when it was
					// first written. The Summary-prefixed name is the
					// convention that makes them findable; keep it.
					if node.Tok != token.CONST {
						return true
					}
					for _, spec := range node.Specs {
						vs, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, name := range vs.Names {
							if !strings.HasPrefix(name.Name, "Summary") || i >= len(vs.Values) {
								continue
							}
							if s, ok := stringLit(vs.Values[i]); ok {
								seen[s] = true
							}
						}
					}
				case *ast.CallExpr:
					// r.errorf(rng, "Summary", "detail", ...)
					sel, ok := node.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "errorf" || len(node.Args) < 2 {
						return true
					}
					if s, ok := stringLit(node.Args[1]); ok {
						seen[s] = true
					} else {
						// Loud, not skipped. A non-literal summary is exactly
						// the shape that evades this scanner, so it fails here
						// rather than passing silently.
						nonLiteral = append(nonLiteral, fset.Position(node.Args[1].Pos()).String())
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

	for _, pos := range nonLiteral {
		t.Errorf("%s: errorf's summary is not a string literal, so this scanner cannot see it and the registry cannot cover it. Use a Summary-prefixed package constant instead.", pos)
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
