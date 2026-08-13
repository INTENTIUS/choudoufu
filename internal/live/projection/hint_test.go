// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/states"
)

// This is issue #64's snapshot-guided leg, the read side. TestSnapshot_noReader's
// restated invariant ("nothing reads a snapshot as authority") names this
// file's two functions as the one sanctioned exception; these tests are the
// proof that the exception is held to what the restatement actually allows:
// a reduced hint, never the snapshot's full content, and a graceful "no
// hint" answer rather than a panic or a partial result for every way a
// snapshot can be missing, corrupted, or unreadable.

// ---------------------------------------------------------------------------
// The reduced shape
// ---------------------------------------------------------------------------

// TestHint_reducedShapeOnly is the automated half of the restated invariant:
// [Hint] may only ever carry the reduced hint set - which types, which
// identifiers, when - never anything from [snapshotResource] that would let
// a caller reconstruct the snapshot's full per-resource record
// (AttributesHash, Markers, Identity, ImportID as such). A Hint that grew a
// field beyond this list would be a second, wider reader growing quietly
// behind the one TestSnapshot_noReader was updated to allow.
func TestHint_reducedShapeOnly(t *testing.T) {
	allowed := map[string]bool{
		"Estate":      true,
		"WrittenAt":   true,
		"Types":       true,
		"Identifiers": true,
	}
	rt := reflect.TypeOf(Hint{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if !allowed[name] {
			t.Errorf("Hint carries field %q, which is not one of the reduced hint fields %v; "+
				"a hint reader may only ever narrow what a caller can learn from a snapshot, never widen it "+
				"back toward the full record - see TestSnapshot_noReader's restated invariant", name, allowed)
		}
	}
}

// ---------------------------------------------------------------------------
// ReadHintFile
// ---------------------------------------------------------------------------

// TestReadHintFile_roundTrip builds a real snapshot the same way a live run
// would (through Manager, never by hand-writing the JSON) and confirms
// ReadHintFile reduces it to exactly the types and identifiers the state
// carried.
func TestReadHintFile_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	when := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(when))
	if err := m.WriteState(testHintState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	hint, err := ReadHintFile(path)
	if err != nil {
		t.Fatalf("ReadHintFile: %s", err)
	}
	assertHintMatchesFixture(t, hint, when)
}

// TestReadHintFile_missing: no file at all is a plain error, never a panic
// and never a Hint with zeroed-out fields standing in for "nothing here".
func TestReadHintFile_missing(t *testing.T) {
	hint, err := ReadHintFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("want an error for a missing snapshot file, got nil")
	}
	if hint != nil {
		t.Errorf("want a nil Hint alongside the error, got %#v", hint)
	}
}

// TestReadHintFile_emptyPath is the same "no path configured" refusal
// [ReadHintFile] gives when the caller passes what "no snapshot_path" looks
// like, without ever touching the filesystem.
func TestReadHintFile_emptyPath(t *testing.T) {
	if _, err := ReadHintFile(""); err == nil {
		t.Fatal("want an error for an empty path")
	}
}

// TestReadHintFile_corrupted covers the ways a snapshot file can exist and
// still be unusable: not JSON at all, JSON but the wrong shape, and JSON
// carrying a formatVersion this build does not recognize (a future or a
// foreign one). Every case is an error, never a partial Hint.
func TestReadHintFile_corrupted(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"not JSON", "not json at all {{{"},
		{"wrong shape", `{"hello": "world"}`},
		{"unrecognized format", `{"formatVersion": "some-other-format-v9", "estate": "x", "resources": []}`},
		{"truncated", `{"formatVersion": "tofu-live-sn`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.json")
			if err := os.WriteFile(path, []byte(c.data), 0o600); err != nil {
				t.Fatalf("writing fixture: %s", err)
			}
			hint, err := ReadHintFile(path)
			if err == nil {
				t.Fatalf("want an error for %s, got a Hint: %#v", c.name, hint)
			}
			if hint != nil {
				t.Errorf("want a nil Hint alongside the error, got %#v", hint)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ReadHintBranch
// ---------------------------------------------------------------------------

// TestReadHintBranch_roundTrip is TestReadHintFile_roundTrip's branch-carrier
// twin: the same state, written to the tofu-snapshots/<estate> branch of a
// real repository, read back and reduced the same way.
func TestReadHintBranch_roundTrip(t *testing.T) {
	gitForTest(t)
	dir := initGitRepo(t)
	when := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

	m := NewManager()
	m.EnableSnapshotBranch(dir, "branch-estate", fixedClock(when))
	if err := m.WriteState(testHintState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	hint, err := ReadHintBranch(dir, "branch-estate")
	if err != nil {
		t.Fatalf("ReadHintBranch: %s", err)
	}
	assertHintMatchesFixture(t, hint, when)
}

// TestReadHintBranch_missing: a repository with no tofu-snapshots/<estate>
// branch at all - the ordinary "nothing has ever been applied with a branch
// carrier enabled" case - is an error, not a Hint with nothing in it.
func TestReadHintBranch_missing(t *testing.T) {
	gitForTest(t)
	dir := initGitRepo(t)

	hint, err := ReadHintBranch(dir, "no-such-estate")
	if err == nil {
		t.Fatal("want an error for a branch that was never written")
	}
	if hint != nil {
		t.Errorf("want a nil Hint alongside the error, got %#v", hint)
	}
}

// TestReadHintBranch_notARepo: moduleDir outside any git repository is the
// same errSnapshotBranchUnavailable class the writer already distinguishes,
// wrapped so a caller (internal/live/discovery's guided mode) can tell "no
// carrier here" from "a carrier exists and is broken" the way
// Manager.writeSnapshotCarriers already does for the write side.
func TestReadHintBranch_notARepo(t *testing.T) {
	gitForTest(t)
	dir := t.TempDir() // deliberately not a git repository

	_, err := ReadHintBranch(dir, "my-estate")
	if err == nil {
		t.Fatal("want an error for a non-repository directory")
	}
	if !isSnapshotBranchUnavailable(err) {
		t.Errorf("error %q is not classified as errSnapshotBranchUnavailable", err)
	}
}

// TestReadHintBranch_corruptedBlob: the branch exists and the ref resolves,
// but the blob at snapshot.json is not a valid, recognized snapshot. Mirrors
// TestReadHintFile_corrupted for the branch carrier, written with the same
// plumbing the production writer uses so the fixture is a faithful "bad
// commit", not a shortcut around the read path.
func TestReadHintBranch_corruptedBlob(t *testing.T) {
	gitBin := gitForTest(t)
	dir := initGitRepo(t)

	commitBadBlob(t, gitBin, dir, "corrupt-estate", []byte("not json at all {{{"))

	hint, err := ReadHintBranch(dir, "corrupt-estate")
	if err == nil {
		t.Fatal("want an error for a corrupted blob")
	}
	if hint != nil {
		t.Errorf("want a nil Hint alongside the error, got %#v", hint)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// isSnapshotBranchUnavailable reports whether err is or wraps
// errSnapshotBranchUnavailable, without pulling in errors.Is at every call
// site above.
func isSnapshotBranchUnavailable(err error) bool {
	for err != nil {
		if err == errSnapshotBranchUnavailable { //nolint:errorlint // also matches the wrapped %w case via Unwrap below
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// commitBadBlob writes arbitrary bytes to refs/heads/tofu-snapshots/<estate>
// using the same plumbing sequence writeSnapshotBranch uses, so a corrupted
// fixture is committed the way a real (if broken) writer would leave one,
// not assembled by some other means ReadHintBranch would never actually
// meet in practice.
func commitBadBlob(t *testing.T, gitBin, dir, estate string, data []byte) {
	t.Helper()
	blob, err := gitPlumb(gitBin, dir, data, "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatalf("hash-object: %s", err)
	}
	tree, err := gitPlumb(gitBin, dir, []byte("100644 blob "+blob+"\t"+snapshotBranchFileName+"\n"), "mktree")
	if err != nil {
		t.Fatalf("mktree: %s", err)
	}
	commit, err := gitPlumb(gitBin, dir, nil, "commit-tree", tree, "-m", "corrupted snapshot fixture")
	if err != nil {
		t.Fatalf("commit-tree: %s", err)
	}
	ref := snapshotBranchNamespace + estate
	if _, err := gitPlumb(gitBin, dir, nil, "update-ref", ref, commit, ""); err != nil {
		t.Fatalf("update-ref: %s", err)
	}
}

// testHintState is a small, two-type state: enough for ReadHintFile /
// ReadHintBranch's round trip to prove both the type set and the
// per-type identifier set survive, which a single-resource fixture
// (testProjectionState) cannot distinguish from a caller that only reduced
// to "the one resource this state had".
func testHintState() *states.State {
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_s3_bucket", Name: "data"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"bucket-a","bucket":"bucket-a"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_sns_topic", Name: "alerts"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"arn:aws:sns:us-east-1:000000000000:alerts"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)
	return state
}

// assertHintMatchesFixture checks a Hint read back against testHintState's
// known shape.
func assertHintMatchesFixture(t *testing.T, hint *Hint, when time.Time) {
	t.Helper()
	if hint == nil {
		t.Fatal("got a nil Hint")
	}
	if !hint.WrittenAt.Equal(when) {
		t.Errorf("WrittenAt = %s, want %s", hint.WrittenAt, when)
	}
	wantTypes := map[string]bool{"aws_s3_bucket": true, "aws_sns_topic": true}
	if len(hint.Types) != len(wantTypes) {
		t.Errorf("Types = %v, want %v", hint.Types, wantTypes)
	}
	for typ := range wantTypes {
		if !hint.Types[typ] {
			t.Errorf("Types is missing %q: %v", typ, hint.Types)
		}
	}
	if !hint.Identifiers["aws_s3_bucket"]["bucket-a"] {
		t.Errorf("Identifiers[aws_s3_bucket] = %v, want it to carry \"bucket-a\"", hint.Identifiers["aws_s3_bucket"])
	}
	if !hint.Identifiers["aws_sns_topic"]["arn:aws:sns:us-east-1:000000000000:alerts"] {
		t.Errorf("Identifiers[aws_sns_topic] = %v, want it to carry the topic ARN", hint.Identifiers["aws_sns_topic"])
	}
}
