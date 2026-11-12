// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

// choudoufu fork port (#2409): remove when upstream #4682 lands. The whole
// file is the apply engine's side of package forget: what an applied object
// records as destroy = false in state.

import (
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
)

// forkSkipDestroy is what the object plan applies records as destroy =
// false: what its resource block sets, as the default runtime writes it on
// every apply. The planning engine already refused an invalid argument, so
// an error here reads as false. False with the port off.
func (ops *execOperations) forkSkipDestroy(plan *exec.ManagedResourceObjectFinalPlan) bool {
	if plan == nil || ops.forget == nil {
		return false
	}
	skip, diags := ops.forget.SkipDestroy(plan.Addr.InstanceAddr)
	return skip && !diags.HasErrors()
}
