// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
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
// does nothing to an object that is gone. Neither is a block whose object
// the same plan creates: that is an ordinary create in dependency order.
const SummaryFieldGranularTargetMissing = "Patched object does not exist"

// PlannedObject is a whole Kubernetes object a plan creates (or
// replaces), read off the planned value: its kind, namespace and name.
// Known is false when any of the three is not known until apply.
type PlannedObject struct {
	Kind, Namespace, Name string
	Known                 bool
}

// FieldGranularMissingRefusals is one error, in address order, per planned
// create of a field-granular instance whose patched object discovery found
// missing (missing, [Result.FieldGranularMissing]) and that nothing in the
// same plan creates. creates and planned are one cluster's - one provider
// configuration's - planned field-granular creates and whole-object
// creates. The plan is the input, not the configuration, so a destroy plan
// (no creates), a -target that prunes the block, and a block whose object
// the same plan creates (corpus-govuk-cluster-services' greenfield: a
// kubernetes_labels on a kubernetes_secret_v1 of the same root) are never
// refused. A planned object whose identity is not known yet could be the
// one, so it suppresses the refusal for its kind: a false negative leaves
// the answer to the apply, a false positive stops a plan that would work.
func FieldGranularMissingRefusals(missing map[string]string, creates []FieldGranularWrite, planned []PlannedObject) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if len(missing) == 0 || len(creates) == 0 {
		return diags
	}
	sameNamespace := func(a, b string) bool {
		if a == "" {
			a = "default"
		}
		if b == "" {
			b = "default"
		}
		return a == b
	}
	createdHere := func(o kubesweep.ObjectRef) bool {
		for _, p := range planned {
			if !p.Known {
				if p.Kind == "" || p.Kind == o.Kind {
					return true
				}
				continue
			}
			if p.Kind == o.Kind && p.Name == o.Name && (sameNamespace(p.Namespace, o.Namespace) || p.Kind == "Namespace" || p.Kind == "Node") {
				return true
			}
		}
		return false
	}
	sorted := append([]FieldGranularWrite(nil), creates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Addr.String() < sorted[j].Addr.String() })
	for _, w := range sorted {
		a := w.Addr.String()
		obj, gone := missing[a]
		if !gone || w.Object.Name == "" || createdHere(w.Object) {
			continue
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryFieldGranularTargetMissing, fmt.Sprintf(
			"The object this block patches does not exist: %s writes fields of %s, and the cluster has no such object. Recreate it, or remove the block. With nothing to write into, the apply would fail; stock plans no change here only because its state file still holds the fields.",
			a, obj)))
	}
	return diags
}
