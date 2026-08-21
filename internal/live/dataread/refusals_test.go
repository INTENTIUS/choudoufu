// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/refusalscan"
)

// TestRefusalsRegistered is the lockstep test every live registry carries:
// every summary this package can produce is registered, and every
// registered summary is producible.
//
// The two dynamic sites are the one legitimate shape bent slightly: both
// pass a Summary-prefixed constant through a variable - refuse takes the
// summary as a parameter, and read raises a Source's classified
// ReasonSummary, which classify assigned from the same constants. Every
// value that can flow there is one of the constants in summaries.go, which
// the scan sees and the registry covers.
func TestRefusalsRegistered(t *testing.T) {
	summaries := make([]string, 0, len(refusals))
	whats := map[string]string{}
	for _, r := range Refusals() {
		summaries = append(summaries, r.Summary)
		whats[r.Summary] = r.What
	}
	refusalscan.Check(t, refusalscan.Params{
		Dir:          ".",
		SkipFile:     "refusals.go",
		Registered:   summaries,
		What:         whats,
		DynamicSites: []string{"refuse", "read"},
	})
}
