// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"strings"
	"testing"
)

// TestLiveModeMovedRenameOnNewRuntime is #2256 U10's done-when (#2374): a
// live-mode rename declared with a moved block runs on the new runtime,
// through the real apply entry point, as an in-place tag update.
//
//  1. apply over an empty cloud creates both resources with their markers;
//  2. the configuration renames aws_vpc.main to aws_vpc.primary and records
//     the rename in a moved block;
//  3. a plan and then an apply show one in-place update and no create or
//     destroy, and the VPC reaches the cloud carrying its new tofu-address.
//
// The VPC is the renamed one because nothing in its configuration names it:
// the only way the second run finds it is by its old marker, which
// internal/live/discovery indexes under the new address (internal/live/moved).
// With the gap table's moved row refusing in live mode, step 3 fails on that
// refusal.
func TestLiveModeMovedRenameOnNewRuntime(t *testing.T) {
	t.Setenv("TOFU_X_EXPERIMENTAL_RUNTIME", "1")
	t.Setenv("TOFU_X_EXPERIMENTAL_RUNTIME_NEXT", "")

	cloud := newNewRuntimeCycleCloud()
	apply := func(m Meta) int {
		return (&ApplyCommand{Meta: m}).Run([]string{"-no-color", "-auto-approve"})
	}
	plan := func(m Meta) int {
		return (&PlanCommand{Meta: m}).Run([]string{"-no-color"})
	}

	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	// 1. Create.
	stdout := newRuntimeCycleRun(t, "create", apply, cloud.meta)
	if !strings.Contains(stdout, "Apply complete! Resources: 2 added, 0 changed, 0 destroyed.") {
		t.Fatalf("create: the apply did not create both resources:\n%s", stdout)
	}
	if got := cloud.liveTestCloud.tags["aws_vpc/"+newRuntimeCycleVPC]["tofu-address"]; got != "aws_vpc.main" {
		t.Fatalf("create: the stored VPC carries tofu-address %q, want %q", got, "aws_vpc.main")
	}

	// 2. Rename, with a moved block.
	src, err := os.ReadFile("main.tf")
	if err != nil {
		t.Fatal(err)
	}
	renamed := strings.Replace(string(src), `resource "aws_vpc" "main" {`, `resource "aws_vpc" "primary" {`, 1)
	if renamed == string(src) {
		t.Fatal("the fixture no longer declares aws_vpc.main; the rename did not apply")
	}
	renamed += "\nmoved {\n  from = aws_vpc.main\n  to   = aws_vpc.primary\n}\n"
	if err := os.WriteFile("main.tf", []byte(renamed), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Plan, then apply: one in-place update.
	stdout = newRuntimeCycleRun(t, "rename plan", plan, cloud.meta)
	if !strings.Contains(stdout, "aws_vpc.primary will be updated in-place") {
		t.Errorf("rename plan: aws_vpc.primary is not planned as an in-place update:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Plan: 0 to add, 1 to change, 0 to destroy.") {
		t.Fatalf("rename plan: want exactly one in-place update and no create or destroy:\n%s", stdout)
	}

	stdout = newRuntimeCycleRun(t, "rename apply", apply, cloud.meta)
	if !strings.Contains(stdout, "Apply complete! Resources: 0 added, 1 changed, 0 destroyed.") {
		t.Fatalf("rename apply: want one change and no create or destroy:\n%s", stdout)
	}
	if n := cloud.createCount(); n != 2 {
		t.Errorf("rename apply: the cloud saw %d creates in all, want the 2 from step 1", n)
	}
	if len(cloud.destroyed) != 0 {
		t.Errorf("rename apply: the cloud saw destroys %v, want none", cloud.destroyed)
	}
	tags := cloud.applied["aws_vpc.primary"]
	if tags == nil {
		t.Fatalf("rename apply: no object was applied carrying tofu-address aws_vpc.primary; applied: %v", cloud.applied)
	}
	if got := tags["tofu-estate"]; got != "live-unit" {
		t.Errorf("rename apply: aws_vpc.primary was applied with tofu-estate %q, want %q", got, "live-unit")
	}
}
