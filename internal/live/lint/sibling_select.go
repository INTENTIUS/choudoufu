// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
)

// This file closes the gap internal/live/identity/splat.go's own doc comment
// names and declines to attempt: RuleCountIndex refuses count.index inside
// any collection accessor on sight, whatever the collection is, so a
// configuration never reaches [identity.resolver.resolveElementCall] or
// [identity.resolver.resolveIndexedTraversal] - the two resolutions that
// already know exactly what those spellings mean.
//
// The wall it removes, measured on the estate that found it
// (corpus-eks-basic, terraform-aws-modules/vpc v6.6.1's main.tf:200/201 and
// 348/351):
//
//	subnet_id      = element(aws_subnet.public[*].id, count.index)
//	route_table_id = element(aws_route_table.public[*].id, <a conditional over count.index>)
//
// # The two questions are not the same question
//
// RuleCountIndex exists for one thing: a VALUE COMPUTED FROM THE INDEX and
// then written somewhere that names a cloud object. "name-${count.index % 3}"
// is that shape, and two instances colliding on one rendered string is a
// wrong marker, so the rule refuses everything it cannot prove injective
// (see [analyzeCountIndexSafety], and [countIndexDomain] for the exhaustive
// second chance).
//
// element(R[*].attr, idx) is not that shape. It computes nothing: it SELECTS
// one instance of a sibling managed resource and reads one attribute of it.
// What the identity layer builds from it is not a string this run rendered
// but an [identity.ParentRef] - a promise to read that instance's own
// identity once it is known - and R[idx].attr is the same selection spelled
// the other way, resolved by the same [identity.resolver.parentPart].
//
// # Why stepping aside here cannot write a wrong marker
//
// Because the exact question RuleCountIndex approximates is asked again,
// downstream, over the whole rendered identity rather than one argument at a
// time. [identity.resolver.checkCollisions] runs at the end of every
// resolution and reports two instances of one type that resolve to the same
// identity - comparing [identity.Formula.String] for a ClassParentDerived
// resolution and [identity.concreteIdentityKey] for a ClassConcrete one.
// Both are exact, both carry the parent's own instance key
// ([identity.ParentRef.String] renders `aws_subnet.private[1].id`), and both
// end in a named error rather than a silently adopted object.
//
// That is strictly stronger than what this rule can see, in the one direction
// splat.go singles out as the open question. vpc's own
//
//	route_table_id = element(aws_route_table.private[*].id, var.single_nat_gateway ? 0 : count.index)
//
// maps every association instance onto ONE route table when single_nat_gateway
// is true, which per-argument reasoning must refuse and which is nonetheless
// completely safe, because the OTHER component of the same identity - the
// subnet - still varies. checkCollisions sees both components; this rule sees
// one argument. So the answer is not to teach this rule to join arguments
// together, but to let the check that already joins them decide, and confine
// this one to the values it is actually about. A collapse that really does
// produce two identical identities is refused by name:
//
//	aws_route_table_association.a[0] and aws_route_table_association.a[1]
//	both resolve to the identity "${aws_subnet.x[0].id}/${aws_route_table.y[0].id}".
//
// # The gate, and what it is for
//
// This route applies only where the enclosing type's identity table row names
// its identity attributes - [countIndexScope] with neither skip nor walkAll.
// That is the population where every outcome is one of the two that make
// stepping aside safe. A row with Components resolves:
//
//   - ClassConcrete or ClassParentDerived, which checkCollisions compares
//     exactly, whole identity against whole identity;
//   - ClassNeedsDiscovery (a missing cloud property, a name_prefix, a
//     server-assigned-if-absent name) or ClassRecordLocated (an operator's
//     `markers = record` selection, #365), in both of which the identity does
//     not come from a configuration argument at all - it is discovered, or
//     read out of the record keyed by the instance's own address - so no
//     argument's count.index can reach it;
//   - or a refusal, which is loud.
//
// The two excluded populations are excluded for their own reasons and not by
// accident:
//
//   - skip is ServerAssigned and RECORD_ADMITTED, where the rule never fires
//     in the first place;
//   - walkAll is a type with no row, and EXTERNAL_ADMITTED. Both are the
//     "no data says which argument carries the object" default, and the
//     second is ClassRecordBacked, which checkCollisions does not compare.
//     Neither may lean on a downstream check that does not run for it.
//
// [checkChildModuleArgs] calls the same walk with walkAll, so a module-call
// argument is untouched here too: what a child module does with the value is
// not this claim.
//
// # Reach
//
// The rule names no resource type and reads no type name: it is keyed on the
// shape of the expression and on whether the enclosing type's row names its
// identity attributes at all. Measured against the pinned hashicorp/aws table
// by TestSiblingSelectionReachesEveryComponentRow, that is 574 of the 1042
// admitted rows; the other 468 are ServerAssigned, record-backed, logical, or
// carry no argument-reading component, and the rule never fired for them
// either. aws_route_table_association is where a real crossing found it.

// siblingInstanceSelection reports whether expr, taken as a whole, selects
// one attribute of one instance of a sibling MANAGED resource, with
// count.index reaching nothing but the position that chooses which instance.
//
// Two spellings are recognised, and they are exactly the two the identity
// resolver already resolves through [identity.resolver.parentPart]:
//
//	element(R[*].attr, <idx>)   resolveElementCall
//	R[<idx>].attr               resolveIndexedTraversal
//
// Anything else answers false, including the same selection wrapped in
// anything at all - a template, an arithmetic expression, an object or list
// constructor. Wrapping makes the result a computed value again, which is
// precisely what RuleCountIndex is about, and the caller's refusal stands.
//
// The collection half is required to be free of count.index, so that a
// nested selection whose own SOURCE varies with the index
// (element(R[count.index][*].attr, i), were it writable) is not quietly
// admitted by a check that only looked at the outer index.
func siblingInstanceSelection(expr hclsyntax.Expression) bool {
	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return siblingInstanceSelection(e.Expression)

	case *hclsyntax.FunctionCallExpr:
		// element()'s wraparound picks one element of the list by position;
		// over a splat of a managed resource that list IS the resource's
		// instances, so the pick is an instance. See
		// [identity.resolver.resolveElementCall].
		if e.Name != "element" || len(e.Args) != 2 || e.ExpandFinal {
			return false
		}
		splat, isSplat := e.Args[0].(*hclsyntax.SplatExpr)
		if !isSplat {
			return false
		}
		if referencesCountIndex(splat) {
			return false
		}
		return managedSplatAttr(splat)

	case *hclsyntax.RelativeTraversalExpr:
		// R[<idx>].attr: hclsyntax builds an IndexExpr the moment the index
		// is not a constant, with the trailing ".attr" as the relative
		// traversal. See [identity.resolver.resolveIndexedTraversal].
		if len(e.Traversal) != 1 {
			return false
		}
		if _, isAttr := e.Traversal[0].(hcl.TraverseAttr); !isAttr {
			return false
		}
		idx, isIndex := e.Source.(*hclsyntax.IndexExpr)
		if !isIndex {
			return false
		}
		if referencesCountIndex(idx.Collection) {
			return false
		}
		return managedResourceTraversal(idx.Collection)
	}
	return false
}

// managedSplatAttr reports whether e is a splat over a bare managed resource
// selecting exactly one attribute - R[*].attr and its legacy R.*.attr
// spelling, which hclsyntax parses into the same node.
//
// It is [identity.resolver.splatTargets]' own restriction, stated here in the
// terms this package has: an identity can be built from a single attribute of
// another resource, so R.*.tags.Name is not this shape.
func managedSplatAttr(e *hclsyntax.SplatExpr) bool {
	rel, isRel := e.Each.(*hclsyntax.RelativeTraversalExpr)
	if !isRel || rel.Source != hclsyntax.Expression(e.Item) || len(rel.Traversal) != 1 {
		return false
	}
	if _, isAttr := rel.Traversal[0].(hcl.TraverseAttr); !isAttr {
		return false
	}
	return managedResourceTraversal(e.Source)
}

// managedResourceTraversal reports whether expr is a bare traversal naming a
// managed resource and nothing further - aws_subnet.private, not
// aws_subnet.private.id, not data.aws_subnet.x, not var.subnets.
//
// A data source is deliberately not this shape: its instances are not objects
// this estate owns, so "which instance was selected" is not a claim about a
// live object with an identity of its own.
func managedResourceTraversal(expr hclsyntax.Expression) bool {
	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		return false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() || ref == nil || len(ref.Remaining) > 0 {
		return false
	}
	res, isRes := ref.Subject.(addrs.Resource)
	return isRes && res.Mode == addrs.ManagedResourceMode
}

// referencesCountIndex reports whether expr reaches count.index anywhere,
// through the generic Variables() walk every hclsyntax expression implements
// - the same containment test [analyzeCountIndexSafety]'s own default branch
// falls back on.
func referencesCountIndex(expr hclsyntax.Expression) bool {
	return len(countIndexOnlyTraversals(expr.Variables())) > 0
}
