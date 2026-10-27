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
	"strings"
	"testing"

	"github.com/intentius/choudoufu/tools/statpages"
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
// The rule covers every proof in a proven cell, whatever it runs on. A proof is dated when it is
//
//   - a scenario whose evidence includes a file with a YYYY-MM-DD "date"
//     field (live/smoke/evidence/<name>.lastrun.json, written from the
//     run's two logs), or whose last_run carries a proven result with a
//     YYYY-MM-DD date. last_run is the last result measured, kept across
//     releases the way live/gauntlet.json keeps a row's last_run
//     (live/claims_last_run_test.go holds each one to git history);
//   - steps of another claim's scenario (proven_by), whose own proof is
//     dated; or
//   - a gauntlet stage (stage: "<estate>:<stage id>") whose row in
//     live/gauntlet.json reads pass and records the date of that stage's
//     run. It counts only while it reads pass: the day a re-measure turns
//     it red, this test fails; or
//   - a Go test (go_test, GitHub issue #2288) whose run tools/gotest-record
//     wrote to live/smoke/evidence/go-test/<test>.json: a CI run at a
//     commit, or a maintainer run with its log.
//
// A promise with an undated proof is no claim: the proof leaves the cell,
// or the claim leaves the list for a GitHub issue. The one exception is a
// Go test proof with no recorded run yet. It is waiting, CLAIMS.md says so,
// and it stands beside the cell's dated proofs without counting as one: a
// cell whose every proof is waiting is not proven.

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
// evidence files and its carried last_run, or "". A last_run counts only
// when it reads proven.
func scenarioDatedRun(p smokeProof, date evidenceDateFunc) string {
	newest := ""
	if lr := p.LastRun; lr != nil && lr.Status == "proven" && provenCellEvidenceDate.MatchString(lr.Date) {
		newest = lr.Date
	}
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
	out, dated := cellRuns(f, c, provider, rows, date)
	if len(c.Providers[provider].Proofs) > 0 && dated == 0 {
		out = append(out, "no proof in the cell names a dated run; a Go test proof waiting for its first recorded run does not count as one")
	}
	return out
}

// cellRuns lists the undated proofs in one cell, as undatedProofs does, and
// counts the dated ones. A waiting Go test proof is neither.
func cellRuns(f smokeClaimsFile, c smokeClaim, provider string, rows map[string]gauntletStageRow, date evidenceDateFunc) (out []string, dated int) {
	byID := map[int]smokeClaim{}
	for _, x := range f.Claims {
		byID[x.ID] = x
	}
	for _, p := range c.Providers[provider].Proofs {
		n := len(out)
		switch {
		case p.Scenario != "":
			if scenarioDatedRun(p, date) == "" {
				out = append(out, fmt.Sprintf("%s has no evidence file with a YYYY-MM-DD date and no proven last_run", filepath.Base(p.Scenario)))
			}
		case p.GoTest != "":
			if date(statpages.GoTestEvidenceRel(p.GoTest)) == "" {
				continue // waiting for its first recorded run
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
				out = append(out, fmt.Sprintf("proven_by %d: %s has no evidence file with a YYYY-MM-DD date and no proven last_run", p.ProvenBy, filepath.Base(from.Scenario)))
			}
		default:
			out = append(out, "a proof with no scenario, stage, proven_by or Go test")
		}
		if len(out) == n {
			dated++
		}
	}
	return out, dated
}

// datedRunProblems is the rule over the whole file: a proven cell has no
// undated proof.
func datedRunProblems(f smokeClaimsFile, rows map[string]gauntletStageRow, date evidenceDateFunc) []string {
	var out []string
	for _, c := range f.Claims {
		for _, p := range f.ProviderOrder {
			cell, ok := c.Providers[p]
			if !ok {
				continue
			}
			if cell.Status != "proven" {
				continue
			}
			for _, u := range undatedProofs(f, c, p, rows, date) {
				out = append(out, fmt.Sprintf("claim %d (%s): %s; a cell is proven on a dated run or a passing stage, and a promise without one is a GitHub issue", c.ID, p, u))
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
	proven := 0
	for _, c := range f.Claims {
		for _, cell := range c.Providers {
			if cell.Status == "proven" {
				proven++
			}
		}
	}
	if proven == 0 {
		t.Error("no cell reads proven; the rule is checking nothing")
	}
	t.Logf("%d cells read proven", proven)
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
		if path == "dated.json" || path == statpages.GoTestEvidenceRel("TestRecorded") {
			return "2026-10-04"
		}
		return ""
	}
	goTest := func(name string) smokeProof { return smokeProof{GoTest: name, GoPackage: "./pkg/x"} }
	scen := func(name string, ev ...string) smokeProof {
		return smokeProof{Scenario: "live/smoke/scenarios/" + name + ".sh", Command: "just smoke " + name, Evidence: ev}
	}
	carried := func(name, status, day string) smokeProof {
		p := scen(name)
		p.LastRun = &smokeLastRun{Status: status, Claim: 1, Commit: "958bcf7f2dc7fd956ecccce8bcc017a9ef81c04a", Date: day, Version: "v0.26.0"}
		return p
	}
	file := func(status string, proofs ...smokeProof) smokeClaimsFile {
		return smokeClaimsFile{ProviderOrder: []string{"aws"}, Claims: []smokeClaim{
			{ID: 1, Providers: map[string]smokeClaimProviderCell{"aws": {Status: status, Proofs: proofs}}},
			{ID: 2, Providers: map[string]smokeClaimProviderCell{"aws": {Status: "proven", Proofs: []smokeProof{scen("other", "dated.json")}}}},
			// Claim 3 is the source of a proven_by case below; the rule skips
			// it, so its undated scenario is reported only through claim 1.
			{ID: 3, Providers: map[string]smokeClaimProviderCell{"aws": {Status: "n/a", Proofs: []smokeProof{scen("bare")}}}},
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
		{"passing dated stage", file("proven", smokeProof{Stage: "est:day2_crash"}), false},
		{"failing stage", file("proven", smokeProof{Stage: "est:day2_remove"}), true},
		{"passing stage with no run date", file("proven", smokeProof{Stage: "est:undated"}), true},
		{"stage of an estate that is gone", file("proven", smokeProof{Stage: "gone:day2_crash"}), true},
		{"stage citation with no stage", file("proven", smokeProof{Stage: "est"}), true},
		{"proven_by a dated scenario", file("proven", smokeProof{ProvenBy: 2, Command: "just smoke other"}), false},
		{"proven_by an undated scenario", file("proven", smokeProof{ProvenBy: 3, Command: "just smoke bare"}), true},
		{"scenario carrying a proven last_run", file("proven", carried("a", "proven", "2026-10-11")), false},
		{"last_run that did not read proven", file("proven", carried("a", "failed", "2026-10-11")), true},
		{"last_run with no date", file("proven", carried("a", "proven", "")), true},
		{"recorded Go test", file("proven", goTest("TestRecorded")), false},
		{"waiting Go test beside a dated scenario", file("proven", scen("a", "dated.json"), goTest("TestWaiting")), false},
		{"waiting Go test alone", file("proven", goTest("TestWaiting")), true},
		{"waiting Go test beside an undated scenario", file("proven", goTest("TestRecorded"), goTest("TestWaiting"), scen("b")), true},
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
