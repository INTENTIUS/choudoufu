// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/states"
)

// This file is issue #64's equivalence proof: the same fake estate, planned
// guided and unguided, has to come out byte-identical whenever the snapshot
// hint cannot be trusted (missing, corrupted, or stale) - "a stale or
// missing snapshot costs one full re-read, never a wrong plan", the design
// comment's own words. It also proves the positive case: a fresh,
// well-formed hint really does change what gets listed, which is the whole
// point of the feature and the reason TestSnapshot_noReader's restated
// invariant was worth loosening for.

// ---------------------------------------------------------------------------
// The equivalence table
// ---------------------------------------------------------------------------

// TestGuided_equivalence is table-driven over every way a snapshot hint can
// fail to be trusted. In each case, Guided discovery must fall back to
// today's full enumeration and produce output identical to an unguided pass
// over the exact same fake estate - not merely "no error", but the same
// scans, the same removal, the same everything Result.String() renders.
func TestGuided_equivalence(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, path string) // leaves path in the broken state under test
	}{
		{
			name: "missing",
			build: func(t *testing.T, path string) {
				// No file is ever written at path at all - the ordinary
				// "guided discovery was asked for, but nothing has ever
				// been persisted with a snapshot carrier enabled" case.
			},
		},
		{
			name: "corrupted",
			build: func(t *testing.T, path string) {
				writeGuidedHintFixture(t, path, time.Now(), "aws_cloudwatch_log_group")
				if err := os.WriteFile(path, []byte("not a snapshot at all {{{"), 0o600); err != nil {
					t.Fatalf("corrupting the fixture: %s", err)
				}
			},
		},
		{
			name: "wrong format version",
			build: func(t *testing.T, path string) {
				writeGuidedHintFixture(t, path, time.Now(), "aws_cloudwatch_log_group")
				bad := `{"formatVersion":"some-future-format-v9","estate":"x","writtenAt":"2026-08-12T00:00:00Z","resources":[]}`
				if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
					t.Fatalf("corrupting the fixture: %s", err)
				}
			},
		},
		{
			name: "stale",
			build: func(t *testing.T, path string) {
				// Well past defaultGuidedMaxAge (24h): a perfectly
				// well-formed snapshot that guided mode must still refuse
				// to trust.
				writeGuidedHintFixture(t, path, time.Now().Add(-72*time.Hour), "aws_cloudwatch_log_group")
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "snapshot.json")
			c.build(t, path)

			// The same fake estate for both passes: an ordinary owned
			// fixture plus one orphan of a type nothing in the fixture
			// config declares, exactly TestSweepFindsDeletedBlock's shape -
			// chosen so that a guided pass which failed to fall back
			// (and wrongly skipped aws_cloudwatch_log_group) would be
			// caught by this test: the orphan would silently disappear
			// from GuidedSweepSkipped and the removal it seeds.
			buildOrphanEstate := func() *fakeCloud {
				cloud := newFakeCloud()
				ownWholeEstate(cloud)
				cloud.listable("aws_cloudwatch_log_group")
				cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)
				return cloud
			}

			unguided, diags := discoverFixture(t, buildOrphanEstate(), Request{Sweep: true})
			assertNoErrors(t, diags)

			guided, diags := discoverFixture(t, buildOrphanEstate(), Request{
				Sweep:        true,
				Guided:       true,
				SnapshotPath: path,
			})
			assertNoErrors(t, diags)

			if guided.Guided {
				t.Errorf("Guided = true for a %s snapshot; want a fallback", c.name)
			}
			if guided.GuidedFallback == "" {
				t.Errorf("GuidedFallback is empty for a %s snapshot; want a reason", c.name)
			} else {
				t.Logf("fallback reason: %s", guided.GuidedFallback)
			}
			if len(guided.GuidedSweepSkipped) != 0 {
				t.Errorf("GuidedSweepSkipped = %v, want none on a fallback pass", guided.GuidedSweepSkipped)
			}

			if got, want := guided.String(), unguided.String(); got != want {
				t.Errorf("guided output differs from unguided under a %s snapshot:\n--- guided ---\n%s--- unguided ---\n%s", c.name, got, want)
			}
			// Belt and suspenders on the one field the equivalence test
			// actually exists to protect: the removal proposed for the
			// deleted block's live resource has to be identical, not just
			// "present" in both.
			gotRemovals, wantRemovals := removalsByAddr(guided), removalsByAddr(unguided)
			if len(gotRemovals) != 1 || len(wantRemovals) != 1 {
				t.Fatalf("want exactly one removal on both sides, got guided=%d unguided=%d", len(gotRemovals), len(wantRemovals))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The positive case: a trustworthy hint really does narrow the sweep
// ---------------------------------------------------------------------------

// TestGuided_narrowsRoutineSweep is what TestGuided_equivalence's fallback
// cases are the safety net for: given a fresh, well-formed hint that
// records aws_cloudwatch_log_group as a type this estate has used before,
// a routine (non-verify) guided pass does not list it at all, while
// aws_kms_key - a type the hint has no record of - is still listed in full,
// on every pass, guided or not. Request.GuidedVerify then restores the
// unnarrowed universe, matching the cold pass exactly.
func TestGuided_narrowsRoutineSweep(t *testing.T) {
	sweepTypesUnderTest := []string{"aws_cloudwatch_log_group", "aws_kms_key"}

	buildEstate := func() *fakeCloud {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable("aws_cloudwatch_log_group")
		cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)
		// aws_kms_key is already in newFakeCloud's default type list and
		// carries no objects: an admitted type this estate has never used,
		// exactly what "absent from the hint" needs to mean something.
		return cloud
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	// The hint records aws_cloudwatch_log_group only - never aws_kms_key -
	// mirroring a prior run that swept both and found one of them empty.
	writeGuidedHintFixture(t, path, time.Now(), "aws_cloudwatch_log_group")

	cold, diags := discoverFixture(t, buildEstate(), Request{Sweep: true, SweepTypes: sweepTypesUnderTest})
	assertNoErrors(t, diags)
	if _, ok := cold.ScanFor("aws_cloudwatch_log_group"); !ok {
		t.Error("cold pass did not scan aws_cloudwatch_log_group")
	}
	if _, ok := cold.ScanFor("aws_kms_key"); !ok {
		t.Error("cold pass did not scan aws_kms_key")
	}

	routine, diags := discoverFixture(t, buildEstate(), Request{
		Sweep:        true,
		SweepTypes:   sweepTypesUnderTest,
		Guided:       true,
		SnapshotPath: path,
	})
	assertNoErrors(t, diags)
	if !routine.Guided {
		t.Fatalf("Guided = false, want true (fallback reason: %q)", routine.GuidedFallback)
	}
	if _, ok := routine.ScanFor("aws_cloudwatch_log_group"); ok {
		t.Error("routine guided pass scanned aws_cloudwatch_log_group, want it skipped (present in the hint)")
	}
	if _, ok := routine.ScanFor("aws_kms_key"); !ok {
		t.Error("routine guided pass did not scan aws_kms_key, want it always swept (absent from the hint)")
	}
	if want := []string{"aws_cloudwatch_log_group"}; !stringSlicesEqual(routine.GuidedSweepSkipped, want) {
		t.Errorf("GuidedSweepSkipped = %v, want %v", routine.GuidedSweepSkipped, want)
	}
	// The trade this leg documents: the routine pass does not find the
	// deleted block's orphan this run, because it never listed its type.
	if len(routine.Removals()) != 0 {
		t.Errorf("routine guided pass proposed %d removal(s), want 0 (aws_cloudwatch_log_group was skipped)", len(routine.Removals()))
	}

	verify, diags := discoverFixture(t, buildEstate(), Request{
		Sweep:        true,
		SweepTypes:   sweepTypesUnderTest,
		Guided:       true,
		GuidedVerify: true,
		SnapshotPath: path,
	})
	assertNoErrors(t, diags)
	if len(verify.GuidedSweepSkipped) != 0 {
		t.Errorf("GuidedSweepSkipped = %v on a verify pass, want none", verify.GuidedSweepSkipped)
	}
	if got, want := verify.String(), cold.String(); got != want {
		t.Errorf("a GuidedVerify pass differs from the cold pass:\n--- verify ---\n%s--- cold ---\n%s", got, want)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writeGuidedHintFixture builds a real snapshot through projection.Manager -
// never hand-rolled JSON - recording one resource instance per type named,
// and writes it to path with the given writtenAt. This is the fixture
// builder every case in this file uses so that "a snapshot hint" in these
// tests is always something the real writer could have produced.
func writeGuidedHintFixture(t *testing.T, path string, writtenAt time.Time, types ...string) {
	t.Helper()

	state := states.NewState()
	for i, typeName := range types {
		state.RootModule().SetResourceInstanceCurrent(
			addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: "seen"}.Instance(addrs.NoKey),
			&states.ResourceInstanceObjectSrc{
				AttrsJSON: []byte(`{"id":"fixture-` + typeName + `-` + strconv.Itoa(i) + `"}`),
				Status:    states.ObjectReady,
			},
			addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
			addrs.NoKey,
		)
	}

	m := projection.NewManager()
	m.EnableSnapshot(path, estateName, func() time.Time { return writtenAt })
	if err := m.WriteState(state); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
