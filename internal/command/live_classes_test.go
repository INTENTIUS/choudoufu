// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestLiveClassTableIsTotal is GitHub issue #810's guard for this package:
// adding an [identity.Class] fails here instead of being found later by a
// gauntlet estate whose live-plan labelled a bound instance wrong or whose
// live-ls reported a rung as a gap. Remove a row from liveClassTable and
// this goes red; that is how it was proven load-bearing.
func TestLiveClassTableIsTotal(t *testing.T) {
	missing, unknown := identity.ClassTableGaps(liveClassTable)
	for _, c := range missing {
		t.Errorf("liveClassTable has no row for identity.Class %q: decide what the live commands do with it (vouch a cache read? bound-report source? formula parents? a live-ls rung?) rather than leaving every one of them to the zero handler", c)
	}
	for _, c := range unknown {
		t.Errorf("liveClassTable has a row for %q, which identity.AllClasses does not declare", c)
	}
}

// TestLiveClassRungDetailTravelsWithRung holds the one pairing inside a row
// that a total-table check cannot see: liveLsRung returns the detail
// sentence alongside the rung, and a row carrying one without the other
// would report an empty explanation to an operator.
func TestLiveClassRungDetailTravelsWithRung(t *testing.T) {
	for _, c := range identity.AllClasses() {
		h := liveClassTable[c]
		if (h.lsRung == "") != (h.lsDetail == "") {
			t.Errorf("liveClassTable[%s] has lsRung=%q and lsDetail=%q; a rung with no detail (or the reverse) is a live-ls row an operator cannot act on", c, h.lsRung, h.lsDetail)
		}
	}
}
