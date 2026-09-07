// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/refusalscan"
)

// TestRefusalsRegistered is the lockstep test internal/live/identity and
// internal/live/lint already have, applied to this package.
//
// It parses this package's own non-test source and requires every diagnostic
// Summary it finds to be in the registry, and every registry entry to be
// produced somewhere.
//
// Since GitHub issue #644 retired the HCL-rewriting engine, this package
// raises no diagnostic of its own at all: the three registry entries are
// produced by internal/live/check's node-path port and by
// internal/live/projection, and the scan's job here is the other direction
// - that no summary literal creeps back into this package outside the
// registry. [refusalscan.Params.Registered] is satisfied by the constants
// in summaries.go, which is why they stay constants rather than becoming
// literals at their raising sites.
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
	})
}
