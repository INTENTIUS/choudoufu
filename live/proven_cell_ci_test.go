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

// provenCellCIAllowlist is the cells #1590 has not yet wired: today's 23
// proven, emulator-or-kind AWS cells that no workflow runs. Each entry
// names the claim and provider it stands in for and points at #1590, which
// is doing the wiring in a parallel branch. This list may only shrink -
// TestProvenCellAllowlistOnlyShrinks fails the moment a listed scenario
// reaches a workflow matrix, so a name can be deleted here but never
// re-added once #1590 (or a later branch) lands it.
var provenCellCIAllowlist = map[string]string{
	"no-silent-orphans":                         "claim 1 (aws); #1590",
	"no-self-managed-locks":                     "claim 2 (aws); #1590",
	"staleness-costs-reads":                     "claim 3 (aws); #1590",
	"recovery-is-a-rerun":                       "claim 5 (aws); #1590",
	"roundtrip":                                 "claim 6 (aws); #1590",
	"identity-is-a-tag":                         "claim 7 (aws); #1590",
	"stock-when-you-need-it":                    "claim 8 (aws); #1590",
	"unchanged-is-free":                         "claim 9 (aws); #1590",
	"cache-serves-the-whole-estate":             "claim 10 (aws); #1590",
	"count-is-a-fungible-set":                   "claim 11 (aws); #1590",
	"carve-by-retag":                            "claim 12 (aws); #1590",
	"the-tag-is-the-boundary":                   "claim 13 (aws); #1590",
	"plan-cost-tracks-the-estate":               "claim 14 (aws); #1590",
	"apply-what-was-approved":                   "claim 15 (aws); #1590",
	"the-boundary-holds-across-regions":         "claim 16 (aws); #1590",
	"record-only-survives-cache-loss":           "claim 17 (aws); #1590",
	"a-shadow-is-not-a-claimant":                "claim 18 (aws); #1590",
	"the-boundary-holds-across-accounts":        "claim 19 (aws); #1590",
	"plan-cost-under-foreign-load":              "claim 20 (aws); #1590",
	"no-secret-survives-in-what-the-tool-keeps": "claim 40 (aws); #1590",
	"the-estate-answers-in-the-present-tense":   "claim 41 (aws); #1590",
	"a-killed-apply-hides-nothing":              "claim 42 (aws); #1590",
	"two-estates-at-once":                       "claim 43 (aws); #1590",
}

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
		if s.Cell.Status != "proven" {
			continue
		}
		proven++

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
			if !strings.Contains(reason, "#1590") {
				t.Errorf("%s: allowlisted with reason %q, which does not name #1590; every entry here points at the issue wiring it", s, reason)
			}
			continue
		}
		t.Errorf("%s: proven and runs on an emulator or kind, but no .github/workflows/*.yml scenario matrix names %q; a claim no job runs is a claim whose \"proven\" cell is one laptop's word, which is how claim 28 stayed red across several merges (#1379)", s, s.Name)
	}
	if proven == 0 {
		t.Fatal("no cell has status proven; this guard is checking nothing")
	}
}

// TestProvenCellAllowlistOnlyShrinks: #1590 is wiring the 23 cells above in
// a parallel branch. The allowlist exists so this guard can land before
// that work finishes, not so an entry can sit there forever - the moment a
// listed scenario appears in a workflow matrix, it has to come out of
// provenCellCIAllowlist, and this test is what makes leaving it in a build
// failure rather than a missed cleanup.
func TestProvenCellAllowlistOnlyShrinks(t *testing.T) {
	wired := provenCellWiredScenarios(t)
	for name, reason := range provenCellCIAllowlist {
		if wired[name] {
			t.Errorf("%q is allowlisted (%s) but now appears in a workflow's scenario matrix; remove it from provenCellCIAllowlist in live/proven_cell_ci_test.go instead of leaving it - the list may only shrink", name, reason)
		}
	}
}
