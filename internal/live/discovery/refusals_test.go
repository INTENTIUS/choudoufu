// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/refusalscan"
)

// TestRefusalsRegistered is the lockstep test internal/live/identity and
// internal/live/stamp also carry: this package cannot gain a refusal without
// gaining a registry entry for it, and cannot keep an entry for a refusal it
// no longer produces.
func TestRefusalsRegistered(t *testing.T) {
	summaries := make([]string, 0, len(refusals))
	whats := map[string]string{}
	for _, r := range Refusals() {
		summaries = append(summaries, r.Summary)
		whats[r.Summary] = r.What
	}
	refusalscan.Check(t, refusalscan.Params{
		Dir:        ".",
		SkipFile:   "refusals.go",
		Registered: summaries,
		What:       whats,
		// Fourteen of this package's diagnostics choose their summary out
		// of problemSummaries by ProblemKind. Naming the map is what makes
		// those values visible to the scan; problemDiag is the one function
		// that reads it and passes the result on, and every value it can
		// read is registered by the map scan already.
		SummaryMaps:  []string{"problemSummaries"},
		DynamicSites: []string{"problemDiag"},
	})
}
