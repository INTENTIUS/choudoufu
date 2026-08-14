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
// Four shapes of summary are found:
//
//   - hcl.Diagnostic{Summary: "..."} and any other composite literal with a
//     Summary field set to a string literal.
//   - The second argument of a call to tfdiags.Sourceless, or to any method
//     whose name ends in "errorf". Those are the two positional idioms this
//     repository builds diagnostics with.
//   - A package-level constant whose name starts with "Summary".
//   - The string values of a package-level map named in [Params.SummaryMaps],
//     for the one case where a summary is chosen by table lookup.
//
// The third and fourth exist because a summary reaching a diagnostic through
// a variable or a parameter is invisible to the first two.
//
// # Everything else is an error, not a skip
//
// The first version of this scanner recorded what it recognised and silently
// ignored what it did not. An audit defeated it in four ways in one sitting:
// a summary built with fmt.Sprintf, a summary read out of a local variable,
// a summary taken from a struct field, and a new helper method named
// refusef alongside the errorf the scanner hardcoded. All four added a
// refusal a user can hit, and all four left the suite green - the scanner
// reporting that everything was registered precisely because it could see
// nothing.
//
// So a summary position holding anything this scanner cannot resolve to a
// literal now fails. The fix at such a site is to name the string: a
// Summary-prefixed package constant, or an entry in a declared summary map.
// That is a constraint on how diagnostics are written, and it is the only
// version of this check that means anything.
package refusalscan

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
	// site in the scanned source, so that a legitimate case is written down
	// rather than handled by deleting the assertion.
	AllowUnproduced []string

	// SummaryMaps are package-level maps whose string values are summaries,
	// by variable name. internal/live/discovery picks a summary out of
	// problemSummaries by ProblemKind, which is a reasonable way to write
	// fourteen related diagnostics and invisible to a scan of literals.
	//
	// Naming the map here is what makes it visible. It is a list rather
	// than a convention on the name so that adding one is a decision
	// somebody takes.
	SummaryMaps []string

	// DynamicSites are functions in which a summary reaches a diagnostic
	// through a variable, named so the exemption is written down.
	//
	// There is exactly one legitimate shape: a function that looks a
	// summary up in a [Params.SummaryMaps] map and passes the result on.
	// Every value of that map is already required to be registered, so the
	// call site adds nothing. Anything else on this list is a hole, and it
	// is a hole with a name on it.
	DynamicSites []string
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

	summaryMaps := map[string]bool{}
	for _, name := range p.SummaryMaps {
		summaryMaps[name] = true
	}
	dynamic := map[string]bool{}
	for _, name := range p.DynamicSites {
		dynamic[name] = true
	}

	// Pass one: every constant string in the scanned files, by name.
	//
	// Upstream code does not follow this repository's Summary-prefixed
	// naming convention and has no reason to: HCL's ops.go declares
	// `const invalidIndex = "Invalid index"` inside the function that uses
	// it. Resolving constants by value rather than by name is what lets the
	// same scanner read our source and theirs.
	consts := map[string]string{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			decl, ok := n.(*ast.GenDecl)
			if !ok || decl.Tok != token.CONST {
				return true
			}
			for _, spec := range decl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if s, ok := stringLit(vs.Values[i]); ok {
						consts[name.Name] = s
					}
				}
			}
			return true
		})
	}

	seen := map[string]bool{}
	var unresolved []string
	note := func(where ast.Node, what string) {
		unresolved = append(unresolved, fset.Position(where.Pos()).String()+": "+what)
	}
	// resolve reads a summary expression, recording it if it can.
	resolve := func(e ast.Expr) bool {
		if s, ok := stringLit(e); ok {
			seen[s] = true
			return true
		}
		if ident, ok := e.(*ast.Ident); ok {
			if s, ok := consts[ident.Name]; ok {
				seen[s] = true
				return true
			}
		}
		// A Summary-prefixed name declared in a file this scan skips - the
		// registry file itself, or another package - is recorded there
		// rather than here.
		return isSummaryConstRef(e) || isSummaryMapRef(e, summaryMaps)
	}

	for _, file := range files {
		// enclosing tracks the function a node sits in, so that
		// DynamicSites can exempt one by name.
		var enclosing string
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				enclosing = node.Name.Name

			case *ast.GenDecl:
				collectDecl(node, summaryMaps, seen, note)

			case *ast.CallExpr:
				name, ok := calleeName(node.Fun)
				if !ok || !isDiagConstructor(name) || len(node.Args) < 2 {
					return true
				}
				if resolve(node.Args[1]) {
					return true
				}
				if dynamic[enclosing] {
					return true
				}
				note(node.Args[1], fmt.Sprintf(
					"%s's summary argument is neither a string literal nor a Summary-prefixed constant, so this scanner cannot see it", name))

			case *ast.KeyValueExpr:
				key, ok := node.Key.(*ast.Ident)
				if !ok || key.Name != "Summary" {
					return true
				}
				if resolve(node.Value) {
					return true
				}
				if dynamic[enclosing] {
					return true
				}
				note(node.Value, "a diagnostic's Summary field is neither a string literal nor a Summary-prefixed constant, so this scanner cannot see it")
			}
			return true
		})
	}

	for _, msg := range unresolved {
		t.Errorf("%s.\nName the string instead: a Summary-prefixed package constant, or an entry in a map declared in this check's SummaryMaps. "+
			"A summary this scanner cannot read is a refusal the registry cannot cover, and the scanner would otherwise report that everything is registered.", msg)
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// collectDecl records summaries declared as constants or as the values of a
// declared summary map.
func collectDecl(decl *ast.GenDecl, summaryMaps map[string]bool, seen map[string]bool, note func(ast.Node, string)) {
	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			switch {
			case decl.Tok == token.CONST && strings.HasPrefix(name.Name, "Summary"):
				if s, ok := stringLit(vs.Values[i]); ok {
					seen[s] = true
					continue
				}
				// A Summary-prefixed constant assembled from other
				// constants reads as declared and is not: every call site
				// referring to it passes the isSummaryConstRef check, so
				// nothing else would notice.
				note(vs.Values[i], fmt.Sprintf("the constant %s is not a plain string literal, so its value cannot be registered", name.Name))

			case decl.Tok == token.VAR && summaryMaps[name.Name]:
				lit, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					note(vs.Values[i], fmt.Sprintf("the summary map %s is not a composite literal", name.Name))
					continue
				}
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						note(elt, fmt.Sprintf("an entry of the summary map %s is not a key/value pair", name.Name))
						continue
					}
					if s, ok := stringLit(kv.Value); ok {
						seen[s] = true
						continue
					}
					note(kv.Value, fmt.Sprintf("an entry of the summary map %s is not a string literal", name.Name))
				}
			}
		}
	}
}

// isDiagConstructor reports whether a call builds a diagnostic with its
// summary as the second positional argument.
//
// "errorf" is matched as a suffix rather than exactly, which is what makes a
// sibling helper - refusef, warnf, an errorf on another receiver - covered
// by default instead of silently exempt. An audit added exactly that helper
// and watched the contract switch itself off.
//
// The suffix is lowercase on purpose. Matching "Errorf" too would sweep in
// every fmt.Errorf in the package, whose second argument is an ordinary
// format operand and never a summary.
func isDiagConstructor(name string) bool {
	return name == "Sourceless" || strings.HasSuffix(name, "errorf")
}

func calleeName(fun ast.Expr) (string, bool) {
	switch v := fun.(type) {
	case *ast.SelectorExpr:
		return v.Sel.Name, true
	case *ast.Ident:
		return v.Name, true
	}
	return "", false
}

// isSummaryMapRef reports whether an expression indexes a declared summary
// map, as problemSummaries[ProblemCollision] does. Every value of such a map
// is registered by [collectDecl], so the call site adds nothing.
func isSummaryMapRef(e ast.Expr, summaryMaps map[string]bool) bool {
	idx, ok := e.(*ast.IndexExpr)
	if !ok {
		return false
	}
	name, ok := idx.X.(*ast.Ident)
	return ok && summaryMaps[name.Name]
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
