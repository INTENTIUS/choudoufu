// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"testing"
)

func settingsOf(fs []BucketFinding) []BucketSetting {
	var out []BucketSetting
	for _, f := range fs {
		out = append(out, f.Setting)
	}
	return out
}

func sameSettings(a, b []BucketSetting) bool {
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

// TestAWaiverReachesOnlyWhatItNames is GitHub issue #1340's third acceptance
// item, per setting and not once: with every assertion failing, waiving one
// leaves exactly the other two refused. Run for wrong settings and for
// unreadable ones, since the same waiver answers both.
func TestAWaiverReachesOnlyWhatItNames(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		var allFailing []BucketFinding
		for _, s := range BucketSettings {
			allFailing = append(allFailing, BucketFinding{Setting: s, Unreadable: unreadable, Found: "wrong"})
		}
		for i, waived := range BucketSettings {
			refused, hidden := SplitWaived(allFailing, []string{string(waived)})
			var wantRefused []BucketSetting
			for j, s := range BucketSettings {
				if j != i {
					wantRefused = append(wantRefused, s)
				}
			}
			if !sameSettings(settingsOf(refused), wantRefused) {
				t.Errorf("unreadable=%v, waiving %q: refused %v, want %v", unreadable, waived, settingsOf(refused), wantRefused)
			}
			if !sameSettings(settingsOf(hidden), []BucketSetting{waived}) {
				t.Errorf("unreadable=%v, waiving %q: the waiver hid %v, want only %q", unreadable, waived, settingsOf(hidden), waived)
			}
		}
	}
}

// TestNoWaiverRefusesEverythingAndAPassingBucketNeedsNone pins the two ends.
func TestNoWaiverRefusesEverythingAndAPassingBucketNeedsNone(t *testing.T) {
	var failing, passing []BucketFinding
	for _, s := range BucketSettings {
		failing = append(failing, BucketFinding{Setting: s, Found: "wrong"})
		passing = append(passing, BucketFinding{Setting: s, OK: true})
	}
	if refused, hidden := SplitWaived(failing, nil); len(refused) != len(BucketSettings) || len(hidden) != 0 {
		t.Errorf("no waiver: refused %d, hidden %d, want %d and 0", len(refused), len(hidden), len(BucketSettings))
	}
	all := []string{"versioning", "lifecycle", "public_access_block"}
	if refused, hidden := SplitWaived(passing, all); len(refused) != 0 || len(hidden) != 0 {
		t.Errorf("a passing bucket under a full waiver: refused %d, hidden %d, want 0 and 0 - a waiver hides a failure, it does not invent one", len(refused), len(hidden))
	}
	// A name that is not a setting waives nothing. internal/configs refuses
	// one at decode time; this is the second stop.
	if refused, _ := SplitWaived(failing, []string{"versionning"}); len(refused) != len(BucketSettings) {
		t.Errorf("a misspelt waiver reached %d setting(s)", len(BucketSettings)-len(refused))
	}
}

// TestEveryWaiverStatesItsOwnCost: a generic cost is the "running with
// reduced checks" the issue rules out.
func TestEveryWaiverStatesItsOwnCost(t *testing.T) {
	seen := map[string]BucketSetting{}
	for _, s := range BucketSettings {
		cost := BucketWaiverCost(s)
		if cost == "" || cost == BucketWaiverCost("not-a-setting") {
			t.Errorf("%q has no cost of its own: %q", s, cost)
		}
		if other, dup := seen[cost]; dup {
			t.Errorf("%q and %q state the same cost", s, other)
		}
		seen[cost] = s
	}
}
