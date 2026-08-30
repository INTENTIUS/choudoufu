// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/command/format"
)

// GitHub issue #587: the adoption-only view.
//
// live/e2e/terralith-scale/MIGRATION.md measured a real migration and
// recorded that no adoption path appears anywhere in the plan's output for
// 7 of 55 resources, and that the two sections which do carry one
// ("Unowned", "Adoptable") are 5.6% of a 2,885-line report. During a
// migration the operator's question is "what can be adopted, what cannot,
// and why", and the plan answers it only in pieces, spread across three
// sections that are each about something else.
//
// This file renders that question and nothing else. It invents no verdict:
// every row is built from the values the ordinary sections already receive
// ([StatelessUnowned], [StatelessForeign], [StatelessOmission]) plus one
// call to markers.Taggable, the single implementation of taggability that
// live-import's own ratification report also reaches through
// (internal/live/liveimport/tags.go's taggable, which delegates to it and
// nothing else). See statelessAdoptionReport in the command package for the
// build, and live/ADOPTION.md for why the ratification report itself is not
// reachable from a plan: liveimport.Ratify requires a parsed tfstate, and a
// configuration under a live block has none.

// StatelessAdoptionClass is what this run found for one declared instance,
// on the one question this view asks. Every declared instance the projection
// attempted gets exactly one.
type StatelessAdoptionClass string

const (
	// AdoptionMarked is a live resource this estate's marker is already on.
	// Nothing to do; it is already this estate's.
	AdoptionMarked StatelessAdoptionClass = "MARKED"

	// AdoptionAdoptable is a live resource a marker write would bind, with
	// the values to write (and, where the type has one, a paste-ready
	// command) in hand.
	AdoptionAdoptable StatelessAdoptionClass = "ADOPTABLE"

	// AdoptionNoPath is the gap MIGRATION.md found: the instance needs a
	// marker to ever be found again, and this run has no live resource to
	// offer for it. Something may well be live; this run cannot say which
	// object it is, so it cannot print a command.
	AdoptionNoPath StatelessAdoptionClass = "NO_PATH"

	// AdoptionInTheWay is a live resource at the declared identity that this
	// run may not claim - another estate holds it, or this run has no estate
	// name to write.
	AdoptionInTheWay StatelessAdoptionClass = "IN_THE_WAY"

	// AdoptionAbsent means nothing live was found at the instance's
	// identity. There is nothing to adopt, which is not a problem: the plan
	// creates it.
	AdoptionAbsent StatelessAdoptionClass = "ABSENT"

	// AdoptionWaitsOnParent means the instance's identity is a formula over
	// a parent's live identity and the parent is not resolved yet. Adopting
	// the parent resolves this one for free; there is no separate action
	// here, and never a marker.
	AdoptionWaitsOnParent StatelessAdoptionClass = "WAITS_ON_PARENT"

	// AdoptionUnreadable is everything else the projection could not read -
	// a provider error, a cycle. Reported so the ledger's counts add up to
	// the declared population rather than quietly losing rows.
	AdoptionUnreadable StatelessAdoptionClass = "UNREADABLE"
)

// StatelessAdoptionRow is one declared resource instance's answer.
type StatelessAdoptionRow struct {
	// Addr is the declared instance address.
	Addr string

	// TypeName is its resource type.
	TypeName string

	// Class is what this run found. Exactly one per row.
	Class StatelessAdoptionClass

	// CanCarryMarker is whether the provider's schema for TypeName has a
	// tags argument of the shape live/MARKERS.md describes - markers.Taggable
	// over the schema this run's own provider served, the same predicate
	// live-import's UNTAGGABLE verdict is. False is not a defect and not a
	// gap: an untaggable resource is identified by its own declaration and
	// its parents' identities, so no marker is ever written onto it and
	// there is nothing to adopt.
	CanCarryMarker bool

	// LiveID is the identity of the live resource this row is about, when
	// one was found.
	LiveID string

	// DisplayName is a human-friendlier name for the same object, when the
	// type has one distinct from LiveID.
	DisplayName string

	// Matched is what a content match was made on, for an ADOPTABLE row that
	// came from the foreign classifier rather than from a declared
	// identity's own read.
	Matched []StatelessTag

	// MarkerEstate and MarkerAddress are the tofu-estate and tofu-address
	// values that adopt the resource. Set only on ADOPTABLE rows.
	MarkerEstate  string
	MarkerAddress string

	// Hint is a paste-ready command that writes both markers, when the type
	// has one. Empty for a type whose tagging call this fork does not print
	// (IAM and Route53 both have their own), which is not the same as the
	// resource being unadoptable - MarkerEstate and MarkerAddress still say
	// exactly what to write.
	Hint string

	// HeldBy is the estate that holds an IN_THE_WAY resource, empty when the
	// obstacle is that this run has no estate name of its own.
	HeldBy string

	// Detail is the sentence the underlying stage wrote about this instance,
	// carried through verbatim rather than reworded: for NO_PATH and
	// UNREADABLE rows it is the projection's own omission detail, which is
	// the only place the reason is stated.
	Detail string
}

// StatelessAdoption is the whole adoption question for one run.
type StatelessAdoption struct {
	// Estate is the estate name this run looked for markers of, empty when
	// the run could not settle one.
	Estate string

	// Rows is every declared instance the projection attempted, in address
	// order.
	Rows []StatelessAdoptionRow

	// Swept is whether a discovery sweep ran at all. Without one, nothing
	// here says whether an unmarked live resource exists to be adopted, and
	// the view says so rather than letting an empty ADOPTABLE list read as
	// "there is nothing to adopt".
	Swept bool
}

// Empty reports whether there is nothing to render.
func (a StatelessAdoption) Empty() bool { return len(a.Rows) == 0 }

// Adoption renders the adoption ledger. It is a no-op on the ordinary
// stateless plan view, which renders its sections as they arrive; this
// implementation renders this section and nothing else, which is what
// "-adoption-only" means.
func (v *StatelessAdoptionHuman) Adoption(rep StatelessAdoption) {
	cols := v.view.outputColumns()

	out := func(s string) { v.view.streams.Print(s) }
	colored := func(f string, args ...any) {
		v.view.streams.Print(v.view.colorize.Color(fmt.Sprintf(f, args...)))
	}
	wrapped := func(s string, indent int) {
		for _, line := range strings.Split(strings.TrimRight(format.WordWrap(s, cols-indent), "\n"), "\n") {
			out(strings.Repeat(" ", indent) + line + "\n")
		}
	}

	if rep.Empty() {
		colored("\n[reset][bold]Adoption: no declared resource instances[reset]\n\n")
		wrapped("This configuration declares no managed resource instances, so there is nothing to adopt.", 0)
		v.view.outputHorizRule()
		return
	}

	estate := rep.Estate
	if estate == "" {
		estate = "(none: this run has no estate name)"
	} else {
		estate = fmt.Sprintf("%q", estate)
	}

	colored("\n[reset][bold]Adoption: %d declared resource %s, estate %s[reset]\n\n",
		len(rep.Rows), noun(len(rep.Rows), "instance", "instances"), estate)
	wrapped(statelessAdoptionIntro, 0)
	out("\n")

	// The marker split first, because on a real estate the larger half is
	// the one that needs no marker, and a reader who meets it as a footnote
	// under a list of problems reads it as one. See #582 section 1.3.
	var carriers, derived []StatelessAdoptionRow
	for _, r := range rep.Rows {
		if r.CanCarryMarker {
			carriers = append(carriers, r)
		} else {
			derived = append(derived, r)
		}
	}

	colored("[bold]Identity by declaration: %d of %d %s[reset]\n\n",
		len(derived), len(rep.Rows), noun(len(rep.Rows), "instance", "instances"))
	if len(derived) == 0 {
		wrapped("Every declared instance here can carry an ownership marker.", 2)
	} else {
		wrapped(statelessAdoptionDerivedIntro, 2)
		out("\n")
		for _, line := range typeTally(derived) {
			out("  " + line + "\n")
		}
		if n := classCount(derived, AdoptionWaitsOnParent); n > 0 {
			out("\n")
			wrapped(fmt.Sprintf("%d of them cannot be resolved yet because a parent they derive from is not resolved either. Adopting that parent resolves them; there is nothing to do to these directly.", n), 2)
		}
		// Said explicitly rather than left to be inferred from a row
		// appearing in two places: a derived instance can still be listed
		// in an actionable section below, and without this line the two
		// counts read as contradicting each other.
		if n := classCount(derived, AdoptionNoPath) + classCount(derived, AdoptionInTheWay) + classCount(derived, AdoptionUnreadable); n > 0 {
			out("\n")
			wrapped(fmt.Sprintf("%d of them appear in a section below anyway. Needing no marker is not the same as being resolved: an instance whose identity this run could not build, or could not read, is listed there with the reason, and none of those reasons is a missing marker.", n), 2)
		}
	}
	out("\n")

	colored("[bold]Identity by marker: %d of %d %s[reset]\n\n",
		len(carriers), len(rep.Rows), noun(len(rep.Rows), "instance", "instances"))
	if len(carriers) == 0 {
		wrapped("No declared instance here can carry an ownership marker.", 2)
		out("\n")
	} else {
		for _, c := range adoptionCarrierOrder {
			n := classCount(carriers, c)
			if n == 0 && !adoptionAlwaysShown[c] {
				continue
			}
			out(fmt.Sprintf("  %4d  %-16s %s\n", n, adoptionClassLabel[c], adoptionClassGloss[c]))
		}
		if !rep.Swept {
			out("\n")
			wrapped("No discovery sweep ran this time, so nothing above says whether an unmarked live resource exists for any of these. An empty adoptable list here is not a report that there is nothing to adopt.", 2)
		}
	}

	v.view.outputHorizRule()

	// The actionable sections. Each is skipped when empty; a run with
	// nothing to adopt and nothing blocked prints the ledger alone.
	v.adoptionSection(rep, AdoptionAdoptable, "Adoptable now", statelessAdoptionAdoptableIntro,
		func(r StatelessAdoptionRow) {
			if len(r.Matched) > 0 {
				out("      matched on: " + tagSummary(r.Matched, 0) + "\n")
			}
			if r.Hint != "" {
				// Deliberately not word-wrapped, like every other adoption
				// hint in this package: a wrapped command is a command that
				// does not run when pasted.
				out("      adopt with: " + r.Hint + "\n")
			}
			out("      or write: tofu-estate=" + r.MarkerEstate + " tofu-address=" + r.MarkerAddress + "\n")
		})

	v.adoptionSection(rep, AdoptionNoPath, "No adoption path", statelessAdoptionNoPathIntro,
		func(r StatelessAdoptionRow) {
			if r.Detail != "" {
				wrapped(r.Detail, 6)
			}
		})

	v.adoptionSection(rep, AdoptionInTheWay, "In the way", statelessAdoptionInTheWayIntro,
		func(r StatelessAdoptionRow) {
			if r.HeldBy != "" {
				wrapped(fmt.Sprintf("held by estate %q. Moving a resource between estates is a deliberate retag by its owner, never a side effect of this estate planning.", r.HeldBy), 6)
				return
			}
			wrapped("Whether this estate owns it cannot be checked, because this run has no estate name. Pass -estate=<name>, or name the estate in the live block, and re-run.", 6)
		})

	v.adoptionSection(rep, AdoptionUnreadable, "Not read", statelessAdoptionUnreadableIntro,
		func(r StatelessAdoptionRow) {
			if r.Detail != "" {
				wrapped(r.Detail, 6)
			}
		})
}

// adoptionSection renders one class's rows under a heading, or nothing when
// the class is empty. body writes whatever goes under each row's own line.
func (v *StatelessAdoptionHuman) adoptionSection(rep StatelessAdoption, class StatelessAdoptionClass, title, intro string, body func(StatelessAdoptionRow)) {
	var rows []StatelessAdoptionRow
	for _, r := range rep.Rows {
		if r.Class == class {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return
	}

	cols := v.view.outputColumns()
	out := func(s string) { v.view.streams.Print(s) }

	v.view.streams.Print(v.view.colorize.Color(fmt.Sprintf(
		"\n[reset][bold]%s: %d resource %s[reset]\n\n", title, len(rows), noun(len(rows), "instance", "instances"))))
	for _, line := range strings.Split(strings.TrimRight(format.WordWrap(intro, cols), "\n"), "\n") {
		out(line + "\n")
	}
	out("\n")

	for _, r := range rows {
		// "<- type id" is a claim that this row is ABOUT a particular live
		// object, so a row with no live object does not make it. A NO_PATH
		// row is exactly the case where no live object is in hand, and
		// rendering it as "<- aws_iam_policy (no identity)" reads as a
		// failed read rather than as "nothing was offered".
		if r.LiveID == "" {
			v.view.streams.Print(v.view.colorize.Color(fmt.Sprintf(
				"  [bold]%s[reset] (%s)\n", r.Addr, r.TypeName)))
		} else {
			v.view.streams.Print(v.view.colorize.Color(fmt.Sprintf(
				"  [bold]%s[reset] <- %s %s%s\n", r.Addr, r.TypeName, r.LiveID, displaySuffix(r.DisplayName, r.LiveID))))
		}
		body(r)
	}

	v.view.outputHorizRule()
}

// classCount counts rows of one class.
func classCount(rows []StatelessAdoptionRow, class StatelessAdoptionClass) int {
	n := 0
	for _, r := range rows {
		if r.Class == class {
			n++
		}
	}
	return n
}

// typeTally renders "N  aws_type" lines, largest first then by name, so the
// derived population reads as a small set of ordinary families rather than
// as a long list of individually surprising resources.
func typeTally(rows []StatelessAdoptionRow) []string {
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.TypeName]++
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	out := make([]string, 0, len(names))
	for _, n := range names {
		// Count first, right-aligned, so this tally and the marker half's
		// line up down the same column. Nothing follows the name, so it is
		// not padded: a trailing run of spaces is invisible in a terminal
		// and very visible in a test's expected output.
		out = append(out, fmt.Sprintf("%4d  %s", counts[n], n))
	}
	return out
}

// adoptionCarrierOrder is the order the marker-carrying half's tally is
// printed in: what this estate already has, then what it can have, then
// what it cannot.
var adoptionCarrierOrder = []StatelessAdoptionClass{
	AdoptionMarked,
	AdoptionAdoptable,
	AdoptionNoPath,
	AdoptionInTheWay,
	AdoptionAbsent,
	AdoptionUnreadable,
	AdoptionWaitsOnParent,
}

// adoptionAlwaysShown are the tally lines printed even at zero, because a
// zero there is a result an operator wants to read: nothing left to adopt,
// nothing without a path, nothing in the way.
var adoptionAlwaysShown = map[StatelessAdoptionClass]bool{
	AdoptionMarked:    true,
	AdoptionAdoptable: true,
	AdoptionNoPath:    true,
	AdoptionInTheWay:  true,
}

var adoptionClassLabel = map[StatelessAdoptionClass]string{
	AdoptionMarked:        "already marked",
	AdoptionAdoptable:     "adoptable now",
	AdoptionNoPath:        "no path",
	AdoptionInTheWay:      "in the way",
	AdoptionAbsent:        "nothing live",
	AdoptionUnreadable:    "not read",
	AdoptionWaitsOnParent: "waits on parent",
}

var adoptionClassGloss = map[StatelessAdoptionClass]string{
	AdoptionMarked:        "this estate's markers are already on the live resource",
	AdoptionAdoptable:     "a marker write binds the live resource named below",
	AdoptionNoPath:        "needs a marker; this run found no live resource to offer",
	AdoptionInTheWay:      "a live resource holds the identity and is not this run's to claim",
	AdoptionAbsent:        "nothing live was found; an apply creates it",
	AdoptionUnreadable:    "the projection could not read it; see below",
	AdoptionWaitsOnParent: "a parent it derives from is not resolved yet",
}

const statelessAdoptionIntro = `Only this question is answered here, and no cloud object was written to answer it. Two things carry a resource's identity under live resource markers: an ownership marker written onto the live resource, or the resource's own declaration. Which one applies is a property of the resource type, not of this estate, so the two halves are counted separately below.`

const statelessAdoptionDerivedIntro = `These carry no marker and never will: the provider's schema for their type has no tags argument, so their identity is composed from their own declaration and from parents that do carry markers. Nothing is adopted here and nothing is written here. This is the association, attachment and membership family, and on a real estate it is routinely the larger half.`

const statelessAdoptionAdoptableIntro = `Each of these is a live resource this run found at a declared resource's identity, carrying no marker for this estate. Writing the two tags shown adopts it; nothing was bound, because ownership is the tofu-estate and tofu-address pair and nothing else, so claiming one is a tag write you make on purpose. Where no command is printed the type has its own tagging call this fork does not spell out - the two values are still the whole contract.`

const statelessAdoptionNoPathIntro = `The provider assigns each of these its own identity, so nothing in the declaration reconstructs it: only an ownership marker on the live resource, or a record this estate keeps, ever finds it again. This run has neither, and no live resource to offer for it. That does not mean nothing is live - it means this run cannot say which object it is, so it can print no command. Find the object yourself and write the two markers onto it, or migrate from a state file with "choudoufu live-import", which reads every identity out of the state and needs none of this.`

const statelessAdoptionInTheWayIntro = `Each of these is a live resource sitting at a declared resource's identity that this run may not claim. Nothing here is adopted and nothing here is destroyed.`

const statelessAdoptionUnreadableIntro = `Each of these could not be read at all, so this run cannot say whether it is adoptable. The sentence under each is the reason the stage that failed gave.`

// NewStatelessAdoption returns the "-adoption-only" implementation of
// [StatelessPlan]: it renders the adoption ledger and nothing else.
//
// It is a second implementation of the VIEW, never of the judgement. Every
// other method is a deliberate no-op, so the pipeline calls the same
// methods in the same order either way and the sections this mode drops are
// dropped in one place rather than by threading a flag through the run.
// Progress is the one exception, kept because a heartbeat goes to stderr and
// proves a slow sweep is still running - the same reason the ordinary view
// treats it differently.
func NewStatelessAdoption(view *View) StatelessPlan {
	return &StatelessAdoptionHuman{view: view}
}

// StatelessAdoptionHuman renders only the adoption ledger.
type StatelessAdoptionHuman struct {
	view *View
}

var _ StatelessPlan = (*StatelessAdoptionHuman)(nil)

// Progress passes through: it writes to stderr, never to the report.
func (v *StatelessAdoptionHuman) Progress(p StatelessProgress) {
	(&StatelessPlanHuman{view: v.view}).Progress(p)
}

func (v *StatelessAdoptionHuman) Omissions([]StatelessOmission)   {}
func (v *StatelessAdoptionHuman) Unowned([]StatelessUnowned)      {}
func (v *StatelessAdoptionHuman) Foreign(StatelessForeign)        {}
func (v *StatelessAdoptionHuman) Policy(StatelessPolicyReport)    {}
func (v *StatelessAdoptionHuman) GuidedFallback(string)           {}
func (v *StatelessAdoptionHuman) Lookalikes([]StatelessLookalike) {}
