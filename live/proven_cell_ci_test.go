// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A cell in live/smoke/claims.json reads proven only on a dated run.
//
// GitHub issue #1591 first required it of real-account cells: a cell whose
// scenario reaches a real account cannot run on every pull request, so it
// carried dated evidence instead, the way claim 28 stayed red for eighteen
// days while its own row said "proven" (#1379). The other cells were exempt
// on the premise that a workflow ran them. #2018 deleted those workflows,
// so since then no claim scenario runs anywhere except by hand, and the
// exemption read "proven" on nobody's run for about forty proofs.
//
// The rule now covers every proof in a proven or restated cell, whatever
// it runs on. A proof is dated when it is
//
//   - a scenario whose evidence includes a file with a YYYY-MM-DD "date"
//     field (live/smoke/evidence/<name>.lastrun.json, written from the
//     run's two logs);
//   - steps of another claim's scenario (proven_by), whose own proof is
//     dated; or
//   - a gauntlet stage (stage: "<estate>:<stage id>") whose row in
//     live/gauntlet.json reads pass and records the date of that stage's
//     run. It counts only while it reads pass: the day a re-measure turns
//     it red, this test fails until the cell reads unproven.
//
// A cell with any undated proof reads unproven, and keeps its proofs, which
// still say what would prove it. An unproven cell whose every proof is
// dated fails too, so a cell does not stay unproven after its run lands.

// provenCellEvidenceDate matches a lastrun evidence file's "date" field:
// YYYY-MM-DD, the spelling every existing evidence file under
// live/smoke/evidence uses.
var provenCellEvidenceDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// gauntletStageDate matches a stage_runs date in live/gauntlet.json, which
// is an RFC 3339 timestamp; its first ten runes are the day.
var gauntletStageDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`)

const gauntletArtifactPath = "gauntlet.json"

// gauntletStageRow is the part of a live/gauntlet.json estate row a stage
// citation reads.
type gauntletStageRow struct {
	Name      string            `json:"name"`
	Stages    map[string]string `json:"stages"`
	StageRuns map[string]struct {
		Date string `json:"date"`
	} `json:"stage_runs"`
}

func readGauntletStageRows(t *testing.T) map[string]gauntletStageRow {
	t.Helper()
	raw, err := os.ReadFile(gauntletArtifactPath)
	if err != nil {
		t.Fatalf("read %s: %v", gauntletArtifactPath, err)
	}
	var g struct {
		Estates []gauntletStageRow `json:"estates"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decode %s: %v", gauntletArtifactPath, err)
	}
	if len(g.Estates) == 0 {
		t.Fatalf("%s has no estate rows; a stage citation would resolve against nothing", gauntletArtifactPath)
	}
	out := map[string]gauntletStageRow{}
	for _, e := range g.Estates {
		out[e.Name] = e
	}
	return out
}

// splitStageCitation splits "<estate>:<stage id>".
func splitStageCitation(cite string) (estate, stage string, ok bool) {
	estate, stage, ok = strings.Cut(cite, ":")
	return estate, stage, ok && estate != "" && stage != "" && !strings.Contains(stage, ":")
}

// stageCitationProblem says why a stage citation does not resolve to a
// dated pass, or "" when it does. exists reports whether the estate and the
// stage are there at all, whatever they read.
func stageCitationProblem(rows map[string]gauntletStageRow, cite string) (problem string, exists bool) {
	estate, stage, ok := splitStageCitation(cite)
	if !ok {
		return fmt.Sprintf("stage %q is not \"<estate>:<stage id>\"", cite), false
	}
	row, ok := rows[estate]
	if !ok {
		return fmt.Sprintf("stage %q names estate %s, which has no row in live/gauntlet.json", cite, estate), false
	}
	verdict, ok := row.Stages[stage]
	if !ok {
		return fmt.Sprintf("stage %q: estate %s has no stage %s in live/gauntlet.json", cite, estate, stage), false
	}
	if verdict != "pass" {
		return fmt.Sprintf("stage %q reads %s in live/gauntlet.json, not pass", cite, verdict), true
	}
	if !gauntletStageDate.MatchString(row.StageRuns[stage].Date) {
		return fmt.Sprintf("stage %q reads pass and its stage_runs entry carries no date", cite), true
	}
	return "", true
}

// evidenceDate returns the YYYY-MM-DD "date" of an evidence file, or "".
type evidenceDateFunc func(path string) string

func readEvidenceDate(path string) string {
	raw, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(path)))
	if err != nil {
		return ""
	}
	var rec struct {
		Date string `json:"date"`
	}
	if json.Unmarshal(raw, &rec) != nil || !provenCellEvidenceDate.MatchString(rec.Date) {
		return ""
	}
	return rec.Date
}

// scenarioDatedRun returns the newest dated run among a scenario proof's
// evidence files, or "".
func scenarioDatedRun(p smokeProof, date evidenceDateFunc) string {
	newest := ""
	for _, ev := range p.Evidence {
		if d := date(ev); d > newest {
			newest = d
		}
	}
	return newest
}

// undatedProofs lists, one line each, the proofs in one (claim, provider)
// cell that name neither a dated run nor a passing gauntlet stage.
func undatedProofs(f smokeClaimsFile, c smokeClaim, provider string, rows map[string]gauntletStageRow, date evidenceDateFunc) []string {
	byID := map[int]smokeClaim{}
	for _, x := range f.Claims {
		byID[x.ID] = x
	}
	var out []string
	for _, p := range c.Providers[provider].Proofs {
		switch {
		case p.Scenario != "":
			if scenarioDatedRun(p, date) == "" {
				out = append(out, fmt.Sprintf("%s has no evidence file with a YYYY-MM-DD date", filepath.Base(p.Scenario)))
			}
		case p.Stage != "":
			if problem, _ := stageCitationProblem(rows, p.Stage); problem != "" {
				out = append(out, problem)
			}
		case p.ProvenBy != 0:
			var from *smokeProof
			for _, bp := range byID[p.ProvenBy].Providers[provider].scenarios() {
				if bp.Command == p.Command {
					bp := bp
					from = &bp
				}
			}
			if from == nil {
				out = append(out, fmt.Sprintf("proven_by %d names %q, which is no scenario of claim %d on %s", p.ProvenBy, p.Command, p.ProvenBy, provider))
			} else if scenarioDatedRun(*from, date) == "" {
				out = append(out, fmt.Sprintf("proven_by %d: %s has no evidence file with a YYYY-MM-DD date", p.ProvenBy, filepath.Base(from.Scenario)))
			}
		default:
			out = append(out, "a proof with no scenario, stage or proven_by")
		}
	}
	return out
}

// datedRunProblems is the rule over the whole file: a proven or restated
// cell has no undated proof, and an unproven cell has at least one.
func datedRunProblems(f smokeClaimsFile, rows map[string]gauntletStageRow, date evidenceDateFunc) []string {
	var out []string
	for _, c := range f.Claims {
		for _, p := range f.ProviderOrder {
			cell, ok := c.Providers[p]
			if !ok {
				continue
			}
			undated := undatedProofs(f, c, p, rows, date)
			switch cell.Status {
			case "proven", "restated":
				for _, u := range undated {
					out = append(out, fmt.Sprintf("claim %d (%s) reads %s, and %s; a cell is proven on a dated run or a passing stage, so it reads unproven until one lands", c.ID, p, cell.Status, u))
				}
			case "unproven":
				if len(undated) == 0 {
					out = append(out, fmt.Sprintf("claim %d (%s) reads unproven, and every one of its %d proof(s) names a dated run or a passing stage; it reads proven", c.ID, p, len(cell.Proofs)))
				}
			}
		}
	}
	return out
}

// TestProvenCellsNameADatedRun holds claims.json to the rule above.
func TestProvenCellsNameADatedRun(t *testing.T) {
	f := readSmokeClaims(t)
	rows := readGauntletStageRows(t)
	for _, p := range datedRunProblems(f, rows, readEvidenceDate) {
		t.Error(p)
	}
	proven, unproven := 0, 0
	for _, c := range f.Claims {
		for _, cell := range c.Providers {
			switch cell.Status {
			case "proven":
				proven++
			case "unproven":
				unproven++
			}
		}
	}
	if proven == 0 {
		t.Error("no cell reads proven; the rule is checking nothing")
	}
	t.Logf("%d cells read proven, %d unproven: %s", proven, unproven, unprovenCellsSummary(f))
}

// TestStageCitationsResolve: every stage a proof cites, in a cell of any
// status, names an estate row and a stage that live/gauntlet.json carries.
// A citation of an estate that was deleted or folded is a dangling link
// whatever the cell reads.
func TestStageCitationsResolve(t *testing.T) {
	f := readSmokeClaims(t)
	rows := readGauntletStageRows(t)
	for _, c := range f.Claims {
		for p, cell := range c.Providers {
			for _, pr := range cell.Proofs {
				if pr.Stage == "" {
					continue
				}
				if problem, exists := stageCitationProblem(rows, pr.Stage); !exists {
					t.Errorf("claim %d (%s): %s", c.ID, p, problem)
				}
			}
		}
	}
}

// TestDatedRunRuleCatches proves the rule red against fixtures, so a green
// TestProvenCellsNameADatedRun means something. Each case is one way a cell
// used to read proven on nobody's run, and must be reported.
func TestDatedRunRuleCatches(t *testing.T) {
	rows := map[string]gauntletStageRow{
		"est": {Name: "est",
			Stages: map[string]string{"day2_crash": "pass", "day2_remove": "fail", "undated": "pass"},
			StageRuns: map[string]struct {
				Date string `json:"date"`
			}{"day2_crash": {Date: "2026-10-10T07:38:29Z"}, "day2_remove": {Date: "2026-10-10T07:38:29Z"}},
		},
	}
	date := func(path string) string {
		if path == "dated.json" {
			return "2026-10-04"
		}
		return ""
	}
	scen := func(name string, ev ...string) smokeProof {
		return smokeProof{Scenario: "live/smoke/scenarios/" + name + ".sh", Command: "just smoke " + name, Evidence: ev}
	}
	file := func(status string, proofs ...smokeProof) smokeClaimsFile {
		return smokeClaimsFile{ProviderOrder: []string{"aws"}, Claims: []smokeClaim{
			{ID: 1, Providers: map[string]smokeClaimProviderCell{"aws": {Status: status, Proofs: proofs}}},
			{ID: 2, Providers: map[string]smokeClaimProviderCell{"aws": {Status: "proven", Proofs: []smokeProof{scen("other", "dated.json")}}}},
			{ID: 3, Providers: map[string]smokeClaimProviderCell{"aws": {Status: "unproven", Proofs: []smokeProof{scen("bare")}}}},
		}}
	}
	for _, tc := range []struct {
		name   string
		f      smokeClaimsFile
		report bool
	}{
		{"dated scenario", file("proven", scen("a", "dated.json")), false},
		{"scenario with no evidence", file("proven", scen("a")), true},
		{"scenario whose evidence has no date", file("proven", scen("a", "undated.json")), true},
		{"one dated proof beside an undated one", file("proven", scen("a", "dated.json"), scen("b")), true},
		{"restated and undated", file("restated", scen("a")), true},
		{"passing dated stage", file("proven", smokeProof{Stage: "est:day2_crash"}), false},
		{"failing stage", file("proven", smokeProof{Stage: "est:day2_remove"}), true},
		{"passing stage with no run date", file("proven", smokeProof{Stage: "est:undated"}), true},
		{"stage of an estate that is gone", file("proven", smokeProof{Stage: "gone:day2_crash"}), true},
		{"stage citation with no stage", file("proven", smokeProof{Stage: "est"}), true},
		{"proven_by a dated scenario", file("proven", smokeProof{ProvenBy: 2, Command: "just smoke other"}), false},
		{"proven_by an undated scenario", file("proven", smokeProof{ProvenBy: 3, Command: "just smoke bare"}), true},
		{"unproven with every proof dated", file("unproven", scen("a", "dated.json")), true},
		{"unproven and undated", file("unproven", scen("a")), false},
	} {
		got := datedRunProblems(tc.f, rows, date)
		if tc.report && len(got) == 0 {
			t.Errorf("%s: the rule reported nothing", tc.name)
		}
		if !tc.report && len(got) > 0 {
			t.Errorf("%s: the rule reported %v", tc.name, got)
		}
	}
}

// unprovenCellsSummary lists unproven cells by claim, for the log.
func unprovenCellsSummary(f smokeClaimsFile) string {
	var out []string
	for _, c := range f.Claims {
		for p, cell := range c.Providers {
			if cell.Status == "unproven" {
				out = append(out, fmt.Sprintf("%d/%s", c.ID, p))
			}
		}
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
