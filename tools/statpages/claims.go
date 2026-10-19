// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

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
}

// claimsSpans is the claims-table shortcode as markdown: a row per claim,
// a cell per provider with its status and proofs. A proof with an evidence
// file carries that run's date and commit; a gauntlet-stage proof carries
// the date and commit of the run that measured the stage.
func claimsSpans(root string) (map[string]string, error) {
	var d claimsData
	if err := readJSON(root, ClaimsRel, &d); err != nil {
		return nil, err
	}
	var a gauntletArtifact
	if err := readJSON(root, GauntletArtifactRel, &a); err != nil {
		return nil, err
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
			for _, pr := range pv.Proofs {
				line, err := proofLine(root, a, pr)
				if err != nil {
					return nil, fmt.Errorf("claim %d (%s): %w", c.ID, p, err)
				}
				if line != "" {
					parts = append(parts, line)
				}
			}
			fmt.Fprintf(&s, " | %s", cell(strings.Join(parts, "<br>")))
		}
		s.WriteString(" |\n")
	}
	return map[string]string{"claims": s.String()}, nil
}

func proofLine(root string, a gauntletArtifact, pr claimProof) (string, error) {
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
				return "", err
			}
			if e.Date == "" {
				continue
			}
			ran := fmt.Sprintf(", ran %s", day(e.Date))
			if e.Commit != "" {
				ran += fmt.Sprintf(" at `%s`", short(e.Commit))
			}
			if !seen[ran] {
				seen[ran] = true
				line += ran
			}
		}
		return line, nil
	case pr.ProvenBy != 0:
		return fmt.Sprintf("proven by claim %d: `%s`", pr.ProvenBy, pr.Command), nil
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
			}
		}
		return line, nil
	}
	return "", nil
}
