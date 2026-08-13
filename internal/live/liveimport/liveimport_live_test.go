// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// This is issue #61's live half, and like mv's and foreign's it drives the
// built binary rather than the package underneath it: the claim under test
// is what an operator sees and what a later live-plan can do with what
// live-import wrote, both properties of the command rather than of a
// function this package exports.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/liveimport/ -run TestLiveImportAgainstFloci -v
//
// The shape is exactly the one the issue asks for:
//
//  1. Stock terraform stands up live/e2e/import-fixture - a small,
//     deliberately marker-free configuration - the ordinary way: init,
//     apply, a real terraform.tfstate on disk. Nothing about this step goes
//     through choudoufu at all.
//  2. "choudoufu live-import -state=... -estate=..." ratifies the state
//     file's two resources against floci. Both are read-only server-assigned
//     types, so before any marker exists a plain live-plan cannot find them
//     by anything but a marker - this is the estate live-import exists to
//     migrate.
//  3. The same command, given -approve, stamps tofu-estate and tofu-address
//     directly onto the two live resources. The tfstate file is never
//     touched by either run - this test asserts its mtime and content are
//     unchanged at the end.
//  4. "choudoufu live-plan -estate=..." over the same configuration now
//     finds both resources by their fresh markers and proposes no changes at
//     all: the empty diff the issue asks this test to prove.
//
// It is gated because it needs Docker, terraform, the AWS CLI and a Go
// toolchain, the same requirements mv's and foreign's live tests carry.

const (
	importAwsRegion  = "us-east-1"
	importTerraform  = "terraform"
	importEstateName = "live-import-e2e"
)

func TestLiveImportAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "live-import bulk migration")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, importTerraform)

	flociPort := flocitest.StartFloci(t, "cdf-p61")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", importAwsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := flocitest.CopyFixtureDir(t, flocitest.ImportFixtureDir(t))

	// --- 1. Stand the estate up the ordinary way -------------------------
	flocitest.Run(t, dir, importTerraform, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, importTerraform, "apply", "-auto-approve", "-input=false", "-no-color")
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	statePath := filepath.Join(dir, "terraform.tfstate")
	before, err := os.ReadFile(statePath) //nolint:gosec // a fixed path in this test's own temp dir
	if err != nil {
		t.Fatalf("stock apply left no readable state file: %v", err)
	}
	beforeInfo, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat on the state file: %v", err)
	}

	// --- 2. Ratify, without -approve: read-only -------------------------
	start := time.Now()
	ratifyOut := runLiveImport(t, tofuBin, dir, "-state=terraform.tfstate", "-estate="+importEstateName, "-no-color", "-input=false")
	t.Logf("live-import (ratify) took %s\n%s", time.Since(start), ratifyOut)

	for _, want := range []string{"aws_vpc.main", "aws_security_group.main", "No tag has been written"} {
		if !strings.Contains(ratifyOut, want) {
			t.Errorf("the ratify-only report does not mention %q:\n%s", want, ratifyOut)
		}
	}
	verifiedCount := strings.Count(ratifyOut, "VERIFIED (")
	if verifiedCount == 0 || strings.Contains(ratifyOut, "VERIFIED (0)") {
		t.Errorf("nothing verified on the ratify-only run:\n%s", ratifyOut)
	}

	assertStateUnchanged(t, statePath, before, beforeInfo)

	// --- 3. Approve: the one step that writes ----------------------------
	start = time.Now()
	approveOut := runLiveImport(t, tofuBin, dir, "-state=terraform.tfstate", "-estate="+importEstateName, "-approve", "-no-color", "-input=false")
	t.Logf("live-import -approve took %s\n%s", time.Since(start), approveOut)

	for _, want := range []string{"STAMPED", "aws_vpc.main", "aws_security_group.main"} {
		if !strings.Contains(approveOut, want) {
			t.Errorf("the approve report does not mention %q:\n%s", want, approveOut)
		}
	}
	if strings.Contains(approveOut, "FAILED") {
		t.Errorf("the approve run reports a FAILED outcome:\n%s", approveOut)
	}

	assertStateUnchanged(t, statePath, before, beforeInfo)

	// The markers landed on the live objects themselves.
	vpcID := flocitest.AWSCLI(t, flociPort, "ec2", "describe-vpcs",
		"--filters", "Name=tag:tofu-estate,Values="+importEstateName,
		"--query", "Vpcs[0].VpcId", "--output", "text")
	if vpcID == "" || vpcID == "None" {
		t.Fatalf("no VPC carries tofu-estate = %s after -approve", importEstateName)
	}
	sgID := flocitest.AWSCLI(t, flociPort, "ec2", "describe-security-groups",
		"--filters", "Name=tag:tofu-estate,Values="+importEstateName, "Name=tag:tofu-address,Values=aws_security_group.main",
		"--query", "SecurityGroups[0].GroupId", "--output", "text")
	if sgID == "" || sgID == "None" {
		t.Fatalf("no security group carries this estate's markers after -approve")
	}

	// --- 4. The empty-diff proof ------------------------------------------
	start = time.Now()
	cmd := exec.Command(tofuBin, "live-plan", "-estate="+importEstateName, "-no-color", "-input=false") //nolint:gosec // paths are this test's own temp dir
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	planOutput := string(out)
	t.Logf("live-plan took %s\n%s", time.Since(start), planOutput)
	if err != nil {
		t.Fatalf("live-plan failed: %v", err)
	}

	add, change, destroy, ok := flocitest.PlanSummary(planOutput)
	switch {
	case strings.Contains(planOutput, "No changes."):
		t.Log("live-plan sees no changes: the imported markers bound both resources")
	case !ok:
		t.Fatalf("could not read a plan summary out of the live-plan output:\n%s", planOutput)
	default:
		t.Errorf("live-import should have made live-plan's diff empty; got %d add, %d change, %d destroy:\n%s", add, change, destroy, planOutput)
	}
	if strings.Contains(planOutput, "will be created") || strings.Contains(planOutput, "will be destroyed") {
		t.Errorf("live-plan's output contains a create or destroy after live-import:\n%s", planOutput)
	}

	// The state file is still exactly what stock terraform wrote, all the
	// way through both live-import runs and the live-plan that followed.
	assertStateUnchanged(t, statePath, before, beforeInfo)
}

func runLiveImport(t *testing.T, tofuBin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(tofuBin, append([]string{"live-import"}, args...)...) //nolint:gosec // paths are this test's own temp dir
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		t.Fatalf("live-import %v failed: %v\n%s", args, err, output)
	}
	return output
}

// assertStateUnchanged is this test's version of the package's central
// claim: live-import never writes to the state file and never reads it a
// second time. Byte-for-byte content plus an unchanged mtime is the closest
// an external test can get to proving "never opened for writing" from
// outside the process.
func assertStateUnchanged(t *testing.T, path string, wantContent []byte, wantInfo os.FileInfo) {
	t.Helper()

	gotInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat on the state file: %v", err)
	}
	if !gotInfo.ModTime().Equal(wantInfo.ModTime()) {
		t.Errorf("the state file's mtime changed: was %s, now %s - something wrote to it", wantInfo.ModTime(), gotInfo.ModTime())
	}

	got, err := os.ReadFile(path) //nolint:gosec // a fixed path in this test's own temp dir
	if err != nil {
		t.Fatalf("reading the state file: %v", err)
	}
	if string(got) != string(wantContent) {
		t.Errorf("the state file's content changed; live-import must never write it")
	}
}
