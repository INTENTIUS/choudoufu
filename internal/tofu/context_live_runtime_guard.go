// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveOnNewRuntimeSummary is the summary of the error live mode gets when the
// new runtime has been chosen. Tests match on it.
const LiveOnNewRuntimeSummary = "Live mode cannot run on the new runtime yet"

// refuseLiveOnNewRuntime is called at each point where the experimental
// runtime has been chosen to plan, apply or validate (#2256 G, #2339). The
// new runtime in internal/engine never calls the fork's ConfigValueAdjuster,
// so a live run routed there would create resources without ownership
// markers and report no error.
//
// It is keyed on the decision, not on any environment variable: every live
// entry point sets ConfigValueAdjuster, so this one check covers them all
// however the runtime was switched on, saved-plan applies included. #2256 U6
// lifts it once markers, records and identity resolution are ported.
func (c *Context) refuseLiveOnNewRuntime() tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if c.configValueAdjuster == nil {
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		LiveOnNewRuntimeSummary,
		"This run uses live mode, and OpenTofu's new runtime (internal/engine) has been switched on, for example by TOFU_X_EXPERIMENTAL_RUNTIME. "+
			"The new runtime does not write choudoufu's ownership markers yet, so resources it created would be unowned. "+
			"Unset the variable to use the default runtime. Progress is tracked in https://github.com/INTENTIUS/choudoufu/issues/2256.",
	))
}
