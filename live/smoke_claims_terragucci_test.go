// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/tools/statpages"
)

// terragucci's rows as extra proofs (GitHub issue #2301, rulings C and E on
// INTENTIUS/chant#4586). terragucci runs choudoufu in its pipelines on its
// harness and records one row per scenario and claim in
// docs-site/src/data/e2e-claims.json. Claims 2, 5, 6, 7, 13, 28 and 45 cite
// those rows in their AWS cells as product-scenario proofs, beside every
// engine proof the cells already had. The scenario tg-estate-overlap is the
// race live/CONCURRENCY.md lists as "Two runs change different resources",
// which replaces #2255's draft.
//
// No row is recorded yet. The proofs name terragucci's commit that wrote the
// scenarios and the release the claims are about; each waits, counting for
// nothing, until a row kept under live/smoke/evidence/product-scenario/ ran
// that release. The tests below hold the citations to the cells #2301 names
// and show, with fixture rows, that a row counts at the claimed release and
// at no other.
//
// Proving it red: drop a proof from claims.json, or move one to another
// cell, and TestTerragucciRowsCiteTheirCells fails; change the version check
// in statpages.ProductScenarioCounts or cellRuns, and
// TestTerragucciRowsCountOnlyAtTheClaimedRelease fails.

// terragucciCells is #2301's table: each claim's AWS cell and the
// terragucci rows it cites, as "<scenario> <claim>".
var terragucciCells = map[int][]string{
	2:  {"tg-estate-overlap choudoufu/rows-overlap", "smoke choudoufu/rows-overlap", "smoke choudoufu/concurrency", "smoke choudoufu/rows-wait"},
	5:  {"smoke choudoufu/rows-takeover", "smoke choudoufu/killed"},
	6:  {"smoke choudoufu/migrate"},
	7:  {"tg-migrate choudoufu/migrate-stock-in"},
	13: {"smoke choudoufu/iam"},
	28: {"smoke choudoufu/shared-bucket", "smoke choudoufu/record-versions"},
	45: {"smoke drift/choudoufu", "smoke-choudoufu drift/tofu"},
}

// terragucciEngineProofs is how many engine proofs (scenarios, steps of
// one, stages, Go tests) each of those AWS cells carried when #2301 added
// the rows. Ruling E keeps every one of them.
var terragucciEngineProofs = map[int]int{2: 4, 5: 1, 6: 2, 7: 2, 13: 5, 28: 4, 45: 1}

func terragucciProofsByCell(t *testing.T, f smokeClaimsFile) (rows map[int][]string, engine map[int]int, proofs []statpages.ProductScenarioProof) {
	t.Helper()
	rows, engine = map[int][]string{}, map[int]int{}
	for _, c := range f.Claims {
		for p, cell := range c.Providers {
			for _, pr := range cell.Proofs {
				ps := pr.ProductScenario
				if ps == nil {
					if p == "aws" {
						engine[c.ID]++
					}
					continue
				}
				if ps.Repo != "INTENTIUS/terragucci" {
					continue
				}
				if p != "aws" {
					t.Errorf("claim %d (%s) cites terragucci %s %s; terragucci's rows run on floci, an AWS cell", c.ID, p, ps.Scenario, ps.Claim)
				}
				rows[c.ID] = append(rows[c.ID], ps.Scenario+" "+ps.Claim)
				proofs = append(proofs, *ps)
			}
		}
	}
	return rows, engine, proofs
}

// TestTerragucciRowsCiteTheirCells: each cell #2301 names cites exactly its
// terragucci rows, no other cell cites one, and every engine proof stayed.
func TestTerragucciRowsCiteTheirCells(t *testing.T) {
	f := readSmokeClaims(t)
	rows, engine, _ := terragucciProofsByCell(t, f)
	for id, want := range terragucciCells {
		got := append([]string(nil), rows[id]...)
		w := append([]string(nil), want...)
		sort.Strings(got)
		sort.Strings(w)
		if strings.Join(got, ",") != strings.Join(w, ",") {
			t.Errorf("claim %d (aws) cites terragucci rows %v; #2301 names %v", id, got, w)
		}
		if engine[id] < terragucciEngineProofs[id] {
			t.Errorf("claim %d (aws) has %d engine proofs, %d before #2301; a product row never replaces an engine proof (ruling E)", id, engine[id], terragucciEngineProofs[id])
		}
	}
	for id, got := range rows {
		if _, ok := terragucciCells[id]; !ok {
			t.Errorf("claim %d cites terragucci rows %v, a cell #2301 does not name", id, got)
		}
	}

	raw, err := os.ReadFile("CONCURRENCY.md")
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "| Two runs change different resources |") {
			row = line
		}
	}
	if !strings.Contains(row, "tg-estate-overlap") || !strings.Contains(row, "INTENTIUS/terragucci") {
		t.Errorf("live/CONCURRENCY.md's \"Two runs change different resources\" row %q does not link terragucci's tg-estate-overlap", row)
	}
}

// TestTerragucciRowsWaitUntilRecorded: in the tree as committed no
// terragucci row is kept, so none of the proofs counts, and each cell still
// rests on a dated engine proof.
func TestTerragucciRowsWaitUntilRecorded(t *testing.T) {
	f := readSmokeClaims(t)
	_, _, proofs := terragucciProofsByCell(t, f)
	claimed, err := statpages.ClaimedRelease("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range proofs {
		rec, err := statpages.ReadProductScenarioRecord("..", p)
		if err != nil {
			t.Fatal(err)
		}
		if rec != nil {
			// A recorded row is held by TestProductScenarioRowsSayWhatTheirProofsSay
			// and counted by TestProvenCellsNameADatedRun.
			continue
		}
		if ok, _ := statpages.ProductScenarioCounts(p, nil, claimed); ok {
			t.Errorf("terragucci %s %s counts with no kept row", p.Scenario, p.Claim)
		}
		line, err := statpages.ProductScenarioLine("..", p, claimed)
		if err != nil {
			t.Fatal(err)
		}
		// A proof that last ran an older release reports that version;
		// one at the claimed release waits for its row.
		want := "waiting for its row"
		if p.EngineVersion != claimed {
			want = "last proven at choudoufu " + p.EngineVersion
		}
		if !strings.Contains(line, want) {
			t.Errorf("terragucci %s %s renders as %q; want it to say %q", p.Scenario, p.Claim, line, want)
		}
	}
}

// TestTerragucciRowsCountOnlyAtTheClaimedRelease keeps a fixture row for
// every terragucci proof in claims.json, in a temp tree, and shows that each
// counts toward its cell at the claimed release and does not count at
// another.
func TestTerragucciRowsCountOnlyAtTheClaimedRelease(t *testing.T) {
	f := readSmokeClaims(t)
	_, _, proofs := terragucciProofsByCell(t, f)
	if len(proofs) == 0 {
		t.Fatal("claims.json cites no terragucci row; this test is checking nothing")
	}
	claimed, err := statpages.ClaimedRelease("..")
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	for _, dir := range []string{statpages.HistoryDir, statpages.ProductScenarioEvidenceDir} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []string{"v0.30.0", "v" + claimed} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(statpages.HistoryDir), v+".json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := func(p statpages.ProductScenarioProof) {
		rec := productRowFor(p)
		rec.RowFile = "docs-site/src/data/e2e-claims.json"
		raw, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(statpages.ProductScenarioEvidenceRel(p))), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, p := range proofs {
		at := p
		at.EngineVersion = claimed
		at.Date = "2026-11-02"
		keep(at)
		rec, err := statpages.ReadProductScenarioRecord(root, at)
		if err != nil || rec == nil {
			t.Fatalf("fixture row for %s %s: %v", at.Scenario, at.Claim, err)
		}
		if ok, why := statpages.ProductScenarioCounts(at, rec, claimed); !ok {
			t.Errorf("terragucci %s %s at %s did not count: %s", at.Scenario, at.Claim, claimed, why)
		}
		line, err := statpages.ProductScenarioLine(root, at, claimed)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(line, "last proven 2026-11-02") || !strings.Contains(line, "/blob/"+at.Commit+"/docs-site/src/data/e2e-claims.json") {
			t.Errorf("terragucci %s %s at the claimed release renders as %q; want a link to its row and its date", at.Scenario, at.Claim, line)
		}

		old := at
		old.EngineVersion = "0.30.0"
		keep(old)
		oldRec, err := statpages.ReadProductScenarioRecord(root, old)
		if err != nil || oldRec == nil {
			t.Fatalf("fixture row for %s %s at 0.30.0: %v", old.Scenario, old.Claim, err)
		}
		if ok, _ := statpages.ProductScenarioCounts(old, oldRec, claimed); ok {
			t.Errorf("terragucci %s %s at 0.30.0 counted toward claims about %s", old.Scenario, old.Claim, claimed)
		}
		line, err = statpages.ProductScenarioLine(root, old, claimed)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(line, "does not count") {
			t.Errorf("terragucci %s %s at 0.30.0 renders as %q; want it named as not counting", old.Scenario, old.Claim, line)
		}
	}

	// The cell count: a kept row at the claimed release adds one dated
	// proof to its cell, and a kept row at another release adds none.
	rows := readGauntletStageRows(t)
	// A release that is not the claimed one; the rows kept above ran claimed.
	otherRelease := "0.0.1"
	kept := map[string]bool{}
	for _, p := range proofs {
		kept[statpages.ProductScenarioEvidenceRel(p)] = true
	}
	withRows := func(path string) string {
		if kept[path] {
			return "2026-11-02"
		}
		return readEvidenceDate(path)
	}
	for _, c := range f.Claims {
		want, ok := terragucciCells[c.ID]
		if !ok {
			continue
		}
		_, before := cellRuns(f, c, "aws", rows, readEvidenceDate, claimed)
		_, at := cellRuns(f, rerunAt(c, claimed), "aws", rows, withRows, claimed)
		_, other := cellRuns(f, rerunAt(c, claimed), "aws", rows, withRows, otherRelease)
		if at-before != len(want) {
			t.Errorf("claim %d (aws): kept rows at %s added %d dated proofs, want %d", c.ID, claimed, at-before, len(want))
		}
		if other != before {
			t.Errorf("claim %d (aws): rows that ran %s counted toward claims about %s (%d dated, %d without them)", c.ID, claimed, otherRelease, other, before)
		}
		if before == 0 {
			t.Errorf("claim %d (aws) has no dated engine proof without terragucci's rows", c.ID)
		}
	}
}

// rerunAt is c with every product-scenario proof citing the engine version
// v, as if terragucci had re-run its scenarios against that release. The
// file itself keeps the version each row ran, so a proof that last ran an
// older release reads as last proven at that version and never fails the suite.
func rerunAt(c smokeClaim, v string) smokeClaim {
	cells := map[string]smokeClaimProviderCell{}
	for name, cell := range c.Providers {
		proofs := make([]smokeProof, len(cell.Proofs))
		for j, p := range cell.Proofs {
			if p.ProductScenario != nil {
				ps := *p.ProductScenario
				ps.EngineVersion = v
				p.ProductScenario = &ps
			}
			proofs[j] = p
		}
		cell.Proofs = proofs
		cells[name] = cell
	}
	c.Providers = cells
	return c
}
