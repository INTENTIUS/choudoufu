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

// live/smoke/claims.json is the source of truth for the claims the smoke
// scenarios prove (#1055). Before it existed the twenty claims lived only as
// prose in one 12,700-word site page, and nothing tied a claim to the
// scenario that runs it. This test ties the three together: the JSON, the
// scenario directory, and the per-claim pages plus the site's data copy.
//
// Since #1112 a claim is a generic promise and proof is per provider: a row
// is the promise (id, slug, title, theme, the substrates it applies to), and
// each provider cell carries its own proofs, because the mechanism differs
// from one substrate to the next. Since #1817 a cell carries a list of them:
// 43 claims were folded into 16 short promises, and the scenarios of the 27
// folded ones stayed as proofs of their survivors. A scenario still proves
// one cell, and its header says which: "# CLAIM 7 (kubernetes) - <title>.
// ~2 min." A folded number is retired, never reused, and the top-level
// "retired" list is what keeps it resolving.
//
// Proving it red: add a scenario file with a "CLAIM 47 (aws)" header and no
// proof, point two proofs at one scenario, or drop a claim's entry from
// site/src/data/redirects.json; each fails a different check below.

const (
	smokeClaimsPath   = "smoke/claims.json"
	smokeScenariosDir = "smoke/scenarios"
	siteClaimsPages   = "smoke/claims"
	// claimsGitHubBase is where a claim's old site URL redirects: its page
	// in the repository, under siteClaimsPages.
	claimsGitHubBase = "https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/claims/"
)

// smokeProviderNames is how a page heads a provider's section: "## On AWS".
var smokeProviderNames = map[string]string{"aws": "AWS", "kubernetes": "Kubernetes", "azure": "Azure"}

type smokeClaimsFile struct {
	Themes        map[string]string `json:"themes"`
	ProviderOrder []string          `json:"provider_order"`
	Retired       []smokeRetired    `json:"retired"`
	Demos         []smokeProof      `json:"demos"`
	Claims        []smokeClaim      `json:"claims"`
}

// smokeRetired is a claim number that is no longer a claim of its own.
// Most were a second proof of an existing promise and now live in that
// promise's cell for one provider; their slug is still the scenario's name
// and still an old URL. One (#1817's 37) became a demo, and says so.
//
// CoveredBy is for a retired number whose own scenario was deleted because
// another proof shows the same thing: it names that proof, either a
// scenario registered in some cell ("a-killed-apply-hides-nothing") or a
// gauntlet stage ("<estate>:<stage id>"). Claim and Provider still say
// where the number's URL lands.
//
// MergedInto is for a whole claim folded into another live claim: its
// statement nested inside the survivor's, its proofs moved into the
// survivor's cells, and its page became a stub pointing at the survivor's
// page. It names no provider, because every cell moved.
//
// Reserved is a number held back with no claim behind it: a promise with
// no proof left the list, and its number is never reused. It names nothing
// else, and its page is a stub.
type smokeRetired struct {
	ID         int    `json:"id"`
	Slug       string `json:"slug"`
	Claim      int    `json:"claim"`
	Provider   string `json:"provider"`
	Demo       bool   `json:"demo"`
	CoveredBy  string `json:"covered_by"`
	MergedInto int    `json:"merged_into"`
	Reserved   bool   `json:"reserved"`
}

// target is the live claim a retired number's URL lands on.
func (r smokeRetired) target() int {
	if r.MergedInto != 0 {
		return r.MergedInto
	}
	return r.Claim
}

type smokeClaim struct {
	ID        int                               `json:"id"`
	Slug      string                            `json:"slug"`
	Title     string                            `json:"title"`
	Theme     string                            `json:"theme"`
	Substrate []string                          `json:"substrate"`
	Providers map[string]smokeClaimProviderCell `json:"providers"`
}

// smokeClaimProviderCell is one (claim, provider) cell: what is true of
// the promise on that provider, and the proofs that show it. #1817 made
// proofs a list, because folding 43 claims into 16 left most cells with
// more than one scenario.
type smokeClaimProviderCell struct {
	Status string       `json:"status"`
	Note   string       `json:"note"`
	Proofs []smokeProof `json:"proofs"`
}

// smokeProof is one proof in a cell: a scenario of its own, or (ProvenBy)
// some steps of another claim's scenario on the same provider.
type smokeProof struct {
	Scenario      string   `json:"scenario"`
	Command       string   `json:"command"`
	Minutes       int      `json:"minutes"`
	NeedsGo       bool     `json:"needs_go"`
	NeedsEmulator bool     `json:"needs_emulator"`
	RealService   bool     `json:"real_service"`
	BreakMode     string   `json:"break_mode"`
	Evidence      []string `json:"evidence"`
	Note          string   `json:"note"`
	// ProvenBy names the claim whose cell for the same provider carries the
	// scenario that proves this, for a proof that is steps of another
	// claim's scenario rather than a scenario of its own. Command is then
	// that scenario's command, and Note names the steps.
	ProvenBy int `json:"proven_by"`
	// Stage cites a gauntlet stage as the proof: "<estate>:<stage id>",
	// resolved against that estate's row in live/gauntlet.json. It counts
	// only while that row's stage reads pass (proven_cell_ci_test.go). Note
	// says what the stage asserts that the claim needs.
	Stage string `json:"stage"`
	// LastRun is the last result measured for a scenario proof, carried
	// until a newer run replaces it, the way live/gauntlet.json carries a
	// row's last_run. A release never resets it; a page that renders it
	// says when it is older than the newest release.
	LastRun *smokeLastRun `json:"last_run,omitempty"`
}

// smokeLastRun is a proof's carried result. Claim is the claim whose cell
// listed the scenario at Commit, which a later merge of claims may have
// changed; Version is the release tag Commit follows (git describe).
type smokeLastRun struct {
	Status  string `json:"status"`
	Claim   int    `json:"claim"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Version string `json:"version"`
}

// scenarios returns the proofs in the cell that are scenarios of its own.
func (c smokeClaimProviderCell) scenarios() []smokeProof {
	var out []smokeProof
	for _, p := range c.Proofs {
		if p.Scenario != "" {
			out = append(out, p)
		}
	}
	return out
}

// smokeScenarioCell is one proof that is a scenario, with the (claim,
// provider) cell it proves.
type smokeScenarioCell struct {
	Claim    smokeClaim
	Provider string
	// Status is the cell's status.
	Status string
	Cell   smokeProof
	// Name is the scenario's file name without .sh, which is also what
	// `just smoke` takes.
	Name string
}

func (s smokeScenarioCell) String() string {
	if s.Claim.ID == 0 {
		return fmt.Sprintf("demo %s.sh", s.Name)
	}
	return fmt.Sprintf("claim %d (%s) %s.sh", s.Claim.ID, s.Provider, s.Name)
}

// smokeScenarioCells returns every proof that is a scenario, in claim
// order, then provider_order, then the cell's own order.
func smokeScenarioCells(f smokeClaimsFile) []smokeScenarioCell {
	var out []smokeScenarioCell
	for _, c := range f.Claims {
		for _, p := range f.ProviderOrder {
			cell, ok := c.Providers[p]
			if !ok {
				continue
			}
			for _, pr := range cell.scenarios() {
				out = append(out, smokeScenarioCell{
					Claim: c, Provider: p, Status: cell.Status, Cell: pr,
					Name: strings.TrimSuffix(filepath.Base(pr.Scenario), ".sh"),
				})
			}
		}
	}
	return out
}

// smokeDemoNames is the set of demo scenario names claims.json lists.
func smokeDemoNames(f smokeClaimsFile) map[string]bool {
	out := map[string]bool{}
	for _, d := range f.Demos {
		out[strings.TrimSuffix(filepath.Base(d.Scenario), ".sh")] = true
	}
	return out
}

// A cell is proven, every proof in it on a dated run
// (proven_cell_ci_test.go), or n/a where the substrate has no such concept.
// A promise with no proof is a GitHub issue, never a cell.
var smokeClaimStatuses = map[string]bool{"proven": true, "n/a": true}

// scenarioHeader is the second line of every claim scenario:
// "# CLAIM 13 (aws) - The tag is the boundary: ... ~4 min." The number, the
// provider and the minutes must agree with the cell that names the scenario.
var scenarioHeader = regexp.MustCompile(`^# CLAIM (\d+) \(([a-z0-9]+)\) - .*~(\d+) min\.?\s*$`)

// scenarioHeaderTitle splits the same line into its number, its provider,
// the sentence it states as the claim, and its minutes.
var scenarioHeaderTitle = regexp.MustCompile(`^# CLAIM (\d+) \(([a-z0-9]+)\) - (.*?)\.?\s*~(\d+) min\.?\s*$`)

// realServiceHeaderSuffix is what a real-service scenario's header adds to
// the claims.json title, so `just smoke` lists it as one only the
// maintainer starts. TestSmokeClaimsRealServiceSaysSo requires the header
// to say REAL AWS; this is the one spelling of that which also leaves the
// two titles comparable.
const realServiceHeaderSuffix = " (REAL AWS, maintainer-run)"

// goToolchainCall matches a scenario line that runs the Go toolchain. It is
// what needs_go means: whether a reader without Go can run the scenario, its
// BREAK arm included.
var goToolchainCall = regexp.MustCompile(`\bgo (build|run|test)\b`)

func readSmokeClaims(t *testing.T) smokeClaimsFile {
	t.Helper()
	raw, err := os.ReadFile(smokeClaimsPath)
	if err != nil {
		t.Fatalf("read %s: %v", smokeClaimsPath, err)
	}
	var f smokeClaimsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode %s: %v", smokeClaimsPath, err)
	}
	if len(f.Claims) == 0 {
		t.Fatalf("%s lists no claims; this test is checking nothing", smokeClaimsPath)
	}
	if len(smokeScenarioCells(f)) == 0 {
		t.Fatalf("%s: no provider cell carries a scenario; this test is checking nothing", smokeClaimsPath)
	}
	return f
}

// TestSmokeClaimsMatchScenarios: every claim scenario is the scenario of
// exactly one proof, every proof that names a scenario names one that
// exists, and the script's own header states that proof's claim, provider
// and minutes. Claim numbers are stable forever: the live ones and the
// retired ones together are 1..N with no gap and no number used twice.
func TestSmokeClaimsMatchScenarios(t *testing.T) {
	f := readSmokeClaims(t)
	slugs := map[string]bool{}
	ids := map[int]string{}
	byID := map[int]smokeClaim{}
	for _, c := range f.Claims {
		if slugs[c.Slug] {
			t.Errorf("slug %q appears twice", c.Slug)
		}
		slugs[c.Slug] = true
		if prev, dup := ids[c.ID]; dup {
			t.Errorf("claim id %d appears twice (%s and %s)", c.ID, prev, c.Slug)
		}
		ids[c.ID] = c.Slug
		byID[c.ID] = c
		if _, ok := f.Themes[c.Theme]; !ok {
			t.Errorf("claim %d: theme %q is not in the file's themes map", c.ID, c.Theme)
		}
	}
	demos := smokeDemoNames(f)
	if len(demos) == 0 {
		t.Errorf("%s lists no demos; import, greenfield and full are demos and belong there", smokeClaimsPath)
	}
	for _, d := range f.Demos {
		name := strings.TrimSuffix(filepath.Base(d.Scenario), ".sh")
		if want := filepath.ToSlash(filepath.Join("live", smokeScenariosDir, name+".sh")); d.Scenario != want {
			t.Errorf("demo %s: scenario is %q, want %q", name, d.Scenario, want)
		}
		if _, err := os.Stat(filepath.Join(smokeScenariosDir, name+".sh")); err != nil {
			t.Errorf("demo %s: %v", name, err)
		}
		if want := "just smoke " + name; d.Command != want {
			t.Errorf("demo %s: command is %q, want %q", name, d.Command, want)
		}
		if d.Minutes <= 0 {
			t.Errorf("demo %s: minutes is %d; a demo states how long it runs", name, d.Minutes)
		}
	}
	retiredBySlug := map[string]smokeRetired{}
	var coveredBy []smokeRetired
	for _, r := range f.Retired {
		if prev, dup := ids[r.ID]; dup {
			t.Errorf("retired claim %d (%s) reuses a number that is also %s; a claim's number is stable forever", r.ID, r.Slug, prev)
		}
		ids[r.ID] = r.Slug
		if slugs[r.Slug] {
			t.Errorf("retired claim %d's slug %q is also a live claim's slug", r.ID, r.Slug)
		}
		if r.Demo {
			if r.Claim != 0 || r.Provider != "" {
				t.Errorf("retired claim %d is a demo and still names claim %d (%s); a demo belongs to no cell", r.ID, r.Claim, r.Provider)
			}
			if !demos[r.Slug] {
				t.Errorf("retired claim %d says it became a demo, and %s lists no demo %s.sh", r.ID, smokeClaimsPath, r.Slug)
			}
			continue
		}
		if r.Reserved {
			if r.Claim != 0 || r.Provider != "" || r.CoveredBy != "" || r.MergedInto != 0 {
				t.Errorf("retired claim %d is reserved and also names a claim, provider, covered_by or merged_into; a reserved number names nothing", r.ID)
			}
			if _, err := os.Stat(filepath.Join(smokeScenariosDir, r.Slug+".sh")); err == nil {
				t.Errorf("retired claim %d is reserved and %s/%s.sh is still a scenario; a script with no claim lives under live/smoke/drafts/", r.ID, smokeScenariosDir, r.Slug)
			}
			continue
		}
		if r.MergedInto != 0 {
			if r.Claim != 0 || r.Provider != "" || r.CoveredBy != "" {
				t.Errorf("retired claim %d is merged_into %d and also names claim %d, provider %q or covered_by %q; a merged claim moved whole, so it names only the claim it moved into", r.ID, r.MergedInto, r.Claim, r.Provider, r.CoveredBy)
			}
			if _, ok := byID[r.MergedInto]; !ok {
				t.Errorf("retired claim %d says it was merged into claim %d, which is not a claim", r.ID, r.MergedInto)
			}
			continue
		}
		retiredBySlug[r.Slug] = r
		into, ok := byID[r.Claim]
		if !ok {
			t.Errorf("retired claim %d says it moved into claim %d, which is not a claim", r.ID, r.Claim)
			continue
		}
		if r.CoveredBy != "" {
			coveredBy = append(coveredBy, r)
			continue
		}
		want := filepath.ToSlash(filepath.Join("live", smokeScenariosDir, r.Slug+".sh"))
		carried := false
		for _, p := range into.Providers[r.Provider].scenarios() {
			carried = carried || p.Scenario == want
		}
		if !carried {
			t.Errorf("retired claim %d (%s) says it is claim %d on %s, and that cell carries no proof %s.sh", r.ID, r.Slug, r.Claim, r.Provider, r.Slug)
		}
	}
	for i := 1; i <= len(ids); i++ {
		if _, ok := ids[i]; !ok {
			t.Errorf("claim ids are not contiguous: %d is neither a claim nor retired", i)
		}
	}

	cellByName := map[string]smokeScenarioCell{}
	for _, s := range smokeScenarioCells(f) {
		cell := s.Cell
		if prev, dup := cellByName[s.Name]; dup {
			t.Errorf("%s.sh is the scenario of both %s and %s; a scenario proves one promise on one provider", s.Name, prev, s)
		}
		if demos[s.Name] {
			t.Errorf("%s.sh is a demo in %s and also %s", s.Name, smokeClaimsPath, s)
		}
		cellByName[s.Name] = s
		if want := filepath.ToSlash(filepath.Join("live", smokeScenariosDir, s.Name+".sh")); cell.Scenario != want {
			t.Errorf("%s: scenario is %q, want %q", s, cell.Scenario, want)
		}
		// Which claim a scenario proves is its registration: the cell in
		// claims.json whose proofs list it. Its file name need not be the
		// claim's slug. Until #1504 it had to be the slug, a slug with or
		// without its readability prefix, or a retired claim's slug, and a
		// second proof of one promise on one provider could only be added by
		// minting a retired number for something that was never a claim.
		//
		// What the naming rule used to catch is still caught, by the checks
		// that read the registration itself: a scenario on disk that no cell
		// lists fails below ("the proof of no (claim, provider) cell"); a
		// cell listing a scenario that is not on disk fails the count at the
		// end; a scenario listed by two cells fails above; and the header's
		// CLAIM N (provider) must name the cell that lists it.
		if want := "just smoke " + s.Name; cell.Command != want {
			t.Errorf("%s: command is %q, want %q", s, cell.Command, want)
		}
		if cell.Minutes <= 0 {
			t.Errorf("%s: minutes is %d; a scenario states how long it runs", s, cell.Minutes)
		}
		if cell.BreakMode == "" {
			t.Errorf("%s: break_mode is empty; every proof ships with its failure demonstrated", s)
		}
		if cell.ProvenBy != 0 {
			t.Errorf("%s: the proof is a scenario of its own and also proven_by %d; say one", s, cell.ProvenBy)
		}
		for _, ev := range cell.Evidence {
			if _, err := os.Stat(filepath.Join("..", filepath.FromSlash(ev))); err != nil {
				t.Errorf("%s: evidence %q does not exist", s, ev)
			}
		}
	}
	for _, c := range f.Claims {
		for p, cell := range c.Providers {
			for _, pr := range cell.Proofs {
				if pr.Scenario != "" {
					if pr.Stage != "" {
						t.Errorf("claim %d (%s): %s is a scenario and also cites stage %s; say one", c.ID, p, pr.Scenario, pr.Stage)
					}
					continue
				}
				switch {
				case pr.ProvenBy == 0 && pr.Stage == "":
					t.Errorf("claim %d (%s): a proof with no scenario, no proven_by and no stage proves nothing", c.ID, p)
				case pr.ProvenBy != 0 && pr.Stage != "":
					t.Errorf("claim %d (%s): a proof is proven_by %d and also cites stage %s; say one", c.ID, p, pr.ProvenBy, pr.Stage)
				}
				if pr.Stage != "" && pr.Command != "" {
					t.Errorf("claim %d (%s): stage proof %s carries command %q; a stage runs only inside its estate's gauntlet run, which is the maintainer's", c.ID, p, pr.Stage, pr.Command)
				}
				if pr.Stage != "" && strings.TrimSpace(pr.Note) == "" {
					t.Errorf("claim %d (%s): stage proof %s with no note; say what the stage asserts that the claim needs", c.ID, p, pr.Stage)
				}
				if pr.Minutes != 0 || pr.BreakMode != "" || pr.NeedsGo || pr.NeedsEmulator || pr.RealService || len(pr.Evidence) > 0 {
					t.Errorf("claim %d (%s): a proven_by or stage proof carries minutes, a break mode, a flag or evidence; those describe a scenario, and the scenario's own proof carries them", c.ID, p)
				}
			}
		}
	}
	// A retired number covered by another proof: its own scenario is gone,
	// and what it showed is shown by a registered scenario or a gauntlet
	// stage. A stage citation must name a row and stage that exist; whether
	// it passes is the cell's business, not the pointer's.
	if len(coveredBy) > 0 {
		rows := readGauntletStageRows(t)
		for _, r := range coveredBy {
			if _, err := os.Stat(filepath.Join(smokeScenariosDir, r.Slug+".sh")); err == nil {
				t.Errorf("retired claim %d (%s) is covered_by %s, and its own scenario is still on disk; delete it or drop covered_by", r.ID, r.Slug, r.CoveredBy)
			}
			if strings.Contains(r.CoveredBy, ":") {
				if problem, exists := stageCitationProblem(rows, r.CoveredBy); !exists {
					t.Errorf("retired claim %d (%s): covered_by %s", r.ID, r.Slug, problem)
				}
				continue
			}
			if _, ok := cellByName[r.CoveredBy]; !ok {
				t.Errorf("retired claim %d (%s) is covered_by %q, which is the scenario of no cell in %s", r.ID, r.Slug, r.CoveredBy, smokeClaimsPath)
			}
		}
	}

	entries, err := os.ReadDir(smokeScenariosDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".sh")
		if !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(smokeScenariosDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(string(raw), "\n", 3)
		if demos[name] {
			if len(lines) > 1 && strings.HasPrefix(lines[1], "# CLAIM") {
				t.Errorf("%s is a demo in %s and its header claims a number: %q", e.Name(), smokeClaimsPath, lines[1])
			}
			continue
		}
		seen++
		s, ok := cellByName[name]
		if !ok {
			t.Errorf("scenario %s is the proof of no (claim, provider) cell and no demo in %s", e.Name(), smokeClaimsPath)
			continue
		}
		if len(lines) < 2 {
			t.Errorf("%s has no header line", e.Name())
			continue
		}
		m := scenarioHeader.FindStringSubmatch(lines[1])
		if m == nil {
			t.Errorf("%s line 2 is not a \"# CLAIM N (provider) - ... ~M min.\" header: %q", e.Name(), lines[1])
			continue
		}
		if fmt.Sprint(s.Claim.ID) != m[1] || s.Provider != m[2] {
			t.Errorf("%s header says CLAIM %s (%s), %s says it is a proof of claim %d (%s)", e.Name(), m[1], m[2], smokeClaimsPath, s.Claim.ID, s.Provider)
		}
		if fmt.Sprint(s.Cell.Minutes) != m[3] {
			t.Errorf("%s header says ~%s min, %s says %d", e.Name(), m[3], smokeClaimsPath, s.Cell.Minutes)
		}
	}
	if seen != len(cellByName) {
		t.Errorf("%d claim scenarios on disk, %d proofs carrying one in %s", seen, len(cellByName), smokeClaimsPath)
	}
}

// TestSmokeClaimScenarioHeadersStateTheClaim: the sentence a scenario prints
// about itself and the sentence the index prints about it are the same
// sentence. #1379's audit found claims 29, 34 and 35 stating one claim in the
// script and another in claims.json, with nothing checking: a reader who runs
// `just smoke <slug>` and a reader who reads the claims table were told
// different things about what was proven. Until #1817 twenty-five headers
// were on a ratchet that let them restate the claim at length; since the
// titles became short promises every header states its claim's title
// exactly, and what the scenario shows is on the claim's page.
//
// Proving it red: put claim 29's old header back
// ("A record store bucket without versioning, a lifecycle that expires
// noncurrent versions, or public-access block is refused ...").
func TestSmokeClaimScenarioHeadersStateTheClaim(t *testing.T) {
	f := readSmokeClaims(t)
	for _, s := range smokeScenarioCells(f) {
		c, name := s.Claim, s.Name+".sh"
		raw, err := os.ReadFile(filepath.Join(smokeScenariosDir, name))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(string(raw), "\n", 3)
		if len(lines) < 2 {
			t.Errorf("%s has no header line", name)
			continue
		}
		m := scenarioHeaderTitle.FindStringSubmatch(lines[1])
		if m == nil {
			t.Errorf("%s line 2 is not a \"# CLAIM N (provider) - <title>. ~M min.\" header: %q", name, lines[1])
			continue
		}
		want := c.Title
		if s.Cell.RealService {
			want += realServiceHeaderSuffix
		}
		if got := m[3]; got != want {
			t.Errorf("%s: the scenario's header states\n  %q\nand %s states\n  %q\nThe two are what a reader running the scenario and a reader reading the index are each told this claim is.", s, got, smokeClaimsPath, want)
		}
	}
}

// TestSmokeClaimsNeedGoExactlyWhenTheyRunIt: needs_go is what the site's
// claims table prints as "needs Go", and it is what tells a reader whose
// machine has no Go toolchain which claims they can run. #1379 found it false
// for all six claims whose BREAK arm builds a patched binary with
// `go build -overlay`, one of which (claim 36) the README already described
// as needing Go.
//
// Proving it red: set any of those cells back to false, or delete the
// `go build -overlay` line from a scenario whose cell says true.
func TestSmokeClaimsNeedGoExactlyWhenTheyRunIt(t *testing.T) {
	f := readSmokeClaims(t)
	runsGo := 0
	for _, s := range smokeScenarioCells(f) {
		name := s.Name + ".sh"
		raw, err := os.ReadFile(filepath.Join(smokeScenariosDir, name))
		if err != nil {
			t.Fatal(err)
		}
		// Executable lines only: a comment explaining that some other
		// scenario builds a binary is not this scenario needing Go.
		var calls []string
		for i, line := range smokeExecutableLines(string(raw)) {
			if line != "" && goToolchainCall.MatchString(line) {
				calls = append(calls, fmt.Sprintf("line %d: %s", i+1, line))
			}
		}
		if len(calls) > 0 {
			runsGo++
		}
		if len(calls) > 0 && !s.Cell.NeedsGo {
			t.Errorf("%s runs the Go toolchain and %s says needs_go is false, so the claims table tells a reader with no Go that they can run it:\n  %s", s, smokeClaimsPath, strings.Join(calls, "\n  "))
		}
		if len(calls) == 0 && s.Cell.NeedsGo {
			t.Errorf("%s: %s says needs_go is true and no executable line in the scenario runs go build, go run or go test", s, smokeClaimsPath)
		}
	}
	if runsGo == 0 {
		t.Errorf("no claim scenario runs the Go toolchain at all; every BREAK arm that builds a patched binary has gone, or this guard is looking in the wrong place")
	}
}

// smokeStackUp is the call that starts the pinned floci emulator.
var smokeStackUp = regexp.MustCompile(`\bstack_up\b`)

// TestSmokeClaimsNeedEmulatorExactlyWhenTheySaySo: needs_emulator is what
// the claims table prints as "needs the emulator", and it is for the proof
// whose scenario runs on one substrate and keeps its records on another. A
// Kubernetes proof that brings up floci needs Docker and the AWS CLI on top
// of kind and kubectl, and a reader who has only the second pair finds that
// out from a failure halfway through a ten-minute run otherwise (#1394).
//
// An aws cell is not asked to carry the flag: there the emulator IS the
// substrate, which the column already says.
//
// Proving it red: set claim 27's needs_emulator to false, or delete the
// stack_up call from its scenario. Both were run on 2026-09-19.
func TestSmokeClaimsNeedEmulatorExactlyWhenTheySaySo(t *testing.T) {
	f := readSmokeClaims(t)
	withStackUp := 0
	for _, s := range smokeScenarioCells(f) {
		name := s.Name + ".sh"
		raw, err := os.ReadFile(filepath.Join(smokeScenariosDir, name))
		if err != nil {
			t.Fatal(err)
		}
		var calls []string
		for i, line := range smokeExecutableLines(string(raw)) {
			if line != "" && smokeStackUp.MatchString(line) {
				calls = append(calls, fmt.Sprintf("line %d: %s", i+1, line))
			}
		}
		if len(calls) > 0 {
			withStackUp++
		}
		if s.Provider == "aws" {
			if s.Cell.NeedsEmulator {
				t.Errorf("%s: needs_emulator is true on an aws cell, where the emulator is what the proof runs on; the flag is for a proof that runs on one substrate and keeps its records on another", s)
			}
			continue
		}
		if len(calls) > 0 && !s.Cell.NeedsEmulator {
			t.Errorf("%s starts the emulator, and %s says needs_emulator is false, so the claims table tells a reader with kind and no Docker that they can run it:\n  %s", s, smokeClaimsPath, strings.Join(calls, "\n  "))
		}
		if len(calls) == 0 && s.Cell.NeedsEmulator {
			t.Errorf("%s: %s says needs_emulator is true and no executable line in the scenario calls stack_up", s, smokeClaimsPath)
		}
	}
	if withStackUp == 0 {
		t.Errorf("no claim scenario starts the emulator at all; this guard is looking in the wrong place")
	}
}

const smokeRefusal = `[ "${SMOKE_REAL_AWS:-0}" = "1" ]`

// smokeHeredocStart matches the opening of a heredoc, so its body (a
// terraform file, a Python patch) is not read as shell.
var smokeHeredocStart = regexp.MustCompile(`<<-?\s*(?:'([A-Za-z_]\w*)'|"([A-Za-z_]\w*)"|([A-Za-z_]\w*))`)

// smokeTouchesAWS: the ways a scenario reaches an account. `real_aws_begin`
// clears the emulator endpoint and reads the caller identity; `bucket_up`
// and `role_with_policy` create resources; `aws`/`awsl` in command position
// is any API call at all; `just up`/`just verify` run the shipped project
// against the account. Sourcing bucket-iam.sh defines functions and calls
// none of them, so it is allowed before the refusal.
var smokeTouchesAWS = []*regexp.Regexp{
	regexp.MustCompile(`\breal_aws_begin\b`),
	regexp.MustCompile(`\bbucket_up\b`),
	regexp.MustCompile(`\brole_with_policy\b`),
	regexp.MustCompile("(^|[;&|(`]|\\$\\()\\s*(aws|awsl)\\s"),
	regexp.MustCompile(`\bjust\s+(up|verify)\b`),
}

// smokeExecutableLines returns one entry per line of the script, holding the
// trimmed line where bash would execute it and "" where it would not: blank
// lines, whole-line comments, and heredoc bodies. A guard that greps the raw
// file cannot tell a refusal from a refusal someone commented out, which is
// the first of the three mutations #1379 recorded against this test.
func smokeExecutableLines(script string) []string {
	lines := strings.Split(script, "\n")
	out := make([]string, len(lines))
	term := ""
	for i, line := range lines {
		if term != "" {
			if strings.TrimSpace(line) == term {
				term = ""
			}
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out[i] = t
		if m := smokeHeredocStart.FindStringSubmatch(line); m != nil {
			term = m[1] + m[2] + m[3]
		}
	}
	return out
}

// smokeFirstAWSLine returns the index of the first executable line that can
// reach an account, or -1.
func smokeFirstAWSLine(exec []string) (int, string) {
	for i, line := range exec {
		if line == "" {
			continue
		}
		for _, re := range smokeTouchesAWS {
			if re.MatchString(line) {
				return i, line
			}
		}
	}
	return -1, ""
}

// smokeRefusalLine returns the index of the executable line that refuses to
// start without SMOKE_REAL_AWS=1 and whether that line is wired to `fail`,
// either on itself or on the line its backslash continues onto. A refusal
// whose `|| fail` went missing is a line that computes a boolean and throws
// it away.
func smokeRefusalLine(exec []string) (idx int, wired bool) {
	for i, line := range exec {
		if !strings.HasPrefix(line, smokeRefusal) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, smokeRefusal))
		if strings.HasPrefix(rest, "|| fail") {
			return i, true
		}
		if rest != `\` {
			return i, false
		}
		for j := i + 1; j < len(exec); j++ {
			if exec[j] == "" {
				continue
			}
			return i, strings.HasPrefix(exec[j], "|| fail")
		}
		return i, false
	}
	return -1, false
}

// TestSmokeClaimsRealServiceSaysSo: a proof that needs a real AWS account
// says so in the index, and its scenario refuses to start without
// SMOKE_REAL_AWS=1. The bucket backend epic (#1332) has five such claims,
// and its rule is that the index states it rather than leaving a cell
// nobody can explain. The other direction matters as much: a scenario that
// reaches for real AWS without the refusal would spend a maintainer's money
// from a paste-and-go prompt, and CLAUDE.md's rule is that such a run is
// never started by anything but the maintainer.
//
// The audit in #1379 showed the substring form of this test green against a
// commented-out refusal, a refusal moved below every resource the scenario
// creates, and an emulator scenario that sourced bucket-iam.sh and called
// real_aws_begin with no refusal at all. So what is checked is where the
// refusal sits in the file bash would run: an executable line, wired to
// `fail`, above the first line that can reach an account.
//
// A demo is held to the same rule (#1817): the recommended secure
// configuration stopped being a claim and is still a real-AWS run, so it
// still refuses to start without the variable and smoke.sh still reads its
// real_service off claims.json to leave its teardown unbounded.
func TestSmokeClaimsRealServiceSaysSo(t *testing.T) {
	f := readSmokeClaims(t)
	all := smokeScenarioCells(f)
	realDemos := 0
	for _, d := range f.Demos {
		if d.RealService {
			realDemos++
		}
		all = append(all, smokeScenarioCell{Provider: "aws", Cell: d, Name: strings.TrimSuffix(filepath.Base(d.Scenario), ".sh")})
	}
	if realDemos == 0 {
		t.Errorf("no demo in %s is real_service; the-recommended-secure-configuration is one, so this test is reading the wrong list", smokeClaimsPath)
	}
	for _, s := range all {
		name := s.Name + ".sh"
		raw, err := os.ReadFile(filepath.Join(smokeScenariosDir, name))
		if err != nil {
			t.Fatal(err)
		}
		script := string(raw)
		exec := smokeExecutableLines(script)
		refusalAt, wired := smokeRefusalLine(exec)
		awsAt, awsLine := smokeFirstAWSLine(exec)

		if !s.Cell.RealService {
			// An emulator claim's aws calls go to the emulator endpoint, so
			// they prove nothing either way. What it must not do is reach for
			// the real-AWS helpers: real_aws_begin unsets that endpoint, and
			// bucket-iam.sh exists to create buckets, roles and keys in the
			// account whose credentials are in the environment. #1379's third
			// mutation was exactly this file, with no refusal in it.
			if refusalAt >= 0 {
				t.Errorf("%s: real_service is false in %s, but line %d refuses to start without SMOKE_REAL_AWS=1", name, smokeClaimsPath, refusalAt+1)
			}
			for i, line := range exec {
				for _, banned := range []string{"real_aws_begin", "bucket-iam.sh", "bucket_up", "role_with_policy"} {
					if strings.Contains(line, banned) {
						t.Errorf("%s: real_service is false in %s, but line %d uses %s, which only runs against a real account: %s", name, smokeClaimsPath, i+1, banned, line)
					}
				}
			}
			continue
		}

		if s.Provider != "aws" {
			t.Errorf("%s: real_service is true on a %s cell; today only an aws proof reaches a real account (SMOKE_REAL_AWS)", s, s.Provider)
		}
		// Without this the ordering check below is vacuous: a scenario no
		// pattern matches would pass it however the refusal is placed.
		if awsAt < 0 {
			t.Errorf("%s: real_service is true in %s and no line in the scenario matches any of the ways this test knows to reach an account, so the ordering check below would prove nothing; either the cell is wrong or smokeTouchesAWS is out of date", s, smokeClaimsPath)
		}
		if refusalAt < 0 {
			t.Errorf("%s: real_service is true in %s and no executable line tests exactly %s; a refusal sitting in a comment, or one whose default is not 0, is not one", s, smokeClaimsPath, smokeRefusal)
		} else {
			if !wired {
				t.Errorf("%s: line %d tests %s and does not follow it with `|| fail`, so the scenario runs on regardless", s, refusalAt+1, smokeRefusal)
			}
			// At column zero, so it runs when the file runs. A refusal in a
			// function body or an if block is one the scenario may never
			// reach, and reads exactly like one it always reaches.
			if raw := strings.Split(script, "\n")[refusalAt]; raw != strings.TrimLeft(raw, " \t") {
				t.Errorf("%s: the refusal on line %d is indented, so it sits inside a block or a function and may never run; it belongs at the top level of the script", s, refusalAt+1)
			}
			if awsAt >= 0 && awsAt < refusalAt {
				t.Errorf("%s: line %d can reach an account before the refusal on line %d: %s", s, awsAt+1, refusalAt+1, awsLine)
			}
		}

		if !strings.Contains(strings.SplitN(script, "\n", 3)[1], "REAL AWS") {
			t.Errorf("%s: the scenario's header line does not say REAL AWS, so `just smoke` lists it like any other", s)
		}
		if note := s.Cell.Note; !strings.Contains(note, "maintainer-run") || !strings.Contains(note, "SMOKE_REAL_AWS=1") {
			t.Errorf("%s: the proof's note must say it is maintainer-run and how to run it; got %q", s, note)
		}
		if strings.Contains(script, "stack_up") {
			t.Errorf("%s: a real-AWS scenario starts the emulator", s)
		}
	}
}

// TestSmokeClaimsProviderCells: every row states every provider in
// provider_order with a status from the fixed vocabulary, and the vocabulary
// means what #1112 ruled. proven is a proof that runs on that provider and
// catches its BREAK control, so a proven cell carries at least one proof: a
// scenario of its own, steps of another claim's scenario on the same
// provider (proven_by), or a gauntlet stage. n/a is a substrate with no
// such concept and carries none. The row's substrate is the providers the promise
// applies to: every cell that is not n/a.
func TestSmokeClaimsProviderCells(t *testing.T) {
	f := readSmokeClaims(t)
	if len(f.ProviderOrder) < 2 || f.ProviderOrder[0] != "aws" {
		t.Fatalf("provider_order = %v, want aws first and at least one more", f.ProviderOrder)
	}
	byID := map[int]smokeClaim{}
	for _, c := range f.Claims {
		byID[c.ID] = c
	}
	for _, c := range f.Claims {
		var applies []string
		scenarios := 0
		for _, p := range f.ProviderOrder {
			cell, ok := c.Providers[p]
			if !ok {
				t.Errorf("claim %d: no cell for provider %q", c.ID, p)
				continue
			}
			if !smokeClaimStatuses[cell.Status] {
				t.Errorf("claim %d, %s: status %q is not proven or n/a; a promise with no proof is a GitHub issue", c.ID, p, cell.Status)
			}
			if cell.Status != "n/a" {
				applies = append(applies, p)
			}
			scenarios += len(cell.scenarios())
			proves := cell.Status == "proven"
			if len(cell.Proofs) > 0 && !proves {
				t.Errorf("claim %d, %s: the cell carries %d proof(s) and reads %q; a cell with proofs reads proven", c.ID, p, len(cell.Proofs), cell.Status)
			}
			if proves && len(cell.Proofs) == 0 {
				t.Errorf("claim %d, %s: proven with no proof; name it, or drop the claim and file an issue", c.ID, p)
			}
			if cell.Status != "proven" && strings.TrimSpace(cell.Note) == "" {
				t.Errorf("claim %d, %s: status %q with no note; say what is true instead", c.ID, p, cell.Status)
			}
			for _, pr := range cell.Proofs {
				if pr.ProvenBy == 0 {
					continue
				}
				by, ok := byID[pr.ProvenBy]
				switch {
				case pr.ProvenBy == c.ID:
					t.Errorf("claim %d, %s: proven_by names the claim itself", c.ID, p)
				case !ok:
					t.Errorf("claim %d, %s: proven_by %d is not a claim", c.ID, p, pr.ProvenBy)
				default:
					found := false
					for _, bp := range by.Providers[p].scenarios() {
						found = found || bp.Command == pr.Command
					}
					if !found {
						t.Errorf("claim %d, %s: proven_by %d with command %q, and claim %d's %s cell carries no proof with that command", c.ID, p, pr.ProvenBy, pr.Command, pr.ProvenBy, p)
					}
				}
				if strings.TrimSpace(pr.Note) == "" {
					t.Errorf("claim %d, %s: proven_by %d with no note; say which steps of that scenario are the proof", c.ID, p, pr.ProvenBy)
				}
			}
		}
		if len(c.Providers) != len(f.ProviderOrder) {
			t.Errorf("claim %d: %d provider cells, provider_order has %d", c.ID, len(c.Providers), len(f.ProviderOrder))
		}
		if scenarios == 0 {
			t.Errorf("claim %d: no cell carries a scenario of its own; a promise nothing runs is not a claim", c.ID)
		}
		if strings.Join(c.Substrate, ",") != strings.Join(applies, ",") {
			t.Errorf("claim %d: substrate is %v and the cells that are not n/a are %v; substrate is the substrates the promise applies to", c.ID, c.Substrate, applies)
		}
	}
}

// smokeProviderSection returns the body of a page's "## On <Name>" section,
// up to the next "## " heading, and whether the page has one.
func smokeProviderSection(page, provider string) (string, bool) {
	head := "\n## On " + smokeProviderNames[provider] + "\n"
	i := strings.Index(page, head)
	if i < 0 {
		return "", false
	}
	body := page[i+len(head):]
	if j := strings.Index(body, "\n## "); j >= 0 {
		body = body[:j]
	}
	return body, true
}

// TestSmokeClaimsSiteCopyAndPages: the site renders a byte-for-byte copy of
// the file, and every promise has exactly one page under live/smoke/claims
// whose front matter names its slug, with a "## On <Provider>" section for
// every cell that carries proofs, and in that section the command of every
// proof in the cell (#1112, #1817), or the estate:stage a stage proof cites. A retired claim's page is a stub
// pointing at the section it moved into, so a link to it from an issue
// still lands; a retired claim that became a demo keeps its page, which
// says so and gives the demo's command.
//
// The pages lived under site/content/docs/claims until GitHub issue #1414
// moved the evidence off the site and beside the scenarios; the site keeps
// a redirect at each old URL, which TestSmokeClaimsSiteRedirects holds. This
// test also held the Hugo site's byte copy of claims.json to the source
// until the copy went with the Hugo site (#2092); the Astro site reads
// live/smoke/claims.json itself.
func TestSmokeClaimsPages(t *testing.T) {
	f := readSmokeClaims(t)
	pages := map[string]bool{}
	entries, err := os.ReadDir(siteClaimsPages)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "README.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		pages[strings.TrimSuffix(e.Name(), ".md")] = true
	}
	known := map[string]bool{}
	bySlugID := map[int]string{}
	sections := 0
	for _, c := range f.Claims {
		known[c.Slug] = true
		bySlugID[c.ID] = c.Slug
		if !pages[c.Slug] {
			t.Errorf("claim %d has no page at %s/%s.md", c.ID, siteClaimsPages, c.Slug)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(siteClaimsPages, c.Slug+".md"))
		if err != nil {
			t.Fatal(err)
		}
		page := string(raw)
		if !strings.Contains(page, "\nclaim: "+c.Slug+"\n") {
			t.Errorf("%s/%s.md front matter does not carry `claim: %s`", siteClaimsPages, c.Slug, c.Slug)
		}
		for _, p := range f.ProviderOrder {
			cell := c.Providers[p]
			if len(cell.Proofs) == 0 {
				continue
			}
			if _, ok := smokeProviderNames[p]; !ok {
				t.Errorf("provider %q has no display name in smokeProviderNames, so its section heading cannot be checked", p)
				continue
			}
			body, ok := smokeProviderSection(page, p)
			if !ok {
				t.Errorf("%s/%s.md: claim %d is %s on %s and the page has no \"## On %s\" section; one page per promise, one section per cell with proofs", siteClaimsPages, c.Slug, c.ID, cell.Status, p, smokeProviderNames[p])
				continue
			}
			sections++
			for _, pr := range cell.Proofs {
				if pr.Stage != "" {
					if !strings.Contains(body, pr.Stage) {
						t.Errorf("%s/%s.md: the \"## On %s\" section never names the gauntlet stage `%s` its cell cites", siteClaimsPages, c.Slug, smokeProviderNames[p], pr.Stage)
					}
					continue
				}
				if !strings.Contains(body, pr.Command) {
					t.Errorf("%s/%s.md: the \"## On %s\" section never tells the reader to run `%s`", siteClaimsPages, c.Slug, smokeProviderNames[p], pr.Command)
				}
			}
		}
	}
	if sections == 0 {
		t.Errorf("no page carries a provider section; this test is checking nothing")
	}
	for _, r := range f.Retired {
		known[r.Slug] = true
		if !pages[r.Slug] {
			t.Errorf("retired claim %d has no stub at %s/%s.md; links to it from issues and pull requests would 404", r.ID, siteClaimsPages, r.Slug)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(siteClaimsPages, r.Slug+".md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "\nretired: "+fmt.Sprint(r.ID)+"\n") {
			t.Errorf("%s/%s.md does not carry front matter `retired: %d`", siteClaimsPages, r.Slug, r.ID)
		}
		if r.Reserved {
			continue
		}
		if r.Demo {
			if want := "just smoke " + r.Slug; !strings.Contains(string(raw), want) {
				t.Errorf("%s/%s.md: retired claim %d is a demo now and its page never gives `%s`", siteClaimsPages, r.Slug, r.ID, want)
			}
			continue
		}
		want := bySlugID[r.Claim] + ".md#on-" + strings.ToLower(smokeProviderNames[r.Provider])
		if r.MergedInto != 0 {
			want = bySlugID[r.MergedInto] + ".md"
		}
		if !strings.Contains(string(raw), "("+want+")") {
			t.Errorf("%s/%s.md is not a stub for retired claim %d linking to %s", siteClaimsPages, r.Slug, r.ID, want)
		}
	}
	var names []string
	for p := range pages {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		if !known[p] {
			t.Errorf("%s/%s.md has no row and no retired entry in %s", siteClaimsPages, p, smokeClaimsPath)
		}
	}
}

// TestSmokeClaimsSiteRedirects: every claim's old site URL still resolves,
// as a redirect to its page in the repository (#1414). Links to
// /docs/claims/<slug>/ are in issues, pull requests and search indexes, and
// a claim whose redirect is missing would answer them with a 404. A retired
// claim's URL redirects to the page of the claim it moved into (#1112; a
// Hugo alias on that claim's stub before #2092); one that became a demo
// (#1817) keeps its own redirect, to its own page.
//
// Since the Astro cutover (#2092) the redirects are entries in
// site/src/data/redirects.json, which site/astro.config.mjs turns into
// pages; scripts/site-url-check.mjs checks the built pages resolve.
func TestSmokeClaimsSiteRedirects(t *testing.T) {
	f := readSmokeClaims(t)
	raw, err := os.ReadFile(siteRedirectJSON)
	if err != nil {
		t.Fatal(err)
	}
	var redirects map[string]string
	if err := json.Unmarshal(raw, &redirects); err != nil {
		t.Fatalf("decode %s: %v", siteRedirectJSON, err)
	}
	expect := func(slug, target string) {
		from := "/docs/claims/" + slug + "/"
		want := claimsGitHubBase + target + ".md"
		got, ok := redirects[from]
		if !ok {
			t.Errorf("%s has no redirect for %s, so that old claim URL would 404; add it, pointing at %s", siteRedirectJSON, from, want)
			return
		}
		if got != want {
			t.Errorf("%s redirects %s to %s, want %s", siteRedirectJSON, from, got, want)
		}
	}
	bySlugID := map[int]string{}
	for _, c := range f.Claims {
		bySlugID[c.ID] = c.Slug
		expect(c.Slug, c.Slug)
	}
	if len(bySlugID) == 0 {
		t.Fatalf("%s lists no claims; this test is checking nothing", smokeClaimsPath)
	}
	for _, r := range f.Retired {
		if r.Demo || r.Reserved {
			expect(r.Slug, r.Slug)
			continue
		}
		target, ok := bySlugID[r.target()]
		if !ok {
			t.Errorf("retired claim %d moved into claim %d, which has no row", r.ID, r.target())
			continue
		}
		expect(r.Slug, target)
	}
}

// smokeTitleMaxWords and smokeTitleMaxRunes bound a claim's title (#1817).
// A title is a promise of about ten words; anything with a step in it goes
// on the claim's page. By September 2026 eleven titles ran past 150
// characters and four (24, 26, 27 and 39) were paragraphs restating their
// scenario's steps, which is how 46 claims grew where 16 promises were.
//
// The issue's rule is about ten words, and the longest approved title is
// eleven ("Identity is a tag you can read, move and carve by", 49
// characters), so the word bound is twelve: one word of slack over the
// approved list, and nowhere near the 20-word sentences that came before.
// The rune bound is there because a word count alone lets a title grow by
// long words and joined clauses; 60 leaves room for a twelve-word title of
// ordinary words while refusing the shape that grew back each time, a
// promise followed by a colon and its proof.
const (
	smokeTitleMaxWords = 12
	smokeTitleMaxRunes = 60
)

// TestSmokeClaimTitlesAreShortPromises: every live claim's title fits the
// bound above. Proving it red: put claim 24's old title back as a live row
// (the 1,200-character paragraph #1817 retired); it fails on both counts.
func TestSmokeClaimTitlesAreShortPromises(t *testing.T) {
	f := readSmokeClaims(t)
	for _, c := range f.Claims {
		words := len(strings.Fields(c.Title))
		runes := len([]rune(c.Title))
		if words > smokeTitleMaxWords || runes > smokeTitleMaxRunes {
			t.Errorf("claim %d's title is %d words and %d characters; a title is a promise of at most %d words and %d characters, and the steps belong on live/smoke/claims/%s.md: %q",
				c.ID, words, runes, smokeTitleMaxWords, smokeTitleMaxRunes, c.Slug, c.Title)
		}
	}
}
