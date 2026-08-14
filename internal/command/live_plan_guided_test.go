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
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// This file is issue #64's default-on leg: the fork's own commands (plain
// "choudoufu plan"/"choudoufu apply" under a live block, and "choudoufu
// live-plan") now turn guided discovery on by themselves whenever the
// estate has a record store to read a hint from (issue #109's carrier),
// rather than leaving it at [discovery.Request]'s own off-by-default zero
// value. See statelessApplyGuidedDiscovery in live_plan.go for the policy
// and internal/live/discovery/guided.go's file doc comment for the
// mechanics it drives.

// ---------------------------------------------------------------------------
// statelessApplyGuidedDiscovery: the policy, in isolation
// ---------------------------------------------------------------------------

// TestStatelessApplyGuidedDiscovery covers every input the policy branches
// on, with no provider and no live system involved: only the "live" block's
// record_store, the opened store handle, and the opt-out environment
// variable.
func TestStatelessApplyGuidedDiscovery(t *testing.T) {
	liveConfig := func(live *configs.Live) *configs.Config {
		return &configs.Config{Module: &configs.Module{Live: live}}
	}
	openStore := func(t *testing.T) staterecord.Store {
		t.Helper()
		store, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalStore: %s", err)
		}
		return store
	}
	withRecordStore := &configs.Live{
		Estate:      "unit",
		RecordStore: &configs.LiveRecordStore{Type: "local"},
	}

	t.Run("no live block at all", func(t *testing.T) {
		var req discovery.Request
		statelessApplyGuidedDiscovery(&configs.Config{Module: &configs.Module{}}, openStore(t), &req)
		if req.Guided {
			t.Errorf("Guided = true with no live block, want false")
		}
	})

	t.Run("live block with no record_store", func(t *testing.T) {
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(&configs.Live{Estate: "unit"}), nil, &req)
		if req.Guided {
			t.Errorf("Guided = true with no record_store, want false: there is no hint carrier to guide anything")
		}
		if req.HintStore != nil {
			t.Error("HintStore was populated with no record_store configured")
		}
	})

	t.Run("a record_store turns guided discovery on", func(t *testing.T) {
		store := openStore(t)
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(withRecordStore), store, &req)
		if !req.Guided {
			t.Fatalf("Guided = false, want true")
		}
		if req.HintStore == nil {
			t.Error("HintStore is nil, want the opened store")
		}
		if req.GuidedMaxAge != defaultAutoGuidedMaxAge {
			t.Errorf("GuidedMaxAge = %s, want %s", req.GuidedMaxAge, defaultAutoGuidedMaxAge)
		}
		if req.GuidedVerifyAge != defaultAutoGuidedVerifyAge {
			t.Errorf("GuidedVerifyAge = %s, want %s", req.GuidedVerifyAge, defaultAutoGuidedVerifyAge)
		}
	})

	t.Run("a record_store the caller could not open stays off", func(t *testing.T) {
		// The config names a store but the caller passes no handle - the
		// "store would not open" path, which must fall back to today's
		// full sweep rather than engaging with nothing to read.
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(withRecordStore), nil, &req)
		if req.Guided {
			t.Errorf("Guided = true with no opened store, want false")
		}
	})

	t.Run("the opt-out environment variable forces it off", func(t *testing.T) {
		t.Setenv(guidedDiscoveryDisableEnvVar, "1")
		var req discovery.Request
		statelessApplyGuidedDiscovery(liveConfig(withRecordStore), openStore(t), &req)
		if req.Guided {
			t.Errorf("Guided = true with %s set, want false", guidedDiscoveryDisableEnvVar)
		}
		if req.HintStore != nil {
			t.Error("HintStore was populated despite the opt-out")
		}
	})
}

// ---------------------------------------------------------------------------
// End to end: the fallback note and a real, fresh hint
// ---------------------------------------------------------------------------

// TestStatelessMode_guidedDiscoveryFallbackNote runs a plain "choudoufu plan"
// over the live-block-record-store fixture (a record_store configured, no
// hint ever persisted) with no cloud state - the ordinary first run of a new
// estate. Guided discovery engages itself (the record store is enough) but
// has nothing to read yet, so it falls back to today's full sweep, and that
// fallback has to reach the plan output as an informational note rather than
// being silent.
func TestStatelessMode_guidedDiscoveryFallbackNote(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), td)
	t.Chdir(td)

	c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if !strings.Contains(stdout, "Guided discovery: fell back to a full sweep") {
		t.Errorf("no guided-discovery fallback note in the output:\n%s", stdout)
	}
	// Wrap-safe: the view word-wraps the reason, so assert on a fragment
	// short enough to survive any terminal width the test harness picks.
	if !strings.Contains(stdout, "no hint") || !strings.Contains(stdout, "falling back") {
		t.Errorf("the fallback note does not carry discovery's own reason:\n%s", stdout)
	}
}

// TestStatelessMode_guidedDiscoveryOptOut is the same run as
// TestStatelessMode_guidedDiscoveryFallbackNote, but with the opt-out
// environment variable set: guided discovery is never requested at all, so
// there is nothing to fall back from and the note must not appear, even
// though the exact same "no hint has ever been persisted" condition holds.
func TestStatelessMode_guidedDiscoveryOptOut(t *testing.T) {
	t.Setenv(guidedDiscoveryDisableEnvVar, "1")

	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), td)
	t.Chdir(td)

	c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if stdout := output.Stdout(); strings.Contains(stdout, "Guided discovery") {
		t.Errorf("a guided-discovery note appeared with %s set, want none:\n%s", guidedDiscoveryDisableEnvVar, stdout)
	}
}

// TestStatelessMode_guidedDiscoveryEngagesWithFreshHint persists a real,
// fresh, well-formed hint into the fixture's own record store (written
// through the real projection.Manager, never hand-rolled JSON, the same
// discipline internal/live/discovery's own equivalence tests hold to)
// before running the plan. With a hint guided discovery can actually trust,
// the pass engages instead of falling back, so the fallback note must not
// appear.
func TestStatelessMode_guidedDiscoveryEngagesWithFreshHint(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), td)
	t.Chdir(td)

	writeCommandGuidedHintFixture(t, td, "stateless-unit", time.Now())

	c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if stdout := output.Stdout(); strings.Contains(stdout, "Guided discovery") {
		t.Errorf("a guided-discovery fallback note appeared despite a fresh, well-formed hint:\n%s", stdout)
	}
}

// writeCommandGuidedHintFixture persists a real, empty-of-resources hint
// through projection.Manager into the record_store "local" directory the
// fixture's own live block defaults to (a ".tofu-records" directory beside
// the module - see projection.NewRecordStore), for estate, with the given
// writtenAt - enough for guided discovery to read back a fresh, well-formed
// [projection.Hint] and trust it, which is all these tests need: what the
// hint records is not under test here, only whether one is readable at all.
func writeCommandGuidedHintFixture(t *testing.T, moduleDir, estate string, writtenAt time.Time) {
	t.Helper()

	store, err := staterecord.NewLocalStore(filepath.Join(moduleDir, ".tofu-records"))
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}

	m := projection.NewManager()
	m.EnableHint(store, estate, func() time.Time { return writtenAt })
	if err := m.WriteState(states.NewState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}
	if w := m.HintWarning(); len(w) != 0 {
		t.Fatalf("writing the hint fixture warned: %s", w.Err())
	}
}
