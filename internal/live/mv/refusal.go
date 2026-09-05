// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// RefusalCode is live-mv's machine-readable half of the small set of
// refusals GitHub issue #791's -json flag has to name without a reader
// parsing English out of a diagnostic's Detail: nothing carries the old
// address, two resources carry it, something already carries the new one,
// the destination is not in configuration, and the plan would change more
// than tags.
//
// Every diagnostic this package raises still carries its full human text
// exactly as it did before this type existed - see [refuse]'s own doc
// comment for why attaching a code changes nothing about what an operator
// reads. A diagnostic outside these five shapes (a malformed marker, a
// resource owned by the wrong estate, a provider bug mid-apply) carries no
// code at all, which reads back as the empty string: a caller has to fall
// back to the text for those, and that is by design rather than a gap left
// to fill in - the issue asked for five codes, not for the resolution of
// every string this package ever prints.
type RefusalCode string

const (
	// RefusalNothingAtOldAddress is "nothing carries the old address": a
	// sweep of the type turned up nothing bearing the old marker at all,
	// whether because it was never written, was written to a different
	// estate, or was already rewritten to some third address.
	RefusalNothingAtOldAddress RefusalCode = "nothing_at_old_address"

	// RefusalTwoAtOldAddress is "two resources carry it": a sweep found more
	// than one live resource claiming the old address inside one estate,
	// which is the corruption a rename refuses to arbitrate rather than
	// guess at.
	RefusalTwoAtOldAddress RefusalCode = "two_at_old_address"

	// RefusalNewAddressClaimed is "something already carries the new one":
	// the destination address is not free, whether that is a second live
	// resource claiming it or the very resource being renamed reporting
	// that this move already ran.
	RefusalNewAddressClaimed RefusalCode = "new_address_claimed"

	// RefusalDestinationNotDeclared is "the destination is not in
	// configuration": [anchorAddr]'s ordering refusal, for a rename run
	// before the resource block itself was renamed and without
	// -allow-missing-config to say that is expected.
	RefusalDestinationNotDeclared RefusalCode = "destination_not_declared"

	// RefusalPlanChangesMoreThanTags is "the plan would change more than
	// tags": the provider's own plan for the tag write requires replacing
	// the resource, or moves an attribute this rename never touched -
	// [mover.checkPlan]'s two guards between a plan and an apply.
	RefusalPlanChangesMoreThanTags RefusalCode = "plan_changes_more_than_tags"
)

// refuse builds a diagnostic exactly as [tfdiags.Sourceless] does, with one
// addition: code rides along on its ExtraInfo, retrievable anywhere with
// tfdiags.ExtraInfo[RefusalCode](diag), the same retrieval
// internal/live/identity's InstanceFailure already establishes the pattern
// for (see internal/live/identity/manageddemand.go). Severity, summary and
// detail are untouched - a caller that never asks for the code reads exactly
// the diagnostic [tfdiags.Sourceless] would have produced, which is what
// keeps this additive: nothing about live-mv's human output, or any
// existing test asserting on it, changes by a byte.
func refuse(code RefusalCode, severity tfdiags.Severity, summary, detail string) tfdiags.Diagnostic {
	return refusalDiag{Diagnostic: tfdiags.Sourceless(severity, summary, detail), code: code}
}

// refusalDiag is one diagnostic with an ExtraInfo override and nothing else
// changed, the same shape internal/live/identity's managedDemandDiag takes
// for the same reason: embedding keeps every other method - Severity,
// Description, Source, FromExpr - exactly what the wrapped diagnostic
// already answered.
type refusalDiag struct {
	tfdiags.Diagnostic
	code RefusalCode
}

func (d refusalDiag) ExtraInfo() interface{} { return d.code }

// CodedRefusal returns the first error diagnostic in diags that carries a
// [RefusalCode], together with the code itself. ok is false when none does
// - either Move succeeded, or it refused for a reason outside the five
// [RefusalCode] shapes, which is an honest answer rather than a bug: see
// [RefusalCode]'s own doc comment. A caller that gets ok == false but still
// has an error in diags (GitHub issue #791's -json flag among them) is
// meant to fall back to that diagnostic's own text, not to invent a sixth
// code.
//
// Returning the diagnostic itself, not only the code, is what lets a caller
// print the SAME diagnostic's Summary and Detail alongside the code rather
// than doing a second, potentially different, scan to find "the" refusal
// text - two scans over a Diagnostics slice with more than one error could
// disagree about which one is "the" refusal; one scan cannot.
func CodedRefusal(diags tfdiags.Diagnostics) (code RefusalCode, diag tfdiags.Diagnostic, ok bool) {
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		if c := tfdiags.ExtraInfo[RefusalCode](d); c != "" {
			return c, d, true
		}
	}
	return "", nil, false
}

// RefusalFrom is [CodedRefusal] for a caller that only wants the code.
func RefusalFrom(diags tfdiags.Diagnostics) RefusalCode {
	code, _, _ := CodedRefusal(diags)
	return code
}
