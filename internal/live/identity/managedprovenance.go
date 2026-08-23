// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the provenance half of GitHub issue #187's second pass: given
// an identity argument that evaluated to an unknown, WHERE did the unknown
// come from?
//
// The question has to be asked because two very different situations produce
// the same cty.UnknownVal at [resolver.stringValue]:
//
//   - The caller performed a managed read or a managed plan this run, and the
//     value it handed back is unknown until the sibling resource is applied.
//     aws_acm_certificate's domain_validation_options is the carrier: the AWS
//     provider fills each element's domain_name during PlanResourceChange and
//     leaves resource_record_name unknown. Measured against the real provider
//     in internal/live/projection's TestPlanInstancesAgainstTheAWSProvider.
//
//   - internal/live/check's loader substituted cty.UnknownVal for a required
//     root variable nothing set (check/load.go's variableValues.value). That
//     is the #183 cohort, which must stay refused, and it is the path
//     tools/refusal-probe, "just corpus" and live-check all take.
//
// cty cannot tell them apart. Marks were tried and refuted - IsMarked() is
// this fork's panic guard in roughly seventy places and internal/live/marksafe
// is a static prover requiring exactly that shape, so a second mark kind is
// indistinguishable from a sensitivity mark to every one of those guards.
//
// So the discriminator is structural, and it is deliberately NARROW. Three
// things must all hold before an unknown is attributed to a managed read, and
// the first of them makes the whole classification inert for any run that
// supplied no managed results at all - which is every offline corpus run:
//
//  1. This run was given [Context.ManagedResults]. A resolver with none can
//     never reach the classification, whatever the configuration says.
//
//     Measured by mutation: forcing this condition true breaks no test,
//     because conditions 3's two legs are each individually sufficient today -
//     one needs an entry in the results index, the other needs an expansion
//     built from one. It is kept because that sufficiency rests on how the
//     shared index is KEYED (data addresses carry a "data." prefix, managed
//     ones do not - see [newResolver]), and the safety property here should
//     not depend on a keying convention two seams away. Reported as an
//     untested condition rather than left looking load-bearing.
//  2. The expression names no root variable directly. An unset variable is
//     the collision this exists to avoid, so any expression that could be
//     drawing its unknown from one is not attributed. This under-attributes
//     - "${var.prefix}-${each.value.name}" is refused rather than classified
//     - which is the safe direction: a missing marker outranks a wrong one.
//  3. Either the expression itself names an attribute of a managed resource
//     this run's results cover and whose covered value is not wholly known,
//     or it reads each.* inside a block whose for_each was built from such a
//     reference.
//
// The third condition's second leg is what the ACM shape needs: the refused
// argument is `name = each.value.name`, which mentions no resource at all.
// The provenance lives on the expansion, recorded when the for_each value was
// evaluated - see [expansion.managedFrom].

// managedFromExpr reports the managed resource block whose covered-but-unknown
// value expr reads, when there is exactly the kind of one condition 3's first
// leg describes.
//
// The returned address is module-relative and rendered the way the
// configuration writes it, because it goes into an operator-facing sentence.
func (r *resolver) managedFromExpr(expr hcl.Expression) (string, bool) {
	if !r.managedResults || expr == nil {
		return "", false
	}
	travs := expr.Variables()
	if namesAVariable(travs) {
		return "", false
	}
	for _, trav := range travs {
		if addr, ok := r.managedUnknownAt(trav); ok {
			return addr, true
		}
	}
	return "", false
}

// managedFromScope is condition 3's second leg: the argument reads each.key or
// each.value, and this instance's expansion was built from a managed value
// this run supplied. See [expansion.managedFrom].
func (r *resolver) managedFromScope(expr hcl.Expression, scope instScope) (string, bool) {
	if !r.managedResults || expr == nil || scope.managedFrom == "" {
		return "", false
	}
	travs := expr.Variables()
	if namesAVariable(travs) {
		return "", false
	}
	for _, trav := range travs {
		if len(trav) == 0 {
			continue
		}
		if root, ok := trav[0].(hcl.TraverseRoot); ok && root.Name == "each" {
			return scope.managedFrom, true
		}
	}
	return "", false
}

// managedFrom is [resolver.managedFromExpr] and [resolver.managedFromScope]
// together: the one question every caller actually asks, and the one that
// keeps the two legs' precedence in a single place.
func (r *resolver) managedFrom(expr hcl.Expression, scope instScope) (string, bool) {
	if addr, ok := r.managedFromExpr(expr); ok {
		return addr, true
	}
	return r.managedFromScope(expr, scope)
}

// namesAVariable reports whether any traversal roots at `var`. It is
// condition 2, stated once.
//
// It looks only at DIRECT references, which is the whole of what an
// expression's own Variables() can see: a variable reached through a local is
// invisible here. That is not a hole, because an expression that reads a
// local names no managed resource and no each.* either, so neither leg of
// condition 3 can fire for it in the first place.
func namesAVariable(travs []hcl.Traversal) bool {
	for _, trav := range travs {
		if len(trav) == 0 {
			continue
		}
		if root, ok := trav[0].(hcl.TraverseRoot); ok && root.Name == "var" {
			return true
		}
	}
	return false
}

// SummaryNonStaticIdentityArgument is [resolver.stringValue]'s refusal for a
// value that is not wholly known. It is spelled out at the raise site as a
// literal, because internal/live/refusalscan reads summaries statically and
// fails the build on one it cannot see there; this constant exists only for
// the withdrawal below, which has to be sure of what it is removing.
const SummaryNonStaticIdentityArgument = "Non-static identity argument"

// siblingApplyResolution turns this instance's sibling-apply refusals into a
// [ClassNeedsDiscovery] resolution, withdrawing the diagnostics they raised,
// and reports whether it applied at all.
//
// It applies only when EVERY component that failed did so for this reason.
// hardFailed is the whole of that test and it fails closed: a component that
// could not be resolved for any other reason leaves the instance refused, its
// diagnostics untouched, exactly as before. That ordering matters because the
// alternative - classifying whenever any sibling-apply refusal is present -
// would silently swallow a real refusal standing beside it.
//
// The diagnostics are removed by index and only when the diagnostic at that
// index still carries the summary [resolver.stringValue] raised. A mismatch
// cannot happen through any path this package has today, and the check is
// what makes it harmless if one is ever added: the worst outcome becomes a
// leftover refusal beside the classification, not a different resource's
// error disappearing.
func (r *resolver) siblingApplyResolution(addr addrs.AbsResourceInstance, sibMark int, sibArgs []string, hardFailed bool) (Resolution, bool) {
	if hardFailed || len(r.pendingSiblingApply) <= sibMark {
		return Resolution{}, false
	}
	mine := r.pendingSiblingApply[sibMark:]

	drop := make(map[int]bool, len(mine))
	sibling := ""
	for _, ref := range mine {
		if ref.diagIdx >= 0 && ref.diagIdx < len(r.diags) &&
			r.diags[ref.diagIdx].Description().Summary == SummaryNonStaticIdentityArgument {
			drop[ref.diagIdx] = true
		}
		if sibling == "" {
			sibling = ref.sibling
		}
	}
	if len(drop) > 0 {
		kept := make(tfdiags.Diagnostics, 0, len(r.diags)-len(drop))
		for i, d := range r.diags {
			if drop[i] {
				continue
			}
			kept = append(kept, d)
		}
		r.diags = kept
	}
	r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]

	args := append([]string{sibling}, sibArgs...)
	return Resolution{
		Addr:  addr,
		Class: ClassNeedsDiscovery,
		Reason: fmt.Sprintf(
			"%s takes %s from %s, which the provider does not fill in until %s is applied, so the value is not known until that object exists.",
			addr.String(), orList(sibArgs), sibling, sibling),
		Cause:     DiscoverySiblingApply,
		CauseArgs: args,
	}, true
}

// managedUnknownAt reports whether trav names an attribute of a managed
// resource this run's results cover AND the covered value is not wholly
// known. It is [resolver.managedCovered] plus the knownness test, which is
// what separates "the read answered" from "the read answered with an unknown".
//
// The knownness question is asked of the whole covered object rather than of
// the selected attribute. Selecting would mean walking trav's remaining steps
// into a value a provider process produced, which is a marked-value hazard for
// no gain here: a covered object that is wholly known cannot be the source of
// any unknown, and one that is not wholly known is the only candidate there
// is, since condition 2 has already excluded the other one.
func (r *resolver) managedUnknownAt(trav hcl.Traversal) (string, bool) {
	if !r.managedCovered(trav) {
		return "", false
	}
	ref, diags := addrs.ParseRef(trav)
	if diags.HasErrors() {
		return "", false
	}
	var res addrs.Resource
	switch subj := ref.Subject.(type) {
	case addrs.Resource:
		res = subj
	case addrs.ResourceInstance:
		res = subj.Resource
	default:
		return "", false
	}
	val, ok := r.dataIndex[r.modInst.String()][res.String()]
	if !ok || val == cty.NilVal {
		return "", false
	}
	// ContainsMarked before IsWhollyKnown: the latter iterates collections
	// and panics on a marked one. A marked result is not attributed at all -
	// a sensitive value has its own refusal in [resolver.stringValue], which
	// is the message it deserves.
	if val.ContainsMarked() {
		return "", false
	}
	if val.IsWhollyKnown() {
		return "", false
	}
	return res.String(), true
}
