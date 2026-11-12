// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

import (
	"context"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/engine/forkport/datasources"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/lang/eval"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// DataRead implements [exec.Operations].
func (ops *execOperations) DataRead(
	ctx context.Context,
	desired *eval.DesiredResourceInstance,
	plannedVal cty.Value,
) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	log.Printf("[TRACE] apply phase: DataRead %s", desired.Addr)
	if ops.inRunEstates != nil && ops.forkInRunRead(desired) { // choudoufu fork port (#2370): remove when upstream #4058 lands
		return ops.forkDataRead(ctx, desired)
	}
	if datasources.Enabled() { // choudoufu fork port (#2414): remove when upstream #4058 and #4740 land
		return ops.forkReadData(ctx, desired)
	}
	if ops.inRunEstates != nil { // choudoufu fork port (#2370): remove when upstream #4058 lands
		return ops.forkDataRead(ctx, desired)
	}
	panic("unimplemented")
}
