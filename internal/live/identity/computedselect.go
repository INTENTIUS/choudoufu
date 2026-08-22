// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
)

// This file is the ROUTING half of GitHub issue #368's second estate,
// corpus-rds-complete-postgres, and it is a routing question rather than a
// function-application one - transform.go landed the functions and measured
// that this estate did not move.
//
// terraform-aws-modules/security-group's universal ingress rule is
//
//	cidr_blocks = compact(split(",", lookup(
//	  var.ingress_with_cidr_blocks[count.index], "cidr_blocks",
//	  join(",", var.ingress_cidr_blocks))))
//
// and the caller - the rds module's own examples/complete-postgres - writes
// the list it indexes with one object literal whose `cidr_blocks` leaf is
// `module.vpc.vpc_cidr_block`, itself `try(aws_vpc.this[0].cidr_block,
// null)`. Reduced to four variants over one fixture
// (testdata/deferred-through-module-list), exactly one of them resolved:
//
//	[var.L[0].cidr_blocks]                                   RESOLVES
//	[var.L[count.index].cidr_blocks]                         refused
//	compact(split(",", lookup(var.L[0], "cidr_blocks", …)))  refused
//	compact(split(",", lookup(var.L[count.index], …)))       refused
//
// The first line is the whole argument that the other three are ROUTING
// failures and not analysis gaps: the very same module output, selected out
// of the very same module-call argument, through the very same
// [resolver.resolveNamed] chase, resolves to a formula over the VPC's own
// attribute. What stopped the other three is that neither spelling reaches
// resolveNamed at all.
//
//   - `var.L[count.index]` is not an absolute traversal. HCL builds an
//     IndexExpr the moment an index is not a constant, so
//     [resolver.namedLeaf]'s hcl.AbsTraversalForExpr gate declines and the
//     reference is never chased across the module-call boundary.
//   - `lookup(<anything>, "k", d)` is a FunctionCallExpr, which that same
//     gate declines for the same reason. [resolver.resolveLookupCall] exists
//     but reads each.value alone; nothing routes a lookup over a module-call
//     argument.
//
// # The rule, and why it is not a widening of what may be resolved
//
// Both spellings are the same shape: a reference whose steps this package
// can read, written with something other than a bare traversal. So the rule
// is to FOLD - to compute the steps the reference selects, then hand the
// result to the identical [resolver.resolveNamed] / [resolver.resolveModuleOutput]
// chase a bare traversal already takes, under every restriction that chase
// already enforces. An index is folded by evaluating it in this instance's
// own scope, which is what [resolver.resolveIndexedTraversal] already does
// for `aws_subnet.this[count.index].id`; a three-argument lookup() is folded
// into one attribute step, which is what [resolver.eachValueSelector] already
// does for `lookup(each.value, "k", d)`. Neither is new evaluation
// machinery, and no aws_* type name appears anywhere in it: the rule is
// keyed on the shape of the expression, so it applies to every provider type
// this package admits.
//
// # Last, never first
//
// [resolver.resolveExpr] reaches this only after evalStatic, namedLeaf,
// resolveSelection, resolveTransformCall AND [resolver.tolerantPart] have all
// declined - the point that function returns false from today. That ordering
// is the safety argument [resolver.tolerantPart]'s own doc makes and it is
// load-bearing here for a second reason: `lookup(var.ingress_with_cidr_blocks[
// count.index], "from_port", …)` on the very same list resolves through
// tolerantPart to the integer the caller wrote, and it must keep doing so.
// This adds a resolution where there was a refusal, or it changes nothing.
//
// # The one thing it is stricter about than the path it copies
//
// [resolver.resolveNamed] deliberately drops the variable's DECLARED TYPE
// (see its own note, and typedvar.go's closing paragraph: what a declared
// type means for a symbolic Formula is an open question). That is tolerable
// for the shapes it already carries, where a type that does not have the
// selected attribute makes OpenTofu raise "This object does not have an
// attribute named …" and the configuration never runs. It is NOT tolerable
// for lookup(), whose whole job is to answer a missing key with the third
// argument, silently: a declared type that drops `cidr_blocks` would leave
// the module reading the DEFAULT while this rendered the caller's own
// expression into a cloud tag. So this route, and only this route, checks
// the declared type first ([declaredSelectionIsIdentity]) and declines
// unless prepareFinalInputVariableValue's conversion is the identity
// function all the way down to the selected leaf. Being stricter than the
// literal-index path costs resolutions and cannot cost a marker, which is
// the direction HANDOFF.md's safety rule points.

// foldedSelect is [resolver.resolveExpr]'s last route, and the entry point
// for everything above.
//
// It answers only with a resolution. Every declining path restores r.diags
// and r.pendingSiblingApply to what they were on entry, so the refusal the
// caller already recorded is the one that stands - never a second, vaguer one
// stapled on top of it.
func (r *resolver) foldedSelect(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	syn, isSyn := expr.(hclsyntax.Expression)
	if !isSyn {
		return nil, false
	}

	mark, sibMark := len(r.diags), len(r.pendingSiblingApply)
	decline := func() ([]Part, bool) {
		r.diags = r.diags[:mark]
		r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
		return nil, false
	}

	trav, folded := r.foldSelection(syn, scope, ident, 0)
	if !folded || len(trav) < 2 {
		return decline()
	}
	root := trav.RootName()
	if root != "local" && root != "var" && root != "module" {
		// namedLeaf's own set, and for its reasons: every other root is
		// either evaluable on its own or has a diagnostic of its own.
		return decline()
	}
	nameStep, isAttr := trav[1].(hcl.TraverseAttr)
	if !isAttr {
		return decline()
	}
	rest := trav[2:]

	var parts []Part
	var resolved, applicable bool
	if root == "module" {
		// An output has no declared type, so nothing converts what is read
		// out of it and there is nothing for the gate below to check.
		parts, resolved, applicable = r.resolveModuleOutput(nameStep.Name, rest, ident)
	} else {
		parts, resolved, applicable = r.foldedNamed(root, nameStep.Name, rest, scope, ident)
	}
	if !applicable || !resolved {
		return decline()
	}
	return parts, true
}

// foldedNamed is [resolver.resolveNamed] with the declared-type gate this
// route owes, and is otherwise the same two steps in the same order.
func (r *resolver) foldedNamed(root, name string, rest []hcl.Traverser, scope instScope, ident configs.StaticIdentifier) ([]Part, bool, bool) {
	defExpr, defScope, decl, restore, ok := r.namedDef(root, name, scope)
	if !ok {
		return nil, false, false
	}
	defer restore()
	if !declaredSelectionIsIdentity(decl, rest) {
		return nil, false, false
	}
	return r.selectStatic(defExpr, rest, defScope, ident, 0)
}

// foldSelection turns an expression into the absolute traversal it selects,
// computing whatever the expression spells some other way.
//
// Three things are folded, and nothing else:
//
//   - a bare traversal, which hcl.AbsTraversalForExpr already answers and
//     which is returned unchanged;
//   - an index whose key this package can evaluate for THIS instance -
//     count.index, each.key, a local, a literal - which becomes a
//     hcl.TraverseIndex step;
//   - lookup(<one of the above>, "<a static key>", <default>), which becomes
//     a hcl.TraverseAttr step, exactly as [resolver.eachValueSelector] folds
//     the same call for each.value.
//
// A two-argument lookup() is deliberately not folded: it RAISES on a missing
// key rather than answering with a fallback, so "the element does not have
// it" is a different outcome there, and this function would flatten the two.
func (r *resolver) foldSelection(expr hclsyntax.Expression, scope instScope, ident configs.StaticIdentifier, depth int) (hcl.Traversal, bool) {
	if depth > maxStaticDecomposeDepth {
		return nil, false
	}
	if call, isCall := expr.(*hclsyntax.FunctionCallExpr); isCall {
		if call.Name != "lookup" || len(call.Args) != 3 || call.ExpandFinal {
			return nil, false
		}
		base, baseOK := r.foldTraversal(call.Args[0], scope, ident, depth+1)
		if !baseOK {
			return nil, false
		}
		key, keyOK := r.staticString(call.Args[1], scope, ident)
		if !keyOK {
			return nil, false
		}
		return appendStep(base, hcl.TraverseAttr{Name: key, SrcRange: call.Args[1].Range()}), true
	}
	return r.foldTraversal(expr, scope, ident, depth)
}

// foldTraversal is foldSelection's index half, recursive because an index may
// sit under an attribute step and under another index.
func (r *resolver) foldTraversal(expr hclsyntax.Expression, scope instScope, ident configs.StaticIdentifier, depth int) (hcl.Traversal, bool) {
	if depth > maxStaticDecomposeDepth {
		return nil, false
	}
	if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() {
		return trav, true
	}
	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return r.foldTraversal(e.Expression, scope, ident, depth+1)

	case *hclsyntax.IndexExpr:
		base, baseOK := r.foldTraversal(e.Collection, scope, ident, depth+1)
		if !baseOK {
			return nil, false
		}
		key, keyOK := r.staticStepKey(e.Key, scope, ident)
		if !keyOK {
			return nil, false
		}
		return appendStep(base, hcl.TraverseIndex{Key: key, SrcRange: e.SrcRange}), true

	case *hclsyntax.RelativeTraversalExpr:
		base, baseOK := r.foldTraversal(e.Source, scope, ident, depth+1)
		if !baseOK {
			return nil, false
		}
		out := make(hcl.Traversal, 0, len(base)+len(e.Traversal))
		out = append(out, base...)
		out = append(out, e.Traversal...)
		return out, true
	}
	return nil, false
}

// appendStep copies rather than appending in place: the base traversal is
// very often hcl.AbsTraversalForExpr's own slice over an expression this
// resolver will read again, and growing it in place would let one fold's
// step land in another's reading of the same expression.
func appendStep(base hcl.Traversal, step hcl.Traverser) hcl.Traversal {
	out := make(hcl.Traversal, 0, len(base)+1)
	out = append(out, base...)
	return append(out, step)
}

// staticStepKey evaluates an index key into the traversal step it names,
// leaving no diagnostic behind when it cannot - the same rollback
// [resolver.staticIndex] and [resolver.staticString] use, for the same
// reason: a failure here means "this shape is not one this route reads", not
// "this configuration is wrong".
//
// The two key kinds are the two [stepKeyString] and [stepIndexInt] read, and
// they are the two every resource expansion in this package already produces.
// A marked key is refused rather than unmarked: a step decides which element
// of a caller's list becomes an identity, and an identity is written to a
// cloud tag in plaintext.
func (r *resolver) staticStepKey(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (cty.Value, bool) {
	if r.isSymbolic(expr, scope) {
		return cty.NilVal, false
	}
	mark := len(r.diags)
	val, ok := r.evalStatic(expr, scope, ident)
	r.diags = r.diags[:mark]
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsKnown() || val.IsMarked() {
		return cty.NilVal, false
	}
	switch val.Type() {
	case cty.String, cty.Number:
		return val, true
	}
	return cty.NilVal, false
}

// declaredSelectionIsIdentity reports whether reading rest out of a variable
// declared as decl gives back exactly what the CALLER wrote there.
//
// OpenTofu never uses a module call's argument as written: it applies the
// variable's optional-attribute defaults and converts the whole value to the
// declared type before anything inside the module reads it
// (prepareFinalInputVariableValue). Rendering the caller's own expression is
// therefore sound only where that conversion is the identity function on the
// leaf being selected - which is [preservedExpr]'s rule (#301), applied here
// along a path of steps rather than to one element.
//
// The two permissive answers are the two that convert nothing: no declaration
// at all (a local, or a variable this module does not declare) and a
// declaration with no type constraint (`variable "x" {}`, whose
// ConstraintType is cty.DynamicPseudoType). Anything else has to land on a
// string or on a dynamic position after every step, and an object type that
// does not have the selected attribute answers false rather than "absent" -
// this route never falls back, so the distinction costs nothing here.
func declaredSelectionIsIdentity(decl *configs.Variable, rest []hcl.Traverser) bool {
	if decl == nil {
		return true
	}
	ty := decl.ConstraintType
	if ty == cty.NilType || ty == cty.DynamicPseudoType {
		return true
	}
	leaf, ok := typeAtSteps(ty, rest)
	if !ok {
		return false
	}
	return leaf == cty.String || leaf == cty.DynamicPseudoType
}

// typeAtSteps walks a type constraint along a traversal, answering with the
// type at the end of it, and declines for any step the type does not settle.
//
// A dynamic position short-circuits: nothing below an `any` is constrained,
// so nothing below it is converted either. A SET declines outright - a set
// has no positions to index and OpenTofu rejects an index into one - and so
// does every primitive with steps still owed.
func typeAtSteps(ty cty.Type, rest []hcl.Traverser) (cty.Type, bool) {
	for _, step := range rest {
		if ty == cty.NilType || ty == cty.DynamicPseudoType {
			return cty.DynamicPseudoType, true
		}
		switch {
		case ty.IsObjectType():
			name, ok := stepKeyString(step)
			if !ok || !ty.HasAttribute(name) {
				return cty.NilType, false
			}
			ty = ty.AttributeType(name)
		case ty.IsMapType():
			if _, ok := stepKeyString(step); !ok {
				return cty.NilType, false
			}
			ty = ty.ElementType()
		case ty.IsListType():
			if _, ok := stepIndexInt(step); !ok {
				return cty.NilType, false
			}
			ty = ty.ElementType()
		case ty.IsTupleType():
			i, ok := stepIndexInt(step)
			if !ok || i < 0 || i >= ty.Length() {
				return cty.NilType, false
			}
			ty = ty.TupleElementType(i)
		default:
			return cty.NilType, false
		}
	}
	return ty, true
}
