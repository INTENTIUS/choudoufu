// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// keyedModuleEstate is live/e2e/estate-module-keyed/'s own naming: the
// estate a for_each-expanded module call's two instances are stamped into,
// and the module-qualified, per-instance addresses their markers carry.
const (
	keyedModuleEstate = "ec2-module-keyed-cohort"
	keyedAddrA        = `module.wrapped["a"].aws_eip.app`
	keyedAddrB        = `module.wrapped["b"].aws_eip.app`
)

// TestModuleKeyedForEachAgainstFloci is 59c's headline proof, live: a
// module call expanded with for_each over two string keys stands up as two
// live resources, each marker on the wire is the exact keyed address the
// static walkers computed - not merely a Go value this run's own process
// believes, but the tag actually written to floci and read back with the
// AWS CLI - a plain plan recovers both (also the discovery proof, since
// aws_eip is server-assigned), and removing one key from the module call's
// for_each proposes destroying exactly that key's instance and nothing for
// its sibling: the sibling-stability property that is the entire reason a
// keyed module is worth admitting over a counted one (live/LIMITATIONS.md,
// "child-module" - a for_each key does not renumber the way a count index
// does).
//
//	TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestModuleKeyedForEachAgainstFloci -v
func TestModuleKeyedForEachAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "keyed module for_each")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	flociPort = flocitest.StartFloci(t, "cdf-59c")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+flociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := flocitest.CopyFixtureDir(t, flocitest.EstateModuleKeyedDir(t))

	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")
	assertNoState(t, dir, "after init")

	// --- 1. Stand up: both keyed instances are created --------------------

	apply := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(apply, "Apply complete!") {
		t.Fatalf("the apply did not complete:\n%s", apply)
	}
	added, changed, destroyed, ok := applySummary(apply)
	if !ok || added != 2 || changed != 0 || destroyed != 0 {
		t.Fatalf("want 2 added / 0 changed / 0 destroyed, got %d/%d/%d (ok=%v):\n%s", added, changed, destroyed, ok, apply)
	}
	assertNoState(t, dir, "after the apply")

	// --- 2. Both markers on the wire are the keyed, module-qualified
	//        addresses -------------------------------------------------
	//
	// Read with the AWS CLI, never with tofu: the claim under test is that
	// the tag values actually written to the emulator are the keyed
	// addresses, one per instance, not merely that some Go value inside
	// this run's own process looked right.

	eips := eipsByAllocation(t)
	if len(eips) != 2 {
		t.Fatalf("want exactly two EIPs after standup, got %d: %v", len(eips), eips)
	}
	gotAddrs := make(map[string]bool, 2)
	for eipID, tags := range eips {
		if got := tags["tofu-estate"]; got != keyedModuleEstate {
			t.Errorf("EIP %s carries tofu-estate=%q live, want %q", eipID, got, keyedModuleEstate)
		}
		gotAddrs[tags["tofu-address"]] = true
	}
	for _, want := range []string{keyedAddrA, keyedAddrB} {
		if !gotAddrs[want] {
			t.Errorf("no live EIP carries tofu-address=%q; got %v", want, gotAddrs)
		}
	}

	// --- 3. A plain plan recovers both instances of the estate ------------
	//
	// This is also the discovery proof: aws_eip's identity is
	// server-assigned (internal/live/identity/table.go), so nothing in
	// configuration names either instance and the only way a plan can
	// recover both without proposing a duplicate create is to list every
	// EIP in the account and match each one's tofu-address tag against the
	// keyed addresses the module traversal computes for the declared
	// block's two instances.

	clean := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(clean, "No changes.") {
		t.Errorf("the second plan did not recover the keyed-module estate (keyed module traversal or marker discovery is broken):\n%s", clean)
	}
	if got := flocitest.ChangedResources(clean); len(got) > 0 {
		t.Errorf("a clean plan proposed changes to %v:\n%s", got, clean)
	}
	assertNoState(t, dir, "after a plain plan")

	// --- 4. The sibling-stability proof: removing key "a" proposes
	//        destroying exactly that instance, nothing for "b" -------------

	removeKeyA(t, dir)

	afterRemoval := tofu(t, tofuBin, dir, "plan")
	changedAfterRemoval := flocitest.ChangedResources(afterRemoval)
	if len(changedAfterRemoval) != 1 || changedAfterRemoval[0] != keyedAddrA {
		t.Fatalf("removing key \"a\" from the for_each should propose changes to exactly [%s], got %v:\n%s",
			keyedAddrA, changedAfterRemoval, afterRemoval)
	}
	if !strings.Contains(afterRemoval, "1 to destroy") {
		t.Errorf("the plan after removing key \"a\" does not propose exactly one destroy:\n%s", afterRemoval)
	}
	assertNoState(t, dir, "after the sibling-stability plan")

	// --- 5. Apply it: only "a"'s live resource is destroyed ----------------

	removalApply := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(removalApply, "Apply complete!") {
		t.Fatalf("the removal apply did not complete:\n%s", removalApply)
	}
	rAdded, rChanged, rDestroyed, rOK := applySummary(removalApply)
	if !rOK || rAdded != 0 || rChanged != 0 || rDestroyed != 1 {
		t.Fatalf("want 0 added / 0 changed / 1 destroyed, got %d/%d/%d (ok=%v):\n%s", rAdded, rChanged, rDestroyed, rOK, removalApply)
	}
	assertNoState(t, dir, "after the removal apply")

	eips = eipsByAllocation(t)
	if len(eips) != 1 {
		t.Fatalf("want exactly one EIP after removing key \"a\", got %d: %v", len(eips), eips)
	}
	eipID, tags := oneEIP(t, eips)
	if got := tags["tofu-address"]; got != keyedAddrB {
		t.Errorf("the surviving EIP %s carries tofu-address=%q live, want %q: removing key \"a\" must not disturb its sibling", eipID, got, keyedAddrB)
	}
	if got := tags["tofu-estate"]; got != keyedModuleEstate {
		t.Errorf("the surviving EIP %s carries tofu-estate=%q live, want %q", eipID, got, keyedModuleEstate)
	}

	// --- 6. The follow-up plan is empty ------------------------------------

	after := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(after, "No changes.") {
		t.Errorf("the follow-up plan after removing key \"a\" did not come back empty:\n%s", after)
	}
	if got := flocitest.ChangedResources(after); len(got) > 0 {
		t.Errorf("the follow-up plan proposed changes to %v:\n%s", got, after)
	}
	assertNoState(t, dir, "after the follow-up plan")
}

// removeKeyA edits the keyed module call's for_each down to just "b",
// leaving everything else - the wrapped module's own resource block, its
// variables.tf, the estate's provider wiring - untouched. This is the
// sibling-stability scenario's whole setup: the resource block inside the
// module never changes, only which keys the module call's for_each
// produces, which is exactly what tells apart a for_each key's address from
// a count index's position (live/LIMITATIONS.md, "child-module").
func removeKeyA(t *testing.T, dir string) {
	t.Helper()

	mainFile := filepath.Join(dir, "main.tf")
	data, err := os.ReadFile(mainFile) //nolint:gosec // a fixed path under this test's own temp dir
	if err != nil {
		t.Fatalf("reading %s: %v", mainFile, err)
	}
	edited := strings.Replace(string(data), `toset(["a", "b"])`, `toset(["b"])`, 1)
	if edited == string(data) {
		t.Fatalf("did not find the expected for_each literal in %s:\n%s", mainFile, data)
	}
	if err := os.WriteFile(mainFile, []byte(edited), 0o600); err != nil {
		t.Fatalf("writing %s: %v", mainFile, err)
	}
}
