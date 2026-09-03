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
	"github.com/intentius/choudoufu/internal/live/listclient"
)

// renameCase is one module path the same re-keyed-block shape is driven
// through: the declared instance nothing claimed, and the live orphan whose
// marker names the key that block used to have.
type renameCase struct {
	label    string
	declared addrs.AbsResourceInstance
	orphan   addrs.AbsResourceInstance
	marker   string
}

// moduleRenameCases builds the three cases from the fixture's own resolved
// instances rather than from hand-written address strings, for the reason
// TestClassifyOrphans_countedModuleStepKeepsItsIndex gives: two hand-written
// spellings cannot notice the two sides drifting apart.
func moduleRenameCases(t *testing.T) (*renameCase, *renameCase, *renameCase, []addrs.AbsResourceInstance) {
	t.Helper()

	cfg := loadModuleConfig(t, filepath.Join("testdata", "module-rename-withhold"))

	res, diags := identity.Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("identity resolution failed: %s", diags.Err())
	}

	var declared []addrs.AbsResourceInstance
	for _, r := range res.All() {
		if r.Addr.Resource.Resource.Type != "aws_subnet" {
			continue
		}
		declared = append(declared, r.Addr)
	}
	if len(declared) != 3 {
		t.Fatalf("the fixture resolved %d aws_subnet instances, want exactly 3 (root, module.net, module.counted[0]): %v", len(declared), declared)
	}

	var root, static, counted *renameCase
	for _, addr := range declared {
		// The orphan is the same block at the key the configuration no longer
		// declares: the whole shape of a for_each rename.
		old := addr.Resource.Resource.Instance(addrs.StringKey("a")).Absolute(addr.Module)
		c := &renameCase{
			declared: addr,
			orphan:   old,
			marker:   EscapeAddress(old.String()),
		}
		switch {
		case addr.Module.IsRoot():
			c.label = "root module"
			root = c
		case addr.Module[0].InstanceKey == addrs.NoKey:
			c.label = "static module call"
			static = c
		default:
			c.label = "count'd module call"
			counted = c
		}
	}
	if root == nil || static == nil || counted == nil {
		t.Fatalf("the fixture did not produce one instance per module path: %v", declared)
	}
	return root, static, counted, declared
}

// TestClassifyOrphans_renameIsWithheldAtEveryModulePath is issue #316.
//
// [classifyOrphans]'s first check is the one its own doc comment calls the
// whole safety property: an orphan sitting in a resource block that still has
// an unclaimed declared instance is a possible rename, and a rename must
// never be planned as a destroy. Getting it wrong the other way destroys and
// recreates a live resource nobody asked to touch.
//
// The check compared two strings that could only ever be equal for a
// root-module address. The declared side dropped the module path outright
// (addr.Resource.Resource.String() on an [addrs.AbsResourceInstance] is
// "aws_subnet.this", module path and all), while the read side cut the
// escaped marker at its FIRST ":", which in "module.counted:0.aws_subnet.
// this:a" is the module step's own count index. So the guard fired for a
// re-keyed root resource and for nothing inside any module.
//
// The property this pins is parity: the same shape, at three module paths,
// has to get the same answer.
func TestClassifyOrphans_renameIsWithheldAtEveryModulePath(t *testing.T) {
	root, static, counted, declared := moduleRenameCases(t)

	for _, c := range []*renameCase{root, static, counted} {
		t.Run(c.label, func(t *testing.T) {
			result := &Result{Verdicts: Verdicts{Orphans: []OwnedResource{{
				TypeName:   "aws_subnet",
				ImportID:   "subnet-0deadbeef",
				Marker:     c.marker,
				Normalized: c.marker,
				Swept:      true,
			}}}, Report: Report{Unbound: declared}}

			diags := classifyOrphans(t.Context(), Request{Estate: "rename-withhold", Config: nil}, listclient.Schemas{}, result)

			o := result.Orphans[0]
			if o.Removal {
				t.Errorf("the live resource marked %q was proposed for destruction while %s is unclaimed.\n"+
					"That is a destroy-and-recreate of a resource whose key was merely renamed.",
					c.marker, c.declared)
			}
			if o.Withheld == "" {
				t.Errorf("no withholding reason was recorded for %q; the guard did not fire at all", c.marker)
			}
			if len(result.Resolutions) != 0 {
				t.Errorf("a removal resolution was produced for %q: %v", c.marker, result.Resolutions)
			}
			if diags.HasErrors() {
				t.Errorf("classifying an ordinary re-keyed orphan reported errors: %s", diags.Err())
			}
		})
	}
}

// TestClassifyOrphans_anUndecodableMarkerStillReachesTheGuard pins
// [orphanBlockKey]'s fallback, which exists for compatibility rather than for
// correctness and would otherwise be the kind of branch a later reader
// deletes as dead.
//
// Withholding runs BEFORE the malformed-marker report on purpose, so a
// corrupt tag value sitting in a block that still has an unclaimed instance
// is withheld silently and no error is raised for it. "aws_subnet.this:" - a
// truncated marker, an instance key introduced and then not written - is
// exactly that: [UnescapeAddress] refuses it, and the text-level cut still
// finds the block it belongs to. Reading the block off the decoded address
// alone would turn that silence into a hard error on an estate that plans
// cleanly today.
func TestClassifyOrphans_anUndecodableMarkerStillReachesTheGuard(t *testing.T) {
	root, _, _, _ := moduleRenameCases(t)

	const corrupt = "aws_subnet.this:"
	if _, ok := UnescapeAddress(corrupt); ok {
		t.Fatalf("%q decodes to an address, so this test is not exercising the fallback at all", corrupt)
	}

	result := &Result{Verdicts: Verdicts{Orphans: []OwnedResource{{
		TypeName:   "aws_subnet",
		ImportID:   "subnet-0deadbeef",
		Marker:     corrupt,
		Normalized: corrupt,
		Swept:      true,
	}}}, Report: Report{Unbound: []addrs.AbsResourceInstance{root.declared}}}

	diags := classifyOrphans(t.Context(), Request{Estate: "rename-withhold"}, listclient.Schemas{}, result)

	if diags.HasErrors() {
		t.Errorf("a corrupt marker in a block with an unclaimed declared instance raised an error: %s", diags.Err())
	}
	o := result.Orphans[0]
	if o.Removal {
		t.Errorf("the live resource marked %q was proposed for destruction", corrupt)
	}
	if o.Withheld == "" {
		t.Errorf("no withholding reason was recorded for %q", corrupt)
	}
}

// TestClassifyOrphans_aBlockMovedAcrossModulesStaysWithheld pins the one
// judgement in [blockKey]: the key is the block's type and name with the
// module path taken off, so a live resource whose block moved between module
// paths is withheld rather than destroyed.
//
// The root-to-module direction is not new - it is what the guard did before
// issue #316, by accident of dropping the module path on the declared side -
// and it is pinned here because keying the guard on the module-qualified
// block address would silently take it away, turning a destroy-free plan for
// an ordinary "extract this into a module" refactor into one that destroys
// and recreates every resource it moved. The module-to-root direction is the
// symmetric case the old guard could not see at all.
func TestClassifyOrphans_aBlockMovedAcrossModulesStaysWithheld(t *testing.T) {
	root, static, _, _ := moduleRenameCases(t)

	for _, tc := range []struct {
		label  string
		orphan *renameCase
		moved  *renameCase
	}{
		{"root block moved into a module", root, static},
		{"module block moved to the root", static, root},
	} {
		t.Run(tc.label, func(t *testing.T) {
			// Only the destination is unbound: the orphan's own block
			// is gone from the module path its marker names.
			result := &Result{
				Report: Report{Unbound: []addrs.AbsResourceInstance{tc.moved.declared}},
				Verdicts: Verdicts{Orphans: []OwnedResource{{
					TypeName:   "aws_subnet",
					ImportID:   "subnet-0deadbeef",
					Marker:     tc.orphan.marker,
					Normalized: tc.orphan.marker,
					Swept:      true,
				}}}}

			if diags := classifyOrphans(t.Context(), Request{Estate: "rename-withhold"}, listclient.Schemas{}, result); diags.HasErrors() {
				t.Fatalf("classifying a moved-block orphan reported errors: %s", diags.Err())
			}

			if o := result.Orphans[0]; o.Removal {
				t.Errorf("the live resource marked %q was proposed for destruction while %s - the same block at another module path - is unclaimed",
					tc.orphan.marker, tc.moved.declared)
			}
		})
	}
}

// TestClassifyOrphans_deletedBlockInAModuleIsStillARemoval is the other half,
// and it is the half that stops the fix being "withhold everything". An
// orphan whose block no longer exists at all - no declared instance of it
// anywhere, unclaimed or otherwise - is an ordinary removal, and it stays one
// at every module path.
func TestClassifyOrphans_deletedBlockInAModuleIsStillARemoval(t *testing.T) {
	_, static, counted, declared := moduleRenameCases(t)

	for _, c := range []*renameCase{static, counted} {
		t.Run(c.label, func(t *testing.T) {
			// Same module instance, a resource block the configuration does
			// not declare under any key.
			gone := addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: "aws_subnet",
				Name: "deleted",
			}.Instance(addrs.StringKey("a")).Absolute(c.declared.Module)
			marker := EscapeAddress(gone.String())

			result := &Result{Verdicts: Verdicts{Orphans: []OwnedResource{{
				TypeName:   "aws_subnet",
				ImportID:   "subnet-0deadbeef",
				Marker:     marker,
				Normalized: marker,
				Swept:      true,
			}}}, Report: Report{Unbound: declared}}

			if diags := classifyOrphans(t.Context(), Request{Estate: "rename-withhold"}, listclient.Schemas{}, result); diags.HasErrors() {
				t.Fatalf("classifying a deleted-block orphan reported errors: %s", diags.Err())
			}

			o := result.Orphans[0]
			if !o.Removal {
				t.Errorf("the orphan of a deleted block %q was withheld from removal (%q); nothing declares that block under any key",
					marker, o.Withheld)
			}
			if len(result.Resolutions) != 1 {
				t.Fatalf("want one removal resolution for %q, got %d", marker, len(result.Resolutions))
			}
			if got := result.Resolutions[0].Addr.String(); got != gone.String() {
				t.Errorf("the removal resolution is at %s, want %s", got, gone)
			}
		})
	}
}
