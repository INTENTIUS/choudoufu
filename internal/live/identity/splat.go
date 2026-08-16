// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// This file is the second half of GitHub issue #196's corpus slice, and it
// is one rule about ARITY, not a rule about functions.
//
// A splat over a managed resource - aws_security_group.cluster.*.id, or the
// modern aws_security_group.cluster[*].id - is a list with exactly as many
// elements as that resource has instances, and this package already knows
// how many that is: [resolver.expansionFor] computes the instance keys of
// every resource in the module before any identity is built, from count or
// for_each alone. So when the expansion has exactly one instance the splat
// is a one-element list, and a one-element list has no "between" for a
// separator to appear in: join(<any separator>, [x]) is x, and one([x]) is
// x. That is the whole rule.
//
// What it is NOT is a claim about join(). The separator is never evaluated
// and never consulted, because at arity one its value cannot affect the
// result; join("-", ...) resolves exactly as join("", ...) does, and a test
// asserts that. What join() and one() contribute is only the second half of
// the shape: they are the two spellings OpenTofu has for "collapse this
// collection to a scalar" that this package can currently reach. Two named
// functions is a real cost and worth stating plainly rather than dressing
// up: measured against live/corpus-manifest.json, this rule moves 60 sites,
// all of them join("", <splat>) in terraform-aws-modules/eks's local.tf,
// and zero one() sites - one() is here because it is the same arity claim,
// spelled shorter, not to make the rule look broader than it is.
//
// The 60 are twelve identical sites in each of five eks examples, and they
// go away rather than reappearing under another label - which is not a
// given, since #196's first half moved fourteen sites from "Identity not
// resolvable from configuration" to "Unresolvable identity" underneath and
// netted zero. Run per entry with hashicorp/aws schemas, basic goes from 91
// sites to 79, irsa 75 to 63, launch_templates 76 to 64,
// managed_node_groups 75 to 63, spot_instances 76 to 64, with every other
// refusal count in each entry bit-identical, "Unresolvable identity"
// included. The parents these references land on
// (aws_security_group.cluster[0], aws_iam_role.cluster[0]) are named
// through name_prefix, so they are ServerAssigned and the rules reading
// them resolve parent-derived. None of the five becomes unblocked: each
// keeps 63 to 79 sites from unrelated refusals.
//
// The measured shapes it deliberately does NOT move, from the same probe:
//
//   - element(<splat>, <index>) - 21 sites, every one of them in
//     terraform-aws-modules/vpc and every one with count.index somewhere in
//     the index expression. That is an index SELECTION into a multi-element
//     list, a different rule, and it has to clear the same injectivity
//     analysis internal/live/lint/count_index.go performs before it can be
//     allowed anywhere near an identity. That lint already fires at those
//     exact lines - vpc/examples/issues carries 177 count-index sites,
//     including main.tf:348 and main.tf:351, the two element() index
//     positions on aws_route_table_association.private - so resolving
//     element() without settling injectivity first would remove 21 identity
//     sites and unblock nothing. Nothing here opens that door: the arity
//     check below refuses every splat that is not one element long,
//     whatever is wrapped around it.
//   - join(".", reverse(split(".", aws_instance.x.private_ip))) - 6 sites in
//     cisagov/cyhy-amis. The argument is not a splat and the value genuinely
//     is not known until apply.
//
// The refusals are the existing "Identity not resolvable from
// configuration" summary, reached one more way, exactly as
// [resolver.resolveConditional] does - no new registry entry, because it is
// not a new rule, only a more precise account of the same one.

// resolveArityCollapse recognizes an expression that reduces a splat over a
// managed resource to a single scalar - join(sep, R.*.attr) and one(R.*.attr)
// - and resolves it to that one instance's attribute when, and only when,
// the resource provably expands to exactly one instance.
//
// applicable is false whenever the shape is not this at all: another
// function, the wrong argument count, an argument that is not a splat, a
// splat over something that is not a bare managed resource, or a splat
// selecting anything but a single attribute. The caller's own "cannot
// follow" diagnostic stands unreplaced in those cases - the same contract
// [resolver.resolveIndexedTraversal] has. When applicable is true, ok
// reports whether resolution succeeded and a diagnostic is already
// recorded in its place when it did not.
func (r *resolver) resolveArityCollapse(call *hclsyntax.FunctionCallExpr, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	var coll hclsyntax.Expression
	switch call.Name {
	case "join":
		if len(call.Args) != 2 {
			return nil, false, false
		}
		// The separator is deliberately not evaluated: at arity one it
		// cannot reach the result. It is still required to be free of
		// managed-resource references, so that an expression this package
		// would refuse to evaluate anywhere else does not become invisible
		// by sitting in a position whose value happens not to matter.
		if r.isSymbolic(call.Args[0], scope) {
			return nil, false, false
		}
		coll = call.Args[1]
	case "one":
		if len(call.Args) != 1 {
			return nil, false, false
		}
		coll = call.Args[0]
	default:
		return nil, false, false
	}

	splat, isSplat := coll.(*hclsyntax.SplatExpr)
	if !isSplat {
		return nil, false, false
	}
	insts, attrName, instOK, instApplicable := r.splatTargets(splat)
	if !instApplicable {
		return nil, false, false
	}
	if !instOK {
		// The resource's own expansion already failed and already carries a
		// diagnostic - see [resolver.expansionFor].
		return nil, false, true
	}

	if len(insts) != 1 {
		r.refuseSplatArity(splat, len(insts), call.Name, ident)
		return nil, false, true
	}
	got, gotOK := r.parentPart(insts[0], attrName, coll.Range(), ident)
	return got, gotOK, true
}

// splatTargets decomposes a splat expression into the resource instances it
// iterates and the single attribute it selects from each.
//
// It accepts both spellings - the modern R[*].attr and the legacy R.*.attr,
// which hclsyntax parses into the same *hclsyntax.SplatExpr node - and
// restricts the per-item clause to exactly one attribute step, the same
// restriction [resolver.resolveIndexedTraversal] already places on an
// indexed reference: an identity can be built from a single attribute of
// another resource, so R.*.tags.Name is not this shape and is left to the
// caller's generic refusal.
//
// The instance order is the resource's own expansion order. A resource
// keyed by strings (for_each) is refused as not-applicable rather than
// ordered arbitrarily: OpenTofu does not accept a splat over a map of
// instances either, and inventing an order for one here would be this
// package asserting something the language does not.
func (r *resolver) splatTargets(e *hclsyntax.SplatExpr) (insts []addrs.AbsResourceInstance, attrName string, ok bool, applicable bool) {
	rel, isRel := e.Each.(*hclsyntax.RelativeTraversalExpr)
	if !isRel || rel.Source != hclsyntax.Expression(e.Item) || len(rel.Traversal) != 1 {
		return nil, "", false, false
	}
	attrStep, isAttr := rel.Traversal[0].(hcl.TraverseAttr)
	if !isAttr {
		return nil, "", false, false
	}

	trav, diags := hcl.AbsTraversalForExpr(e.Source)
	if diags.HasErrors() {
		return nil, "", false, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		return nil, "", false, false
	}
	resAddr, isRes := ref.Subject.(addrs.Resource)
	if !isRes || len(ref.Remaining) > 0 || resAddr.Mode != addrs.ManagedResourceMode {
		return nil, "", false, false
	}
	rc := r.mod.ResourceByAddr(resAddr)
	if rc == nil {
		return nil, "", false, false
	}

	exp, expOK := r.expansionFor(rc)
	if !expOK {
		return nil, "", false, true
	}
	for _, k := range exp.keys {
		switch k.(type) {
		case addrs.IntKey:
		case nil:
			// addrs.NoKey: the single instance of an unrepeated resource,
			// which a splat wraps into a one-element list.
		default:
			return nil, "", false, false
		}
	}
	for _, k := range exp.keys {
		insts = append(insts, resAddr.Instance(k).Absolute(r.modInst))
	}
	return insts, attrStep.Name, true, true
}

// refuseSplatArity is the arity refusal, and it is the only thing standing
// between this rule and an identity built from several live objects' values
// concatenated together. n is never 1 here.
func (r *resolver) refuseSplatArity(splat *hclsyntax.SplatExpr, n int, fn string, ident configs.StaticIdentifier) {
	if n == 0 {
		r.errorf(splat.Range(), "Identity not resolvable from configuration",
			"%s reduces a list of another resource's attributes to one value with %s(), but that resource expands to no instances at all, so the result is the empty string. An empty string names no live object, and writing one into an identity would claim ownership of nothing while looking like an ordinary answer.",
			ident.Subject, fn)
		return
	}
	r.errorf(splat.Range(), "Identity not resolvable from configuration",
		"%s reduces a list of another resource's attributes to one value with %s(), but that resource expands to %d instances. A one-element list collapses to its one element whatever separator is used, which is the case this can resolve; with %d, the result is several live objects' values run together, which names none of them.",
		ident.Subject, fn, n, n)
}
