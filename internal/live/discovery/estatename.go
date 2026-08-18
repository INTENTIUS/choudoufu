// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// DeclaredEstateNames reads the distinct estate names a configuration
// already stamps on itself, sorted, over the whole static module tree: the
// [TagEstate] entry of every managed resource's tags argument, read only
// where it is a literal object expression whose value the static evaluator
// can settle.
//
// It is what answers "which estate is this" for an operator who passed no
// -estate flag, and both "choudoufu live-plan" and the estate-plan
// instrument have to give the same answer to that question - they are
// reporting on the same run. They had a body-for-body copy each, whose own
// doc said it mirrored the other, and nothing was watching: stopping the
// instrument's copy recursing into child modules left the whole tree green.
// Issue #285.
//
// Only tag values that evaluate from configuration alone count. A tag built
// from another resource's attribute is not readable here, and is not
// treated as a partial answer: it is simply not one of the values, which at
// worst costs the operator an -estate flag.
func DeclaredEstateNames(ctx context.Context, cfg *configs.Config) []string {
	seen := make(map[string]bool)
	declaredEstateNamesFrom(ctx, cfg, seen)

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// declaredEstateNamesFrom is [DeclaredEstateNames]'s recursive step: one
// module's resources in name order, then its children in name order.
func declaredEstateNamesFrom(ctx context.Context, cfg *configs.Config, seen map[string]bool) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	mod := cfg.Module
	if mod.StaticEvaluator == nil {
		return
	}

	names := make([]string, 0, len(mod.ManagedResources))
	for name := range mod.ManagedResources {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rc := mod.ManagedResources[name]

		content, _, contentDiags := rc.Config.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "tags"}},
		})
		if contentDiags.HasErrors() {
			continue
		}
		attr, ok := content.Attributes["tags"]
		if !ok {
			continue
		}
		pairs, pairDiags := hcl.ExprMap(attr.Expr)
		if pairDiags.HasErrors() {
			// A tags argument that is not written as an object literal - a
			// merge() call, a variable - cannot be picked apart here.
			continue
		}
		for _, pair := range pairs {
			key, keyDiags := pair.Key.Value(nil)
			if keyDiags.HasErrors() || key.IsNull() || key.Type() != cty.String || key.AsString() != TagEstate {
				continue
			}
			val, valDiags := mod.StaticEvaluator.Evaluate(ctx, pair.Value, configs.StaticIdentifier{
				Module:    cfg.Path,
				Subject:   fmt.Sprintf("%s.tags", rc.Addr()),
				DeclRange: attr.Range,
			})
			if valDiags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() || val.Type() != cty.String {
				continue
			}
			if s := val.AsString(); s != "" {
				seen[s] = true
			}
		}
	}

	for _, name := range identity.SortedChildNames(cfg.Children) {
		declaredEstateNamesFrom(ctx, cfg.Children[name], seen)
	}
}
