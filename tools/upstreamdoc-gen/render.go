// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/intentius/choudoufu/internal/live/mdspan"
	"github.com/intentius/choudoufu/internal/live/newruntime"
)

const (
	// pageRel is the page this tool writes into.
	pageRel = "site/src/content/docs/how-it-works/upstream.md"
	// forkSurfaceRel is tools/forkdiff-gen's committed artifact.
	forkSurfaceRel = "live/fork-surface.json"

	spanBase = "base"
	spanGaps = "gaps"

	upstreamRepo = "https://github.com/opentofu/opentofu"
	forkRepo     = "https://github.com/INTENTIUS/choudoufu"
)

var markers = mdspan.For("upstreamdoc-gen")

// forkSurface is the part of live/fork-surface.json the base span reads.
type forkSurface struct {
	ForkPoint           string `json:"fork_point"`
	ForkPointShort      string `json:"fork_point_short"`
	BaseOpenTofuVersion string `json:"base_opentofu_version"`
}

func loadForkSurface(root string) (forkSurface, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(forkSurfaceRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return forkSurface{}, fmt.Errorf("reading %s: %w", forkSurfaceRel, err)
	}
	var fs forkSurface
	if err := json.Unmarshal(data, &fs); err != nil {
		return forkSurface{}, fmt.Errorf("decoding %s: %w", forkSurfaceRel, err)
	}
	if fs.ForkPoint == "" || fs.ForkPointShort == "" || fs.BaseOpenTofuVersion == "" {
		return forkSurface{}, fmt.Errorf("%s has no fork_point, fork_point_short or base_opentofu_version", forkSurfaceRel)
	}
	return fs, nil
}

// renderPage returns doc with both spans rendered from the committed sources.
func renderPage(root, doc string) (string, error) {
	fs, err := loadForkSurface(root)
	if err != nil {
		return "", err
	}
	out, err := markers.Replace(pageRel, doc, spanBase, renderBase(fs))
	if err != nil {
		return "", err
	}
	return markers.Replace(pageRel, out, spanGaps, renderGaps(newruntime.Rows()))
}

// renderBase is the base span: the upstream commit and the version it reports.
func renderBase(fs forkSurface) string {
	return fmt.Sprintf("The base is upstream OpenTofu commit [`%s`](%s/commit/%s), whose `version/VERSION` reads `%s`. `choudoufu version` prints that version beside the fork's own tag.\n",
		fs.ForkPointShort, upstreamRepo, fs.ForkPoint, fs.BaseOpenTofuVersion)
}

// isRefusal reports whether a check kind stops the run.
func isRefusal(check string) bool {
	switch check {
	case newruntime.CheckLive, newruntime.CheckPreflight, newruntime.CheckPostPlan, newruntime.CheckEngine:
		return true
	}
	return false
}

// where says where a refusal row stops the run, from the check kinds the
// gap table's own comment defines.
func where(r newruntime.Row) string {
	switch r.Check {
	case newruntime.CheckLive:
		return "in live mode"
	case newruntime.CheckPreflight:
		return "before planning, on the configuration or state"
	case newruntime.CheckEngine:
		return "inside planning"
	case newruntime.CheckPostPlan:
		s := "on the plan, before it is shown or saved"
		switch r.Scope {
		case newruntime.ScopeLive:
			s += ", in live mode only"
		case newruntime.ScopeNonLive:
			s += ", outside live mode only"
		}
		return s
	}
	return r.Check
}

var issueRef = regexp.MustCompile(`#(\d{3,5})\b`)

// linkify turns each #NNNN in a row's text into a link: to opentofu/opentofu
// when the row's upstream list names it, to choudoufu otherwise. That is the
// table's own convention (gaps.json's comment).
func linkify(r newruntime.Row, s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return issueRef.ReplaceAllStringFunc(s, func(m string) string {
		if slices.Contains(r.Upstream, m) {
			return fmt.Sprintf("[%s](%s/issues/%s)", m, upstreamRepo, m[1:])
		}
		return fmt.Sprintf("[%s](%s/issues/%s)", m, forkRepo, m[1:])
	})
}

// waitsFor is the closing column: the row's upstream issues as links, after
// its closed_by text when that says more than "upstream".
func waitsFor(r newruntime.Row) string {
	var links []string
	for _, u := range r.Upstream {
		if !strings.Contains(r.ClosedBy, u) {
			links = append(links, linkify(r, u))
		}
	}
	text := linkify(r, r.ClosedBy)
	if len(links) == 0 {
		return text
	}
	if r.ClosedBy == "upstream" {
		return "upstream " + strings.Join(links, ", ")
	}
	return text + "; upstream " + strings.Join(links, ", ")
}

func cell(r newruntime.Row, s string) string {
	if s == "" {
		return "none"
	}
	return linkify(r, s)
}

// renderGaps is the gaps span: three tables from the gap table, in file
// order. Pure, so render_test.go can compare without writing.
func renderGaps(rows []newruntime.Row) string {
	var b strings.Builder

	b.WriteString("### Refused\n\n")
	b.WriteString("A run that reaches one of these stops with `" + newruntime.RefusalSummary + "`, naming the row and what it waits for.\n\n")
	b.WriteString("| Row | Gap | Where it stops | What it refuses | Waits for |\n|---|---|---|---|---|\n")
	for _, r := range rows {
		if !isRefusal(r.Check) {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", r.ID, linkify(r, r.Gap), where(r), cell(r, r.Interim), waitsFor(r))
	}

	b.WriteString("\n### Accepted, and named in the warning\n\n")
	b.WriteString("Every run on the new runtime prints `" + newruntime.WarningSummary + "` with these lines.\n\n")
	b.WriteString("| Row | Warning line | Waits for |\n|---|---|---|\n")
	for _, r := range rows {
		if r.Check == newruntime.CheckAccepted {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", r.ID, linkify(r, r.Warning), waitsFor(r))
		}
	}
	for _, r := range rows {
		if r.Check != newruntime.CheckAccepted && r.Warning != "" {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", r.ID, linkify(r, r.Warning), waitsFor(r))
		}
	}

	b.WriteString("\n### Runs, with no refusal\n\n")
	b.WriteString("| Row | Gap | How | Closed by |\n|---|---|---|---|\n")
	for _, r := range rows {
		if r.Check == newruntime.CheckNone {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", r.ID, linkify(r, r.Gap), cell(r, r.Interim), waitsFor(r))
		}
	}
	return b.String()
}
