// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package main

// The onboarded-form measurement.
//
// # What it answers, and why nothing answered it before
//
// choudoufu's primary goal is a fully migrated estate: someone writes
// ordinary Terraform, adds a live block, applies, and the fork manages it
// with no state file. Every instrument in this repository measured something
// else - whether a stranger's configuration could be ADOPTED exactly as
// published - because every corpus entry is somebody else's published
// configuration and not one of the 250 declares a live block, a record_store
// or the sidecar file. So the classes a record_store answers - the whole
// logical-resource class, the record-located half of markerless-type - were
// counted as language wall in every figure this project has produced, and
// they are not language wall. They are the estate not having been onboarded.
//
// -onboarded measures both forms of every entry in one sweep, with one set of
// provider schemas, one tree and one manifest. Two forms in one process
// rather than two sweeps compared afterwards is the whole design: a
// published sweep and an onboarded sweep taken separately can differ in a
// provider acquisition, a module install or an uncommitted edit, and the
// delta would carry that difference silently. Here they cannot differ in
// anything except the edit.
//
// # What it does not answer
//
// This is check.Analyze over edited text. It says nothing about whether the
// estate then applies, whether the markers land on the right objects, or
// whether a second plan is empty - live/e2e is where those claims are made
// and it is still hand-written, one estate at a time. An entry reading
// "cleared by onboarding" here has cleared the offline gate, which is step 5
// of the loop in HANDOFF.md and not step 6.

import (
	"context"
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/onboard"
	"github.com/intentius/choudoufu/internal/providers"
)

// measureOnboarded computes one entry's onboarding edit, analyzes the edited
// form, and folds the result into the entry and the run's totals.
//
// published is the report the caller already produced for the same entry, and
// schemas the schema map it was produced with. Both are reused rather than
// recomputed, which is the guarantee the design rests on: the two forms
// cannot differ in a provider acquisition, a module install, a variable file
// or a tree, because there is only one of each.
func measureOnboarded(
	ctx context.Context,
	e *entry,
	dir string,
	varFiles []string,
	schemas map[string]providers.Schema,
	published check.Report,
	causes *causeCatalog,
	r *run,
	opts sweepOptions,
) {
	plan := onboard.Compute(dir)
	e.Onboarding = &onboardingRecord{
		Status:    string(plan.Status),
		Reason:    plan.Reason,
		Estate:    plan.Estate,
		Added:     plan.Added,
		Rewritten: plan.Rewritten,
		Removed:   plan.Removed,
	}

	t := r.Totals.Onboarding
	switch plan.Status {
	case onboard.StatusUnmeasurable:
		t.Unmeasurable++
		return
	case onboard.StatusAlreadyOnboarded:
		// The published form IS the onboarded form. Reusing the report
		// rather than re-analyzing is not an optimization: re-running it
		// could differ only through nondeterminism, and a difference here
		// would be indistinguishable from an effect of the edit.
		t.Already++
		e.Onboarded = formOf(published, causes, r.Layers)
	default:
		t.Edited++
		load := check.LoadOverlay(ctx, dir, plan.Overlay, varFiles...)
		rep := check.Analyze(ctx, load.Config, check.Context{Schemas: schemas})
		rep.Load = load
		e.Onboarded = formOf(rep, causes, r.Layers)

		if opts.verbose && opts.only != "" {
			for _, f := range rep.Findings {
				for _, s := range f.Sites {
					fmt.Printf("%s\tONBOARDED\t%s:%d\t%s\t%s\n", e.Name, s.File, s.Line, f.ID, causes.site(f.Layer, s, f.ID))
				}
			}
		}
	}

	if e.Onboarded.Blocked {
		t.Blocked++
	}
	t.Sites += e.Onboarded.Sites
	t.Instances += e.Onboarded.Instances
	switch {
	case e.Blocked && !e.Onboarded.Blocked:
		t.Cleared++
	case !e.Blocked && e.Onboarded.Blocked:
		t.Worse++
	}
}

// formOf renders one analysis into a [formResult], counting exactly what the
// published columns count so the two are subtractable.
//
// layers is the run's refusal-ID-to-layer map, filled here as well as from
// the published pass: an ID that only ever appears in the onboarded form
// would otherwise print with a blank layer, and a blank layer beside a
// nonzero count reads as a bug in the tool rather than as a finding.
func formOf(rep check.Report, causes *causeCatalog, layers map[string]string) *formResult {
	f := &formResult{
		Readable:  rep.Readable(),
		Blocked:   rep.Blocked(),
		Sites:     rep.Sites(),
		Instances: rep.Instances,
		Shadowed:  rep.Shadowed,
		Refusals:  map[string]int{},
	}
	for _, x := range rep.Findings {
		f.Refusals[x.ID] += len(x.Sites)
		if layers != nil {
			layers[x.ID] = string(x.Layer)
		}
		for _, s := range x.Sites {
			if f.Causes == nil {
				f.Causes = map[string]map[string]int{}
			}
			if f.Causes[x.ID] == nil {
				f.Causes[x.ID] = map[string]int{}
			}
			f.Causes[x.ID][causes.site(x.Layer, s, x.ID)]++
		}
	}
	for _, x := range rep.Warnings {
		if f.Warnings == nil {
			f.Warnings = map[string]int{}
		}
		f.Warnings[x.ID] += len(x.Sites)
	}
	return f
}

// onboardingRecord is what internal/live/onboard concluded about one entry,
// carried into the JSON so a reader can audit the edit that produced the
// onboarded column rather than trusting it.
type onboardingRecord struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Estate string `json:"estate,omitempty"`

	Added     []string `json:"added,omitempty"`
	Rewritten []string `json:"rewritten,omitempty"`
	Removed   []string `json:"removed,omitempty"`
}

// formResult is one form's analysis of one entry. The published form's
// numbers stay on [entry] itself, exactly where every existing consumer -
// tools/estate-plan, every burndown figure, every -diff - already reads them;
// this is the second form, kept in its own object so nothing can add the two
// together by accident.
type formResult struct {
	Readable bool `json:"readable"`
	Blocked  bool `json:"blocked"`

	Sites     int            `json:"sites"`
	Refusals  map[string]int `json:"refusals"`
	Instances int            `json:"instances"`
	Shadowed  int            `json:"shadowed"`

	Warnings map[string]int            `json:"warnings,omitempty"`
	Causes   map[string]map[string]int `json:"causes,omitempty"`
}

// onboardingTotals are the run-wide onboarded-form counts. Every field is
// written even at zero: "no entry was unmeasurable" and "the field was never
// populated" must not look the same in the artifact.
type onboardingTotals struct {
	// Edited, Already and Unmeasurable partition the entries. Their sum is
	// [totals.Entries] in an -onboarded run, and a summary asserts that
	// rather than trusting it.
	Edited       int `json:"edited"`
	Already      int `json:"already_onboarded"`
	Unmeasurable int `json:"unmeasurable"`

	// Blocked, Sites and Instances are over the MEASURABLE entries only -
	// Edited plus Already. An unmeasurable entry contributes to none of
	// them, which is why Unmeasurable sits beside them in every table this
	// file prints.
	Blocked   int `json:"blocked"`
	Sites     int `json:"sites"`
	Instances int `json:"instances"`

	// Cleared counts entries blocked in published form and not blocked in
	// onboarded form. It is the headline: how much of what reads as the
	// language wall is the estate not having been onboarded.
	Cleared int `json:"cleared_by_onboarding"`

	// Worse counts entries NOT blocked in published form that ARE blocked
	// in onboarded form. It should be zero and is reported at zero on
	// purpose: an edit that refuses a configuration which used to pass is
	// the failure this whole instrument could most easily hide inside an
	// improving aggregate.
	Worse int `json:"worse_after_onboarding"`
}

// populationRow is one origin's share of the table.
type populationRow struct {
	Origin           string
	Entries          int
	Unmeasurable     int
	BlockedPublished int
	BlockedOnboarded int
	Cleared          int
	Worse            int
}

// populations builds the per-origin table. The split is not cosmetic:
// tools/estate-plan keeps published deployments and terraform-aws-modules
// examples apart because onboarding an example onboards nobody's
// infrastructure, and a single total over both answers neither question.
func populations(r *run) []populationRow {
	byOrigin := map[string]*populationRow{}
	order := []string{}
	for _, e := range r.Entries {
		row, ok := byOrigin[e.Origin]
		if !ok {
			row = &populationRow{Origin: e.Origin}
			byOrigin[e.Origin] = row
			order = append(order, e.Origin)
		}
		row.Entries++
		if e.Blocked {
			row.BlockedPublished++
		}
		if e.Onboarded == nil {
			row.Unmeasurable++
			continue
		}
		if e.Onboarded.Blocked {
			row.BlockedOnboarded++
		}
		switch {
		case e.Blocked && !e.Onboarded.Blocked:
			row.Cleared++
		case !e.Blocked && e.Onboarded.Blocked:
			row.Worse++
		}
	}
	sort.Strings(order)
	rows := make([]populationRow, 0, len(order))
	for _, o := range order {
		rows = append(rows, *byOrigin[o])
	}
	return rows
}

// summarizeOnboarding prints the onboarded-form sections. It runs only in
// -onboarded mode; a default sweep prints exactly what it always printed.
func summarizeOnboarding(r *run) {
	if !r.OnboardedForm {
		return
	}
	t := r.Totals.Onboarding

	fmt.Println()
	fmt.Println("ONBOARDED FORM - each entry re-analyzed after internal/live/onboard's edit:")
	fmt.Println("  a live sidecar declaring record_store \"local\", and the backend or cloud block removed.")
	fmt.Printf("  edit computed %d   already onboarded %d   unmeasurable %d\n", t.Edited, t.Already, t.Unmeasurable)
	if sum := t.Edited + t.Already + t.Unmeasurable; sum != r.Totals.Entries {
		// The three buckets must partition the corpus. If they do not, the
		// onboarded column is over a population nobody has named.
		fmt.Printf("  WARNING: those three account for %d entries, not %d\n", sum, r.Totals.Entries)
	}
	// The third step of the operator's edit - pinning the provider, issue
	// #269 - is not a source edit and is not in [onboard.Plan]. Say where it
	// actually is, in both directions: in a schema-backed run the probe
	// already holds the provider at one release for BOTH forms, so the step
	// is in the published column too and the two columns differ only by the
	// text edit. In a schema-less run there is no provider at all, and
	// claiming the pin is applied would be the wrong half of the sentence.
	if r.Schemas {
		fmt.Printf("  the provider pin (issue #269) is not part of the edit: both forms are measured against\n"+
			"  %s %s already, so that step is in the published column too.\n", r.PinSource, r.PinVersion)
	} else {
		fmt.Println("  NO SCHEMAS: identity.LocatedType fails closed without them, so markerless-type reads as")
		fmt.Println("  surviving onboarding when a record_store answers it. Re-run with -schemas before quoting this.")
	}

	fmt.Println()
	fmt.Printf("%-24s %8s %8s %12s %12s %9s %7s\n",
		"population", "entries", "unmeas.", "blocked pub", "blocked onb", "cleared", "worse")
	for _, row := range populations(r) {
		fmt.Printf("%-24s %8d %8d %12d %12d %9d %7d\n",
			row.Origin, row.Entries, row.Unmeasurable,
			row.BlockedPublished, row.BlockedOnboarded, row.Cleared, row.Worse)
	}

	summarizeOnboardingLadder(r)
	summarizeOnboardingClasses(r)
	summarizeOnboardingOutliers(r)
}

// rungs reads one form's [check.OnboardingClass] out of what is already
// recorded, rather than storing a second copy of it. The published side is
// computed from [entry.Refusals] and [entry.Readable] - the same two inputs
// tools/corpus-gen gives the classifier - so nothing is added to the
// published payload and no consumer of this artifact sees it change.
func rungs(readable bool, refusals map[string]int) check.OnboardingClass {
	ids := make([]string, 0, len(refusals))
	for id := range refusals {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return check.ClassifyOnboarding(readable, ids)
}

// summarizeOnboardingLadder is the verdict table, and it is the one to read
// rather than "blocked".
//
// [check.Report.Blocked] is len(Findings) > 0, which counts the data-read
// pass's eligible-read finding as a blocker. That finding is not a refusal -
// dataread.SummaryEligibleRead's own declaration says so, and
// check.ClassifyOnboarding lands an estate carrying only those on the
// data-read-eligible rung. Counting it read 118 blocked where the ladder
// reads 56 one-away, and put a class no code change removes at the top of a
// work queue. So: blocked is the number the corpus artifacts carry and is
// printed above for continuity; the ladder is what the two forms should be
// compared on.
func summarizeOnboardingLadder(r *run) {
	type cell struct{ pub, onb int }
	byOrigin := map[string]map[check.OnboardingClass]*cell{}
	origins := []string{}
	for _, e := range r.Entries {
		m, ok := byOrigin[e.Origin]
		if !ok {
			m = map[check.OnboardingClass]*cell{}
			byOrigin[e.Origin] = m
			origins = append(origins, e.Origin)
		}
		pub := rungs(e.Readable, e.Refusals)
		if m[pub] == nil {
			m[pub] = &cell{}
		}
		m[pub].pub++
		if e.Onboarded == nil {
			continue
		}
		onb := rungs(e.Onboarded.Readable, e.Onboarded.Refusals)
		if m[onb] == nil {
			m[onb] = &cell{}
		}
		m[onb].onb++
	}
	sort.Strings(origins)

	fmt.Println()
	fmt.Println("onboarding ladder (check.ClassifyOnboarding), published form -> onboarded form:")
	for _, o := range origins {
		fmt.Printf("  %s\n", o)
		for _, class := range check.OnboardingClasses() {
			c := byOrigin[o][class]
			if c == nil {
				continue
			}
			fmt.Printf("    %-20s %6d -> %-6d (%+d)\n", class, c.pub, c.onb, c.onb-c.pub)
		}
	}
}

// summarizeOnboardingClasses is the part worth more than the totals: which
// refusal classes the edit empties and which survive it. What survives is the
// real language wall - the set no configuration edit an operator makes will
// clear, and therefore the set this fork has to change code to answer.
func summarizeOnboardingClasses(r *run) {
	pubSites, onbSites := map[string]int{}, map[string]int{}
	pubCfg, onbCfg := map[string]int{}, map[string]int{}
	for _, e := range r.Entries {
		for id, n := range e.Refusals {
			pubSites[id] += n
			pubCfg[id]++
		}
		if e.Onboarded == nil {
			continue
		}
		for id, n := range e.Onboarded.Refusals {
			onbSites[id] += n
			onbCfg[id]++
		}
	}

	ids := make([]string, 0, len(pubSites))
	seen := map[string]bool{}
	for id := range pubSites {
		ids = append(ids, id)
		seen[id] = true
	}
	for id := range onbSites {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return pubSites[ids[i]] > pubSites[ids[j]] })

	fmt.Println()
	fmt.Println("refusal classes, published form -> onboarded form:")
	fmt.Printf("  %9s %9s %8s   %7s %7s   %-10s %s\n", "sites pub", "sites onb", "delta", "cfg pub", "cfg onb", "layer", "id")
	var vanished, survived []string
	for _, id := range ids {
		mark := " "
		switch {
		case pubSites[id] > 0 && onbSites[id] == 0:
			mark = "*"
			vanished = append(vanished, id)
		case onbSites[id] > 0:
			survived = append(survived, id)
		}
		fmt.Printf("%s %9d %9d %+8d   %7d %7d   %-10s %s\n",
			mark, pubSites[id], onbSites[id], onbSites[id]-pubSites[id], pubCfg[id], onbCfg[id], r.Layers[id], id)
	}
	fmt.Printf("\n  * emptied corpus-wide by onboarding (%d): %v\n", len(vanished), vanished)
	fmt.Printf("    survives somewhere (%d): %v\n", len(survived), survived)

	summarizeWallBehindTheStore(r)
}

// summarizeWallBehindTheStore answers the question the totals raise and do
// not settle: onboarding empties a class on a great many estates and frees
// almost none of them, so what is behind it.
//
// The unit is the ESTATE, because that is the unit that onboards. An estate
// clears when its LAST class clears, so a class emptied across forty estates
// that each carry two others has moved forty estates zero rungs. This ranks
// the classes still standing on the estates onboarding demonstrably helped -
// it removed at least one class from each of them - which is the shortest
// path from this measurement to a piece of work.
func summarizeWallBehindTheStore(r *run) {
	remaining := map[string]int{}
	var helped, freed int
	for _, e := range r.Entries {
		if e.Onboarded == nil {
			continue
		}
		var removedAClass bool
		for id := range e.Refusals {
			if e.Onboarded.Refusals[id] == 0 {
				removedAClass = true
				break
			}
		}
		if !removedAClass {
			continue
		}
		helped++
		if len(e.Onboarded.Refusals) == 0 {
			freed++
			continue
		}
		for id := range e.Onboarded.Refusals {
			remaining[id]++
		}
	}

	fmt.Printf("\n  onboarding removed at least one class from %d estate(s); %d of those had nothing left.\n", helped, freed)
	if len(remaining) == 0 {
		return
	}
	ids := make([]string, 0, len(remaining))
	for id := range remaining {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if remaining[ids[i]] != remaining[ids[j]] {
			return remaining[ids[i]] > remaining[ids[j]]
		}
		return ids[i] < ids[j]
	})
	fmt.Printf("  what still blocks the other %d, by how many of them carry it:\n", helped-freed)
	for _, id := range ids {
		fmt.Printf("    %4d estates  %-10s %s\n", remaining[id], r.Layers[id], id)
	}
}

// summarizeOnboardingOutliers names the entries whose verdict moved and the
// ones no honest edit could be computed for. Both are lists a reader has to
// be able to check by hand; a count of either invites the reader to believe
// it.
func summarizeOnboardingOutliers(r *run) {
	var worse, unmeasurable []string
	for _, e := range r.Entries {
		if e.Onboarding != nil && e.Onboarding.Status == string(onboard.StatusUnmeasurable) {
			unmeasurable = append(unmeasurable, fmt.Sprintf("%s (%s)", e.Name, e.Onboarding.Reason))
		}
		if e.Onboarded != nil && !e.Blocked && e.Onboarded.Blocked {
			worse = append(worse, e.Name)
		}
	}

	fmt.Println()
	if len(worse) == 0 {
		fmt.Println("entries the edit made WORSE: none")
	} else {
		fmt.Printf("entries the edit made WORSE (%d) - an onboarding edit must not refuse what passed:\n", len(worse))
		for _, n := range worse {
			fmt.Printf("  %s\n", n)
		}
	}
	if len(unmeasurable) == 0 {
		fmt.Println("entries no honest edit could be computed for: none")
		return
	}
	fmt.Printf("entries no honest edit could be computed for (%d):\n", len(unmeasurable))
	for _, n := range unmeasurable {
		fmt.Printf("  %s\n", n)
	}
}
