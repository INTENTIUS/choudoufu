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
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/tools/statpages"
)

// A product-scenario proof (product_scenario, GitHub issue #2299, ruling C
// on INTENTIUS/chant#4586) cites one recorded row of a CI product's
// scenario run: terragucci, plywood or sql-yodeler ran a scenario on the
// shared e2e harness against a released choudoufu, a plain run passed and
// a BREAK=1 run was caught, and the product committed a row for it. The
// proof names the row by six fields: repo, scenario, claim, commit, date and
// engine_version.
//
// In claims.json the proof is one flat object with "kind": "product-scenario"
// and the keys repo, scenario, claim, commit, date, engine_version and note:
// the kind and keys plunge (#57) and sql-yodeler (#301) use for the same
// proof, so all three engines cite a product row alike. engine_version is
// spelled X.Y.Z, as theirs is.
//
// The row itself is kept under live/smoke/evidence/product-scenario/, one
// file per proof, copied from the product's committed row file (row_file at
// commit). This file holds the proofs to their shape, each kept row to its
// proof, and each cell to ruling E: a product row is an extra proof and
// never the only one in a cell. proven_cell_ci_test.go counts a proof
// toward its cell only when its row is kept and ran the release the claims
// are about, the newest in live/history, so a v0.28.0 row never proves a
// claim about v0.31.0.
//
// Proving it red: TestProductScenarioRulesCatch below, a fixture row per
// way a proof or a row can be wrong.

// productScenarioProof is one product_scenario proof with its cell.
type productScenarioProof struct {
	Claim    int
	Provider string
	Proof    statpages.ProductScenarioProof
}

func (g productScenarioProof) String() string {
	return fmt.Sprintf("claim %d (%s) product scenario %s %s", g.Claim, g.Provider, g.Proof.Repo, g.Proof.Scenario)
}

func productScenarioProofs(f smokeClaimsFile) []productScenarioProof {
	var out []productScenarioProof
	for _, c := range f.Claims {
		for _, p := range f.ProviderOrder {
			for _, pr := range c.Providers[p].Proofs {
				if pr.ProductScenario != nil {
					out = append(out, productScenarioProof{Claim: c.ID, Provider: p, Proof: *pr.ProductScenario})
				}
			}
		}
	}
	return out
}

// productScenarioCellProblems is ruling E over one cell: a cell that cites
// a product row also carries an engine proof (a scenario, steps of one, a
// gauntlet stage or a Go test).
func productScenarioCellProblems(cell smokeClaimProviderCell) []string {
	products, engine := 0, 0
	for _, pr := range cell.Proofs {
		if pr.ProductScenario != nil {
			products++
		} else {
			engine++
		}
	}
	if products > 0 && engine == 0 {
		return []string{"the cell's only proofs are product rows; a product row stands beside an engine proof and never replaces one (ruling E)"}
	}
	return nil
}

// productScenarioProofFieldProblems is the shape rule over one proof as
// claims.json carries it, with the scenario-only fields it must not carry.
func productScenarioProofFieldProblems(pr smokeProof) []string {
	out := statpages.ProductScenarioProofProblems(*pr.ProductScenario)
	if pr.Command != "" || pr.Minutes != 0 || pr.BreakMode != "" || pr.NeedsGo || pr.NeedsEmulator || pr.RealService || len(pr.Evidence) > 0 || pr.LastRun != nil {
		out = append(out, "carries a command, minutes, a break mode, a needs_ flag, evidence or a last_run; those describe an engine scenario, and the product's row carries its own run")
	}
	if strings.TrimSpace(pr.Note) == "" {
		out = append(out, "no note; say what the product scenario shows that the claim needs")
	}
	return out
}

// productScenarioKeys are the keys a product-scenario proof object may
// carry: the kind, the six fields and a note.
var productScenarioKeys = append([]string{"kind", "note"}, statpages.ProductScenarioFields...)

// decodeProductScenarioProof decodes one proof from claims.json's JSON and
// reports a product-scenario proof missing any of its six fields, which a
// plain Unmarshal would leave as zero values, or carrying a key that is
// none of them (a command, minutes, evidence: an engine scenario's).
func decodeProductScenarioProof(raw []byte) (smokeProof, []string) {
	var pr smokeProof
	if err := json.Unmarshal(raw, &pr); err != nil {
		return pr, []string{err.Error()}
	}
	if pr.ProductScenario == nil {
		return pr, nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return pr, []string{err.Error()}
	}
	var out []string
	for _, k := range statpages.ProductScenarioFields {
		if _, ok := keys[k]; !ok {
			out = append(out, fmt.Sprintf("a product-scenario proof has no %s", k))
		}
	}
	allowed := map[string]bool{}
	for _, k := range productScenarioKeys {
		allowed[k] = true
	}
	for k := range keys {
		if !allowed[k] {
			out = append(out, fmt.Sprintf("a product-scenario proof carries %q, which is none of its keys (%s)", k, strings.Join(productScenarioKeys, ", ")))
		}
	}
	sort.Strings(out)
	return pr, out
}

// TestProductScenarioProofsHaveTheirShape holds every product-scenario
// proof in claims.json to its six fields and every cell that cites one to
// ruling E. Since choudoufu#2301 claims 2, 5, 6, 7, 13, 28 and 45 cite
// terragucci's rows (smoke_claims_terragucci_test.go), and
// TestProductScenarioRulesCatch holds the rules against fixtures.
func TestProductScenarioProofsHaveTheirShape(t *testing.T) {
	raw, err := os.ReadFile(smokeClaimsPath)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Claims []struct {
			ID        int `json:"id"`
			Providers map[string]struct {
				Proofs []json.RawMessage `json:"proofs"`
			} `json:"providers"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range f.Claims {
		for p, cell := range c.Providers {
			for _, rawProof := range cell.Proofs {
				pr, problems := decodeProductScenarioProof(rawProof)
				if pr.ProductScenario == nil {
					continue
				}
				n++
				problems = append(problems, productScenarioProofFieldProblems(pr)...)
				for _, problem := range problems {
					t.Errorf("claim %d (%s) product scenario: %s", c.ID, p, problem)
				}
			}
		}
	}
	typed := readSmokeClaims(t)
	seen := map[string]productScenarioProof{}
	for _, g := range productScenarioProofs(typed) {
		rel := statpages.ProductScenarioEvidenceRel(g.Proof)
		if prev, dup := seen[rel]; dup {
			t.Errorf("%s is also %s; a product row proves one cell", g, prev)
		}
		seen[rel] = g
	}
	for _, c := range typed.Claims {
		for p, cell := range c.Providers {
			for _, problem := range productScenarioCellProblems(cell) {
				t.Errorf("claim %d (%s): %s", c.ID, p, problem)
			}
		}
	}
	t.Logf("%d product-scenario proofs in %s", n, smokeClaimsPath)
}

// TestProductScenarioRowsSayWhatTheirProofsSay: every file under
// live/smoke/evidence/product-scenario/ is the kept row of a proof
// claims.json lists, and says what that proof says. A row no proof cites is
// a run of nothing.
func TestProductScenarioRowsSayWhatTheirProofsSay(t *testing.T) {
	f := readSmokeClaims(t)
	byRel := map[string]productScenarioProof{}
	for _, g := range productScenarioProofs(f) {
		byRel[statpages.ProductScenarioEvidenceRel(g.Proof)] = g
	}
	dir := filepath.Join("..", filepath.FromSlash(statpages.ProductScenarioEvidenceDir))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		t.Logf("%s does not exist yet; every product-scenario proof is waiting", statpages.ProductScenarioEvidenceDir)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for _, e := range entries {
		rel := statpages.ProductScenarioEvidenceDir + "/" + e.Name()
		if !strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("%s is not a kept row", rel)
			continue
		}
		g, ok := byRel[rel]
		if !ok {
			t.Errorf("%s is the row of no product_scenario proof in %s", rel, smokeClaimsPath)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var rec statpages.ProductScenarioRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		kept++
		for _, p := range statpages.ProductScenarioRecordProblems(g.Proof, rec) {
			t.Errorf("%s (%s): %s", rel, g, p)
		}
	}
	t.Logf("%d of %d product-scenario proofs have a kept row", kept, len(byRel))
}

// productAt is the fixture proof the tests below and TestDatedRunRuleCatches
// share: terragucci's rows-overlap row, at the given choudoufu release.
func productAt(engineVersion string) statpages.ProductScenarioProof {
	return statpages.ProductScenarioProof{
		Repo: "INTENTIUS/terragucci", Scenario: "tg-estate-overlap", Claim: "choudoufu/rows-overlap",
		Commit: "8ab80a59c0ffee0000000000000000000000beef", Date: "2026-11-02", EngineVersion: engineVersion,
	}
}

// productRowFor is the kept row that says what proof says.
func productRowFor(p statpages.ProductScenarioProof) statpages.ProductScenarioRecord {
	return statpages.ProductScenarioRecord{
		Repo: p.Repo, RowFile: "docs-site/src/data/smoke.json", Scenario: p.Scenario, Claim: p.Claim,
		Product: strings.TrimPrefix(p.Repo, "INTENTIUS/"), Commit: p.Commit, Date: p.Date,
		EngineBinary: "choudoufu", EngineVersion: p.EngineVersion, FlociDigest: "sha256:63d8f1ed",
		Plain: "pass", Break: "caught",
	}
}

// TestProductScenarioRulesCatch proves the shape, row and version rules red
// against fixtures, so a green run over claims.json means something.
func TestProductScenarioRulesCatch(t *testing.T) {
	good := productAt("0.31.0")
	if got := statpages.ProductScenarioProofProblems(good); len(got) != 0 {
		t.Fatalf("a well-formed proof was reported: %v", got)
	}
	if got := statpages.ProductScenarioRecordProblems(good, productRowFor(good)); len(got) != 0 {
		t.Fatalf("a row that says what its proof says was reported: %v", got)
	}

	t.Run("schema rejects a proof missing a field", func(t *testing.T) {
		full := `{"kind":"product-scenario","repo":"INTENTIUS/terragucci","scenario":"tg-estate-overlap","claim":"choudoufu/rows-overlap","commit":"8ab80a59c0ffee0000000000000000000000beef","date":"2026-11-02","engine_version":"0.31.0","note":"n"}`
		pr, problems := decodeProductScenarioProof([]byte(full))
		if len(problems) != 0 {
			t.Fatalf("a proof with all six fields was reported: %v", problems)
		}
		if pr.ProductScenario == nil || pr.Scenario != "" || pr.ProductScenario.Scenario != "tg-estate-overlap" {
			t.Fatalf("decoded as %+v; want the product scenario id under ProductScenario and no choudoufu scenario path", pr)
		}
		for _, field := range statpages.ProductScenarioFields {
			var m map[string]string
			if err := json.Unmarshal([]byte(full), &m); err != nil {
				t.Fatal(err)
			}
			delete(m, field)
			obj, _ := json.Marshal(m)
			if _, problems := decodeProductScenarioProof(obj); len(problems) == 0 {
				t.Errorf("a proof with no %s was not reported", field)
			}
		}
		extra := strings.Replace(full, `"note":"n"`, `"note":"n","minutes":3`, 1)
		if _, problems := decodeProductScenarioProof([]byte(extra)); len(problems) == 0 {
			t.Error("a proof carrying minutes was not reported")
		}
		other := strings.Replace(full, `"kind":"product-scenario"`, `"kind":"go_test"`, 1)
		if _, problems := decodeProductScenarioProof([]byte(other)); len(problems) == 0 {
			t.Error("a proof of another kind was not reported")
		}
	})

	with := func(edit func(*statpages.ProductScenarioProof)) statpages.ProductScenarioProof {
		p := good
		edit(&p)
		return p
	}
	for _, tc := range []struct {
		name string
		p    statpages.ProductScenarioProof
	}{
		{"a repo outside INTENTIUS", with(func(p *statpages.ProductScenarioProof) { p.Repo = "someone/terragucci" })},
		{"choudoufu citing itself", with(func(p *statpages.ProductScenarioProof) { p.Repo = "INTENTIUS/choudoufu" })},
		{"an empty scenario", with(func(p *statpages.ProductScenarioProof) { p.Scenario = "" })},
		{"an empty claim", with(func(p *statpages.ProductScenarioProof) { p.Claim = "" })},
		{"a short sha", with(func(p *statpages.ProductScenarioProof) { p.Commit = "8ab80a59" })},
		{"a typed date", with(func(p *statpages.ProductScenarioProof) { p.Date = "2 Nov" })},
		{"a version spelled with a v", with(func(p *statpages.ProductScenarioProof) { p.EngineVersion = "v0.31.0" })},
	} {
		if got := statpages.ProductScenarioProofProblems(tc.p); len(got) == 0 {
			t.Errorf("proof shape, %s: the rule reported nothing", tc.name)
		}
	}

	t.Run("a proof carrying scenario fields or no note", func(t *testing.T) {
		p := good
		if got := productScenarioProofFieldProblems(smokeProof{ProductScenario: &p, Note: "n"}); len(got) != 0 {
			t.Fatalf("a clean proof was reported: %v", got)
		}
		if got := productScenarioProofFieldProblems(smokeProof{ProductScenario: &p}); len(got) == 0 {
			t.Error("a proof with no note was not reported")
		}
		if got := productScenarioProofFieldProblems(smokeProof{ProductScenario: &p, Note: "n", Minutes: 3}); len(got) == 0 {
			t.Error("a proof carrying minutes was not reported")
		}
	})

	row := func(edit func(*statpages.ProductScenarioRecord)) statpages.ProductScenarioRecord {
		r := productRowFor(good)
		edit(&r)
		return r
	}
	for _, tc := range []struct {
		name string
		r    statpages.ProductScenarioRecord
	}{
		{"a row at another engine version", row(func(r *statpages.ProductScenarioRecord) { r.EngineVersion = "0.28.0" })},
		{"a row of another scenario", row(func(r *statpages.ProductScenarioRecord) { r.Scenario = "tg-migrate" })},
		{"a row of another claim", row(func(r *statpages.ProductScenarioRecord) { r.Claim = "choudoufu/rows-wait" })},
		{"a row at another commit", row(func(r *statpages.ProductScenarioRecord) { r.Commit = strings.Repeat("a", 40) })},
		{"a row of another day", row(func(r *statpages.ProductScenarioRecord) { r.Date = "2026-11-03" })},
		{"a row from another repo", row(func(r *statpages.ProductScenarioRecord) { r.Repo = "INTENTIUS/plywood" })},
		{"a row that ran terraform", row(func(r *statpages.ProductScenarioRecord) { r.EngineBinary = "terraform" })},
		{"a failed plain run", row(func(r *statpages.ProductScenarioRecord) { r.Plain = "fail" })},
		{"a break run that was missed", row(func(r *statpages.ProductScenarioRecord) { r.Break = "missed" })},
		{"no floci digest", row(func(r *statpages.ProductScenarioRecord) { r.FlociDigest = "" })},
		{"a row file outside the repo", row(func(r *statpages.ProductScenarioRecord) { r.RowFile = "../smoke.json" })},
		{"a product that is not the repo", row(func(r *statpages.ProductScenarioRecord) { r.Product = "plywood" })},
	} {
		if got := statpages.ProductScenarioRecordProblems(good, tc.r); len(got) == 0 {
			t.Errorf("kept row, %s: the rule reported nothing", tc.name)
		}
	}

	t.Run("a row counts only at the claimed release", func(t *testing.T) {
		rec := productRowFor(good)
		if ok, why := statpages.ProductScenarioCounts(good, &rec, "0.31.0"); !ok {
			t.Errorf("a kept row at the claimed release did not count: %s", why)
		}
		old := productAt("0.28.0")
		oldRec := productRowFor(old)
		if ok, _ := statpages.ProductScenarioCounts(old, &oldRec, "0.31.0"); ok {
			t.Error("a v0.28.0 row counted toward a claim about v0.31.0")
		}
		if ok, _ := statpages.ProductScenarioCounts(good, nil, "0.31.0"); ok {
			t.Error("a proof with no kept row counted")
		}
	})

	t.Run("ruling E", func(t *testing.T) {
		p := good
		alone := smokeClaimProviderCell{Status: "proven", Proofs: []smokeProof{{ProductScenario: &p}}}
		if got := productScenarioCellProblems(alone); len(got) == 0 {
			t.Error("a cell whose only proof is a product row was not reported")
		}
		beside := smokeClaimProviderCell{Status: "proven", Proofs: []smokeProof{{Scenario: "live/smoke/scenarios/a.sh"}, {ProductScenario: &p}}}
		if got := productScenarioCellProblems(beside); len(got) != 0 {
			t.Errorf("a product row beside a scenario was reported: %v", got)
		}
	})
}

// TestProductScenarioRendersWithItsRow renders a fixture tree's proof line
// both ways: kept at the claimed release (a link to the row and its date),
// and at another release (named as not counting).
func TestProductScenarioRendersWithItsRow(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(statpages.HistoryDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v0.9.0", "v0.28.0", "v0.31.0"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(statpages.HistoryDir), v+".json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := statpages.ClaimedRelease(root)
	if err != nil || claimed != "0.31.0" {
		t.Fatalf("ClaimedRelease = %q, %v; want 0.31.0 (the newest by version, not by name, spelled without its v)", claimed, err)
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(statpages.ProductScenarioEvidenceDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := func(p statpages.ProductScenarioProof) {
		raw, err := json.Marshal(productRowFor(p))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(statpages.ProductScenarioEvidenceRel(p))), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	current := productAt("0.31.0")
	keep(current)
	line, err := statpages.ProductScenarioLine(root, current, claimed)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"terragucci scenario", "https://github.com/INTENTIUS/terragucci/blob/" + current.Commit + "/docs-site/src/data/smoke.json", "choudoufu/rows-overlap", "last proven 2026-11-02"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not carry %q", line, want)
		}
	}
	old := productAt("0.28.0")
	old.Scenario = "tg-estate-overlap-old"
	keep(old)
	line, err = statpages.ProductScenarioLine(root, old, claimed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "does not count") || strings.Contains(line, "last proven") {
		t.Errorf("a v0.28.0 row renders as %q; want it named as not counting", line)
	}
	waiting := productAt("0.31.0")
	waiting.Scenario = "never-kept"
	line, err = statpages.ProductScenarioLine(root, waiting, claimed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "waiting for its row") {
		t.Errorf("an unkept row renders as %q; want it waiting", line)
	}
	bad := productRowFor(current)
	bad.EngineVersion = "0.28.0"
	raw, _ := json.Marshal(bad)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(statpages.ProductScenarioEvidenceRel(current))), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := statpages.ProductScenarioLine(root, current, claimed); err == nil {
		t.Error("a kept row that misquotes its proof's version rendered without an error")
	}
}
