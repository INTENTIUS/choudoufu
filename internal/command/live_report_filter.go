// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1197's -filter: a report-only narrowing over the three
// categories live-plan computes (unowned, adoptable, foreign). The
// 2026-09-26 ruling on that issue is the spec: a filter narrows the report,
// never the plan. Nothing in this file touches the planned changes or the
// exit code; see [arguments.ReportFilter].

// planRejectReportFilter refuses -filter (GitHub issue #1197) where it would
// do nothing, for planRejectAdoptionOnly's reason: an operator who asked for
// a narrower report and got the ordinary one, with no sign the flag was
// ignored, is worse off than one told so.
//
// Two shapes. A run with no live block reads a state file and prints none of
// the three categories -filter selects among. And -adoption-only prints a
// different report, the adoption ledger, whose sections are not these
// categories; letting one flag silently win is the conflict this file
// already refuses for -adoption-only with -json.
func planRejectReportFilter(filter arguments.ReportFilter, adoptionOnly, live bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if !filter.Active() {
		return diags
	}
	if !live {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"-filter needs a configuration under live resource markers",
			"-filter narrows the live-markers report's unowned, adoptable and foreign sections, and this configuration has no live block, so this run reads a state file and prints none of them. Add a live block naming the estate, or drop -filter.",
		))
	}
	if adoptionOnly {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"-filter and -adoption-only cannot be combined",
			"-adoption-only prints the adoption ledger, whose sections are not the unowned, adoptable and foreign categories -filter selects among. Rerun with only one of the two flags.",
		))
	}
	return diags
}

// statelessNoSweepAnswer answers an adoptable or foreign filter on a run whose
// estate-wide sweep did not run at all, and so never reached view.Foreign.
// Unfiltered that run prints no foreign section, as it always has; a run that
// asked for one of those categories by name gets the section's own "nothing
// was swept" answer instead of silence, which is what keeps a filter matching
// nothing distinguishable from a filter that did nothing.
func statelessNoSweepAnswer(view views.StatelessPlan, filter arguments.ReportFilter) {
	if !filter.Active() {
		return
	}
	if !filter.Shows(arguments.ReportForeign) && !filter.Shows(arguments.ReportAdoptable) {
		return
	}
	view.Foreign(views.StatelessForeign{})
}

// livePlanFilterDocument narrows live-plan's -json document to filter's
// categories. A category the filter left out marshals as null, and the
// document's "filter" field names what was kept, so a reader can tell "not
// selected" from "selected and empty" without knowing the command line.
//
// Only unowned, adoptable and foreign are narrowed. Swept stays whatever
// the filter, because it is what says what an empty adoptable or foreign
// list means; bound, omissions and diagnostics describe the plan and are
// never narrowed.
func livePlanFilterDocument(doc views.LivePlanDocument, filter arguments.ReportFilter) views.LivePlanDocument {
	if !filter.Active() {
		return doc
	}
	doc.Filter = append([]string(nil), filter...)
	if !filter.Shows(arguments.ReportUnowned) {
		doc.Unowned = nil
	}
	if !filter.Shows(arguments.ReportAdoptable) {
		doc.Adoptable = nil
	}
	if !filter.Shows(arguments.ReportForeign) {
		doc.Foreign = nil
	} else if doc.Foreign == nil {
		// livePlanForeign returns nil for an empty sweep; selected by
		// name, an empty category is [] so that it never reads as the
		// null of a category the filter left out.
		doc.Foreign = []views.LivePlanForeign{}
	}
	if filter.Shows(arguments.ReportUnowned) && doc.Unowned == nil {
		doc.Unowned = []views.StatelessUnowned{}
	}
	if filter.Shows(arguments.ReportAdoptable) && doc.Adoptable == nil {
		doc.Adoptable = []views.LivePlanAdoptable{}
	}
	return doc
}
