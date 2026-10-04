// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package tfdiags

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
)

func TestAppendWithoutDuplicates(t *testing.T) {
	at := func(line int) *hcl.Diagnostic {
		return &hcl.Diagnostic{
			Severity: hcl.DiagWarning,
			Summary:  "Deprecated Resource",
			Detail:   "use the _v1 type",
			Subject:  &hcl.Range{Filename: "main.tf", Start: hcl.Pos{Line: line, Column: 1}, End: hcl.Pos{Line: line, Column: 9}},
		}
	}
	var validate Diagnostics
	validate = validate.Append(at(1), at(5))

	var plan Diagnostics
	plan = plan.Append(at(1), at(5), at(9))
	errAt1 := *at(1)
	errAt1.Severity = hcl.DiagError
	plan = plan.Append(&errAt1)

	got := validate.AppendWithoutDuplicates(plan...)
	if len(got) != 4 {
		t.Fatalf("got %d diagnostics, want 4 (two from validate, line 9's warning, and the error at line 1 whose severity differs)", len(got))
	}
	if got[2].Source().Subject.Start.Line != 9 || got[3].Severity() != Error {
		t.Fatalf("unexpected merge result: %#v", got)
	}

	// Duplicates within newDiags itself are not the merge's business.
	var none Diagnostics
	if got := none.AppendWithoutDuplicates(plan[0], plan[0]); len(got) != 2 {
		t.Fatalf("got %d, want 2: only the receiver's existing members are deduplicated against", len(got))
	}
}
