// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// sweepParallelismEnvVar sets how many of the estate-wide sweep's list calls
// this run has in flight at once - [discovery.Request.SweepParallelism].
// Unset means [discovery.DefaultSweepParallelism]; 1 reproduces the
// sequential sweep exactly, one call at a time in universe order.
//
// GitHub issue #612. Issue #605 made the sweep concurrent and defaulted it to
// ten, and the number reached the engine from nowhere: nothing in
// internal/command referenced the field at all, so an operator who hit
// throttling on a real account could not turn it down short of patching and
// rebuilding. This variable is that knob.
//
// In flight is exactly what it bounds, and issue #839 is why that sentence is
// worth reading twice. The sweep used to spend the same slot on a call it was
// waiting for and on a listing it had already received, which meant the
// number an operator set here was really a bound on how far the LISTING could
// get ahead of the scan loop - and one slow call spent the whole of it. The
// listings now have their own bound, [discovery.Request.SweepBuffer], which
// follows this one at [discovery.DefaultSweepBufferFactor] per slot and has
// no variable of its own: an operator turning the sweep down is turning down
// what the account is asked for, which is this.
//
// # Why an environment variable and not a flag
//
// Because the code path that sweeps would have rejected the flag. A
// configuration carrying a live block runs the live pipeline under plain
// "choudoufu plan" and "choudoufu apply", and live-plan itself delegates to
// PlanCommand for such a configuration (LivePlanCommand.Run's alias), handing
// it the ORIGINAL argument slice to parse with the stock plan flag set. A
// flag registered where -estate is registered - [arguments.ParseLivePlan]'s
// hook, which is live-plan's own flag set and nothing else - would therefore
// parse in live-plan and then die as "flag provided but not defined" in the
// delegate, on exactly the configurations that sweep the most.
//
// The alternative is registering it on [arguments.Plan] and
// [arguments.Apply], where -adoption-only lives for that same delegation
// reason. That widens two stock flag sets, needs a stock-run refusal beside
// planRejectAdoptionOnly so a state-backed plan does not silently ignore it,
// and puts a second parallelism-spelled option next to stock's -parallelism
// on one command line - which is the confusion issue #612 asks to avoid
// rather than create. It stays available if the flag is later wanted; adding
// one is additive to this.
//
// What is here instead is this fork's own recorded convention for an
// operational lever on the live path: see [guidedDiscoveryDisableEnvVar]'s
// doc comment, which argues it at length - a lever for a run that is
// misbehaving, not a decision a team checks in and reviews the way estate and
// record_store are - and [cloudControlEnvVar] for the TOFU_LIVE_ prefix. One
// name covers live-plan's "-estate" form, plain plan and plain apply, with no
// stock surface changed.
//
// # The name, against issue #583's -parallelism
//
// live-import has carried -parallelism since issue #583, for how many
// resources -approve stamps at once. That is stock's spelling used for
// stock's meaning: the bound on concurrent provider round trips the
// operation's own work makes, which on live-import is the stamp pass because
// that is the only concurrent work the command has, and on plan and apply is
// the graph walk. Nothing here widens either.
//
// This knob is deliberately not spelled "parallelism" on any command line, so
// there is no command where the two appear together and no reading of
// -parallelism that has to be qualified. The scope it names is a phase of one
// run: the estate-wide sweep, before the graph walk exists.
const sweepParallelismEnvVar = "TOFU_LIVE_SWEEP_PARALLELISM"

// sweepParallelismSetting resolves [sweepParallelismEnvVar] into the value
// [discovery.Request.SweepParallelism] is given, refusing a setting that is
// not a positive whole number.
//
// The refusal is stock's own, in stock's words: internal/tofu/context.go
// rejects a non-positive -parallelism rather than reading it as "no limit",
// and internal/command/arguments/live_import.go already restates that
// sentence for issue #583's flag. It is restated once more here rather than
// invented a second time, and TestSweepParallelismRefusesInStocksOwnWords
// reads live-import's actual rendered refusal to check that it still matches.
//
// A refused setting is an error rather than a fallback to the default: an
// operator who wrote a bound down meant to be running under it, and quietly
// substituting ten is how a run that was turned down to escape throttling
// throttles again with nothing said. The returned int is the default in that
// case only so callers that ignore diagnostics cannot get a zero, which
// sweepParallelism in internal/live/discovery would read as "unset".
//
// # On the default, which is a decision and not an inheritance
//
// [discovery.DefaultSweepParallelism] stays at ten. The reasons, and what is
// still unmeasured:
//
//   - This number bounds the sweep's LIST calls and only those. The
//     prefetch's inflight channel (internal/live/discovery/sweepconcurrency.go)
//     is acquired per swept type for its one list call and released when that
//     call returns (issue #839; before it, the scan loop released it); the
//     per-object
//     GetResource refinement issue #586 describes - one call per listed
//     object arriving without Tags, which is what scales with the account's
//     own population rather than with the estate - runs in the SEQUENTIAL
//     consumer, one at a time, at every setting. So ten here is ten calls in
//     flight, not ten streams, and it does not multiply by anything.
//   - Against that, stock's own precedent holds honestly: an OpenTofu plan of
//     the same estate walks its graph at -parallelism 10, and each slot is a
//     provider round trip against the same account. An operator who can plan
//     this configuration is already asking for ten.
//   - The failure mode is already mitigated where the sweep's bulk lands.
//     internal/live/cloudcontrol/retry.go retries a ThrottlingException with
//     full jitter, five attempts, five-second cap - AWS's own recommendation
//     for many concurrent callers backing off one API. Too high therefore
//     costs wall clock before it costs a run.
//   - Lowering it today would swap one number chosen without read-side
//     evidence for another number chosen without read-side evidence, and give
//     back most of what issue #605 bought (203 of a 205-second plan at scale
//     1). Issue #567's 26-35x Rate exceeded at scale 4 is a WRITE measurement
//     on IAM; it is the reason to want this knob, not a measurement of this
//     path.
//
// Unmeasured, and unmeasurable here: read-side throttling under load. floci
// does not throttle, so no emulator run can settle whether ten is right for a
// populated real account. What issue #612 fixes is that ten was unchangeable,
// which was the actual hazard - not the ten.
func sweepParallelismSetting() (int, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	raw := strings.TrimSpace(os.Getenv(sweepParallelismEnvVar))
	if raw == "" {
		return discovery.DefaultSweepParallelism, diags
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return discovery.DefaultSweepParallelism, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid parallelism value",
			fmt.Sprintf("%s is how many of the estate-wide sweep's list calls run at once, so it takes a whole number; %q is not one. Unset it to take the default of %d.", sweepParallelismEnvVar, raw, discovery.DefaultSweepParallelism),
		))
	}
	if n < 1 {
		return discovery.DefaultSweepParallelism, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid parallelism value",
			fmt.Sprintf("The parallelism must be a positive value. Not %d. %s is how many of the estate-wide sweep's list calls run at once; 1 is the sequential sweep, and unsetting it takes the default of %d.", n, sweepParallelismEnvVar, discovery.DefaultSweepParallelism),
		))
	}
	return n, diags
}
