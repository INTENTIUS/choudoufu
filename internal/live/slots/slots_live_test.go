// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package slots_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// P3.1's live half. Like P2.1's and P2.4's it runs the built binary against a
// real emulator, because every claim here is a claim about what an operator
// sees in a plan over a cloud that is actually holding the resources.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/slots/ -run TestSlotsAgainstFloci -v -timeout 30m
//
// Two estates, for the two halves of the contract:
//
//  1. The P0.1 estate fixture, which carries no slot markers at all. Its
//     count set has to bind exactly as it did before this task - by the
//     per-instance addresses it writes by hand - and the only new thing in
//     the plan is the slot each member is missing.
//
//  2. A fixture of this test's own, taken all the way through the slot
//     lifecycle: stamped, scaled down, converged, scaled up. That is where
//     the scale-down claim (one destroy, zero churn) and the minting claim
//     (new slots above the high-water mark) are proved against live tags.
//
// It is gated because it needs Docker, terraform, the AWS CLI and several
// minutes.

// flociPort is this run's emulator port, chosen by the kernel when the test
// starts the container. Helpers below read it after the test assigns it.
var flociPort string

const (
	awsRegion = "us-east-1"

	// slotsEstate is this test's own estate, kept away from the P0.1 estate
	// so the two halves cannot see each other's resources.
	slotsEstate = "slots-e2e"

	// estateName is the P0.1 fixture's own estate, which it declares.
	estateName = "stateless-e2e"

	terraformBin = "terraform"
)

func TestSlotsAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "count slot")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	flociPort = flocitest.StartFloci(t, "cdf-p31")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+flociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)

	t.Run("estate-fixture-compat", func(t *testing.T) { estateCompat(t, tofuBin) })
	t.Run("slot-lifecycle", func(t *testing.T) { slotLifecycle(t, tofuBin) })
}

// ---------------------------------------------------------------------------
// 1. The estate fixture, which has no slots
// ---------------------------------------------------------------------------

// estateCompat stands up the P0.1 estate and plans against it. Its EIPs carry
// per-index tofu-address tags and no tofu-slot, so this is the compatibility
// path over real resources: the set binds by address, nothing is created or
// destroyed, and each member is proposed the slot its address already implies.
func estateCompat(t *testing.T, tofuBin string) {
	dir := flocitest.CopyEstate(t)

	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")
	dropState(t, dir)

	// Sanity: the estate really does carry no slots, or nothing below means
	// anything.
	for _, e := range liveEIPs(t, estateName) {
		if e.slot != "" {
			t.Fatalf("the estate fixture's %s already carries a slot (%s); this test's premise is gone", e.id, e.slot)
		}
	}

	out := statelessPlan(t, tofuBin, dir)

	// The whole count set bound: no member of it is created, and nothing is
	// destroyed anywhere.
	for _, addr := range []string{"aws_eip.pool[0]", "aws_eip.pool[1]", "aws_eip.pool[2]"} {
		if strings.Contains(out, "# "+addr+" will be created") {
			t.Errorf("%s was planned as a create, so the compatibility path did not bind it:\n%s", addr, out)
		}
		if strings.Contains(out, "# "+addr+" will be destroyed") {
			t.Errorf("%s was planned as a destroy:\n%s", addr, out)
		}
	}
	if _, _, destroy, ok := flocitest.PlanSummary(out); ok && destroy != 0 {
		t.Errorf("the pre-slot estate plans %d destroy(s):\n%s", destroy, out)
	}

	// And each member is proposed the slot its address implies - the
	// one-time migration, visible in the diff exactly where an ownership
	// change should be visible.
	for i := 0; i < 3; i++ {
		block := flocitest.ResourceBlock(t, out, fmt.Sprintf("aws_eip.pool[%d]", i))
		if !tagIsSet(block, "tofu-slot", strconv.Itoa(i)) {
			t.Errorf("aws_eip.pool[%d] is not proposed slot %d:\n%s", i, i, block)
		}
		if extra := flocitest.NonTagChanges(block); len(extra) > 0 {
			t.Errorf("the slot migration on aws_eip.pool[%d] also changes %v:\n%s", i, extra, block)
		}
		if tagIsRewritten(block, "tofu-address") {
			t.Errorf("the slot migration on aws_eip.pool[%d] also rewrote its address:\n%s", i, block)
		}
	}
	t.Logf("estate fixture: count set bound by address, three slot markers proposed, no create and no destroy")
}

// ---------------------------------------------------------------------------
// 2. The slot lifecycle, on this test's own fixture
// ---------------------------------------------------------------------------

func slotLifecycle(t *testing.T, tofuBin string) {
	dir := writePoolFixture(t)

	// Stock terraform stands the set up carrying estate and address markers
	// and no slots - the state of the world one run before this feature.
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")
	dropState(t, dir)

	// --- Stamp the slots ------------------------------------------------
	out := statelessPlan(t, tofuBin, dir)
	if add, _, destroy, ok := flocitest.PlanSummary(out); ok && (add != 0 || destroy != 0) {
		t.Fatalf("the migration plan is not changes-only (%d add, %d destroy):\n%s", add, destroy, out)
	}
	migrated := map[string]string{}
	for i := 0; i < 3; i++ {
		block := flocitest.ResourceBlock(t, out, fmt.Sprintf("aws_eip.pool[%d]", i))
		slot := tagValue(t, block, "tofu-slot")
		if slot != strconv.Itoa(i) {
			t.Errorf("aws_eip.pool[%d] migrates to slot %q, want %d - the migration must not permute the set", i, slot, i)
		}
		migrated[fmt.Sprintf("aws_eip.pool:%d", i)] = slot
	}

	// Stand in for the apply this fork does not have yet: write the tags the
	// plan proposed, onto the resource whose address the plan bound.
	for _, e := range liveEIPs(t, slotsEstate) {
		slot, ok := migrated[e.address]
		if !ok {
			t.Fatalf("the plan says nothing about the live EIP %s at %s", e.id, e.address)
		}
		flocitest.AWSCLI(t, flociPort, "ec2", "create-tags", "--resources", e.id, "--tags", "Key=tofu-slot,Value="+slot)
	}

	slotted := liveEIPs(t, slotsEstate)
	if len(slotted) != 3 {
		t.Fatalf("want three live EIPs after the migration, got %d", len(slotted))
	}
	bySlot := map[string]string{}
	for _, e := range slotted {
		if e.slot == "" {
			t.Fatalf("%s did not take its slot tag", e.id)
		}
		bySlot[e.slot] = e.id
	}
	t.Logf("after migration: %v", bySlot)

	// --- The stamped estate plans clean ---------------------------------
	out = statelessPlan(t, tofuBin, dir)
	if !strings.Contains(out, "No changes.") {
		t.Errorf("a fully slotted count set does not plan clean:\n%s", out)
	}

	// --- Scale down: exactly one destroy, the highest slot, no churn ----
	out = statelessPlan(t, tofuBin, dir, "-var", "pool_size=2")
	add, change, destroy, ok := flocitest.PlanSummary(out)
	if !ok {
		t.Fatalf("no plan summary:\n%s", out)
	}
	if add != 0 || change != 0 || destroy != 1 {
		t.Errorf("scale-down plans %d to add, %d to change, %d to destroy; want 0/0/1:\n%s", add, change, destroy, out)
	}
	changed := flocitest.ChangedResources(out)
	if len(changed) != 1 || changed[0] != "aws_eip.pool[2]" {
		t.Errorf("scale-down touches %v, want only aws_eip.pool[2]:\n%s", changed, out)
	}
	doomed := bySlot["2"]
	if !strings.Contains(flocitest.ResourceBlock(t, out, "aws_eip.pool[2]"), doomed) {
		t.Errorf("the destroy is not the highest slot's allocation (%s):\n%s", doomed, out)
	}
	t.Logf("scale-down 3->2: one destroy, %s (slot 2), zero churn on the survivors", doomed)

	// --- Converge: release it, as an apply would -------------------------
	flocitest.AWSCLI(t, flociPort, "ec2", "release-address", "--allocation-id", doomed)

	out = statelessPlan(t, tofuBin, dir, "-var", "pool_size=2")
	if !strings.Contains(out, "No changes.") {
		t.Errorf("the scaled-down estate does not plan clean after the delete:\n%s", out)
	}

	// --- Scale up: two creates, each carrying a freshly minted slot ------
	out = statelessPlan(t, tofuBin, dir, "-var", "pool_size=4")
	add, change, destroy, ok = flocitest.PlanSummary(out)
	if !ok {
		t.Fatalf("no plan summary:\n%s", out)
	}
	if add != 2 || change != 0 || destroy != 0 {
		t.Errorf("scale-up plans %d to add, %d to change, %d to destroy; want 2/0/0:\n%s", add, change, destroy, out)
	}

	// The high-water mark is the highest slot the estate can still see, which
	// after the release is 1. Both mints are above it, and neither collides
	// with a slot a survivor holds.
	held := map[string]bool{}
	for _, e := range liveEIPs(t, slotsEstate) {
		held[e.slot] = true
	}
	var minted []int
	for _, addr := range []string{"aws_eip.pool[2]", "aws_eip.pool[3]"} {
		block := flocitest.ResourceBlock(t, out, addr)
		if !strings.Contains(block, "will be created") {
			t.Errorf("%s is not a create:\n%s", addr, block)
			continue
		}
		slot := tagValue(t, block, "tofu-slot")
		if slot == "" {
			t.Errorf("the create of %s carries no slot at all - it would be born unowned:\n%s", addr, block)
			continue
		}
		if held[slot] {
			t.Errorf("the create of %s was minted slot %s, which a live resource already holds", addr, slot)
		}
		n, err := strconv.Atoi(slot)
		if err != nil {
			t.Errorf("the create of %s carries a slot that is not a number: %q", addr, slot)
			continue
		}
		minted = append(minted, n)
	}
	if len(minted) == 2 && minted[0] == minted[1] {
		t.Errorf("both creates were minted the same slot %d", minted[0])
	}
	for _, n := range minted {
		if n <= 1 {
			t.Errorf("minted slot %d is not above the high-water mark of 1", n)
		}
	}
	t.Logf("scale-up 2->4: two creates carrying newly minted slots %v (live set held %v)", minted, keysOf(held))
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// poolFixture is a count set on its own, with markers written the way the
// P0.1 estate writes them and a cardinality this test can move. It is
// deliberately not the estate fixture: this task may not edit that one, and
// every claim here is about what happens when the count changes.
const poolFixture = `# Written by internal/live/slots's integration test.

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

variable "pool_size" {
  type    = number
  default = 3
}

# The fungible set. No argument distinguishes one member from another, which
# is what makes the slot marker the only thing that can.
resource "aws_eip" "pool" {
  count = var.pool_size

  domain = "vpc"

  tags = {
    tofu-estate  = "` + slotsEstate + `"
    tofu-address = "aws_eip.pool:${count.index}"
  }
}
`

func writePoolFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(poolFixture), 0o600); err != nil {
		t.Fatalf("writing the pool fixture: %v", err)
	}
	return dir
}

// dropState deletes the state file stock terraform left behind. The non-event
// is the demo: nothing else adopts the estate.
func dropState(t *testing.T, dir string) {
	t.Helper()

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")
}

// ---------------------------------------------------------------------------
// Reading the cloud
// ---------------------------------------------------------------------------

// liveEIP is one live elastic IP and the markers it carries.
type liveEIP struct {
	id      string
	address string
	slot    string
}

// liveEIPs lists an estate's elastic IPs with their markers, straight from the
// cloud rather than from anything this fork said about it.
//
// The estate filter is applied here rather than sent as --filters, because
// floci's ec2:DescribeAddresses ignores tag filters: asked for one estate it
// returns every address in the account, and the two estates in this file both
// have an aws_eip.pool:0. That is a gap in the emulator, not a claim about
// real EC2, and the whole reason ownership is decided by reading the tag off
// each resource rather than by trusting a filtered list to be a census.
func liveEIPs(t *testing.T, estate string) []liveEIP {
	t.Helper()

	out := flocitest.AWSCLI(t, flociPort, "ec2", "describe-addresses",
		"--query", "Addresses[].[AllocationId,"+
			"Tags[?Key=='tofu-estate']|[0].Value,"+
			"Tags[?Key=='tofu-address']|[0].Value,"+
			"Tags[?Key=='tofu-slot']|[0].Value]",
		"--output", "text")

	var eips []liveEIP
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || fields[1] != estate {
			continue
		}
		e := liveEIP{id: fields[0], address: fields[2]}
		if len(fields) > 3 && fields[3] != "None" {
			e.slot = fields[3]
		}
		eips = append(eips, e)
	}
	return eips
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Running the command and reading its output
// ---------------------------------------------------------------------------

func statelessPlan(t *testing.T, tofuBin, dir string, args ...string) string {
	t.Helper()

	full := append([]string{"live-plan", "-no-color", "-input=false"}, args...)
	start := time.Now()
	cmd := exec.Command(tofuBin, full...) //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("choudoufu %s took %s\n%s", strings.Join(full, " "), time.Since(start), output)
	if err != nil {
		t.Fatalf("live-plan failed: %v", err)
	}
	return output
}

// tagValue reads the value a diff sets a tag to, whatever the renderer's
// alignment padding is.
func tagValue(t *testing.T, block, key string) string {
	t.Helper()

	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*=\s*"([^"]*)"`)
	m := re.FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return m[1]
}

func tagIsSet(block, key, value string) bool {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*=\s*"` + regexp.QuoteMeta(value) + `"`)
	return re.MatchString(block)
}

func tagIsRewritten(block, key string) bool {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*=\s*"[^"]*"\s*->`)
	return re.MatchString(block)
}

// ---------------------------------------------------------------------------
// floci and the binary
// ---------------------------------------------------------------------------
