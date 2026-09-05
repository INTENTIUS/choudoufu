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

	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// readParallelismEnvVar sets how many of the read pass's per-instance provider
// round trips this run has in flight at once - [projection.Options.
// ReadParallelism]. Unset means [projection.DefaultReadParallelism]; 1
// reproduces the sequential read loop exactly, one instance at a time in loop
// order.
//
// In flight is exactly what it bounds, and issue #683 is why that sentence is
// worth reading twice. The pass used to spend the same slot on a call it was
// waiting for and on an answer it had already received, which meant the
// number an operator set here was really a bound on how far the READING could
// get ahead of the consuming loop - and one slow read spent the whole of it.
// The answers now have their own bound, [projection.Options.ReadBuffer],
// which follows this one at [projection.DefaultReadBufferFactor] per slot and
// has no variable of its own: an operator turning the read pass down is
// turning down what the account is asked for, which is this.
//
// GitHub issue #626, which is issue #612's defect one phase over. Issue #585
// made the read pass concurrent and shipped Options.ReadParallelism as the
// engine-side knob, and deliberately stopped at the command boundary because
// internal/command was another unit's at the time. So the field existed and
// nothing outside internal/live/projection referenced it: the engine's default
// of ten was the only setting any run could have, and an operator who hit
// throttling could not turn it down short of patching and rebuilding.
//
// # Why an environment variable and not a flag
//
// The same reason [sweepParallelismEnvVar] is one, and it applies harder here.
// A configuration carrying a live block runs the live pipeline under plain
// "choudoufu plan" and "choudoufu apply", and live-plan itself delegates such a
// configuration to PlanCommand (LivePlanCommand.Run's alias), handing it the
// ORIGINAL argument slice to parse with the stock plan flag set. A flag
// registered on live-plan's own flag set - [arguments.ParseLivePlan]'s hook,
// where -estate lives - would therefore parse in live-plan and then die as
// "flag provided but not defined" in the delegate, on exactly the
// configurations that read the most, since a live-block configuration is the
// one whose read pass runs from live_mode.go's PriorState.
//
// One name covers live-plan's "-estate" form, plain plan and plain apply, with
// no stock surface changed. See [guidedDiscoveryDisableEnvVar]'s doc comment
// for this fork's recorded convention on operational levers - a lever for a run
// that is misbehaving, not a decision a team checks in and reviews the way
// estate and record_store are - and [cloudControlEnvVar] for the TOFU_LIVE_
// prefix.
//
// # The name, against -parallelism and against the sweep's variable
//
// Three bounds now exist on one pipeline, and they bound three different
// phases of it:
//
//   - stock's -parallelism bounds the graph walk, on live-plan exactly as on a
//     stock plan. Unchanged, and not widened here.
//   - [sweepParallelismEnvVar] bounds the estate-wide marker sweep's list
//     calls, which run before the projection exists.
//   - this one bounds the projection's read pass: the ImportResourceState and
//     ReadResource pair issue #584 measured to be, call for call, exactly the
//     calls stock's refresh makes, which run after the sweep and before the
//     graph.
//
// Neither variable is spelled "parallelism" on any command line, so there is no
// command where two of the three appear together as flags and no reading of
// -parallelism that has to be qualified. live-plan's own help lists all three
// and says which phase each bounds; TestReadParallelismIsDocumentedBesideTheOthers
// holds that pairing.
const readParallelismEnvVar = "TOFU_LIVE_READ_PARALLELISM"

// readParallelismSetting resolves [readParallelismEnvVar] into the value
// [projection.Options.ReadParallelism] is given, refusing a setting that is not
// a positive whole number.
//
// The refusal is stock's own, in stock's words: internal/tofu/context.go
// rejects a non-positive -parallelism rather than reading it as "no limit",
// internal/command/arguments/live_import.go restates that sentence for issue
// #583's flag, and [sweepParallelismSetting] restates it for issue #612's
// variable. It is restated once more here rather than invented a fourth time,
// and TestReadParallelismRefusesInStocksOwnWords reads live-import's actual
// rendered refusal to check that it still matches.
//
// A refused setting is an error rather than a fallback to the default: an
// operator who wrote a bound down meant to be running under it, and quietly
// substituting ten is how a run that was turned down to escape throttling
// throttles again with nothing said. The returned int is the default in that
// case only so callers that ignore diagnostics cannot get a zero, which
// [projection.Options.ReadParallelism] would read as "unset" and answer with
// the same ten.
//
// # On the default, which is a decision and not an inheritance
//
// [projection.DefaultReadParallelism] stays at ten. The reasons, and what is
// still unmeasured:
//
//   - Stock's own precedent holds honestly, and holds more directly here than
//     it does for the sweep. Issue #584 measured this pass's calls to be
//     exactly the calls stock's refresh makes - 148, 556 and 1372 at scales 1,
//     4 and 10, call for call - and stock makes them at -parallelism 10. So ten
//     here asks the account for precisely what an OpenTofu refresh of the same
//     estate already asks it for, through the same provider process. This is
//     not "stock uses ten somewhere"; it is the same calls at the same width.
//   - The failure mode is mitigated where these calls land:
//     internal/live/cloudcontrol/retry.go retries a ThrottlingException with
//     full jitter, five attempts, five-second cap - AWS's own recommendation
//     for many concurrent callers backing off one API. Too high therefore
//     costs wall clock before it costs a run.
//   - Lowering it today would swap one number chosen without read-side
//     evidence for another number chosen without read-side evidence, and give
//     back most of what issue #585 bought (~190 of the 322 seconds issue #617
//     measured for a scale-10 plan). Issue #567's 26-35x Rate exceeded at
//     scale 4 is a WRITE measurement on IAM; it is the reason to want this
//     knob, not a measurement of this path.
//
// Unmeasured, and unmeasurable here: read-side throttling under load. floci
// does not throttle, so no emulator run can settle whether ten is right for a
// populated real account, and no emulator run can justify going higher than the
// number real AWS is already known to tolerate for a refresh. What issue #626
// fixes is that ten was unreachable, which was the actual hazard - not the ten.
func readParallelismSetting() (int, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	raw := strings.TrimSpace(os.Getenv(readParallelismEnvVar))
	if raw == "" {
		return projection.DefaultReadParallelism, diags
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return projection.DefaultReadParallelism, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid parallelism value",
			fmt.Sprintf("%s is how many of the read pass's per-instance provider round trips run at once, so it takes a whole number; %q is not one. Unset it to take the default of %d.", readParallelismEnvVar, raw, projection.DefaultReadParallelism),
		))
	}
	if n < 1 {
		return projection.DefaultReadParallelism, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid parallelism value",
			fmt.Sprintf("The parallelism must be a positive value. Not %d. %s is how many of the read pass's per-instance provider round trips run at once; 1 is the sequential read pass, and unsetting it takes the default of %d.", n, readParallelismEnvVar, projection.DefaultReadParallelism),
		))
	}
	return n, diags
}
