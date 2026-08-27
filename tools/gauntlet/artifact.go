// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ArtifactPath is the gauntlet's committed result, relative to the repo root.
// live/history/<version>.json is a copy of it taken at release.
const ArtifactPath = "live/gauntlet.json"

// SiteDataPath is where the renderer copies the artifact for Hugo.
const SiteDataPath = "site/data/gauntlet.json"

// Verdicts a stage can carry for one estate.
const (
	VerdictPass   = "pass"
	VerdictFail   = "fail"
	VerdictNotRun = "not_run"
)

// Protocols: how an estate's verdicts were obtained.
const (
	// ProtocolGauntlet: the script emitted GAUNTLET stage lines
	// (live/e2e/lib/gauntlet.sh) and the runner recorded them.
	ProtocolGauntlet = "gauntlet"
	// ProtocolLegacy: verdicts imported once from
	// live/corpus-crossing-manifest.json, which was hand-recorded from each
	// crossing's verified output. The runner never overwrites a legacy entry
	// unless the script now speaks the protocol.
	ProtocolLegacy = "legacy"
)

// Artifact is live/gauntlet.json.
//
// It used to also carry a top-level Commit and Generated: a single
// "measured the whole board at commit X, at time Y" stamp. No procedure
// produces that fact honestly - `gauntlet run <estate>` runs one estate, not
// the board, and `gauntlet render` deliberately never advances either field
// (see #414) - so it is gone rather than fixed to lie less. Emulator stays,
// but read it for what it is: CONFIGURATION, not evidence. It is a plain
// copy of live/floci-image, true of the checked-out tree on every Rebuild
// regardless of what has or hasn't been run - the pin the NEXT `gauntlet
// run` will use. It says nothing about what any past run actually used.
//
// The evidence half lives one level down, per estate: `last_run.commit`
// and `last_run.date` (#413), and `last_run.emulator` (this field's own
// former mistake, one layer under #414 - the board banner used to borrow
// this top-level Emulator to describe every row's evidence, which is true
// for exactly one instant, when a full sweep finishes, and false after any
// incremental re-run changes this field while old rows sit unrun). Each
// estate's own `last_run.emulator` is stamped by RunEstates at the moment
// that estate's script actually launched (run.go), from the same pin this
// field holds at that instant - so a row's recorded emulator is what that
// run really used, never copied from configuration at render time. A page-
// level claim derived from those rows is computed fresh at render time
// (boardBanner, render.go), never stored here, so it cannot go stale
// independently of the rows it summarizes, and it must render disagreement
// honestly rather than pick one row's digest and assert it of the board.
type Artifact struct {
	Schema   int                   `json:"schema"`
	Emulator string                `json:"emulator"`
	Stages   []Stage               `json:"stages"`
	Sets     map[string]SetSummary `json:"sets"`
	Estates  []EstateResult        `json:"estates"`
}

// SetSummary is one headline bar.
type SetSummary struct {
	Label   string           `json:"label"`
	Estates int              `json:"estates"`
	Clear   int              `json:"clear"`
	Stages  map[string]Tally `json:"stages"`
}

// Tally counts verdicts for one stage over one set.
type Tally struct {
	Pass   int `json:"pass"`
	Fail   int `json:"fail"`
	NotRun int `json:"not_run"`
}

// EstateResult is one estate's row.
type EstateResult struct {
	Name     string            `json:"name"`
	Source   string            `json:"source"`
	URL      string            `json:"url,omitempty"`
	Pin      string            `json:"pin,omitempty"`
	Lane     string            `json:"lane"`
	Set      string            `json:"set"`
	Reason   string            `json:"reason,omitempty"`
	Script   string            `json:"script"`
	Stages   map[string]string `json:"stages"`
	Clear    bool              `json:"clear"`
	Protocol string            `json:"protocol"`
	LastRun  *LastRun          `json:"last_run,omitempty"`
	Notes    string            `json:"notes,omitempty"`
}

// LastRun records the run that produced the verdicts.
//
// Emulator is the digest that run actually launched against - written by
// RunEstates (run.go) at run time from the same live/floci-image read the
// script itself reads, never copied from the artifact's top-level Emulator
// at render time. Empty means one of two things: a row from before this
// field existed (backfilled from git history where the exact historical
// pin was recoverable - see the emulatorBackfill comment in artifact.go -
// and left empty where it was not), or a legacy-protocol run that never
// recorded provenance at all. Either way, empty is never treated as "must
// match the current pin" - IsStale treats it as stale precisely because it
// cannot be shown to match.
type LastRun struct {
	Commit   string            `json:"commit"`
	Date     string            `json:"date"`
	Emulator string            `json:"emulator,omitempty"`
	ExitCode int               `json:"exit_code"`
	Detail   map[string]string `json:"detail,omitempty"`
}

// IsStale reports whether r's last recorded run measured against a
// different emulator image than the one currently pinned. A row with no
// last_run is not "stale" by this definition - it has never run at all,
// which callers should check for separately (r.LastRun == nil) since it is
// a different fact than "ran, but against a superseded or unrecorded
// image". An empty r.LastRun.Emulator (unrecorded provenance) always
// compares unequal to a real digest, so it reads as stale here too - the
// artifact must never claim a measurement was made against an image it
// cannot show it was made against (see the backfill comment on Emulator).
func IsStale(r EstateResult, currentEmulator string) bool {
	return r.LastRun != nil && r.LastRun.Emulator != currentEmulator
}

// SetLabels name the two headline bars. "all" is every estate; "core" is the
// pinned population. The keys are what the Hugo shortcode reads.
var SetLabels = map[string]string{
	"core": "Core estates",
	"all":  "All estates",
}

// LoadArtifact reads the committed artifact. A missing file is an empty
// artifact, so the first run starts from nothing rather than erroring.
func LoadArtifact(root string) (*Artifact, error) {
	b, err := os.ReadFile(filepath.Join(root, ArtifactPath))
	if os.IsNotExist(err) {
		return &Artifact{Schema: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var a Artifact
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", ArtifactPath, err)
	}
	return &a, nil
}

// Rebuild recomputes everything derived in the artifact from the manifest
// and the per-estate verdicts: the stage list, each estate's clear flag, the
// set summaries. Verdicts for estates no longer in the manifest are dropped;
// estates new to the manifest appear with every stage not_run. It is the one
// place those rules live.
func (a *Artifact) Rebuild(m *Manifest, emulator string) {
	prev := map[string]EstateResult{}
	for _, r := range a.Estates {
		prev[r.Name] = r
	}
	a.Schema = 1
	a.Emulator = emulator
	a.Stages = Stages()

	var rows []EstateResult
	for _, e := range m.Estates {
		r, ok := prev[e.Name]
		if !ok {
			r = EstateResult{Protocol: ProtocolLegacy}
		}
		r.Name, r.Source, r.URL, r.Pin = e.Name, e.Source, e.URL, e.Pin
		r.Lane, r.Set, r.Reason, r.Script = e.Lane, e.Set, e.Reason, e.ScriptPath()
		if r.Stages == nil {
			r.Stages = map[string]string{}
		}
		for _, s := range Stages() {
			if _, ok := r.Stages[s.ID]; !ok {
				r.Stages[s.ID] = VerdictNotRun
			}
		}
		// Drop verdicts for stages that no longer exist.
		for id := range r.Stages {
			if _, ok := StageByID(id); !ok {
				delete(r.Stages, id)
			}
		}
		r.Clear = isClear(r.Stages)
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	a.Estates = rows

	a.Sets = map[string]SetSummary{}
	for key, label := range SetLabels {
		sum := SetSummary{Label: label, Stages: map[string]Tally{}}
		for _, r := range rows {
			if key == "core" && r.Set != SetCore {
				continue
			}
			sum.Estates++
			if r.Clear {
				sum.Clear++
			}
			for _, s := range Stages() {
				t := sum.Stages[s.ID]
				switch r.Stages[s.ID] {
				case VerdictPass:
					t.Pass++
				case VerdictFail:
					t.Fail++
				default:
					t.NotRun++
				}
				sum.Stages[s.ID] = t
			}
		}
		a.Sets[key] = sum
	}
}

// isClear is the definition of the headline number: every active stage
// passes. Planned stages do not count either way.
func isClear(stages map[string]string) bool {
	for _, s := range ActiveStages() {
		if stages[s.ID] != VerdictPass {
			return false
		}
	}
	return true
}

// Canonical encodes the artifact the one way the tool writes it.
func (a *Artifact) Canonical() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SaveArtifact writes the artifact to live/gauntlet.json.
func SaveArtifact(root string, a *Artifact) error {
	b, err := a.Canonical()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ArtifactPath), b, 0o644)
}

// Result returns the row for an estate.
func (a *Artifact) Result(name string) (EstateResult, bool) {
	for _, r := range a.Estates {
		if r.Name == name {
			return r, true
		}
	}
	return EstateResult{}, false
}

// SetResult replaces or appends an estate's row.
func (a *Artifact) SetResult(r EstateResult) {
	for i := range a.Estates {
		if a.Estates[i].Name == r.Name {
			a.Estates[i] = r
			return
		}
	}
	a.Estates = append(a.Estates, r)
}
