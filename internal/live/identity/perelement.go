// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/configs"
)

// This file is [Component.PerElement]'s implementation: an identity whose
// tail is one segment per element of a collection-typed argument, rather
// than one segment per component. The documented archetype is
// aws_iam_user_group_membership's `user1/group1/group2`.
//
// The whole of the difference from every other component is that the
// collection is expanded into its ELEMENTS before anything is rendered.
// That matters for more than the segment count. A collection reaching
// [resolver.resolveExpr] whole refuses - there is no string rendering of a
// tuple, and a tuple carrying a managed-resource reference is symbolic, so
// it falls out of that function's switch into "Identity not resolvable from
// configuration". Expanded first, each element is an ordinary identity
// expression that the existing machinery already knows how to render: a
// literal, a variable, or a reference to a sibling's identity attribute.

// precedingSeparator returns the separator the component at index i joins
// its elements with: the Literal of the component immediately before it,
// when that component is a pure separator (no Attrs, no Cloud). Every
// multi-segment row in the table spells its separators that way, so a
// PerElement component reads its own separator out of the row rather than
// carrying a second copy of it that could disagree.
//
// The empty string when there is no such predecessor, which is the honest
// answer for a first component: nothing precedes it, so nothing says how its
// elements join, and they concatenate with nothing between them exactly as
// two adjacent components already do.
func precedingSeparator(comps []Component, i int) string {
	if i == 0 {
		return ""
	}
	prev := comps[i-1]
	if len(prev.Attrs) != 0 || prev.Cloud != CloudNone {
		return ""
	}
	return prev.Literal
}

// perElementParts renders one PerElement component: it expands expr into
// element expressions, resolves each one on its own, canonicalises the order
// when every element yields a key, and joins the results with sep.
func (r *resolver) perElementParts(expr hcl.Expression, scope instScope, attr *hcl.Attribute, ident configs.StaticIdentifier, sep string) ([]Part, bool) {
	elems, ok := r.elementParts(expr, scope, attr, ident, 0)
	if !ok {
		return nil, false
	}
	if len(elems) == 0 {
		r.errorf(attr.Range, "Empty per-element identity argument",
			"%s has no elements. The provider's import identity for this type is one segment per value of %s, so an empty collection names no object at all.",
			ident.Subject, attr.Name)
		return nil, false
	}
	elems = canonicaliseElements(elems)

	var out []Part
	for i, e := range elems {
		if i > 0 && sep != "" {
			out = append(out, Part{Literal: sep})
		}
		out = append(out, e...)
	}
	return coalesce(out), true
}

// canonicaliseElements sorts the elements into a stable order, but only when
// every one of them has a key to sort by - see [Component.PerElement]'s
// all-or-nothing rule. An element that waits on a live value (any Part with
// a Parent) has no key, and one such element leaves the whole sequence in
// the order the configuration wrote it.
func canonicaliseElements(elems [][]Part) [][]Part {
	keyed := make([]struct {
		key   string
		parts []Part
	}, len(elems))
	for i, e := range elems {
		var buf strings.Builder
		for _, p := range e {
			if p.Parent != nil {
				// No key for this element, so no key for the sequence.
				return elems
			}
			buf.WriteString(p.Literal)
		}
		keyed[i].key = buf.String()
		keyed[i].parts = e
	}
	// The key travels WITH its element. A sort over elems alone, comparing
	// a parallel key slice by index, reads keys the sort has already
	// permuted out from under it - the comparison would be against whatever
	// element happens to sit at that index now, not the one being compared.
	sort.SliceStable(keyed, func(a, b int) bool { return keyed[a].key < keyed[b].key })

	// Collapse equal elements, on the same evidence the sort rests on. The
	// soundness argument for reordering at all is that the provider parses
	// the tail back into a SET, so permuting it changes nothing the provider
	// sees. A set collapses duplicates too, and that half was missed: two
	// equal elements render one segment each, so `groups = ["a", "a"]` -
	// reachable through a list-typed variable, a concat or a flatten, since a
	// set-typed variable is already deduped by cty before it arrives - built
	// `user/a/a` where the object's own ID is `user/a`. That is a wrong
	// rendered identity, which outranks a missing one.
	//
	// Only ever applied together with the sort, so the same all-or-nothing
	// condition covers both: an element with no key returns early above and
	// the sequence is left exactly as written.
	out := make([][]Part, 0, len(keyed))
	var last string
	for i, k := range keyed {
		if i > 0 && k.key == last {
			continue
		}
		last = k.key
		out = append(out, k.parts)
	}
	return out
}

// elementParts expands a collection-valued identity argument into one
// resolved part list per element.
//
// Three shapes, in the order they are tried:
//
//   - a syntactic list/set/tuple construct written in configuration, which
//     is what hcl.ExprList recognises. Each element is resolved as its own
//     identity expression, so `[aws_iam_group.a.name, aws_iam_group.b.name]`
//     composes out of two sibling references rather than refusing as one
//     unrenderable tuple.
//   - a bare each.value bound to an element EXPRESSION rather than a value
//     (#260's binding, see eachvalue.go). This is the shape the
//     rust-lang/simpleinfra estate is written in - `groups = each.value`
//     over a local whose values are lists of sibling references - and it is
//     the reason the hop exists here at all: the element expression belongs
//     to the module it was written in, so it is expanded there and its
//     elements are resolved there.
//   - anything else that evaluates statically to a known, unmarked
//     collection: a variable or local typed list(string)/set(string), whose
//     elements are then plain literals. This is the same fallback
//     [resolver.soleElementFromValue] applies for the one-element rule, for
//     the same reason - nothing about "one segment per element" is specific
//     to how the collection was spelled.
//
// Anything else refuses with its own diagnostic, or leaves standing whichever
// diagnostic the evaluation already recorded.
func (r *resolver) elementParts(expr hcl.Expression, scope instScope, attr *hcl.Attribute, ident configs.StaticIdentifier, depth int) ([][]Part, bool) {
	if depth > maxStaticDecomposeDepth {
		return nil, false
	}
	if paren, ok := expr.(*hclsyntax.ParenthesesExpr); ok {
		return r.elementParts(paren.Expression, scope, attr, ident, depth+1)
	}

	if elems, diags := hcl.ExprList(expr); !diags.HasErrors() && elems != nil {
		out := make([][]Part, 0, len(elems))
		for _, e := range elems {
			got, ok := r.resolveExpr(e, scope, ident)
			if !ok {
				return nil, false
			}
			out = append(out, got)
		}
		return out, true
	}

	if parts, ok, applicable := r.eachValueElements(expr, scope, attr, ident, depth); applicable {
		return parts, ok
	}

	if parts, ok, applicable := r.staticElements(expr, scope, ident); applicable {
		return parts, ok
	}

	r.errorf(attr.Range, "Per-element identity argument not resolvable",
		"%s builds its import identity from one segment per value of %s, and this configuration does not say what those values are. "+
			"The argument must be a list written in configuration, a variable or local holding one, or a for_each element bound to one.",
		ident.Subject, attr.Name)
	return nil, false
}

// eachValueElements is [resolver.elementParts]'s hop from a bare `each.value`
// to the element expression this instance's for_each binds it to.
//
// Without it, `groups = each.value` reaches [resolver.selectStatic] with no
// remaining traversal steps, which resolves the element expression WHOLE -
// and a tuple of managed-resource references has no whole rendering, so the
// resolution refuses. The hop is not specific to any resource type: it says
// only that a collection reached through each.value is expanded the same way
// a collection written inline is.
//
// applicable is false when expr is not a bare each.value, or when nothing is
// bound for this instance's key, leaving the caller's other shapes to try.
func (r *resolver) eachValueElements(expr hcl.Expression, scope instScope, attr *hcl.Attribute, ident configs.StaticIdentifier, depth int) ([][]Part, bool, bool) {
	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() || len(trav) != 2 || trav.RootName() != "each" || !isAttrStep(trav[1], "value") {
		return nil, false, false
	}
	b := scope.eachValueExpr
	if b == nil || b.expr == nil {
		return nil, false, false
	}

	// The element expression belongs to the module it was WRITTEN in, which
	// is the calling module whenever the for_each source came through a
	// module variable. Re-entering it puts back the locals, variables, data
	// results and provider mapping that hop had - the same restore
	// [resolver.eachValueSelect] performs for the same reason.
	savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
	if !r.enterModuleFor(b.modInst) {
		return nil, false, false
	}
	defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()

	got, ok := r.elementParts(b.expr, b.scope, attr, ident, depth+1)
	return got, ok, true
}

// staticElements is the value-shaped fallback: expr is not a syntactic
// construct and not an each.value binding, so evaluate it and expand the
// collection it produces.
//
// applicable is false whenever nothing here applies - expr references a
// managed resource (isSymbolic, nothing evaluable without the cloud), or
// evaluation fails, or the result is not a known, unmarked collection - and
// in every such case no diagnostic is left behind, so the caller's own
// refusal is the one the operator reads. The marked check is before
// ElementIterator, which panics on a marked value, exactly as
// [resolver.soleElementFromValue] guards it.
func (r *resolver) staticElements(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) ([][]Part, bool, bool) {
	if r.isSymbolic(expr, scope) {
		return nil, false, false
	}
	mark := len(r.diags)
	val, ok := r.evalStatic(expr, scope, ident)
	if !ok {
		r.diags = r.diags[:mark]
		return nil, false, false
	}
	ty := val.Type()
	if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() {
		return nil, false, false
	}
	if val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return nil, false, false
	}
	var out [][]Part
	for it := val.ElementIterator(); it.Next(); {
		_, v := it.Element()
		s, ok := r.stringValue(v, expr, ident)
		if !ok {
			return nil, false, true
		}
		out = append(out, []Part{{Literal: s}})
	}
	return out, true, true
}
