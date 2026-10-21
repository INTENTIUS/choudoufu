// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"fmt"
	"strings"
)

// measured is the newest dated run behind a proof.
type measured struct{ Date, Commit string }

// newer reports whether m is a later day than o (a nil o is older).
func (m measured) newer(o *measured) bool {
	return o == nil || day(m.Date) > day(o.Date)
}

func (m measured) String() string {
	return fmt.Sprintf("last proven %s at `%s`", day(m.Date), short(m.Commit))
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
		Status string `json:"status"`
		Commit string `json:"commit"`
		Date   string `json:"date"`
	} `json:"last_run"`
}

// claimsSpans is the claims table as markdown: a row per claim, a cell per
// provider listing each proof's command and the date and commit it was last
// proven at. A run is a scenario's evidence file or carried last_run, the
// run of the scenario another claim's proof is steps of, or the gauntlet
// run that measured a cited stage.
func claimsSpans(root string) (map[string]string, error) {
	var d claimsData
	if err := readJSON(root, ClaimsRel, &d); err != nil {
		return nil, err
	}
	var a gauntletArtifact
	if err := readJSON(root, GauntletArtifactRel, &a); err != nil {
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
			if !ok || len(pv.Proofs) == 0 {
				if ok && pv.Status == "n/a" {
					s.WriteString(" | n/a")
				} else {
					s.WriteString(" | ")
				}
				continue
			}
			var lines []string
			for _, pr := range pv.Proofs {
				line, err := proofLine(root, a, pr, byID[pr.ProvenBy][p].Proofs)
				if err != nil {
					return nil, fmt.Errorf("claim %d (%s): %w", c.ID, p, err)
				}
				lines = append(lines, line)
			}
			fmt.Fprintf(&s, " | %s", cell(strings.Join(lines, "<br>")))
		}
		s.WriteString(" |\n")
	}
	return map[string]string{"claims": s.String()}, nil
}

// proofLine is one proof as a line. source is the cell a proven_by proof
// takes its scenario's run from.
func proofLine(root string, a gauntletArtifact, pr claimProof, source []claimProof) (string, error) {
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
		run, err := scenarioRun(root, pr)
		if err != nil {
			return "", err
		}
		if run != nil {
			line += ", " + run.String()
		}
		return line, nil
	case pr.ProvenBy != 0:
		line := fmt.Sprintf("`%s` (claim %d)", pr.Command, pr.ProvenBy)
		for _, src := range source {
			if src.Scenario == "" || src.Command != pr.Command {
				continue
			}
			run, err := scenarioRun(root, src)
			if err != nil {
				return "", err
			}
			if run != nil {
				line += ", " + run.String()
			}
		}
		return line, nil
	case pr.Stage != "":
		line := fmt.Sprintf("gauntlet stage `%s`", pr.Stage)
		estate, stage, _ := strings.Cut(pr.Stage, ":")
		for _, e := range a.Estates {
			if e.Name != estate || e.Stages[stage] != "pass" {
				continue
			}
			run, ok := e.StageRuns[stage]
			if !ok && e.LastRun != nil {
				run = *e.LastRun
			}
			if run.Date != "" {
				line += ", " + measured{Date: run.Date, Commit: run.Commit}.String()
			}
		}
		return line, nil
	}
	return "", nil
}

// scenarioRun is the newest dated run among a scenario proof's evidence
// files and its carried last_run, or nil when none is on record.
func scenarioRun(root string, pr claimProof) (*measured, error) {
	var newest *measured
	for _, ev := range pr.Evidence {
		var e runStamp
		if err := readJSON(root, ev, &e); err != nil {
			return nil, err
		}
		if m := (measured{Date: e.Date, Commit: e.Commit}); e.Date != "" && m.newer(newest) {
			newest = &m
		}
	}
	if lr := pr.LastRun; lr != nil && lr.Status == "proven" && lr.Date != "" {
		if m := (measured{Date: lr.Date, Commit: lr.Commit}); m.newer(newest) {
			newest = &m
		}
	}
	return newest, nil
}
