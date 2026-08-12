// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// The count set through the whole command: discovery matches the live members
// against the declared count, stamping writes the slot assignment into the
// configuration, and the ordinary plan engine does the rest. These are the
// unit-level proof of the harness step P3.5 wires up.

const countEstate = "stateless-count"

// TestLivePlan_countBoundBySlotPlansClean: three declared, three live,
// each carrying its slot. Everything binds and nothing changes - including
// the markers, which the live resources already carry exactly as this run
// would stamp them.
func TestLivePlan_countBoundBySlotPlansClean(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-count"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	statelessSlottedEIP(cloud, "eipalloc-a", "0", "aws_eip.pool:0")
	statelessSlottedEIP(cloud, "eipalloc-b", "1", "aws_eip.pool:1")
	statelessSlottedEIP(cloud, "eipalloc-c", "2", "aws_eip.pool:2")

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=" + countEstate})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("a fully bound count set did not plan clean:\n%s", stdout)
	}
	if strings.Contains(stdout, "Not read from the live system") {
		t.Errorf("an instance of the set was omitted from the projection:\n%s", stdout)
	}
	// Every one of the three was read back, which is what says the binding
	// covered the whole set rather than one lucky member.
	for _, id := range []string{"eipalloc-a", "eipalloc-b", "eipalloc-c"} {
		if !cloud.imported("aws_eip", id) {
			t.Errorf("%s was never read from the live system; imports were %v", id, cloud.imports)
		}
	}
}

// TestLivePlan_countScaleDownDestroysTheHighestSlot is the P3.5
// acceptance: 3 live, count = 2, exactly one destroy and it is the highest
// slot, with nothing at all proposed for the survivors.
func TestLivePlan_countScaleDownDestroysTheHighestSlot(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-count"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	statelessSlottedEIP(cloud, "eipalloc-a", "0", "aws_eip.pool:0")
	statelessSlottedEIP(cloud, "eipalloc-b", "1", "aws_eip.pool:1")
	statelessSlottedEIP(cloud, "eipalloc-c", "2", "aws_eip.pool:2")

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=" + countEstate, "-var", "pool_size=2", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (a destroy is proposed)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "0 to add, 0 to change, 1 to destroy") {
		t.Errorf("the plan summary is not a single destroy:\n%s", stdout)
	}

	// The one destroy is the instance address just past the declared count -
	// the same place a stock run's shrunken count puts its leftover.
	changed := statelessChangedResources(stdout)
	if len(changed) != 1 {
		t.Fatalf("the plan touches %v, want only aws_eip.pool[2]:\n%s", changed, stdout)
	}
	if changed[0] != "aws_eip.pool[2] will be destroyed" {
		t.Errorf("the one change is %q, want aws_eip.pool[2] destroyed", changed[0])
	}

	// And it is the highest slot's resource, not merely something at index 2.
	if !strings.Contains(stdout, "eipalloc-c") {
		t.Errorf("the destroy does not name the highest slot's resource:\n%s", stdout)
	}
	for _, id := range []string{"eipalloc-a", "eipalloc-b"} {
		if strings.Contains(stdout, id+`" -> null`) {
			t.Errorf("a survivor (%s) appears on a removal line:\n%s", id, stdout)
		}
	}
}

// TestLivePlan_countScaleUpMintsSlots: two live, count = 4, so two
// creates - and the created instances carry slots above the high-water mark,
// stamped into the plan rather than left for a later run to guess.
func TestLivePlan_countScaleUpMintsSlots(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-count"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	statelessSlottedEIP(cloud, "eipalloc-a", "0", "aws_eip.pool:0")
	statelessSlottedEIP(cloud, "eipalloc-b", "3", "aws_eip.pool:1")

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=" + countEstate, "-var", "pool_size=4", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "2 to add, 0 to change, 0 to destroy") {
		t.Errorf("the plan summary is not two creates:\n%s", stdout)
	}
	// One past the highest live slot, and upward: never 1 or 2, which are
	// free only in the sense that nothing live is holding them.
	for _, want := range []string{"4", "5"} {
		if !statelessTagIsSet(stdout, "tofu-slot", want) {
			t.Errorf("the plan does not show a create carrying tofu-slot %s:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"1", "2"} {
		if statelessTagIsSet(stdout, "tofu-slot", unwanted) {
			t.Errorf("a minted slot reused the value %s, below the high-water mark:\n%s", unwanted, stdout)
		}
	}
}

// TestLivePlan_countWithoutSlotsMigrates is the compatibility path end to
// end: an estate whose count set carries per-instance addresses and no slots
// binds by address, and the only thing the plan proposes is the slot tag each
// member is missing, at the assignment its address already implies.
func TestLivePlan_countWithoutSlotsMigrates(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-count"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	for i, id := range []string{"eipalloc-a", "eipalloc-b", "eipalloc-c"} {
		statelessMarkedEIP(cloud, id, fmt.Sprintf("aws_eip.pool:%d", i))
	}

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=" + countEstate, "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (the slot markers are proposed)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "0 to add, 3 to change, 0 to destroy") {
		t.Errorf("the migration is not three in-place updates:\n%s", stdout)
	}
	for i := 0; i < 3; i++ {
		if !statelessTagIsSet(stdout, "tofu-slot", fmt.Sprintf("%d", i)) {
			t.Errorf("instance %d is not proposed the slot its address implies:\n%s", i, stdout)
		}
	}
	// The migration moves nothing else: no create, no destroy, and no
	// address rewritten.
	if strings.Contains(stdout, "will be created") || strings.Contains(stdout, "will be destroyed") {
		t.Errorf("the slot migration proposed a create or destroy:\n%s", stdout)
	}
	if statelessTagIsRewritten(stdout, "tofu-address") {
		t.Errorf("the slot migration also rewrote an address:\n%s", stdout)
	}
}

// TestLivePlan_countStaleAddressIsRepaired is the stale-address decision,
// live: after the lowest slot is destroyed out of band the survivors' address
// tags name indices they no longer hold. The slots bind them, so nothing is
// created or destroyed, and the plan proposes writing the addresses back.
func TestLivePlan_countStaleAddressIsRepaired(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-count"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	statelessSlottedEIP(cloud, "eipalloc-b", "1", "aws_eip.pool:1")
	statelessSlottedEIP(cloud, "eipalloc-c", "2", "aws_eip.pool:2")

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=" + countEstate, "-var", "pool_size=2", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (the stale addresses are repaired)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	// Two in-place updates and nothing else: the resources are bound, so the
	// only thing wrong with them is what their address tags say.
	if !strings.Contains(stdout, "0 to add, 2 to change, 0 to destroy") {
		t.Errorf("the repair is not two in-place updates:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"aws_eip.pool:0"`) {
		t.Errorf("the plan does not write the bound index back into an address tag:\n%s", stdout)
	}
	// The slots themselves do not move: a rebinding is not a renaming of the
	// set's members.
	if statelessTagIsRewritten(stdout, "tofu-slot") {
		t.Errorf("the repair rewrote a slot, which would rename the set's members:\n%s", stdout)
	}
}

// TestLivePlan_countMixedSlotsIsFatal: half the set stamped and half not
// is two answers to which member is which, and the run stops on it rather
// than binding by the half it likes better.
func TestLivePlan_countMixedSlotsIsFatal(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-count"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	statelessSlottedEIP(cloud, "eipalloc-a", "0", "aws_eip.pool:0")
	statelessMarkedEIP(cloud, "eipalloc-b", "aws_eip.pool:1")
	statelessMarkedEIP(cloud, "eipalloc-c", "aws_eip.pool:2")

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=" + countEstate})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Partial slot markers on a count set") {
		t.Errorf("no diagnostic naming the condition:\n%s", stderr)
	}
	if !strings.Contains(stderr, "eipalloc-a") || !strings.Contains(stderr, "eipalloc-b") {
		t.Errorf("the diagnostic does not name both sides:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// statelessSlottedEIP adds a live EIP that both lists (so discovery can read
// its markers) and reads back (so the projection can materialize it), carrying
// all three markers.
func statelessSlottedEIP(c *statelessTestCloud, id, slot, address string) {
	statelessEIP(c, id, map[string]string{
		"tofu-estate":  countEstate,
		"tofu-address": address,
		"tofu-slot":    slot,
	})
}

// statelessMarkedEIP is the same for a member of a pre-slot estate: estate and
// address markers, no slot.
func statelessMarkedEIP(c *statelessTestCloud, id, address string) {
	statelessEIP(c, id, map[string]string{
		"tofu-estate":  countEstate,
		"tofu-address": address,
	})
}

func statelessEIP(c *statelessTestCloud, id string, tags map[string]string) {
	attrs := map[string]string{"id": id, "domain": "vpc"}
	c.put("aws_eip", id, attrs)
	c.tags["aws_eip/"+id] = tags
	c.list("aws_eip", id, "", tags, attrs)
}

var statelessChangeHeader = regexp.MustCompile(`^\s*# (.+ will be .+)$`)

// statelessTagIsSet reports whether the plan output shows a tag being set to
// a value, whatever the renderer's alignment padding happens to be.
func statelessTagIsSet(output, key, value string) bool {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*=\s*"` + regexp.QuoteMeta(value) + `"`)
	return re.MatchString(output)
}

// statelessTagIsRewritten reports whether a tag appears on a line proposing a
// change from one value to another.
func statelessTagIsRewritten(output, key string) bool {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*=\s*"[^"]*"\s*->`)
	return re.MatchString(output)
}

// statelessChangedResources lists the renderer's per-resource headers, which
// is how "the plan touches exactly these" is asserted without matching on the
// whole diff.
func statelessChangedResources(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		if m := statelessChangeHeader.FindStringSubmatch(line); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}
