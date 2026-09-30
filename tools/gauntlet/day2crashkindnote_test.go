// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// TestDay2CrashKindNoteSaysWhatItDoesNotInterrupt is GitHub issue #1683's
// first part. day2_crash's kind note said the create-before-destroy window
// the emulator's half interrupts "does not exist here (see day2_replace)".
// Since #1641 switched day2_replace on for kind, it does: a
// create_before_destroy rename creates the new object, carrying the
// block's address annotation, before destroying the old one. The kind
// stage still interrupts a multi-object apply instead, so the note has to
// say plainly that the rename window exists and is not interrupted here,
// and point at #1683.
//
// The same note said a same-estate rename "writes nothing on the cluster
// at all (#1066)", which stopped being true with #1639: the rename
// rewrites the address annotation, and on a kubernetes_manifest object it
// is two requests, a window whose recovery is #1764.
func TestDay2CrashKindNoteSaysWhatItDoesNotInterrupt(t *testing.T) {
	var reason string
	for _, s := range Stages() {
		if s.ID == "day2_crash" {
			reason = s.Substrates[SubstrateKind]
		}
	}
	if reason == "" {
		t.Fatal("day2_crash has no kind note")
	}
	for _, stale := range []string{
		"does not exist here",
		"writes nothing on the cluster at all",
	} {
		if strings.Contains(reason, stale) {
			t.Errorf("day2_crash's kind note still says %q: %s", stale, reason)
		}
	}
	for _, want := range []string{"create_before_destroy", "does not interrupt", "1683", "1639", "1764"} {
		if !strings.Contains(reason, want) {
			t.Errorf("day2_crash's kind note does not say %q: %s", want, reason)
		}
	}
}
