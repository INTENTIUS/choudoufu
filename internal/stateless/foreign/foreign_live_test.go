// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// This is P2.4's live half, and it runs the real command rather than the
// packages underneath it: an estate stood up by stock terraform, its state
// deleted, one unmarked security group created out of band with the AWS CLI,
// and then `choudoufu live-plan` over the lot.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/foreign/ -run TestForeignAgainstFloci -v
//
// Driving the built binary is the point: the claim under test is about what
// an operator sees and about what the planner can do, and both of those are
// properties of the command, not of a function this package exports.
//
// It is gated because it needs Docker, terraform, the AWS CLI, a Go
// toolchain and a few minutes.

// flociPort is this run's emulator port, chosen by the kernel when the test
// starts the container. A helper below reads it after the test assigns it.
var flociPort string

const (
	awsRegion = "us-east-1"

	// terraformBin stands the estate up. Stock terraform on purpose: the
	// estate this classification runs over must be one stateless mode did
	// not create.
	terraformBin = "terraform"
)

func TestForeignAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "foreign-classification")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	flociPort = flocitest.StartFloci(t, "cdf-p24")
	endpoint := "http://localhost:" + flociPort

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := flocitest.CopyEstate(t)

	// Stock terraform stands the estate up, then tofu init - mandatory, and
	// not a formality: terraform populates registry.terraform.io provider
	// paths and tofu resolves registry.opentofu.org, so without it every
	// projection entry fails with an unavailable provider (P1.5's note).
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	// One live resource of a managed type that nothing in this estate owns.
	vpcID := flocitest.AWSCLI(t, flociPort, "ec2", "describe-vpcs",
		"--filters", "Name=tag:tofu-estate,Values="+estateName,
		"--query", "Vpcs[0].VpcId", "--output", "text")
	if vpcID == "" || vpcID == "None" {
		t.Fatalf("the estate's VPC could not be found by its marker")
	}
	foreignName := fmt.Sprintf("stateless-e2e-foreign-%d", os.Getpid())
	foreignSG := flocitest.AWSCLI(t, flociPort, "ec2", "create-security-group",
		"--group-name", foreignName,
		"--description", "unmanaged, no tofu-estate marker",
		"--vpc-id", vpcID,
		"--query", "GroupId", "--output", "text")
	if foreignSG == "" || foreignSG == "None" {
		t.Fatal("could not create the unmarked security group")
	}
	t.Logf("out-of-band security group %s (%s) in %s carries no marker", foreignSG, foreignName, vpcID)

	// --- The whole pipeline, through the command -------------------------
	start := time.Now()
	cmd := exec.Command(tofuBin, "live-plan", "-no-color", "-input=false") //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	output := string(out)
	t.Logf("choudoufu live-plan took %s\n%s", elapsed, output)
	if err != nil {
		t.Fatalf("live-plan failed: %v", err)
	}

	// --- FOREIGN ---------------------------------------------------------
	section := foreignSection(t, output)
	if !strings.Contains(output, "Foreign resources:") {
		t.Fatal("no foreign section in the output")
	}
	if !strings.Contains(section, foreignSG) {
		t.Errorf("the out-of-band security group %s is not reported as foreign:\n%s", foreignSG, section)
	}
	if !strings.Contains(section, foreignName) {
		t.Errorf("the foreign section does not carry the group's name %q:\n%s", foreignName, section)
	}

	// floci serves a default VPC's worth of unmarked resources - subnets, a
	// route table, an internet gateway, security groups. Under this
	// package's rules every one of them is plain FOREIGN: none of them is a
	// count or for_each member of this configuration, none has a name or
	// CIDR matching a declared instance, and the declared instances are all
	// bound anyway. The default VPC itself never appears at all, because the
	// AWS provider appends is-default = false to every EC2 list.
	count := foreignCount(t, output)
	t.Logf("FOREIGN resources reported: %d (the out-of-band SG plus floci's unmarked defaults)", count)
	if count < 2 {
		t.Errorf("only %d foreign resource(s) reported; floci's unmarked defaults were expected alongside the created one:\n%s", count, section)
	}
	if strings.Contains(output, "Adoptable:") {
		t.Errorf("something was offered for adoption, but every declared instance is bound and no live resource matches one:\n%s", section)
	}

	// --- PROTECTION ------------------------------------------------------
	add, change, destroy, ok := flocitest.PlanSummary(output)
	switch {
	case strings.Contains(output, "No changes."):
		t.Log("the estate planned clean: no changes at all")
	case !ok:
		t.Fatalf("could not read a plan summary out of the output")
	default:
		t.Logf("plan summary: %d to add, %d to change, %d to destroy", add, change, destroy)
	}
	// The destroy claim is the load-bearing one and it is absolute: nothing
	// unowned may ever become a deletion candidate, whatever else the plan
	// says.
	if ok && destroy != 0 {
		t.Errorf("the plan proposes %d destroy(s); nothing unowned may ever be a deletion candidate", destroy)
	}
	if strings.Contains(output, "will be destroyed") {
		t.Error("the plan output contains a destroy")
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "-") && strings.Contains(line, foreignSG) {
			t.Errorf("the foreign security group appears on a removal line: %q", line)
		}
	}

	// --- The two emulator read gaps, and what RA.1 made of them ----------
	//
	// This assertion used to be "add == 0, and no omissions section at all",
	// which was true while the projection read a client-named resource by
	// import ID and asked no further questions. RA.1 made ownership a
	// precondition for entering the prior state: a resource that reads back
	// without this estate's tofu-estate marker is not owned, so it stays out
	// of the projection and the configuration's declaration of it plans as a
	// create.
	//
	// Three of the fixture's resources read back untagged on floci, none of
	// them by anything this fork does:
	//
	//   aws_iam_role.app                  floci-gaps #5  - iam:GetRole omits Tags
	//   aws_ssm_parameter.demo_effect     #10            - PutParameter drops inline tags
	//   aws_ssm_parameter.demo_existence  #10            - same gap, the RA.6 fixture
	//
	// So all three are correctly UNOWNED here: absent from the prior state,
	// listed as an [UNOWNED] omission with an adoption hint, and planned as a
	// create. That is the corrected behaviour, and the assertion is exact -
	// those three addresses and no others, still zero destroys. On an
	// emulator that served tags on every read the estate would materialize
	// whole and the count would be zero, which is also accepted below.
	unowned := unownedOmissions(output)
	wantUnowned := []string{"aws_iam_role.app", "aws_ssm_parameter.demo_effect", "aws_ssm_parameter.demo_existence"}
	if len(unowned) == 0 {
		t.Log("no [UNOWNED] omissions: the emulator served tags on both known read gaps")
		if ok && add != 0 {
			t.Errorf("the plan proposes %d create(s) with nothing reported unowned:\n%s", add, output)
		}
	} else {
		if !slices.Equal(unowned, wantUnowned) {
			t.Errorf("the [UNOWNED] omissions are %v, want exactly the two documented emulator read gaps %v:\n%s",
				unowned, wantUnowned, omissionSection(output))
		}
		if ok && add != len(unowned) {
			t.Errorf("the plan proposes %d create(s) but reports %d unowned instance(s); every create here must be an unowned one:\n%s",
				add, len(unowned), output)
		}
		for _, addr := range unowned {
			if !strings.Contains(output, "# "+addr+" will be created") {
				t.Errorf("%s is reported unowned but has no create header; the omission and the plan disagree:\n%s", addr, output)
			}
			if !strings.Contains(output, `tofu-address="`+addr+`"`) {
				t.Errorf("%s's omission carries no adoption hint naming it:\n%s", addr, omissionSection(output))
			}
		}
		// The unowned resources are reported, never touched. An [UNOWNED]
		// omission that also produced an update or a destroy would be the
		// bug C1 named.
		for _, addr := range unowned {
			if strings.Contains(output, "# "+addr+" will be updated in-place") ||
				strings.Contains(output, "# "+addr+" will be destroyed") {
				t.Errorf("%s is unowned but the plan proposes to change or destroy it:\n%s", addr, output)
			}
		}
	}

	// Everything else the fixture declares still materialized: the omissions
	// section lists the unowned pair and nothing more.
	if got := omissionCount(t, output); got != len(unowned) {
		t.Errorf("the plan omits %d instance(s) but only %d are the documented unowned pair:\n%s",
			got, len(unowned), omissionSection(output))
	}

	// --- Epistemics ------------------------------------------------------
	if !strings.Contains(output, "Not swept:") {
		t.Error("the output does not say which resource types were never listed")
	}
	for _, typeName := range []string{"aws_s3_bucket", "aws_iam_role", "aws_cloudwatch_log_group"} {
		if !strings.Contains(output, typeName) {
			t.Errorf("%s is not named as an unswept type; the report implies a census it did not make", typeName)
		}
	}

	// Nothing was written: the estate is recovered from tags every run.
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("a state file exists after live-plan (err = %v)", err)
	}

	// --- P3.2: a for_each key renamed in configuration -------------------
	// Riding on the estate this test already stood up rather than paying for
	// a second one. It runs last because it edits the configuration.
	renameKeyIsOffered(t, dir, tofuBin)
}

// renameKeyIsOffered renames a subnet's for_each key in the standing estate's
// configuration and checks that the next plan offers the marker rewrite: the
// live subnet is still there under the old key, the new key is a create, and
// the run says the two are probably the same resource.
func renameKeyIsOffered(t *testing.T, dir, tofuBin string) {
	t.Helper()

	subnetB := flocitest.AWSCLI(t, flociPort, "ec2", "describe-subnets",
		"--filters", "Name=tag:tofu-address,Values=aws_subnet.this:b",
		"--query", "Subnets[0].SubnetId", "--output", "text")
	if subnetB == "" || subnetB == "None" {
		t.Fatalf("the subnet at key b could not be found by its marker")
	}

	// The whole edit. The fixture writes tofu-address from each.key, so
	// renaming the key in the map is the entire configuration change - which
	// is exactly why the live resource is left carrying the old one.
	localsPath := filepath.Join(dir, "locals.tf")
	before, err := os.ReadFile(localsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", localsPath, err)
	}
	after := strings.Replace(string(before), "b = { cidr", "c = { cidr", 1)
	if after == string(before) {
		t.Fatalf("the estate fixture's subnet map has no key b to rename:\n%s", before)
	}
	if err := os.WriteFile(localsPath, []byte(after), 0o600); err != nil {
		t.Fatalf("writing %s: %v", localsPath, err)
	}

	cmd := exec.Command(tofuBin, "live-plan", "-no-color", "-input=false") //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("choudoufu live-plan after renaming the subnet key b -> c\n%s", output)
	if err != nil {
		t.Fatalf("live-plan after the rename failed: %v", err)
	}

	section := flocitest.SectionFrom(output, "Renamed keys?")
	if section == "" {
		t.Fatalf("no rename section after a for_each key changed:\n%s", output)
	}
	if !strings.Contains(section, `aws_subnet.this["b"] (live `+subnetB) {
		t.Errorf("the rename line does not pair the old key with the live subnet %s:\n%s", subnetB, section)
	}
	if !strings.Contains(section, `-> aws_subnet.this["c"]`) {
		t.Errorf("the rename line does not name the new key:\n%s", section)
	}
	wantCmd := `choudoufu live-mv 'aws_subnet.this["b"]' 'aws_subnet.this["c"]'`
	if !strings.Contains(section, wantCmd) {
		t.Errorf("the exact command is missing:\n  want %s\n%s", wantCmd, section)
	}

	// Offered, not applied: the plan is what it was before the hint existed.
	if !strings.Contains(output, `aws_subnet.this["c"] will be created`) {
		t.Errorf("the new key is not planned as a create:\n%s", output)
	}
	if _, _, destroy, ok := flocitest.PlanSummary(output); ok && destroy != 0 {
		t.Errorf("the plan proposes %d destroy(s); a rename candidate must not turn the old key into one:\n%s", destroy, output)
	}
	if strings.Contains(output, "will be destroyed") {
		t.Error("the plan output after the rename contains a destroy")
	}
}

// ---------------------------------------------------------------------------
// Output readers
// ---------------------------------------------------------------------------

var foreignHeader = regexp.MustCompile(`Foreign resources: (\d+) live resource`)

func foreignCount(t *testing.T, output string) int {
	t.Helper()

	m := foreignHeader.FindStringSubmatch(output)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("unreadable foreign count %q", m[1])
	}
	return n
}

func foreignSection(t *testing.T, output string) string {
	t.Helper()
	return flocitest.SectionFrom(output, "Foreign resources:")
}

func omissionSection(output string) string {
	return flocitest.SectionFrom(output, "Not read from the live system")
}

var omissionHeader = regexp.MustCompile(`Not read from the live system: (\d+) resource instances?`)

// omissionCount is how many instances the plan says it could not read, from
// the section header's own number rather than by counting lines: the header
// is the claim, and a section whose body disagreed with it would be a bug
// worth catching here.
func omissionCount(t *testing.T, output string) int {
	t.Helper()

	m := omissionHeader.FindStringSubmatch(output)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("unreadable omission count %q", m[1])
	}
	return n
}

var unownedOmission = regexp.MustCompile(`^\s+(\S+) \[UNOWNED\]\s*$`)

// unownedOmissions is every address the omissions section marks [UNOWNED] -
// RA.1's classification for a live resource that exists at the declared
// identity but carries no marker for this estate. Sorted, so the caller can
// compare against a fixed list without caring what order discovery ran in.
func unownedOmissions(output string) []string {
	var out []string
	for _, line := range strings.Split(omissionSection(output), "\n") {
		if m := unownedOmission.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// floci, the estate and the binary
// ---------------------------------------------------------------------------
