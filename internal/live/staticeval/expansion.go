// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staticeval

import (
	"context"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/configs"
)

// expansion.go derives what a block's count and for_each say about its
// instances, as far as configuration alone can say it. Anything neither can
// answer is reported as "not computable here" rather than guessed at: the
// callers are a lint pass and an address-length check, and a guessed
// instance key becomes a guessed marker.

// Count computes the value of a count expression, or reports that it is not
// computable here. It mirrors [ForEachKeys]: the same traversal pre-filter
// keeps the static scope's panic classes out of the evaluator, and anything
// it cannot evaluate is skipped rather than guessed at.
func Count(ctx context.Context, mod *configs.Module, expr hcl.Expression) (int, bool) {
	if mod == nil || mod.StaticEvaluator == nil {
		return 0, false
	}
	if !AllowedExpr(expr) {
		return 0, false
	}

	val, ok := EvaluateOK(ctx, mod.StaticEvaluator, expr, "count")
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return 0, false
	}

	var n int
	if err := gocty.FromCtyValue(val, &n); err != nil {
		// Not a whole number: not a legal count at all, and identity
		// resolution says so with its own message.
		return 0, false
	}
	return n, true
}

// ForEachKeys computes the instance keys a for_each expression produces, or
// reports that they are not computable here.
//
// The traversal pre-filter is not an optimization: staticScopeData panics by
// contract on repetition, resource, module, output and check references
// ("Not Available in Static Context"), so an expression mentioning one must
// not be handed to the static evaluator at all.
func ForEachKeys(ctx context.Context, mod *configs.Module, expr hcl.Expression) ([]string, bool) {
	if mod == nil || mod.StaticEvaluator == nil {
		return nil, false
	}
	if !AllowedExpr(expr) {
		return nil, false
	}

	val, ok := EvaluateOK(ctx, mod.StaticEvaluator, expr, "for_each")
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return nil, false
	}

	ty := val.Type()
	var keys []string
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		for it := val.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			if k.Type() != cty.String || k.IsNull() {
				return nil, false
			}
			keys = append(keys, k.AsString())
		}
	case ty.IsSetType(), ty.IsListType(), ty.IsTupleType():
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			if v.Type() != cty.String || v.IsNull() {
				// A non-string collection is not a legal for_each at all;
				// identity resolution says so with its own message.
				return nil, false
			}
			// The whole-value IsMarked test above is not enough here: cty
			// hoists a marked element's mark to the containing SET, but a
			// LIST or TUPLE keeps it on the element, so `for_each =
			// [var.secret]` arrives as an unmarked tuple holding a marked
			// string and AsString below panicked. Reported "cannot check",
			// the same answer a marked whole value already gets - the
			// for_each is refused by identity resolution either way.
			if v.IsMarked() {
				return nil, false
			}
			keys = append(keys, v.AsString())
		}
	default:
		return nil, false
	}

	// Sorted so that two runs over one configuration report the same issues
	// in the same order; cty iterates a map in key order already, but an
	// object type's attribute order is not something to rely on here.
	sort.Strings(keys)
	return keys, true
}
