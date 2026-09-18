// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// probe scaffolding for #1308: build real boards from synthetic artifacts,
// merge them textually the way git does, and report what comes out.

func probeEstates() []Estate {
	var es []Estate
	for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"} {
		es = append(es, Estate{Name: n, Source: "s", Lane: "reference", Set: SetCore, Reason: "r"})
	}
	return es
}

func probeArtifact(t *testing.T, m *Manifest, rows map[string]*EstateResult) *Artifact {
	t.Helper()
	a := &Artifact{Emulator: "sha256:probe"}
	for _, e := range probeEstates() {
		r, ok := rows[e.Name]
		if !ok {
			r = &EstateResult{Name: e.Name, Stages: stagesAllNotRun()}
		}
		a.Estates = append(a.Estates, *r)
	}
	a.Rebuild(m, nil, "sha256:probe", OracleVersions{})
	return a
}

func probeRow(name, date string, pass bool) *EstateResult {
	st := stagesAllNotRun()
	if pass {
		st = stagesAllPass()
	}
	return &EstateResult{
		Name: name, Stages: st,
		LastRun: &LastRun{Commit: "0000000000000000000000000000000000000000", Date: date, Emulator: "sha256:probe"},
	}
}

func mergeFile(t *testing.T, ours, base, theirs []byte) (out []byte, conflicts bool) {
	t.Helper()
	dir := t.TempDir()
	w := func(n string, b []byte) string {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cmd := exec.Command("git", "merge-file", "-p", w("ours", ours), w("base", base), w("theirs", theirs))
	o, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() > 0 {
			return o, true
		}
		t.Fatal(err)
	}
	return o, false
}

func TestProbeBoardTextualMerge(t *testing.T) {
	m := &Manifest{Estates: probeEstates()}
	clean := map[string]ScriptStaleness{}
	for _, e := range probeEstates() {
		clean[e.Name] = ScriptStaleness{State: ScriptCurrent}
	}
	withChanged := func(names ...string) map[string]ScriptStaleness {
		out := map[string]ScriptStaleness{}
		for k, v := range clean {
			out[k] = v
		}
		for _, n := range names {
			out[n] = ScriptStaleness{State: ScriptChanged, Changed: []string{"live/e2e/estates/" + n + "/run.sh"}}
		}
		return out
	}

	baseRows := map[string]*EstateResult{}
	for _, e := range probeEstates() {
		baseRows[e.Name] = probeRow(e.Name, "2026-09-01T00:00:00Z", false)
	}
	cp := func(extra map[string]*EstateResult) map[string]*EstateResult {
		out := map[string]*EstateResult{}
		for k, v := range baseRows {
			c := *v
			out[k] = &c
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	type scenario struct {
		name              string
		oursRows          map[string]*EstateResult
		theirsRows        map[string]*EstateResult
		baseSt, oursSt    map[string]ScriptStaleness
		theirsSt, truthSt map[string]ScriptStaleness
	}
	scenarios := []scenario{
		{
			name:       "two distinct rows re-run",
			oursRows:   cp(map[string]*EstateResult{"alpha": probeRow("alpha", "2026-09-10T00:00:00Z", true)}),
			theirsRows: cp(map[string]*EstateResult{"foxtrot": probeRow("foxtrot", "2026-09-10T00:00:00Z", true)}),
			baseSt:     clean, oursSt: clean, theirsSt: clean, truthSt: clean,
		},
		{
			name:       "one row re-run, one row's script edited",
			oursRows:   cp(map[string]*EstateResult{"alpha": probeRow("alpha", "2026-09-10T00:00:00Z", true)}),
			theirsRows: cp(nil),
			baseSt:     clean, oursSt: clean, theirsSt: withChanged("foxtrot"), truthSt: withChanged("foxtrot"),
		},
		{
			name:       "two distinct scripts edited",
			oursRows:   cp(nil),
			theirsRows: cp(nil),
			baseSt:     clean, oursSt: withChanged("alpha"), theirsSt: withChanged("foxtrot"), truthSt: withChanged("alpha", "foxtrot"),
		},
		{
			name:       "same script edited on both sides",
			oursRows:   cp(nil),
			theirsRows: cp(nil),
			baseSt:     clean, oursSt: withChanged("charlie"), theirsSt: withChanged("charlie"), truthSt: withChanged("charlie"),
		},
		{
			name:       "our row re-run, their script edited on a DIFFERENT row, banner already lit on both",
			oursRows:   cp(map[string]*EstateResult{"alpha": probeRow("alpha", "2026-09-10T00:00:00Z", true)}),
			theirsRows: cp(nil),
			baseSt:     withChanged("charlie"), oursSt: withChanged("charlie"), theirsSt: withChanged("charlie", "foxtrot"), truthSt: withChanged("charlie", "foxtrot"),
		},
	}

	for _, s := range scenarios {
		baseA := probeArtifact(t, m, baseRows)
		oursA := probeArtifact(t, m, s.oursRows)
		theirsA := probeArtifact(t, m, s.theirsRows)
		// truth: the merged artifact, which merge-artifact would produce.
		truthRows := map[string]*EstateResult{}
		for k, v := range s.oursRows {
			truthRows[k] = v
		}
		for k, v := range s.theirsRows {
			if v.LastRun != nil && baseRows[k].LastRun != nil && v.LastRun.Date != baseRows[k].LastRun.Date {
				truthRows[k] = v
			}
		}
		truthA := probeArtifact(t, m, truthRows)

		bb := mustCanon(t, buildBoard(m, baseA, s.baseSt))
		ob := mustCanon(t, buildBoard(m, oursA, s.oursSt))
		tb := mustCanon(t, buildBoard(m, theirsA, s.theirsSt))
		truth := mustCanon(t, buildBoard(m, truthA, s.truthSt))

		merged, conflicts := mergeFile(t, ob, bb, tb)
		verdict := "EQUALS TRUTH"
		if string(merged) != string(truth) {
			if boardsDifferOnlyInScriptStaleness(truth, merged) {
				verdict = "WRONG, advisory-only -> PASSES the render-currency guard"
			} else {
				verdict = "WRONG, blocking -> caught by the render-currency guard"
			}
		}
		fmt.Printf("scenario %-70s conflicts=%-5v %s\n", s.name, conflicts, verdict)
	}
}

func mustCanon(t *testing.T, b Board) []byte {
	t.Helper()
	c, err := b.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestProbeRealBoardTextualMerge: the same experiment against the real
// 31-row board, where identical repeated lines across rows give git's
// line-based merge the most room to mis-align.
func TestProbeRealBoardTextualMerge(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	st := AllScriptStaleness(root, a)
	base := mustCanon(t, buildBoard(m, a, st))

	// Pick two rows far apart in the sorted board order.
	names := []string{}
	for _, r := range a.Estates {
		names = append(names, r.Name)
	}
	if len(names) < 4 {
		t.Skip("not enough rows")
	}
	touch := func(name string, date string, pass bool) *Artifact {
		c := *a
		c.Estates = append([]EstateResult(nil), a.Estates...)
		for i := range c.Estates {
			if c.Estates[i].Name != name {
				continue
			}
			r := c.Estates[i]
			stg := map[string]string{}
			for k, v := range r.Stages {
				stg[k] = v
			}
			for _, s := range HeadlineStages() {
				if pass {
					stg[s.ID] = VerdictPass
				} else {
					stg[s.ID] = VerdictFail
				}
			}
			r.Stages = stg
			lr := *r.LastRun
			lr.Date = date
			r.LastRun = &lr
			c.Estates[i] = r
		}
		c.Rebuild(m, nil, a.Emulator, a.Oracle)
		return &c
	}

	for _, pair := range [][2]string{{names[0], names[len(names)-1]}, {names[1], names[2]}} {
		oursA := touch(pair[0], "2026-09-17T00:00:00Z", true)
		theirsA := touch(pair[1], "2026-09-17T00:00:00Z", false)
		// truth: both rows moved.
		bothA := touch(pair[0], "2026-09-17T00:00:00Z", true)
		{
			c := touch(pair[1], "2026-09-17T00:00:00Z", false)
			for i := range bothA.Estates {
				if bothA.Estates[i].Name == pair[1] {
					for j := range c.Estates {
						if c.Estates[j].Name == pair[1] {
							bothA.Estates[i] = c.Estates[j]
						}
					}
				}
			}
			bothA.Rebuild(m, nil, a.Emulator, a.Oracle)
		}
		ob := mustCanon(t, buildBoard(m, oursA, st))
		tb := mustCanon(t, buildBoard(m, theirsA, st))
		truth := mustCanon(t, buildBoard(m, bothA, st))
		merged, conflicts := mergeFile(t, ob, base, tb)
		verdict := "EQUALS TRUTH"
		if string(merged) != string(truth) {
			if boardsDifferOnlyInScriptStaleness(truth, merged) {
				verdict = "WRONG, advisory-only -> PASSES the render-currency guard"
			} else {
				verdict = "WRONG, blocking -> caught by the render-currency guard"
			}
		}
		fmt.Printf("real board %s + %s: conflicts=%v %s\n", pair[0], pair[1], conflicts, verdict)
	}
}

// TestProbeRealBoardConflict: two branches that each re-run one estate on a
// DIFFERENT day - the six conflicts of 2026-09-18.
func TestProbeRealBoardConflict(t *testing.T) {
	root := testRoot(t)
	m, _ := LoadManifest(root)
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	st := AllScriptStaleness(root, a)
	base := mustCanon(t, buildBoard(m, a, st))
	touch := func(name, date string) *Artifact {
		c := *a
		c.Estates = append([]EstateResult(nil), a.Estates...)
		for i := range c.Estates {
			if c.Estates[i].Name == name && c.Estates[i].LastRun != nil {
				r := c.Estates[i]
				lr := *r.LastRun
				lr.Date = date
				r.LastRun = &lr
				c.Estates[i] = r
			}
		}
		c.Rebuild(m, nil, a.Emulator, a.Oracle)
		return &c
	}
	n0, n1 := a.Estates[0].Name, a.Estates[len(a.Estates)-1].Name
	ob := mustCanon(t, buildBoard(m, touch(n0, "2026-09-17T10:00:00Z"), st))
	tb := mustCanon(t, buildBoard(m, touch(n1, "2026-09-18T10:00:00Z"), st))
	merged, conflicts := mergeFile(t, ob, base, tb)
	n := 0
	for _, line := range splitLines(string(merged)) {
		if len(line) > 7 && line[:7] == "<<<<<<<" {
			n++
		}
	}
	fmt.Printf("real board, %s re-run on the 17th vs %s on the 18th: conflicts=%v hunks=%d\n", n0, n1, conflicts, n)
	for _, line := range splitLines(string(merged)) {
		if len(line) > 7 && (line[:7] == "<<<<<<<" || line[:7] == ">>>>>>>" || line[:7] == "=======") {
			continue
		}
	}
	// show the conflicted region
	ls := splitLines(string(merged))
	for i, line := range ls {
		if len(line) > 7 && line[:7] == "<<<<<<<" {
			for j := i; j < len(ls) && j < i+8; j++ {
				s := ls[j]
				if len(s) > 160 {
					s = s[:160] + "..."
				}
				fmt.Println("   ", s)
			}
			break
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
