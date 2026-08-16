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

// This file is one rule about WHICH ARGUMENT of try() the language selects,
// not a rule about what try() computes. It is the same move
// [resolver.resolveConditional] makes for `cond ? A : B` - decide the
// selection statically, then hand the selected sub-expression straight back
// to [resolver.resolveExpr] so every existing rule applies to it unchanged -
// with one difference: what selects an argument here is not a condition
// written by the author but whether the argument would raise an error, and
// for the arguments this package can decide, that is settled by resource
// expansion, which [resolver.expansionFor] has already computed from count
// or for_each before any identity is built.
//
// The two verdicts, and why each is sound:
//
//   - An argument that references one instance of a managed resource whose
//     expansion HAS that key cannot error. OpenTofu's try (hcl/v2's
//     ext/tryfunc) moves past an argument only when evaluating it produces
//     an error; an unknown value is explicitly NOT an error there - the
//     comment on that branch is "we need to be conservative and return a
//     dynamic value" - so the not-yet-created attribute a plan sees does not
//     cause a fall-through. The value the run finally settles on is that
//     argument's.
//
//   - An argument that indexes a managed resource with a key its expansion
//     does NOT have raises "Invalid index" every time it is evaluated, at
//     plan and at apply alike, because the expansion is fixed by a count or
//     for_each this package has already required to be statically known. try
//     therefore moves past it, and the next argument is what the language
//     evaluates.
//
// Everything else is undecidable here and says so by returning applicable
// false, which leaves the caller's own "cannot follow" refusal standing
// exactly as it did before this file existed. In particular an argument
// reaching more than one attribute step past the instance
// (R[0].tags["env"]) is not decided as succeeding: a known map missing that
// key errors at APPLY, after a plan that saw the map as unknown and could
// not tell, so committing to it would be this package guessing.
//
// That last restriction is redundancy rather than the only thing holding the
// line, and saying so is more useful than implying otherwise: an argument
// with more than one step past the instance is refused by
// [resolver.resolveTraversal]'s own "a single attribute of another resource"
// rule even if this file hands it over as selected. Mutating the restriction
// away leaves every test here green, because both paths refuse. The two
// decisions that ARE load-bearing, and that a mutation does break, are that
// an argument proved to error must not be selected and that an undecidable
// argument must not fall through to the next one.
//
// Measured against live/corpus-manifest.json, try() heads exactly 4 refused
// identity sites, and this moves all 4 - three in terraform-aws-modules/lambda
// (modules/deploy/main.tf:6, :213, :270) and one in terraform-aws-modules/vpc
// (main.tf:19). The vpc one is the fall-through direction and the three
// lambda ones the first-argument direction, so both verdicts are exercised
// by real configuration rather than only by fixtures. No other function is
// recognized: coalesce() selects on null rather than on error and is a
// different question, and can() yields a bool, which is not an identity
// component at all.

// armVerdict is what this package can prove about one try() argument.
type armVerdict int

const (
	// armUndecidable is the default and the safe one: nothing here can say
	// whether the language would select this argument.
	armUndecidable armVerdict = iota

	// armErrors means evaluating this argument raises an error on every
	// run, so try() moves past it to the next.
	armErrors

	// armSelected means evaluating this argument cannot raise an error, so
	// try() stops here and this argument's value is the call's.
	armSelected
)

// resolveFallbackChain recognizes try(...) and resolves it to whichever of
// its arguments the language would select, when that selection is decidable
// from resource expansion alone.
//
// applicable is false whenever the shape is not this at all, or whenever any
// argument up to and including the selected one cannot be decided: the
// caller's own "cannot follow" diagnostic stands unreplaced, the same
// contract [resolver.resolveArityCollapse] and
// [resolver.resolveIndexedTraversal] have. When applicable is true, ok
// reports whether resolution succeeded and a diagnostic is already recorded
// in its place when it did not.
func (r *resolver) resolveFallbackChain(call *hclsyntax.FunctionCallExpr, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	if call.Name != "try" || len(call.Args) == 0 {
		return nil, false, false
	}

	for _, arm := range call.Args {
		verdict, expansionFailed := r.fallbackArmVerdict(arm, scope, ident)
		switch {
		case expansionFailed:
			// The referenced resource's own expansion already failed and
			// already carries a diagnostic, wherever that first happened -
			// see [resolver.expansionFor], and the same branch in
			// [resolver.resolveIndexedTraversal].
			return nil, false, true
		case verdict == armErrors:
			continue
		case verdict == armSelected:
			// Handed back whole, so parentPart's registered-IdentityAttrs
			// check, the provider-schema boundary in
			// [resolver.siblingLiteralExpr], and every other rule apply to
			// it exactly as they would had the author written this argument
			// on its own.
			got, gotOK := r.resolveExpr(arm, scope, ident)
			return got, gotOK, true
		default:
			return nil, false, false
		}
	}

	// Every argument provably errors, which means the call itself fails and
	// this configuration does not plan under stock OpenTofu either. Nothing
	// is claimed about it here.
	return nil, false, false
}

// fallbackArmVerdict decides one try() argument.
//
// expansionFailed distinguishes the one case where the caller must not fall
// back to its generic refusal: the argument does name a managed resource,
// but working out how many instances that resource has already failed and
// already produced a diagnostic of its own.
func (r *resolver) fallbackArmVerdict(arm hclsyntax.Expression, scope instScope, ident configs.StaticIdentifier) (verdict armVerdict, expansionFailed bool) {
	if !r.isSymbolic(arm, scope) {
		// No managed resource anywhere in it, so this package can evaluate
		// it outright - and an expression that evaluates is an expression
		// that did not error, which is the whole question. The probe leaves
		// no diagnostic behind either way: a failure here means "cannot
		// decide", not "this configuration is wrong", and the caller's own
		// refusal is the one that should be read. Same rollback
		// [resolver.soleElementFromValue] uses for the same reason.
		mark := len(r.diags)
		_, evalOK := r.evalStatic(arm, scope, ident)
		r.diags = r.diags[:mark]
		if evalOK {
			return armSelected, false
		}
		return armUndecidable, false
	}

	instAddr, attrOK := fallbackArmTarget(arm)
	if !attrOK {
		return armUndecidable, false
	}
	rc := r.mod.ResourceByAddr(instAddr.Resource)
	if rc == nil {
		return armUndecidable, false
	}
	exp, expOK := r.expansionFor(rc)
	if !expOK {
		return armUndecidable, true
	}
	if exp.hasKey(instAddr.Key) {
		return armSelected, false
	}
	return armErrors, false
}

// fallbackArmTarget decomposes a try() argument into the single managed
// resource instance it reads one attribute of.
//
// It accepts exactly what [resolver.resolveTraversal] resolves - a bare
// reference or a literal-keyed one, followed by one attribute step - and
// nothing more, so an argument this returns true for is an argument
// resolveExpr already knows how to turn into parts. Anything else is left
// undecidable: see the file comment on why one extra step past the
// attribute (R[0].tags["env"]) must not be treated as certain to succeed.
func fallbackArmTarget(arm hclsyntax.Expression) (addrs.ResourceInstance, bool) {
	var none addrs.ResourceInstance

	trav, diags := hcl.AbsTraversalForExpr(arm)
	if diags.HasErrors() {
		return none, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		return none, false
	}

	var instAddr addrs.ResourceInstance
	switch subject := ref.Subject.(type) {
	case addrs.Resource:
		instAddr = subject.Instance(addrs.NoKey)
	case addrs.ResourceInstance:
		instAddr = subject
	default:
		return none, false
	}
	if instAddr.Resource.Mode != addrs.ManagedResourceMode {
		return none, false
	}
	if len(ref.Remaining) != 1 {
		return none, false
	}
	if _, isAttr := ref.Remaining[0].(hcl.TraverseAttr); !isAttr {
		return none, false
	}
	return instAddr, true
}
