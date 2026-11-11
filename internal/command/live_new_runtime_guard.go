// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// liveCommandLayerOnNewRuntimeSummary is the summary of the error live mode
// gets when the command layer runs on the new runtime. Tests match on it.
const liveCommandLayerOnNewRuntimeSummary = "Live mode cannot run with the new runtime's command layer"

// liveRefusesNewRuntime is the command-layer half of #2256 G (#2339). It is
// called from each live entry point: plan and apply (which also cover
// composed roots, since liveBegin hands those to liveBeginComposed),
// live-plan and per-resource live plan.
//
// It asks [Meta.NewRuntimeEnabled], whatever that reads: the opt-in variable
// today, upstream's TOFU_X_EXPERIMENTAL_RUNTIME_NEXT after #2256 U1. The
// runtime-decision half in internal/tofu is lifted by U6; this half stays
// until upstream calls the new command layer stable, because that layer
// loads modules itself and the fork's edits to the loaded configuration
// would not reach the run.
func (m *Meta) liveRefusesNewRuntime() tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if !m.NewRuntimeEnabled() {
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		liveCommandLayerOnNewRuntimeSummary,
		"This run uses live mode, and the command layer of OpenTofu's new runtime has been switched on by an experimental opt-in variable. "+
			"Live mode does not support that layer yet: it would run without choudoufu's ownership markers. "+
			"Unset the variable to use the default runtime. Progress is tracked in https://github.com/INTENTIUS/choudoufu/issues/2256.",
	))
}
