// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// TestPropagateModuleRenameFollowsAMovedBlockHopBeforeLiveMv is
// gauntlet:giantswarm-mv-children's own reproduction of the
// corpus-giantswarm-crossplane day2_rename wall: a plain `moved` block
// relocates module.crossplane -> module.crossplane_renamed first (D1, no
// live-mv call at all, so nothing in the record store physically moves -
// see this package's own doc comment on why a `moved` block never does),
// and THEN a live-mv call relocates module.crossplane_renamed ->
// module.crossplane_final with no second `moved` block (D2). A
// record-located sibling with no marker of its own is still keyed at
// module.crossplane - one hop further back than req.Old's own module
// (module.crossplane_renamed) - and this call must still carry it all the
// way to module.crossplane_final.
func TestPropagateModuleRenameFollowsAMovedBlockHopBeforeLiveMv(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)

	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
module "crossplane_final" {
  source = "./crossplane"
}

moved {
  from = module.crossplane
  to   = module.crossplane_renamed
}
`)
	writeFile(t, dir, "crossplane/main.tf", `
resource "aws_iam_role" "role" {}

resource "aws_iam_role_policy" "extra" {
  for_each = toset(["extra-tagging"])
}
`)
	cfg := loadConfigDir(t, dir)

	anchorOld := mustAddr(t, "module.crossplane_renamed.aws_iam_role.role")
	anchorNew := mustAddr(t, "module.crossplane_final.aws_iam_role.role")

	// The record-located sibling: still keyed at module.crossplane, the
	// address that predates BOTH the `moved` block and the live-mv call -
	// exactly where it was left the moment it was first recorded, since
	// neither a `moved` block nor a live-mv call for a DIFFERENT resource
	// (the role) ever touched it.
	childTwoHopsBack := mustAddr(t, `module.crossplane.aws_iam_role_policy.extra["extra-tagging"]`)
	childFinal := mustAddr(t, `module.crossplane_final.aws_iam_role_policy.extra["extra-tagging"]`)
	if _, err := projection.SeedLocatedForInstance(ctx, store, childTwoHopsBack, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "policy-two-hops-back"}); err != nil {
		t.Fatalf("seeding the two-hops-back child record: %s", err)
	}

	// A sibling module this rename must never touch, same mutation check as
	// TestPropagateModuleRenameMovesARecordBackedChildAndTheAnchorsOwnRecord.
	untouched := mustAddr(t, "module.unrelated.aws_iam_role_policy.other")
	if _, err := projection.SeedLocatedForInstance(ctx, store, untouched, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "untouched"}); err != nil {
		t.Fatalf("seeding the unrelated-module record: %s", err)
	}

	m := &mover{
		req: Request{
			Estate:      recordFallbackEstate,
			Old:         anchorOld,
			New:         anchorNew,
			Config:      cfg,
			RecordStore: store,
		},
		res: &Result{Old: anchorOld, New: anchorNew},
	}

	diags := m.propagateModuleRename(ctx)
	if diags.HasErrors() {
		t.Fatalf("propagateModuleRename returned an error: %s", diags.Err())
	}

	assertRecordMoved(t, ctx, store, childTwoHopsBack, childFinal, "policy-two-hops-back")

	rec, _, _, found, err := store.GetIdentity(ctx, untouched)
	if err != nil {
		t.Fatalf("reading the unrelated-module record after the rename: %s", err)
	}
	if !found {
		t.Fatalf("the unrelated module's record vanished; this rename must never touch module.unrelated")
	}
	if rec.ImportID != "untouched" {
		t.Errorf("the unrelated module's record changed value (ImportID = %q, want %q)", rec.ImportID, "untouched")
	}
}

// TestPropagateModuleRenameStillMovesAOneHopChildWithAMovedBlockInConfig
// pins the shape [gauntlet:sweep-moved-alias] and the original
// propagateModuleRename fix already covered - a record sitting exactly at
// req.Old's own module (one hop back, no earlier `moved`-block hop involved
// at all) - now that renameBoundaryOrigins also runs. A regression that
// somehow made the multi-prefix search MISS the direct, zero-hop prefix
// would only show up here, not in the two-hops-back test above.
func TestPropagateModuleRenameStillMovesAOneHopChildWithAMovedBlockInConfig(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)

	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
module "crossplane_final" {
  source = "./crossplane"
}

moved {
  from = module.crossplane
  to   = module.crossplane_renamed
}
`)
	writeFile(t, dir, "crossplane/main.tf", `
resource "aws_iam_role" "role" {}

resource "aws_iam_role_policy_attachment" "attach" {}
`)
	cfg := loadConfigDir(t, dir)

	anchorOld := mustAddr(t, "module.crossplane_renamed.aws_iam_role.role")
	anchorNew := mustAddr(t, "module.crossplane_final.aws_iam_role.role")

	// This sibling's record already sits at req.Old's own module (as if an
	// earlier live-mv call, not a `moved` block, had already carried it one
	// hop) - the original, pre-this-unit case.
	childOneHopBack := mustAddr(t, "module.crossplane_renamed.aws_iam_role_policy_attachment.attach")
	childFinal := mustAddr(t, "module.crossplane_final.aws_iam_role_policy_attachment.attach")
	if _, err := projection.SeedLocatedForInstance(ctx, store, childOneHopBack, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "attach-one-hop-back"}); err != nil {
		t.Fatalf("seeding the one-hop-back child record: %s", err)
	}

	m := &mover{
		req: Request{
			Estate:      recordFallbackEstate,
			Old:         anchorOld,
			New:         anchorNew,
			Config:      cfg,
			RecordStore: store,
		},
		res: &Result{Old: anchorOld, New: anchorNew},
	}

	diags := m.propagateModuleRename(ctx)
	if diags.HasErrors() {
		t.Fatalf("propagateModuleRename returned an error: %s", diags.Err())
	}

	assertRecordMoved(t, ctx, store, childOneHopBack, childFinal, "attach-one-hop-back")
}

// TestRenameBoundaryOriginsSkipsAnUnrelatedMovedBlock is the conservative
// side of renameBoundaryOrigins: a `moved` block that exists in the same
// configuration but names a completely different module boundary must
// never contribute a spurious extra prefix. Proven by mutation: a record
// seeded at the unrelated block's OWN "from" address must NOT move, which
// would only happen if renameBoundaryOrigins wrongly treated every
// [moved.Honoured] statement as relevant instead of following req.Old's own
// alias chain.
func TestRenameBoundaryOriginsSkipsAnUnrelatedMovedBlock(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)

	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
module "crossplane_final" {
  source = "./crossplane"
}

module "somewhere_else" {
  source = "./elsewhere"
}

moved {
  from = module.long_gone
  to   = module.somewhere_else
}
`)
	writeFile(t, dir, "crossplane/main.tf", `
resource "aws_iam_role" "role" {}
`)
	writeFile(t, dir, "elsewhere/main.tf", `
resource "aws_iam_role_policy" "extra" {}
`)
	cfg := loadConfigDir(t, dir)

	// No `moved` block at all relates module.crossplane to
	// module.crossplane_final - this rename is a bare, single-hop live-mv,
	// exactly TestPropagateModuleRenameMovesARecordBackedChildAndTheAnchorsOwnRecord's
	// own shape, just with an unrelated `moved` block also present in the
	// same config tree.
	anchorOld := mustAddr(t, "module.crossplane.aws_iam_role.role")
	anchorNew := mustAddr(t, "module.crossplane_final.aws_iam_role.role")

	// Sitting at the UNRELATED block's own "from" address. If
	// renameBoundaryOrigins folded every Honoured statement in blindly
	// rather than following req.Old's own chain, this would be mistaken for
	// a match and moved somewhere under module.crossplane_final - the wrong
	// live object bound to the wrong address, exactly the wrong-marker
	// hazard HANDOFF.md's safety rule exists to stop.
	decoy := mustAddr(t, "module.long_gone.aws_iam_role_policy.extra")
	if _, err := projection.SeedLocatedForInstance(ctx, store, decoy, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "decoy"}); err != nil {
		t.Fatalf("seeding the decoy record: %s", err)
	}

	m := &mover{
		req: Request{
			Estate:      recordFallbackEstate,
			Old:         anchorOld,
			New:         anchorNew,
			Config:      cfg,
			RecordStore: store,
		},
		res: &Result{Old: anchorOld, New: anchorNew},
	}

	diags := m.propagateModuleRename(ctx)
	if diags.HasErrors() {
		t.Fatalf("propagateModuleRename returned an error: %s", diags.Err())
	}

	rec, _, _, found, err := store.GetIdentity(ctx, decoy)
	if err != nil {
		t.Fatalf("reading the decoy record after the rename: %s", err)
	}
	if !found {
		t.Fatalf("the decoy record moved even though no `moved` block relates module.long_gone to module.crossplane's own rename - an unrelated moved block must never contribute a prefix")
	}
	if rec.ImportID != "decoy" {
		t.Errorf("the decoy record changed value (ImportID = %q, want %q)", rec.ImportID, "decoy")
	}
}

// TestPropagateModuleRenameReconcilesADuplicateAtTwoOldPrefixes is the
// second wall found driving corpus-giantswarm-crossplane for real with
// renameBoundaryOrigins in place: an ordinary apply along a `moved`-block
// hop (D1 in that estate's own script) refreshes every declared instance's
// own kind=identity record at the address current when it ran, without
// deleting the copy an earlier hop left behind - so a record-located
// sibling can have TWO stored records, one at req.Old's own module
// (the fresher one, written by the most recent apply) and one further back
// (stale, from before the `moved` block existed at all). Both now match
// under renameBoundaryOrigins' own multi-prefix set and land on the SAME
// destination; without reconciliation, [projection.RecordStore.MoveRecord]'s
// own CAS correctly refuses the second write ("expected no record to
// exist"), which is exactly what a real run against floci hit. The fresher
// (closer) copy must win the move; the staler one must be deleted outright,
// not left behind - see propagateModuleRename's own doc comment for why an
// orphaned duplicate is unsafe to leave, not merely untidy.
func TestPropagateModuleRenameReconcilesADuplicateAtTwoOldPrefixes(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)

	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
module "crossplane_final" {
  source = "./crossplane"
}

moved {
  from = module.crossplane
  to   = module.crossplane_renamed
}
`)
	writeFile(t, dir, "crossplane/main.tf", `
resource "aws_iam_role" "role" {}

resource "aws_iam_role_policy" "extra" {
  for_each = toset(["extra-tagging"])
}
`)
	cfg := loadConfigDir(t, dir)

	anchorOld := mustAddr(t, "module.crossplane_renamed.aws_iam_role.role")
	anchorNew := mustAddr(t, "module.crossplane_final.aws_iam_role.role")

	// The stale, two-hops-back copy: left over from before the `moved`
	// block existed, never touched by D1's own apply.
	stale := mustAddr(t, `module.crossplane.aws_iam_role_policy.extra["extra-tagging"]`)
	// The fresh, one-hop-back copy: written by D1's own apply refreshing
	// every declared instance's record at the address current then.
	fresh := mustAddr(t, `module.crossplane_renamed.aws_iam_role_policy.extra["extra-tagging"]`)
	final := mustAddr(t, `module.crossplane_final.aws_iam_role_policy.extra["extra-tagging"]`)

	if _, err := projection.SeedLocatedForInstance(ctx, store, stale, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "stale-copy"}); err != nil {
		t.Fatalf("seeding the stale copy: %s", err)
	}
	if _, err := projection.SeedLocatedForInstance(ctx, store, fresh, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "fresh-copy"}); err != nil {
		t.Fatalf("seeding the fresh copy: %s", err)
	}

	m := &mover{
		req: Request{
			Estate:      recordFallbackEstate,
			Old:         anchorOld,
			New:         anchorNew,
			Config:      cfg,
			RecordStore: store,
		},
		res: &Result{Old: anchorOld, New: anchorNew},
	}

	diags := m.propagateModuleRename(ctx)
	if diags.HasErrors() {
		t.Fatalf("propagateModuleRename returned an error reconciling a duplicate: %s", diags.Err())
	}

	// The fresh copy's content wins at the final destination.
	rec, _, _, found, err := store.GetIdentity(ctx, final)
	if err != nil {
		t.Fatalf("reading the record at the final address: %s", err)
	}
	if !found {
		t.Fatalf("no record found at the final address %s", final)
	}
	if rec.ImportID != "fresh-copy" {
		t.Errorf("the record at %s has ImportID = %q, want %q (the fresher, closer copy) - a superseded duplicate must never win", final, rec.ImportID, "fresh-copy")
	}

	// Both source copies are gone - the winner moved, the loser deleted.
	if _, _, _, found, err := store.GetIdentity(ctx, fresh); err != nil {
		t.Fatalf("reading the fresh copy's old address after the move: %s", err)
	} else if found {
		t.Errorf("a record still exists at the fresh copy's old address %s after the move", fresh)
	}
	if _, _, _, found, err := store.GetIdentity(ctx, stale); err != nil {
		t.Fatalf("reading the stale copy's address after reconciliation: %s", err)
	} else if found {
		t.Errorf("the stale duplicate at %s was not cleaned up - left behind, it would resurface as a false orphan on the next plan", stale)
	}
}

// TestRenameBoundaryOriginsWithNoConfigIsTheOriginalSinglePrefix confirms
// req.Config == nil (every pre-existing propagateModuleRename test's own
// shape) still reduces to exactly the original single-prefix behavior -
// renameBoundaryOrigins must not panic or change behavior when there is no
// configuration to read `moved` blocks from at all.
func TestRenameBoundaryOriginsWithNoConfigIsTheOriginalSinglePrefix(t *testing.T) {
	oldPrefix := addrs.ModuleInstance{{Name: "crossplane_renamed"}}
	newPrefix := addrs.ModuleInstance{{Name: "crossplane_final"}}
	reqOld := mustAddr(t, "module.crossplane_renamed.aws_iam_role.role")

	got := renameBoundaryOrigins(nil, reqOld, oldPrefix, newPrefix)
	if len(got) != 1 || !got[0].Equal(oldPrefix) {
		t.Fatalf("renameBoundaryOrigins(nil config, ...) = %v, want exactly [%s]", got, oldPrefix)
	}
}
