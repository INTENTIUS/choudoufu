// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestLookalikeGuardAgainstFloci is the veteran's fear, staged for real: an
// estate stood up by stock terraform, its state deleted the way every other
// test in this tier deletes it, and then this estate's own security group
// stripped of its tofu-estate and tofu-address tags with the AWS CLI - the
// console-cleanup-or-tag-policy-misfire scenario the lookalike guard exists
// for. The next `choudoufu live-plan` sees the declared address unmatched,
// like it would for a brand new resource, and the claim under test is that
// the plan does not stay quiet about it: the create it proposes for
// aws_security_group.main carries a warning naming the very security group
// that was just stripped, with the command that adopts it instead of
// duplicating it.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/foreign/ -run TestLookalikeGuardAgainstFloci -v
//
// Driving the built binary, like TestForeignAgainstFloci, because the claim
// is about what an operator sees in the plan output, not about a function
// this package exports.
func TestLookalikeGuardAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "lookalike-guard")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	port := flocitest.StartFloci(t, "cdf-lookalike")
	endpoint := "http://localhost:" + port

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := flocitest.CopyEstate(t)

	// Stock terraform stands the estate up - including aws_security_group.main,
	// whose tags are literal HCL in live/e2e/estate/network.tf rather than
	// anything choudoufu stamps, so the live security group carries its
	// tofu-estate and tofu-address tags the instant stock terraform creates
	// it. Then tofu init, mandatory for the same registry-mirroring reason
	// TestForeignAgainstFloci documents.
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	// The resource under attack: the estate's own security group, found by
	// the marker a moment before that marker is stripped off it.
	sgID := flocitest.AWSCLI(t, port, "ec2", "describe-security-groups",
		"--filters", "Name=tag:tofu-address,Values=aws_security_group.main",
		"--query", "SecurityGroups[0].GroupId", "--output", "text")
	if sgID == "" || sgID == "None" {
		t.Fatalf("the estate's security group could not be found by its marker")
	}
	t.Logf("aws_security_group.main is live as %s, about to lose its markers", sgID)

	// The attack: console cleanup, a tag policy misfire, anything that
	// strips tofu-estate and tofu-address off a live, server-assigned
	// resource without deleting it. What is left behind is indistinguishable
	// from a resource nobody ever owned.
	flocitest.AWSCLI(t, port, "ec2", "delete-tags",
		"--resources", sgID,
		"--tags", "Key=tofu-estate", "Key=tofu-address")

	// Confirm the strip actually worked before trusting anything the plan
	// says about it.
	remaining := flocitest.AWSCLI(t, port, "ec2", "describe-tags",
		"--filters", "Name=resource-id,Values="+sgID, "Name=key,Values=tofu-estate,tofu-address",
		"--query", "Tags[].Key", "--output", "text")
	if remaining != "" && remaining != "None" {
		t.Fatalf("the marker tags are still on %s after delete-tags: %q", sgID, remaining)
	}

	// --- The whole pipeline, through the command -------------------------
	start := time.Now()
	cmd := exec.Command(tofuBin, "live-plan", "-no-color", "-input=false") //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	output := string(out)
	t.Logf("choudoufu live-plan took %s\n%s", elapsed, output)
	if err != nil {
		t.Fatalf("live-plan failed: %v", err)
	}

	// The stripped security group's declared address is now unmatched by any
	// marker, exactly the shape of a brand new resource, so the plan
	// proposes creating it.
	if !strings.Contains(output, "# aws_security_group.main will be created") {
		t.Fatalf("the plan does not propose creating aws_security_group.main after its markers were stripped:\n%s", output)
	}

	// --- The load-bearing assertion: the warning names the stripped SG ---
	if !strings.Contains(output, "Possible duplicates:") {
		t.Fatalf("no lookalike-guard section in the output, though the stripped security group should have produced one:\n%s", output)
	}
	section := flocitest.SectionFrom(output, "Possible duplicates:")
	if !strings.Contains(section, "aws_security_group.main") {
		t.Errorf("the lookalike section does not name aws_security_group.main:\n%s", section)
	}
	if !strings.Contains(section, "[POSSIBLE DUPLICATE]") {
		t.Errorf("the lookalike section does not carry the [POSSIBLE DUPLICATE] tag:\n%s", section)
	}
	if !strings.Contains(section, sgID) {
		t.Errorf("the warning does not name the stripped security group %s:\n%s", sgID, section)
	}
	if !strings.Contains(section, "matched on: name=stateless-e2e-main") {
		t.Errorf("the warning does not show what it matched on:\n%s", section)
	}
	if !strings.Contains(section, "adopt with: aws ec2 create-tags") || !strings.Contains(section, sgID) {
		t.Errorf("the warning's adoption command does not name %s:\n%s", sgID, section)
	}
	if !strings.Contains(section, "tofu-estate,Value="+estateName) ||
		!strings.Contains(section, "tofu-address,Value=aws_security_group.main") {
		t.Errorf("the adoption command does not stamp both markers:\n%s", section)
	}

	// The guard warns; it never blocks. The create the plan already proposed
	// for aws_security_group.main is still there, unmodified by the
	// warning's presence - and the security group rules that carry
	// security_group_id = aws_security_group.main.id are legitimately
	// replaced alongside it, since a new group means a new ID for them to
	// point at. That ripple is a property of the dependency graph, not of
	// this guard, so the assertion here is narrow: the stripped security
	// group's own address is never proposed as a destroy, because it was
	// never in the prior state to begin with.
	add, _, destroy, ok := flocitest.PlanSummary(output)
	if ok {
		t.Logf("plan summary: %d to add, %d to destroy", add, destroy)
	}
	if ok && add == 0 {
		t.Errorf("the plan proposes no creates at all, but aws_security_group.main should be one:\n%s", output)
	}
	if strings.Contains(output, "# aws_security_group.main will be destroyed") {
		t.Error("the plan proposes destroying aws_security_group.main, which was never in the prior state to begin with")
	}
}
