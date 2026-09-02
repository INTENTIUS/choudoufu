// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestDowngradeForNodeResolution_InstanceFailureBecomesWarning is this
// unit's item 3: a real per-instance identity refusal (the same fixture
// TestNodeSeamComponentsFromValueResolvesWhatStaticRefuses uses) has its
// severity flipped, and the instance's absence from the Result is
// unaffected - downgrading the diagnostic never resolves the instance
// itself, only what the caller does about the fact that it did not
// resolve.
func TestDowngradeForNodeResolution_InstanceFailureBecomesWarning(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "node-seam-computed-boundary"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: siblingTestSchemas()})
	if !diags.HasErrors() {
		t.Fatalf("expected the ordinary static refusal before any downgrade")
	}
	if _, ok := result.Get(mustAddr(t, "aws_lb_target_group_attachment.reads_computed")); ok {
		t.Fatalf("reads_computed resolved statically; this test needs it refused")
	}

	downgraded := DowngradeForNodeResolution(diags)
	if downgraded.HasErrors() {
		t.Fatalf("expected no errors after downgrade:\n%s", renderDiags(downgraded))
	}
	if !hasDiag(downgraded, "Not an identity attribute", "computed_val") {
		t.Fatalf("the downgraded diagnostic should still be findable by summary/detail:\n%s", renderDiags(downgraded))
	}
	found := false
	for _, d := range downgraded {
		if d.Severity() == tfdiags.Warning && tfdiags.ExtraInfo[InstanceFailure](d).Addr == "aws_lb_target_group_attachment.reads_computed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no warning-severity diagnostic tagged with the refused instance's address")
	}

	// The instance itself is still absent from the result - downgrading
	// the diagnostic is not resolving the instance.
	if _, ok := result.Get(mustAddr(t, "aws_lb_target_group_attachment.reads_computed")); ok {
		t.Fatalf("downgrading a diagnostic must never resolve the instance it was about")
	}
}

// TestDowngradeForNodeResolution_LeavesNonInstanceErrorsFatal proves the
// downgrade is narrow: a diagnostic with no [InstanceFailure] tag - an
// ordinary configuration problem unrelated to one instance's identity -
// passes through untouched, at Error severity, so the run still stops.
func TestDowngradeForNodeResolution_LeavesNonInstanceErrorsFatal(t *testing.T) {
	plain := tfdiags.Sourceless(tfdiags.Error, "Some other problem", "not about any one instance")
	var diags tfdiags.Diagnostics
	diags = diags.Append(plain)

	downgraded := DowngradeForNodeResolution(diags)
	if !downgraded.HasErrors() {
		t.Fatalf("a non-instance error must remain fatal after downgrade")
	}
}

// TestDowngradeForNodeResolution_EmptyIsEmpty is a cheap guard against a
// nil/empty slice becoming a non-nil one with no elements, which would
// change every caller's `len(diags) == 0` check for no reason.
func TestDowngradeForNodeResolution_EmptyIsEmpty(t *testing.T) {
	if got := DowngradeForNodeResolution(nil); len(got) != 0 {
		t.Errorf("DowngradeForNodeResolution(nil) = %#v, want empty", got)
	}
}
