// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	version "github.com/hashicorp/go-version"
)

// ProductScenarioProof is a proof that cites one recorded row of a CI
// product's scenario run (GitHub issue #2299, ruling C on INTENTIUS/chant#4586).
// The product (terragucci, plywood, sql-yodeler) runs the scenario on the
// shared e2e harness against a released choudoufu binary, with a plain run
// that must pass and a BREAK=1 run that must be caught, and commits one row
// for it. The proof names that row: the product's repository, the scenario
// id, the product's claim id, the product commit the row ran at, the date of
// the run and the choudoufu version it ran.
//
// In claims.json the proof is one flat object, {"kind": "product-scenario",
// "repo", "scenario", "claim", "commit", "date", "engine_version", "note"},
// the same kind and keys plunge's and sql-yodeler's claims use
// (INTENTIUS/plunge#57, INTENTIUS/sql-yodeler#301). Its "scenario" is the
// product's scenario id, never a choudoufu scenario path, which is why the
// decoders read it through DecodeProductScenario rather than as a field of
// the scenario proof.
//
// A product row is an extra proof beside the engine's own (ruling E). It
// never replaces an engine scenario: the engine's BREAK arms rebuild the
// engine from source, which a product running a released binary cannot do.
type ProductScenarioProof struct {
	Repo          string `json:"repo"`
	Scenario      string `json:"scenario"`
	Claim         string `json:"claim"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
	EngineVersion string `json:"engine_version"`
}

// ProductScenarioKind is the proof's "kind" in claims.json.
const ProductScenarioKind = "product-scenario"

// ProductScenarioFields are the six fields a product-scenario proof must
// carry, in the order the three engines' claims list them.
var ProductScenarioFields = []string{"repo", "scenario", "claim", "commit", "date", "engine_version"}

// DecodeProductScenario reads one claims.json proof object. It returns nil
// for a proof with no kind (a scenario, steps of one, a stage or a Go
// test), the cited row for kind product-scenario, and an error for any
// other kind.
func DecodeProductScenario(raw []byte) (*ProductScenarioProof, error) {
	var k struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, err
	}
	switch k.Kind {
	case "":
		return nil, nil
	case ProductScenarioKind:
		var p ProductScenarioProof
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return &p, nil
	}
	return nil, fmt.Errorf("proof kind %q is not %q; the other proof kinds carry no kind field", k.Kind, ProductScenarioKind)
}

// ProductScenarioEvidenceDir holds the cited rows, one file per proof,
// each a copy of the row from the product's committed row file.
const ProductScenarioEvidenceDir = "live/smoke/evidence/product-scenario"

// HistoryDir is choudoufu's release ledger, one vX.Y.Z.json per release
// (`go run ./tools/gauntlet snapshot vX.Y.Z`).
const HistoryDir = "live/history"

// ProductScenarioRecord is one product row as choudoufu keeps it: the row's
// own fields (the row shape of INTENTIUS/chant#4587: product, product
// commit, engine binary and version, floci digest, date, plain and break
// verdicts), plus the scenario and claim it belongs to and RowFile, the
// path of the committed row file in the product repository the row was
// copied from.
type ProductScenarioRecord struct {
	Repo          string `json:"repo"`
	RowFile       string `json:"row_file"`
	Scenario      string `json:"scenario"`
	Claim         string `json:"claim"`
	Product       string `json:"product"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
	EngineBinary  string `json:"engine_binary"`
	EngineVersion string `json:"engine_version"`
	FlociDigest   string `json:"floci_digest"`
	Plain         string `json:"plain"`
	Break         string `json:"break"`
}

// RowURL is the row file at the commit the row ran, on GitHub.
func (r ProductScenarioRecord) RowURL() string {
	return fmt.Sprintf("https://github.com/%s/blob/%s/%s", r.Repo, r.Commit, r.RowFile)
}

var (
	fullSHA        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	isoDay         = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	releaseTag     = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	engineVersion  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	productRepo    = regexp.MustCompile(`^INTENTIUS/[a-z0-9][a-z0-9-]*$`)
	scenarioID     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	productClaimID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)*$`)
)

// ProductScenarioProofProblems is the shape rule for one proof: all six
// fields, each in its spelling. A full sha, a YYYY-MM-DD date and a release
// spelled X.Y.Z with no "v" (plunge's and sql-yodeler's spelling), so a row
// can be found again and its version compared.
func ProductScenarioProofProblems(p ProductScenarioProof) []string {
	var out []string
	if !productRepo.MatchString(p.Repo) {
		out = append(out, fmt.Sprintf("repo %q is not INTENTIUS/<product>", p.Repo))
	} else if p.Repo == "INTENTIUS/choudoufu" {
		out = append(out, "repo is choudoufu itself; a product-scenario proof cites a product's run, and choudoufu's own runs are scenarios")
	}
	if !scenarioID.MatchString(p.Scenario) {
		out = append(out, fmt.Sprintf("scenario %q is not a scenario id (lowercase, digits, dashes)", p.Scenario))
	}
	if !productClaimID.MatchString(p.Claim) {
		out = append(out, fmt.Sprintf("claim %q is not a product claim id (e.g. choudoufu/rows-overlap)", p.Claim))
	}
	if !fullSHA.MatchString(p.Commit) {
		out = append(out, fmt.Sprintf("commit %q is not a full 40-character sha", p.Commit))
	}
	if !isoDay.MatchString(p.Date) {
		out = append(out, fmt.Sprintf("date %q is not YYYY-MM-DD", p.Date))
	}
	if !engineVersion.MatchString(p.EngineVersion) {
		out = append(out, fmt.Sprintf("engine_version %q is not a choudoufu release spelled X.Y.Z", p.EngineVersion))
	}
	return out
}

// ProductScenarioRecordProblems checks a kept row against the proof that
// cites it: the row says what the proof says, ran choudoufu, and reads pass
// on the plain run and caught on the BREAK=1 run. A row at another engine
// version than the proof names is a misquote and is reported here; a proof
// whose version is not the claimed one is a separate matter
// (ProductScenarioCounts).
func ProductScenarioRecordProblems(p ProductScenarioProof, r ProductScenarioRecord) []string {
	var out []string
	same := func(field, proof, row string) {
		if proof != row {
			out = append(out, fmt.Sprintf("%s: the proof says %q, the row says %q", field, proof, row))
		}
	}
	same("repo", p.Repo, r.Repo)
	same("scenario", p.Scenario, r.Scenario)
	same("claim", p.Claim, r.Claim)
	same("commit", p.Commit, r.Commit)
	same("date", p.Date, r.Date)
	same("engine_version", p.EngineVersion, r.EngineVersion)
	if want := strings.TrimPrefix(r.Repo, "INTENTIUS/"); r.Product != want {
		out = append(out, fmt.Sprintf("product %q is not the repository's name %q", r.Product, want))
	}
	if r.EngineBinary != "choudoufu" {
		out = append(out, fmt.Sprintf("engine_binary %q; a choudoufu claim counts only a row that ran choudoufu", r.EngineBinary))
	}
	if r.Plain != "pass" {
		out = append(out, fmt.Sprintf("plain verdict %q, want pass", r.Plain))
	}
	if r.Break != "caught" {
		out = append(out, fmt.Sprintf("break verdict %q, want caught", r.Break))
	}
	if strings.TrimSpace(r.FlociDigest) == "" {
		out = append(out, "no floci_digest; a row says which emulator it ran on")
	}
	if r.RowFile == "" || strings.HasPrefix(r.RowFile, "/") || strings.Contains(r.RowFile, "..") {
		out = append(out, fmt.Sprintf("row_file %q is not a path inside the product repository", r.RowFile))
	}
	return out
}

// ProductScenarioCounts reports whether a proof counts toward its cell
// given the claimed release: only when its kept row is there and the
// engine version it ran is the claimed one. A row at v0.28.0 never proves a
// claim about v0.31.0. why says what is missing when it does not count.
func ProductScenarioCounts(p ProductScenarioProof, rec *ProductScenarioRecord, claimed string) (ok bool, why string) {
	switch {
	case rec == nil:
		return false, "waiting for its row to be recorded"
	case p.EngineVersion != claimed:
		return false, fmt.Sprintf("ran choudoufu %s and the claims are about %s, so it does not count", p.EngineVersion, claimed)
	}
	return true, ""
}

// ProductScenarioEvidenceRel is where a proof's row is kept: one file per
// (repo, scenario, claim), slashes turned into "--".
func ProductScenarioEvidenceRel(p ProductScenarioProof) string {
	name := strings.Join([]string{p.Repo, p.Scenario, p.Claim}, "--")
	return ProductScenarioEvidenceDir + "/" + strings.ReplaceAll(name, "/", "--") + ".json"
}

// ReadProductScenarioRecord reads a proof's kept row, or nil when none is
// kept yet. A kept row that does not say what its proof says is an error.
func ReadProductScenarioRecord(root string, p ProductScenarioProof) (*ProductScenarioRecord, error) {
	rel := ProductScenarioEvidenceRel(p)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var rec ProductScenarioRecord
	if err := readJSON(root, rel, &rec); err != nil {
		return nil, err
	}
	if problems := ProductScenarioRecordProblems(p, rec); len(problems) > 0 {
		return nil, fmt.Errorf("%s: %s", rel, strings.Join(problems, "; "))
	}
	return &rec, nil
}

// ClaimedRelease is the release the claims are about: the newest vX.Y.Z
// that live/history records, the in-tree ledger of choudoufu's releases,
// spelled X.Y.Z as engine_version is.
func ClaimedRelease(root string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(HistoryDir)))
	if err != nil {
		return "", err
	}
	var releases []*version.Version
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		if e.IsDir() || name == e.Name() || !releaseTag.MatchString(name) {
			continue
		}
		v, err := version.NewVersion(name)
		if err != nil {
			continue
		}
		releases = append(releases, v)
	}
	if len(releases) == 0 {
		return "", fmt.Errorf("%s records no vX.Y.Z release", HistoryDir)
	}
	sort.Sort(sort.Reverse(version.Collection(releases)))
	return releases[0].String(), nil
}

// ProductScenarioLine is one product-scenario proof as a CLAIMS.md line.
func ProductScenarioLine(root string, p ProductScenarioProof, claimed string) (string, error) {
	product := strings.TrimPrefix(p.Repo, "INTENTIUS/")
	line := fmt.Sprintf("%s scenario `%s`, claim `%s`, choudoufu %s", product, p.Scenario, p.Claim, p.EngineVersion)
	rec, err := ReadProductScenarioRecord(root, p)
	if err != nil {
		return "", err
	}
	if rec != nil {
		line = fmt.Sprintf("%s scenario [`%s`](%s), claim `%s`, choudoufu %s", product, p.Scenario, rec.RowURL(), p.Claim, p.EngineVersion)
	}
	if ok, why := ProductScenarioCounts(p, rec, claimed); !ok {
		return line + ", " + why, nil
	}
	return line + ", " + measured{Date: rec.Date, Commit: rec.Commit}.String(), nil
}
