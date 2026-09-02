// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestUntagApplyReleasesOrphanTagAgainstFloci is GitHub issue #67's
// remaining piece from the policy matrix batch: apply-time tag release for
// undeclared_tagged = "untag" - a live resource this estate owns but whose
// configuration block was removed. TestPolicyMatrixAgainstFloci (this
// package's policy_live_test.go) exercises "untag" on the declared_tagged
// side, where the ordinary plan graph performs the release because the
// resource still has a config block to hang an update off of; this test
// exercises the side that batch's own commit message deferred:
//
//	Deferred: automatic tag release for undeclared_tagged = "untag" (a
//	resource with no declared address has no graph target for an ordinary
//	update diff; the plan renders the would-be release with identity
//	evidence and an explanatory note, apply does not perform it yet).
//
// internal/live/untag is that release, run once from
// internal/backend/local's StatelessRun.AfterApply after a real apply -
// never a plan - has finished changing the live system. This test drives it
// end to end:
//
//  1. An ordinary first apply, no policy block, creates a VPC (which stays
//     declared throughout, so there is always something for marker
//     discovery to anchor the sweep's provider on) and a log group. Both
//     carry this estate's ordinary markers afterward.
//  2. The log group's block is removed and a policy block sets
//     undeclared_tagged = "untag". The plan renders the release as its own
//     entry - the resource is NOT destroyed, its tofu-estate tag is named as
//     what will be released - rather than proposing the log group's
//     destruction the way today's fixed sweep would.
//  3. The apply performs the release for real: the log group survives, and
//     the AWS CLI - never choudoufu's own report - confirms its tofu-estate
//     tag is gone.
//  4. A third plan no longer has anything to say about the log group at
//     all: it fell out of this estate's sweep the moment its tofu-estate
//     tag came off, which is the "foreign/unowned" outcome the matrix
//     promises for a resource with no estate marker.
func TestUntagApplyReleasesOrphanTagAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "untag apply")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	utPort = flocitest.StartFloci(t, "cdf-untagapply")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+utPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()

	// --- 1. An ordinary first apply: the log group becomes declared_tagged ---

	writeFixture(t, dir, utFixture(true, false))
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	apply1 := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(apply1, "Apply complete!") {
		t.Fatalf("the first apply did not complete:\n%s", apply1)
	}
	added, changed, destroyed, ok := applySummary(apply1)
	if !ok || added != 2 || changed != 0 || destroyed != 0 {
		t.Fatalf("first apply: want 2/0/0, got %d/%d/%d (ok=%v):\n%s", added, changed, destroyed, ok, apply1)
	}

	vpcID := utAWSText(t, "ec2", "describe-vpcs",
		"--filters", "Name=cidr,Values="+utVPCCIDR, "--query", "Vpcs[0].VpcId")
	if vpcID == "" || vpcID == "None" {
		t.Fatal("the VPC was not created")
	}

	logTags1 := utLogGroupTags(t)
	if logTags1["tofu-estate"] != utEstate {
		t.Fatalf("the log group does not carry this estate's marker after the first apply: %v", logTags1)
	}
	if logTags1["tofu-address"] == "" {
		t.Fatalf("the log group carries no tofu-address after the first apply: %v", logTags1)
	}

	// --- 2. The second plan: the log group's block is gone, policy says ---
	// --- "untag" for undeclared_tagged ------------------------------------

	writeFixture(t, dir, utFixture(false, true))

	plan := tofu(t, tofuBin, dir, "plan")

	if !strings.Contains(plan, "NOT destroyed") {
		t.Errorf("the plan does not say the log group is NOT destroyed:\n%s", plan)
	}
	if !strings.Contains(plan, "aws_cloudwatch_log_group") || !strings.Contains(plan, "tofu-estate") {
		t.Errorf("the plan does not name the log group and the tag it releases:\n%s", plan)
	}
	if !strings.Contains(plan, "undeclared_tagged=untag") {
		t.Errorf("the plan does not attribute the withheld log group to undeclared_tagged=untag:\n%s", plan)
	}
	if !strings.Contains(plan, "unmanaged") {
		t.Errorf("the plan does not state the management consequence of releasing the estate marker:\n%s", plan)
	}
	// The withheld log group is not part of the plan graph at all - it was
	// filtered out of the resolutions applyOrphanPolicy narrowed, never
	// proposed as a destroy for the ordinary plan engine to count - so with
	// nothing else to do, the plan graph itself is empty and prints "No
	// changes" rather than a "Plan: N to add..." summary line. That the
	// policy section above still explains the untag in detail, on an
	// otherwise clean plan, is exactly the "NOT destroyed" claim already
	// checked above.
	if add, _, destroy, ok := flocitest.PlanSummary(plan); ok && destroy != 0 {
		t.Errorf("want 0 destroyed on the second plan (untag never destroys), got add=%d destroy=%d:\n%s", add, destroy, plan)
	} else if !ok && !strings.Contains(plan, "No changes. Your infrastructure matches the configuration.") {
		t.Errorf("want a clean second plan graph (nothing for untag to destroy):\n%s", plan)
	}

	// --- 3. The second apply: the release actually happens -----------------

	apply2 := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(apply2, "Apply complete!") {
		t.Fatalf("the second apply did not complete:\n%s", apply2)
	}
	if _, _, destroyed2, ok := applySummary(apply2); !ok || destroyed2 != 0 {
		t.Fatalf("the second apply destroyed something; untag must never destroy:\n%s", apply2)
	}
	// Plain "RELEASED" and "FAILED" substrings would also match the
	// section's own explanatory prose ("RELEASED means the tag is
	// confirmed gone...", "FAILED means the resource was left exactly as
	// it was found..."), so the per-resource status line itself - two-space
	// indent, then the word, right before the type name - is what is
	// actually checked for.
	if !strings.Contains(apply2, "  RELEASED aws_cloudwatch_log_group") {
		t.Errorf("the second apply's output does not report the log group's tag as released:\n%s", apply2)
	}
	if !strings.Contains(apply2, "tofu-estate") {
		t.Errorf("the second apply's output does not name the released key:\n%s", apply2)
	}
	if strings.Contains(apply2, "  FAILED aws_cloudwatch_log_group") {
		t.Errorf("the second apply reports a failed release:\n%s", apply2)
	}

	// --- 4. Read the cloud, not the tool's own report ----------------------

	logTags2 := utLogGroupTags(t)
	if _, present := logTags2["tofu-estate"]; present {
		t.Errorf("the log group still carries tofu-estate=%q after untag; want it released", logTags2["tofu-estate"])
	}
	if !utLogGroupExists(t) {
		t.Error("the log group was destroyed; untag must never delete, want it merely unmanaged")
	}

	stillThereVPC := utAWSText(t, "ec2", "describe-vpcs", "--vpc-ids", vpcID, "--query", "Vpcs[0].VpcId")
	if stillThereVPC != vpcID {
		t.Errorf("the VPC (declared_tagged, untouched by this test's policy) was destroyed; want it unaffected")
	}

	// --- 5. A third plan has nothing left to say about the log group -------
	//
	// Its tofu-estate tag is gone, so this estate's sweep no longer finds it
	// at all - it is foreign/unowned per the matrix, not a resource this
	// estate withholds from a sweep or proposes any action for.
	third := tofu(t, tofuBin, dir, "plan")
	if strings.Contains(third, "aws_cloudwatch_log_group") {
		t.Errorf("the third plan still mentions the log group; it should have fallen out of this estate's ownership entirely once its tag was released:\n%s", third)
	}
	// A plan with nothing left to propose prints "No changes." rather than
	// the "Plan: N to add..." summary line flocitest.PlanSummary parses, so
	// a clean plan is asserted directly rather than through that helper -
	// only the VPC is left, and it is unchanged.
	if !strings.Contains(third, "No changes. Your infrastructure matches the configuration.") {
		t.Errorf("want a clean third plan now that the log group is foreign and nothing else changed:\n%s", third)
	}

	assertNoState(t, dir, "after the untag-apply scenario")
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

const (
	utEstate  = "untag-apply"
	utVPCCIDR = "10.69.0.0/16"
	utLogName = "/untag-apply/app"
)

// utFixture is the estate this test drives through the undeclared_tagged =
// "untag" quadrant. withLogGroup controls whether the log group block is
// declared (step 1: yes, so it becomes declared_tagged and gets this
// estate's marker; step 2 and after: no, so its live resource becomes
// undeclared_tagged). withPolicy turns on the policy block naming
// undeclared_tagged = "untag" - false for the first apply, matching
// TestPolicyMatrixAgainstFloci's own "ordinary first apply, no policy
// block" step.
func utFixture(withLogGroup, withPolicy bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `terraform {
  required_version = ">= 1.5.0"

  live {
    estate = %q
`, utEstate)

	if withPolicy {
		b.WriteString(`
    policy {
      undeclared_tagged = "untag"
    }
`)
	}

	fmt.Fprintf(&b, `  }

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
}
`, utVPCCIDR)

	if withLogGroup {
		fmt.Fprintf(&b, `
resource "aws_cloudwatch_log_group" "app" {
  name              = %q
  retention_in_days = 1
}
`, utLogName)
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// Reading the cloud
// ---------------------------------------------------------------------------

func utLogGroupTags(t *testing.T) map[string]string {
	t.Helper()
	out, err := utAWSOutput("logs", "list-tags-log-group", "--log-group-name", utLogName,
		"--query", "tags", "--output", "json")
	if err != nil {
		// The log group is gone, which the caller reports as a change in
		// behaviour rather than as an error here.
		return nil
	}
	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "null" {
		return nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(out), &tags); err != nil {
		t.Fatalf("decoding the log group's tags from %q: %v", out, err)
	}
	return tags
}

func utLogGroupExists(t *testing.T) bool {
	t.Helper()
	out := utAWSText(t, "logs", "describe-log-groups",
		"--log-group-name-prefix", utLogName, "--query", "logGroups[0].logGroupName")
	return out == utLogName
}

// ---------------------------------------------------------------------------
// The AWS CLI, on this test's own port
// ---------------------------------------------------------------------------

// utPort is this run's emulator port, chosen by the kernel when this test's
// container starts. The utAWS* helpers read it after the test assigns it.
var utPort string

func utAWSOutput(args ...string) (string, error) {
	full := append([]string{"--endpoint-url", "http://localhost:" + utPort}, args...)
	out, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed CLI name, test-controlled args
	return strings.TrimSpace(string(out)), err
}

func utAWSText(t *testing.T, args ...string) string {
	t.Helper()
	return utAWSQuery(t, append(args, "--output", "text")...)
}

func utAWSQuery(t *testing.T, args ...string) string {
	t.Helper()
	out, err := utAWSOutput(args...)
	if err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
	return out
}
