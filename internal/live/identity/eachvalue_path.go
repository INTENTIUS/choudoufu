// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// eachValuePathValue is GitHub issue #1962's read: an argument written as
// `each.value`, or as a traversal into it, selected by path (a
// [Component.Path], such as a manifest's metadata.name) out of the for_each
// element the expansion bound as a VALUE.
//
// The shape is the one live/kubernetes/COMPATIBILITY.md recommends for a
// rendered Helm chart: for_each over the render split and yamldecoded inside
// the for_each expression, `manifest = each.value`. The for_each value was
// already resolved to produce the instance keys, so reading the natural key
// out of the same value adds no guess.
//
// applicable is true only when expr is that shape AND the bound value is
// non-null, wholly known and carries no mark anywhere. Everything else -
// an element bound only as an expression (#260), a value with an unknown in
// it (#354's deferred binding), a sensitive value - reports false and the
// caller keeps the route and the refusal it had. The guard is on the whole
// element rather than on the selected leaf on purpose: a partly unknown or
// partly sensitive element is one this package declines to read keys out
// of at all, which costs nothing for a rendered chart, whose documents are
// wholly known.
//
// present reports whether the path exists; a null leaf counts as absent,
// which is how Kubernetes itself reads `namespace: ~`.
func eachValuePathValue(expr hcl.Expression, path hcl.Traversal, scope instScope) (leaf cty.Value, present bool, applicable bool) {
	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() || trav.RootName() != "each" || len(trav) < 2 || !isAttrStep(trav[1], "value") {
		return cty.NilVal, false, false
	}
	val := scope.repetition.EachValue
	if val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.ContainsMarked() {
		return cty.NilVal, false, false
	}
	steps := make([]hcl.Traverser, 0, len(trav)-2+len(path))
	steps = append(steps, trav[2:]...)
	steps = append(steps, path...)
	for _, step := range steps {
		next, ok := valueStep(val, step)
		if !ok {
			return cty.NilVal, false, true
		}
		val = next
	}
	if val.IsNull() {
		return cty.NilVal, false, true
	}
	return val, true, true
}

// valueStep applies one attribute or index step to a wholly known value,
// reporting false when the key or index is not there rather than raising
// the language's error.
func valueStep(val cty.Value, step hcl.Traverser) (cty.Value, bool) {
	// A marked step value refuses rather than unmarks: a marked value never
	// becomes an identity component.
	if val.IsMarked() || val.IsNull() {
		return cty.NilVal, false
	}
	ty := val.Type()
	if key, ok := stepKeyString(step); ok {
		switch {
		case ty.IsObjectType():
			if !ty.HasAttribute(key) {
				return cty.NilVal, false
			}
			return val.GetAttr(key), true
		case ty.IsMapType():
			k := cty.StringVal(key)
			if has := val.HasIndex(k); has.IsMarked() || !has.True() {
				return cty.NilVal, false
			}
			return val.Index(k), true
		}
		return cty.NilVal, false
	}
	if idx, ok := stepIndexInt(step); ok && (ty.IsTupleType() || ty.IsListType()) {
		if idx < 0 || idx >= val.LengthInt() {
			return cty.NilVal, false
		}
		return val.Index(cty.NumberIntVal(int64(idx))), true
	}
	return cty.NilVal, false
}
