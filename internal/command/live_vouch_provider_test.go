// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestVouchProviderIsThePassesOwnProviderConfiguration pins the one line
// issue #745's fix rests on, and it reads the source for the same reason
// TestSweepParallelismReachesTheDiscoveryRequest does: against a mock cloud
// there is no output, no diagnostic and no call count that differs between
// a request that stamps its sightings and one that does not. What differs
// is whether a cache hit is possible at all, and both failure directions
// are silent - stamping nothing costs reads forever, stamping the wrong
// configuration puts the flattening back.
//
// The property is structural: discovery.Request.VouchProvider must be the
// provider configuration this pass actually listed through - the same
// identifier handed to provs.ConfiguredProvider - and must NOT be the
// scopeProvider parameter, which the single-provider path deliberately
// passes as its zero value.
func TestVouchProviderIsThePassesOwnProviderConfiguration(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "live_plan.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing live_plan.go: %v", err)
	}

	one := findFuncDecl(t, file, "statelessDiscoverOne")

	vouch, ok := requestFieldValue(t, one, "VouchProvider").(*ast.Ident)
	if !ok {
		t.Fatalf("discovery.Request.VouchProvider is set from %s, not from a parameter of statelessDiscoverOne", exprText(requestFieldValue(t, one, "VouchProvider")))
	}

	// The handle that does the listing is built from one of the same
	// function's parameters; the sightings have to be stamped with that
	// one, or they name an account the pass never called.
	listed := ""
	ast.Inspect(one, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ConfiguredProvider" || len(call.Args) != 2 {
			return true
		}
		if ident, ok := call.Args[1].(*ast.Ident); ok {
			listed = ident.Name
		}
		return false
	})
	if listed == "" {
		t.Fatal("statelessDiscoverOne makes no ConfiguredProvider(ctx, <parameter>) call, so this test cannot tell which provider configuration the pass lists through")
	}
	if vouch.Name != listed {
		t.Errorf("discovery.Request.VouchProvider is set from %q, but the pass lists through the provider configured from %q. A sighting stamped with a configuration other than the one that made the list call is evidence about an account this pass never read.", vouch.Name, listed)
	}

	scope, ok := requestFieldValue(t, one, "ScopeProvider").(*ast.Ident)
	if ok && scope.Name == vouch.Name {
		t.Errorf("discovery.Request.VouchProvider and ScopeProvider are both set from %q. ScopeProvider is the zero value on the single-provider path (statelessDiscover passes addrs.AbsProviderConfig{} there), so sharing it would stamp every single-provider run's sightings with a configuration no instance's own provider address can equal, and the cache would never serve.", vouch.Name)
	}
}
