// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

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
)

// contract is azurerm's own statement of where each identity attribute of a
// type comes from (#1120 unit 17), read from the resource identity
// acceptance test the provider generates for every type that serves a
// resource identity schema (internal/tools/generator-tests resourceidentity,
// written to <type>_resource_identity_gen_test.go or, for the batch types,
// resources/<type>/resource_identity_gen_test.go). The test checks every
// identity field (customstatecheck.ExpectAllIdentityFieldsAreChecked), so
// the attributes recorded here are the whole identity.
//
// internal/live/substrate holds an identity-schema type's ARM id template to
// it before the template may stand in for the identity
// (azurermContractProblem): the template has to read each attribute from
// where the provider says it is, which is what makes a segment read out of a
// parent's ARM id argument exact rather than a guess from argument names.
type contract struct {
	// Source is the test file, relative to the checkout.
	Source     string                  `json:"source"`
	Attributes map[string]contractAttr `json:"attributes"`
}

// contractAttr is one identity attribute's check; exactly one field is set.
type contractAttr struct {
	// Argument: statecheck.ExpectIdentityValueMatchesStateAtPath, the
	// identity value is this argument's value.
	Argument string `json:"argument,omitempty"`
	// Within: customstatecheck.ExpectStateContainsIdentityValueAtPath, the
	// identity value is contained in this argument's value, an ARM id.
	Within string `json:"within,omitempty"`
	// ProviderSubscription: statecheck.ExpectIdentityValue against
	// data.Subscriptions.Primary or .Secondary, the subscription the test's
	// provider block targets (resource_provider_feature_registration's test
	// sets its provider block's subscription_id to the secondary one).
	ProviderSubscription bool `json:"provider_subscription,omitempty"`
	// Constant: statecheck.ExpectIdentityValue against a string literal.
	Constant string `json:"constant,omitempty"`
	// Other: any other check, as its expression reads.
	Other string `json:"other,omitempty"`
}

// readContracts reads every resource identity test under the checkout's
// internal/services, by resource type.
func readContracts(src string) (map[string]*contract, error) {
	out := map[string]*contract{}
	root := filepath.Join(src, "internal", "services")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "resource_identity_gen_test.go") {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		found, err := contractsIn(path, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		for typ, c := range found {
			if prev, dup := out[typ]; dup {
				return fmt.Errorf("%s: two resource identity tests, %s and %s", typ, prev.Source, c.Source)
			}
			out[typ] = c
		}
		return nil
	})
	return out, err
}

// contractsIn reads one test file's checks, keyed by the resource type of
// the address each check names ("azurerm_storage_container.test").
func contractsIn(path, rel string) (map[string]*contract, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]*contract{}
	var bad error
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || bad != nil {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		var kind string
		switch sel.Sel.Name {
		case "ExpectIdentityValueMatchesStateAtPath", "ExpectStateContainsIdentityValueAtPath", "ExpectIdentityValue":
			kind = sel.Sel.Name
		default:
			return true
		}
		if len(call.Args) != 3 {
			bad = fmt.Errorf("%s: %s with %d arguments", rel, kind, len(call.Args))
			return false
		}
		addr, ok1 := stringLit(call.Args[0])
		attr, ok2 := jsonPath(call.Args[1])
		if !ok1 || !ok2 {
			bad = fmt.Errorf("%s: a %s whose address or identity path is not a literal", rel, kind)
			return false
		}
		typ, _, _ := strings.Cut(addr, ".")
		var a contractAttr
		switch kind {
		case "ExpectIdentityValueMatchesStateAtPath":
			if a.Argument, ok = jsonPath(call.Args[2]); !ok {
				a = contractAttr{Other: exprText(call.Args[2])}
			}
		case "ExpectStateContainsIdentityValueAtPath":
			if a.Within, ok = jsonPath(call.Args[2]); !ok {
				a = contractAttr{Other: exprText(call.Args[2])}
			}
		default:
			a = knownValue(call.Args[2])
		}
		c := out[typ]
		if c == nil {
			c = &contract{Source: rel, Attributes: map[string]contractAttr{}}
			out[typ] = c
		}
		if _, dup := c.Attributes[attr]; dup {
			bad = fmt.Errorf("%s: %s's identity attribute %s is checked twice", rel, typ, attr)
			return false
		}
		c.Attributes[attr] = a
		return true
	})
	return out, bad
}

// jsonPath reads tfjsonpath.New("x") as "x".
func jsonPath(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" {
		return "", false
	}
	return stringLit(call.Args[0])
}

// knownValue reads ExpectIdentityValue's expectation:
// knownvalue.StringExact(data.Subscriptions.<any>) is the provider
// configuration's subscription, knownvalue.StringExact("TXT") a constant,
// and anything else is kept as written.
func knownValue(e ast.Expr) contractAttr {
	call, ok := e.(*ast.CallExpr)
	if ok && len(call.Args) == 1 {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "StringExact" {
			if s, ok := stringLit(call.Args[0]); ok {
				return contractAttr{Constant: s}
			}
			if inner, ok := call.Args[0].(*ast.SelectorExpr); ok {
				if mid, ok := inner.X.(*ast.SelectorExpr); ok && mid.Sel.Name == "Subscriptions" {
					if id, ok := mid.X.(*ast.Ident); ok && id.Name == "data" {
						return contractAttr{ProviderSubscription: true}
					}
				}
			}
		}
	}
	return contractAttr{Other: exprText(e)}
}

// contractSummary is how many identity-schema types carry a contract, for
// the summary line.
func contractSummary(entries []entry) string {
	with, without := 0, []string{}
	for _, e := range entries {
		if e.Class != classIdentitySchema {
			continue
		}
		if e.IdentityContract != nil {
			with++
		} else {
			without = append(without, e.Type)
		}
	}
	sort.Strings(without)
	return strconv.Itoa(with) + " identity-schema types carry azurerm's identity test; none for " + strings.Join(without, ", ")
}
