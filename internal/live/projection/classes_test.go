// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestClassTableIsTotal is GitHub issue #810's guard for this package:
// adding an [identity.Class] fails here instead of being found later by a
// gauntlet estate that took the wrong path. Remove a row from classTable
// and this goes red; that is how it was proven load-bearing.
func TestClassTableIsTotal(t *testing.T) {
	missing, unknown := identity.ClassTableGaps(classTable)
	for _, c := range missing {
		t.Errorf("classTable has no row for identity.Class %q: decide what projection does with it (hold it undeclared? its own record door? which orderWork list?) rather than letting classFor's needs-discovery fallback decide", c)
	}
	for _, c := range unknown {
		t.Errorf("classTable has a row for %q, which identity.AllClasses does not declare", c)
	}
}

// TestEveryClassRoutesSomewhere is the half of the table the type system
// cannot check: a row present but with no orderWork function would compile,
// and then panic on the first resolution of that class.
func TestEveryClassRoutesSomewhere(t *testing.T) {
	for _, c := range identity.AllClasses() {
		if classTable[c].orderWork == nil {
			t.Errorf("classTable[%s].orderWork is nil; orderWork would panic on a resolution of that class", c)
		}
	}
}
