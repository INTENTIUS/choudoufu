// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planning

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/override"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// destroyFromConfig is choudoufu's destroy-from-config hook on the new
// runtime (GitHub issues #2309, #2344), the counterpart of the one in the
// old runtime's planDestroy (internal/tofu). For a current object being
// deleted while its resource instance is still declared, each destroy-time
// argument a provider override names (skip_final_snapshot, force_destroy,
// ...) takes its configured value in the prior state that the delete is
// planned with and recorded as. The apply engine reads that value back from
// the plan (see the applying package's destroyFromPlan) rather than
// evaluating configuration again.
//
// The configuration comes through the per-object metadata (upstream `#4655`),
// so it is whatever the evaluator holds for the instance. It is swapped for
// upstream's own destroy-time arguments when `#3472` closes.
func (p *planGlue) destroyFromConfig(ctx context.Context, meta *exec.ResourceInstanceObjectMeta, providerInst addrs.AbsProviderInstanceCorrect, addr addrs.AbsResourceInstanceObject, prior cty.Value) (cty.Value, tfdiags.Diagnostics) {
	if !addr.IsCurrent() || meta.DestroyConfigValue == nil || prior.IsNull() {
		return prior, nil
	}
	client, _ := p.providerClient(ctx, providerInst)
	if client == nil || len(override.DestroyAttributes(ctx, client, meta.ResourceType)) == 0 {
		return prior, nil
	}
	config := keepSensitiveMarksOnly(meta.DestroyConfigValue(ctx))
	if config == cty.NilVal {
		return prior, nil
	}
	unmarkedPrior, priorMarks := prior.UnmarkDeepWithPaths()
	after, diags := override.DestroyConfig(ctx, client, meta.ResourceType, unmarkedPrior, config)
	return after.MarkWithPaths(priorMarks), diags
}

// keepSensitiveMarksOnly drops the evaluator's bookkeeping marks from a
// configuration value and keeps sensitive and ephemeral ones, so that
// [override.DestroyConfig] still leaves an argument set from a sensitive or
// ephemeral value alone, as it does on the old runtime.
func keepSensitiveMarksOnly(v cty.Value) cty.Value {
	if v == cty.NilVal {
		return v
	}
	unmarked, pvms := v.UnmarkDeepWithPaths()
	var keep []cty.PathValueMarks
	for _, pvm := range pvms {
		kept := cty.NewValueMarks()
		for m := range pvm.Marks {
			if m == marks.Sensitive || m == marks.Ephemeral {
				kept[m] = struct{}{}
			}
		}
		if len(kept) > 0 {
			keep = append(keep, cty.PathValueMarks{Path: pvm.Path, Marks: kept})
		}
	}
	return unmarked.MarkWithPaths(keep)
}
