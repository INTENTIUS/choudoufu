// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestIdentityWarningsRenderAsWarnings is GitHub issue #1120's follow-up:
// the table read every identity refusal as "error", so the
// foreign-resource-group warning ranked as a blocker. It reads the label off
// the same catalog the table is rendered from, so a refusal registered as a
// warning and a refusal registered as an error each render their own way.
func TestIdentityWarningsRenderAsWarnings(t *testing.T) {
	want := map[string]string{
		identity.SummaryForeignResourceGroup:   "warning",
		identity.SummaryNoOrphanRecovery:       "warning",
		identity.SummarySchemaDisagreement:     "warning",
		identity.SummarySchemaBreaking:         "error",
		"Two resources with the same identity": "error",
	}
	seen := map[string]bool{}
	for _, r := range check.AllRefusals() {
		if r.Layer != check.LayerIdentity {
			continue
		}
		label, ok := want[r.ID]
		if !ok {
			continue
		}
		seen[r.ID] = true
		if got := severityLabel(r); got != label {
			t.Errorf("%q renders %q, want %q", r.ID, got, label)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%q is not in the catalog under the identity layer", id)
		}
	}
}
