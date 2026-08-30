// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RegressionsPath is the hand-authored acknowledgment ledger for a
// legitimate stage regression - the one place one is recorded, in the same
// change that causes it, so a reviewer sees it in the diff.
//
// It exists because live/gauntlet.json itself may never be hand-edited
// (HANDOFF.md's worker brief: "What you must not do"): #552 could land a
// cohort's legitimate resource-count shrink as a hand edit to the
// generated cohort-acceptance.json directly, but the equivalent move here
// would mean hand-typing a verdict into the one artifact this whole tool
// exists to measure honestly, which is exactly the thing this repo does
// not do. A separate, small, explicitly-hand-authored file gets the same
// property - a human's deliberate acknowledgment, visible in the PR diff,
// landed alongside the regression it explains - without ever touching the
// generated artifact by hand.
const RegressionsPath = "live/gauntlet/regressions.json"

// Regression is one acknowledged stage regression: a human's record that a
// specific estate's specific stage was expected to move off "pass" in this
// change, and why.
type Regression struct {
	Estate string `json:"estate"`
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
	Issue  string `json:"issue,omitempty"`
}

// LoadRegressions reads the acknowledgment ledger. A missing file reads as
// no acknowledgments, the same "absence is not evidence of anything but
// absence" rule LoadArtifact and LoadBehaviorIndex already follow.
func LoadRegressions(root string) ([]Regression, error) {
	b, err := os.ReadFile(filepath.Join(root, RegressionsPath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var regs []Regression
	if err := json.Unmarshal(b, &regs); err != nil {
		return nil, fmt.Errorf("%s: %w", RegressionsPath, err)
	}
	return regs, nil
}

// RegressionViolation is one estate/stage pair that regressed from a
// recorded pass with nothing on file explaining why.
type RegressionViolation struct {
	Estate string
	Stage  string
	From   string
	To     string
}

func (v RegressionViolation) Error() string {
	return fmt.Sprintf("%s: %s regressed from %s to %s with no matching entry in %s", v.Estate, v.Stage, v.From, v.To, RegressionsPath)
}

// RatchetViolations is the gauntlet layer's counterpart to
// internal/live/acceptance's ratchetViolations (#539/#552): a pure
// function, no I/O, so a test can demonstrate it firing without a live
// docker run - see ratchet_test.go's doc comments for why that split
// matters (the same reasoning as tools/row-gen/retraction_test.go's
// retractionRefusal and acceptance's own ratchetViolations: a helper that
// prints or fails directly can only be proven red by leaving an
// actually-failing case in the suite, which is not a demonstration this
// repository commits).
//
// committed is the artifact as it stood on disk before this run (read
// fresh, never the in-memory Artifact RunEstates goes on to mutate - see
// cmdRun); current is the same estates' rows after this run. Only a stage
// COMMITTED as pass counts: cohorts' enforceRatchet made exactly this call
// (walk the committed passing set, not the current results) because a
// fixture that stops producing a verdict at all is the easiest way for a
// pass to silently stop happening, and the same is true here for an
// estate dropped from the manifest or a stage the script no longer
// reaches. fail -> fail, fail -> not_run, not_run -> fail and so on are
// not regressions: the committed artifact never promised those, so this
// run cannot break a promise it never made.
//
// An estate present in committed but absent from current is skipped, not
// flagged - RunEstates only mutates rows for the estates it was asked to
// run, and every row this run did not touch is byte-for-byte identical
// between committed and current already (Artifact.Rebuild carries an
// unmutated row forward untouched), so it produces no violation on its own
// merits; a caller that only ever compares whole-artifact snapshots (as
// cmdRun does) never needs this case to fire, but a caller that hands in a
// filtered current slice should not have "I only asked about a subset" read
// back as "the rest regressed".
func RatchetViolations(committed, current []EstateResult) []RegressionViolation {
	cur := map[string]EstateResult{}
	for _, r := range current {
		cur[r.Name] = r
	}
	var out []RegressionViolation
	for _, c := range committed {
		r, ok := cur[c.Name]
		if !ok {
			continue
		}
		stageIDs := make([]string, 0, len(c.Stages))
		for id := range c.Stages {
			stageIDs = append(stageIDs, id)
		}
		sort.Strings(stageIDs)
		for _, id := range stageIDs {
			verdict := c.Stages[id]
			if verdict != VerdictPass {
				continue
			}
			now, ok := r.Stages[id]
			if !ok {
				// The stage no longer exists in this run's row - Rebuild
				// already deletes verdicts for retired stage ids before
				// this comparison would ever see one in practice, but a
				// defensive skip costs nothing and cannot hide a real
				// regression: a retired stage cannot still be "pass".
				continue
			}
			if now != VerdictPass {
				out = append(out, RegressionViolation{Estate: c.Name, Stage: id, From: verdict, To: now})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Estate != out[j].Estate {
			return out[i].Estate < out[j].Estate
		}
		return out[i].Stage < out[j].Stage
	})
	return out
}

// UnacknowledgedViolations removes any violation with a matching
// (estate, stage) entry in acks, returning the rest - the ones a run must
// still fail on.
//
// Matching ignores From/To on purpose: an acknowledgment written for one
// pass -> fail transition on an estate/stage pair is intended to cover
// landing that transition, once, in the change that earns it. The known
// gap this leaves: if the artifact is not re-measured for a while and the
// same estate/stage genuinely regresses a SECOND time later, a
// still-present old entry would silently swallow the new regression too,
// since nothing here expires an entry once its transition has landed. That
// risk is accepted rather than solved by, e.g., pinning the acknowledged
// commit or verdict pair - see the PR for this file's reasoning about why
// that extra machinery was not worth building for a first version of this
// guard; a human removing an entry once it has served its purpose is the
// mitigation today, not a mechanical one.
func UnacknowledgedViolations(violations []RegressionViolation, acks []Regression) []RegressionViolation {
	acked := map[[2]string]bool{}
	for _, a := range acks {
		acked[[2]string{a.Estate, a.Stage}] = true
	}
	var out []RegressionViolation
	for _, v := range violations {
		if acked[[2]string{v.Estate, v.Stage}] {
			continue
		}
		out = append(out, v)
	}
	return out
}
