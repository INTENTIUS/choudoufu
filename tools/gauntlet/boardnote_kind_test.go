// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// TestLastRunNoteReadsKindSubstrateImage is issue #1700's guard.
// lastRunNote predates the Emulator/SubstrateImage split (#1594, #1627): it
// switches only on r.LastRun.Emulator, which a kind-substrate row leaves
// empty on purpose, recording what it actually ran against in
// r.LastRun.SubstrateImage instead. Before this fix every kind-lane
// estate's page rendered "This run's emulator image was not recorded" even
// on a run that recorded exactly what it ran against.
//
// providerNote (board.go) already gets this shape right for the provider
// pins: branch on r.Substrate == SubstrateKind, read the Kubernetes-shaped
// field. lastRunNote needs the same branch, reading SubstrateImage and
// comparing it against the current kind-node-image pin instead of
// a.Emulator.
func TestLastRunNoteReadsKindSubstrateImage(t *testing.T) {
	const pin = "kindest/node:v1.37.0@sha256:currentpin00000000000000000000000000000000000000000000000000"
	const stale = "kindest/node:v1.36.0@sha256:stalepin000000000000000000000000000000000000000000000000000"

	matching := EstateResult{
		Name:      "k8s-one",
		Substrate: SubstrateKind,
		Protocol:  ProtocolGauntlet,
		LastRun: &LastRun{
			Commit:         "abc1234",
			Date:           "2026-09-27T00:00:00Z",
			ExitCode:       0,
			SubstrateImage: pin,
		},
	}
	note, legacy := lastRunNote(matching, &Artifact{Emulator: "ghcr.io/lex00/floci@sha256:unrelated"}, pin)
	if legacy != "" {
		t.Fatalf("legacy = %q, want empty", legacy)
	}
	if strings.Contains(note, "was not recorded") {
		t.Errorf("note claims the substrate image was not recorded, but LastRun.SubstrateImage = %q: %q", pin, note)
	}
	if !strings.Contains(note, pin) {
		t.Errorf("note does not name the recorded substrate image %q: %q", pin, note)
	}
	if strings.Contains(note, "Stale") {
		t.Errorf("note says Stale for a row that matches the current kind-node-image pin: %q", note)
	}

	staleRow := matching
	staleRow.LastRun = &LastRun{
		Commit:         "abc1234",
		Date:           "2026-09-27T00:00:00Z",
		ExitCode:       0,
		SubstrateImage: stale,
	}
	staleNote, _ := lastRunNote(staleRow, &Artifact{}, pin)
	if !strings.Contains(staleNote, "Stale") {
		t.Errorf("note does not say Stale for a row measured against %q when the current pin is %q: %q", stale, pin, staleNote)
	}
	if !strings.Contains(staleNote, pin) {
		t.Errorf("stale note does not name the current pin %q: %q", pin, staleNote)
	}

	unrecorded := matching
	unrecorded.LastRun = &LastRun{Commit: "abc1234", Date: "2026-09-27T00:00:00Z", ExitCode: 0}
	unrecordedNote, _ := lastRunNote(unrecorded, &Artifact{}, pin)
	if !strings.Contains(unrecordedNote, "was not recorded") {
		t.Errorf("note should say the substrate image was not recorded when SubstrateImage is empty: %q", unrecordedNote)
	}
}
