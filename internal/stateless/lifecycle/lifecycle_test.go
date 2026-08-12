// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// TestStatelessLifecycleAgainstFloci is P4.1's live half, and the first time
// this fork applies anything to a cloud.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/lifecycle/ -run TestStatelessLifecycleAgainstFloci -v
//
// It starts from an empty account and drives an estate through its whole life
// with the two plain commands - no stateless-prefixed subcommand, no flag
// asking for stateless behaviour, nothing but a "live" block in the
// configuration:
//
//  1. tofu apply -auto-approve creates the estate. Nothing in the fixture
//     writes an ownership tag; the markers on the live resources afterwards
//     are the ones stamping put into the configuration this run planned from.
//  2. Nothing was recorded. The working directory is walked for anything
//     that looks like state, before and after every step.
//  3. tofu plan is clean, which is the whole claim: the second run recovered
//     the same estate from the live system alone.
//  4. A tag written out of band shows up as exactly one change, on exactly
//     one resource.
//  5. Shrinking a count destroys exactly the surplus member, and the survivor
//     keeps its identity.
//  6. Deleting a whole resource block destroys exactly that block's live
//     resource. This step asserted the opposite when P4.1 wrote it - the
//     removal gap, which had no fix inside phase 4 - and P5.1's estate-wide
//     sweep is what turned it round.
func TestStatelessLifecycleAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "stateless lifecycle")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	flociPort = flocitest.StartFloci(t, "cdf-p41")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+flociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()
	writeFixture(t, dir, fixture(2, true))

	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")
	assertNoState(t, dir, "after init")

	// --- 1. The first apply: an empty account becomes an estate ----------

	apply := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(apply, "Apply complete!") {
		t.Fatalf("the first apply did not complete:\n%s", apply)
	}
	added, changed, destroyed, ok := applySummary(apply)
	if !ok {
		t.Fatalf("no apply summary:\n%s", apply)
	}
	t.Logf("first apply: %d added, %d changed, %d destroyed", added, changed, destroyed)
	// The VPC, the subnet, the bucket, the log group and both EIPs.
	if added != 6 || changed != 0 || destroyed != 0 {
		t.Errorf("want 6 added / 0 changed / 0 destroyed, got %d/%d/%d", added, changed, destroyed)
	}
	assertNoState(t, dir, "after the first apply")

	// --- 2. The markers are on the live resources ------------------------
	//
	// Read from the cloud with the AWS CLI, never from tofu: the claim is
	// that the ownership record is on the resource, and asking the tool that
	// wrote it would be asking it to confirm its own story.

	vpcID := awsText(t, "ec2", "describe-vpcs",
		"--filters", "Name=cidr,Values="+vpcCIDR, "--query", "Vpcs[0].VpcId")
	if vpcID == "" || vpcID == "None" {
		t.Fatal("the VPC was not created")
	}
	assertTags(t, ec2Tags(t, vpcID), "aws_vpc.main", map[string]string{
		"tofu-estate": estate, "tofu-address": "aws_vpc.main",
	})

	subnetID := awsText(t, "ec2", "describe-subnets",
		"--filters", "Name=cidr-block,Values="+subnetCIDR, "--query", "Subnets[0].SubnetId")
	if subnetID == "" || subnetID == "None" {
		t.Fatal("the subnet was not created")
	}
	assertTags(t, ec2Tags(t, subnetID), "aws_subnet.app", map[string]string{
		"tofu-estate": estate, "tofu-address": "aws_subnet.app",
	})

	assertTags(t, bucketTags(t), "aws_s3_bucket.data", map[string]string{
		"tofu-estate": estate, "tofu-address": "aws_s3_bucket.data",
	})

	assertTags(t, logGroupTags(t), "aws_cloudwatch_log_group.app", map[string]string{
		"tofu-estate": estate, "tofu-address": "aws_cloudwatch_log_group.app",
	})

	// The count members: same estate, same address, different slots. No
	// index is an identity, so what distinguishes them is the slot marker.
	eips := eipsByAllocation(t)
	if len(eips) != 2 {
		t.Fatalf("want 2 EIPs, got %d: %v", len(eips), eips)
	}
	slots := map[string]string{}
	for id, tags := range eips {
		if got := tags["tofu-estate"]; got != estate {
			t.Errorf("EIP %s carries tofu-estate %q, want %q", id, got, estate)
		}
		if !strings.HasPrefix(tags["tofu-address"], "aws_eip.pool") {
			t.Errorf("EIP %s carries tofu-address %q", id, tags["tofu-address"])
		}
		if tags["tofu-slot"] == "" {
			t.Errorf("EIP %s carries no tofu-slot", id)
		}
		slots[tags["tofu-slot"]] = id
	}
	if len(slots) != 2 {
		t.Errorf("the two EIPs do not carry distinct slots: %v", slots)
	}
	t.Logf("EIP slots: %v", slots)

	// --- 3. A plain plan recovers the same estate ------------------------

	clean := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(clean, "No changes.") {
		t.Errorf("the second run did not recover the estate:\n%s", clean)
	}
	if got := flocitest.ChangedResources(clean); len(got) > 0 {
		t.Errorf("a clean plan proposed changes to %v:\n%s", got, clean)
	}
	assertNoState(t, dir, "after a plain plan")

	// --- 4. Out-of-band drift is exactly its own diff --------------------

	awsRun(t, "ec2", "create-tags", "--resources", vpcID,
		"--tags", "Key=Owner,Value=someone-else")
	t.Logf("wrote Owner=someone-else onto %s out of band", vpcID)

	drift := tofu(t, tofuBin, dir, "plan")
	add, change, destroy, ok := flocitest.PlanSummary(drift)
	if !ok {
		t.Fatalf("no plan summary after the out-of-band change:\n%s", drift)
	}
	t.Logf("drift plan: %d to add, %d to change, %d to destroy", add, change, destroy)
	if add != 0 || change != 1 || destroy != 0 {
		t.Errorf("want 0 to add / 1 to change / 0 to destroy, got %d/%d/%d:\n%s", add, change, destroy, drift)
	}
	if got := flocitest.ChangedResources(drift); len(got) != 1 || got[0] != "aws_vpc.main" {
		t.Errorf("the drift plan touches %v, want only aws_vpc.main:\n%s", got, drift)
	}
	if block := flocitest.ResourceBlock(t, drift, "aws_vpc.main"); !strings.Contains(block, "Owner") {
		t.Errorf("the VPC's diff does not mention the tag that was added out of band:\n%s", block)
	} else if extra := flocitest.NonTagChanges(block); len(extra) > 0 {
		t.Errorf("the drift diff changes more than tags: %v\n%s", extra, block)
	}

	// Applying it puts the estate back, which is the drift half of the
	// lifecycle and not only a plan-time observation.
	back := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if _, ch, _, ok := applySummary(back); !ok || ch != 1 {
		t.Errorf("the corrective apply did not change exactly one resource:\n%s", back)
	}
	if tags := ec2Tags(t, vpcID); tags["Owner"] != "" {
		t.Errorf("the out-of-band tag survived the corrective apply: %v", tags)
	}
	assertNoState(t, dir, "after the corrective apply")

	// --- 5. Shrinking the count destroys exactly the surplus member ------

	writeFixture(t, dir, fixture(1, true))

	shrink := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	added, changed, destroyed, ok = applySummary(shrink)
	if !ok {
		t.Fatalf("no apply summary for the scale-down:\n%s", shrink)
	}
	t.Logf("scale-down apply: %d added, %d changed, %d destroyed", added, changed, destroyed)
	if added != 0 || destroyed != 1 {
		t.Errorf("want 0 added / 1 destroyed, got %d added / %d changed / %d destroyed:\n%s",
			added, changed, destroyed, shrink)
	}

	after := eipsByAllocation(t)
	if len(after) != 1 {
		t.Fatalf("want 1 EIP left, got %d: %v", len(after), after)
	}
	// Exactness: the survivor is the one that was there before, with the
	// same allocation ID and the same slot. A destroy-and-recreate would
	// satisfy "one left" and fail here, which is why the check is on the ID.
	for id, tags := range after {
		if _, existed := eips[id]; !existed {
			t.Errorf("the surviving EIP %s is not one of the originals %v", id, eips)
		}
		if slots[tags["tofu-slot"]] != id {
			t.Errorf("the surviving EIP %s changed slot: %v", id, tags)
		}
	}
	// The rest of the estate was not disturbed on the way.
	if got := ec2Tags(t, vpcID); got["tofu-address"] != "aws_vpc.main" {
		t.Errorf("the VPC lost its marker during the scale-down: %v", got)
	}
	assertNoState(t, dir, "after the scale-down")

	final := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(final, "No changes.") {
		t.Errorf("the shrunken estate did not converge:\n%s", final)
	}

	// --- 6. Deleting a whole resource block destroys its live resource ---
	//
	// This was the gap P4.1 left open and P5.1 closed. A deleted block used
	// to remove the only thing that made its type visible: discovery lists a
	// type because some declared instance is waiting on a marker, and a
	// deleted block declares nothing, so the live resource was not in the
	// projection and the plan had nothing to destroy it from. The estate-wide
	// sweep is what changed: every admitted type is now listed for this
	// estate's markers whether or not the configuration still mentions it,
	// and a live resource carrying a marker for an address nothing declares
	// enters the prior state with no configuration behind it - which is
	// exactly what a stock run's removal looks like.

	writeFixture(t, dir, fixture(1, false))

	removal := tofu(t, tofuBin, dir, "plan")
	add, change, destroy, ok = flocitest.PlanSummary(removal)
	if !ok {
		t.Fatalf("no plan summary after the block was deleted:\n%s", removal)
	}
	t.Logf("removal plan: %d to add, %d to change, %d to destroy", add, change, destroy)
	if add != 0 || change != 0 || destroy != 1 {
		t.Errorf("want 0 to add / 0 to change / 1 to destroy, got %d/%d/%d:\n%s", add, change, destroy, removal)
	}
	if got := flocitest.ChangedResources(removal); len(got) != 1 || got[0] != "aws_cloudwatch_log_group.app" {
		t.Errorf("the removal plan touches %v, want only aws_cloudwatch_log_group.app:\n%s", got, removal)
	}
	// The marker fact that makes the destroy legitimate is printed, not just
	// the destroy itself.
	if !strings.Contains(removal, "Owned and undeclared") {
		t.Errorf("the plan does not report the resource as owned and undeclared:\n%s", removal)
	}

	gone := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if _, _, destroyed, ok := applySummary(gone); !ok || destroyed != 1 {
		t.Errorf("the removal apply did not destroy exactly one resource:\n%s", gone)
	}
	if got := logGroupTags(t); got != nil {
		t.Errorf("the log group survived the removal apply: %v", got)
	}

	converged := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(converged, "No changes.") {
		t.Errorf("the estate did not converge after the removal:\n%s", converged)
	}

	assertNoState(t, dir, "at the end of the lifecycle")
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// flociPort is this run's emulator port, chosen by the kernel when P4.1's
// test starts its container. The aws* helpers below read it after that test
// assigns it; no other test in this package touches them.
var flociPort string

const (
	awsRegion = "us-east-1"

	estate     = "p41-lifecycle"
	vpcCIDR    = "10.61.0.0/16"
	subnetCIDR = "10.61.1.0/24"
	bucketName = "p41-lifecycle-data"
	logGroup   = "/p41-lifecycle/app"
)

// fixture is the estate, with the live block that is the only thing
// asking for any of this, and with no ownership tag written anywhere: every
// marker the test reads off the cloud got there by stamping.
//
// eips is the count, and withLogGroup controls whether the log group's block
// exists at all, which is step 6's edit.
func fixture(eips int, withLogGroup bool) string {
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

resource "aws_vpc" "main" {
  cidr_block = %q
}

resource "aws_subnet" "app" {
  vpc_id     = aws_vpc.main.id
  cidr_block = %q
}

resource "aws_s3_bucket" "data" {
  bucket = %q
}

resource "aws_eip" "pool" {
  count = %d

  domain = "vpc"
}
`, estate, vpcCIDR, subnetCIDR, bucketName, eips)

	if withLogGroup {
		fmt.Fprintf(&b, `
resource "aws_cloudwatch_log_group" "app" {
  name              = %q
  retention_in_days = 1
}
`, logGroup)
	}
	return b.String()
}

func writeFixture(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The no-state assertion
// ---------------------------------------------------------------------------

// assertNoState walks the working directory and fails on anything that looks
// like state. It runs after every step rather than once at the end, so that a
// file written and then cleaned up would still be caught.
//
// .terraform is skipped: it holds the provider plugins and the dependency
// lock, which "choudoufu init" writes and which are not state. Its own
// .terraform/terraform.tfstate - the backend record - is checked explicitly,
// because that one is a state file and a stateless run has no business
// creating it.
func assertNoState(t *testing.T, dir, when string) {
	t.Helper()

	var found []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		if info.IsDir() {
			if info.Name() == "terraform.tfstate.d" {
				found = append(found, rel+" (workspace directory)")
			}
			if rel == ".terraform" {
				// Not skipped entirely - the backend record inside it is
				// checked below - but its plugin tree is not walked.
				if _, err := os.Stat(filepath.Join(path, "terraform.tfstate")); err == nil {
					found = append(found, ".terraform/terraform.tfstate (backend record)")
				}
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if strings.HasSuffix(name, ".tfstate") || strings.HasSuffix(name, ".tfstate.backup") ||
			strings.HasSuffix(name, ".lock.info") {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the working directory: %v", err)
	}
	if len(found) > 0 {
		t.Errorf("state artifacts exist %s: %v", when, found)
	}
}

// ---------------------------------------------------------------------------
// Reading the cloud
// ---------------------------------------------------------------------------

func ec2Tags(t *testing.T, id string) map[string]string {
	t.Helper()

	out := awsJSON(t, "ec2", "describe-tags",
		"--filters", "Name=resource-id,Values="+id,
		"--query", "Tags[].{Key:Key,Value:Value}")
	return decodeTagList(t, out)
}

func bucketTags(t *testing.T) map[string]string {
	t.Helper()

	out := awsJSON(t, "s3api", "get-bucket-tagging", "--bucket", bucketName,
		"--query", "TagSet[].{Key:Key,Value:Value}")
	return decodeTagList(t, out)
}

func logGroupTags(t *testing.T) map[string]string {
	t.Helper()

	out, err := awsOutput("logs", "list-tags-log-group", "--log-group-name", logGroup,
		"--query", "tags", "--output", "json")
	if err != nil {
		// The log group is gone, which step 6 reports as a change in
		// behaviour rather than as an error here.
		return nil
	}
	var tags map[string]string
	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(out), &tags); err != nil {
		t.Fatalf("decoding the log group's tags from %q: %v", out, err)
	}
	return tags
}

// eipsByAllocation is every elastic IP in the account, keyed by allocation ID
// with its tags. The whole account, not a filtered subset: an assertion that
// exactly one EIP survives has to be able to see one that should not.
func eipsByAllocation(t *testing.T) map[string]map[string]string {
	t.Helper()

	out := awsJSON(t, "ec2", "describe-addresses",
		"--query", "Addresses[].{Id:AllocationId,Tags:Tags}")

	var raw []struct {
		Id   string `json:"Id"`
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("decoding describe-addresses output %q: %v", out, err)
	}

	byID := make(map[string]map[string]string, len(raw))
	for _, a := range raw {
		tags := make(map[string]string, len(a.Tags))
		for _, tag := range a.Tags {
			tags[tag.Key] = tag.Value
		}
		byID[a.Id] = tags
	}
	return byID
}

func decodeTagList(t *testing.T, out string) map[string]string {
	t.Helper()

	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "null" {
		return nil
	}
	var pairs []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	}
	if err := json.Unmarshal([]byte(out), &pairs); err != nil {
		t.Fatalf("decoding a tag list from %q: %v", out, err)
	}
	tags := make(map[string]string, len(pairs))
	for _, p := range pairs {
		tags[p.Key] = p.Value
	}
	return tags
}

func assertTags(t *testing.T, got map[string]string, addr string, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s carries %s=%q live, want %q (all tags: %v)", addr, k, got[k], v, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Running the binary and reading its output
// ---------------------------------------------------------------------------

func tofu(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()

	full := append([]string{args[0], "-no-color", "-input=false"}, args[1:]...)
	start := time.Now()
	cmd := exec.Command(bin, full...) //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("choudoufu %s took %s\n%s", strings.Join(full, " "), time.Since(start), output)
	if err != nil {
		t.Fatalf("choudoufu %s failed: %v", strings.Join(full, " "), err)
	}
	return output
}

var applySummaryLine = regexp.MustCompile(`Apply complete! Resources: (\d+) added, (\d+) changed, (\d+) destroyed`)

func applySummary(output string) (added, changed, destroyed int, ok bool) {
	m := applySummaryLine.FindStringSubmatch(output)
	if m == nil {
		return 0, 0, 0, false
	}
	added, _ = strconv.Atoi(m[1])
	changed, _ = strconv.Atoi(m[2])
	destroyed, _ = strconv.Atoi(m[3])
	return added, changed, destroyed, true
}

// ---------------------------------------------------------------------------
// floci and the binary
// ---------------------------------------------------------------------------

func awsOutput(args ...string) (string, error) {
	full := append([]string{"--endpoint-url", "http://localhost:" + flociPort}, args...)
	out, err := exec.Command("aws", full...).Output()
	return strings.TrimSpace(string(out)), err
}

// awsText and awsJSON differ only in the output format they ask for. They
// are two functions rather than one with a flag because a caller that passed
// --output itself would silently get whichever of the two the CLI saw last.
func awsText(t *testing.T, args ...string) string {
	t.Helper()
	return awsQuery(t, append(args, "--output", "text")...)
}

func awsJSON(t *testing.T, args ...string) string {
	t.Helper()
	return awsQuery(t, append(args, "--output", "json")...)
}

func awsQuery(t *testing.T, args ...string) string {
	t.Helper()

	out, err := awsOutput(args...)
	if err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
	return out
}

func awsRun(t *testing.T, args ...string) {
	t.Helper()

	if _, err := awsOutput(args...); err != nil {
		t.Fatalf("aws %s failed: %v", strings.Join(args, " "), err)
	}
}
