// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"
)

// TestLivePlan_rootOutputsNoOpWhenUnchanged is GitHub issue #348's direct
// repro: a configuration whose root module declares `output` blocks that
// read a resource attribute, an expression built from one, and a sensitive
// value. Before the fix, [projection.Manager.GetRootOutputValues] (and, for
// live-plan specifically, the state handed straight to tofu.Context.Plan)
// never carried any root output values at all, so every declared output
// rendered as "+ new" on every single run, even when live-plan proposed
// changing zero resources. See internal/live/projection/outputs.go's
// ApplyRootOutputValues, which evaluates the root module's output
// expressions against the projected prior state before the plan runs - the
// stateless equivalent of what a normal refresh does before diffing "prior"
// output values against "planned" ones.
func TestLivePlan_rootOutputsNoOpWhenUnchanged(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-outputs"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id":     "tofu-stateless-unit-data",
		"bucket": "tofu-stateless-unit-data",
		"arn":    "arn:aws:s3:::tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("plan is not empty - issue #348's root outputs are showing a diff even though nothing changed:\n%s", stdout)
	}
	if strings.Contains(stdout, "Changes to Outputs:") {
		t.Errorf("root outputs still render a diff section with no resource changes:\n%s", stdout)
	}
	if strings.Contains(stdout, "bucket_arn") || strings.Contains(stdout, "bucket_label") || strings.Contains(stdout, "bucket_secret") {
		t.Errorf("an output name appears in the plan output at all, which only happens inside a diff section:\n%s", stdout)
	}
}

// TestLivePlan_rootOutputsChangeWhenResourceIsCreated is the negative
// control: when the resource an output reads from is genuinely new (not yet
// read from the live system, so live-plan proposes creating it), the output
// that reads its attribute has to show as changed too. A fix for #348 that
// makes every output unconditionally read as unchanged would be worse than
// the bug it replaces - HANDOFF.md's "did this change turn any warning into
// silence" question, applied to outputs instead of a marker.
func TestLivePlan_rootOutputsChangeWhenResourceIsCreated(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-outputs"), td)
	t.Chdir(td)

	// Nothing put in the cloud: the bucket does not exist yet.
	cloud := newStatelessTestCloud()

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (changes present, -detailed-exitcode)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if !strings.Contains(stdout, "aws_s3_bucket.data will be created") {
		t.Errorf("the un-materialized bucket is not planned for creation:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Changes to Outputs:") {
		t.Errorf("outputs reading the new bucket's attributes should show as changed:\n%s", stdout)
	}
}
