// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestClassTableIsTotal is GitHub issue #810's guard for this package:
// adding an [identity.Class] fails here instead of being found later by a
// rename that took the marker-rewrite path for a resource whose identity is
// not a marker. Remove a row from classTable and this goes red; that is how
// it was proven load-bearing.
func TestClassTableIsTotal(t *testing.T) {
	missing, unknown := identity.ClassTableGaps(classTable)
	for _, c := range missing {
		t.Errorf("classTable has no row for identity.Class %q: decide what a rename does with it (refuse, because the identity is a record key? search by listing? materialize against the whole resolution list?) rather than leaving all three to the zero handler and rewriting a marker", c)
	}
	for _, c := range unknown {
		t.Errorf("classTable has a row for %q, which identity.AllClasses does not declare", c)
	}
}
