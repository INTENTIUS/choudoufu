// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestClassTableIsTotal is GitHub issue #810's guard for this package:
// adding an [identity.Class] fails here instead of passing gate 4 by
// omission, which is the direction that writes a marker. Remove a row from
// classTable and this goes red; that is how it was proven load-bearing.
func TestClassTableIsTotal(t *testing.T) {
	missing, unknown := identity.ClassTableGaps(classTable)
	for _, c := range missing {
		t.Errorf("classTable has no row for identity.Class %q: say whether an instance of it has an identity of its own to write down, rather than leaving the zero handler to answer \"yes, write the marker\"", c)
	}
	for _, c := range unknown {
		t.Errorf("classTable has a row for %q, which identity.AllClasses does not declare", c)
	}
}
