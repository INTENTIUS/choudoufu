// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"os"
	"strings"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// collectUnclaimedEnvVar forces the estate-wide sweep's account-inventory
// question - [discovery.Request.CollectUnclaimed] - on or off for this run,
// overriding what the command would otherwise choose.
//
// "1", "true", "on" and "yes" turn it on; "0", "false", "off" and "no" turn
// it off; unset leaves the choice to the command, which is
// [collectUnclaimedSetting]'s adoptionOnly argument. Anything else is an
// error rather than a silent default, for the reason
// [sweepParallelismSetting] gives about its own refusal: an operator who
// wrote a setting meant to be running under it.
//
// # Why an environment variable, in addition to a flag
//
// The same delegation reason [sweepParallelismEnvVar]'s doc comment argues
// at length: a flag registered on live-plan's own flag set dies as "flag
// provided but not defined" in the PlanCommand it delegates to for a
// configuration carrying a live block, which is most of them. What is
// registered on the stock flag sets already, for exactly that reason, is
// -adoption-only, and that is the flag this setting reads (below). This
// variable is what covers live-plan's own "-estate" form, plain apply, and
// any run that wants the account inventory without the adoption-only
// renderer.
const collectUnclaimedEnvVar = "TOFU_LIVE_COLLECT_UNCLAIMED"

// collectUnclaimedSetting resolves whether this run asks the estate-wide
// sweep's account-inventory question: "what is in my account that this
// estate does not know about".
//
// The default is adoptionOnly - GitHub issue #587's -adoption-only flag -
// and that is the whole of the mechanism
// the CollectUnclaimed ruling (#604) left to be
// designed. The charter offers three shapes (an opt-in flag, a periodic
// schedule the record store tracks, or on for the commands where the answer
// is the point) and this is the third, using the flag that already exists
// for the question. The charter's own words: -adoption-only "is the obvious
// first place for the capability to become real, and it is not that yet".
// Today it selects a different renderer and gates nothing
// ([statelessPlanView]); with this, it also selects what the run goes and
// looks at.
//
// # What turning it off does and does not do
//
// It does not turn off the estate-wide sweep. The tagging leg still makes
// its one estate-filtered GetResources call, [recordOrphanReadSweep] still
// walks the record store, and the parent-read and fold-child legs still
// run, so every removal an ordinary plan proposes today it still proposes -
// which is what the day2_remove gauntlet stages check by value. What it
// drops is the per-type enumeration of admitted types this estate has no
// evidence of ever having touched, which is where the cost lives:
// 710 API calls to 157 on a migrated 79-instance terralith against floci
// sha256:c55d74e1, against stock's 150 for the same estate. See
// internal/live/discovery/nativesweep.go for the narrowing rule, the
// evidence sources, and the one removal shape it gives up.
//
// A plan that did not ask says so rather than implying an answer: the
// stateless plan view's "Foreign resources" section renders the narrowing
// (see [views.StatelessForeign.NativeSweepSkipped]).
func collectUnclaimedSetting(adoptionOnly bool) (bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	raw := strings.TrimSpace(os.Getenv(collectUnclaimedEnvVar))
	if raw == "" {
		return adoptionOnly, diags
	}
	switch strings.ToLower(raw) {
	case "1", "true", "on", "yes":
		return true, diags
	case "0", "false", "off", "no":
		return false, diags
	}
	return adoptionOnly, diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"Invalid collect-unclaimed value",
		fmt.Sprintf("%s says whether this run asks the estate-wide sweep which live resources carry no ownership marker at all, so it takes an on/off value: 1, true, on, yes, 0, false, off or no. %q is not one of them. Unset it to let the command decide, which means on under -adoption-only and off otherwise.", collectUnclaimedEnvVar, raw),
	))
}
