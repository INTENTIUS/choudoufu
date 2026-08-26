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

// TestPropagateModuleRenameMovesTheOwnRecordEvenWithNoModuleBoundary is
// GitHub issue #412: renaming a resource within the SAME module (no module
// step differs at all, so [moduleRenameBoundary] finds nothing and the
// sweep below never runs) still has to move THIS resource's own record -
// the one thing [mover.propagateModuleRename] now does unconditionally,
// before that guard. Before the #412 fix this was the mirror image of
// [TestPropagateModuleRenameMovesARecordBackedChildAndTheAnchorsOwnRecord]'s
// old-key/new-key check, except it asserted the WRONG thing (the record
// staying at its stale key) - reproduced empirically against floci first,
// see this package's own git history for the key dump.
//
// Value-asserted key-SET before/after, not just the two addresses this
// rename names: the whole store is listed both times, so a fix that moved
// the wrong key, or left an extra key behind, or dropped the sibling, would
// all be caught here even though none of the two-address assertions below
// would notice.
func TestPropagateModuleRenameMovesTheOwnRecordEvenWithNoModuleBoundary(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)

	old := mustAddr(t, "module.db.random_id.snapshot_identifier[0]")
	newAddr := mustAddr(t, "module.db.random_id.snapshot_identifier_renamed[0]")

	// An unrelated sibling record, in a different resource entirely (not
	// even the same module call), that this rename must never touch -
	// the mutation check: a fix broad enough to move every key, or to key
	// off something other than req.Old/req.New, would show up here.
	sibling := mustAddr(t, "module.other.random_id.unrelated[0]")

	if _, err := projection.SeedLocatedForInstance(ctx, store, old, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "same-module-rename"}); err != nil {
		t.Fatalf("seeding the renamed resource's own record: %s", err)
	}
	if _, err := projection.SeedLocatedForInstance(ctx, store, sibling, recordFallbackProviderAddr, projection.LocatedRecord{ImportID: "untouched"}); err != nil {
		t.Fatalf("seeding the sibling record: %s", err)
	}

	prefix := store.Prefix()
	before := recordKeySet(t, ctx, store, prefix)
	if !before[old.String()] || !before[sibling.String()] || len(before) != 2 {
		t.Fatalf("fixture did not seed the two keys this test expects: %v", before)
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

	after := recordKeySet(t, ctx, store, prefix)
	want := map[string]bool{newAddr.String(): true, sibling.String(): true}
	if len(after) != len(want) {
		t.Fatalf("key set after the rename = %v, want exactly %v", after, want)
	}
	for addr := range want {
		if !after[addr] {
			t.Errorf("key set after the rename is missing %s: %v", addr, after)
		}
	}
	if after[old.String()] {
		t.Errorf("the old key %s is still present after the rename; it must be gone, not just duplicated", old)
	}

	// The renamed resource's own record: moved, byte-identical apart from
	// its address (assertRecordMoved's own check).
	assertRecordMoved(t, ctx, store, old, newAddr, "same-module-rename")

	// The sibling: untouched, same value, same key.
	rec, _, _, found, err := store.GetIdentity(ctx, sibling)
	if err != nil {
		t.Fatalf("reading the sibling record after the rename: %s", err)
	}
	if !found {
		t.Fatalf("the sibling record vanished; a same-module rename of an unrelated resource must never touch it")
	}
	if rec.ImportID != "untouched" {
		t.Errorf("the sibling record changed value (ImportID = %q, want %q)", rec.ImportID, "untouched")
	}

	// A rename is not a destroy: the moved record must read back as an
	// ordinary live identity, never as a tombstone. MoveRecord (record.go)
	// never calls the tombstone path at all - it only rewrites env.Address
	// and re-encodes the SAME envelope - but this is the assertion that
	// would catch a future rewrite of this function that routed the own-key
	// move through delete-then-recreate instead of a real MoveRecord.
	if tombstones, _, _, err := store.GetTombstones(ctx, newAddr); err != nil {
		t.Fatalf("reading tombstones at the new address: %s", err)
	} else if len(tombstones) != 0 {
		t.Errorf("the moved record carries tombstone entries (%v); a rename must never tombstone the address it moves to", tombstones)
	}
	if tombstones, _, _, err := store.GetTombstones(ctx, old); err != nil {
		t.Fatalf("reading tombstones at the old address: %s", err)
	} else if len(tombstones) != 0 {
		t.Errorf("the vacated old address carries tombstone entries (%v); a rename must leave nothing behind, tombstoned or otherwise", tombstones)
	}
}

// TestPropagateModuleRenameOwnKeyMoveIsANoopWithNoRecord is #412's other
// edge: most live-mv renames are of a markable resource with NO record of
// its own (an ordinary taggable type, whose marker rewrite is
// [mover.rewrite]'s job, not this function's). The new unconditional
// MoveRecord call at the top of [mover.propagateModuleRename] must stay a
// no-op for that overwhelmingly common case - no error, and nothing
// created at either address - exactly like every other RecordStore
// consumer in this package when there is nothing recorded.
func TestPropagateModuleRenameOwnKeyMoveIsANoopWithNoRecord(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)

	old := mustAddr(t, "module.db.random_id.snapshot_identifier[0]")
	newAddr := mustAddr(t, "module.db.random_id.snapshot_identifier_renamed[0]")

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
		t.Fatalf("propagateModuleRename returned an error when there was nothing to move: %s", diags.Err())
	}

	if _, _, _, found, err := store.GetIdentity(ctx, old); err != nil {
		t.Fatalf("reading the old address: %s", err)
	} else if found {
		t.Errorf("a record exists at the old address that this test never seeded one for")
	}
	if _, _, _, found, err := store.GetIdentity(ctx, newAddr); err != nil {
		t.Fatalf("reading the new address: %s", err)
	} else if found {
		t.Errorf("a record was created at the new address even though nothing was recorded at the old one")
	}
}

// recordKeySet is every address the store currently holds a record for,
// decoded from its raw keys the same way [mover.propagateModuleRename]
// itself does (projection.RecordAddr) - a value-level check on the whole
// store, not just the one or two addresses a given test already names.
func recordKeySet(t *testing.T, ctx context.Context, store *projection.RecordStore, prefix string) map[string]bool {
	t.Helper()

	keys, err := store.List(ctx)
	if err != nil {
		t.Fatalf("listing the record store: %s", err)
	}
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		addr, ok := projection.RecordAddr(prefix, key)
		if !ok {
			t.Fatalf("a stored key %q did not decode against prefix %q", key, prefix)
		}
		out[addr.String()] = true
	}
	return out
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
