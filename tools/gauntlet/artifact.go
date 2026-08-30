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
	// ProtocolLiveAWS: a real-AWS certification run (issue #440), never an
	// EstateResult.Protocol value - see LiveCertResult in livecert.go for
	// why. validEstateProtocols below deliberately does NOT list this
	// constant among the values an EstateResult may carry: an EstateResult
	// with Protocol == ProtocolLiveAWS is exactly the conflation #440's
	// brief warns against (a real-AWS verdict folded into the
	// emulator-driven a.Estates/a.Sets machinery), and
	// TestArtifactAgreesWithManifest (via IsValidEstateProtocol) stays the
	// guard that catches it if it ever happens by mistake.
	ProtocolLiveAWS = "live-aws"
)

// validEstateProtocols is every Protocol value an EstateResult may
// legitimately carry. ProtocolLiveAWS is deliberately absent. A single list
// backs both TestArtifactAgreesWithManifest's check and
// TestProtocolLiveAWSNeverValidOnEstateResult (livecert_test.go), so the
// two cannot silently drift into disagreement.
var validEstateProtocols = []string{ProtocolGauntlet, ProtocolLegacy}

// IsValidEstateProtocol reports whether p is a protocol an EstateResult may
// legitimately carry - see validEstateProtocols.
func IsValidEstateProtocol(p string) bool {
	for _, v := range validEstateProtocols {
		if p == v {
			return true
		}
	}
	return false
}

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
	Schema   int    `json:"schema"`
	Emulator string `json:"emulator"`
	// Oracle mirrors Emulator exactly, for the stock terraform/tofu releases
	// hashicorp/setup-terraform and opentofu/setup-opentofu install instead
	// of the floci digest (issue #544): CONFIGURATION, not evidence - a
	// plain copy of live/oracle-versions.json on every Rebuild, the pin the
	// NEXT `gauntlet run` will use. Unlike Emulator, a row's own
	// last_run.oracle (LastRun.Oracle below) is never copied from this
	// field even at the moment a run starts: nothing forces the terraform
	// or tofu binary actually on PATH to match this pin the way FLOCI_IMAGE
	// forces the emulator to (see run.go's probeOracle) - a local checkout
	// can drift from it silently, which is the other half of #544's root
	// cause. So last_run.oracle is measured, by actually invoking whatever
	// is on PATH, never asserted from this field.
	Oracle  OracleVersions        `json:"oracle"`
	Stages  []Stage               `json:"stages"`
	Sets    map[string]SetSummary `json:"sets"`
	Estates []EstateResult        `json:"estates"`
	// BehaviorsProven and BehaviorsTotal are #522's headline metric:
	// "behaviors proven: N of 14". Computed in Rebuild from
	// live/behaviors.json, the same way every other derived field here is -
	// never stored by hand. See BehaviorsProven (behaviors.go) for exactly
	// what "proven" means. BehaviorsTotal is always len(Stages()) (14
	// today); it does not vary with the behavior index.
	BehaviorsProven int `json:"behaviors_proven"`
	BehaviorsTotal  int `json:"behaviors_total"`
	// LiveCert carries real-AWS certification results (issue #440) - one
	// entry per certified estate. It is a SEPARATE top-level slice, never a
	// row in Estates, on purpose: Rebuild (below) never reads or writes it,
	// so it can never be summed into Sets["core"]/Sets["all"] the way an
	// emulator-protocol row is. See livecert.go for the type and why the
	// separation is structural, not a convention a future change could
	// accidentally erode.
	LiveCert []LiveCertResult `json:"live_cert,omitempty"`
}

// OracleVersions is the stock terraform and tofu releases the gauntlet
// compares choudoufu's plan against for one configuration (issue #544): the
// same kind of fact the emulator digest already is, with the same power to
// invalidate a comparison - a row measured against terraform 1.15.8 and one
// measured against 1.16.0 are not directly comparable, and #498's root
// cause was exactly that, unrecorded. Terraform is the "terraform_version"
// field of `terraform version -json`; Tofu is the same field of
// `tofu version -json` (OpenTofu kept the key name for compatibility). A
// binary this tool never invoked, or could not find on PATH, leaves its
// field empty - never guessed.
type OracleVersions struct {
	Terraform string `json:"terraform,omitempty"`
	Tofu      string `json:"tofu,omitempty"`
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
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Emulator string `json:"emulator,omitempty"`
	// Oracle is the stock terraform and tofu releases this run actually
	// found on PATH (issue #544) - measured, not configured: probeOracle
	// (run.go) runs `terraform version -json` and `tofu version -json`
	// itself, once per RunEstates call, the same way commit is a real
	// `git rev-parse HEAD` rather than an assumption. A nil pointer means
	// the same two things Emulator's empty string means: a row from before
	// this field existed, or a legacy-protocol run that recorded no
	// provenance. Never treat nil as "must match the current pin" for the
	// same reason IsStale never treats an empty Emulator that way.
	Oracle   *OracleVersions   `json:"oracle,omitempty"`
	ExitCode int               `json:"exit_code"`
	Detail   map[string]string `json:"detail,omitempty"`
	// DurationS is the whole run's wall-clock seconds: measured in Go around
	// the script's process (runOne, run.go), from just before cmd.Run() to
	// just after it returns. Recorded for every protocol, gauntlet or
	// legacy, because it needs nothing from the script's own stdout - unlike
	// Seconds below, it is never zero-value-omitted-as-unknown; a run that
	// took under 0.05s (rounded away) is indistinguishable from a run that
	// recorded nothing only in the legacy-protocol case, which predates this
	// field entirely and so never sets it.
	DurationS float64 `json:"duration_s,omitempty"`
	// Seconds is per-stage wall-clock seconds, stage id -> seconds spent on
	// it this run, read from that stage's own `duration_s=` field (#434).
	// Populated only for a gauntlet-protocol run whose script sources a
	// live/e2e/lib/gauntlet.sh new enough to emit duration_s; a legacy run,
	// or a gauntlet run against an older library copy, leaves it absent
	// rather than guessing. Carried forward across runs exactly like Detail
	// already is (RunEstates, run.go): a stage this run never reached keeps
	// its previously recorded duration rather than losing it.
	Seconds map[string]float64 `json:"stage_seconds,omitempty"`
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
	return loadArtifactFile(filepath.Join(root, ArtifactPath))
}

// loadArtifactFile reads an artifact from an arbitrary path - live/gauntlet.json
// or one of its live/history/<version>.json snapshots (cmdNotes, notes.go).
// Snapshots can predate a schema change; Go's decoder already does the right
// thing for that (unknown fields are ignored, missing ones zero-value), so
// this is the one loader every schema variant goes through. See notes.go's
// package comment for which fields that leaves safe to read across versions.
func loadArtifactFile(path string) (*Artifact, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Artifact{Schema: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var a Artifact
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &a, nil
}

// Rebuild recomputes everything derived in the artifact from the manifest
// and the per-estate verdicts: the stage list, each estate's clear flag, the
// set summaries, and (from bi) the behaviors-proven headline metric.
// Verdicts for estates no longer in the manifest are dropped; estates new to
// the manifest appear with every stage not_run. It is the one place those
// rules live.
//
// bi may be nil (a caller with no behavior index in hand - most existing
// tests, and any command that only cares about the estate side): Rebuild
// still sets BehaviorsTotal from Stages() and leaves BehaviorsProven at 0,
// exactly what BehaviorsProven(nil) returns, so a nil bi never crashes and
// never fabricates evidence.
//
// It deliberately never reads or writes a.LiveCert. That field answers a
// different question (did ONE real-AWS run, on ONE date, verify what the
// emulator already agreed to) than a.Estates/a.Sets answer (does choudoufu
// match stock against the pinned emulator, re-measurable on demand) - see
// HANDOFF.md "What a measurement is worth" and livecert.go. Folding
// LiveCert into this function's loop below is exactly the conflation
// issue #440's brief calls out; the fix here is structural (a separate
// slice this function's own loop never iterates), not a flag to remember to
// check.
//
// oracle is a.Oracle's fresh value, the same "configuration for the next
// run" role emulator already has - see live/oracle-versions.json and
// OracleVersions's own doc comment for why that is a different fact than
// what a past run's last_run.oracle recorded.
func (a *Artifact) Rebuild(m *Manifest, bi *BehaviorIndex, emulator string, oracle OracleVersions) {
	prev := map[string]EstateResult{}
	for _, r := range a.Estates {
		prev[r.Name] = r
	}
	a.Schema = 1
	a.Emulator = emulator
	a.Oracle = oracle
	a.Stages = Stages()
	a.BehaviorsProven, a.BehaviorsTotal = BehaviorsProven(bi)

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

// isClear is the definition of the headline number: every headline stage
// (active and Headline: true, see HeadlineStages in stages.go) passes.
// Planned stages do not count either way, and neither does an active stage
// marked non-headline (#482) - "strict" is the current example: it can run,
// pass or fail per estate, without ever moving this.
func isClear(stages map[string]string) bool {
	return isClearAgainst(HeadlineStages(), stages)
}

// isClearAgainst is isClear's logic against an explicit headline stage list.
// Split out so a test can pin the headline-exemption behavior against a
// synthetic stage list, independent of which real stage in Stages() happens
// to be both active and non-headline today (gauntlet_test.go).
func isClearAgainst(headline []Stage, stages map[string]string) bool {
	for _, s := range headline {
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
