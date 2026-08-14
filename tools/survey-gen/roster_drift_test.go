// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestRosterStatusAgreesWithAdmission is issue #100 item 1's adjudication
// made enforceable. The SURVEY.md per-type table is deliberate curation -
// which 68 types make up "the top set" is an editorial judgment, and the
// rows carry hand-written evidence prose no data file should flatten - but
// its Status column makes a checkable claim: a `wired` row is admitted by
// the identity table, and a row on any other status is not. Issue #91 was
// exactly that claim drifting silently (aws_instance and aws_key_pair
// admitted while their rows said otherwise, lost in a merge); this test
// makes the recurrence loud.
//
// readRoster is already the other half of the adjudication: it is a strict
// parser with a fixed Path vocabulary, an exact cell count, a required
// schema/docs tier and duplicate detection, so the hand table fails loudly
// on malformation - a data file that happens to render as markdown.
func TestRosterStatusAgreesWithAdmission(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the repository root")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")

	rows, err := readRoster(filepath.Join(root, "live", "SURVEY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("readRoster parsed no rows; the per-type table moved or changed shape")
	}

	// knownConflicts holds the rows where the disagreement is a real open
	// question rather than table drift, each with the issue that owns it. An
	// entry here is exempt from the flip rule but still checked for
	// staleness: the day the conflict resolves, the entry must go.
	knownConflicts := map[string]string{
		// The survey's ops rule excludes it (the secret half is unreadable
		// after create); the identity table admits it ServerAssigned. One
		// of the two is wrong, and it is not a bookkeeping call.
		"aws_iam_access_key": "https://github.com/INTENTIUS/choudoufu/issues/125",
	}

	for _, row := range rows {
		_, admitted := identity.LookupType(row.Type)
		conflict, held := knownConflicts[row.Type]
		disagrees := (row.Status == "wired") != admitted
		switch {
		case held && !disagrees:
			t.Errorf("%s: listed in knownConflicts (%s) but the table and the survey now agree - stale entry, delete it", row.Type, conflict)
		case held:
			// The disagreement is tracked; nothing more to say here.
		case row.Status == "wired" && !admitted:
			t.Errorf("%s: SURVEY.md says wired, but internal/live/identity does not admit it - the table drifted behind an admission removal", row.Type)
		case row.Status != "wired" && admitted:
			t.Errorf("%s: SURVEY.md says %q, but internal/live/identity admits it - flip the row to wired (the #91 drift shape)", row.Type, row.Status)
		}
	}
}
