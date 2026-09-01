// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/command/format"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states/statefile"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// AdoptionOnlyPlan wraps a [Plan] view so that the resource diff is not
// printed, for GitHub issue #587's "-adoption-only" mode.
//
// The mode's whole point is legibility during a migration.
// live/e2e/terralith-scale/MIGRATION.md measured the plan-based adoption loop
// at 2,885 lines for 55 resources and 7,649 for 205, of which the sections
// carrying an adoption path were 5.6% and 5.5%. Leaving the diff in and
// adding a ledger above it would move that ratio by a page and fix nothing:
// the operator would still be scrolling a several-thousand-line report to
// find the part that answers the question they asked.
//
// Three things are suppressed: the rendered diff, the next-step hint that
// follows it, and the BODY of every warning - never a warning's existence,
// and never an error.
//
// The warnings are here because dropping the diff alone did not fix the
// reported problem. Measured against live/e2e/estate-block plus an IAM
// role and its inline policy, on the pinned emulator: a plain plan is 926
// lines, dropping the diff and the other sections leaves 500, and 470 of
// those 500 are warning bodies - 36 "Incomplete sweep for undeclared
// resources", one per provider type the emulator cannot list, at 8 to 9
// lines each. That is the [OBJECT_UNTAGGED] floor
// live/e2e/terralith-scale/MIGRATION.md measured as most of the noise, and
// it is bounded by the provider's type count rather than by the estate, so
// it does not shrink as the signal grows.
//
// So each warning is rendered as one line - its summary, with a count when
// the same summary recurs - under a heading that says how many there were
// and how to read them in full. Nothing is hidden: every warning the run
// produced is still named on screen, and the sentence pointing at plain
// "choudoufu plan" is printed with them rather than left to be guessed.
//
// Errors are never touched. Interrupts, the backend's own progress and the
// emergency state dump all pass straight through, because a mode that hid
// a failure while claiming to summarize adoption would be worse than the
// noise it removes.
//
// This type is a view, not a pipeline mode: the projection and the plan
// graph are the same, so a resource's adoption verdict here is the same
// verdict the run would have printed without the flag, which is the
// property that makes the ledger trustworthy.
//
// What the FLAG does beyond selecting this view has changed since
// the CollectUnclaimed ruling (#604), and the
// sentence that used to sit here ("the same live reads, the same discovery
// sweep ... the flag buys no time") is no longer true of it: -adoption-only
// now also asks the estate-wide sweep which live resources carry no
// ownership marker at all, so it reads MORE than an ordinary plan rather
// than the same. See [command.collectUnclaimedSetting]. Nothing about this
// view changed; only what the run it renders went and looked at.
type AdoptionOnlyPlan struct {
	inner Plan
	view  *View
}

var _ Plan = (*AdoptionOnlyPlan)(nil)

// NewAdoptionOnlyPlan wraps inner. view is the base view the compact warning
// list is written through, the same one the ledger itself uses.
func NewAdoptionOnlyPlan(inner Plan, view *View) Plan {
	return &AdoptionOnlyPlan{inner: inner, view: view}
}

func (v *AdoptionOnlyPlan) Operation() Operation {
	return &adoptionOnlyOperation{inner: v.inner.Operation(), view: v.view}
}

func (v *AdoptionOnlyPlan) Hooks() []tofu.Hook { return v.inner.Hooks() }
func (v *AdoptionOnlyPlan) Diagnostics(diags tfdiags.Diagnostics) {
	v.inner.Diagnostics(compactWarnings(v.view, diags))
}
func (v *AdoptionOnlyPlan) HelpPrompt()      { v.inner.HelpPrompt() }
func (v *AdoptionOnlyPlan) Backend() Backend { return v.inner.Backend() }

// adoptionOnlyOperation is the half that actually drops output: Plan,
// PlannedChange and PlanNextStep. Everything else is the inner view's.
type adoptionOnlyOperation struct {
	inner Operation
	view  *View
}

var _ Operation = (*adoptionOnlyOperation)(nil)

// Plan is the resource diff. Dropped.
func (v *adoptionOnlyOperation) Plan(*plans.Plan, *tofu.Schemas) {}

// PlannedChange is one change rendered on its own, which the human view uses
// for the -json/streaming shapes rather than the plan report. Dropped for the
// same reason as Plan.
func (v *adoptionOnlyOperation) PlannedChange(*plans.ResourceInstanceChangeSrc) {}

// PlanNextStep is "To perform exactly these actions, run the following
// command to apply". Dropped: it points at a diff this mode did not print,
// and the ledger's own sections say what to do next.
func (v *adoptionOnlyOperation) PlanNextStep(string, string) {}

func (v *adoptionOnlyOperation) Interrupted()              { v.inner.Interrupted() }
func (v *adoptionOnlyOperation) FatalInterrupt()           { v.inner.FatalInterrupt() }
func (v *adoptionOnlyOperation) Stopping()                 { v.inner.Stopping() }
func (v *adoptionOnlyOperation) Cancelled(mode plans.Mode) { v.inner.Cancelled(mode) }
func (v *adoptionOnlyOperation) Diagnostics(d tfdiags.Diagnostics) {
	v.inner.Diagnostics(compactWarnings(v.view, d))
}

func (v *adoptionOnlyOperation) EmergencyDumpState(f *statefile.File, enc encryption.StateEncryption) error {
	return v.inner.EmergencyDumpState(f, enc)
}

// compactWarnings renders every warning in diags as one line - its summary,
// prefixed by a count when the same summary recurs - and returns the
// diagnostics that still have to be rendered in full, which is the errors.
//
// Order is first-appearance, not frequency: the run produced them in an
// order, and re-ranking a list of things nobody is being asked to act on
// buys nothing and makes two runs over the same estate disagree about which
// line is first.
//
// A nil view means no compaction: the caller has nowhere to write the list,
// and dropping warning bodies with nothing printed in their place is exactly
// the silent-hiding this mode must not do.
func compactWarnings(view *View, diags tfdiags.Diagnostics) tfdiags.Diagnostics {
	if view == nil {
		return diags
	}

	var kept tfdiags.Diagnostics
	var order []string
	counts := map[string]int{}
	for _, d := range diags {
		if d.Severity() != tfdiags.Warning {
			kept = kept.Append(d)
			continue
		}
		summary := d.Description().Summary
		if counts[summary] == 0 {
			order = append(order, summary)
		}
		counts[summary]++
	}
	if len(order) == 0 {
		return kept
	}

	total := 0
	for _, s := range order {
		total += counts[s]
	}

	view.streams.Print(view.colorize.Color(fmt.Sprintf(
		"\n[reset][bold]%d %s withheld by -adoption-only[reset]\n\n",
		total, noun(total, "warning was", "warnings were"))))
	for _, s := range order {
		if n := counts[s]; n > 1 {
			view.streams.Print(fmt.Sprintf("  %4dx  %s\n", n, s))
			continue
		}
		view.streams.Print(fmt.Sprintf("         %s\n", s))
	}
	view.streams.Print("\n")
	for _, line := range strings.Split(strings.TrimRight(format.WordWrap(
		"Each is named above and none is an error. Run the same command without -adoption-only to read them in full; most of them describe resource types this estate does not declare, and their number is set by the provider's type count rather than by this estate.",
		view.outputColumns()), "\n"), "\n") {
		view.streams.Print(line + "\n")
	}
	view.outputHorizRule()

	return kept
}
