// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package refusalscan is the lockstep check behind GitHub issue #110's first
// acceptance criterion: a refusal cannot exist in a live package without an
// entry in that package's registry.
//
// It parses Go source, collects every diagnostic Summary the code can
// produce, and compares that set against the registry the caller passes in.
// Both directions fail: a refusal with no entry, and an entry no call site
// produces.
//
// The scanner lives here rather than in each package's own test because four
// packages need it - internal/live/identity, internal/live/lint's sibling
// check, internal/live/stamp and internal/live/discovery - and because one of
// them has to reach across a package boundary into internal/configs, which a
// package-local scanner cannot do. Four copies of a scanner is four places
// for the scanner to quietly stop working, and a scanner that has stopped
// working reports that everything is registered.
//
// # What it can and cannot see
//
// Three shapes of summary are found:
//
//   - hcl.Diagnostic{Summary: "..."} and any other composite literal with a
//     Summary field set to a string literal.
//   - A call to a method named errorf whose second argument is a string
//     literal, which is internal/live/identity's resolver idiom.
//   - A package-level constant whose name starts with "Summary".
//
// The third exists because a summary that reaches a diagnostic through a
// variable or a parameter is invisible to the first two. That is not
// hypothetical: internal/live/identity's schema_verify.go escaped its own
// registry that way, and internal/live/stamp raises two of its four summaries
// through a parameter. Declaring them as Summary-prefixed constants is the
// convention that keeps them findable, and [Check] reports a non-literal it
// cannot resolve rather than skipping it.
package refusalscan

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

// Params configures one package's check.
type Params struct {
	// Dir is the package directory to scan, relative to the test's own
	// working directory. Mutually exclusive with Files.
	Dir string

	// Files are specific source files to scan, for the case where the
	// refusals belong to another package and only part of it is reachable.
	// Mutually exclusive with Dir.
	Files []string

	// SkipFile is a base filename inside Dir to leave out, normally the
	// registry itself: it holds the summaries as data, not as call sites,
	// and scanning it would make every entry justify itself.
	SkipFile string

	// Registered is every Summary the registry lists.
	Registered []string

	// What maps a Summary to its description, checked non-empty. A registry
	// entry that describes nothing cannot be rendered into a document, which
	// is what the registry exists for.
	What map[string]string

	// AllowUnproduced are registry entries deliberately kept without a call
	// site in the scanned source. Empty for every caller today; the field
	// exists so that a legitimate case is written down rather than handled
	// by deleting the assertion.
	AllowUnproduced []string
}

// Check runs the scan and reports every mismatch.
func Check(t *testing.T, p Params) {
	t.Helper()

	found := Summaries(t, p)
	if len(found) == 0 {
		t.Fatal("scanned the source and found no diagnostic summaries; the scanner has stopped working")
	}

	registered := map[string]bool{}
	for _, s := range p.Registered {
		if s == "" {
			t.Error("registry entry with an empty Summary")
			continue
		}
		if registered[s] {
			t.Errorf("registry lists %q twice", s)
		}
		registered[s] = true
		if p.What != nil && strings.TrimSpace(p.What[s]) == "" {
			t.Errorf("registry entry %q has no description; the whole point is that it describes itself", s)
		}
	}

	for _, s := range found {
		if !registered[s] {
			t.Errorf("this package can produce the refusal %q and its registry does not list it.\n"+
				"Add an entry describing what triggers it, so it can be documented rather than discovered by an operator.", s)
		}
	}

	inSource := map[string]bool{}
	for _, s := range found {
		inSource[s] = true
	}
	allowed := map[string]bool{}
	for _, s := range p.AllowUnproduced {
		allowed[s] = true
	}
	for s := range registered {
		if !inSource[s] && !allowed[s] {
			t.Errorf("the registry lists %q, but no call site in the scanned source produces it. Remove the entry, or the refusal was renamed without updating it.", s)
		}
	}
}

// Summaries returns every diagnostic summary the scanned source can produce,
// sorted. It is exported for a caller that wants the set rather than the
// comparison.
func Summaries(t *testing.T, p Params) []string {
	t.Helper()

	fset := token.NewFileSet()
	var files []*ast.File

	switch {
	case p.Dir != "" && len(p.Files) > 0:
		t.Fatal("refusalscan: set Dir or Files, not both")
	case p.Dir != "":
		pkgs, err := parser.ParseDir(fset, p.Dir, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("parsing %s: %s", p.Dir, err)
		}
		for _, pkg := range pkgs {
			for name, file := range pkg.Files {
				if p.SkipFile != "" && filepath.Base(name) == p.SkipFile {
					continue
				}
				files = append(files, file)
			}
		}
	case len(p.Files) > 0:
		for _, rel := range p.Files {
			file, err := parser.ParseFile(fset, filepath.Clean(rel), nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %s", rel, err)
			}
			files = append(files, file)
		}
	default:
		t.Fatal("refusalscan: neither Dir nor Files was set")
	}

	seen := map[string]bool{}
	var nonLiteral []string
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GenDecl:
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
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "errorf" || len(node.Args) < 2 {
					return true
				}
				if s, ok := stringLit(node.Args[1]); ok {
					seen[s] = true
				} else if !isSummaryConstRef(node.Args[1]) {
					nonLiteral = append(nonLiteral, fset.Position(node.Args[1].Pos()).String())
				}
			case *ast.KeyValueExpr:
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

	for _, pos := range nonLiteral {
		t.Errorf("%s: the summary is not a string literal and does not name a Summary-prefixed constant, so this scanner cannot see it and the registry cannot cover it.", pos)
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// isSummaryConstRef reports whether an expression names a Summary-prefixed
// identifier, which the constant case above has already recorded by value.
func isSummaryConstRef(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return strings.HasPrefix(v.Name, "Summary")
	case *ast.SelectorExpr:
		return strings.HasPrefix(v.Sel.Name, "Summary")
	}
	return false
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
