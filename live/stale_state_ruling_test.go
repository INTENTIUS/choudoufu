// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stale-state ruling (maintainer, 2026-08-30, recorded on issue #685):
// choudoufu can do anything OpenTofu does as long as the state file can be
// stale, and losing it is a refresh, never a failure. Three lines are the
// operative contract:
//
//	the cache is never consulted for ownership
//	when cache and live disagree, live wins, always
//	losing the cache costs a slower run and nothing else
//
// This guard pins those lines into HANDOFF.md's foundation section, so the
// playbook cannot drift away from the ruling the way the "stateless"
// framing drifted from "allowed to be stale" (#604, #685). The ruling's
// authority is this test plus the issue record, deliberately not a prose
// document: a decision that lives in a document gets renamed, re-homed and
// re-grown (rfc/ became rulings/ and doubled inside two days); a decision
// that lives in a guard fails loudly when someone edits it away.
//
// The guard also pins the known-gap marker: until #685's cache lands,
// HANDOFF must say, next to the ruling, that today's behavior rebuilds
// prior state from live reads and that this is the open defect, not the
// design. When #685 closes, updating the foundation section and this test
// together is the conscious act; this test failing on that day is it
// working, not it being stale.
func TestStaleStateRulingPinnedInHandoff(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "HANDOFF.md"))
	if err != nil {
		t.Fatalf("reading HANDOFF.md: %v", err)
	}
	text := string(b)

	for _, line := range []string{
		"the cache is never consulted for ownership",
		"when cache and live disagree, live wins, always",
		"losing the cache costs a slower run and nothing else",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("HANDOFF.md no longer carries the ruling line %q; the stale-state ruling (#685) is pinned here on purpose - restore the line or bring the ruling's change here consciously", line)
		}
	}

	if !strings.Contains(text, "#685") {
		t.Errorf("HANDOFF.md no longer names #685 next to the ruling; if the cache landed and the defect note was removed, update this guard in the same change - silence is the one state this file refuses")
	}
}
