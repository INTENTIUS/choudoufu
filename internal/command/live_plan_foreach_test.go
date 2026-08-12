// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"
)

// The keyed set through the whole command. Binding a for_each member by its
// marker is P2.3's; what these cover is the surface P3.2 adds on top of it -
// what a run says when a key in the configuration changes and the live
// resource is still marked with the old one.

const foreachEstate = "stateless-subnets"

// TestLivePlan_foreachBindsByKey is the baseline the rename case is a
// deviation from: two declared keys, two live subnets marked with those keys,
// everything binds and nothing changes.
func TestLivePlan_foreachBindsByKey(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-foreach"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	statelessMarkedSubnet(cloud, "subnet-a", "aws_subnet.this:a", "10.42.1.0/24")
	statelessMarkedSubnet(cloud, "subnet-b", "aws_subnet.this:b", "10.42.2.0/24")

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=" + foreachEstate})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("a fully bound keyed set did not plan clean:\n%s", stdout)
	}
	if strings.Contains(stdout, "Renamed keys?") {
		t.Errorf("a run with nothing renamed printed the rename section:\n%s", stdout)
	}
}

// TestLivePlan_foreachRenamedKeyIsOffered is P3.2's acceptance: a key
// changed in configuration, the live resource still carries the old one, and
// the run says so - with the exact live-mv command - while planning
// exactly what it planned before.
func TestLivePlan_foreachRenamedKeyIsOffered(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-foreach"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	// The live estate as the fixture's default map created it.
	statelessMarkedSubnet(cloud, "subnet-a", "aws_subnet.this:a", "10.42.1.0/24")
	statelessMarkedSubnet(cloud, "subnet-b", "aws_subnet.this:b", "10.42.2.0/24")

	c, done := newLivePlanCommand(t, cloud)

	// The configuration edit: key "b" is now key "c", same subnet, same CIDR.
	// "a" is untouched, and is here to prove a bound member is not dragged
	// into the pairing.
	code := c.Run([]string{
		"-no-color", "-estate=" + foreachEstate, "-detailed-exitcode",
		"-var", `subnets={a="10.42.1.0/24",c="10.42.2.0/24"}`,
	})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (the new key is a create)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	// The hint: both addresses, the live ID, and a command that runs as
	// written.
	if !strings.Contains(stdout, "Renamed keys?") {
		t.Fatalf("no rename section:\n%s", stdout)
	}
	if !strings.Contains(stdout, `aws_subnet.this["b"] (live subnet-b) -> aws_subnet.this["c"]`) {
		t.Errorf("the rename line does not name both sides and the live resource:\n%s", stdout)
	}
	wantCmd := `choudoufu live-mv 'aws_subnet.this["b"]' 'aws_subnet.this["c"]'`
	if !strings.Contains(stdout, wantCmd) {
		t.Errorf("the exact command is missing:\n  want %s\n%s", wantCmd, stdout)
	}
	// The bound member is nobody's rename candidate.
	if strings.Contains(stdout, `aws_subnet.this["a"] (live`) {
		t.Errorf("a bound instance was offered as a rename:\n%s", stdout)
	}

	// Offered, never applied: the plan is exactly what it would have been
	// without the hint - one create for the new key, no destroy, and nothing
	// at all for the instance that bound.
	if !strings.Contains(stdout, "1 to add, 0 to change, 0 to destroy") {
		t.Errorf("the plan is not a single create:\n%s", stdout)
	}
	changed := statelessChangedResources(stdout)
	if len(changed) != 1 || changed[0] != `aws_subnet.this["c"] will be created` {
		t.Errorf("the plan touches %v, want only the new key created:\n%s", changed, stdout)
	}
	// And the live resource behind the old key is still there, unclaimed by
	// any declared address and untouched by the plan.
	if strings.Contains(stdout, "subnet-b\" -> null") {
		t.Errorf("the renamed resource appears on a removal line:\n%s", stdout)
	}
}

// TestLivePlan_foreachAmbiguousRenameOffersNothing: two keys gone and two
// new ones is a rename this run cannot resolve. It says which resources and
// which addresses are involved, and offers no command at all.
func TestLivePlan_foreachAmbiguousRenameOffersNothing(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-foreach"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	statelessMarkedSubnet(cloud, "subnet-a", "aws_subnet.this:a", "10.42.1.0/24")
	statelessMarkedSubnet(cloud, "subnet-b", "aws_subnet.this:b", "10.42.2.0/24")

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{
		"-no-color", "-estate=" + foreachEstate, "-detailed-exitcode",
		"-var", `subnets={c="10.42.1.0/24",d="10.42.2.0/24"}`,
	})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "Renamed keys?") || !strings.Contains(stdout, "cannot be paired") {
		t.Fatalf("the ambiguity was not reported:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_subnet.this [AMBIGUOUS]") {
		t.Errorf("the ambiguous block is not named:\n%s", stdout)
	}
	for _, want := range []string{"subnet-a", "subnet-b", `aws_subnet.this["c"]`, `aws_subnet.this["d"]`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the ambiguity does not name %s:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "live-mv '") {
		t.Errorf("a command was printed for a pairing this run cannot make:\n%s", stdout)
	}
	if !strings.Contains(stdout, "2 to add, 0 to change, 0 to destroy") {
		t.Errorf("the plan is not two creates:\n%s", stdout)
	}
}

// statelessMarkedSubnet adds a live subnet that lists (so discovery reads its
// markers) and reads back (so the projection can materialize it), carrying the
// estate and address markers exactly as a stamped run wrote them.
func statelessMarkedSubnet(c *statelessTestCloud, id, address, cidr string) {
	tags := map[string]string{
		"tofu-estate":  foreachEstate,
		"tofu-address": address,
	}
	attrs := map[string]string{"id": id, "cidr_block": cidr}
	c.put("aws_subnet", id, attrs)
	c.tags["aws_subnet/"+id] = tags
	c.list("aws_subnet", id, "", tags, attrs)
}
