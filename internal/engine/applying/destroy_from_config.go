// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/override"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
)

// destroyFromPlan is the apply half of choudoufu's destroy-from-config hook
// on the new runtime (GitHub issues #2309, #2344). The planning engine wrote
// the configured destroy-time arguments into the delete's prior state, which
// the plan records as the change's Before. The apply engine plans the delete
// again from the prior state, so this copies those same arguments from the
// planned Before into it, and the delete is sent what the plan showed. The
// old runtime gets the same result by sending the planned Before itself.
//
// Configuration is not evaluated again here: in the apply phase that would
// wait on the final state of its dependencies, which a destroy deletes after
// this object. The override's warning was shown at plan time, so the
// diagnostics are dropped.
func (ops *execOperations) destroyFromPlan(ctx context.Context, client providers.Interface, typeName string, addr addrs.AbsResourceInstanceObject, current cty.Value) cty.Value {
	if ops.plannedChanges == nil || client == nil || !addr.IsCurrent() || current == cty.NilVal || current.IsNull() {
		return current
	}
	if len(override.DestroyAttributes(ctx, client, typeName)) == 0 {
		return current
	}
	rc := ops.plannedChanges.ResourceInstance(addr.InstanceAddr)
	if rc == nil || rc.Action != plans.Delete {
		return current
	}
	before, err := rc.Before.Decode(current.Type())
	if err != nil || before == cty.NilVal {
		return current
	}
	unmarked, priorMarks := current.UnmarkDeepWithPaths()
	after, _ := override.DestroyConfig(ctx, client, typeName, unmarked, before)
	return after.MarkWithPaths(priorMarks)
}
