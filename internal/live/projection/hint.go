// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// This file is the one deliberate, reviewed exception to the "no reader"
// half of P4.2 rule 3 - see TestSnapshot_noReader's restated doc comment in
// snapshot_test.go for the invariant this file is held to, and the package
// doc comment above [snapshot] for what it is an exception to.
//
// The restated rule: nothing reads a snapshot AS AUTHORITY. [ReadHintFile]
// and [ReadHintBranch] read one, but everything they hand back is [Hint]:
// which resource types the snapshot spans and which identifiers it last saw
// per type, never a resource's attributes, its full identity object, or
// anything else a caller could act on as if it were a live read.
// [TestHint_reducedShapeOnly] enforces that Hint's fields never grow past
// that reduced shape. A caller (internal/live/discovery's guided mode) uses
// a Hint to decide what to look at first and what to skip on a routine pass
// - never to decide what the plan says. Every one of this file's two
// exported functions returns (nil, error) rather than panicking or handing
// back a partial Hint on any problem: a missing file, an unreadable branch,
// a corrupted or unrecognized-format blob. The caller's contract (see
// internal/live/discovery's guided.go) is to treat every such error as
// "fall back to today's full enumeration", never to surface it as a run
// failure - staleness and absence are this cache's whole contract, restated
// once more for the read side.

// Hint is what a caller may learn from the most recent observational
// snapshot: a hint about what the estate looked like as of WrittenAt, never
// a record it can act on directly.
type Hint struct {
	// Estate is the estate name the snapshot recorded.
	Estate string

	// WrittenAt is when the snapshot was built. Nothing in this package
	// gates on it - see the doc comment on [snapshot.WrittenAt] - but a
	// caller deciding how much to trust the hint (internal/live/discovery's
	// staleness check) needs it to decide with.
	WrittenAt time.Time

	// Types is the set of resource types the snapshot's resources spanned.
	// A type's absence here means "this snapshot recorded nothing of it",
	// not "this estate has none" - see the package's staleness contract.
	Types map[string]bool

	// Identifiers is, per type, the set of identifiers the snapshot last
	// saw: a resource's ImportID when it had one, or its Identity object
	// JSON-encoded when it did not. Advisory only - a caller that finds one
	// of these still live has learned nothing about whether it is bound,
	// orphaned, or renamed; that classification still has to come from an
	// actual read.
	Identifiers map[string]map[string]bool
}

// ReadHintFile reads the file-carrier snapshot at path (the "live" block's
// snapshot_path, [Manager.EnableSnapshot]'s target) and reduces it to a
// [Hint]. Every failure - the file does not exist, cannot be read, does not
// parse, or names a format this build does not recognize - comes back as a
// non-nil error and a nil Hint; there is no partial or best-effort result.
func ReadHintFile(path string) (*Hint, error) {
	if path == "" {
		return nil, errors.New("no snapshot_path is configured")
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is caller-configured, same as writeSnapshot's own target
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return decodeHint(data)
}

// ReadHintBranch reads the most recent commit on the branch-carrier
// snapshot ([Manager.EnableSnapshotBranch]'s target,
// refs/heads/tofu-snapshots/<estate> in the git repository enclosing
// moduleDir) and reduces it to a [Hint]. It is read-only plumbing - one "git
// show" of the branch tip's blob - and never touches the worktree, the
// index, or HEAD, mirroring [writeSnapshotBranch]'s own promise not to.
//
// Every failure - no git on PATH, moduleDir not inside a repository, no such
// branch, a blob that will not parse - comes back as a non-nil error and a
// nil Hint, [errSnapshotBranchUnavailable]-wrapped for the first two exactly
// as the writer's own failures are, so a caller can tell "there is no
// carrier here" from "there is one and something is wrong with it" the same
// way [Manager.writeSnapshotCarriers] already does for the write side.
func ReadHintBranch(moduleDir, estate string) (*Hint, error) {
	if estate == "" {
		return nil, errors.New("the estate name is not settled, so there is no tofu-snapshots branch to read")
	}

	gitBin, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("%w: no git executable on PATH", errSnapshotBranchUnavailable)
	}
	if _, err := gitPlumb(gitBin, moduleDir, nil, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("%w: %s is not inside a git repository", errSnapshotBranchUnavailable, moduleDir)
	}

	ref := snapshotBranchNamespace + estate
	data, err := gitPlumb(gitBin, moduleDir, nil, "show", ref+":"+snapshotBranchFileName)
	if err != nil {
		return nil, fmt.Errorf("%w: reading %s: %s", errSnapshotBranchUnavailable, strings.TrimPrefix(ref, "refs/heads/"), err)
	}
	return decodeHint([]byte(data))
}

// decodeHint parses data as a [snapshot] and reduces it to a [Hint]. It is
// the one place this file spells [snapshotFormatVersion] as anything but a
// literal - by identifier, from the same package snapshot.go declares it in
// - so nothing here duplicates the format string TestSnapshot_noReader
// polices.
func decodeHint(data []byte) (*Hint, error) {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decoding the snapshot: %w", err)
	}
	if snap.FormatVersion != snapshotFormatVersion {
		return nil, fmt.Errorf("unrecognized snapshot format %q", snap.FormatVersion)
	}

	h := &Hint{
		Estate:      snap.Estate,
		Types:       make(map[string]bool),
		Identifiers: make(map[string]map[string]bool),
	}
	if when, err := time.Parse(time.RFC3339, snap.WrittenAt); err == nil {
		h.WrittenAt = when
	}

	for _, r := range snap.Resources {
		h.Types[r.Type] = true

		id := r.ImportID
		if id == "" && len(r.Identity) > 0 {
			if enc, err := json.Marshal(r.Identity); err == nil {
				id = string(enc)
			}
		}
		if id == "" {
			continue
		}
		if h.Identifiers[r.Type] == nil {
			h.Identifiers[r.Type] = make(map[string]bool)
		}
		h.Identifiers[r.Type][id] = true
	}

	return h, nil
}
