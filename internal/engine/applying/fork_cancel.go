// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

// choudoufu fork port (#2381): remove when the row closes. The whole file
// is the apply engine's side of package cancel: no managed resource
// instance object's apply starts once the run has been stopped. It is what
// lets the gauntlet's day2_crash e2e crash hook (internal/command's
// apply_e2etesting_crash.go, TOFU_E2E_APPLY_RESOURCE_INTERRUPT) hold the
// window between a create_before_destroy replace's create and its deposed
// destroy on the new runtime, as the stop hook holds it on the default one.

import (
	"context"
	"errors"
	"log"

	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkHaltedBeforeApply reports whether the run was stopped before this
// apply started, and if so what ManagedApply returns in its place: the
// default runtime's "execution halted" error, with nothing sent to the
// provider and no tracer event, so the CLI prints no "Creating..." or
// "Destroying..." for it. A create half that was given a fallback (the
// object a create_before_destroy replace deposed for it) restores that
// object as current first, as managedApply does when a create returns no
// object. Any other object is left in the working state as it was, so a
// deposed object whose destroy is refused here stays deposed.
func (ops *execOperations) forkHaltedBeforeApply(ctx context.Context, plan *exec.ManagedResourceObjectFinalPlan, fallback *exec.ResourceInstanceObject) (*exec.ResourceInstanceObject, tfdiags.Diagnostics, bool) {
	var diags tfdiags.Diagnostics
	if plan == nil || ops.stopped == nil || !ops.stopped() {
		return nil, diags, false
	}
	log.Printf("[WARN] apply phase: ManagedApply %s not started because the run was stopped", plan.Addr)
	diags = diags.Append(errors.New("execution halted"))
	if fallback == nil || !fallback.Addr.IsDeposed() || !fallback.Addr.InstanceAddr.Equal(plan.Addr.InstanceAddr) {
		return nil, diags, true
	}
	if _, err := ops.workingState.MaybeRestoreResourceInstanceDeposed(fallback.Addr.InstanceAddr, fallback.Addr.DeposedKey); err != nil {
		diags = diags.Append(err)
	}
	ret, moreDiags := ops.resourceInstanceStateObject(ctx, plan.Addr.InstanceAddr, states.NotDeposed)
	return ret, diags.Append(moreDiags), true
}
