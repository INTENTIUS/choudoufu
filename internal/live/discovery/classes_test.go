// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestClassTableIsTotal is GitHub issue #810's guard for this package:
// adding an [identity.Class] fails here instead of being found later by a
// gauntlet estate whose instances of the new class were skipped by ten
// separate loops, each of which named the classes it knew and continued past
// everything else. Remove a row from classTable and this goes red; that is
// how it was proven load-bearing.
func TestClassTableIsTotal(t *testing.T) {
	missing, unknown := identity.ClassTableGaps(classTable)
	for _, c := range missing {
		t.Errorf("classTable has no row for identity.Class %q: decide what discovery does with it (does it join the binding demand? is it a parent worth a scoped list call? can it answer a formula lookup, or name a declared import identity?) rather than leaving every field to the zero handler, which skips it at all ten sites", c)
	}
	for _, c := range unknown {
		t.Errorf("classTable has a row for %q, which identity.AllClasses does not declare", c)
	}
}
