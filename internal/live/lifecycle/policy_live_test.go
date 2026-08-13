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

// TestPolicyMatrixAgainstFloci is GitHub issue #67's behavioral half, run
// against the maintainer's own motivating example - verbatim from the
// issue, minus the tag_key/tag_value question (both default to the estate
// marker here):
//
//	policy {
//	  declared_tagged     = "untag"     # remove the tag; source owns it now
//	  declared_untagged   = "converge"  # ordinary management
//	  undeclared_tagged   = "keep"      # the tag protects it
//	  undeclared_untagged = "delete"    # account-scope reconciliation
//	}
//
// One estate, one apply, all four quadrants exercised at once - exactly the
// motivating table's shape - and every claim checked by reading the cloud
// with the AWS CLI, never by trusting choudoufu's own report of what it did:
//
//   - declared_tagged: aws_vpc.main is declared before this apply and
//     already carries the estate marker (an ordinary first apply put it
//     there). "untag" releases the marker; the VPC survives, unmanaged.
//   - declared_untagged: an S3 bucket created out of band, with no tags at
//     all, is declared for the first time in this apply's configuration.
//     "converge" admits it (its identity is the bucket name, not a guess)
//     and marker stamping's ordinary tag write adopts it.
//   - undeclared_tagged: the log group block from the first apply is
//     removed from this apply's configuration. Under today's fixed
//     behavior that is a sweep-and-destroy; "keep" leaves it live.
//   - undeclared_untagged: a security group created out of band, with no
//     tags at all, inside this policy's scope (types = ["aws_security_group"]).
//     "delete" is the only quadrant that reaches for something this estate
//     never declared and never owned.
func TestPolicyMatrixAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "policy matrix")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	pmPort = flocitest.StartFloci(t, "cdf-issue67")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+pmPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()

	// --- 0. An ordinary first apply, no policy block at all --------------
	//
	// This is what makes aws_vpc.main declared_tagged going into step 1: an
	// estate with today's fixed behavior, exactly like
	// TestStatelessLifecycleAgainstFloci, so that "untag" has a marker to
	// release rather than one this test planted by hand.
	writeFixture(t, dir, pmFixture(true, "", nil))
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	apply1 := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(apply1, "Apply complete!") {
		t.Fatalf("the first apply did not complete:\n%s", apply1)
	}
	added, changed, destroyed, ok := applySummary(apply1)
	if !ok || added != 3 || changed != 0 || destroyed != 0 {
		t.Fatalf("first apply: want 3/0/0, got %d/%d/%d (ok=%v):\n%s", added, changed, destroyed, ok, apply1)
	}

	vpcID := pmAWSText(t, "ec2", "describe-vpcs",
		"--filters", "Name=cidr,Values="+pmVPCCIDR, "--query", "Vpcs[0].VpcId")
	if vpcID == "" || vpcID == "None" {
		t.Fatal("the VPC was not created")
	}
	pmAssertTags(t, pmEC2Tags(t, vpcID), "aws_vpc.main", map[string]string{
		"tofu-estate": pmEstate, "tofu-address": "aws_vpc.main",
	})

	// --- Out-of-band setup for the two undeclared-side candidates ---------

	// declared_untagged's candidate: a bucket that exists, with no tags, and
	// no declared block yet.
	pmAWSRun(t, "s3api", "create-bucket", "--bucket", pmBucket)
	if tags := pmBucketTags(t); len(tags) != 0 {
		t.Fatalf("the adoptable bucket already carries tags before it is declared: %v", tags)
	}

	// undeclared_untagged's candidate: a security group in the estate's own
	// VPC, with no tags and nothing in configuration that will ever declare
	// it. It is what "delete" reaches for.
	sgID := pmAWSText(t, "ec2", "create-security-group",
		"--group-name", "issue67-stray", "--description", "issue67 stray sg",
		"--vpc-id", vpcID, "--query", "GroupId")
	if sgID == "" || sgID == "None" {
		t.Fatal("the stray security group was not created")
	}
	t.Logf("stray security group %s created out of band, no tags", sgID)

	// --- 1. The second apply: all four quadrants at once ------------------
	//
	// The log group block is dropped (declared -> undeclared for it), the
	// bucket block is added (undeclared -> declared for it, still untagged
	// live), and the policy block turns today's fixed behavior upside down
	// for every quadrant.
	writeFixture(t, dir, pmFixture(false, pmBucket, []string{"aws_security_group"}))

	plan := tofu(t, tofuBin, dir, "plan")

	// declared_tagged = "untag" must say, in so many words, that this
	// resource leaves management - GitHub issue #67's own requirement for
	// the tag_key = estate-marker case, which is what this scenario uses.
	if !strings.Contains(plan, "leaves management") {
		t.Errorf("the plan does not say that untag leaves the VPC unmanaged:\n%s", plan)
	}
	if !strings.Contains(plan, "aws_vpc.main") || !strings.Contains(plan, "tofu-estate") {
		t.Errorf("the plan does not show the VPC's tofu-estate tag being released:\n%s", plan)
	}

	// undeclared_untagged = "delete" must render the security group
	// individually, with its identity, never as a bare count.
	if !strings.Contains(plan, "aws_security_group") || !strings.Contains(plan, sgID) {
		t.Errorf("the plan does not itemize the stray security group %s by identity:\n%s", sgID, plan)
	}

	apply2 := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(apply2, "Apply complete!") {
		t.Fatalf("the second apply did not complete:\n%s", apply2)
	}
	t.Logf("second apply:\n%s", apply2)

	// --- 2. Read the cloud, not the tool's own report ---------------------

	// declared_tagged = "untag": the VPC survives, and no longer carries
	// this estate's marker.
	vpcTags := pmEC2Tags(t, vpcID)
	if _, present := vpcTags["tofu-estate"]; present {
		t.Errorf("the VPC still carries tofu-estate=%q after untag; want it released", vpcTags["tofu-estate"])
	}
	stillThere := pmAWSText(t, "ec2", "describe-vpcs", "--vpc-ids", vpcID, "--query", "Vpcs[0].VpcId")
	if stillThere != vpcID {
		t.Errorf("the VPC was destroyed; untag must never delete, want it merely unmanaged")
	}

	// declared_untagged = "converge": the bucket is adopted - it now
	// carries this estate's markers, written by the ordinary tag update
	// the plan showed.
	pmAssertTags(t, pmBucketTags(t), "aws_s3_bucket.data", map[string]string{
		"tofu-estate": pmEstate, "tofu-address": "aws_s3_bucket.data",
	})

	// undeclared_tagged = "keep": the log group's block is gone from
	// configuration and the log group is still live, tags untouched.
	logTags := pmLogGroupTags(t)
	if logTags == nil {
		t.Error("the log group was destroyed; undeclared_tagged = \"keep\" must have kept it")
	} else if logTags["tofu-estate"] != pmEstate {
		t.Errorf("the log group's tofu-estate tag changed under \"keep\": got %q, want %q", logTags["tofu-estate"], pmEstate)
	}

	// undeclared_untagged = "delete": the stray security group, which this
	// estate never declared and never owned, is gone.
	sgGone := pmAWSText(t, "ec2", "describe-security-groups",
		"--filters", "Name=group-id,Values="+sgID, "--query", "SecurityGroups[].GroupId")
	if strings.Contains(sgGone, sgID) {
		t.Errorf("the stray security group %s still exists; undeclared_untagged = \"delete\" should have removed it", sgID)
	}

	// A third plan is deliberately NOT clean, and that is this exact policy
	// combination's own second-order consequence rather than a bug: with
	// declared_tagged still assigned "untag", nothing can stay tagged for
	// more than one apply. The VPC lost its marker in apply2, so marker
	// discovery cannot find it any more - it reads as declared_untagged
	// (needs discovery, no import identity) and the plan proposes creating
	// it again, exactly the "leaves management" consequence stated above in
	// so many words. The bucket adopted in apply2 is now declared_tagged
	// for the first time, and the same untag verb immediately releases its
	// marker too. Nothing here is destroyed and nothing about the security
	// group scope fires again, because there is nothing left in it to
	// reconcile.
	third := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(third, "aws_vpc.main") {
		t.Errorf("the third plan does not propose recreating the now-invisible VPC, which is untag's own documented consequence:\n%s", third)
	}
	if add, _, destroy, ok := flocitest.PlanSummary(third); !ok || add < 1 || destroy != 0 {
		t.Errorf("want at least 1 add and 0 destroy on the third plan (the VPC recreate, no more security groups to reconcile), got add=%d destroy=%d ok=%v:\n%s", add, destroy, ok, third)
	}

	assertNoState(t, dir, "after the policy matrix scenario")
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

const (
	pmEstate  = "issue67-matrix"
	pmVPCCIDR = "10.67.0.0/16"
	pmSubCIDR = "10.67.1.0/24"
	pmLogName = "/issue67-matrix/app"
	pmBucket  = "issue67-matrix-adoptable"
)

// pmFixture is the estate this test drives through the maintainer's
// four-quadrant example. withLogGroup controls whether the log group block
// is declared (step 0: yes, so undeclared_tagged has something to withhold
// in step 1; step 1: no, so its live resource becomes undeclared_tagged).
// bucketName empty omits the bucket block entirely (step 0: nothing has
// been created to declare yet); non-empty declares it (step 1: the
// adoption candidate). scopeTypes non-nil turns on the policy block with
// the maintainer's verbatim matrix and a scope naming those types; nil
// (step 0) means no policy block at all.
func pmFixture(withLogGroup bool, bucketName string, scopeTypes []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `terraform {
  required_version = ">= 1.5.0"

  live {
    estate = %q
`, pmEstate)

	if scopeTypes != nil {
		quoted := make([]string, len(scopeTypes))
		for i, s := range scopeTypes {
			quoted[i] = fmt.Sprintf("%q", s)
		}
		fmt.Fprintf(&b, `
    policy {
      declared_tagged     = "untag"
      declared_untagged   = "converge"
      undeclared_tagged   = "keep"
      undeclared_untagged = "delete"
      threshold           = 10

      scope {
        types = [%s]
      }
    }
`, strings.Join(quoted, ", "))
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

resource "aws_subnet" "app" {
  vpc_id     = aws_vpc.main.id
  cidr_block = %q
}
`, pmVPCCIDR, pmSubCIDR)

	if withLogGroup {
		fmt.Fprintf(&b, `
resource "aws_cloudwatch_log_group" "app" {
  name              = %q
  retention_in_days = 1
}
`, pmLogName)
	}

	if bucketName != "" {
		fmt.Fprintf(&b, `
resource "aws_s3_bucket" "data" {
  bucket = %q
}
`, bucketName)
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// Reading the cloud
// ---------------------------------------------------------------------------

func pmEC2Tags(t *testing.T, id string) map[string]string {
	t.Helper()
	out := pmAWSJSON(t, "ec2", "describe-tags",
		"--filters", "Name=resource-id,Values="+id,
		"--query", "Tags[].{Key:Key,Value:Value}")
	return decodeTagList(t, out)
}

func pmBucketTags(t *testing.T) map[string]string {
	t.Helper()
	out := pmAWSJSON(t, "s3api", "get-bucket-tagging", "--bucket", pmBucket,
		"--query", "TagSet[].{Key:Key,Value:Value}")
	return decodeTagList(t, out)
}

func pmLogGroupTags(t *testing.T) map[string]string {
	t.Helper()
	out, err := pmAWSOutput("logs", "list-tags-log-group", "--log-group-name", pmLogName,
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

func pmAssertTags(t *testing.T, got map[string]string, addr string, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s carries %s=%q live, want %q (all tags: %v)", addr, k, got[k], v, got)
		}
	}
}

// ---------------------------------------------------------------------------
// The AWS CLI, on this test's own port
// ---------------------------------------------------------------------------

// pmPort is this run's emulator port, chosen by the kernel when this test's
// container starts. The pmAWS* helpers read it after the test assigns it.
var pmPort string

func pmAWSOutput(args ...string) (string, error) {
	full := append([]string{"--endpoint-url", "http://localhost:" + pmPort}, args...)
	out, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed CLI name, test-controlled args
	return strings.TrimSpace(string(out)), err
}

func pmAWSText(t *testing.T, args ...string) string {
	t.Helper()
	return pmAWSQuery(t, append(args, "--output", "text")...)
}

func pmAWSJSON(t *testing.T, args ...string) string {
	t.Helper()
	return pmAWSQuery(t, append(args, "--output", "json")...)
}

func pmAWSQuery(t *testing.T, args ...string) string {
	t.Helper()
	out, err := pmAWSOutput(args...)
	if err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
	return out
}

func pmAWSRun(t *testing.T, args ...string) {
	t.Helper()
	if _, err := pmAWSOutput(args...); err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
}
