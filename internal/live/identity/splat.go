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
//   - element(<splat>, <index>) - 21 sites at the time of this note, every
//     one of them in terraform-aws-modules/vpc and every one with
//     count.index somewhere in the index expression. That is an index
//     SELECTION into a multi-element list, a different rule from arity
//     collapse, and GitHub issue #321 gave it its own resolver
//     (resolveElementCall, below) once a real crossing
//     (corpus-security-group-complete) reached it for real: both operands
//     are tagged, admitted resources, and element(R[*].attr, idx) names the
//     same live object element() itself would pick at apply time, for
//     every idx, by element()'s own wraparound definition - not a claim
//     that needs an injectivity proof at all, unlike a value written into a
//     tag.
//
//     What #321 did NOT settle, and left as a real, separately-scoped gap:
//     internal/live/lint/count_index.go's RuleCountIndex refuses
//     count.index inside ANY collection-accessor call (element, lookup,
//     slice, chunklist) on sight, whatever the collection is, and its
//     second-chance domain check (count_index_domain.go) only ever renders
//     a var/local/path/terraform-rooted collection - a splat over a managed
//     resource never qualifies, so it stays "unprovable" and the hit
//     stands. That lint pass runs, and gates the whole plan, BEFORE
//     resolution - so a configuration where the enclosing resource's own
//     count is knowable without a data read hits RuleCountIndex first and
//     never reaches resolveElementCall at all. It is what vpc/examples/
//     issues (177 count-index sites, including the same main.tf:348/351
//     positions) still hits. resolveElementCall only fires in the config
//     where a real crossing found it because that block's own count is
//     itself gated behind a data source's value (#313), which lint's
//     earlier, data-read-free scan cannot see either - so lint treats the
//     block as having no instances at all (admission.go's
//     blockHasNoInstances) and skips it, and resolution runs afterward,
//     once the data has actually been read. Teaching count_index.go that
//     "index selects a sibling INSTANCE via marker" is a different
//     claim than "index selects a VALUE that must differ" is real,
//     generalizing work, not attempted here - it also has a genuine open
//     question resolveElementCall does not: whether an argument that maps
//     several sibling instances onto the SAME parent (var.single_nat_gateway
//     ? 0 : count.index, when true) is still safe once considered alongside
//     the OTHER identity component that always does vary, which
//     count_index.go currently has no way to see, checking one argument at
//     a time.
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

// resolveElementCall recognizes element(R[*].attr, idx) - a splat over a
// managed resource picked by element()'s own wraparound indexing - and
// resolves it the way a direct indexed traversal (R[idx].attr,
// [resolver.resolveIndexedTraversal]) already does: element(R[*].attr, idx)
// and R[idx % len(R)].attr name the same live object, for every idx, by
// element()'s own definition (github.com/zclconf/go-cty/cty/function/stdlib's
// ElementFunc: `index = index % l; if index < 0 { index += l }`), so this is
// that same resolution reached through the second spelling rather than new
// machinery. idx is evaluated once, against the current instance's own scope
// ([resolver.evalStatic] - the same call [resolver.resolveIndexedTraversal]
// makes for idx.Key, so count.index, a conditional over it
// (var.single_nat_gateway ? 0 : count.index), and every other shape that
// scope already answers resolve exactly as they would spelled as a bare
// index), then wrapped modulo R's own instance count and handed to
// [resolver.parentPart].
//
// This is deliberately NOT the arity-collapse rule above: that rule exists
// because join/one need the list to be exactly one element long for a
// separator or a "no duplicates" claim to be moot. element() picks one
// element out of a list of any length by position, which is a different and
// unconditional claim - every index, wrapped, names exactly one instance -
// so no arity restriction applies here at all.
//
// applicable is false whenever the shape is not this at all: not element(),
// the wrong argument count, or the first argument not a splat over a bare
// managed resource selecting a single attribute - [resolver.splatTargets]'
// own restriction, the same one the arity-collapse rule above relies on.
// The caller's own "cannot follow" diagnostic stands unreplaced in those
// cases. When applicable is true, ok reports whether resolution succeeded,
// and a diagnostic has already been recorded in its place when it did not -
// either by this function directly, or by whatever evaluated the index or
// the source resource's own expansion.
func (r *resolver) resolveElementCall(call *hclsyntax.FunctionCallExpr, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	if call.Name != "element" || len(call.Args) != 2 {
		return nil, false, false
	}

	splat, isSplat := call.Args[0].(*hclsyntax.SplatExpr)
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
	if len(insts) == 0 {
		r.errorf(call.Range(), "Identity not resolvable from configuration",
			"%s picks an element from a list built from another resource with element(), but that resource expands to no instances at all, so there is nothing to pick: element() itself errors on an empty list at apply time.",
			ident.Subject)
		return nil, false, true
	}

	idxVal, idxOK := r.evalStatic(call.Args[1], scope, ident)
	if !idxOK {
		// evalStatic already recorded why.
		return nil, false, true
	}
	idx, idxIsInt := elementIndexValue(idxVal)
	if !idxIsInt {
		r.errorf(call.Args[1].Range(), "Identity not resolvable from configuration",
			"%s calls element() with an index that is not a whole number, so it cannot select one of the source resource's instances.",
			ident.Subject)
		return nil, false, true
	}

	// element()'s own wraparound - see this function's doc comment.
	n := len(insts)
	wrapped := idx % n
	if wrapped < 0 {
		wrapped += n
	}

	got, gotOK := r.parentPart(insts[wrapped], attrName, call.Range(), ident)
	return got, gotOK, true
}

// resolveConcatIndex recognizes concat(A[*].attr, B[*].attr, ...,
// [literal, ...])[N] - a list built by concatenating zero or more splats
// over managed resources with zero or more literal-list arguments, then
// picked apart by a single index - and resolves it the way
// [resolver.resolveElementCall] resolves element(R[*].attr, idx): N is
// evaluated once against the current instance's own scope, and each
// argument's own contribution to the flattened list is exactly as many
// elements as that argument's own length. For a splat, that length is the
// source resource's own instance count ([resolver.expansionFor], through
// [resolver.splatTargets] - the same machinery every other rule in this file
// uses); for a literal list ([hclsyntax.TupleConsExpr], the `[...]` syntax)
// it is simply the number of elements written. Summing those lengths in
// argument order locates which argument N falls into and at what position
// within it, and that argument's own element at that position - a resource
// instance's attribute, or whatever that literal-list element turns out to
// be - is the answer: a splat position resolves through
// [resolver.parentPart], exactly as resolveElementCall's does, and a
// literal-list position resolves through [resolver.resolveExpr] on that one
// element, which already knows how to turn a plain literal into a Part and
// would equally resolve a resource reference sitting in that slot, without
// this function needing its own copy of that logic.
//
// This is the same claim resolveElementCall's own doc comment makes for
// element(): concat(...)[N] and a direct reference to whichever source
// argument's element N provably lands on name the same value, for every N
// that is provably in range, by concat()'s own definition - it does nothing
// but flatten its arguments into one list, so this is not a claim that needs
// an injectivity proof, unlike a value written into a tag.
//
// applicable is false whenever the shape is not this at all: not an index
// into a concat() call. Once applicable, an argument this package cannot
// size without reading the cloud (anything but a recognized splat or a
// literal list), an index that is not a known non-negative whole number, or
// an index this package can prove is out of range given every argument's
// provable length, is a resolution failure (ok=false) with its own specific
// diagnostic recorded - the same contract every other rule in this file
// follows.
//
// expr arrives as one of two different node shapes for the same surface
// syntax, and both are handled here rather than only the more obvious one.
// HCL's parser folds a constant index directly into a traversal step - the
// same folding that makes R[0].attr and R.attr both parse as plain
// traversals - so concat(...)[0] (the shape this package actually needs;
// #324's own local.this_sg_id uses a literal 0) parses as a
// *hclsyntax.RelativeTraversalExpr whose Source is the concat() call and
// whose one-element Traversal is a single hcl.TraverseIndex, NOT as a
// *hclsyntax.IndexExpr. A non-constant index such as concat(...)[count.index]
// cannot be folded that way and does produce a genuine *hclsyntax.IndexExpr.
// Both are accepted so a caller reaching either shape gets the same
// resolution; a RelativeTraversalExpr carrying anything beyond that one
// index step (a trailing .attr, selecting into a sub-object of whatever
// element concat() picked) is left to applicable=false, unhandled.
func (r *resolver) resolveConcatIndex(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	var collExpr hclsyntax.Expression
	var keyExpr hcl.Expression
	switch e := expr.(type) {
	case *hclsyntax.IndexExpr:
		collExpr, keyExpr = e.Collection, e.Key
	case *hclsyntax.RelativeTraversalExpr:
		if len(e.Traversal) != 1 {
			return nil, false, false
		}
		idxStep, isIdx := e.Traversal[0].(hcl.TraverseIndex)
		if !isIdx {
			return nil, false, false
		}
		src, isExpr := e.Source.(hclsyntax.Expression)
		if !isExpr {
			return nil, false, false
		}
		collExpr = src
		keyExpr = &hclsyntax.LiteralValueExpr{Val: idxStep.Key, SrcRange: idxStep.SrcRange}
	default:
		return nil, false, false
	}

	call, isCall := collExpr.(*hclsyntax.FunctionCallExpr)
	if !isCall || call.Name != "concat" || len(call.Args) == 0 {
		return nil, false, false
	}

	idxVal, idxOK := r.evalStatic(keyExpr, scope, ident)
	if !idxOK {
		// evalStatic already recorded why.
		return nil, false, true
	}
	// elementIndexValue is reused rather than indexKeyValue: both accept a
	// number, but a list index (unlike a resource instance key) is never a
	// string, and this function needs the plain int to walk arguments below
	// - indexKeyValue hands back an addrs.InstanceKey instead. Negative is
	// rejected explicitly next, unlike element()'s own caller: a plain [N]
	// index does not wrap around the way element()'s does.
	idx, idxIsInt := elementIndexValue(idxVal)
	if !idxIsInt {
		r.errorf(keyExpr.Range(), "Identity not resolvable from configuration",
			"%s indexes concat() with a value that is not a whole number, so it cannot select one of its elements.",
			ident.Subject)
		return nil, false, true
	}
	if idx < 0 {
		r.errorf(keyExpr.Range(), "Identity not resolvable from configuration",
			"%s indexes concat() with a negative index (%d). Unlike element(), a plain [N] index does not wrap around and errors at apply time.",
			ident.Subject, idx)
		return nil, false, true
	}

	remaining := idx
	total := 0
	for _, argExpr := range call.Args {
		if splat, isSplat := argExpr.(*hclsyntax.SplatExpr); isSplat {
			insts, attrName, instOK, instApplicable := r.splatTargets(splat)
			if instApplicable {
				if !instOK {
					// The resource's own expansion already failed and
					// already carries a diagnostic - see
					// [resolver.expansionFor].
					return nil, false, true
				}
				if remaining < len(insts) {
					got, gotOK := r.parentPart(insts[remaining], attrName, argExpr.Range(), ident)
					return got, gotOK, true
				}
				remaining -= len(insts)
				total += len(insts)
				continue
			}
			// A splat splatTargets cannot decompose (a multi-step per-item
			// traversal, a splat over something other than a bare managed
			// resource) falls through to the generic "unrecognized argument"
			// refusal below, exactly as any other unclassifiable argument
			// does: this package does not know how many elements it
			// contributes, so it cannot locate N through it either.
		} else if tuple, isTuple := argExpr.(*hclsyntax.TupleConsExpr); isTuple {
			if remaining < len(tuple.Exprs) {
				got, gotOK := r.resolveExpr(tuple.Exprs[remaining], scope, ident)
				return got, gotOK, true
			}
			remaining -= len(tuple.Exprs)
			total += len(tuple.Exprs)
			continue
		}

		r.errorf(argExpr.Range(), "Identity not resolvable from configuration",
			"%s builds an identity from concat(), but one of its arguments is neither a splat over a managed resource nor a literal list, so how many elements it contributes to the combined list is not known without reading the cloud.",
			ident.Subject)
		return nil, false, true
	}

	r.errorf(keyExpr.Range(), "Identity not resolvable from configuration",
		"%s indexes concat() at position %d, but its arguments provably contribute only %d element(s) in total, so this index is out of range and would error at apply time.",
		ident.Subject, idx, total)
	return nil, false, true
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
