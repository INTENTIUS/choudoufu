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
// scenarios prove (#1055): id, slug, scenario, run time, per-provider
// status. Before it existed the twenty claims lived only as prose in one
// 12,700-word site page, and nothing tied a claim to the scenario that runs
// it. This test ties the three together: the JSON, the scenario directory,
// and the site's per-claim pages plus its data copy.
//
// Proving it red: add a scenario file with a "CLAIM 21" header and no row,
// or a row whose slug has no page, or edit site/data/claims.json by hand;
// each fails a different check below.

const (
	smokeClaimsPath   = "smoke/claims.json"
	smokeScenariosDir = "smoke/scenarios"
	siteClaimsCopy    = "../site/data/claims.json"
	siteClaimsPages   = "../site/content/docs/claims"
)

// smokeDemoScenarios are the scenarios that are demos, not claims. They
// carry no CLAIM header and no row; anything else under the scenario
// directory must have both.
var smokeDemoScenarios = map[string]bool{"import": true, "greenfield": true, "full": true}

type smokeClaimsFile struct {
	Themes        map[string]string `json:"themes"`
	ProviderOrder []string          `json:"provider_order"`
	Claims        []smokeClaim      `json:"claims"`
}

type smokeClaim struct {
	ID        int                               `json:"id"`
	Slug      string                            `json:"slug"`
	Title     string                            `json:"title"`
	Scenario  string                            `json:"scenario"`
	Command   string                            `json:"command"`
	Minutes   int                               `json:"minutes"`
	NeedsGo   bool                              `json:"needs_go"`
	Theme     string                            `json:"theme"`
	BreakMode string                            `json:"break_mode"`
	Substrate string                            `json:"substrate"`
	Providers map[string]smokeClaimProviderCell `json:"providers"`
	Evidence  []string                          `json:"evidence"`
}

type smokeClaimProviderCell struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

var smokeClaimStatuses = map[string]bool{"proven": true, "restated": true, "n/a": true, "open": true}

// scenarioHeader is the second line of every claim scenario:
// "# CLAIM 13 - The tag is the boundary: ... ~4 min." The number and the
// minutes must agree with the row.
var scenarioHeader = regexp.MustCompile(`^# CLAIM (\d+) - .*~(\d+) min\.?\s*$`)

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
	return f
}

// TestSmokeClaimsMatchScenarios: every claim scenario has exactly one row
// and every row names a scenario that exists, with the id and minutes the
// script's own header states.
func TestSmokeClaimsMatchScenarios(t *testing.T) {
	f := readSmokeClaims(t)
	bySlug := map[string]smokeClaim{}
	ids := map[int]bool{}
	for _, c := range f.Claims {
		if _, dup := bySlug[c.Slug]; dup {
			t.Errorf("slug %q appears twice", c.Slug)
		}
		bySlug[c.Slug] = c
		if ids[c.ID] {
			t.Errorf("claim id %d appears twice", c.ID)
		}
		ids[c.ID] = true
		if want := filepath.ToSlash(filepath.Join("live", smokeScenariosDir, c.Slug+".sh")); c.Scenario != want {
			t.Errorf("claim %d: scenario is %q, want %q (the slug is the scenario name)", c.ID, c.Scenario, want)
		}
		if want := "just smoke " + c.Slug; c.Command != want {
			t.Errorf("claim %d: command is %q, want %q", c.ID, c.Command, want)
		}
		if _, ok := f.Themes[c.Theme]; !ok {
			t.Errorf("claim %d: theme %q is not in the file's themes map", c.ID, c.Theme)
		}
		if c.BreakMode == "" {
			t.Errorf("claim %d: break_mode is empty; every claim ships with its failure demonstrated", c.ID)
		}
		for _, ev := range c.Evidence {
			if _, err := os.Stat(filepath.Join("..", filepath.FromSlash(ev))); err != nil {
				t.Errorf("claim %d: evidence %q does not exist", c.ID, ev)
			}
		}
	}
	for i := 1; i <= len(f.Claims); i++ {
		if !ids[i] {
			t.Errorf("claim ids are not contiguous: %d is missing", i)
		}
	}

	entries, err := os.ReadDir(smokeScenariosDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".sh")
		if !strings.HasSuffix(e.Name(), ".sh") || smokeDemoScenarios[name] {
			continue
		}
		seen++
		c, ok := bySlug[name]
		if !ok {
			t.Errorf("scenario %s has no row in %s (or belongs in smokeDemoScenarios)", e.Name(), smokeClaimsPath)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(smokeScenariosDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(string(raw), "\n", 3)
		if len(lines) < 2 {
			t.Errorf("%s has no header line", e.Name())
			continue
		}
		m := scenarioHeader.FindStringSubmatch(lines[1])
		if m == nil {
			t.Errorf("%s line 2 is not a \"# CLAIM N - ... ~M min.\" header: %q", e.Name(), lines[1])
			continue
		}
		if fmt.Sprint(c.ID) != m[1] {
			t.Errorf("%s header says CLAIM %s, %s says id %d", e.Name(), m[1], smokeClaimsPath, c.ID)
		}
		if fmt.Sprint(c.Minutes) != m[2] {
			t.Errorf("%s header says ~%s min, %s says %d", e.Name(), m[2], smokeClaimsPath, c.Minutes)
		}
	}
	if seen != len(f.Claims) {
		t.Errorf("%d claim scenarios on disk, %d rows in %s", seen, len(f.Claims), smokeClaimsPath)
	}
}

// TestSmokeClaimsProviderCells: every row states every provider in
// provider_order with a status from the fixed vocabulary, the provider the
// scenario itself runs on (substrate) is proven, and a cell that is not
// proven carries a note saying what is true instead.
func TestSmokeClaimsProviderCells(t *testing.T) {
	f := readSmokeClaims(t)
	if len(f.ProviderOrder) < 2 || f.ProviderOrder[0] != "aws" {
		t.Fatalf("provider_order = %v, want aws first and at least one more", f.ProviderOrder)
	}
	for _, c := range f.Claims {
		if _, ok := c.Providers[c.Substrate]; !ok {
			t.Errorf("claim %d: substrate %q is not one of its provider cells", c.ID, c.Substrate)
		}
		for _, p := range f.ProviderOrder {
			cell, ok := c.Providers[p]
			if !ok {
				t.Errorf("claim %d: no cell for provider %q", c.ID, p)
				continue
			}
			if !smokeClaimStatuses[cell.Status] {
				t.Errorf("claim %d, %s: status %q is not one of proven/restated/n/a/open", c.ID, p, cell.Status)
			}
			if p == c.Substrate && cell.Status != "proven" {
				t.Errorf("claim %d: %s status is %q, but the scenario runs on %s; a scenario that runs and passes is proven there", c.ID, p, cell.Status, c.Substrate)
			}
			if cell.Status != "proven" && strings.TrimSpace(cell.Note) == "" {
				t.Errorf("claim %d, %s: status %q with no note; say what is true instead", c.ID, p, cell.Status)
			}
		}
		if len(c.Providers) != len(f.ProviderOrder) {
			t.Errorf("claim %d: %d provider cells, provider_order has %d", c.ID, len(c.Providers), len(f.ProviderOrder))
		}
	}
}

// TestSmokeClaimsSiteCopyAndPages: the site renders a byte-for-byte copy
// of the file, and every claim has exactly one page under
// site/content/docs/claims whose front matter names its slug.
func TestSmokeClaimsSiteCopyAndPages(t *testing.T) {
	src, err := os.ReadFile(smokeClaimsPath)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := os.ReadFile(siteClaimsCopy)
	if err != nil {
		t.Fatalf("read %s: %v (cp %s %s)", siteClaimsCopy, err, smokeClaimsPath, siteClaimsCopy)
	}
	if string(src) != string(cp) {
		t.Errorf("%s differs from %s; copy it (the site renders the copy, the smoke owns the source)", siteClaimsCopy, smokeClaimsPath)
	}

	f := readSmokeClaims(t)
	pages := map[string]bool{}
	entries, err := os.ReadDir(siteClaimsPages)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "_index.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		pages[strings.TrimSuffix(e.Name(), ".md")] = true
	}
	var slugs []string
	for _, c := range f.Claims {
		slugs = append(slugs, c.Slug)
		if !pages[c.Slug] {
			t.Errorf("claim %d has no page at %s/%s.md", c.ID, siteClaimsPages, c.Slug)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(siteClaimsPages, c.Slug+".md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "\nclaim: "+c.Slug+"\n") {
			t.Errorf("%s/%s.md front matter does not carry `claim: %s`", siteClaimsPages, c.Slug, c.Slug)
		}
		if !strings.Contains(string(raw), c.Command) {
			t.Errorf("%s/%s.md never tells the reader to run `%s`", siteClaimsPages, c.Slug, c.Command)
		}
	}
	sort.Strings(slugs)
	for p := range pages {
		if i := sort.SearchStrings(slugs, p); i >= len(slugs) || slugs[i] != p {
			t.Errorf("%s/%s.md has no row in %s", siteClaimsPages, p, smokeClaimsPath)
		}
	}
}
