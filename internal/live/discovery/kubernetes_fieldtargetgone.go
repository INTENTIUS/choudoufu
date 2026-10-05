// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// SummaryFieldGranularTargetMissing is the refusal for a field-granular
// block whose patched object does not exist (#1885, ruled 2026-10-04,
// option (b)).
//
// Stock keeps the block in its state and plans "No changes.": the
// provider's Read answers a NotFound with a warning ("Resource deleted",
// "Secret deleted", "ConfigMap deleted") and leaves the state as it was.
// choudoufu has no state to keep, so the instance is absent and the plan
// would be a create, which the provider's Create refuses at apply ("The
// resource ... does not exist"). A plan that cannot apply is refused here
// instead, naming the object. A destroy plan is not refused: the instance
// is absent and nothing is destroyed, as stock's destroy of the same block
// does nothing to an object that is gone.
const SummaryFieldGranularTargetMissing = "Patched object does not exist"

// FieldGranularMissingRefusals is one error per declared field-granular
// instance whose patched object does not exist, in address order. The
// caller raises it for a plan that is not a destroy. scope is the run's
// -target/-exclude narrowing (nil for an untargeted run): an instance the
// run does not touch is not refused over (#1176, #1203).
func FieldGranularMissingRefusals(res *Result, scope identity.Scope) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if res == nil || len(res.FieldGranularMissing) == 0 {
		return diags
	}
	names := make([]string, 0, len(res.FieldGranularMissing))
	for a := range res.FieldGranularMissing {
		if scope != nil {
			inst, parseDiags := addrs.ParseAbsResourceInstanceStr(a)
			if !parseDiags.HasErrors() && !scope(inst.ConfigResource()) {
				continue
			}
		}
		names = append(names, a)
	}
	sort.Strings(names)
	for _, a := range names {
		obj := res.FieldGranularMissing[a]
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryFieldGranularTargetMissing, fmt.Sprintf(
			"The object this block patches does not exist: %s writes fields of %s, and the cluster has no such object. Recreate it, or remove the block. With nothing to write into, the apply would fail; stock plans no change here only because its state file still holds the fields.",
			a, obj)))
	}
	return diags
}
