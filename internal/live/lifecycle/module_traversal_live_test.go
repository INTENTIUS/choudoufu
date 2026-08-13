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

// moduleEstate is live/e2e/estate-module/'s own naming: the estate a static
// module call's one resource is stamped into, and the module-qualified
// address its marker carries before the mv step moves it to the root.
const (
	moduleEstate     = "ec2-module-cohort"
	moduleAddrBefore = `module.wrapped.aws_eip.app`
	moduleAddrAfter  = `aws_eip.app`
)

// TestModuleTraversalAgainstFloci is 59b's headline proof, live: a
// static-module estate stands up, its marker on the wire is the
// module-qualified address (not the Go value some internal function
// returns - the tag actually written to floci, read back with the AWS CLI),
// a plain plan recovers it - which is also the discovery proof, since
// aws_eip is server-assigned and the second plan can only recover it by
// listing EIPs and matching this address against their tofu-address tags -
// and choudoufu live-mv crosses the module boundary, moving the resource's
// ownership from "module.wrapped.aws_eip.app" to "aws_eip.app" with an
// empty follow-up plan once the configuration is edited to match.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestModuleTraversalAgainstFloci -v
func TestModuleTraversalAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "module traversal")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	flociPort = flocitest.StartFloci(t, "cdf-59b")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+flociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := flocitest.CopyFixtureDir(t, flocitest.EstateModuleDir(t))

	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")
	assertNoState(t, dir, "after init")

	// --- 1. Stand up: the module's one resource is created ---------------

	apply := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(apply, "Apply complete!") {
		t.Fatalf("the apply did not complete:\n%s", apply)
	}
	added, changed, destroyed, ok := applySummary(apply)
	if !ok || added != 1 || changed != 0 || destroyed != 0 {
		t.Fatalf("want 1 added / 0 changed / 0 destroyed, got %d/%d/%d (ok=%v):\n%s", added, changed, destroyed, ok, apply)
	}
	assertNoState(t, dir, "after the apply")

	// --- 2. The marker on the wire is the module-qualified address -------
	//
	// Read with the AWS CLI, never with tofu: the claim under test is that
	// the tag value actually written to the emulator is the
	// module-qualified address string, not merely that some Go value inside
	// this run's own process looked right.

	eips := eipsByAllocation(t)
	if len(eips) != 1 {
		t.Fatalf("want exactly one EIP after standup, got %d: %v", len(eips), eips)
	}
	eipID, tags := oneEIP(t, eips)
	if got := tags["tofu-estate"]; got != moduleEstate {
		t.Errorf("EIP %s carries tofu-estate=%q live, want %q", eipID, got, moduleEstate)
	}
	if got := tags["tofu-address"]; got != moduleAddrBefore {
		t.Errorf("EIP %s carries tofu-address=%q live, want %q: the marker on the wire must literally be the module-qualified address, not the unqualified one", eipID, got, moduleAddrBefore)
	}

	// --- 3. A plain plan recovers the estate ------------------------------
	//
	// This is also the discovery proof: aws_eip's identity is server-assigned
	// (internal/live/identity/table.go), so nothing in configuration names
	// this instance and the only way a plan can recover it without proposing
	// a duplicate create is to list every EIP in the account and match this
	// one's tofu-address tag - "module.wrapped.aws_eip.app" - against the
	// address the static-module traversal computes for the declared block.
	// A dirty plan here would mean either the walkers or the marker on the
	// wire disagree about what that address is.

	clean := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(clean, "No changes.") {
		t.Errorf("the second plan did not recover the module-scoped estate (module traversal or marker discovery is broken):\n%s", clean)
	}
	if got := flocitest.ChangedResources(clean); len(got) > 0 {
		t.Errorf("a clean plan proposed changes to %v:\n%s", got, clean)
	}
	assertNoState(t, dir, "after a plain plan")

	// --- 4. live-mv crosses the module boundary ---------------------------
	//
	// -allow-missing-config: the destination address is not declared yet at
	// this point, only rewritten as a marker. The configuration edit that
	// makes it declared - moving the resource block from wrapped/ to the
	// root - follows below, the same "marker first, block second" ordering
	// live-mv's own package doc describes for a rename in general.

	mvOut := tofu(t, tofuBin, dir, "live-mv",
		"-estate="+moduleEstate, "-allow-missing-config",
		moduleAddrBefore, moduleAddrAfter)
	if !strings.Contains(mvOut, moduleAddrAfter) {
		t.Fatalf("live-mv did not report the new address %q:\n%s", moduleAddrAfter, mvOut)
	}

	eips = eipsByAllocation(t)
	if len(eips) != 1 {
		t.Fatalf("want exactly one EIP after live-mv, got %d: %v", len(eips), eips)
	}
	eipID, tags = oneEIP(t, eips)
	if got := tags["tofu-address"]; got != moduleAddrAfter {
		t.Errorf("EIP %s carries tofu-address=%q live after live-mv, want %q", eipID, got, moduleAddrAfter)
	}
	if got := tags["tofu-estate"]; got != moduleEstate {
		t.Errorf("EIP %s carries tofu-estate=%q live after live-mv, want %q (a rename must never change ownership)", eipID, got, moduleEstate)
	}

	// --- 5. Flatten the configuration to match ----------------------------
	//
	// The marker now names a root address the configuration does not
	// declare, so a plan at this instant would show the EIP as an orphan of
	// this estate (exactly what -allow-missing-config's own warning says)
	// rather than a clean recovery. Editing the block to match is the
	// second half of the rename, same as any other choudoufu live-mv.

	flattenConfig(t, dir)

	// --- 6. The follow-up plan is empty ------------------------------------

	after := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(after, "No changes.") {
		t.Errorf("the follow-up plan after flattening did not come back empty:\n%s", after)
	}
	if got := flocitest.ChangedResources(after); len(got) > 0 {
		t.Errorf("the follow-up plan proposed changes to %v:\n%s", got, after)
	}
	assertNoState(t, dir, "after the follow-up plan")
}

// oneEIP extracts the single entry of an allocation-ID-keyed EIP map,
// failing the test if there is not exactly one - every call site already
// checked len(eips) == 1, so this only exists to avoid writing the same
// "range over one entry" loop three times.
func oneEIP(t *testing.T, eips map[string]map[string]string) (string, map[string]string) {
	t.Helper()
	for id, tags := range eips {
		return id, tags
	}
	t.Fatal("oneEIP called with no entries")
	return "", nil
}

// flattenConfig moves the estate's one resource out of the "wrapped" static
// module and into the root: wrapped/ec2-module.tf is removed (the module
// call itself, and its now-unused locals.tf, are left in place - an empty
// static module is legal and beside the point of this step) and a new root
// file declares the same resource with no address-bearing arguments of its
// own, since stamping computes its tofu-address from where the block now
// lives and the live-mv step already rewrote the wire to match.
func flattenConfig(t *testing.T, dir string) {
	t.Helper()

	moduleFile := filepath.Join(dir, "wrapped", "ec2-module.tf")
	if err := os.Remove(moduleFile); err != nil {
		t.Fatalf("removing %s: %v", moduleFile, err)
	}

	const flattened = `# Moved here by the live-mv step of TestModuleTraversalAgainstFloci: this
# was module.wrapped.aws_eip.app, and live-mv already rewrote the live
# resource's marker to name it as aws_eip.app instead. No tags argument is
# written by hand - stamping computes tofu-estate/tofu-address for wherever
# a block lives, and the live object already carries the values that
# computation will produce.
resource "aws_eip" "app" {}
`
	if err := os.WriteFile(filepath.Join(dir, "flattened.tf"), []byte(flattened), 0o600); err != nil {
		t.Fatalf("writing flattened.tf: %v", err)
	}
}
