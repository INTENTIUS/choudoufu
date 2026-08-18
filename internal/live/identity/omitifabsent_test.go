// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOmitIfAbsent proves [Component.OmitIfAbsent] against the provider's
// own two documented import forms for aws_lambda_permission (aws 6.59.0's
// Import section):
//
//	my_test_lambda_function/AllowExecutionFromCloudWatch
//	my_test_lambda_function:qualifier_name/AllowExecutionFromCloudWatch
//
// This is the defect the field exists to fix: before it, the ratified row
// had no component naming qualifier at all, so two aws_lambda_permission
// declarations differing only in qualifier rendered the identical ImportID
// and collided under the duplicate-identity guard. Both directions of
// getting an optional component wrong are wrong markers - a spurious ':'
// on an unqualified permission, or a dropped qualifier on a qualified one -
// so both shapes are asserted here, string for string against the
// documented examples, not just "resolves" or "differs".
func TestOmitIfAbsent(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "omit-if-absent"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if len(diags) > 0 {
		for _, d := range diags {
			t.Logf("diagnostic: %s: %s", d.Description().Summary, d.Description().Detail)
		}
		t.Fatalf("unexpected diagnostics (%d)", len(diags))
	}

	unqualified := resolutionAt(t, result, "aws_lambda_permission.unqualified")
	if unqualified.Class != ClassConcrete {
		t.Fatalf("unqualified resolved %s, want concrete", unqualified.Class)
	}
	if want := "my_test_lambda_function/AllowExecutionFromCloudWatch"; unqualified.ImportID != want {
		t.Errorf("unqualified rendered %q, want %q (the provider's own unqualified import example - no colon, no qualifier segment)", unqualified.ImportID, want)
	}
	if got, ok := unqualified.IdentityValues["qualifier"]; ok {
		t.Errorf("unqualified carries IdentityValues[qualifier] = %q; an omitted qualifier must supply no identity value at all, not an empty one", got)
	}

	qualified := resolutionAt(t, result, "aws_lambda_permission.qualified")
	if qualified.Class != ClassConcrete {
		t.Fatalf("qualified resolved %s, want concrete", qualified.Class)
	}
	if want := "my_test_lambda_function:qualifier_name/AllowExecutionFromCloudWatch"; qualified.ImportID != want {
		t.Errorf("qualified rendered %q, want %q (the provider's own qualified import example)", qualified.ImportID, want)
	}
	// The identity-attribute value is the qualifier alone - "qualifier_name"
	// - never ":qualifier_name". The colon is punctuation in the ImportID
	// string; the provider's identity schema attribute has no punctuation
	// of its own. See [Component.Literal]'s doc comment.
	if want := "qualifier_name"; qualified.IdentityValues["qualifier"] != want {
		t.Errorf("qualified's qualifier identity value = %q, want %q (unprefixed - the colon belongs to the ImportID string only)", qualified.IdentityValues["qualifier"], want)
	}

	if unqualified.ImportID == qualified.ImportID {
		t.Fatalf("both instances rendered the same ImportID (%q); that is the exact collision this field exists to prevent", unqualified.ImportID)
	}

	// The corpus regression: `qualifier = try(each.value.qualifier, null)`
	// (.corpus/s3-bucket/modules/notification/main.tf:72) writes the
	// argument, so it is not syntactic absence, but this instance's map has
	// no "qualifier" key and the expression statically evaluates to null.
	// That must resolve exactly like the unqualified case, not raise "Null
	// identity argument".
	presentButNull := resolutionAt(t, result, `aws_lambda_permission.present_but_null["from_map"]`)
	if presentButNull.Class != ClassConcrete {
		t.Fatalf("present_but_null resolved %s, want concrete", presentButNull.Class)
	}
	if want := "my_test_lambda_function/AllowFromMapNotification"; presentButNull.ImportID != want {
		t.Errorf("present_but_null rendered %q, want %q (a present-but-null qualifier must omit exactly like an absent one)", presentButNull.ImportID, want)
	}
	if got, ok := presentButNull.IdentityValues["qualifier"]; ok {
		t.Errorf("present_but_null carries IdentityValues[qualifier] = %q; a statically-null qualifier must supply no identity value at all", got)
	}
}
