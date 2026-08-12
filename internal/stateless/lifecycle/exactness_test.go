// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// TestStatelessExactnessAgainstFloci is P5.1's live half: the two claims the
// phase is named for, made against a real cloud rather than a fake one.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/lifecycle/ -run TestStatelessExactnessAgainstFloci -v
//
// Removal exactness: deleting a whole resource block destroys that block's
// live resource and nothing else. This is the gap P4.1 recorded and could not
// close, because a deleted block declares nothing and discovery only listed
// what something declared. The estate-wide sweep closes it.
//
// Drift exactness: an out-of-band change to one attribute of one resource
// surfaces as a diff on that resource and that attribute, with no neighbour
// noise, for every type in the estate rather than for the one type P4.1
// happened to check.
//
// And the rule that connects them: a renamed for_each key is a rename first.
// The sweep sees the live resource at the old key before the classifier gets
// to pair it with the new one, and if that order were wrong the plan would
// destroy and recreate a resource that needed a tag rewritten.
//
// It runs on its own floci container on its own port, so it is independent of
// the P4.1 lifecycle test and of the snapshot test beside it.
func TestStatelessExactnessAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "stateless exactness")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	flocitest.StartFloci(t, "tofu-stateless-p51", exactnessPort)

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+exactnessPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	t.Setenv("TF_PLUGIN_CACHE_DIR", t.TempDir())

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()
	writeFixture(t, dir, exFixture(exSubnetsAB, true))

	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	// --- 1. The estate goes up -------------------------------------------

	up := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	added, changed, destroyed, ok := applySummary(up)
	if !ok {
		t.Fatalf("no apply summary:\n%s", up)
	}
	t.Logf("estate up: %d added, %d changed, %d destroyed", added, changed, destroyed)
	// VPC, two subnets, the security group, the bucket, the log group, one EIP.
	if added != 7 || changed != 0 || destroyed != 0 {
		t.Errorf("want 7 added / 0 changed / 0 destroyed, got %d/%d/%d", added, changed, destroyed)
	}
	assertNoState(t, dir, "after the first apply")

	if clean := tofu(t, tofuBin, dir, "plan"); !strings.Contains(clean, "No changes.") {
		t.Fatalf("the estate did not recover cleanly, so nothing below means anything:\n%s", clean)
	}

	ids := exReadEstate(t)
	t.Logf("live estate: %+v", ids)

	// --- 2. The drift matrix ---------------------------------------------
	//
	// One out-of-band mutation per resource type, each asserted as exactly
	// one resource with exactly one attribute changed. Tag drift is the
	// mutation every one of these types accepts, and tags are also where a
	// shape or normalization mistake shows up as neighbour noise: a type
	// whose tags_all is mapped badly produces a diff on a resource nobody
	// touched. The log group gets a second, non-tag case, because a diff
	// that is only ever about tags would not prove much about the rest.

	for _, c := range []struct {
		name    string
		addr    string
		mutate  func(t *testing.T)
		attr    string
		nonTags bool

		// unserved names the attributes this type's diff carries that the
		// provider does not report on a read and the configuration does not
		// set, so a prior state built by importing the resource has them
		// null and the plan renders the provider's default arriving. It is
		// not a tolerance for neighbour noise: every entry here was checked
		// against a stock "terraform import" of the same resource against
		// the same emulator, which produces the identical line, so it is a
		// property of import-derived prior state rather than of this fork.
		// A diff that changes anything not named here fails.
		unserved []string
	}{
		{
			name:   "vpc tag",
			addr:   "aws_vpc.main",
			mutate: func(t *testing.T) { exTagEC2(t, ids.vpc, "Owner", "someone-else") },
			attr:   "Owner",
		},
		{
			name:   "subnet tag",
			addr:   `aws_subnet.this["a"]`,
			mutate: func(t *testing.T) { exTagEC2(t, ids.subnets["a"], "Drifted", "yes") },
			attr:   "Drifted",
		},
		{
			name:   "security group tag",
			addr:   "aws_security_group.main",
			mutate: func(t *testing.T) { exTagEC2(t, ids.securityGroup, "Drifted", "yes") },
			attr:   "Drifted",
			// revoke_rules_on_delete is a tofu-side behaviour flag: EC2 does
			// not store it, so no read can return it, and the SDK fills its
			// default in whenever the resource changes at all. A stock
			// "terraform import" of a security group followed by the same tag
			// drift prints this exact line, so it is what an import-derived
			// prior state looks like rather than something this fork lost.
			// See stateless/LIMITATIONS.md.
			unserved: []string{"revoke_rules_on_delete"},
		},
		{
			name:   "eip tag",
			addr:   "aws_eip.pool[0]",
			mutate: func(t *testing.T) { exTagEC2(t, ids.eip, "Drifted", "yes") },
			attr:   "Drifted",
		},
		{
			name: "bucket tag",
			addr: "aws_s3_bucket.data",
			mutate: func(t *testing.T) {
				exAWSRun(t, "s3api", "put-bucket-tagging", "--bucket", exBucket,
					"--tagging", fmt.Sprintf(
						`{"TagSet":[{"Key":"tofu-estate","Value":"%s"},{"Key":"tofu-address","Value":"aws_s3_bucket.data"},{"Key":"Drifted","Value":"yes"}]}`,
						exEstate))
			},
			attr: "Drifted",
		},
		{
			name: "log group tag",
			addr: "aws_cloudwatch_log_group.app",
			mutate: func(t *testing.T) {
				exAWSRun(t, "logs", "tag-log-group", "--log-group-name", exLogGroup,
					"--tags", "Drifted=yes")
			},
			attr: "Drifted",
		},
		{
			name: "log group retention",
			addr: "aws_cloudwatch_log_group.app",
			mutate: func(t *testing.T) {
				exAWSRun(t, "logs", "put-retention-policy", "--log-group-name", exLogGroup,
					"--retention-in-days", "7")
			},
			attr:    "retention_in_days",
			nonTags: true,
		},
	} {
		t.Run("drift/"+c.name, func(t *testing.T) {
			c.mutate(t)

			drift := tofu(t, tofuBin, dir, "plan")
			add, change, destroy, ok := flocitest.PlanSummary(drift)
			if !ok {
				t.Fatalf("no plan summary after the %s drift:\n%s", c.name, drift)
			}
			if add != 0 || change != 1 || destroy != 0 {
				t.Errorf("want 0 to add / 1 to change / 0 to destroy, got %d/%d/%d:\n%s",
					add, change, destroy, drift)
			}
			if got := flocitest.ChangedResources(drift); len(got) != 1 || got[0] != c.addr {
				t.Fatalf("the drift plan touches %v, want only %s:\n%s", got, c.addr, drift)
			}

			block := flocitest.ResourceBlock(t, drift, c.addr)
			if !strings.Contains(block, c.attr) {
				t.Errorf("the diff does not mention %s:\n%s", c.attr, block)
			}
			expected := c.attr
			if !c.nonTags {
				// A tag mutation's own attribute lives inside the tags map,
				// which exChangedAttrs already excludes along with tags_all.
				expected = ""
			}
			if extra := exUnexpectedAttrs(block, expected, c.unserved); len(extra) > 0 {
				t.Errorf("the %s mutation also changed %v:\n%s", c.name, extra, block)
			}

			// Correcting it is part of the claim: exactness is not only that
			// the plan says the right thing, but that applying it puts the
			// estate back and the next plan is clean.
			back := tofu(t, tofuBin, dir, "apply", "-auto-approve")
			if _, ch, _, ok := applySummary(back); !ok || ch != 1 {
				t.Errorf("the corrective apply did not change exactly one resource:\n%s", back)
			}
			if after := tofu(t, tofuBin, dir, "plan"); !strings.Contains(after, "No changes.") {
				t.Errorf("the estate did not converge after correcting the %s drift:\n%s", c.name, after)
			}
			assertNoState(t, dir, "after correcting the "+c.name+" drift")
		})
	}

	// The live identities must be the ones we started with: a drift that was
	// corrected by replacing a resource would satisfy every check above.
	if after := exReadEstate(t); after.vpc != ids.vpc || after.subnets["a"] != ids.subnets["a"] ||
		after.securityGroup != ids.securityGroup || after.eip != ids.eip {
		t.Errorf("the drift matrix replaced a resource: before %+v, after %+v", ids, after)
	}

	// --- 3. A renamed for_each key is a rename, not a destroy ------------
	//
	// The key "b" becomes "c" in configuration. The live subnet still carries
	// the marker for "b", which the sweep sees before anything has paired it
	// with the unclaimed "c" - and if the sweep won that race the plan would
	// destroy a subnet and create another one in its place.

	writeFixture(t, dir, exFixture(exSubnetsAC, true))

	renamePlan := tofu(t, tofuBin, dir, "plan")
	add, change, destroy, ok := flocitest.PlanSummary(renamePlan)
	if !ok {
		t.Fatalf("no plan summary after the key rename:\n%s", renamePlan)
	}
	t.Logf("rename plan: %d to add, %d to change, %d to destroy", add, change, destroy)
	if destroy != 0 {
		t.Errorf("a renamed key was planned as a destroy, got %d to destroy:\n%s", destroy, renamePlan)
	}
	if add != 1 {
		t.Errorf("want exactly the new key created, got %d to add:\n%s", add, renamePlan)
	}
	if !strings.Contains(renamePlan, "Renamed keys?") {
		t.Errorf("the plan does not offer the rename:\n%s", renamePlan)
	}
	if strings.Contains(renamePlan, "Owned and undeclared") {
		t.Errorf("the renamed key was also reported as a removal:\n%s", renamePlan)
	}
	mv := exRenameCommand(t, renamePlan)
	t.Logf("offered rename: %s", mv)

	// Running the offered command is the whole rename, and the estate is
	// clean afterwards with the same live subnet.
	exRunTofuWords(t, tofuBin, dir, mv)
	renamed := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(renamed, "No changes.") {
		t.Errorf("the estate did not converge after the rename:\n%s", renamed)
	}
	if after := exReadEstate(t); after.subnets["c"] != ids.subnets["b"] {
		t.Errorf("the renamed subnet is not the original: %q was %q", after.subnets["c"], ids.subnets["b"])
	}
	assertNoState(t, dir, "after the rename")

	// --- 4. Removal exactness --------------------------------------------
	//
	// The security group's whole block is deleted. Its type is now declared
	// nowhere, so nothing in the configuration would ever cause it to be
	// listed: only the estate-wide sweep can see it.

	writeFixture(t, dir, exFixture(exSubnetsAC, false))

	removal := tofu(t, tofuBin, dir, "plan")
	add, change, destroy, ok = flocitest.PlanSummary(removal)
	if !ok {
		t.Fatalf("no plan summary after the block was deleted:\n%s", removal)
	}
	t.Logf("removal plan: %d to add, %d to change, %d to destroy", add, change, destroy)
	if add != 0 || change != 0 || destroy != 1 {
		t.Errorf("want 0 to add / 0 to change / 1 to destroy, got %d/%d/%d:\n%s", add, change, destroy, removal)
	}
	if got := flocitest.ChangedResources(removal); len(got) != 1 || got[0] != "aws_security_group.main" {
		t.Errorf("the removal plan touches %v, want only aws_security_group.main:\n%s", got, removal)
	}
	if !strings.Contains(removal, "Owned and undeclared") {
		t.Errorf("the plan does not say why destroying it is legitimate:\n%s", removal)
	}
	if !strings.Contains(removal, ids.securityGroup) {
		t.Errorf("the removal section does not name the live resource %s:\n%s", ids.securityGroup, removal)
	}

	gone := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if _, _, destroyed, ok := applySummary(gone); !ok || destroyed != 1 {
		t.Errorf("the removal apply did not destroy exactly one resource:\n%s", gone)
	}
	// Read from the cloud, not from tofu: the claim is that the resource is
	// gone, not that the tool believes it is.
	if exSecurityGroupExists(t, ids.securityGroup) {
		t.Errorf("the security group %s is still live after the removal apply", ids.securityGroup)
	}
	after := exReadEstate(t)
	if after.vpc != ids.vpc || after.eip != ids.eip {
		t.Errorf("the removal disturbed the rest of the estate: before %+v, after %+v", ids, after)
	}

	converged := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(converged, "No changes.") {
		t.Errorf("the estate did not converge after the removal:\n%s", converged)
	}
	assertNoState(t, dir, "at the end of the exactness run")
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

const (
	// exactnessPort is this test's own floci port. 4601 is the e2e harness's,
	// 4603-4608 belong to neighbouring suites, 4615 is P4.1's and 4618 the
	// snapshot test's; P5.1 takes 4620.
	exactnessPort = "4620"

	exEstate     = "p51-exactness"
	exVPCCIDR    = "10.71.0.0/16"
	exBucket     = "p51-exactness-data"
	exLogGroup   = "/p51-exactness/app"
	exSGName     = "p51-exactness-main"
	exSubnetsAB  = `{ a = "10.71.1.0/24", b = "10.71.2.0/24" }`
	exSubnetsAC  = `{ a = "10.71.1.0/24", c = "10.71.2.0/24" }`
	exSubnetACID = "10.71.2.0/24"
)

// exFixture is the estate. Nothing in it writes an ownership tag: every
// marker this test reads off the cloud got there by stamping.
//
// subnets is the for_each map, which is what the rename step edits, and
// withSecurityGroup controls whether the security group's block exists at
// all, which is what the removal step deletes.
func exFixture(subnets string, withSecurityGroup bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `terraform {
  required_version = ">= 1.5.0"

  live {
    estate = %q
  }

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

locals {
  subnets = %s
}

resource "aws_vpc" "main" {
  cidr_block = %q
}

resource "aws_subnet" "this" {
  for_each = local.subnets

  vpc_id     = aws_vpc.main.id
  cidr_block = each.value
}

resource "aws_s3_bucket" "data" {
  bucket = %q
}

resource "aws_cloudwatch_log_group" "app" {
  name              = %q
  retention_in_days = 1
}

resource "aws_eip" "pool" {
  count = 1

  domain = "vpc"
}
`, exEstate, subnets, exVPCCIDR, exBucket, exLogGroup)

	if withSecurityGroup {
		fmt.Fprintf(&b, `
resource "aws_security_group" "main" {
  name        = %q
  description = "exactness fixture"
  vpc_id      = aws_vpc.main.id
}
`, exSGName)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Reading the cloud
// ---------------------------------------------------------------------------

// exEstateIDs is the estate's live identities, read with the AWS CLI so that
// "the same resource survived" is a claim about the cloud rather than about
// what tofu remembers.
type exEstateIDs struct {
	vpc           string
	subnets       map[string]string // marker key -> subnet ID
	securityGroup string
	eip           string
}

func exReadEstate(t *testing.T) exEstateIDs {
	t.Helper()

	out := exEstateIDs{subnets: map[string]string{}}
	out.vpc = exAWSText(t, "ec2", "describe-vpcs",
		"--filters", "Name=cidr,Values="+exVPCCIDR, "--query", "Vpcs[0].VpcId")
	if out.vpc == "" || out.vpc == "None" {
		t.Fatal("the VPC was not created")
	}

	subnets := exAWSJSON(t, "ec2", "describe-subnets",
		"--filters", "Name=vpc-id,Values="+out.vpc,
		"--query", "Subnets[].{Id:SubnetId,Tags:Tags}")
	for id, tags := range exDecodeTagged(t, subnets) {
		marker := tags["tofu-address"]
		if key, ok := strings.CutPrefix(marker, "aws_subnet.this:"); ok {
			out.subnets[key] = id
		}
	}

	out.securityGroup = exAWSText(t, "ec2", "describe-security-groups",
		"--filters", "Name=group-name,Values="+exSGName, "--query", "SecurityGroups[0].GroupId")
	if out.securityGroup == "None" {
		out.securityGroup = ""
	}

	eips := exAWSJSON(t, "ec2", "describe-addresses",
		"--query", "Addresses[].{Id:AllocationId,Tags:Tags}")
	for id := range exDecodeTagged(t, eips) {
		out.eip = id
	}
	return out
}

// exDecodeTagged decodes the {Id, Tags} shape every describe query above asks
// for into a map of identity to tag set.
func exDecodeTagged(t *testing.T, out string) map[string]map[string]string {
	t.Helper()

	var raw []struct {
		Id   string `json:"Id"`
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("decoding %q: %v", out, err)
	}
	byID := make(map[string]map[string]string, len(raw))
	for _, r := range raw {
		tags := make(map[string]string, len(r.Tags))
		for _, tag := range r.Tags {
			tags[tag.Key] = tag.Value
		}
		byID[r.Id] = tags
	}
	return byID
}

func exSecurityGroupExists(t *testing.T, id string) bool {
	t.Helper()

	out, err := exAWSOutput("ec2", "describe-security-groups", "--group-ids", id,
		"--query", "SecurityGroups[0].GroupId", "--output", "text")
	if err != nil {
		// The API errors for a group that does not exist, which is the
		// answer this asks for.
		return false
	}
	return strings.TrimSpace(out) == id
}

func exTagEC2(t *testing.T, id, key, value string) {
	t.Helper()
	exAWSRun(t, "ec2", "create-tags", "--resources", id,
		"--tags", fmt.Sprintf("Key=%s,Value=%s", key, value))
	t.Logf("wrote %s=%s onto %s out of band", key, value, id)
}

// ---------------------------------------------------------------------------
// Reading the plan
// ---------------------------------------------------------------------------

var exChangeLine = regexp.MustCompile(`^\s*[+~-]\s+([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// exUnexpectedAttrs is the attributes a diff block changes that the mutation
// does not account for: everything except the mutated attribute, the two tag
// maps that carry any tag mutation, and the named unserved attributes.
func exUnexpectedAttrs(block, want string, unserved []string) []string {
	allowed := map[string]bool{"tags": true, "tags_all": true}
	if want != "" {
		allowed[want] = true
	}
	for _, a := range unserved {
		allowed[a] = true
	}

	var out []string
	for _, line := range strings.Split(block, "\n") {
		m := exChangeLine.FindStringSubmatch(line)
		if m == nil || allowed[m[1]] {
			continue
		}
		out = append(out, m[1])
	}
	return out
}

// exRenameLine matches the rename hint the plan prints. The command is
// "choudoufu live-mv" since RN.1 (it was "tofu live-mv" since PN.1, and
// before that this regex still said "stateless-mv" and so killed the test
// at the rename section, before it ever reached removal exactness - the two
// claims P5.1 is named for).
var exRenameLine = regexp.MustCompile(`rename with: (choudoufu live-mv .*)$`)

func exRenameCommand(t *testing.T, output string) string {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		if m := exRenameLine.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	t.Fatalf("no rename command in the plan output:\n%s", output)
	return ""
}

// exRunTofuWords runs a printed command line. The addresses in it are single
// quoted for a shell, which is what makes it safe to paste; here they are
// unquoted again rather than handed to a shell.
func exRunTofuWords(t *testing.T, bin, dir, command string) {
	t.Helper()

	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "choudoufu" {
		t.Fatalf("not a choudoufu command: %q", command)
	}
	args := make([]string, 0, len(fields)-1)
	for _, f := range fields[1:] {
		args = append(args, strings.Trim(f, "'"))
	}

	cmd := exec.Command(bin, args...) //nolint:gosec // this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	t.Logf("%s\n%s", command, out)
	if err != nil {
		t.Fatalf("%s failed: %v", command, err)
	}
}

// ---------------------------------------------------------------------------
// The AWS CLI, on this test's own port
// ---------------------------------------------------------------------------

func exAWSOutput(args ...string) (string, error) {
	full := append([]string{"--endpoint-url", "http://localhost:" + exactnessPort}, args...)
	out, err := exec.Command("aws", full...).Output()
	return strings.TrimSpace(string(out)), err
}

func exAWSText(t *testing.T, args ...string) string {
	t.Helper()
	return exAWSQuery(t, append(args, "--output", "text")...)
}

func exAWSJSON(t *testing.T, args ...string) string {
	t.Helper()
	return exAWSQuery(t, append(args, "--output", "json")...)
}

func exAWSQuery(t *testing.T, args ...string) string {
	t.Helper()

	out, err := exAWSOutput(args...)
	if err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
	return out
}

func exAWSRun(t *testing.T, args ...string) {
	t.Helper()

	if _, err := exAWSOutput(args...); err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
}

// ---------------------------------------------------------------------------
// floci, on this test's own port
// ---------------------------------------------------------------------------
