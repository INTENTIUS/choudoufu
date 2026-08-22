// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/configs"
)

// This file is GitHub issue #368: a [Formula] that can express a pure
// function applied to a value it only learns at render time.
//
// Until now a formula held literals and [ParentRef]s and nothing else, so
// `element(split("/", <a parent's arn>), 1)` had no representation at all -
// not "refused for a reason", but unsayable. Two core estates stop there:
//
//   - corpus-ecs-fargate: `local.cluster_name = try(element(split("/",
//     var.cluster_arn), 1), "")`, where var.cluster_arn is the ECS cluster's
//     own arn reached through a module output, feeding
//     aws_appautoscaling_target's `resource_id` and, through it, every
//     aws_appautoscaling_policy that reads that attribute back.
//   - corpus-rds-complete-postgres: `cidr_blocks = compact(split(",",
//     lookup(var.ingress_with_cidr_blocks[count.index], "cidr_blocks",
//     join(",", var.ingress_cidr_blocks))))`.
//
// # Why this is not a guess
//
// The rule the whole design turns on is HANDOFF.md's: never write a wrong
// marker. A transform is admitted here only when it is a PURE function of a
// string this package already promises to read, so the identity it renders
// is computed from the live value rather than predicted from its shape.
// Nothing here reasons about what an ARN looks like; `split("/", x)` is
// applied to whatever x turns out to be, exactly as OpenTofu would apply it.
//
// The consequence is that "well-defined at render time" is decided AT render
// time, where the value exists, and not at resolve time, where it does not.
// [applyOps] returns ok == false for every operation that is undefined for
// the value it actually received - an index past the end, a sole-element
// selection over a list that turned out to hold two - and a formula that
// will not render is a missing marker, which is the safe half of the rule.
// What is decided at resolve time is only whether the SHAPE is one of these
// operations at all, which is a question about the configuration text.
//
// # The bound, stated rather than implied
//
// The source of a pipeline must resolve to exactly one [Part] carrying a
// [ParentRef] with no transform of its own. A pipeline over a multi-part
// formula (a template with a literal in it), over a literal (which evaluates
// statically and never reaches here), or over a second pipeline is declined.
// That covers both estates and keeps the render contract to "one value in,
// one value out"; widening it needs its own decision about what a transform
// over a concatenation means.

// OpKind names one pure function [applyOps] can apply to a parent's value.
// The set is deliberately closed: every member is a total function of its
// input except for the boundary conditions applyOps rejects explicitly, and
// none of them consults anything outside the value it is handed.
type OpKind string

const (
	// OpSplit is split(sep, s): one string in, a list of strings out.
	OpSplit OpKind = "split"

	// OpCompact is compact(list): the empty strings dropped. A list in, a
	// list out.
	OpCompact OpKind = "compact"

	// OpElement is element(list, i): the i-th member, wrapping modulo the
	// list's length exactly as OpenTofu's element() wraps it. Undefined,
	// there and here, for an empty list.
	OpElement OpKind = "element"

	// OpIndex is list[i]: the i-th member, with no wrapping. Undefined for
	// an i the list does not have, where element() would have wrapped.
	OpIndex OpKind = "index"

	// OpSole is the [Component.SoleElement] selection expressed as a
	// render-time step: a list in, its single member out, undefined for a
	// list of any other length. It is what lets a formula stand where an
	// identity component takes one element of a collection whose length is
	// not knowable until the value is.
	OpSole OpKind = "sole"
)

// Op is one step of a [ParentRef.Transform].
type Op struct {
	Kind OpKind

	// Sep is the separator, for OpSplit only.
	Sep string

	// Index is the position, for OpElement and OpIndex only. It is never
	// negative: the resolver rejects a negative literal rather than
	// modelling element()'s own error for one.
	Index int
}

// wrap renders op applied to the already-rendered inner expression, in
// OpenTofu syntax, for [ParentRef.String].
func (op Op) wrap(inner string) string {
	switch op.Kind {
	case OpSplit:
		return fmt.Sprintf("split(%q, %s)", op.Sep, inner)
	case OpCompact:
		return "compact(" + inner + ")"
	case OpElement:
		return fmt.Sprintf("element(%s, %d)", inner, op.Index)
	case OpIndex:
		return fmt.Sprintf("%s[%d]", inner, op.Index)
	case OpSole:
		return "one(" + inner + ")"
	default:
		return fmt.Sprintf("%s(%s)", op.Kind, inner)
	}
}

// applyOps runs a transform pipeline over the value a parent lookup
// returned. The value enters as a string, may become a list of strings in
// the middle, and must leave as a string.
//
// ok == false means the pipeline is undefined for THIS value - not that the
// configuration is wrong. The caller ([Formula.Render] and
// [Formula.RenderAttrs]) treats it exactly as it treats an unknown parent:
// the formula does not render, the instance carries no marker, and the plan
// proposes creating the object rather than binding it to something guessed.
func applyOps(ops []Op, s string) (string, bool) {
	if len(ops) == 0 {
		return s, true
	}

	str := s
	var list []string
	isList := false

	for _, op := range ops {
		switch op.Kind {
		case OpSplit:
			if isList {
				return "", false
			}
			list, isList = strings.Split(str, op.Sep), true
		case OpCompact:
			if !isList {
				return "", false
			}
			kept := make([]string, 0, len(list))
			for _, e := range list {
				if e != "" {
					kept = append(kept, e)
				}
			}
			list = kept
		case OpElement:
			if !isList || len(list) == 0 || op.Index < 0 {
				return "", false
			}
			str, isList = list[op.Index%len(list)], false
		case OpIndex:
			if !isList || op.Index < 0 || op.Index >= len(list) {
				return "", false
			}
			str, isList = list[op.Index], false
		case OpSole:
			if !isList || len(list) != 1 {
				return "", false
			}
			str, isList = list[0], false
		default:
			return "", false
		}
	}

	if isList {
		return "", false
	}
	return str, true
}

// opsYield reports what a pipeline holds after its last step, given that it
// starts from a string: false for a string, true for a list. ok is false for
// a pipeline that is not well formed at all - a compact() of a string, a
// split() of a list - which cannot arise from a configuration OpenTofu
// itself accepts, and is checked anyway so that neither caller has to trust
// the peel.
//
// It is the resolve-time half of the two-part discipline this file rests on:
// the SHAPE is checked here, where the configuration text is, and the VALUE
// is checked in [applyOps], where the value is. Neither is a guess about the
// other.
func opsYield(ops []Op) (isList bool, ok bool) {
	for _, op := range ops {
		switch op.Kind {
		case OpSplit:
			if isList {
				return false, false
			}
			isList = true
		case OpCompact:
			if !isList {
				return false, false
			}
		case OpElement, OpIndex, OpSole:
			if !isList {
				return false, false
			}
			isList = false
		default:
			return false, false
		}
	}
	return isList, true
}

// maxTransformDepth bounds the peel below. A pipeline this deep in real
// configuration does not exist; the bound is here so a pathological
// expression cannot recurse without end.
const maxTransformDepth = 8

// resolveTransformCall recognizes a pipeline of pure functions applied to a
// single deferred parent read, and resolves it to that read plus the
// pipeline.
//
// The three results are [resolver.resolveSelection]'s and every other
// applicable-shaped handler's: the parts, whether they resolved, and whether
// this shape is one this function handles at all. applicable == false leaves
// the caller's own diagnostic standing, unreplaced and unaugmented - every
// declining path restores r.diags and r.pendingSiblingApply to what they
// were on entry.
func (r *resolver) resolveTransformCall(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	syn, isSyn := expr.(hclsyntax.Expression)
	if !isSyn {
		return nil, false, false
	}

	mark := len(r.diags)
	sibMark := len(r.pendingSiblingApply)
	decline := func() ([]Part, bool, bool) {
		r.diags = r.diags[:mark]
		r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
		return nil, false, false
	}

	source, ops, peeled := r.peelTransform(syn, scope, ident, 0)
	if !peeled || len(ops) == 0 {
		return decline()
	}
	// A [Part] is a piece of a string. A pipeline that still holds a list
	// after its last step is not one, and must not be carried to render
	// time to fail silently there: an identity argument written as
	// `split(",", <a read>)` with no selection is a collection where a
	// scalar belongs, and the ordinary refusal for that is the right
	// answer. [resolver.soleElementDeferred] is the one caller that wants
	// the list case, and it supplies the selection itself.
	if isList, wellFormed := opsYield(ops); !wellFormed || isList {
		return decline()
	}

	got, gotOK := r.resolveExpr(source, scope, ident)
	if !gotOK {
		// The source itself did not resolve. Whatever refused it is a
		// complaint about the same expression the caller is already
		// refusing, so it is dropped rather than stapled on: this shape is
		// reported as not handled and the caller's own message stands.
		return decline()
	}
	if len(got) != 1 || got[0].Parent == nil || len(got[0].Parent.Transform) > 0 {
		// See the file comment's "bound": one deferred read in, one value
		// out. A concatenation, a literal or an already-transformed read is
		// not something this render contract can carry.
		return decline()
	}

	// Nothing is rolled back on the way out. Every probe this function made
	// that FAILED restored itself ([resolver.staticString],
	// [resolver.staticIndex]), so what is left between mark and here is
	// whatever the source's own successful resolution recorded - a
	// pending sibling-apply refusal, say - and that belongs to the caller
	// exactly as it would had the author written the source on its own.
	// Truncating here would turn one of those into silence, which is the
	// failure mode this repository has shipped green more than once.
	ref := *got[0].Parent
	ref.Transform = ops
	return []Part{{Parent: &ref}}, true, true
}

// peelTransform strips recognized pure functions off the outside of expr,
// innermost-first in the returned slice, and hands back what is left.
//
// It recognizes only shapes whose every parameter other than the value
// itself is statically known here - the separator of a split, the index of
// an element - because a parameter this package cannot read is a parameter
// it would have to guess.
func (r *resolver) peelTransform(expr hclsyntax.Expression, scope instScope, ident configs.StaticIdentifier, depth int) (source hclsyntax.Expression, ops []Op, ok bool) {
	if depth > maxTransformDepth {
		return nil, nil, false
	}

	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return r.peelTransform(e.Expression, scope, ident, depth+1)

	case *hclsyntax.IndexExpr:
		i, iOK := r.staticIndex(e.Key, scope, ident)
		if !iOK {
			return nil, nil, false
		}
		inner, innerOps, innerOK := r.peelTransform(e.Collection, scope, ident, depth+1)
		if !innerOK {
			return nil, nil, false
		}
		return inner, append(innerOps, Op{Kind: OpIndex, Index: i}), true

	case *hclsyntax.RelativeTraversalExpr:
		// HCL folds a constant index into a traversal step rather than
		// building an IndexExpr, so `split("/", x)[1]` arrives here and not
		// in the case above. Only a single index step is recognized; an
		// attribute step past a transform is not a shape this file claims.
		if len(e.Traversal) != 1 {
			return nil, nil, false
		}
		idx, isIndex := e.Traversal[0].(hcl.TraverseIndex)
		if !isIndex {
			return nil, nil, false
		}
		i, iOK := indexIntValue(idx.Key)
		if !iOK {
			return nil, nil, false
		}
		inner, innerOps, innerOK := r.peelTransform(e.Source, scope, ident, depth+1)
		if !innerOK {
			return nil, nil, false
		}
		return inner, append(innerOps, Op{Kind: OpIndex, Index: i}), true

	case *hclsyntax.FunctionCallExpr:
		return r.peelTransformCall(e, scope, ident, depth)
	}

	// Not a transform: this is the source the pipeline reads.
	return expr, nil, true
}

func (r *resolver) peelTransformCall(call *hclsyntax.FunctionCallExpr, scope instScope, ident configs.StaticIdentifier, depth int) (source hclsyntax.Expression, ops []Op, ok bool) {
	peel := func(inner hclsyntax.Expression, op Op) (hclsyntax.Expression, []Op, bool) {
		src, innerOps, innerOK := r.peelTransform(inner, scope, ident, depth+1)
		if !innerOK {
			return nil, nil, false
		}
		return src, append(innerOps, op), true
	}

	switch call.Name {
	case "split":
		if len(call.Args) != 2 {
			return nil, nil, false
		}
		sep, sepOK := r.staticString(call.Args[0], scope, ident)
		if !sepOK {
			return nil, nil, false
		}
		return peel(call.Args[1], Op{Kind: OpSplit, Sep: sep})

	case "compact":
		if len(call.Args) != 1 {
			return nil, nil, false
		}
		return peel(call.Args[0], Op{Kind: OpCompact})

	case "element":
		if len(call.Args) != 2 {
			return nil, nil, false
		}
		i, iOK := r.staticIndex(call.Args[1], scope, ident)
		if !iOK {
			return nil, nil, false
		}
		return peel(call.Args[0], Op{Kind: OpElement, Index: i})

	case "one":
		if len(call.Args) != 1 {
			return nil, nil, false
		}
		// one() over a list of a length this package cannot know. It is not
		// [resolver.resolveArityCollapse]'s case - that one proves the
		// length from a resource's own expansion and needs no render-time
		// step at all - and the two do not overlap, because a splat over a
		// managed resource never reaches here: the source of a pipeline has
		// to resolve to a single ParentRef.
		return peel(call.Args[0], Op{Kind: OpSole})

	case "try":
		// try(A, fallback...) where A is the pipeline. OpenTofu's try moves
		// past an argument only when evaluating it ERRORS, and an unknown
		// value is explicitly not an error there (see fallback.go's own
		// note), so at plan time the language's answer is A's. At apply, A
		// may error and the fallback may win - which is exactly the case
		// [applyOps] refuses to render, because the operation that would
		// have errored is the operation it declines. So the claim this peel
		// makes is the narrow one: whenever the pipeline renders, what it
		// renders is what the language computes. When it does not, this
		// package writes no marker rather than inventing the fallback.
		if len(call.Args) == 0 {
			return nil, nil, false
		}
		src, innerOps, innerOK := r.peelTransform(call.Args[0], scope, ident, depth+1)
		if !innerOK || len(innerOps) == 0 {
			// A bare try() with no pipeline under it belongs to
			// [resolver.resolveFallbackChain], which decides WHICH argument
			// the language selects and has rules this one does not.
			return nil, nil, false
		}
		return src, innerOps, true
	}

	return nil, nil, false
}

// soleElementDeferred is [Component.SoleElement]'s third narrowing, for a
// collection whose LENGTH is not knowable until a live value is: the
// argument is a pipeline of pure functions over a deferred parent read, and
// the last step of that pipeline still produces a list.
//
// `cidr_blocks = compact(split(",", lookup(var.ingress_with_cidr_blocks[
// count.index], "cidr_blocks", join(",", var.ingress_cidr_blocks))))` -
// terraform-aws-modules/security-group's universal ingress rule, and
// corpus-rds-complete-postgres's last identity blocker - is the shape.
// Neither [resolver.soleElementExpr]'s syntactic narrowing nor
// [resolver.soleElementFromValue]'s evaluated one can reach it: there is no
// list construct to count, and no evaluation produces a list either,
// because the leaf is a resource attribute.
//
// Deferring the count is not the same as guessing it. What this returns is
// `one(<the argument>)`, which [resolver.peelTransform] reads as one more
// step of the same pipeline, and [applyOps] applies at render time to the
// list the real value actually produced: exactly one element renders it,
// any other number renders nothing. The refusal the other two narrowings
// raise on the spot ("Ambiguous list-valued identity argument") therefore
// becomes a missing marker rather than a diagnostic - the same trade
// [Formula] already makes for every parent whose live value is not found.
//
// applicable is false for everything that is not this, which leaves both
// narrowings above and resolveExpr's own refusals exactly as they were.
func (r *resolver) soleElementDeferred(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (hcl.Expression, bool) {
	syn, isSyn := expr.(hclsyntax.Expression)
	if !isSyn {
		return nil, false
	}

	mark := len(r.diags)
	sibMark := len(r.pendingSiblingApply)
	decline := func() (hcl.Expression, bool) {
		r.diags = r.diags[:mark]
		r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
		return nil, false
	}

	source, ops, peeled := r.peelTransform(syn, scope, ident, 0)
	if !peeled || len(ops) == 0 {
		return decline()
	}
	if isList, wellFormed := opsYield(ops); !wellFormed || !isList {
		// A pipeline that has already selected a scalar has nothing left
		// for a sole-element selection to select from, and one that is not
		// well formed is not a pipeline.
		return decline()
	}

	// The source has to be the one deferred read this render contract can
	// carry, and it has to be checked HERE rather than left to the wrapped
	// expression: returning one(...) for a source that resolves some other
	// way would replace an answer with a refusal.
	got, gotOK := r.resolveExpr(source, scope, ident)
	if !gotOK || len(got) != 1 || got[0].Parent == nil || len(got[0].Parent.Transform) > 0 {
		return decline()
	}
	r.diags = r.diags[:mark]
	r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]

	rng := expr.Range()
	return &hclsyntax.FunctionCallExpr{
		Name:            "one",
		Args:            []hclsyntax.Expression{syn},
		NameRange:       rng,
		OpenParenRange:  rng,
		CloseParenRange: rng,
	}, true
}

// staticString evaluates an argument this package needs the value of - a
// split()'s separator - leaving no diagnostic behind when it cannot. A
// failure here means "this shape is not recognized", not "this configuration
// is wrong", which is the same rollback [resolver.fallbackArmVerdict] uses
// for the same reason.
func (r *resolver) staticString(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (string, bool) {
	if r.isSymbolic(expr, scope) {
		return "", false
	}
	mark := len(r.diags)
	val, ok := r.evalStatic(expr, scope, ident)
	r.diags = r.diags[:mark]
	if !ok || !val.IsKnown() || val.IsNull() || val.Type() != cty.String || val.IsMarked() {
		return "", false
	}
	return val.AsString(), true
}

// staticIndex is [resolver.staticString] for a position: an index this
// package can read as a non-negative whole number, and nothing else.
func (r *resolver) staticIndex(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (int, bool) {
	if r.isSymbolic(expr, scope) {
		return 0, false
	}
	mark := len(r.diags)
	val, ok := r.evalStatic(expr, scope, ident)
	r.diags = r.diags[:mark]
	if !ok {
		return 0, false
	}
	return indexIntValue(val)
}

// indexIntValue reads a cty value as a non-negative index, or declines.
func indexIntValue(val cty.Value) (int, bool) {
	if !val.IsKnown() || val.IsNull() || val.IsMarked() {
		return 0, false
	}
	num, err := convert.Convert(val, cty.Number)
	if err != nil || num.IsNull() || !num.IsKnown() {
		return 0, false
	}
	var i int
	if err := gocty.FromCtyValue(num, &i); err != nil {
		return 0, false
	}
	if i < 0 {
		return 0, false
	}
	return i, true
}
