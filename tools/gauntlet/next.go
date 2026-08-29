// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
	"strings"
)

// StageStalePin is the synthetic stage id NextUnits gives a clear estate
// whose last recorded run predates the current emulator pin (see the
// trailing-work block below). It is not a real stage in Stages(); FilterByTypes
// tests against it to keep --types from touching the stale-pin rule.
const StageStalePin = "stale_pin"

// Unit is one piece of work the gauntlet can hand to anyone: an estate and
// the first headline stage it does not pass. `gauntlet next` computes the
// ordered list deterministically from the artifact, so two contributors
// running it at the same time pick the same unit, and the worker's claim
// (an open pull request carrying the unit's ID) is how the second one finds
// out and takes the one after.
type Unit struct {
	ID         string `json:"id"` // <estate>/<stage>
	Estate     string `json:"estate"`
	Set        string `json:"set"`
	Stage      string `json:"stage"`
	StageTitle string `json:"stage_title"`
	Verdict    string `json:"verdict"` // fail or not_run
	Detail     string `json:"detail,omitempty"`
	Remaining  int    `json:"remaining"` // headline stages this estate does not yet pass
	Script     string `json:"script"`
	Proves     string `json:"proves"`
	Oracle     string `json:"oracle"`
}

// NextUnits orders the work. Core estates first (the bar that can reach
// 100%), then growing. Within a set, the estate with the fewest remaining
// headline stages comes first, because finishing an estate moves the
// headline number and starting a fresh one does not; ties break by name.
// Within an estate, the first headline stage in stage order that is not
// pass - a stage marked non-headline (Headline: false, e.g. "strict") never
// gates an estate's Clear flag and never surfaces here as the unit to fix
// (#482); it still runs and is reported per estate, just not through this
// selection. An estate whose script is legacy still yields a unit: its
// first unit is always "convert the script to the protocol and re-run",
// which the worker brief says.
func NextUnits(a *Artifact, set string) []Unit {
	return nextUnitsAgainst(HeadlineStages(), a, set)
}

// nextUnitsAgainst is NextUnits' logic against an explicit headline stage
// list. Split out so a test can pin the headline-exemption behavior against
// a synthetic stage list, independent of which real stage in Stages()
// happens to be both active and non-headline today (next_test.go).
func nextUnitsAgainst(headline []Stage, a *Artifact, set string) []Unit {
	active := headline
	type cand struct {
		r         EstateResult
		remaining int
	}
	var cands []cand
	var staleClear []EstateResult // clear, but last run predates the current pin - see below
	for _, r := range a.Estates {
		if set == "core" && r.Set != SetCore {
			continue
		}
		if r.Clear {
			// A clear estate has no failing or not-run headline stage, so it
			// is not ordinary work - unless the evidence backing "clear" is
			// stale: last_run.emulator names an image the current pin has
			// since superseded (or never recorded one at all, IsStale's
			// same "unknown reads as stale" rule as boardBanner). A repin
			// should ENQUEUE units, not silently invalidate the board, so
			// this is real work too, just lower priority than a genuine
			// failure - see the trailing pass below.
			if IsStale(r, a.Emulator) {
				staleClear = append(staleClear, r)
			}
			continue
		}
		n := 0
		for _, s := range active {
			if r.Stages[s.ID] != VerdictPass {
				n++
			}
		}
		cands = append(cands, cand{r, n})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		ci, cj := cands[i], cands[j]
		if (ci.r.Set == SetCore) != (cj.r.Set == SetCore) {
			return ci.r.Set == SetCore
		}
		if ci.remaining != cj.remaining {
			return ci.remaining < cj.remaining
		}
		return ci.r.Name < cj.r.Name
	})
	var units []Unit
	for _, c := range cands {
		for _, s := range active {
			v := c.r.Stages[s.ID]
			if v == VerdictPass {
				continue
			}
			detail := ""
			if c.r.LastRun != nil {
				detail = c.r.LastRun.Detail[s.ID]
			}
			units = append(units, Unit{
				ID: c.r.Name + "/" + s.ID, Estate: c.r.Name, Set: c.r.Set,
				Stage: s.ID, StageTitle: s.Title, Verdict: v, Detail: detail,
				Remaining: c.remaining, Script: c.r.Script, Proves: s.Proves, Oracle: s.Oracle,
			})
			break // one unit per estate: the first stage that is not pass
		}
	}

	// Stale-but-clear units trail every genuine failure or not-run stage,
	// deliberately: an estate already known broken outranks one merely
	// unconfirmed against the current pin. This is the conservative half of
	// #414's next layer down - it makes a repin visible as work rather than
	// silent, without redesigning how the two kinds interleave, which is a
	// bigger ranking question left as a proposal (see the issue this branch
	// cites).
	sort.SliceStable(staleClear, func(i, j int) bool {
		if (staleClear[i].Set == SetCore) != (staleClear[j].Set == SetCore) {
			return staleClear[i].Set == SetCore
		}
		return staleClear[i].Name < staleClear[j].Name
	})
	for _, r := range staleClear {
		emu := "unrecorded"
		if r.LastRun != nil && r.LastRun.Emulator != "" {
			emu = r.LastRun.Emulator
		}
		units = append(units, Unit{
			ID: r.Name + "/" + StageStalePin, Estate: r.Name, Set: r.Set,
			Stage: StageStalePin, StageTitle: "Re-verify against the current emulator pin",
			Verdict:   "stale_evidence",
			Detail:    fmt.Sprintf("every headline stage passed, but last verified against %s; the current pin is %s", emu, a.Emulator),
			Remaining: 0, Script: r.Script,
			Proves: "the estate still behaves like stock against the CURRENT emulator pin, not a superseded one",
			Oracle: "re-run against the pinned image and confirm the same verdicts",
		})
	}
	return units
}

// FormatUnit is the human rendering `gauntlet next` prints.
func FormatUnit(u Unit, r EstateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "unit      %s\n", u.ID)
	fmt.Fprintf(&b, "estate    %s (%s, %d headline stage(s) still to pass)\n", u.Estate, u.Set, u.Remaining)
	fmt.Fprintf(&b, "stage     %s: %s\n", u.Stage, u.StageTitle)
	fmt.Fprintf(&b, "verdict   %s\n", u.Verdict)
	if u.Detail != "" {
		fmt.Fprintf(&b, "detail    %s\n", u.Detail)
	}
	fmt.Fprintf(&b, "protocol  %s\n", r.Protocol)
	fmt.Fprintf(&b, "script    %s\n", u.Script)
	fmt.Fprintf(&b, "proves    %s\n", u.Proves)
	fmt.Fprintf(&b, "oracle    %s\n", u.Oracle)
	fmt.Fprintf(&b, "run       go run ./tools/gauntlet run %s\n", u.Estate)
	fmt.Fprintf(&b, "branch    gauntlet/%s-%s\n", u.Estate, u.Stage)
	return b.String()
}
