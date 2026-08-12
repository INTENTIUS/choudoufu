// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// The RA.1 findings, against a real provider and an emulated cloud, through
// the built binary:
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/stamp/ -run TestOwnershipAgainstFloci -v
//
// Two claims, one container:
//
//   - C1: a live resource sitting at a name this configuration declares and
//     carrying no ownership marker is not this estate's. It is not read into
//     prior state, nothing is planned against it, and the run says so.
//   - C2: a resource whose tags come out of a merge() is stamped anyway, so a
//     marker-discovered type applies with its marker on it and the next plan
//     finds it instead of proposing a second one.
//
// Both are live tests rather than unit tests because both are claims about
// what reaches the cloud: C1's is that nothing does, and C2's is that the
// marker does.
// ra1Port is this run's emulator port, chosen by the kernel when RA.1's test
// starts its container. ra1AWS reads it after that test assigns it.
var ra1Port string

const (
	// ra1Estate and ra1MergeEstate are separate so the two phases cannot bind
	// each other's resources even if the emulator kept them.
	ra1Estate      = "ra1-ownership"
	ra1MergeEstate = "ra1-merge"

	// ra1LogGroup is the log group created out of band, with no markers on
	// it: somebody else's resource, at a name this fork's configuration then
	// declares.
	ra1LogGroup = "/ra1-ownership/app"
)

func TestOwnershipAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "ownership")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	ra1Port = flocitest.StartFloci(t, "cdf-ra1")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+ra1Port)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)

	t.Run("unowned live resource is not adopted", func(t *testing.T) {
		// Somebody else's log group, created with the AWS CLI and tagged with
		// nothing. Its retention differs from the one the configuration below
		// declares, so a run that adopted it would have something to propose.
		ra1AWS(t, "logs", "create-log-group", "--log-group-name", ra1LogGroup)
		ra1AWS(t, "logs", "put-retention-policy", "--log-group-name", ra1LogGroup, "--retention-in-days", "30")

		dir := t.TempDir()
		writeFile(t, dir, "main.tf", ra1UnownedFixture)
		flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

		out := ra1Tofu(t, tofuBin, dir, "plan", "-no-color", "-input=false")

		// The whole finding in three assertions: not adopted, not changed,
		// not destroyed.
		if strings.Contains(out, "will be updated in-place") {
			t.Errorf("an unowned live log group was adopted and planned against:\n%s", out)
		}
		if strings.Contains(out, "will be destroyed") || strings.Contains(out, "must be replaced") {
			t.Errorf("a plan proposed destroying a resource this estate does not own:\n%s", out)
		}
		if !strings.Contains(out, "aws_cloudwatch_log_group.app will be created") {
			t.Errorf("the declared log group is not planned as a create:\n%s", out)
		}
		// And said out loud, with the identity a human needs to go look at it.
		if !strings.Contains(out, "Live resource outside this estate") {
			t.Errorf("the run did not report the unowned resource:\n%s", out)
		}
		if !strings.Contains(out, "[UNOWNED]") || !strings.Contains(out, ra1LogGroup) {
			t.Errorf("the omissions section does not name the live resource:\n%s", out)
		}

		// The live resource is untouched: same retention, still unmarked.
		if got := ra1AWS(t, "logs", "describe-log-groups", "--log-group-name-prefix", ra1LogGroup,
			"--query", "logGroups[0].retentionInDays", "--output", "text"); got != "30" {
			t.Errorf("the unowned log group's retention is now %q, want 30 - something planned against it", got)
		}
		if got := ra1AWS(t, "logs", "list-tags-log-group", "--log-group-name", ra1LogGroup,
			"--query", "tags", "--output", "text"); strings.Contains(got, ra1Estate) {
			t.Errorf("the unowned log group was marked by a run that does not own it: %s", got)
		}

		// The second half of the finding: with the block gone, nothing
		// proposes destroying it either. This is the sweep's answer, and it
		// has to stay "not ours" rather than "ours and undeclared".
		writeFile(t, dir, "main.tf", ra1RemovedFixture)
		out = ra1Tofu(t, tofuBin, dir, "plan", "-no-color", "-input=false")
		if strings.Contains(out, "will be destroyed") {
			t.Errorf("removing the block proposed destroying a resource this estate never owned:\n%s", out)
		}
		if got := ra1AWS(t, "logs", "describe-log-groups", "--log-group-name-prefix", ra1LogGroup,
			"--query", "logGroups[0].logGroupName", "--output", "text"); got != ra1LogGroup {
			t.Fatalf("the unowned log group is gone: %q", got)
		}
	})

	t.Run("merge tags are stamped onto the live resource", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "main.tf", ra1MergeFixture)
		flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

		apply := ra1Tofu(t, tofuBin, dir, "apply", "-auto-approve", "-no-color", "-input=false")
		if !strings.Contains(apply, "Apply complete!") {
			t.Fatalf("the apply did not complete:\n%s", apply)
		}

		// The claim: the marker is on the live resource, read from the cloud
		// rather than from the tool that wrote it. Before the fix the subnet
		// applied with the author's two tags and neither marker.
		subnetID := ra1AWS(t, "ec2", "describe-subnets",
			"--filters", "Name=cidr-block,Values=10.63.1.0/24", "--query", "Subnets[0].SubnetId", "--output", "text")
		if subnetID == "" || subnetID == "None" {
			t.Fatalf("the subnet was not created:\n%s", apply)
		}
		tags := ra1SubnetTags(t, subnetID)
		for key, want := range map[string]string{
			TagEstate:  ra1MergeEstate,
			TagAddress: "aws_subnet.app",
			// The author's own half of the merge survives; the markers are
			// added to it, not instead of it.
			"team": "platform",
			"Name": "app",
		} {
			if tags[key] != want {
				t.Errorf("the live subnet's %s tag is %q, want %q (all tags: %v)", key, tags[key], want, tags)
			}
		}

		// And the payoff: the next plan finds it. The bug's signature was a
		// second create here, forever.
		plan := ra1Tofu(t, tofuBin, dir, "plan", "-no-color", "-input=false")
		if !strings.Contains(plan, "No changes.") {
			t.Errorf("the merge-tagged estate did not plan clean, which is the duplicate-creation loop:\n%s", plan)
		}
		if strings.Contains(plan, "will be created") {
			t.Errorf("a second copy of a merge-tagged resource was proposed:\n%s", plan)
		}
	})
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const ra1Provider = `
terraform {
  required_version = ">= 1.5.0"

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
`

// ra1UnownedFixture declares a log group at the name the CLI already created,
// with a retention this estate would want and the live one does not have. A
// run that adopted it would propose that change.
const ra1UnownedFixture = ra1Provider + `
terraform {
  live {
    estate = "` + ra1Estate + `"
  }
}

resource "aws_cloudwatch_log_group" "app" {
  name              = "` + ra1LogGroup + `"
  retention_in_days = 7
}
`

// ra1RemovedFixture is the same estate with the block deleted, which is what
// makes the estate-wide sweep look for resources it owns and no longer
// declares. It must not find somebody else's.
const ra1RemovedFixture = ra1Provider + `
terraform {
  live {
    estate = "` + ra1Estate + `"
  }
}

resource "aws_vpc" "keep" {
  cidr_block = "10.64.0.0/16"
}
`

// ra1MergeFixture is audit finding C2's shape: a marker-discovered type whose
// tags argument is a merge() the stamping pass cannot read entry by entry.
const ra1MergeFixture = ra1Provider + `
terraform {
  live {
    estate = "` + ra1MergeEstate + `"
  }
}

locals {
  common = {
    team = "platform"
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.63.0.0/16"

  tags = merge(local.common, { Name = "main" })
}

resource "aws_subnet" "app" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.63.1.0/24"

  tags = merge(local.common, { Name = "app" })
}
`

// ---------------------------------------------------------------------------
// Running things
// ---------------------------------------------------------------------------

// ra1Tofu runs the built binary and returns its output, failing the test on a
// nonzero exit. The plain plan and apply are used rather than live-plan
// because the fixtures carry a live block: this is the path an operator
// takes.
func ra1Tofu(t *testing.T, tofuBin, dir string, args ...string) string {
	t.Helper()

	start := time.Now()
	cmd := exec.Command(tofuBin, args...) //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("choudoufu %s took %s\n%s", strings.Join(args, " "), time.Since(start), output)
	if err != nil {
		t.Fatalf("choudoufu %s failed: %v", strings.Join(args, " "), err)
	}
	return output
}

// ra1AWS runs one AWS CLI call against this test's own emulator.
func ra1AWS(t *testing.T, args ...string) string {
	t.Helper()

	full := append([]string{"--endpoint-url", "http://localhost:" + ra1Port}, args...)
	out, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	if err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// ra1SubnetTags reads one subnet's tags off the cloud as a plain map.
func ra1SubnetTags(t *testing.T, subnetID string) map[string]string {
	t.Helper()

	raw := ra1AWS(t, "ec2", "describe-tags",
		"--filters", "Name=resource-id,Values="+subnetID,
		"--query", "Tags[].[Key,Value]", "--output", "text")
	tags := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		tags[key] = value
	}
	return tags
}
