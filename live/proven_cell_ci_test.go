// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// GitHub issue #1591. live/bucket_ci_test.go and live/k8s_ci_test.go each
// hold one workflow to the scenario directory, but nothing holds
// live/smoke/claims.json's "proven" cells to either workflow, or to any
// workflow a third substrate might add. #1590 found 23 AWS cells that no
// job anywhere re-checks: the claims table called them proven on the
// strength of a hand run, the way claim 28 stayed red for eighteen days
// while its own row said "proven" (#1379).
//
// The rule this file enforces: a cell whose scenario runs on the pinned
// emulator or on a kind cluster proves nothing unless some workflow's
// matrix runs it; a cell whose scenario reaches a real account cannot run
// on every pull request (CLAUDE.md: a paid run is the maintainer's to
// start), so it carries dated evidence instead. Which kind a cell is comes
// from the cell itself (real_service), not from its provider name, so this
// keeps working the day a second real-account provider exists.
//
// Proving it red: remove one entry from k8s-smoke.yml's or bucket-smoke.yml's
// scenario matrix - the guard names the claim and provider that entry was
// the only proof of.
//
// provenCellWiredScenarios reads every entry under a `scenario:` matrix in
// any .github/workflows/*.yml file, not just the two that exist today - so
// #1590's claims-smoke.yml, once it lands, needs no change here.

// provenCellCIAllowlist is the cells #1590 measured but could not wire into
// claims-smoke.yml's matrix: each fails against the pinned emulator for a
// reason that is its own finding, not #1590's to fix. Every other cell
// #1591 found unwired (claims 1, 2, 3, 5-20, 40, 41, 42, 43) now runs in
// claims-smoke.yml (see that file's header) and is out of this list.  This
// list may only shrink - TestProvenCellAllowlistOnlyShrinks fails the
// moment a listed scenario reaches a workflow matrix, so a name can be
// deleted here but never re-added once its finding is fixed and it lands.
var provenCellCIAllowlist = map[string]string{
	// #1636: claim 13's BREAK control false-positives on an unrelated
	// sweep warning that happens to contain "AccessDenied".
	"the-tag-is-the-boundary": "claim 13 (aws); #1636",
	// #1637: claim 17 fails at the first apply, both arms, on #950's
	// node-path unmarked-apply refusal, which fires before any record can
	// exist for this claim's untaggable, record-recoverable resource to
	// be exempted by.
	"record-only-survives-cache-loss": "claim 17 (aws); #1637",
}

// provenCellIssueRef matches a GitHub issue reference (#1590, #1636, ...),
// so an allowlist reason can point at whichever issue is tracking it -
// #1590 while a cell is simply not wired yet, or a fix-it issue of its own
// once #1590 measured it and found the scenario itself broken.
var provenCellIssueRef = regexp.MustCompile(`#\d+`)

// provenCellEvidenceDate matches a lastrun evidence file's "date" field:
// YYYY-MM-DD, the spelling every existing evidence file under
// live/smoke/evidence uses.
var provenCellEvidenceDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// provenCellScenarioBlock matches a `scenario:` matrix key and the
// `- name` entries immediately under it, the shape both bucket-smoke.yml
// and k8s-smoke.yml use. It is applied to every workflow file rather than
// to one named file, so a new emulator-or-kind workflow (#1590's
// claims-smoke.yml) is picked up the moment it exists, with no edit here.
var provenCellScenarioBlock = regexp.MustCompile(`(?m)^[ \t]*scenario:[ \t]*\n((?:[ \t]+- [a-z0-9-]+[ \t]*\n)+)`)

// provenCellScenarioEntry pulls one `- name` line out of a matched block.
var provenCellScenarioEntry = regexp.MustCompile(`(?m)^[ \t]+- ([a-z0-9-]+)[ \t]*$`)

const claimsWorkflowsDir = "../.github/workflows"

// provenCellWiredScenarios is the union of every scenario matrix entry in
// every .github/workflows/*.yml file.
func provenCellWiredScenarios(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(claimsWorkflowsDir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	files := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		files++
		raw, err := os.ReadFile(filepath.Join(claimsWorkflowsDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range provenCellScenarioBlock.FindAllStringSubmatch(string(raw), -1) {
			for _, m := range provenCellScenarioEntry.FindAllStringSubmatch(block[1], -1) {
				out[m[1]] = true
			}
		}
	}
	if files == 0 {
		t.Fatalf("found no .yml files under %s; this guard is looking in the wrong place", claimsWorkflowsDir)
	}
	return out
}

// cellRunsItsOwnScenario is true for a cell whose status rests on a
// scenario of its own: proven, or restated with a scenario (the promise
// holds in a weaker form, and that scenario is what shows the weaker form
// holds - claim 7 on Kubernetes, claims 25 and 26 on AWS, #1599). Both are
// a "this runs" statement in the claims table, so both are held to a
// workflow. Before #1599 only "proven" was, and a restated cell's scenario
// could sit in no matrix with nothing saying so.
func cellRunsItsOwnScenario(c smokeClaimProviderCell) bool {
	return c.Scenario != "" && (c.Status == "proven" || c.Status == "restated")
}

// TestProvenCellsRunInAWorkflowOrCarryDatedEvidence is the guard #1591
// asks for: every proven cell that carries its own scenario either has
// that scenario's name in some workflow's matrix, or, if the cell is
// real_service (a real account, never run on every pull request), carries an
// evidence file with a YYYY-MM-DD date. Anything else is the allowlist's
// job, not silence.
func TestProvenCellsRunInAWorkflowOrCarryDatedEvidence(t *testing.T) {
	f := readSmokeClaims(t)
	wired := provenCellWiredScenarios(t)
	if len(wired) < 10 {
		t.Fatalf("found only %d wired scenario names across .github/workflows/*.yml, expected at least 10 (bucket-smoke.yml and k8s-smoke.yml alone carry more); this guard is looking in the wrong place", len(wired))
	}

	proven := 0
	for _, s := range smokeScenarioCells(f) {
		if !cellRunsItsOwnScenario(s.Cell) {
			continue
		}
		if s.Cell.Status == "proven" {
			proven++
		}

		if s.Cell.RealService {
			if len(s.Cell.Evidence) == 0 {
				t.Errorf("%s: proven and real_service, so it never runs in a workflow, but carries no evidence file", s)
				continue
			}
			dated := false
			for _, ev := range s.Cell.Evidence {
				raw, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(ev)))
				if err != nil {
					// live/smoke_claims_test.go already reports a missing
					// evidence file; this test only checks the date.
					continue
				}
				var rec struct {
					Date string `json:"date"`
				}
				if err := json.Unmarshal(raw, &rec); err != nil {
					continue
				}
				if provenCellEvidenceDate.MatchString(rec.Date) {
					dated = true
				}
			}
			if !dated {
				t.Errorf("%s: proven and real_service, but none of %v carries a YYYY-MM-DD \"date\" field; a claim resting on a hand run has to say when it ran", s, s.Cell.Evidence)
			}
			continue
		}

		if wired[s.Name] {
			continue
		}
		if reason, ok := provenCellCIAllowlist[s.Name]; ok {
			if !provenCellIssueRef.MatchString(reason) {
				t.Errorf("%s: allowlisted with reason %q, which names no issue; every entry here points at the issue tracking its wiring or its fix", s, reason)
			}
			continue
		}
		t.Errorf("%s: %s on a scenario that runs on an emulator or kind, but no .github/workflows/*.yml scenario matrix names %q; a claim no job runs is a claim whose %q cell is one laptop's word, which is how claim 28 stayed red across several merges (#1379)", s, s.Cell.Status, s.Name, s.Cell.Status)
	}
	if proven == 0 {
		t.Fatal("no cell has status proven; this guard is checking nothing")
	}
}

// TestProvenCellAllowlistOnlyShrinks: the allowlist exists so a cell can be
// measured and found genuinely broken (its own finding, #1636 or #1637)
// without either adding a red scenario to a matrix or silently dropping it.
// It is not a place to leave a cell forever - the moment a listed scenario
// appears in a workflow matrix, it has to come out of provenCellCIAllowlist,
// and this test is what makes leaving it in a build failure rather than a
// missed cleanup.
func TestProvenCellAllowlistOnlyShrinks(t *testing.T) {
	wired := provenCellWiredScenarios(t)
	for name, reason := range provenCellCIAllowlist {
		if wired[name] {
			t.Errorf("%q is allowlisted (%s) but now appears in a workflow's scenario matrix; remove it from provenCellCIAllowlist in live/proven_cell_ci_test.go instead of leaving it - the list may only shrink", name, reason)
		}
	}
}
