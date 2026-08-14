// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/docsref"
	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestEveryRuleConstantIsRegistered closes the gap [Rules] documents on
// itself: it returns ruleInfo's keys, so a Rule constant declared with no
// ruleInfo entry was absent from the enumeration and nothing noticed.
//
// That is not hypothetical. internal/live/identity's refusals.go records an
// audit that added a Rule with neither a ruleInfo entry nor a resolvable
// docsRef and watched this package pass. [Rule.Summary] and [Rule.DocsRef]
// both fall back to a generated string for an unregistered rule, so such a
// rule fires, reaches a user, and carries the placeholder "Live-markers rule
// <name>" as its whole explanation - while every instrument built on [Rules]
// reports it does not exist.
//
// The scan is over this package's own const declarations of type Rule.
func TestEveryRuleConstantIsRegistered(t *testing.T) {
	declared := ruleConstantsInPackage(t)
	if len(declared) == 0 {
		t.Fatal("scanned the package and found no Rule constants; the scanner has stopped working")
	}

	enumerated := map[Rule]bool{}
	for _, r := range Rules() {
		enumerated[r] = true
	}

	for _, name := range declared {
		rule := Rule(name.value)
		if !enumerated[rule] {
			t.Errorf("%s declares the rule %q and ruleInfo has no entry for it.\n"+
				"It would fire with the placeholder summary %q and no docs reference, and Rules() would report it does not exist.",
				name.constName, name.value, rule.Summary())
			continue
		}
		if info := ruleInfo[rule]; info.summary == "" || info.docsRef == "" {
			t.Errorf("%s (%q) has a ruleInfo entry with an empty summary or docsRef", name.constName, name.value)
		}
	}

	declaredValues := map[Rule]bool{}
	for _, name := range declared {
		declaredValues[Rule(name.value)] = true
	}
	for rule := range enumerated {
		if !declaredValues[rule] {
			t.Errorf("ruleInfo lists %q, but no Rule constant in this package has that value. Remove the entry, or the constant was renamed without updating it.", rule)
		}
	}
}

// TestEveryRuleDocsRefIsResolvable is GitHub issue #110's third acceptance
// criterion for the lint half: a rule whose docsRef names a document and a
// heading has to name one that exists.
//
// The four rules that cited a GitHub issue rather than a shipped document
// are exactly what this catches. An operator cannot read the tracker to find
// out whether their configuration can move.
func TestEveryRuleDocsRefIsResolvable(t *testing.T) {
	for _, rule := range Rules() {
		ref := rule.DocsRef()
		if ref == "" {
			t.Errorf("rule %q has no docsRef", rule)
			continue
		}
		if strings.HasPrefix(ref, "GitHub issue") {
			t.Errorf("rule %q cites %s. A tracker entry is not a shipped document: a user deciding whether their configuration can move to live markers reads live/, not the issue tracker.", rule, ref)
			continue
		}
		parsed, err := docsref.Parse(ref)
		if err != nil {
			t.Errorf("rule %q: %s", rule, err)
			continue
		}
		if err := parsed.Resolve(flocitest.RepoRoot(t)); err != nil {
			t.Errorf("rule %q: %s", rule, err)
		}
	}
}

// ruleConstant is one `X Rule = "y"` declaration.
type ruleConstant struct {
	constName string
	value     string
}

// ruleConstantsInPackage parses this package's non-test source and returns
// every constant declared with type Rule.
func ruleConstantsInPackage(t *testing.T) []ruleConstant {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %s", err)
	}

	var out []ruleConstant
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				decl, ok := n.(*ast.GenDecl)
				if !ok || decl.Tok != token.CONST {
					return true
				}
				// A const block carries its type on the first spec that
				// names one and every later spec inherits it, so track the
				// last type seen the way the compiler does.
				var lastType string
				for _, spec := range decl.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if ident, ok := vs.Type.(*ast.Ident); ok {
						lastType = ident.Name
					}
					if lastType != "Rule" {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						if s, ok := constStringLit(vs.Values[i]); ok {
							out = append(out, ruleConstant{constName: name.Name, value: s})
						} else {
							t.Errorf("%s: the Rule constant %s is not a string literal, so this scanner cannot see its value",
								fset.Position(vs.Values[i].Pos()), name.Name)
						}
					}
				}
				return true
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].constName < out[j].constName })
	return out
}

func constStringLit(e ast.Expr) (string, bool) {
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
