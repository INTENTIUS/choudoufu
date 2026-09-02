// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestCrossEstateDataSourceAgainstFloci is issue #62's live proof: the
// endorsed answer to "split estates cannot reference each other's outputs"
// is a plain data source reading the producer's own live resource, not a
// new SSM-parameter output surface built on the receipts machinery. See
// live/OUTPUTS.md for the decision and its reasoning, and
// internal/live/lint/lint.go's checkDataResources / live/LIMITATIONS.md's
// "remote-state" entry for the refusal whose Detail names this pattern.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestCrossEstateDataSourceAgainstFloci -v
//
// Two independent estates, two independent "choudoufu apply" runs, no state
// file at any point in either directory: the producer estate creates a VPC,
// and the consumer estate's aws_vpc data source - filtered on the
// producer's own tofu-estate/tofu-address marker tags
// (live/MARKERS.md), never through terraform_remote_state - resolves to
// that VPC's real ID, and a subnet declared from it lands inside the
// producer's real VPC. The subnet's live VpcId, read back independently
// with the AWS CLI rather than asked of choudoufu, is the value-flow proof:
// the consumer received the producer's actual value, not a stale or
// coincidental one.
func TestCrossEstateDataSourceAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "cross-estate-data-source")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	xePort = flocitest.StartFloci(t, "cdf-issue62")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+xePort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)

	// --- The producer estate: one VPC, nothing else -----------------------

	producerDir := t.TempDir()
	writeFixture(t, producerDir, xeProducerFixture)
	flocitest.Run(t, producerDir, tofuBin, "init", "-input=false", "-no-color")

	produced := xeTofu(t, tofuBin, producerDir, "apply", "-auto-approve", "-no-color", "-input=false")
	if !strings.Contains(produced, "Apply complete!") {
		t.Fatalf("the producer apply did not complete:\n%s", produced)
	}
	assertNoState(t, producerDir, "after the producer apply")

	vpcID := xeAWS(t, "ec2", "describe-vpcs",
		"--filters", "Name=cidr,Values="+xeProducerCIDR,
		"--query", "Vpcs[0].VpcId", "--output", "text")
	if vpcID == "" || vpcID == "None" {
		t.Fatal("the producer's VPC was not created")
	}
	t.Logf("producer VPC: %s", vpcID)

	gotEstate := xeAWS(t, "ec2", "describe-tags",
		"--filters", "Name=resource-id,Values="+vpcID, "Name=key,Values=tofu-estate",
		"--query", "Tags[0].Value", "--output", "text")
	if gotEstate != xeProducerEstate {
		t.Errorf("the producer VPC carries tofu-estate=%q, want %q", gotEstate, xeProducerEstate)
	}

	// --- The consumer estate: a plain data source, no remote state --------
	//
	// The filter is the producer's own marker tags, not a naming convention
	// invented for this purpose (live/OUTPUTS.md, "The pattern"): both tags
	// are already written on every managed resource, and the pair is unique
	// within the account by construction.

	consumerDir := t.TempDir()
	writeFixture(t, consumerDir, xeConsumerFixture)
	flocitest.Run(t, consumerDir, tofuBin, "init", "-input=false", "-no-color")

	consumed := xeTofu(t, tofuBin, consumerDir, "apply", "-auto-approve", "-no-color", "-input=false")
	if !strings.Contains(consumed, "Apply complete!") {
		t.Fatalf("the consumer apply did not complete:\n%s", consumed)
	}
	// Only the subnet is a managed resource; the data source resolves at
	// plan time and adds nothing to the apply count.
	if added, changed, destroyed, ok := applySummary(consumed); !ok || added != 1 || changed != 0 || destroyed != 0 {
		t.Errorf("want 1 added / 0 changed / 0 destroyed, got %d/%d/%d (ok=%v):\n%s",
			added, changed, destroyed, ok, consumed)
	}
	assertNoState(t, consumerDir, "after the consumer apply")

	// --- The value-flow proof: the subnet really lives in the producer's
	// VPC, confirmed independently of choudoufu with the AWS CLI ----------

	subnetID := xeAWS(t, "ec2", "describe-subnets",
		"--filters", "Name=cidr-block,Values="+xeConsumerCIDR,
		"--query", "Subnets[0].SubnetId", "--output", "text")
	if subnetID == "" || subnetID == "None" {
		t.Fatal("the consumer's subnet was not created")
	}
	subnetVPCID := xeAWS(t, "ec2", "describe-subnets",
		"--subnet-ids", subnetID,
		"--query", "Subnets[0].VpcId", "--output", "text")
	if subnetVPCID != vpcID {
		t.Errorf("the consumer's subnet lives in VPC %q, want the producer's VPC %q: "+
			"the data source did not carry the real value across", subnetVPCID, vpcID)
	}

	gotConsumerEstate := xeAWS(t, "ec2", "describe-tags",
		"--filters", "Name=resource-id,Values="+subnetID, "Name=key,Values=tofu-estate",
		"--query", "Tags[0].Value", "--output", "text")
	if gotConsumerEstate != xeConsumerEstate {
		t.Errorf("the consumer subnet carries tofu-estate=%q, want %q", gotConsumerEstate, xeConsumerEstate)
	}

	// --- Idempotency: the consumer's plan is clean on the next run --------

	clean := xeTofu(t, tofuBin, consumerDir, "plan", "-no-color", "-input=false")
	if !strings.Contains(clean, "No changes.") {
		t.Errorf("the consumer's plan is not clean once the value has flowed across:\n%s", clean)
	}
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// xePort is this run's emulator port, chosen by the kernel when this test
// starts its container. xeAWS reads it after the test assigns it; this test
// needs its own port so it can run alongside the package's other floci
// tests (see ra6Port's comment for the same reasoning).
var xePort string

const (
	xeProducerEstate = "issue62-producer"
	xeConsumerEstate = "issue62-consumer"
	xeProducerCIDR   = "10.62.0.0/16"
	xeConsumerCIDR   = "10.62.1.0/24"
	xeVPCAddr        = "aws_vpc.main"
)

// xeProducerFixture declares nothing but the resource being shared. No
// output block: the whole point of live/OUTPUTS.md's decision is that the
// producer needs none, since the consumer reads the live resource directly.
const xeProducerFixture = `
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "` + xeProducerEstate + `"
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  s3_use_path_style = true
}

resource "aws_vpc" "main" {
  cidr_block = "` + xeProducerCIDR + `"
}
`

// xeConsumerFixture is the endorsed pattern from live/OUTPUTS.md: a data
// source of the producer's own resource type, filtered on the producer's
// tofu-estate/tofu-address marker tags rather than a bespoke naming
// convention or terraform_remote_state (banned,
// internal/live/lint/lint.go's checkDataResources).
const xeConsumerFixture = `
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "` + xeConsumerEstate + `"
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  s3_use_path_style = true
}

data "aws_vpc" "producer" {
  filter {
    name   = "tag:tofu-estate"
    values = ["` + xeProducerEstate + `"]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["` + xeVPCAddr + `"]
  }
}

resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.producer.id
  cidr_block = "` + xeConsumerCIDR + `"
}
`

// ---------------------------------------------------------------------------
// Running things, on this test's own port
// ---------------------------------------------------------------------------

// xeTofu runs the built binary against this test's own working directory and
// returns its combined output, failing the test on a nonzero exit. Mirrors
// ra6Tofu; a separate copy because each floci-gated test in this package
// carries its own port and its own failure message rather than sharing
// package-level state with the others.
func xeTofu(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()

	start := time.Now()
	cmd := exec.Command(bin, args...) //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("choudoufu %s took %s\n%s", strings.Join(args, " "), time.Since(start), output)
	if err != nil {
		t.Fatalf("choudoufu %s failed: %v", strings.Join(args, " "), err)
	}
	return output
}

// xeAWS runs one AWS CLI call against this test's own emulator and returns
// its trimmed output. Mirrors ra6AWS: the package-level awsText/awsJSON/
// awsRun helpers hardcode the shared flociPort (TestStatelessLifecycleAgainstFloci's
// own suite), and this test needs its own port so it can run alongside them.
func xeAWS(t *testing.T, args ...string) string {
	t.Helper()

	full := append([]string{"--endpoint-url", "http://localhost:" + xePort}, args...)
	out, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	if err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}
