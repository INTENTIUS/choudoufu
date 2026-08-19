// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestClassifyOrphans_countedModuleStepKeepsItsIndex is the reachability half
// of the [markers.UnescapeAddress] module-step fix: the proof that a wrong
// module instance key is not a property of a pure function nobody calls with
// a count'd module address, but of the address removal planning prints and
// plans a destroy at.
//
// The chain, all of it production code:
//
//   - internal/live/stamp writes "module.counted[0].aws_vpc.x" for a module
//     call with count = 1 (issue #195 admitted the call;
//     live/LIMITATIONS.md's "child-module" and
//     internal/live/stamp/modulecontext_test.go pin the address). It escapes
//     to "module.counted:0.aws_vpc.x".
//   - When that resource stops being declared - the block deleted from the
//     child module, the whole call deleted, or the "count = var.enabled ? 1
//     : 0" idiom flipped off - the sweep finds the live object by its
//     tofu-estate tag and it lands in Result.Orphans.
//   - [classifyOrphans] calls UnescapeAddress on the marker and puts the
//     result in [identity.Resolution.Addr], which is the address the
//     synthetic prior-state instance enters at and the address every removal
//     line prints.
//
// Before the fix that address was module.counted["0"].aws_vpc.x - a string
// key where the stamp wrote an index, and an address the configuration never
// had.
//
// The expected module path is taken from identity.Resolve over the same
// configuration rather than spelled here, for the reason
// TestCountBlockWalkNamesTheSameModuleInstancesResolutionDoes gives: two
// hand-written spellings cannot notice the two sides drifting apart.
func TestClassifyOrphans_countedModuleStepKeepsItsIndex(t *testing.T) {
	cfg := loadModuleConfig(t, filepath.Join("testdata", "counted-module-orphan"))

	res, diags := identity.Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("identity resolution failed: %s", diags.Err())
	}

	// The module instance resolution says the fixture's one resource lives
	// in. Everything about this test's expectation hangs off it.
	var modInst addrs.ModuleInstance
	found := 0
	for _, r := range res.All() {
		if r.Addr.Resource.Resource.Type != "aws_vpc" {
			continue
		}
		modInst = r.Addr.Module
		found++
	}
	if found != 1 {
		t.Fatalf("the fixture resolved %d aws_vpc instances, want exactly 1", found)
	}
	if len(modInst) != 1 {
		t.Fatalf("resolution puts the fixture's resource at module path %s, want one module step", modInst)
	}
	if _, isInt := modInst[0].InstanceKey.(addrs.IntKey); !isInt {
		t.Fatalf("resolution keys the count'd module call with %T; the fixture is meant to be the count = 1 shape issue #195 admitted", modInst[0].InstanceKey)
	}

	// The orphan: a resource of the same module instance whose block is gone
	// from the child module, carrying the marker a prior apply stamped.
	gone := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "aws_vpc",
		Name: "gone",
	}.Instance(addrs.NoKey).Absolute(modInst)
	marker := EscapeAddress(gone.String())

	result := &Result{
		Orphans: []OwnedResource{{
			TypeName:   "aws_vpc",
			ImportID:   "vpc-0deadbeef",
			Marker:     marker,
			Normalized: marker,
			Swept:      true,
		}},
	}

	req := Request{Estate: "counted-orphan", Config: cfg}
	if d := classifyOrphans(req, result); d.HasErrors() {
		t.Fatalf("classifyOrphans reported errors for an ordinary deleted-block orphan: %s", d.Err())
	}

	o := result.Orphans[0]
	if !o.Addressable {
		t.Fatalf("the marker %q was not addressable at all", marker)
	}
	if !o.Removal {
		t.Fatalf("the orphan was withheld from removal (%q); this test needs it to reach the resolution list", o.Withheld)
	}
	if got := o.Addr.String(); got != gone.String() {
		t.Errorf("marker %q classified at %s, want %s - the destroy is labelled and entered in the prior state at an address the configuration never had",
			marker, got, gone)
	}

	if len(result.Resolutions) != 1 {
		t.Fatalf("classifyOrphans produced %d resolutions, want 1", len(result.Resolutions))
	}
	r := result.Resolutions[0]
	if got := r.Addr.String(); got != gone.String() {
		t.Errorf("the removal resolution is at %s, want %s", got, gone)
	}
	if _, isInt := r.Addr.Module[0].InstanceKey.(addrs.IntKey); !isInt {
		t.Errorf("the removal resolution's module step is keyed %T, want addrs.IntKey - the rendered address can agree while the key type does not, and a module expansion is matched on the key type",
			r.Addr.Module[0].InstanceKey)
	}
	if !r.Undeclared {
		t.Error("the removal resolution is not marked Undeclared, but its block is gone from the child module")
	}

	// And [Result.MarkerVerified], which the projection reads to decide which
	// resolutions already carry proof of ownership, has to name the same
	// address: it keys on the same o.Addr, so a divergence here would mean
	// the two sides disagree about which string this orphan is.
	if !result.MarkerVerified()[gone.String()] {
		t.Errorf("MarkerVerified does not contain %s: %v", gone, result.MarkerVerified())
	}
}
