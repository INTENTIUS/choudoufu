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

// Unit is one piece of work the gauntlet can hand to anyone: an estate and
// the first active stage it does not pass. `gauntlet next` computes the
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
	Remaining  int    `json:"remaining"` // active stages this estate does not yet pass
	Script     string `json:"script"`
	Proves     string `json:"proves"`
	Oracle     string `json:"oracle"`
}

// NextUnits orders the work. Core estates first (the bar that can reach
// 100%), then growing. Within a set, the estate with the fewest remaining
// active stages comes first, because finishing an estate moves the headline
// number and starting a fresh one does not; ties break by name. Within an
// estate, the first active stage in stage order that is not pass. An estate
// whose script is legacy still yields a unit: its first unit is always
// "convert the script to the protocol and re-run", which the worker brief
// says.
func NextUnits(a *Artifact, set string) []Unit {
	active := ActiveStages()
	type cand struct {
		r         EstateResult
		remaining int
	}
	var cands []cand
	for _, r := range a.Estates {
		if set == "core" && r.Set != SetCore {
			continue
		}
		if r.Clear {
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
	return units
}

// FormatUnit is the human rendering `gauntlet next` prints.
func FormatUnit(u Unit, r EstateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "unit      %s\n", u.ID)
	fmt.Fprintf(&b, "estate    %s (%s, %d active stage(s) still to pass)\n", u.Estate, u.Set, u.Remaining)
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
