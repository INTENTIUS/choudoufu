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

// TestImpureFunctionIdentityRefused is the regression for finding F-IMPURE.
//
// configs' static scope ran with PureOnly:false, so uuid() and timestamp()
// returned real values during identity resolution. The resolution came out
// CONCRETE, carrying an import ID that was different on every run and looked
// like every other one, so nothing downstream had anything to notice: each
// plan proposed a create and each apply leaked a resource.
//
// The requirement is not merely "does not resolve". It is a named error and
// no resolution at all, for the direct call and for the same function one
// reference away.
func TestImpureFunctionIdentityRefused(t *testing.T) {
	for _, tc := range []struct {
		dir string
		// wantSummary is the diagnostic heading; the two fixtures reach the
		// refusal by different routes and say so differently.
		wantSummary string
		wantDetail  string
		wantAbsent  []string
	}{
		{
			dir:         "impure-name",
			wantSummary: "Identity derived from an impure function",
			wantDetail:  "uuid()",
			wantAbsent:  []string{"aws_s3_bucket.data", "aws_cloudwatch_log_group.app"},
		},
		{
			dir:         "impure-local",
			wantSummary: "Non-static identity argument",
			wantDetail:  "aws_s3_bucket.data.bucket",
			wantAbsent:  []string{"aws_s3_bucket.data"},
		},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			cfg := loadConfig(t, filepath.Join("testdata", tc.dir), nil)
			result, diags := Resolve(context.Background(), cfg)

			if !diags.HasErrors() {
				t.Fatalf("no error diagnostics; resolution produced %d instances", result.Len())
			}
			if !hasDiag(diags, tc.wantSummary, tc.wantDetail) {
				t.Errorf("no diagnostic with summary %q naming %q. got:\n%s", tc.wantSummary, tc.wantDetail, renderDiags(diags))
			}
			for _, absent := range tc.wantAbsent {
				if res, ok := result.Get(mustAddr(t, absent)); ok {
					t.Errorf("%s resolved anyway, as %s with import ID %q: a fabricated identity is the failure this test exists for",
						absent, res.Class, res.ImportID)
				}
			}
		})
	}
}

// TestImpureFunctionNames covers the recognizer directly, including the
// namespaced spelling and the nesting the audit's expression used.
func TestImpureFunctionNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"bare", `uuid()`, "uuid"},
		{"namespaced", `core::uuid()`, "uuid"},
		{"nested in a template", `"estate-${timestamp()}"`, "timestamp"},
		{"nested in a call", `upper(substr(uuid(), 0, 8))`, "uuid"},
		{"bcrypt", `bcrypt("hunter2")`, "bcrypt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := impureCallsIn(parseExprForTest(t, tc.src))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("impureCallsIn(%s) = %v, want [%s]", tc.src, got, tc.want)
			}
		})
	}

	for _, src := range []string{
		`"estate-static"`,
		`upper(var.name)`,
		`format("%s-%s", var.a, var.b)`,
		`md5("stable input")`,
		`timestamped_thing.name`,
	} {
		if got := impureCallsIn(parseExprForTest(t, src)); len(got) != 0 {
			t.Errorf("impureCallsIn(%s) = %v, want none", src, got)
		}
	}
}
