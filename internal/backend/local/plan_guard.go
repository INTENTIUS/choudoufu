// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// askPlanGuard consults [backend.Operation.PlanGuard], if the caller set one,
// about a finished plan. It returns the guard's diagnostics and whether the
// operation must stop.
//
// It is called from opPlan and opApply, once each, at the point where the
// plan has been shown to the operator and nothing has been applied yet.
// opApply calls it from both of its branches - the plan it just made, and
// the plan it read out of a saved plan file - because a guard that only saw
// the first would be bypassed by "plan -out=p && apply p".
//
// When there is no guard this is a nil check and nothing else, which is
// every run that did not ask for one.
func askPlanGuard(op *backend.Operation, plan *plans.Plan, schemas *tofu.Schemas) (tfdiags.Diagnostics, bool) {
	if op.PlanGuard == nil || plan == nil {
		return nil, false
	}
	diags := op.PlanGuard(plan, schemas)
	return diags, diags.HasErrors()
}
