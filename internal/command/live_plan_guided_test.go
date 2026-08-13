// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/states"
)

// This file is issue #64's default-on leg: the fork's own commands (plain
// "choudoufu plan"/"choudoufu apply" under a live block, and "choudoufu
// live-plan") now turn snapshot-guided discovery on by themselves whenever
// the configuration already pays for a snapshot, rather than leaving it at
// [discovery.Request]'s own off-by-default zero value. See
// statelessApplyGuidedDiscovery in live_plan.go for the policy and
// internal/live/discovery/guided.go's file doc comment for the mechanics it
// drives.

// ---------------------------------------------------------------------------
// statelessApplyGuidedDiscovery: the policy, in isolation
// ---------------------------------------------------------------------------

// TestStatelessApplyGuidedDiscovery covers every input the policy branches
// on, with no provider and no live system involved: only the "live" block's
// own snapshot settings and the opt-out environment variable.
func TestStatelessApplyGuidedDiscovery(t *testing.T) {
	liveConfig := func(live *configs.Live) *configs.Config {
		return &configs.Config{Module: &configs.Module{Live: live}}
	}

	t.Run("no live block at all", func(t *testing.T) {
		var req discovery.Request
		statelessApplyGuidedDiscovery(&configs.Config{Module: &configs.Module{}}, &req)
		if req.Guided {
			t.Errorf("Guided = true with no live block, want false")
		}
	})

	t.Run("live block with no snapshot destination", func(t *testing.T) {
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(&configs.Live{Estate: "unit"}), &req)
		if req.Guided {
			t.Errorf("Guided = true with no snapshot_path or snapshots, want false: there is no hint source to guide anything")
		}
		if req.SnapshotPath != "" || req.SnapshotBranchDir != "" {
			t.Errorf("snapshot fields were populated with no destination configured: path=%q branchDir=%q", req.SnapshotPath, req.SnapshotBranchDir)
		}
	})

	t.Run("snapshot_path turns guided discovery on", func(t *testing.T) {
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(&configs.Live{
			Estate:       "unit",
			SnapshotPath: "snapshot/estate.json",
		}), &req)
		if !req.Guided {
			t.Fatalf("Guided = false, want true")
		}
		if req.SnapshotPath != "snapshot/estate.json" {
			t.Errorf("SnapshotPath = %q, want %q", req.SnapshotPath, "snapshot/estate.json")
		}
		if req.SnapshotBranchDir != "" {
			t.Errorf("SnapshotBranchDir = %q, want empty: snapshots (the branch form) was never set", req.SnapshotBranchDir)
		}
		if req.GuidedMaxAge != defaultAutoGuidedMaxAge {
			t.Errorf("GuidedMaxAge = %s, want %s", req.GuidedMaxAge, defaultAutoGuidedMaxAge)
		}
		if req.GuidedVerifyAge != defaultAutoGuidedVerifyAge {
			t.Errorf("GuidedVerifyAge = %s, want %s", req.GuidedVerifyAge, defaultAutoGuidedVerifyAge)
		}
	})

	t.Run("snapshots (the branch form) turns guided discovery on", func(t *testing.T) {
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(&configs.Live{
			Estate:    "unit",
			Snapshots: true,
		}), &req)
		if !req.Guided {
			t.Fatalf("Guided = false, want true")
		}
		if req.SnapshotBranchDir != "." {
			t.Errorf(`SnapshotBranchDir = %q, want "." (the same module directory EnableSnapshotBranch writes through)`, req.SnapshotBranchDir)
		}
		if req.SnapshotPath != "" {
			t.Errorf("SnapshotPath = %q, want empty: snapshot_path was never set", req.SnapshotPath)
		}
	})

	t.Run("both forms set both fields", func(t *testing.T) {
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(&configs.Live{
			Estate:       "unit",
			SnapshotPath: "snapshot/estate.json",
			Snapshots:    true,
		}), &req)
		if !req.Guided || req.SnapshotPath == "" || req.SnapshotBranchDir == "" {
			t.Errorf("Guided=%v SnapshotPath=%q SnapshotBranchDir=%q, want all three set", req.Guided, req.SnapshotPath, req.SnapshotBranchDir)
		}
	})

	t.Run("the opt-out environment variable forces it off", func(t *testing.T) {
		t.Setenv(guidedDiscoveryDisableEnvVar, "1")
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(&configs.Live{
			Estate:       "unit",
			SnapshotPath: "snapshot/estate.json",
			Snapshots:    true,
		}), &req)
		if req.Guided {
			t.Errorf("Guided = true with %s set, want false", guidedDiscoveryDisableEnvVar)
		}
		if req.SnapshotPath != "" || req.SnapshotBranchDir != "" {
			t.Errorf("snapshot fields were populated despite the opt-out: path=%q branchDir=%q", req.SnapshotPath, req.SnapshotBranchDir)
		}
	})
}

// ---------------------------------------------------------------------------
// End to end: the fallback note and a real, fresh hint
// ---------------------------------------------------------------------------

// TestStatelessMode_guidedDiscoveryFallbackNote runs a plain "choudoufu plan"
// over the live-block-snapshot fixture (snapshot_path set, no snapshot ever
// written) with no cloud state and no snapshot file - the ordinary first run
// of a new estate. Guided discovery engages itself (the fixture's snapshot
// destination is enough) but has nothing to read yet, so it falls back to
// today's full sweep, and that fallback has to reach the plan output as an
// informational note rather than being silent.
func TestStatelessMode_guidedDiscoveryFallbackNote(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-snapshot"), td)
	t.Chdir(td)

	c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if !strings.Contains(stdout, "Snapshot-guided discovery: fell back to a full sweep") {
		t.Errorf("no guided-discovery fallback note in the output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "falling back to full enumeration") {
		t.Errorf("the fallback note does not carry discovery's own reason:\n%s", stdout)
	}
}

// TestStatelessMode_guidedDiscoveryOptOut is the same run as
// TestStatelessMode_guidedDiscoveryFallbackNote, but with the opt-out
// environment variable set: guided discovery is never requested at all, so
// there is nothing to fall back from and the note must not appear, even
// though the exact same "no snapshot has ever been written" condition holds.
func TestStatelessMode_guidedDiscoveryOptOut(t *testing.T) {
	t.Setenv(guidedDiscoveryDisableEnvVar, "1")

	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-snapshot"), td)
	t.Chdir(td)

	c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if stdout := output.Stdout(); strings.Contains(stdout, "Snapshot-guided discovery") {
		t.Errorf("a guided-discovery note appeared with %s set, want none:\n%s", guidedDiscoveryDisableEnvVar, stdout)
	}
}

// TestStatelessMode_guidedDiscoveryEngagesWithFreshHint primes a real,
// fresh, well-formed snapshot at the fixture's own snapshot_path (written
// through the real projection.Manager, never hand-rolled JSON, the same
// discipline internal/live/discovery's own equivalence tests hold to) before
// running the plan. With a hint guided discovery can actually trust, the
// pass engages instead of falling back, so the fallback note must not
// appear.
func TestStatelessMode_guidedDiscoveryEngagesWithFreshHint(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-snapshot"), td)
	t.Chdir(td)

	snapPath := filepath.Join(td, "snapshot", "estate.json")
	writeCommandGuidedHintFixture(t, snapPath, "stateless-unit", time.Now())

	c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if stdout := output.Stdout(); strings.Contains(stdout, "Snapshot-guided discovery") {
		t.Errorf("a guided-discovery fallback note appeared despite a fresh, well-formed hint:\n%s", stdout)
	}
}

// writeCommandGuidedHintFixture writes a real, empty-of-resources snapshot
// through projection.Manager at path, for estate, with the given writtenAt -
// enough for guided discovery to read back a fresh, well-formed [Hint] and
// trust it, which is all these tests need: what the hint records is not
// under test here, only whether one is readable at all.
func writeCommandGuidedHintFixture(t *testing.T, path, estate string, writtenAt time.Time) {
	t.Helper()

	m := projection.NewManager()
	m.EnableSnapshot(path, estate, func() time.Time { return writtenAt })
	if err := m.WriteState(states.NewState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}
}
