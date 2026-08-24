// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// TestPropagateModuleRenameMovesARecordBackedChildAndTheAnchorsOwnRecord is
// the wall e0329b5f58 named on corpus-rds-complete-postgres: live-mv renames
// module.db_default's own aws_db_instance (a taggable resource, rewritten by
// m.rewrite through a real cloud tag write), but module.db_default's
// UNTAGGABLE record-backed sibling -
// module.db_default.module.db_instance.random_id.snapshot_identifier[0] -
// has no marker of its own, so nothing else in this codebase ever teaches a
// later plan where its record went. Unlike a `moved` block (which a
// plan-time alias consult already follows - located.go's
// locatedIdentityWithAliases, GitHub issue #401), live-mv has no `moved`
// block to read, so the fix is to physically re-key the record right here.
//
// This test seeds three records under the estate's store and renames only
// module.db_default -> module.db_default_renamed:
//
//  1. The random_id sibling under the SAME module being renamed - must move,
//     byte-identical apart from its address.
//  2. The anchor resource's OWN identity record, seeded (as
//     internal/live/liveimport/stamp.go's seedIdentityFor does for every
//     stamped instance) under its OLD address - must also move, or a SECOND
//     rename of the same resource would look it up under an address the
//     record store no longer has anything at.
//  3. A random_id under a DIFFERENT module (module.db, never renamed) -
//     must stay exactly where it is: the mutation check this file's own
//     safety rule (HANDOFF.md, "a sibling module's record stays put") is
//     for.
func TestPropagateModuleRenameMovesARecordBackedChildAndTheAnchorsOwnRecord(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)

	anchorOld := mustAddr(t, "module.db_default.module.db_instance.aws_db_instance.this[0]")
	anchorNew := mustAddr(t, "module.db_default_renamed.module.db_instance.aws_db_instance.this[0]")

	childOld := mustAddr(t, "module.db_default.module.db_instance.random_id.snapshot_identifier[0]")
	childNew := mustAddr(t, "module.db_default_renamed.module.db_instance.random_id.snapshot_identifier[0]")

	siblingModule := mustAddr(t, "module.db.module.db_instance.random_id.snapshot_identifier[0]")

	// 1. The anchor's own identity record, exactly as stamp.go's
	// seedIdentityFor writes one for every stamped instance.
	if _, err := projection.SeedLocatedForInstance(ctx, store, anchorOld, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "db-anchor-1234"}); err != nil {
		t.Fatalf("seeding the anchor's own record: %s", err)
	}

	// 2. The record-backed child under the module being renamed.
	if _, err := projection.SeedLocatedForInstance(ctx, store, childOld, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "snapshot-id-abcd"}); err != nil {
		t.Fatalf("seeding the child's record: %s", err)
	}

	// 3. A record under a sibling module that this rename must never touch.
	if _, err := projection.SeedLocatedForInstance(ctx, store, siblingModule, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "snapshot-id-untouched"}); err != nil {
		t.Fatalf("seeding the sibling-module record: %s", err)
	}

	m := &mover{
		req: Request{
			Estate:      recordFallbackEstate,
			Old:         anchorOld,
			New:         anchorNew,
			RecordStore: store,
		},
		res: &Result{
			Old: anchorOld,
			New: anchorNew,
		},
	}

	diags := m.propagateModuleRename(ctx)
	if diags.HasErrors() {
		t.Fatalf("propagateModuleRename returned an error: %s", diags.Err())
	}

	// The child moved, byte-identical apart from its address.
	assertRecordMoved(t, ctx, store, childOld, childNew, "snapshot-id-abcd")

	// The anchor's own record moved too - required so a SECOND rename of
	// the same instance can still find it by its (now current) old address.
	assertRecordMoved(t, ctx, store, anchorOld, anchorNew, "db-anchor-1234")

	// The sibling module's record was never touched: still at its original
	// key, with its original value.
	rec, _, _, found, err := store.GetIdentity(ctx, siblingModule)
	if err != nil {
		t.Fatalf("reading the sibling-module record after the rename: %s", err)
	}
	if !found {
		t.Fatalf("the sibling module's record vanished; a rename of module.db_default must never touch module.db")
	}
	if rec.ImportID != "snapshot-id-untouched" {
		t.Errorf("the sibling module's record changed value (ImportID = %q, want %q); a rename of module.db_default must never touch module.db", rec.ImportID, "snapshot-id-untouched")
	}
}

// assertRecordMoved checks that a record used to live at from, now lives at
// to with the same ImportID, and is gone from from.
func assertRecordMoved(t *testing.T, ctx context.Context, store *projection.RecordStore, from, to addrs.AbsResourceInstance, wantImportID string) {
	t.Helper()

	if _, _, _, found, err := store.GetIdentity(ctx, from); err != nil {
		t.Fatalf("reading the record at the old address %s after the move: %s", from, err)
	} else if found {
		t.Errorf("a record still exists at the old address %s after the move; it should have been deleted", from)
	}

	rec, _, _, found, err := store.GetIdentity(ctx, to)
	if err != nil {
		t.Fatalf("reading the record at the new address %s after the move: %s", to, err)
	}
	if !found {
		t.Fatalf("no record found at the new address %s; the move did not happen", to)
	}
	if rec.ImportID != wantImportID {
		t.Errorf("the moved record's ImportID = %q, want %q - a move must carry the record's content across byte-identical apart from its address", rec.ImportID, wantImportID)
	}
}

// TestPropagateModuleRenameIsANoopWhenTheModuleDidNotChange is the boundary
// on the other side: renaming a resource within the SAME module (no module
// step differs at all) must never touch the record store - there is nothing
// for it to move, and asking would risk mistaking an ordinary resource
// rename for a module-boundary one.
func TestPropagateModuleRenameIsANoopWhenTheModuleDidNotChange(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)

	old := mustAddr(t, "module.db.random_id.snapshot_identifier[0]")
	newAddr := mustAddr(t, "module.db.random_id.snapshot_identifier_renamed[0]")

	if _, err := projection.SeedLocatedForInstance(ctx, store, old, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "unrelated"}); err != nil {
		t.Fatalf("seeding a record fixture: %s", err)
	}

	m := &mover{
		req: Request{
			Estate:      recordFallbackEstate,
			Old:         old,
			New:         newAddr,
			RecordStore: store,
		},
		res: &Result{Old: old, New: newAddr},
	}

	diags := m.propagateModuleRename(ctx)
	if diags.HasErrors() {
		t.Fatalf("propagateModuleRename returned an error for a same-module rename: %s", diags.Err())
	}

	// The record this rename itself is about is untouched by this
	// function - a same-module resource rename has no record propagation
	// to do; the resource's own marker/record handling lives elsewhere in
	// this package.
	if _, _, _, found, err := store.GetIdentity(ctx, old); err != nil {
		t.Fatalf("reading the record after a same-module rename: %s", err)
	} else if !found {
		t.Errorf("the record at the old address vanished; a same-module rename must not move anything")
	}
	if _, _, _, found, err := store.GetIdentity(ctx, newAddr); err != nil {
		t.Fatalf("reading the record at the new address after a same-module rename: %s", err)
	} else if found {
		t.Errorf("a record appeared at the new address; a same-module rename must not move anything")
	}
}

// TestPropagateModuleRenameNilRecordStoreIsANoop confirms the module-boundary
// propagation degrades exactly like every other RecordStore consumer in this
// package when no record_store block is configured: nil in, nil diagnostics
// out, nothing attempted.
func TestPropagateModuleRenameNilRecordStoreIsANoop(t *testing.T) {
	old := mustAddr(t, "module.db_default.module.db_instance.aws_db_instance.this[0]")
	newAddr := mustAddr(t, "module.db_default_renamed.module.db_instance.aws_db_instance.this[0]")

	m := &mover{
		req: Request{Estate: recordFallbackEstate, Old: old, New: newAddr, RecordStore: nil},
		res: &Result{Old: old, New: newAddr},
	}

	diags := m.propagateModuleRename(t.Context())
	if diags.HasErrors() {
		t.Fatalf("propagateModuleRename with a nil RecordStore returned an error: %s", diags.Err())
	}
}
