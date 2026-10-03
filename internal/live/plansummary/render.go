// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plansummary

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// GitLabNoteLimit is the most characters a GitLab merge-request note may
// hold. GitLab's Notes API documents the body of every note, merge-request
// notes included, as "Limited to 1,000,000 characters"
// (https://docs.gitlab.com/api/notes/, read 2026-10-02).
const GitLabNoteLimit = 1_000_000

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (s Summary) unitWord(n int) string {
	if s.Kind == "plan" {
		return plural(n, "instance", "instances")
	}
	return plural(n, "root", "roots")
}

// Headline is the summary's first line: how many units, in how many
// groups, how many failed and how many destroys or replaces.
func (s Summary) Headline() string {
	parts := []string{plural(len(s.Groups), "group", "groups")}
	if len(s.Failed) > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", len(s.Failed)))
	}
	parts = append(parts, plural(s.Destructive, "destroy or replace", "destroys or replaces"))
	return fmt.Sprintf("%s: %s.", s.unitWord(s.Units), strings.Join(parts, ", "))
}

func (s Summary) groupTitle(g Group) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Group %d: %s", g.ID, s.unitWord(len(g.Members)))
	if g.Resource != "" {
		fmt.Fprintf(&b, " of %s", g.Resource)
	}
	if g.Outlier {
		b.WriteString(" (outlier)")
	}
	switch {
	case g.NoChanges:
		b.WriteString(", no changes")
	case g.Extends != 0:
		fmt.Fprintf(&b, ", group %d's change plus", g.Extends)
	case len(g.Members) > 1:
		b.WriteString(", identical change")
	default:
		b.WriteString(", change")
	}
	return b.String()
}

func (l ChangeLine) text() string {
	s := l.Line
	if l.Count > 1 {
		s = fmt.Sprintf("%s (x%d)", s, l.Count)
	}
	if l.DiffersFrom != 0 {
		if len(l.DiffersIn) > 0 {
			s += fmt.Sprintf("  [differs from group %d in: %s]", l.DiffersFrom, strings.Join(l.DiffersIn, ", "))
		} else {
			s += fmt.Sprintf("  [differs from group %d]", l.DiffersFrom)
		}
	}
	return s
}

func (g Group) shownLines() []ChangeLine {
	if g.Extends != 0 {
		return g.Plus
	}
	return g.Changes
}

func (d Destroy) text() string {
	if d.Unit != "" {
		return fmt.Sprintf("%s: %s (%s)", d.Unit, d.Address, d.Action)
	}
	return fmt.Sprintf("%s (%s)", d.Address, d.Action)
}

// Text renders the summary for a terminal.
func (s Summary) Text() string {
	var b strings.Builder
	b.WriteString(s.Headline())
	b.WriteString("\n")
	if len(s.Failed) > 0 {
		fmt.Fprintf(&b, "\nFailed (%d), not grouped:\n", len(s.Failed))
		for _, f := range s.Failed {
			fmt.Fprintf(&b, "  %s: %s\n", f.label(), f.Reason)
		}
	}
	for _, g := range s.Groups {
		fmt.Fprintf(&b, "\n%s:\n", s.groupTitle(g))
		for _, l := range g.shownLines() {
			fmt.Fprintf(&b, "    %s\n", l.text())
		}
		fmt.Fprintf(&b, "  %s: %s\n", s.memberWord(), strings.Join(g.Members, ", "))
		if len(g.Destroys) > 0 {
			fmt.Fprintf(&b, "  destroys and replaces (%d):\n", len(g.Destroys))
			for _, d := range g.Destroys {
				fmt.Fprintf(&b, "    %s\n", d.text())
			}
		}
	}
	return b.String()
}

func (s Summary) memberWord() string {
	if s.Kind == "plan" {
		return "instances"
	}
	return "roots"
}

func (f Failure) label() string {
	if f.Estate != "" && f.Estate != f.Root {
		return fmt.Sprintf("%s (estate %s)", f.Root, f.Estate)
	}
	return f.Root
}

// JSON renders the summary as one indented JSON document.
func (s Summary) JSON() (string, error) {
	out, err := json.MarshalIndent(s, "", "  ")
	return string(out), err
}

// TruncationNotice is the line a markdown note ends with when it leaves
// groups out. what names them.
func TruncationNotice(what string, limit int) string {
	return fmt.Sprintf("**Truncated:** this note leaves out %s, to stay within the %d-character limit of a merge-request note. `choudoufu live-summary` without `-markdown` prints all of it.", what, limit)
}

// Markdown renders the summary as a merge-request note of at most limit
// characters (runes). It keeps whole blocks, failed roots first and then
// groups in order, and cuts from the end until the rest fits: a group is
// never cut in the middle, and what is left out is named in the
// closing notice, with how many destroys and replaces it holds.
func (s Summary) Markdown(limit int) string {
	header := "### choudoufu plan summary\n\n" + s.Headline() + "\n"
	type block struct {
		text     string
		failure  bool
		units    int
		destroys int
	}
	var blocks []block
	for _, f := range s.Failed {
		blocks = append(blocks, block{
			text:    fmt.Sprintf("\n**Failed, not grouped:** `%s`: %s\n", f.label(), oneLine(f.Reason)),
			failure: true,
		})
	}
	for _, g := range s.Groups {
		var b strings.Builder
		fmt.Fprintf(&b, "\n#### %s\n\n", s.groupTitle(g))
		if lines := g.shownLines(); len(lines) > 0 {
			b.WriteString("```\n")
			for _, l := range lines {
				b.WriteString(l.text())
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
		quoted := make([]string, len(g.Members))
		for i, m := range g.Members {
			quoted[i] = "`" + m + "`"
		}
		fmt.Fprintf(&b, "%s: %s\n", strings.ToUpper(s.memberWord()[:1])+s.memberWord()[1:], strings.Join(quoted, ", "))
		if len(g.Destroys) > 0 {
			fmt.Fprintf(&b, "\nDestroys and replaces (%d):\n\n", len(g.Destroys))
			for _, d := range g.Destroys {
				fmt.Fprintf(&b, "- `%s`\n", d.text())
			}
		}
		blocks = append(blocks, block{text: b.String(), units: len(g.Members), destroys: len(g.Destroys)})
	}

	total := utf8.RuneCountInString(header)
	for _, bl := range blocks {
		total += utf8.RuneCountInString(bl.text)
	}
	if total <= limit {
		var b strings.Builder
		b.WriteString(header)
		for _, bl := range blocks {
			b.WriteString(bl.text)
		}
		return b.String()
	}

	// Keep the longest prefix of blocks that fits together with the notice
	// naming the rest. The notice's own length depends on what it names,
	// so each candidate is measured with its own notice.
	lengths := make([]int, len(blocks))
	for i, bl := range blocks {
		lengths[i] = utf8.RuneCountInString(bl.text)
	}
	notice := func(kept int) string {
		var failures, groups, units, destroys int
		for _, bl := range blocks[kept:] {
			if bl.failure {
				failures++
				continue
			}
			groups++
			units += bl.units
			destroys += bl.destroys
		}
		return "\n" + TruncationNotice(cutDescription(failures, groups, units, destroys, s), limit) + "\n"
	}
	kept := len(blocks)
	used := total
	for kept > 0 && used+utf8.RuneCountInString(notice(kept)) > limit {
		kept--
		used -= lengths[kept]
	}
	var b strings.Builder
	b.WriteString(header)
	for _, bl := range blocks[:kept] {
		b.WriteString(bl.text)
	}
	b.WriteString(notice(kept))
	return b.String()
}

func cutDescription(failures, groups, units, destroys int, s Summary) string {
	var parts []string
	if failures > 0 {
		parts = append(parts, plural(failures, "failed root", "failed roots"))
	}
	if groups > 0 {
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", plural(groups, "group", "groups"), s.unitWord(units),
			plural(destroys, "destroy or replace", "destroys or replaces")))
	}
	return strings.Join(parts, " and ")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
