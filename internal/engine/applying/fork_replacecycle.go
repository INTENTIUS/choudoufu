// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

// choudoufu fork port (#2380): remove when upstream #4714 lands. The whole
// file is the apply engine's side of package replacecycle: what an applied
// object records as CreateBeforeDestroy in state.

import (
	"github.com/intentius/choudoufu/internal/engine/forkport/replacecycle"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/resources"
)

// forkCreateBeforeDestroy is the final plan's CreateBeforeDestroy: the
// configured create_before_destroy, or the prior object's, which the
// planning engine set from the effective replace order. False while the
// port is off.
func forkCreateBeforeDestroy(metadata *exec.ResourceInstanceObjectMeta, prior *exec.ResourceInstanceObject) bool {
	if !replacecycle.Enabled() {
		return false
	}
	if prior != nil && prior.State != nil && prior.State.CreateBeforeDestroy {
		return true
	}
	if metadata == nil {
		return false
	}
	order, marks := metadata.ReplaceOrder.Unmark()
	_ = marks
	v, ok := order.ValueOk()
	return ok && v == resources.ReplaceCreateFirst
}

// forkReplacedCreateFirst reports whether an apply is the create half of a
// create-first replace: only that has a fallback, the object it deposed.
// False while the port is off.
func forkReplacedCreateFirst(fallback *exec.ResourceInstanceObject) bool {
	return fallback != nil && replacecycle.Enabled()
}
