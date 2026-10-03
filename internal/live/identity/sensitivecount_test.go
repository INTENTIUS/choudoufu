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

// #1792: stock unmarks a sensitive count and refuses only an ephemeral one
// (internal/lang/evalchecks/eval_count.go, "sensitive values are allowed
// in count but not for_each, as using it here will not disclose the
// sensitive value"). A count's instance keys are 0..n-1, so a marker naming
// aws_s3_bucket.data[0] says nothing about the value that sized it. The
// identity path refused both, which turned a configuration stock plans
// into a refusal.

// TestSensitiveCountResolvesLikeStock covers the resource and the module
// call: each has its own count evaluation (buildExpansion and
// ChildModuleCountKeys), and fixing one leaves the other refusing.
func TestSensitiveCountResolvesLikeStock(t *testing.T) {
	t.Run("resource", func(t *testing.T) {
		cfg := loadConfig(t, filepath.Join("testdata", "sensitive-count"), nil)
		result, diags := Resolve(context.Background(), cfg)
		assertNoErrors(t, diags)
		assertClassifications(t, result, map[string]string{
			`aws_s3_bucket.data[0]`: `CONCRETE estate-data-0`,
			`aws_s3_bucket.data[1]`: `CONCRETE estate-data-1`,
			`aws_s3_bucket.data[2]`: `CONCRETE estate-data-2`,
		})
	})
	t.Run("module call", func(t *testing.T) {
		cfg := loadConfigTree(t, filepath.Join("testdata", "module-count-sensitive"), nil)
		result, diags := Resolve(context.Background(), cfg)
		assertNoErrors(t, diags)
		assertClassifications(t, result, map[string]string{
			`module.user[0].aws_iam_user.this`: `CONCRETE user-0`,
			`module.user[1].aws_iam_user.this`: `CONCRETE user-1`,
		})
	})
}

// TestEphemeralCountStillRefuses is the half of stock's rule that stays: an
// ephemeral count could expose its value as an instance key, and stock
// refuses it with "Invalid count argument". The resource case is in
// TestResolveErrors' table (ephemeral-count); this is the module call's.
func TestEphemeralCountStillRefuses(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-count-ephemeral"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if !hasDiag(diags, "Sensitive count expression", `module "user"`) {
		t.Fatalf("an ephemeral module count was not refused by name; got:\n%s", renderDiags(diags))
	}
	if _, ok := result.Get(mustAddr(t, `module.user[0].aws_iam_user.this`)); ok {
		t.Errorf("module.user[0].aws_iam_user.this resolved under an ephemeral count")
	}
}
