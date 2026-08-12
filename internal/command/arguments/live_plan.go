// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"flag"

	"github.com/opentofu/opentofu/internal/tfdiags"
)

// LivePlan represents the command-line arguments for the live-plan command,
// which is the plan command's whole option set plus -estate.
type LivePlan struct {
	// Plan is the stock plan argument set, embedded so that -target, -var,
	// -var-file, -refresh and the rest parse and behave identically. Anything
	// live-plan cannot honor is rejected by the command after parsing, rather
	// than removed from the flag set here, so that an operator who passes one
	// is told so instead of being ignored.
	*Plan

	// Estate names the estate whose ownership markers this run looks for. An
	// empty value means the flag was absent, which is not an error: the name
	// is then derived from the configuration, and a run that cannot derive it
	// degrades rather than fails. It is an error only when the configuration
	// has a live block, since the block names the estate itself; the command
	// makes that check, because it needs a loaded configuration to make it.
	Estate string
}

// ParseLivePlan processes CLI arguments, returning a LivePlan value, a closer
// function, and errors. If errors are encountered, a LivePlan value is still
// returned representing the best effort interpretation of the arguments.
//
// -estate is registered on the plan command's own flag set rather than added
// to [Plan], which would make it an option of "choudoufu plan" as well: a flag
// naming a stateless concept on the command that has state files. Registering
// it here keeps the stock plan surface exactly as it was, and still gets
// -estate the parsing every other option has - end-of-flags handling, the
// -estate=NAME and -estate NAME forms, and a real error for a flag that is
// not one.
func ParseLivePlan(args []string) (*LivePlan, func(), tfdiags.Diagnostics) {
	livePlan := &LivePlan{}
	plan, closer, diags := parsePlan(args, func(cmdFlags *flag.FlagSet) {
		cmdFlags.StringVar(&livePlan.Estate, "estate", "", "estate")
	})
	livePlan.Plan = plan
	return livePlan, closer, diags
}
