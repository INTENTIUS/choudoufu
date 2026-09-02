// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markerstrip_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestStatefulPlanAfterLiveImportAgainstFloci is GitHub issue #613's
// reproduction, run against the emulator with the real AWS provider, and
// then its refusal.
//
// #611 measured the defect at two scales of tools/terralith-gen (38 of 79
// instances, 137 of 301). This is the same sequence with two resources,
// which is all it takes and which fits in a test:
//
//  1. a stateful apply, no live block anywhere        -> a state file
//  2. live-import -approve from a second directory    -> markers on the cloud
//  3. a stateful plan back in the first directory     -> the defect
//  4. a stateful apply there                          -> the damage
//
// Step 3 used to print "0 to add, 2 to change, 0 to destroy" and step 4 used
// to strip both markers off both resources and exit 0. This test asserts
// that step 3 still shows that diff - the diff is honest and hiding it would
// hide real drift - and that both steps now refuse, with the AWS CLI, never
// choudoufu's own report, confirming the markers survived.
//
// The last step proves the deliberate revert is still available and that
// taking it says so out loud.
func TestStatefulPlanAfterLiveImportAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "stateful marker strip")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	msPort = flocitest.StartFloci(t, "cdf-markerstrip")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+msPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", msRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)

	// --- 1. The stateful estate: no live block, a state file, stock ------
	//
	// This is the directory an operator runs day to day, and the one the
	// defect damages.
	stateful := t.TempDir()
	writeFile(t, stateful, "main.tf", msFixture(false))
	flocitest.Run(t, stateful, tofuBin, "init", "-input=false", "-no-color")

	apply1, code := run(t, stateful, tofuBin, "apply", "-input=false", "-auto-approve", "-no-color")
	if code != 0 || !strings.Contains(apply1, "Apply complete!") {
		t.Fatalf("the first apply did not complete (exit %d):\n%s", code, apply1)
	}
	statePath := filepath.Join(stateful, "terraform.tfstate")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("no state file after a stateful apply: %v", err)
	}

	// --- 2. The migration, from a second directory carrying a live block --
	migrated := t.TempDir()
	writeFile(t, migrated, "main.tf", msFixture(true))
	flocitest.Run(t, migrated, tofuBin, "init", "-input=false", "-no-color")

	importOut, code := run(t, migrated, tofuBin, "live-import",
		"-state="+statePath, "-estate="+msEstate, "-approve", "-no-color")
	if code != 0 {
		t.Fatalf("live-import -approve (exit %d):\n%s", code, importOut)
	}
	if got := msVPCTags(t)["tofu-estate"]; got != msEstate {
		t.Fatalf("the VPC does not carry tofu-estate=%q after live-import -approve; tags are %v", msEstate, msVPCTags(t))
	}
	if msVPCTags(t)["tofu-address"] == "" {
		t.Fatalf("the VPC carries no tofu-address after live-import -approve; tags are %v", msVPCTags(t))
	}

	// --- 3. The defect: a stateful plan in the untouched directory -------
	plan, code := run(t, stateful, tofuBin, "plan", "-input=false", "-no-color")

	// The reproduction. The plan really does propose removing the markers,
	// and still says so: the refusal explains the diff, it does not hide it.
	for _, want := range []string{"tofu-estate", "tofu-address", "will be updated in-place"} {
		if !strings.Contains(plan, want) {
			t.Errorf("the stateful plan does not mention %q - the reproduction has changed shape:\n%s", want, plan)
		}
	}
	// And the refusal.
	if code == 0 {
		t.Errorf("the stateful plan exited 0; want a refusal:\n%s", plan)
	}
	if !strings.Contains(plan, "Plan would remove this estate's ownership markers") {
		t.Errorf("the stateful plan was not refused:\n%s", plan)
	}
	if !strings.Contains(plan, "CHOUDOUFU_UNMIGRATE="+msEstate) {
		t.Errorf("the refusal does not name the deliberate-revert route:\n%s", plan)
	}

	// --- 4. The damage, refused ------------------------------------------
	//
	// -auto-approve is the form with nobody watching, and the one a warning
	// could not have stopped.
	apply2, code := run(t, stateful, tofuBin, "apply", "-input=false", "-auto-approve", "-no-color")
	if code == 0 {
		t.Errorf("the stateful apply exited 0; want a refusal:\n%s", apply2)
	}
	if strings.Contains(apply2, "Apply complete!") {
		t.Errorf("the stateful apply completed; the estate was un-migrated:\n%s", apply2)
	}
	// The cloud, not the report.
	tags := msVPCTags(t)
	if tags["tofu-estate"] != msEstate || tags["tofu-address"] == "" {
		t.Fatalf("the markers were stripped from the live VPC despite the refusal; tags are %v", tags)
	}

	// --- 5. The deliberate revert, which is still available --------------
	env := append(os.Environ(), "CHOUDOUFU_UNMIGRATE="+msEstate)
	apply3, code := runEnv(t, stateful, env, tofuBin, "apply", "-input=false", "-auto-approve", "-no-color")
	if code != 0 {
		t.Fatalf("the approved revert did not run (exit %d):\n%s", code, apply3)
	}
	if !strings.Contains(apply3, "Removing this estate's ownership markers") {
		t.Errorf("the approved revert was silent about what it was doing:\n%s", apply3)
	}
	if got := msVPCTags(t)["tofu-estate"]; got != "" {
		t.Errorf("the approved revert left tofu-estate=%q on the live VPC; want it removed", got)
	}
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

const (
	msEstate  = "markerstrip"
	msRegion  = "us-east-1"
	msVPCCIDR = "10.71.0.0/16"
	msLogName = "/markerstrip/app"
)

// msFixture is the same configuration twice, differing only in the live
// block - which is the whole of what switches modes, and the reason a
// directory can be migrated without any file in it changing.
func msFixture(live bool) string {
	var b strings.Builder
	b.WriteString("terraform {\n  required_version = \">= 1.5.0\"\n")
	if live {
		fmt.Fprintf(&b, "\n  live {\n    estate = %q\n  }\n", msEstate)
	}
	fmt.Fprintf(&b, `
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  s3_use_path_style = true
}

resource "aws_vpc" "main" {
  cidr_block = %q

  tags = {
    Name = "markerstrip"
  }
}

resource "aws_cloudwatch_log_group" "app" {
  name              = %q
  retention_in_days = 1
}
`, msVPCCIDR, msLogName)
	return b.String()
}

// ---------------------------------------------------------------------------
// Running commands, and reading the cloud
// ---------------------------------------------------------------------------

// msPort is this run's emulator port, assigned when the container starts.
var msPort string

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// run runs a command and returns its combined output and exit code without
// failing the test. Half the assertions here are about a NON-zero exit, so a
// helper that fatals on one would be measuring the wrong thing.
func run(t *testing.T, dir, name string, args ...string) (string, int) {
	t.Helper()
	return runEnv(t, dir, nil, name, args...)
}

func runEnv(t *testing.T, dir string, env []string, name string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(name, args...) //nolint:gosec // fixed binary, test-only
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	return "", -1
}

// msVPCTags reads the live VPC's tags through the AWS CLI. This test's
// verdicts rest on this rather than on anything choudoufu prints, because
// "the markers survived" is a claim about the cloud.
func msVPCTags(t *testing.T) map[string]string {
	t.Helper()
	out := flocitest.AWSCLI(t, msPort, "ec2", "describe-vpcs",
		"--filters", "Name=cidr,Values="+msVPCCIDR,
		"--query", "Vpcs[0].Tags", "--output", "json")
	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "null" {
		return nil
	}
	var pairs []struct{ Key, Value string }
	if err := json.Unmarshal([]byte(out), &pairs); err != nil {
		t.Fatalf("decoding the VPC's tags from %q: %v", out, err)
	}
	tags := make(map[string]string, len(pairs))
	for _, p := range pairs {
		tags[p.Key] = p.Value
	}
	return tags
}
