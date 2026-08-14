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
// produced somewhere. Two of the four summaries reach hcl.Diagnostic through
// a parameter rather than as a literal field, which is why they are declared
// as Summary-prefixed constants; see refusals.go.
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
