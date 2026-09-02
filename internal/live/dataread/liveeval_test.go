// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// TestLiveModuleEvaluatorScopesLookupPerModule is [liveModuleEvaluator]'s
// own adversarial case, isolated from the rest of the read/analyze
// pipeline: two modules each declare data.test_zone.shared, and the lookup
// this test hands in returns a DIFFERENT, DISTINGUISHABLE value depending
// on which module it is asked about. If liveModuleEvaluator ever reused one
// module's data-lookup closure for another module's evaluation - the
// concrete failure mode issue #212's fix guards against - this test
// resolves the wrong value instead of refusing, which a table of "eligible
// or not" assertions elsewhere in this package cannot see, because
// eligibility never surfaces WHICH value was fed in.
func TestLiveModuleEvaluatorScopesLookupPerModule(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "cross-module-no-leak"), nil)
	ctx := context.Background()

	lookup := func(m addrs.Module) configs.StaticDataLookup {
		return func(addr addrs.Resource) (cty.Value, bool) {
			if addr.String() != "data.test_zone.shared" {
				return cty.NilVal, false
			}
			if len(m) == 0 {
				return cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("ROOT-VALUE")}), true
			}
			return cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("CHILD-VALUE")}), true
		}
	}

	childModule := addrs.Module{"child"}
	childEval := liveModuleEvaluator(ctx, cfg, childModule, lookup, false, nil, nil)
	if childEval == nil {
		t.Fatal("liveModuleEvaluator returned nil for the child module")
	}

	expr, parseDiags := hclsyntax.ParseExpression([]byte("var.zone_name"), "test.tf", hcl.InitialPos)
	if parseDiags.HasErrors() {
		t.Fatalf("parsing var.zone_name: %s", parseDiags.Error())
	}
	ident := configs.StaticIdentifier{Module: childModule, Subject: "test"}
	val, diags := childEval.Evaluate(ctx, expr, ident)
	if diags.HasErrors() {
		t.Fatalf("evaluating var.zone_name from the child's live evaluator: %s", diags.Error())
	}
	if val.IsNull() || !val.IsKnown() || val.AsString() != "ROOT-VALUE" {
		t.Fatalf("child's var.zone_name (which the module call sets to the root's own data.test_zone.shared.name) evaluated to %#v, want \"ROOT-VALUE\" - a lookup scoped to the wrong module returned the child's own same-named data source's value instead", val)
	}
}
