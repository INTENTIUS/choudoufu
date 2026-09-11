// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

// SiteBoardPath is the site's copy of everything the progress pages show
// that is not already in SiteDataPath: the board-wide sentences
// (boardBanner, runtimeBanner), each stage's headline cell, every estate's
// display row and per-stage table, and the manifest facts (lanes, an
// example entry) the "add an estate" page quotes. It is data, not
// markdown: the site renders it (site/content/docs/progress/_content.gotmpl
// builds one page per estate from it, and the gauntlet-board shortcode
// renders the tables), so a change to the site's layout never touches
// this file and a change to this file never touches the layout (#1055).
//
// Before #1055 this tool wrote the progress pages themselves as markdown
// under site/content/docs/progress/, with hugo-book front matter and relref
// links baked in. That coupled every measured figure on the site to one
// theme's page format.
const SiteBoardPath = "site/data/gauntlet_board.json"

// Board is the shape of SiteBoardPath.
type Board struct {
	Schema   int    `json:"schema"`
	Emulator string `json:"emulator"`
	// Banner is boardBanner's sentence: what the rows below actually agree
	// on about the emulator they ran against and when. Markdown.
	Banner string `json:"banner"`
	// RuntimeBanner is runtimeBanner's sentence about recorded durations.
	// Markdown.
	RuntimeBanner string       `json:"runtime_banner"`
	StageCount    int          `json:"stage_count"`
	Stages        []BoardStage `json:"stages"`
	// Estates is every row, core set first, then by name - the order the
	// index table has always used.
	Estates      []BoardEstate   `json:"estates"`
	LiveCert     []BoardLiveCert `json:"live_cert"`
	Lanes        []string        `json:"lanes"`
	ExampleEntry string          `json:"example_entry"`
}

// BoardStage is one row of the stage table.
type BoardStage struct {
	ID         string `json:"id"`
	Order      int    `json:"order"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Headline   bool   `json:"headline"`
	Tier1Gated bool   `json:"tier1_gated"`
	// HeadlineCell is the Headline column as the table prints it: "yes",
	// "no", or "yes (tier-1 gated)".
	HeadlineCell string `json:"headline_cell"`
	// ProvesFirst is the first sentence of Proves, the index table's cell.
	ProvesFirst string `json:"proves_first"`
	Proves      string `json:"proves"`
}

// BoardEstate is one estate's row on the index plus everything its own
// page shows.
type BoardEstate struct {
	Name     string `json:"name"`
	Set      string `json:"set"`
	Lane     string `json:"lane"`
	Clear    bool   `json:"clear"`
	Source   string `json:"source"`
	URL      string `json:"url,omitempty"`
	Pin      string `json:"pin,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Script   string `json:"script"`
	Protocol string `json:"protocol"`
	Notes    string `json:"notes,omitempty"`
	// Cells is one verdict mark per active stage, in stage order: "pass",
	// "FAIL" or "not run".
	Cells []string `json:"cells"`
	// RuntimeTotal is last_run.duration_s as a stopwatch reads it, or "-".
	RuntimeTotal string `json:"runtime_total"`
	// RuntimeCells is "<stage> <duration>, ..." for every active stage
	// that recorded one, or "none recorded yet".
	RuntimeCells string `json:"runtime_cells"`
	// LastRunNote is the page's "Last run at commit ..." sentence, with
	// its **Stale** marker when the emulator pin has moved. Markdown.
	// Empty for a row with no gauntlet-protocol run.
	LastRunNote string `json:"last_run_note,omitempty"`
	// LegacyNote is set instead of LastRunNote for a row whose verdicts
	// were recorded by hand before the script spoke the protocol.
	LegacyNote string `json:"legacy_note,omitempty"`
	// OracleNote is the oracle-provenance sentence (#544), with its
	// **Stale** marker when the oracle pin has moved. Markdown. Empty for
	// a row whose run never recorded an oracle.
	OracleNote string `json:"oracle_note,omitempty"`
	// StageRows is the estate page's own table, one row per stage in the
	// registry (planned and non-headline stages included, labelled).
	StageRows []BoardStageRow `json:"stage_rows"`
}

// BoardStageRow is one row of an estate page's stage table.
type BoardStageRow struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Verdict  string `json:"verdict"`
	Duration string `json:"duration,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// BoardLiveCert is one live-AWS certification row, sorted by estate. It is
// never summed into any bar; the site's own prose beside it says so.
type BoardLiveCert struct {
	Estate  string  `json:"estate"`
	Target  string  `json:"target"`
	Region  string  `json:"region"`
	Clear   bool    `json:"clear"`
	Date    string  `json:"date"`
	Ceiling float64 `json:"ceiling_usd"`
}

// buildBoard computes every display value the progress pages need from the
// artifact and the manifest. Every sentence in it is computed fresh from
// a.Estates on every call, never carried over, so it cannot go stale
// independently of the rows it summarizes (the #414 rule).
func buildBoard(m *Manifest, a *Artifact) Board {
	b := Board{
		Schema:        1,
		Emulator:      a.Emulator,
		Banner:        boardBanner(a),
		RuntimeBanner: runtimeBanner(a),
		StageCount:    len(a.Stages),
		Lanes:         append([]string(nil), KnownLanes...),
		ExampleEntry:  exampleEntryJSON(m),
		LiveCert:      []BoardLiveCert{},
	}
	for _, s := range a.Stages {
		headline := "yes"
		if !s.Headline {
			headline = "no"
		} else if s.Tier1Gated {
			headline = "yes (tier-1 gated)"
		}
		b.Stages = append(b.Stages, BoardStage{
			ID: s.ID, Order: s.Order, Title: s.Title, Status: s.Status,
			Headline: s.Headline, Tier1Gated: s.Tier1Gated,
			HeadlineCell: headline, ProvesFirst: firstSentence(s.Proves), Proves: s.Proves,
		})
	}
	rows := append([]EstateResult(nil), a.Estates...)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Set != rows[j].Set {
			return rows[i].Set == SetCore
		}
		return rows[i].Name < rows[j].Name
	})
	for _, r := range rows {
		b.Estates = append(b.Estates, boardEstate(r, a))
	}
	certs := append([]LiveCertResult(nil), a.LiveCert...)
	sort.SliceStable(certs, func(i, j int) bool { return certs[i].Estate < certs[j].Estate })
	for _, r := range certs {
		b.LiveCert = append(b.LiveCert, BoardLiveCert{
			Estate: r.Estate, Target: r.Target, Region: r.Region,
			Clear: r.Clear, Date: r.Date, Ceiling: r.CeilingUSD,
		})
	}
	return b
}

// boardEstate is one estate's display row and page fields.
func boardEstate(r EstateResult, a *Artifact) BoardEstate {
	e := BoardEstate{
		Name: r.Name, Set: r.Set, Lane: r.Lane, Clear: r.Clear,
		Source: r.Source, URL: r.URL, Pin: r.Pin, Reason: r.Reason,
		Script: r.Script, Protocol: r.Protocol, Notes: r.Notes,
		Cells:        []string{},
		RuntimeTotal: runtimeTotalCell(r),
		RuntimeCells: runtimeStageCells(r, a),
	}
	for _, s := range a.Stages {
		if s.Status == StatusActive {
			e.Cells = append(e.Cells, verdictMark(r.Stages[s.ID]))
		}
		row := BoardStageRow{ID: s.ID, Title: s.Title, Verdict: verdictMark(r.Stages[s.ID])}
		if s.Status != StatusActive {
			row.Title += " (planned)"
		} else if !s.Headline {
			row.Title += " (not a headline stage)"
		}
		if r.LastRun != nil {
			row.Detail = r.LastRun.Detail[s.ID]
			if secs, ok := r.LastRun.Seconds[s.ID]; ok {
				row.Duration = formatDuration(secs)
			}
		}
		e.StageRows = append(e.StageRows, row)
	}
	e.LastRunNote, e.LegacyNote = lastRunNote(r, a)
	e.OracleNote = oracleNote(r, a)
	return e
}

// lastRunNote is the estate page's provenance sentence: commit, date, exit
// code, the emulator image the run actually used, and a **Stale** marker
// when that image is no longer the pin. The second return is the sentence
// for a row that predates the protocol; exactly one of the two is set for
// any row that has recorded anything.
func lastRunNote(r EstateResult, a *Artifact) (note, legacy string) {
	durationNote := ""
	if r.LastRun != nil && r.LastRun.DurationS > 0 {
		durationNote = " Total run time " + formatDuration(r.LastRun.DurationS) + "."
	}
	if r.Protocol != ProtocolGauntlet {
		return "", "Verdicts were recorded from this estate's crossing script by hand before the script spoke the gauntlet protocol; the next run that does will replace them."
	}
	if r.LastRun == nil {
		return "", ""
	}
	switch {
	case r.LastRun.Emulator == "":
		return fmt.Sprintf("Last run at commit `%s` on %s, exit code %d. This run's emulator image was not recorded.%s", short(r.LastRun.Commit), r.LastRun.Date, r.LastRun.ExitCode, durationNote), ""
	case r.LastRun.Emulator == a.Emulator:
		return fmt.Sprintf("Last run at commit `%s` on %s, exit code %d, against emulator image `%s`.%s", short(r.LastRun.Commit), r.LastRun.Date, r.LastRun.ExitCode, r.LastRun.Emulator, durationNote), ""
	default:
		return fmt.Sprintf("Last run at commit `%s` on %s, exit code %d, against emulator image `%s`. **Stale**: the current pin is `%s`.%s", short(r.LastRun.Commit), r.LastRun.Date, r.LastRun.ExitCode, r.LastRun.Emulator, a.Emulator, durationNote), ""
	}
}

// oracleNote is the oracle-provenance sentence (#544): only present once a
// real run has probed it. Silent for every row that predates the field,
// never claiming evidence a run never recorded.
func oracleNote(r EstateResult, a *Artifact) string {
	if r.LastRun == nil || r.LastRun.Oracle == nil {
		return ""
	}
	if *r.LastRun.Oracle == a.Oracle {
		return fmt.Sprintf("Oracle: stock terraform `%s`, stock tofu `%s` (matches the current pin).", r.LastRun.Oracle.Terraform, r.LastRun.Oracle.Tofu)
	}
	return fmt.Sprintf("Oracle: stock terraform `%s`, stock tofu `%s`. **Stale**: the current pin is terraform `%s`, tofu `%s`.", r.LastRun.Oracle.Terraform, r.LastRun.Oracle.Tofu, a.Oracle.Terraform, a.Oracle.Tofu)
}

// Canonical is the board's on-disk form: two-space indented, trailing
// newline, the same shape Artifact.Canonical writes.
func (b Board) Canonical() ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
