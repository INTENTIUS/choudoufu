// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// This is P3.3's live half, and it runs the built binary rather than the
// package underneath it: the claim is "a rename produces no churn", and churn
// is a property of what live-plan prints after live-mv has run.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/mv/ -run TestMvAgainstFloci -v
//
// The shape is the rename-no-churn acceptance P3.5 wires into the harness,
// proven here for both identity paths against one standing estate:
//
//  1. The P0.1 estate fixture is stood up by stock terraform and its state
//     file is deleted, which is the whole adoption step.
//  2. aws_security_group.main is renamed. That is the server-ID path: the
//     live security group's identity is nowhere in configuration, so the only
//     thing connecting it to an address is the marker, and rewriting the
//     marker is the entire move.
//  3. aws_cloudwatch_log_group.app is renamed. That is the client-named path:
//     the log group's identity is its name, which the rename does not change,
//     so the marker rewrite is what stops the plan from seeing an orphan at
//     the old address and an unmarked group at the new one.
//  4. aws_s3_bucket.data is renamed, which is the same client-named path over
//     the type the emulator has a tagging gap on. It runs last because that
//     gap leaves a standing diff. See flociS3TagGap.
//
// After each, live-plan must be clean - modulo the emulator gaps named
// below - and the live tag must actually read back as the new address through
// the AWS CLI, which is the check that does not trust this fork's own reads.
//
// It is gated because it needs Docker, terraform, the AWS CLI and a few
// minutes.

// flociPort is this run's emulator port, chosen by the kernel when the test
// starts the container. Helpers below read it after the test assigns it.
var flociPort string

const (
	awsRegion = "us-east-1"

	// estateName is the estate the P0.1 fixture stamps on everything.
	estateName = "stateless-e2e"

	// estateBucket is the fixture's bucket, whose name is its identity and
	// which the rename below deliberately does not change.
	estateBucket = "tofu-stateless-e2e-data"

	// terraformBin stands the estate up. Stock terraform on purpose: what is
	// renamed has to be something this fork did not create.
	terraformBin = "terraform"

	// estateLogGroup is the fixture's log group, the client-named type this
	// test renames for a gap-free proof of that path.
	estateLogGroup = "/stateless-e2e/app"

	// flociRoleGap is the standing known gap every plan against this estate
	// carries: iam:GetRole omits Tags, so aws_iam_role.app reads back
	// untagged, which under RA.1's verified ownership means it is not this
	// estate's - omitted as [UNOWNED] and planned as a create. See
	// test/floci-gaps.md #5 in the chant checkout and the harness's note in
	// stateless/e2e/run.sh step 5.
	flociRoleGap = "aws_iam_role.app"

	// flociPolicyChild is the untaggable child of an unowned parent, and the
	// one tolerated shape here that is not an emulator gap at all.
	// aws_s3_bucket_policy carries no tags, so it has no marker and no
	// ownership of its own: it is admitted as a named singleton child of its
	// bucket. Its policy document interpolates aws_iam_role.app's ARN, and
	// the role reads back untagged on floci, so under RA.1 the role is
	// unowned, planned as a create, and its ARN is unknown until apply -
	// which makes the policy's own document unknown, so the policy re-plans.
	// While the bucket keeps its name that is an in-place update; once the
	// bucket rename makes the bucket reference unknown too, it is a
	// replacement. Neither is churn the rename caused: both follow from the
	// role's ARN, and both would disappear the moment the emulator served
	// tags on iam:GetRole. This is the untaggable-child boundary
	// LIMITATIONS.md documents, showing up live.
	flociPolicyChild = "aws_s3_bucket_policy.data"

	// flociBucketChildren is flociPolicyChild's boundary generalized: the
	// #19 slice hung four more untaggable children off the data bucket,
	// and across the bucket rename every one of them re-plans the same
	// way the policy does, because its bucket reference goes unknown when
	// the S3 tag gap plans the parent as a create. Same tolerance, same
	// limits: change or replace only, never a bare destroy.
)

var flociBucketChildren = map[string]bool{
	flociPolicyChild:                                          true,
	"aws_s3_bucket_versioning.data":                           true,
	"aws_s3_bucket_public_access_block.data":                  true,
	"aws_s3_bucket_server_side_encryption_configuration.data": true,
	"aws_s3_bucket_lifecycle_configuration.data":              true,
}

const (

	// flociS3TagGap is a second emulator gap, found by this task. The AWS
	// provider updates aws_s3_bucket tags through the S3 Control TagResource
	// API, sending only the tags whose values changed - which is correct
	// against AWS, where that call merges. The emulator implements it as a
	// whole-tag-set replacement, so every tag the call did not mention is
	// dropped: after any tag update, including a plain "terraform apply"
	// that edits one tag value, the bucket keeps only the changed tag. A
	// marker rewrite therefore leaves the bucket carrying tofu-address and
	// no tofu-estate here. Under RA.1 a resource with no tofu-estate tag is
	// not this estate's, so on the next plan the bucket is UNOWNED rather
	// than showing a tags-only diff: omitted from the prior state with an
	// adoption hint, and planned as a create. The rename itself is correct -
	// the CLI read above confirms tofu-address on the live bucket - and the
	// tolerance is scoped to this one address.
	flociS3TagGap = "aws_s3_bucket.archive"

	// flociSSMGap is a third standing emulator gap (test/floci-gaps.md #10 in
	// the chant checkout): floci drops the inline tags an SSM parameter is
	// created with, so aws_ssm_parameter.demo_effect reads back untagged.
	// Same shape as flociRoleGap and, under RA.1, the same consequence: no
	// marker means not owned, so it is omitted as [UNOWNED] and planned as a
	// create rather than quietly re-tagged in place.
	flociSSMGap = "aws_ssm_parameter.demo_effect"

	// flociSSMExistenceGap is the same #10 gap on the estate's second SSM
	// parameter, the existence-flavor receipt RA.6 added
	// (stateless/e2e/estate/receipts.tf). Same standing shape as
	// flociSSMGap, tolerated the same way.
	flociSSMExistenceGap = "aws_ssm_parameter.demo_existence"
)

func TestMvAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "rename")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	flociPort = flocitest.StartFloci(t, "cdf-p33")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+flociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	// One provider unpack for the whole machine, shared by terraform and tofu.
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := flocitest.CopyEstate(t)

	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	// No destroy on the way out: the cloud these resources live in is the
	// container, and the container is removed. A terraform destroy here would
	// also fail on the lock file tofu init rewrites.
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	// A fresh stock apply predates P3.1's slot markers: every EIP carries
	// tofu-address and no tofu-slot yet, so the very first live-plan
	// would itself propose the one-time slot mint (run.sh's "slot-migration"
	// step, 3c, performs the identical write for the harness's own estate).
	// Perform it here too, or "baseline" below would not be the no-churn
	// starting point the rest of this test needs it to be.
	migrateEipSlots(t, estateName)

	// The baseline: whatever this estate plans to before anything is renamed
	// is what "no churn" has to mean afterwards.
	assertCleanPlan(t, "baseline", statelessPlan(t, tofuBin, dir))

	sgID := flocitest.AWSCLI(t, flociPort, "ec2", "describe-security-groups",
		"--filters", "Name=tag:tofu-estate,Values="+estateName,
		"--query", "SecurityGroups[0].GroupId", "--output", "text")
	if sgID == "" || sgID == "None" {
		t.Fatalf("could not find the estate's security group by its tofu-estate tag")
	}
	t.Logf("the estate's security group is %s", sgID)

	// --- The server-ID path: aws_security_group ---------------------------
	t.Run("security group (server-assigned identity, discovery path)", func(t *testing.T) {
		rename(t, dir, map[string]string{
			`resource "aws_security_group" "main"`: `resource "aws_security_group" "renamed"`,
			"aws_security_group.main":              "aws_security_group.renamed",
		})

		// A dry run first: it reports the same rewrite and writes nothing.
		dry := statelessMv(t, tofuBin, dir, "-dry-run", "aws_security_group.main", "aws_security_group.renamed")
		if !strings.Contains(dry, "Nothing was written (-dry-run)") {
			t.Errorf("the dry run does not say it wrote nothing:\n%s", dry)
		}
		if got := ec2Tag(t, sgID, "tofu-address"); got != "aws_security_group.main" {
			t.Fatalf("-dry-run changed the live tag: tofu-address = %q", got)
		}

		out := statelessMv(t, tofuBin, dir, "aws_security_group.main", "aws_security_group.renamed")
		if !strings.Contains(out, "This was a cloud write.") {
			t.Errorf("the rename does not report a cloud write:\n%s", out)
		}
		if !strings.Contains(out, sgID) {
			t.Errorf("the rename does not name the live resource %s it wrote to:\n%s", sgID, out)
		}

		// The check that does not trust this fork's own reads.
		if got := ec2Tag(t, sgID, "tofu-address"); got != "aws_security_group.renamed" {
			t.Fatalf("the live tofu-address tag on %s reads %q, want aws_security_group.renamed", sgID, got)
		}
		if got := ec2Tag(t, sgID, "tofu-estate"); got != estateName {
			t.Errorf("the estate marker on %s reads %q, want %s", sgID, got, estateName)
		}

		assertCleanPlan(t, "after the security group rename", statelessPlan(t, tofuBin, dir))
	})

	// --- The client-named path: aws_cloudwatch_log_group ------------------
	t.Run("log group (client-named identity, identity path)", func(t *testing.T) {
		rename(t, dir, map[string]string{
			`resource "aws_cloudwatch_log_group" "app"`: `resource "aws_cloudwatch_log_group" "renamed"`,
			"aws_cloudwatch_log_group.app":              "aws_cloudwatch_log_group.renamed",
		})

		out := statelessMv(t, tofuBin, dir, "aws_cloudwatch_log_group.app", "aws_cloudwatch_log_group.renamed")
		if !strings.Contains(out, "This was a cloud write.") {
			t.Errorf("the rename does not report a cloud write:\n%s", out)
		}
		if !strings.Contains(out, "whose identity this configuration names") {
			t.Errorf("the client-named resource was not found through its own identity:\n%s", out)
		}

		tags := logGroupTags(t, estateLogGroup)
		if got := tags["tofu-address"]; got != "aws_cloudwatch_log_group.renamed" {
			t.Fatalf("the live tofu-address tag on %s reads %q, want aws_cloudwatch_log_group.renamed", estateLogGroup, got)
		}
		if got := tags["tofu-estate"]; got != estateName {
			t.Errorf("the estate marker on %s reads %q, want %s", estateLogGroup, got, estateName)
		}

		assertCleanPlan(t, "after the log group rename", statelessPlan(t, tofuBin, dir))
	})

	// --- The same path over the type with the emulator's tagging gap ------
	t.Run("bucket (client-named identity, identity path)", func(t *testing.T) {
		rename(t, dir, map[string]string{
			`resource "aws_s3_bucket" "data"`: `resource "aws_s3_bucket" "archive"`,
			"aws_s3_bucket.data":              "aws_s3_bucket.archive",
		})

		out := statelessMv(t, tofuBin, dir, "aws_s3_bucket.data", "aws_s3_bucket.archive")
		if !strings.Contains(out, "This was a cloud write.") {
			t.Errorf("the rename does not report a cloud write:\n%s", out)
		}
		if !strings.Contains(out, estateBucket) {
			t.Errorf("the rename does not name the live bucket it wrote to:\n%s", out)
		}

		t.Logf("live tag set of %s after the rename: %s",
			estateBucket, flocitest.AWSCLI(t, flociPort, "s3api", "get-bucket-tagging", "--bucket", estateBucket, "--output", "json"))

		// The marker itself: this is the claim, and it holds.
		if got := bucketTag(t, estateBucket, "tofu-address"); got != "aws_s3_bucket.archive" {
			t.Fatalf("the live tofu-address tag on %s reads %q, want aws_s3_bucket.archive", estateBucket, got)
		}
		// The estate tag survives on real AWS and does not survive here. See
		// flociS3TagGap; the same loss happens under a stock terraform apply
		// that edits one tag value, so it is the emulator's, not the rename's.
		if got := bucketTag(t, estateBucket, "tofu-estate"); got != estateName {
			t.Logf("known emulator gap: %s lost its tofu-estate tag (%q) because the emulator's S3 Control TagResource replaces the tag set instead of merging", estateBucket, got)
		}

		assertCleanPlan(t, "after the bucket rename", statelessPlan(t, tofuBin, dir), flociS3TagGap)
	})

	// Nothing was written to disk by any of it.
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("a state file exists after the renames (err = %v)", err)
	}
}

// rename applies the configuration edit a rename is: the resource block's
// label, and every reference to its address - which includes the fixture's
// hand-written tofu-address tag, the value live-plan would otherwise
// dispute with the marker this command wrote.
func rename(t *testing.T, dir string, replacements map[string]string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	changed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) //nolint:gosec // this test's own temp dir
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		out := string(data)
		for from, to := range replacements {
			out = strings.ReplaceAll(out, from, to)
		}
		if out == string(data) {
			continue
		}
		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		changed++
	}
	if changed == 0 {
		t.Fatalf("the rename edited no file in %s; the fixture does not look like the estate", dir)
	}
}

// assertCleanPlan is the no-churn claim, in the shape the estate actually
// has under RA.1's verified ownership.
//
// The claim itself has not moved: a rename is a marker rewrite and nothing
// else, so nothing the rename touched may appear in the plan at all. What
// moved is the baseline it is measured against.
//
// Before RA.1 a client-named resource was read by its import ID and entered
// the prior state whatever tags came back, so this fixture's two untaggable-
// on-floci resources showed up as tags-only in-place diffs and the tolerance
// was "an in-place diff on these addresses, tags only". RA.1 made an
// ownership marker a precondition for entering the prior state: a resource
// that reads back with no tofu-estate tag is not this estate's, so it stays
// out of the projection, the plan proposes creating what the configuration
// declares, and the omissions section says [UNOWNED] with an adoption hint.
// Same emulator gaps, different and more honest rendering:
//
//	aws_iam_role.app                   floci-gaps #5   iam:GetRole omits Tags
//	aws_ssm_parameter.demo_effect               #10    PutParameter drops inline tags
//	aws_ssm_parameter.demo_existence            #10    same gap, the RA.6 fixture
//	aws_s3_bucket.archive                       #9     S3 TagResource replaces the set
//
// The third only appears after the bucket rename, and for the same reason
// the rename is what triggers it: rewriting tofu-address through TagResource
// drops tofu-estate, so the bucket the rename just succeeded on reads back
// unowned on the next plan. That is the emulator's write path, not the
// rename's - the CLI read above already confirmed tofu-address is correct on
// the live bucket.
//
// aws_s3_bucket_policy.data is the fourth tolerated shape and it is not an
// emulator gap at all: it is the documented untaggable-child boundary. The
// policy interpolates the app role's ARN, the role is unowned and therefore
// planned as a create, so the ARN is unknown and the policy re-plans - as an
// in-place update while the bucket keeps its name, and as a replacement once
// the bucket rename makes its own bucket reference unknown too. Tolerated
// by address, and only ever as change or replace; a policy DESTROY with no
// replacement half would be a real finding and still fails.
//
// unownedGaps names the addresses tolerated in the create + [UNOWNED] shape
// beyond the two standing ones.
func assertCleanPlan(t *testing.T, when, output string, unownedGaps ...string) {
	t.Helper()

	unowned := map[string]bool{flociRoleGap: true, flociSSMGap: true, flociSSMExistenceGap: true}
	for _, addr := range unownedGaps {
		unowned[addr] = true
	}

	if output == "" {
		t.Fatalf("%s: live-plan printed nothing", when)
	}
	if !strings.Contains(output, "Plan:") && !strings.Contains(output, "No changes.") {
		t.Fatalf("%s: plan output has no recognizable summary, refusing to trust any assertion against it:\n%s", when, output)
	}

	// Every omission is one of the tolerated unowned addresses, and every
	// one of them is an [UNOWNED] omission specifically - an instance the
	// run could not read for some other reason would be a different and
	// unexplained failure.
	for _, line := range strings.Split(flocitest.SectionFrom(output, "Not read from the live system"), "\n") {
		m := omittedInstance.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		addr, reason := m[1], m[2]
		if !unowned[addr] {
			t.Errorf("%s: %s could not be read (%s), so the plan is not the no-churn answer:\n%s",
				when, addr, reason, flocitest.SectionFrom(output, "Not read from the live system"))
			continue
		}
		if reason != "UNOWNED" {
			t.Errorf("%s: %s is a known emulator tag gap, so it must be omitted as [UNOWNED]; it says [%s] instead:\n%s",
				when, addr, reason, flocitest.SectionFrom(output, "Not read from the live system"))
			continue
		}
		if !strings.Contains(output, `tofu-address="`+addr+`"`) {
			t.Errorf("%s: %s's [UNOWNED] omission carries no adoption hint naming it:\n%s",
				when, addr, flocitest.SectionFrom(output, "Not read from the live system"))
		}
		t.Logf("%s: known emulator tag gap on %s - reads back unowned, so it plans as a create", when, addr)
	}

	for _, addr := range flocitest.ChangedResources(output) {
		block := flocitest.ResourceBlock(t, output, addr)
		switch {
		case strings.Contains(block, "will be created"):
			if !unowned[addr] {
				t.Errorf("%s: the plan proposes creating %s:\n%s", when, addr, block)
			} else if !strings.Contains(output, "  "+addr+" [UNOWNED]") {
				// A create with no omission behind it is the shape C1
				// named: something entered or left the prior state
				// without the ownership check having anything to say.
				t.Errorf("%s: %s is planned as a create but is not reported as an unowned omission:\n%s", when, addr, output)
			}
		case strings.Contains(block, "must be replaced"):
			if !flociBucketChildren[addr] {
				t.Errorf("%s: the plan proposes replacing %s, which a rename must not produce:\n%s", when, addr, block)
			} else {
				t.Logf("%s: %s replaces - its bucket and the unowned role's ARN are both unknown (untaggable-child boundary)", when, addr)
			}
		case strings.Contains(block, "will be destroyed"):
			t.Errorf("%s: the plan proposes destroying %s:\n%s", when, addr, block)
		case flociBucketChildren[addr]:
			t.Logf("%s: %s re-plans - it references an unowned resource (untaggable-child boundary)", when, addr)
		default:
			t.Errorf("%s: the plan proposes a change to %s, which a rename must not produce:\n%s", when, addr, block)
		}
	}

	// The summary and the headers must agree: exactly as many creates as
	// there are unowned omissions, plus a replacement half for each bucket
	// child that replaces, and no destroy that is not one of those same
	// replacements.
	if add, change, destroy, ok := flocitest.PlanSummary(output); ok {
		t.Logf("%s: %d to add, %d to change, %d to destroy", when, add, change, destroy)
		wantAdd := len(unownedInstances(output))
		childrenReplaced := 0
		for addr := range flociBucketChildren {
			if strings.Contains(output, "# "+addr+" must be replaced") {
				childrenReplaced++
			}
		}
		wantAdd += childrenReplaced
		if add != wantAdd {
			t.Errorf("%s: the plan adds %d resource(s) but only %d are accounted for by unowned omissions%s:\n%s",
				when, add, wantAdd, map[bool]string{true: " and the bucket children's replacements"}[childrenReplaced > 0], output)
		}
		if destroy != childrenReplaced {
			t.Errorf("%s: expected %d destroy(s), got %d:\n%s", when, childrenReplaced, destroy, output)
		}
	}
}

var omittedInstance = regexp.MustCompile(`^\s+(\S+) \[([A-Z_]+)\]\s*$`)

// unownedInstances is every address the plan reports as [UNOWNED].
func unownedInstances(output string) []string {
	var out []string
	for _, line := range strings.Split(flocitest.SectionFrom(output, "Not read from the live system"), "\n") {
		if m := omittedInstance.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil && m[2] == "UNOWNED" {
			out = append(out, m[1])
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Running the commands
// ---------------------------------------------------------------------------

func statelessPlan(t *testing.T, tofuBin, dir string, args ...string) string {
	t.Helper()

	full := append([]string{"live-plan", "-no-color", "-input=false"}, args...)
	out, err := runOut(t, dir, tofuBin, full...)
	if err != nil {
		t.Fatalf("live-plan failed: %v\n%s", err, out)
	}
	return out
}

func statelessMv(t *testing.T, tofuBin, dir string, args ...string) string {
	t.Helper()

	full := append([]string{"live-mv", "-no-color"}, args...)
	out, err := runOut(t, dir, tofuBin, full...)
	if err != nil {
		t.Fatalf("live-mv failed: %v\n%s", err, out)
	}
	return out
}

func runOut(t *testing.T, dir, name string, args ...string) (string, error) {
	t.Helper()

	start := time.Now()
	cmd := exec.Command(name, args...) //nolint:gosec // fixed binaries, this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	t.Logf("%s %s took %s\n%s", filepath.Base(name), strings.Join(args, " "), time.Since(start), out)
	return string(out), err
}

// migrateEipSlots stamps tofu-slot onto the estate's three EIPs, the
// one-time write a fresh, pre-P3.1 estate needs (run.sh's "slot-migration"
// step, 3c, does the identical thing for the harness's own estate). For a
// freshly-applied fixture with no prior migration, the slot a plan would
// propose is always the index already in the (escaped) tofu-address tag, so
// this stamps tofu-slot=N directly onto the allocation carrying
// tofu-address=aws_eip.pool:N rather than reading the proposal out of a
// plan first.
func migrateEipSlots(t *testing.T, estate string) {
	t.Helper()

	// Unfiltered read, matched client-side: floci's ec2:DescribeAddresses
	// ignores --filters (floci-gaps #8), the same reason run.sh's
	// slot-migration step reads every address and matches by hand.
	raw := flocitest.AWSCLI(t, flociPort, "ec2", "describe-addresses", "--query",
		"Addresses[].[AllocationId,Tags[?Key=='tofu-estate']|[0].Value,Tags[?Key=='tofu-address']|[0].Value]",
		"--output", "text")

	for i := 0; i < 3; i++ {
		want := fmt.Sprintf("aws_eip.pool:%d", i)
		var allocID string
		for _, line := range strings.Split(raw, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 {
				continue
			}
			if fields[1] == estate && fields[2] == want {
				allocID = fields[0]
				break
			}
		}
		if allocID == "" {
			t.Fatalf("no live EIP tagged tofu-estate=%s tofu-address=%s (raw addresses:\n%s)", estate, want, raw)
		}
		flocitest.AWSCLI(t, flociPort, "ec2", "create-tags", "--resources", allocID, "--tags",
			fmt.Sprintf("Key=tofu-slot,Value=%d", i))
		t.Logf("%s -> %s takes tofu-slot=%d", want, allocID, i)
	}
}

// ---------------------------------------------------------------------------
// Reading the live system without going through this fork
// ---------------------------------------------------------------------------

// ec2Tag reads one tag off an EC2 resource with the AWS CLI's own
// describe-tags, which is the check that nothing in this fork's read path is
// involved in confirming its own write.
func ec2Tag(t *testing.T, resourceID, key string) string {
	t.Helper()

	got := flocitest.AWSCLI(t, flociPort, "ec2", "describe-tags",
		"--filters", "Name=resource-id,Values="+resourceID, "Name=key,Values="+key,
		"--query", "Tags[0].Value", "--output", "text")
	if got == "None" {
		return ""
	}
	return got
}

// logGroupTags reads a log group's whole tag set with the AWS CLI.
func logGroupTags(t *testing.T, name string) map[string]string {
	t.Helper()

	raw := flocitest.AWSCLI(t, flociPort, "logs", "list-tags-log-group", "--log-group-name", name, "--output", "json")
	var parsed struct {
		Tags map[string]string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("cannot read the tags of %s: %v\n%s", name, err, raw)
	}
	return parsed.Tags
}

func bucketTag(t *testing.T, bucket, key string) string {
	t.Helper()

	raw := flocitest.AWSCLI(t, flociPort, "s3api", "get-bucket-tagging", "--bucket", bucket, "--output", "json")
	var parsed struct {
		TagSet []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"TagSet"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("cannot read the tag set of %s: %v\n%s", bucket, err, raw)
	}
	for _, tag := range parsed.TagSet {
		if tag.Key == key {
			return tag.Value
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Reading plan output
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// floci, the fixture and the binary
// ---------------------------------------------------------------------------
