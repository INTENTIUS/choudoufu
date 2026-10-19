// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"fmt"
	"sort"
	"strings"
)

// The board's two inputs, decoded only as far as the page reads them. The
// sentences (banner, runtime banner, script banner, ...) are computed by
// tools/gauntlet's buildBoard and read here as written, so this page and
// the site's progress page cannot word one measurement two ways.
type boardData struct {
	Banner         string `json:"banner"`
	RuntimeBanner  string `json:"runtime_banner"`
	ScriptBanner   string `json:"script_banner"`
	ProviderBanner string `json:"provider_banner"`
	EngineBanner   string `json:"engine_banner"`
	Stages         []struct {
		ID           string `json:"id"`
		Order        int    `json:"order"`
		Title        string `json:"title"`
		Status       string `json:"status"`
		HeadlineCell string `json:"headline_cell"`
		ProvesFirst  string `json:"proves_first"`
	} `json:"stages"`
	Estates []struct {
		Name         string   `json:"name"`
		Set          string   `json:"set"`
		Lane         string   `json:"lane"`
		Clear        bool     `json:"clear"`
		Script       string   `json:"script"`
		Cells        []string `json:"cells"`
		ScriptStale  string   `json:"script_stale"`
		RuntimeTotal string   `json:"runtime_total"`
		RuntimeCells string   `json:"runtime_cells"`
	} `json:"estates"`
}

type tally struct {
	Pass   int `json:"pass"`
	Fail   int `json:"fail"`
	NotRun int `json:"not_run"`
	NA     int `json:"n_a"`
	Stale  int `json:"stale"`
}

type setSummary struct {
	Label    string           `json:"label"`
	Estates  int              `json:"estates"`
	Clear    int              `json:"clear"`
	Verified *int             `json:"verified"`
	Carried  *int             `json:"carried"`
	Stages   map[string]tally `json:"stages"`
}

type runStamp struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
}

type gauntletArtifact struct {
	Emulator        string                `json:"emulator"`
	Sets            map[string]setSummary `json:"sets"`
	Lanes           map[string]setSummary `json:"lanes"`
	BehaviorsTotal  *int                  `json:"behaviors_total"`
	BehaviorsProven int                   `json:"behaviors_proven"`
	Estates         []struct {
		Name      string              `json:"name"`
		LastRun   *runStamp           `json:"last_run"`
		StageRuns map[string]runStamp `json:"stage_runs"`
		Stages    map[string]string   `json:"stages"`
	} `json:"estates"`
	LiveCert []struct {
		Estate     string  `json:"estate"`
		Target     string  `json:"target"`
		Region     string  `json:"region"`
		Clear      bool    `json:"clear"`
		Commit     string  `json:"commit"`
		Date       string  `json:"date"`
		CeilingUSD float64 `json:"ceiling_usd"`
	} `json:"live_cert"`
}

// cellMark is the estates table's one-character form of a board cell.
var cellMark = map[string]string{
	"pass":    "P",
	"FAIL":    "**F**",
	"not run": "-",
	"n/a":     "n/a",
	"stale":   "s",
}

func boardSpans(root string) (map[string]string, error) {
	var b boardData
	if err := readJSON(root, GauntletBoardRel, &b); err != nil {
		return nil, err
	}
	var a gauntletArtifact
	if err := readJSON(root, GauntletArtifactRel, &a); err != nil {
		return nil, err
	}
	return map[string]string{
		"banner":   boardBannerSpan(b),
		"bars":     boardBarsSpan(b, a),
		"stages":   boardStagesSpan(b),
		"estates":  boardEstatesSpan(b, a),
		"runtime":  boardRuntimeSpan(b),
		"livecert": boardLiveCertSpan(a),
	}, nil
}

func boardBannerSpan(b boardData) string {
	var s strings.Builder
	for _, p := range []string{b.Banner, b.EngineBanner, b.ProviderBanner, b.ScriptBanner} {
		if p != "" {
			s.WriteString(p + "\n\n")
		}
	}
	return s.String()
}

// boardBarsSpan is the gauntlet-bars shortcode as tables: the two emulator
// bars (core, all) and the kubernetes lane, then each active stage's tally
// over each bar, read from the artifact's own summaries (tallyRows in
// tools/gauntlet/artifact.go), never recounted here.
func boardBarsSpan(b boardData, a gauntletArtifact) string {
	var bars []setSummary
	for _, k := range []string{"core", "all"} {
		if s, ok := a.Sets[k]; ok {
			bars = append(bars, s)
		}
	}
	if s, ok := a.Lanes["kubernetes"]; ok {
		bars = append(bars, s)
	}
	var s strings.Builder
	s.WriteString("| Bar | Clear | Verified on the current pins | Carried from an earlier pin |\n|---|---|---|---|\n")
	for _, x := range bars {
		v, c := "-", "-"
		if x.Verified != nil {
			v = fmt.Sprint(*x.Verified)
		}
		if x.Carried != nil {
			c = fmt.Sprint(*x.Carried)
		}
		fmt.Fprintf(&s, "| %s | %d of %d | %s | %s |\n", cell(x.Label), x.Clear, x.Estates, v, c)
	}
	s.WriteString("\nPer active stage, over the same bars:\n\n| Stage")
	for _, x := range bars {
		fmt.Fprintf(&s, " | %s", cell(x.Label))
	}
	s.WriteString(" |\n|---")
	for range bars {
		s.WriteString("|---")
	}
	s.WriteString("|\n")
	for _, st := range b.Stages {
		if st.Status != "active" {
			continue
		}
		fmt.Fprintf(&s, "| %s", cell(st.Title))
		for _, x := range bars {
			t := x.Stages[st.ID]
			parts := []string{fmt.Sprintf("%d pass", t.Pass), fmt.Sprintf("%d fail", t.Fail), fmt.Sprintf("%d not run", t.NotRun)}
			if t.NA > 0 {
				parts = append(parts, fmt.Sprintf("%d n/a", t.NA))
			}
			if t.Stale > 0 {
				parts = append(parts, fmt.Sprintf("%d stale", t.Stale))
			}
			fmt.Fprintf(&s, " | %s", strings.Join(parts, ", "))
		}
		s.WriteString(" |\n")
	}
	if a.BehaviorsTotal != nil {
		fmt.Fprintf(&s, "\nBehaviors proven: %d of %d stages have their representative-set fixtures passing against the tier-1 behavior matrix. It is a development-loop signal, not a coverage claim.\n", a.BehaviorsProven, *a.BehaviorsTotal)
	}
	return s.String()
}

func boardStagesSpan(b boardData) string {
	var s strings.Builder
	s.WriteString("| # | Stage | Status | Headline | What a pass proves |\n|---|---|---|---|---|\n")
	for _, st := range b.Stages {
		fmt.Fprintf(&s, "| %d | %s | %s | %s | %s |\n", st.Order, cell(st.Title), st.Status, st.HeadlineCell, cell(st.ProvesFirst))
	}
	return s.String()
}

// boardEstatesSpan is every estate's row: one column per active stage (by
// the stage table's number), and the commit and date of the run the row
// records, so every verdict carries its provenance.
func boardEstatesSpan(b boardData, a gauntletArtifact) string {
	last := map[string]*runStamp{}
	for _, e := range a.Estates {
		last[e.Name] = e.LastRun
	}
	var active []int
	for _, st := range b.Stages {
		if st.Status == "active" {
			active = append(active, st.Order)
		}
	}
	var s strings.Builder
	s.WriteString("P pass, **F** fail, - not run, n/a does not apply on this substrate, s stale (carried from an earlier run than the one the row records). Columns are the stage numbers above.\n\n")
	s.WriteString("| Estate | Set | Lane | Clear")
	for _, n := range active {
		fmt.Fprintf(&s, " | %d", n)
	}
	s.WriteString(" | Last run |\n|---|---|---|---")
	for range active {
		s.WriteString("|---")
	}
	s.WriteString("|---|\n")
	for _, e := range b.Estates {
		clear := "no"
		if e.Clear {
			clear = "yes"
		}
		if e.ScriptStale != "" {
			clear += ", script " + e.ScriptStale
		}
		name := e.Name
		if dir, ok := strings.CutSuffix(e.Script, "/run.sh"); ok && strings.HasPrefix(dir, "live/") {
			name = fmt.Sprintf("[%s](%s/)", e.Name, strings.TrimPrefix(dir, "live/"))
		}
		// The anchor is what the Hugo site's per-estate progress pages
		// (/docs/progress/<estate>/) redirect to since the cutover (#2092).
		fmt.Fprintf(&s, "| <a id=\"%s\"></a>%s | %s | %s | %s", e.Name, name, e.Set, e.Lane, clear)
		for _, c := range e.Cells {
			m, ok := cellMark[c]
			if !ok {
				m = cell(c)
			}
			fmt.Fprintf(&s, " | %s", m)
		}
		run := "no protocol run"
		if lr := last[e.Name]; lr != nil && lr.Commit != "" {
			run = fmt.Sprintf("`%s` %s", short(lr.Commit), day(lr.Date))
		}
		fmt.Fprintf(&s, " | %s |\n", run)
	}
	return s.String()
}

func boardRuntimeSpan(b boardData) string {
	var s strings.Builder
	s.WriteString(b.RuntimeBanner + "\n\n")
	s.WriteString("| Estate | Total | Per stage |\n|---|---|---|\n")
	for _, e := range b.Estates {
		fmt.Fprintf(&s, "| %s | %s | %s |\n", e.Name, cell(e.RuntimeTotal), cell(e.RuntimeCells))
	}
	return s.String()
}

func boardLiveCertSpan(a gauntletArtifact) string {
	if len(a.LiveCert) == 0 {
		return "No live-AWS certification run is recorded yet.\n"
	}
	var s strings.Builder
	s.WriteString("| Estate | Target | Region | Clear | Commit | Date | Ceiling |\n|---|---|---|---|---|---|---|\n")
	certs := append(a.LiveCert[:0:0], a.LiveCert...)
	sort.SliceStable(certs, func(i, j int) bool { return certs[i].Estate < certs[j].Estate })
	for _, c := range certs {
		clear := "no"
		if c.Clear {
			clear = "yes"
		}
		fmt.Fprintf(&s, "| %s | %s | %s | %s | `%s` | %s | $%.2f |\n", c.Estate, c.Target, c.Region, clear, short(c.Commit), day(c.Date), c.CeilingUSD)
	}
	return s.String()
}
