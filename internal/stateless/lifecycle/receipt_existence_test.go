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

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// TestReceiptExistenceFlavorAgainstFloci is RA.6's Go-level claim: the
// EXISTENCE flavor of the receipts pattern (stateless/RECEIPTS.md, "Two
// flavors; prefer the simpler") re-arms its effect the existence way. A
// receipt whose value is a constant carries no information beyond whether
// it exists at all, so breaking it out of band is a genuine delete, not a
// value overwrite, and the next plan proposing exactly one create - on
// exactly that address - is the whole claim.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/lifecycle/ -run TestReceiptExistenceFlavorAgainstFloci -v
//
// Runs through plain `choudoufu apply`/`plan` behind a `live {}` block (P4.1),
// the same path an operator uses, rather than `live-plan -estate=`: the
// claim under test is the receipt pattern working end to end through the
// ordinary command surface, not the flag surface stateless/e2e/run.sh
// already covers in more detail (its receipt-cycle-existence step, added by
// this same task, exercises the identical cycle against the full P0.1
// estate through live-plan).
func TestReceiptExistenceFlavorAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "existence-receipt")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	flocitest.StartFloci(t, "tofu-ra6-floci", ra6Port)

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+ra6Port)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("TF_PLUGIN_CACHE_DIR", t.TempDir())

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()
	writeFixture(t, dir, ra6Fixture)
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	// 1. Apply creates the receipt with its constant value - existence is
	// the entire bit, so the value itself carries nothing worth asserting
	// beyond "it is the declared constant".
	apply := ra6Tofu(t, tofuBin, dir, "apply", "-auto-approve", "-no-color", "-input=false")
	if !strings.Contains(apply, "Apply complete!") {
		t.Fatalf("the apply did not complete:\n%s", apply)
	}
	assertNoState(t, dir, "after apply")

	value := ra6AWS(t, "ssm", "get-parameter", "--name", ra6Param, "--query", "Parameter.Value", "--output", "text")
	if value != "done" {
		t.Fatalf("the receipt's live value reads %q, want the constant \"done\"", value)
	}

	// floci-gaps #10 (chant/test/floci-gaps.md): ssm:PutParameter drops the
	// inline tag set a parameter is created with, so the freshly applied
	// receipt reads back untagged and is correctly unowned until adopted -
	// the same gap stateless/e2e/run.sh's step 3d works around for the full
	// estate fixture. Adopted here with the AWS CLI, a tag write a human
	// would make deliberately, so the delete/re-arm claim below is about the
	// existence cycle itself and not entangled with this standing emulator
	// gap.
	tagsBefore := ra6AWS(t, "ssm", "list-tags-for-resource", "--resource-type", "Parameter",
		"--resource-id", ra6Param, "--query", "TagList[?Key==`tofu-estate`]|[0].Value", "--output", "text")
	if tagsBefore == ra6Estate {
		t.Log("the receipt already carries tofu-estate (floci-gaps #10 appears fixed); nothing to adopt")
	} else {
		ra6AWSRun(t, "ssm", "add-tags-to-resource", "--resource-type", "Parameter", "--resource-id", ra6Param,
			"--tags", "Key=tofu-estate,Value="+ra6Estate, "Key=tofu-address,Value="+ra6Addr)
		t.Logf("adopted %s (floci-gaps #10: PutParameter dropped the inline tag set)", ra6Param)
	}

	adopted := ra6Tofu(t, tofuBin, dir, "plan", "-no-color", "-input=false")
	if !strings.Contains(adopted, "No changes.") {
		t.Fatalf("the plan is not clean once the receipt is adopted:\n%s", adopted)
	}

	// 2. Break it THE EXISTENCE WAY: delete the parameter out of band. Not
	// an overwrite - there is no changed value to write for a receipt whose
	// entire content is whether it exists - so a genuine delete is what
	// plays "the effect's memory is gone".
	ra6AWSRun(t, "ssm", "delete-parameter", "--name", ra6Param)

	// 3. The plan re-arms the effect: exactly one create, on exactly this
	// address, and nothing else. That "and nothing else" is the load-bearing
	// half of the claim - a re-arm that also touched something unrelated
	// would be exactly as wrong as one that touched nothing at all.
	armed := ra6Tofu(t, tofuBin, dir, "plan", "-no-color", "-input=false")
	if !strings.Contains(armed, "# "+ra6Addr+" will be created") {
		t.Errorf("the deleted receipt did not re-arm a create on %s:\n%s", ra6Addr, armed)
	}
	if !strings.Contains(armed, "Plan: 1 to add, 0 to change, 0 to destroy.") {
		t.Errorf("the re-armed plan is not exactly one create and nothing else:\n%s", armed)
	}
	if strings.Contains(armed, "will be destroyed") || strings.Contains(armed, "must be replaced") {
		t.Errorf("the re-armed plan proposes more than the one expected create:\n%s", armed)
	}
	if !strings.Contains(armed, `+ "tofu-estate"  = "`+ra6Estate+`"`) ||
		!strings.Contains(armed, `+ "tofu-address" = "`+ra6Addr+`"`) {
		t.Errorf("the re-armed create does not carry both ownership markers:\n%s", armed)
	}

	// 4. Convergence: recreate it (the layer above playing the effect this
	// receipt stands for) and re-adopt, the same two-call pattern as the
	// initial adoption above - and the same shape floci-gaps #11 already
	// documents for the hash flavor's --overwrite case, except here there is
	// no overwrite call for that gap to touch at all, only the CREATE half's
	// own #10.
	ra6AWSRun(t, "ssm", "put-parameter", "--name", ra6Param, "--type", "String", "--value", "done")
	ra6AWSRun(t, "ssm", "add-tags-to-resource", "--resource-type", "Parameter", "--resource-id", ra6Param,
		"--tags", "Key=tofu-estate,Value="+ra6Estate, "Key=tofu-address,Value="+ra6Addr)

	converged := ra6Tofu(t, tofuBin, dir, "plan", "-no-color", "-input=false")
	if !strings.Contains(converged, "No changes.") {
		t.Errorf("the plan is not clean after recreating and re-adopting the receipt:\n%s", converged)
	}
	assertNoState(t, dir, "after the existence cycle")
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

const (
	// ra6Port is this test's own port, in the 4630+ band no suite claims;
	// 4631 belongs to internal/stateless/stamp/ownership_test.go.
	ra6Port = "4632"

	ra6Estate = "ra6-existence"
	ra6Effect = "ra6-demo"
	ra6Param  = "/tofu-receipts/" + ra6Estate + "/" + ra6Effect
	ra6Addr   = "aws_ssm_parameter.demo_existence"
)

// ra6Fixture is a minimal existence-flavor receipt, the same shape as
// stateless/e2e/estate/receipts.tf's aws_ssm_parameter.demo_existence but
// standalone: a `live {}` block rather than the estate's -estate flag, since
// this test drives plain `choudoufu plan`/`apply`.
const ra6Fixture = `
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "` + ra6Estate + `"
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  s3_use_path_style = true
}

resource "aws_ssm_parameter" "demo_existence" {
  name  = "` + ra6Param + `"
  type  = "String" # never SecureString -- the value carries no information (RECEIPTS.md)
  value = "done"    # constant by design: existence is the bit, not the value

  tags = {
    tofu-estate  = "` + ra6Estate + `"
    tofu-address = "` + ra6Addr + `"
  }
}
`

// ---------------------------------------------------------------------------
// Running things, on this test's own port
// ---------------------------------------------------------------------------

// ra6Tofu runs the built binary against this test's own working directory
// and returns its combined output, failing the test on a nonzero exit.
func ra6Tofu(t *testing.T, tofuBin, dir string, args ...string) string {
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

// ra6AWS runs one AWS CLI call against this test's own emulator and returns
// its trimmed output. The package-level awsText/awsJSON/awsRun helpers
// hardcode the shared flociPort (this package's own suite); this test needs
// its own port so it can run alongside the others.
func ra6AWS(t *testing.T, args ...string) string {
	t.Helper()

	full := append([]string{"--endpoint-url", "http://localhost:" + ra6Port}, args...)
	out, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	if err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// ra6AWSRun is ra6AWS for calls whose output nobody reads.
func ra6AWSRun(t *testing.T, args ...string) {
	t.Helper()
	ra6AWS(t, args...)
}
