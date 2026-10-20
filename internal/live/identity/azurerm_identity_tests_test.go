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
	"strconv"
	"strings"
	"testing"
)

// readAzurermIdentityTests reads every resource identity test azurerm
// generates (internal/services/**/*resource_identity_gen_test.go), by
// resource type, for the identity extract (GitHub issue #2138). It is a
// second reader of the files tools/azurerm-id-gen's contract.go reads, and
// the pin it feeds is a self-consistency check of one generator run
// against azurerm's generated identity tests, not independent evidence
// (#2140): the identity tests are the generator's input as well as the
// pin's, so the pin catches the generator misreading them or the two
// readers drifting apart, and says nothing the tests themselves do not.
// In particular it says nothing about how azurerm parses an id argument at
// run time; substrate.ARMIDSegment's own tests and the parse the generator
// records from the argument's validator (parent_parse) carry that.
func readAzurermIdentityTests(t *testing.T, src string) map[string]map[string]azurermIdentityTestCheck {
	t.Helper()
	if src == "" {
		t.Fatal("-update-azurerm-identity-extract needs AZURERM_SRC, the azurerm checkout at the pinned tag")
	}
	out := map[string]map[string]azurermIdentityTestCheck{}
	root := filepath.Join(src, "internal", "services")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "resource_identity_gen_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}
			var check string
			switch sel.Sel.Name {
			case "ExpectIdentityValueMatchesStateAtPath":
				check = "matches-state"
			case "ExpectStateContainsIdentityValueAtPath":
				check = "contains"
			case "ExpectIdentityValue":
				check = "value"
			default:
				return true
			}
			addr, ok := azurermTestString(call.Args[0])
			if !ok {
				t.Fatalf("%s: a %s whose address is not a literal", path, sel.Sel.Name)
			}
			attr, ok := azurermTestJSONPath(call.Args[1])
			if !ok {
				t.Fatalf("%s: a %s whose identity path is not tfjsonpath.New(literal)", path, sel.Sel.Name)
			}
			typ, _, _ := strings.Cut(addr, ".")
			c := azurermIdentityTestCheck{Check: check}
			if check == "value" {
				inner, ok := call.Args[2].(*ast.CallExpr)
				if ok && len(inner.Args) == 1 {
					if s, ok := azurermTestString(inner.Args[0]); ok {
						c.String = s
					} else {
						c.Expr = azurermTestExpr(inner.Args[0])
					}
				} else {
					c.Expr = azurermTestExpr(call.Args[2])
				}
			} else if c.Path, ok = azurermTestJSONPath(call.Args[2]); !ok {
				c.Expr = azurermTestExpr(call.Args[2])
			}
			if out[typ] == nil {
				out[typ] = map[string]azurermIdentityTestCheck{}
			}
			if _, dup := out[typ][attr]; dup {
				t.Fatalf("%s: %s's identity attribute %s is checked twice", path, typ, attr)
			}
			out[typ][attr] = c
			return false
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func azurermTestString(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}

// azurermTestJSONPath reads tfjsonpath.New("x") as "x".
func azurermTestJSONPath(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "New" {
		return "", false
	}
	return azurermTestString(call.Args[0])
}

// azurermTestExpr renders a selector chain (data.Subscriptions.Primary),
// or the call it is the function of, as written; anything else as "?".
func azurermTestExpr(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return azurermTestExpr(x.X) + "." + x.Sel.Name
	case *ast.CallExpr:
		return azurermTestExpr(x.Fun) + "(...)"
	}
	return "?"
}
