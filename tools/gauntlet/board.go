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
	RuntimeBanner string `json:"runtime_banner"`
	// ScriptBanner is scriptStaleBanner's sentence: how many rows below
	// were measured before their own estate directory, or the shared
	// protocol library they source, last changed (#1264, #1292).
	// Markdown. Empty when the board was built with no checkout to read,
	// which says nothing rather than claiming everything is current.
	ScriptBanner string       `json:"script_banner,omitempty"`
	StageCount   int          `json:"stage_count"`
	Stages       []BoardStage `json:"stages"`
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
	Name string `json:"name"`
	Set  string `json:"set"`
	Lane string `json:"lane"`
	// Substrate is the platform the script runs against, empty for the
	// floci emulator (#1067).
	Substrate string `json:"substrate,omitempty"`
	Clear     bool   `json:"clear"`
	Source    string `json:"source"`
	URL       string `json:"url,omitempty"`
	Pin       string `json:"pin,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Script    string `json:"script"`
	Protocol  string `json:"protocol"`
	Notes     string `json:"notes,omitempty"`
	// Cells is one verdict mark per active stage, in stage order: "pass",
	// "FAIL", "not run", "n/a" (the stage does not apply on the estate's
	// substrate; the estate page's row carries the reason) or "stale" (the
	// verdict was measured by a run other than the one this row records -
	// #1069; the estate page's row and StaleNote below say which verdict
	// and from when).
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
	// StaleNote is staleStagesNote's sentence: how many of this row's
	// verdicts were carried forward from an earlier run rather than
	// measured by the run recorded below (#1069). Markdown. Empty when the
	// row carries none, which is every row whose last run reached every
	// stage and every row written before per-stage provenance existed.
	StaleNote string `json:"stale_note,omitempty"`
	// ScriptStale is this row's whole-row staleness against the watched
	// set - its own estate directory and live/e2e/lib/, the protocol
	// library it sources (#1264, #1292): "changed", "unknown", or empty
	// for a row where neither has moved since the run below measured it.
	// It is the index table's badge; ScriptNote is the sentence, and the
	// sentence says which of the two changed.
	//
	// A different fact from StaleNote above, which is about one RUN
	// aborting before it reached a stage (#1069). This one is about the
	// SCRIPT changing after the run finished.
	ScriptStale string `json:"script_stale,omitempty"`
	// ScriptNote is scriptStaleNote's sentence. Markdown.
	ScriptNote string `json:"script_note,omitempty"`
	// StageRows is the estate page's own table, one row per stage in the
	// registry (planned and non-headline stages included, labelled).
	StageRows []BoardStageRow `json:"stage_rows"`
}

// BoardStageRow is one row of an estate page's stage table.
type BoardStageRow struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Verdict string `json:"verdict"`
	// Provenance is stageProvenanceNote's sentence, set only when this
	// stage's verdict was carried forward from a run other than the one
	// the row records (#1069). Markdown. The site prints it in the same
	// cell as Detail, so a carried verdict says so wherever it is read.
	Provenance string `json:"provenance,omitempty"`
	Duration   string `json:"duration,omitempty"`
	Detail     string `json:"detail,omitempty"`
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
//
// st is per-row script staleness (#1264), read from the checkout by the
// caller for the same reason Render takes tt and scale rather than
// re-deriving them: this function is handed data and stays pure, and a
// caller rendering into a temp directory still reports the real checkout's
// answer. A nil map is "no checkout was read", and every field it feeds
// stays empty rather than claiming every row is current.
func buildBoard(m *Manifest, a *Artifact, st map[string]ScriptStaleness) Board {
	b := Board{
		Schema:        1,
		Emulator:      a.Emulator,
		Banner:        boardBanner(a),
		RuntimeBanner: runtimeBanner(a),
		ScriptBanner:  scriptStaleBanner(a, st),
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
		b.Estates = append(b.Estates, boardEstate(r, a, st[r.Name]))
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

// boardEstate is one estate's display row and page fields. s is this row's
// script staleness (#1264); its zero value renders nothing.
func boardEstate(r EstateResult, a *Artifact, s ScriptStaleness) BoardEstate {
	e := BoardEstate{
		Name: r.Name, Set: r.Set, Lane: r.Lane, Substrate: r.Substrate, Clear: r.Clear,
		Source: r.Source, URL: r.URL, Pin: r.Pin, Reason: r.Reason,
		Script: r.Script, Protocol: r.Protocol, Notes: r.Notes,
		Cells:        []string{},
		RuntimeTotal: runtimeTotalCell(r),
		RuntimeCells: runtimeStageCells(r, a),
		StaleNote:    staleStagesNote(r),
		ScriptNote:   scriptStaleNote(s, EstateDir(r)),
	}
	if s.State == ScriptChanged || s.State == ScriptUnknown {
		e.ScriptStale = s.State
	}
	for _, s := range a.Stages {
		carried := r.StageCarried(s.ID)
		if s.Status == StatusActive {
			e.Cells = append(e.Cells, verdictMarkFor(r.Stages[s.ID], carried))
		}
		row := BoardStageRow{ID: s.ID, Title: s.Title, Verdict: verdictMarkFor(r.Stages[s.ID], carried), Provenance: stageProvenanceNote(r, s.ID)}
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
		if r.Stages[s.ID] == VerdictNA {
			// The reason the stage does not apply here is the row's detail,
			// so an n/a cell is never a silent skip (#1067).
			sub := r.Substrate
			if sub == "" {
				sub = SubstrateFloci
			}
			row.Detail, _ = s.NotApplicable(sub)
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
