// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ChangelogRel is where the newest release is read from: a run older than
// it is stale.
const ChangelogRel = "CHANGELOG.md"

// releaseHeading matches a released CHANGELOG.md heading, "## choudoufu
// v0.30.0 (2026-10-18)"; "(Unreleased)" does not match.
var releaseHeading = regexp.MustCompile(`(?m)^## choudoufu (v\S+) \((\d{4}-\d{2}-\d{2})\)\s*$`)

// release is the newest released version and its day.
type release struct{ Version, Date string }

func latestRelease(root string) (release, error) {
	b, err := os.ReadFile(filepath.Join(root, ChangelogRel))
	if err != nil {
		return release{}, err
	}
	m := releaseHeading.FindSubmatch(b)
	if m == nil {
		return release{}, fmt.Errorf("%s names no released version", ChangelogRel)
	}
	return release{Version: string(m[1]), Date: string(m[2])}, nil
}

// measured is one dated run behind a proof. Version is set when the record
// names it (a carried last_run); an evidence file or a stage has a date.
type measured struct{ Date, Commit, Version string }

// staleAt reports whether m was measured before r: at an older version
// when m names one, otherwise on a day before r's.
func (m measured) staleAt(r release) bool {
	if m.Version != "" {
		return m.Version != r.Version
	}
	return day(m.Date) < r.Date
}

// claimsData is live/smoke/claims.json, as far as the page reads it.
type claimsData struct {
	ProviderOrder []string `json:"provider_order"`
	Claims        []struct {
		ID        int                      `json:"id"`
		Slug      string                   `json:"slug"`
		Title     string                   `json:"title"`
		Theme     string                   `json:"theme"`
		Providers map[string]claimProvider `json:"providers"`
	} `json:"claims"`
}

type claimProvider struct {
	Status string       `json:"status"`
	Proofs []claimProof `json:"proofs"`
}

type claimProof struct {
	Scenario      string   `json:"scenario"`
	Command       string   `json:"command"`
	Minutes       int      `json:"minutes"`
	NeedsGo       bool     `json:"needs_go"`
	NeedsEmulator bool     `json:"needs_emulator"`
	RealService   bool     `json:"real_service"`
	Evidence      []string `json:"evidence"`
	ProvenBy      int      `json:"proven_by"`
	Stage         string   `json:"stage"`
	LastRun       *struct {
		Status  string `json:"status"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
		Version string `json:"version"`
	} `json:"last_run"`
}

// claimsSpans is the claims-table shortcode as markdown: a row per claim,
// a cell per provider with its status and proofs. A proof with an evidence
// file carries that run's date and commit; a carried last_run carries its
// date, commit and version; a gauntlet-stage proof carries the date and
// commit of the run that measured the stage. A run older than the newest
// release in CHANGELOG.md says stale, and so does its cell: a release
// never resets a result, it only ages it.
func claimsSpans(root string) (map[string]string, error) {
	var d claimsData
	if err := readJSON(root, ClaimsRel, &d); err != nil {
		return nil, err
	}
	var a gauntletArtifact
	if err := readJSON(root, GauntletArtifactRel, &a); err != nil {
		return nil, err
	}
	rel, err := latestRelease(root)
	if err != nil {
		return nil, err
	}
	byID := map[int]map[string]claimProvider{}
	for _, c := range d.Claims {
		byID[c.ID] = c.Providers
	}
	var s strings.Builder
	s.WriteString("| # | Claim | Theme")
	for _, p := range d.ProviderOrder {
		fmt.Fprintf(&s, " | %s", strings.ToUpper(p))
	}
	s.WriteString(" |\n|---|---|---")
	for range d.ProviderOrder {
		s.WriteString("|---")
	}
	s.WriteString("|\n")
	for _, c := range d.Claims {
		fmt.Fprintf(&s, "| %d | [%s](smoke/claims/%s.md) | %s", c.ID, cell(c.Title), c.Slug, c.Theme)
		for _, p := range d.ProviderOrder {
			pv, ok := c.Providers[p]
			if !ok {
				s.WriteString(" | ")
				continue
			}
			parts := []string{pv.Status}
			stale := false
			for _, pr := range pv.Proofs {
				line, run, err := proofLine(root, a, pr, rel)
				if err == nil && pr.ProvenBy != 0 {
					// steps of another claim's scenario age with that scenario's run
					for _, src := range byID[pr.ProvenBy][p].Proofs {
						if src.Scenario != "" && src.Command == pr.Command {
							_, run, err = proofLine(root, a, src, rel)
						}
					}
					if run != nil && run.staleAt(rel) {
						line += ", stale"
					}
				}
				if err != nil {
					return nil, fmt.Errorf("claim %d (%s): %w", c.ID, p, err)
				}
				if run != nil && run.staleAt(rel) {
					stale = true
				}
				if line != "" {
					parts = append(parts, line)
				}
			}
			if stale {
				parts[0] += fmt.Sprintf(", stale: measured before %s", rel.Version)
			}
			fmt.Fprintf(&s, " | %s", cell(strings.Join(parts, "<br>")))
		}
		s.WriteString(" |\n")
	}
	return map[string]string{"claims": s.String()}, nil
}

// proofLine is one proof as a line, and the newest dated run behind it
// (nil when none is on record).
func proofLine(root string, a gauntletArtifact, pr claimProof, rel release) (string, *measured, error) {
	var newest *measured
	switch {
	case pr.Scenario != "":
		line := fmt.Sprintf("`%s` ~%d min", pr.Command, pr.Minutes)
		if pr.NeedsGo {
			line += ", needs Go"
		}
		if pr.NeedsEmulator {
			line += ", needs the emulator"
		}
		if pr.RealService {
			line += ", real AWS, maintainer-run"
		}
		seen := map[string]bool{}
		for _, ev := range pr.Evidence {
			var e runStamp
			if err := readJSON(root, ev, &e); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					line += fmt.Sprintf(", evidence `%s` missing", filepath.Base(ev))
					continue
				}
				return "", nil, err
			}
			if e.Date == "" {
				continue
			}
			m := measured{Date: e.Date, Commit: e.Commit}
			if newest == nil || day(m.Date) > day(newest.Date) {
				newest = &m
			}
			ran := fmt.Sprintf(", ran %s", day(e.Date))
			if e.Commit != "" {
				ran += fmt.Sprintf(" at `%s`", short(e.Commit))
			}
			if m.staleAt(rel) {
				ran += ", stale"
			}
			if !seen[ran] {
				seen[ran] = true
				line += ran
			}
		}
		if lr := pr.LastRun; lr != nil && lr.Date != "" && (newest == nil || day(lr.Date) > day(newest.Date)) {
			m := measured{Date: lr.Date, Commit: lr.Commit, Version: lr.Version}
			newest = &m
			line += fmt.Sprintf(", last %s %s at `%s` (%s)", lr.Status, day(lr.Date), short(lr.Commit), lr.Version)
			if m.staleAt(rel) {
				line += ", stale"
			}
		}
		return line, newest, nil
	case pr.ProvenBy != 0:
		return fmt.Sprintf("proven by claim %d: `%s`", pr.ProvenBy, pr.Command), nil, nil
	case pr.Stage != "":
		line := fmt.Sprintf("gauntlet stage `%s`", pr.Stage)
		estate, stage, _ := strings.Cut(pr.Stage, ":")
		for _, e := range a.Estates {
			if e.Name != estate {
				continue
			}
			run, ok := e.StageRuns[stage]
			if !ok && e.LastRun != nil {
				run = *e.LastRun
			}
			if v := e.Stages[stage]; v != "" {
				line += ", " + v
			}
			if run.Date != "" {
				line += fmt.Sprintf(" %s at `%s`", day(run.Date), short(run.Commit))
				m := measured{Date: run.Date, Commit: run.Commit}
				newest = &m
				if m.staleAt(rel) {
					line += ", stale"
				}
			}
		}
		return line, newest, nil
	}
	return "", nil, nil
}
