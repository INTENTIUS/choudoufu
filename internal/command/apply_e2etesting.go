// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"sync"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/zclconf/go-cty/cty"
)

// e2eTestingApplyHooks simulates particularly nasty
// scenarios within OpenTofu's apply engine, such
// as panics due to programming errors
type e2eTestingApplyHook struct {
	tofu.NilHook

	// interruptOnce guards the self-interrupt PostApply implements on
	// !windows builds (apply_e2etesting_crash.go) so a resource address
	// matching TOFU_E2E_APPLY_RESOURCE_INTERRUPT is only ever interrupted
	// once per apply, however many times PostApply itself fires for other
	// resources or generations. Declared here, not in that file, because
	// the struct has exactly one definition and the field costs nothing on
	// windows, where PostApply falls through to tofu.NilHook's no-op and
	// never reads it.
	interruptOnce sync.Once
}

func (e *e2eTestingApplyHook) PreApply(addr addrs.AbsResourceInstance, gen states.Generation, action plans.Action, priorState, plannedNewState cty.Value) (tofu.HookAction, error) {
	if resourceString := os.Getenv("TOFU_E2E_APPLY_RESOURCE_PANIC"); resourceString == addr.String() {
		panic("Crash simulating a critical programming error in the apply process, this should produce an errored.tfstate file")
	}
	return tofu.HookActionContinue, nil
}
