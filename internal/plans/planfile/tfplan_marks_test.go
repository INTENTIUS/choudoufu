// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planfile

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/lang/marks"
)

// TestPathValueMarksToTfplanWritesOnlySensitivePaths (#2256 end phase): a
// plan file records sensitive paths only. The new runtime's planner marks
// every pending change (objchange.MarkPendingChanges), and when every mark
// was written as sensitive, a saved new-runtime plan read back with every
// planned value sensitive, which broke the live approval guard and any
// rendering of the saved plan. The default runtime never puts another kind
// of mark on a change, so nothing else catches this.
func TestPathValueMarksToTfplanWritesOnlySensitivePaths(t *testing.T) {
	type pendingLike struct{ addr string }
	pvm := []cty.PathValueMarks{
		{Path: cty.GetAttrPath("secret"), Marks: cty.NewValueMarks(marks.Sensitive)},
		{Path: cty.GetAttrPath("name"), Marks: cty.NewValueMarks(pendingLike{"aws_vpc.main"})},
		{Path: cty.GetAttrPath("both"), Marks: cty.NewValueMarks(marks.Sensitive, pendingLike{"aws_vpc.main"})},
	}
	got, err := pathValueMarksToTfplan(pvm)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Steps[0].GetAttributeName())
	}
	if len(names) != 2 || names[0] != "secret" || names[1] != "both" {
		t.Fatalf("sensitive paths written = %q, want [secret both]: a non-sensitive mark must not be stored as sensitive", names)
	}
}
