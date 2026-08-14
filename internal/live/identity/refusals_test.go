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
	// A SET, not a count. An audit defeated the count version by blanking
	// duplicate-identity's DocsRef and moving it onto an unrelated refusal:
	// documentation removed from a real refusal, parked somewhere it did not
	// belong, total unchanged, test green. Pinning the membership makes both
	// directions of that move visible.
	undocumented := map[string]bool{
		"Circular for_each reference":                                   true,
		"Circular identity reference":                                   true,
		"Configuration loaded without a static evaluator":               true,
		"Expression not evaluable here":                                 true,
		"Identity argument not set":                                     true,
		"Identity derived from a sensitive value":                       true,
		"Identity derived from an impure function":                      true,
		"Identity not resolvable from configuration":                    true,
		"Identity table and provider schema disagree":                   true,
		"Invalid count":                                                 true,
		"Invalid for_each set":                                          true,
		"Invalid for_each value":                                        true,
		"No configuration to resolve":                                   true,
		"No configuration to scan":                                      true,
		"Non-static count expression":                                   true,
		"Non-static for_each expression":                                true,
		"Non-static identity argument":                                  true,
		"Non-static lifecycle.enabled expression":                       true,
		"Non-string identity argument":                                  true,
		"Not an identity attribute":                                     true,
		"Null identity argument":                                        true,
		"Reference to a module instance that does not exist":            true,
		"Reference to a resource instance that does not exist":          true,
		"Reference to undeclared resource":                              true,
		"Sensitive count expression":                                    true,
		"Sensitive for_each expression":                                 true,
		"Sensitive lifecycle.enabled expression":                        true,
		"The identity table names something the provider does not have": true,
		"Unresolvable identity":                                         true,
		"Unsupported each.value reference":                              true,
		"for_each over a resource that is not keyed":                    true,
	}

	for _, r := range UndocumentedRefusals() {
		if !undocumented[r.Summary] {
			t.Errorf("%q lost its DocsRef. Documentation is not removed from a refusal by accident - if this is deliberate, take it out of this test's set.", r.Summary)
		}
		delete(undocumented, r.Summary)
	}
	for s := range undocumented {
		t.Errorf("%q is documented now - remove it from this test's set to lock the improvement in", s)
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
