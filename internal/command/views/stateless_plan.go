// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"strings"

	"github.com/opentofu/opentofu/internal/command/format"
)

// StatelessOmission is one resource instance that the stateless projection
// could not read from the live system, in a form this package can render
// without importing the projection builder.
//
// The three fields correspond to projection.Omission's Addr, Reason and
// Detail: the address that was not read, a stable machine-readable reason
// code, and a sentence for an operator.
type StatelessOmission struct {
	Addr   string
	Reason string
	Detail string
}

// StatelessForeign is the foreign classification of one discovery pass, in a
// form this package can render without importing the classifier.
//
// The fields correspond to foreign.Result: the live resources nobody claims,
// the ones that exactly match a declared instance and are offered for
// adoption, the counts belonging to other estates, and - the part that must
// never be dropped for being boring - which resource types the sweep can
// speak for at all.
type StatelessForeign struct {
	// Estate is the estate the classification was drawn around.
	Estate string

	// Items are the foreign resources: report only, never deletion
	// candidates.
	Items []StatelessForeignItem

	// Candidates are the adoptable ones, each naming the declared address it
	// matches and the command that would claim it.
	Candidates []StatelessBindCandidate

	// Removals are the live resources this estate owns at addresses the
	// configuration no longer declares, which the plan below proposes
	// destroying. Unlike everything else in this report they are not
	// report-only: each one is in the prior state the plan ran against.
	Removals []StatelessRemoval

	// SweepGaps are the resource types the removal sweep could not
	// enumerate, and SweepCovered the ones it did. An empty Removals list
	// means "nothing undeclared was found among SweepCovered" and nothing
	// more.
	SweepGaps    []StatelessSweepGap
	SweepCovered []string

	// Renames are the live resources this estate owns whose marker names a
	// for_each key the configuration no longer declares, paired with the
	// declared instance they are probably the same resource as.
	Renames []StatelessRename

	// AmbiguousRenames are the resource blocks where such a pairing exists
	// but is not one-to-one, so no rename is offered.
	AmbiguousRenames []StatelessRenameAmbiguity

	// OtherEstates are the per-estate counts. An empty Estate means the
	// resources were counted without their estate being recorded.
	OtherEstates []StatelessEstateCount

	// Swept are the resource types that were listed in full.
	Swept []string

	// Unswept are the types this classification cannot speak for, with a
	// reason code and a sentence each.
	Unswept []StatelessUnsweptType
}

// StatelessForeignItem is one live resource nobody claims.
type StatelessForeignItem struct {
	TypeName    string
	LiveID      string
	DisplayName string
	Tags        []StatelessTag
	Why         string
}

// StatelessBindCandidate is one live resource offered for adoption.
type StatelessBindCandidate struct {
	Addr        string
	TypeName    string
	LiveID      string
	DisplayName string
	Tags        []StatelessTag

	// Matched are the identity-bearing arguments the live resource and the
	// declared instance agreed on exactly.
	Matched []StatelessTag

	// MarkerEstate and MarkerAddress are the tofu-estate and tofu-address
	// values that would adopt the resource.
	MarkerEstate  string
	MarkerAddress string

	// Hint is a one-line command that writes those two tags, empty for a
	// type stateless mode has no command for.
	Hint string
}

// StatelessRemoval is one live resource this estate owns and no longer
// declares, which the plan proposes destroying.
type StatelessRemoval struct {
	// Addr is where it sits in the prior state, which is the address the
	// plan's own destroy line names.
	Addr string

	TypeName    string
	LiveID      string
	DisplayName string

	// Marker is the tofu-address value it carries, escaped as stored.
	Marker string

	// BlockGone distinguishes a deleted resource block from a block that no
	// longer expands to this instance key.
	BlockGone bool

	// Swept is true when the estate-wide sweep found it, which is the case a
	// config-driven scan cannot see at all.
	Swept bool

	// Why is one sentence saying what makes it a removal.
	Why string
}

// StatelessSweepGap is one resource type the removal sweep could not cover.
type StatelessSweepGap struct {
	TypeName string
	Reason   string
	Detail   string
}

// StatelessRename is one live resource that may have moved to a new for_each
// key, with the command that would move its marker.
type StatelessRename struct {
	// OldAddr is the address the live marker claims and NewAddr the declared
	// instance nothing claimed, both unescaped.
	OldAddr string
	NewAddr string

	TypeName    string
	LiveID      string
	DisplayName string

	// Command is the exact live-mv invocation, quoted for a shell.
	Command string
}

// StatelessRenameAmbiguity is one resource block whose orphans and unclaimed
// declared instances do not pair one-to-one.
type StatelessRenameAmbiguity struct {
	Block string

	// Live are the orphaned live resources, as "marker (live ID)".
	Live []string

	// Declared are the addresses nothing claimed.
	Declared []string

	// Detail is one sentence saying what the obstacle is.
	Detail string
}

// StatelessEstateCount is how many live resources another estate owns.
type StatelessEstateCount struct {
	Estate string
	Count  int
	Types  []string
}

// StatelessUnsweptType is one resource type whose live population is unknown.
type StatelessUnsweptType struct {
	TypeName string
	Reason   string
	Detail   string
}

// StatelessTag is one key/value pair: a resource tag, or an argument a
// content match was made on.
type StatelessTag struct {
	Key   string
	Value string
}

// StatelessPlan renders the parts of live-plan's output that have no
// equivalent in a stock plan. The plan itself is rendered by the ordinary
// [Plan] view, so that live-plan and plan produce identical output for
// the part they have in common.
type StatelessPlan interface {
	// Omissions reports the instances that are missing from the projection,
	// which is why the plan that follows proposes to create them.
	Omissions(oms []StatelessOmission)

	// Foreign reports the live resources the estate does not own: what was
	// found, what could be adopted, and which types the sweep covered.
	Foreign(rep StatelessForeign)
}

// NewStatelessPlan returns the human-readable implementation. There is no
// JSON implementation yet, and the live-plan command rejects -json
// rather than emitting output that omits this section.
func NewStatelessPlan(view *View) StatelessPlan {
	return &StatelessPlanHuman{view: view}
}

// StatelessPlanHuman writes the omissions section as a titled block above the
// plan, in the same stream and with the same width and colouring rules as the
// plan renderer itself.
type StatelessPlanHuman struct {
	view *View
}

var _ StatelessPlan = (*StatelessPlanHuman)(nil)

const statelessOmissionsIntro = `A live-markers run builds prior state by reading the live system. It could not read the following resource instances, so they are absent from the prior state and the plan below proposes to create them. This is not a claim that they do not exist: each line says why the instance could not be read.`

func (v *StatelessPlanHuman) Omissions(oms []StatelessOmission) {
	if len(oms) == 0 {
		return
	}

	cols := v.view.outputColumns()

	noun := "instances"
	if len(oms) == 1 {
		noun = "instance"
	}
	v.view.streams.Print(v.view.colorize.Color(fmt.Sprintf(
		"\n[reset][bold]Not read from the live system: %d resource %s[reset]\n\n",
		len(oms), noun,
	)))
	v.view.streams.Print(format.WordWrap(statelessOmissionsIntro, cols) + "\n\n")

	for _, om := range oms {
		v.view.streams.Print(v.view.colorize.Color(fmt.Sprintf(
			"  [bold]%s[reset] [%s]\n", om.Addr, om.Reason,
		)))
		for _, line := range strings.Split(strings.TrimRight(format.WordWrap(om.Detail, cols-6), "\n"), "\n") {
			v.view.streams.Print("      " + line + "\n")
		}
	}

	v.view.outputHorizRule()
}

const statelessForeignIntro = `These live resources carry no ownership marker for this estate. This run reports them and does nothing else: nothing unowned is in the prior state this plan ran against, so no plan can propose destroying any of them. Adopting one means stamping its markers deliberately.`

const statelessAdoptIntro = `Each of these matches a declared resource that discovery could not find, exactly, on the arguments that identify that resource type. None of them was bound: ownership is the tofu-estate and tofu-address tag pair and nothing else, so claiming one is a tag write you make on purpose.`

const statelessSweepIntro = `A classification is only as wide as the sweep behind it. These resource types were not enumerated, so nothing above says whether foreign resources of these types exist.`

const statelessRemovalIntro = `Each of these carries this estate's ownership marker for an address the configuration no longer declares. They are in the prior state this plan ran against, at the address their marker names, so the plan below proposes destroying them the same way it would destroy any resource whose configuration was deleted. Nothing unowned is here: a resource with no marker for this estate is never in the prior state and can never be planned for destruction.`

const statelessSweepGapIntro = `Finding a resource whose block was deleted means listing its type and reading the markers off what comes back, and these types could not be searched. This estate may own resources of them that no plan will propose destroying. An empty removal list is a statement about the types that were swept and about nothing else.`

// statelessSweepGapReasons is the one paragraph each standing gap gets,
// written once for the group rather than once per type.
var statelessSweepGapReasons = map[string]string{
	"TYPE_NOT_LISTABLE": "The provider cannot list these types at all, so nothing of them can be enumerated and a resource of one of them whose block was deleted stays live and unplanned. Destroy it before removing its block, or delete it out of band.",
	"TYPE_NOT_TAGGABLE": "These types carry no tags, so they can carry no ownership marker and there is nothing for a sweep to search on. A resource of one of them is found by an identity built from its own configuration, which means deleting its resource block deletes the only record of which resource it was. Destroy it before removing its block, or delete it out of band.",
}

const statelessRenameIntro = `Each line below is a live resource this estate owns whose ownership marker names a for_each key the configuration no longer declares, beside the one declared instance of the same resource block that nothing claimed. They are probably the same resource under a new key. Nothing was renamed and the plan is unchanged by this: it still proposes creating the new key, and the live resource is still owned at an address nothing declares. Run the command if the pairing is right - rewriting that tag is the whole move.`

const statelessRenameAmbiguousIntro = `The resource blocks below have live resources this estate owns at for_each keys the configuration no longer declares, and declared instances of the same block that nothing claimed - but not one of each, so which live resource became which key is not something a marker answers. Nothing is offered and the plan is unchanged: it proposes creating the new keys, and the live resources are still owned at addresses nothing declares. Rename them one at a time with "choudoufu live-mv" if you know which is which.`

// Foreign renders the classification of the live resources this estate does
// not own, below the omissions and above the plan.
//
// The section is printed whenever discovery ran, including when it found
// nothing: "swept and found none" and "nothing was swept" are different
// answers, and only saying something when resources turn up would make them
// indistinguishable.
func (v *StatelessPlanHuman) Foreign(rep StatelessForeign) {
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

	switch {
	case len(rep.Items) > 0:
		colored("\n[reset][bold]Foreign resources: %d live %s not owned by estate %s[reset]\n\n",
			len(rep.Items), noun(len(rep.Items), "resource", "resources"), rep.Estate)
		wrapped(statelessForeignIntro, 0)
		out("\n")
		for _, item := range rep.Items {
			colored("  [bold]%s %s[reset]%s\n", item.TypeName, liveIDOrNone(item.LiveID), displaySuffix(item.DisplayName, item.LiveID))
			out("      tags: " + tagSummary(item.Tags, 4) + "\n")
			if item.Why != "" {
				wrapped(item.Why, 6)
			}
		}
	case len(rep.Swept) > 0:
		colored("\n[reset][bold]Foreign resources: none among the %d %s swept[reset]\n\n",
			len(rep.Swept), noun(len(rep.Swept), "type", "types"))
		wrapped("Every live resource of "+strings.Join(rep.Swept, ", ")+" carries an ownership marker. This is a statement about those types only.", 0)
	default:
		colored("\n[reset][bold]Foreign resources: nothing was swept[reset]\n\n")
		wrapped("No resource type was listed in full during this run, so nothing is known about live resources that carry no ownership marker. This is not a report that there are none.", 0)
	}

	if len(rep.Candidates) > 0 {
		colored("\n[reset][bold]Adoptable: %d live %s matches a declared resource[reset]\n\n",
			len(rep.Candidates), noun(len(rep.Candidates), "resource", "resources"))
		wrapped(statelessAdoptIntro, 0)
		out("\n")
		for _, c := range rep.Candidates {
			colored("  [bold]%s[reset] <- %s %s%s\n", c.Addr, c.TypeName, liveIDOrNone(c.LiveID), displaySuffix(c.DisplayName, c.LiveID))
			out("      matched on: " + tagSummary(c.Matched, 0) + "\n")
			if c.Hint != "" {
				// Deliberately not word-wrapped: this line is meant to be
				// copied, and a wrapped command is a command that does not
				// run when pasted.
				out("      adopt with: " + c.Hint + "\n")
			}
			wrapped(fmt.Sprintf("or write %s=%s and %s=%s onto it with any tool that honors stateless/MARKERS.md.",
				"tofu-estate", c.MarkerEstate, "tofu-address", c.MarkerAddress), 6)
		}
	}

	if len(rep.Removals) > 0 {
		colored("\n[reset][bold]Owned and undeclared: %d live %s will be destroyed[reset]\n\n",
			len(rep.Removals), noun(len(rep.Removals), "resource", "resources"))
		wrapped(statelessRemovalIntro, 0)
		out("\n")
		for _, rm := range rep.Removals {
			colored("  [bold]%s[reset] <- %s %s%s\n",
				rm.Addr, rm.TypeName, liveIDOrNone(rm.LiveID), displaySuffix(rm.DisplayName, rm.LiveID))
			if rm.Why != "" {
				wrapped(rm.Why, 6)
			}
		}
	}

	if len(rep.SweepGaps) > 0 {
		// Grouped rather than itemized, because most of this list is the same
		// every run: which types a provider version can list, and which
		// types carry tags, are standing facts, and repeating a paragraph
		// about each of a dozen of them would bury the one entry that is
		// about this run. A list call that failed is itemized for exactly
		// that reason.
		byReason := make(map[string][]string)
		var order []string
		var itemized []StatelessSweepGap
		for _, g := range rep.SweepGaps {
			if g.Reason != "TYPE_NOT_LISTABLE" && g.Reason != "TYPE_NOT_TAGGABLE" {
				itemized = append(itemized, g)
				continue
			}
			if _, seen := byReason[g.Reason]; !seen {
				order = append(order, g.Reason)
			}
			byReason[g.Reason] = append(byReason[g.Reason], g.TypeName)
		}

		colored("\n[reset][bold]Not swept for removal: %d resource %s[reset]\n\n",
			len(rep.SweepGaps), noun(len(rep.SweepGaps), "type", "types"))
		wrapped(statelessSweepGapIntro, 0)
		out("\n")
		for _, g := range itemized {
			colored("  [bold]%s[reset] [%s]\n", g.TypeName, g.Reason)
			wrapped(g.Detail, 6)
		}
		for _, reason := range order {
			colored("  [bold]%s[reset] [%s]\n", strings.Join(byReason[reason], ", "), reason)
			wrapped(statelessSweepGapReasons[reason], 6)
		}
	}

	if len(rep.Renames) > 0 || len(rep.AmbiguousRenames) > 0 {
		colored("\n[reset][bold]Renamed keys? %s[reset]\n\n", renameHeadline(rep))
		if len(rep.Renames) > 0 {
			wrapped(statelessRenameIntro, 0)
		} else {
			wrapped(statelessRenameAmbiguousIntro, 0)
		}
		out("\n")
		for _, r := range rep.Renames {
			colored("  [bold]%s[reset] (live %s) -> [bold]%s[reset]\n",
				r.OldAddr, liveWithName(r.LiveID, r.DisplayName), r.NewAddr)
			// Deliberately not word-wrapped, for the same reason the adoption
			// hint is not: this line exists to be copied.
			out("      rename with: " + r.Command + "\n")
		}
		for _, a := range rep.AmbiguousRenames {
			colored("  [bold]%s[reset] [AMBIGUOUS]\n", a.Block)
			out("      live: " + strings.Join(a.Live, ", ") + "\n")
			out("      declared and unclaimed: " + strings.Join(a.Declared, ", ") + "\n")
			wrapped(a.Detail, 6)
		}
	}

	if len(rep.OtherEstates) > 0 {
		parts := make([]string, 0, len(rep.OtherEstates))
		total := 0
		for _, e := range rep.OtherEstates {
			total += e.Count
			name := e.Estate
			if name == "" {
				name = "estate not recorded"
			}
			parts = append(parts, fmt.Sprintf("%s %d [%s]", name, e.Count, strings.Join(e.Types, ", ")))
		}
		out("\n")
		wrapped(fmt.Sprintf("Other estates present: %d live %s carry another estate's marker and were left alone (%s).",
			total, noun(total, "resource", "resources"), strings.Join(parts, "; ")), 0)
	}

	if len(rep.Unswept) > 0 {
		var notScanned []string
		var itemized []StatelessUnsweptType
		for _, u := range rep.Unswept {
			if u.Reason == "NOT_SCANNED" {
				notScanned = append(notScanned, u.TypeName)
				continue
			}
			itemized = append(itemized, u)
		}

		colored("\n[reset][bold]Not swept: %d resource %s[reset]\n\n",
			len(rep.Unswept), noun(len(rep.Unswept), "type", "types"))
		wrapped(statelessSweepIntro, 0)
		out("\n")
		for _, u := range itemized {
			colored("  [bold]%s[reset] [%s]\n", u.TypeName, u.Reason)
			wrapped(u.Detail, 6)
		}
		if len(notScanned) > 0 {
			colored("  [bold]%s[reset] [NOT_SCANNED]\n", strings.Join(notScanned, ", "))
			wrapped("Nothing of these types needed marker discovery, so none of them was listed on this configuration's behalf. The removal sweep above may have listed them, but a sweep asks the provider for this estate's own resources and no others, so no unclaimed resource of these types ever crossed the wire. Their identities come out of configuration, which says nothing about what else exists in the account.", 6)
		}
	}

	v.view.outputHorizRule()
}

func noun(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// renameHeadline is the "Renamed keys?" subsection's one-line count: what can
// be offered, what cannot, or both.
func renameHeadline(rep StatelessForeign) string {
	var parts []string
	if n := len(rep.Renames); n > 0 {
		parts = append(parts, fmt.Sprintf("%d live %s may have moved to a new key",
			n, noun(n, "resource", "resources")))
	}
	if n := len(rep.AmbiguousRenames); n > 0 {
		parts = append(parts, fmt.Sprintf("%d resource %s cannot be paired",
			n, noun(n, "block", "blocks")))
	}
	return strings.Join(parts, ", ")
}

// liveWithName is the live ID, plus the provider's label when it adds
// something, inside one set of parentheses.
func liveWithName(liveID, displayName string) string {
	id := liveIDOrNone(liveID)
	if displayName == "" || displayName == liveID {
		return id
	}
	return id + ", " + displayName
}

func liveIDOrNone(id string) string {
	if id == "" {
		return "(no identity)"
	}
	return id
}

func displaySuffix(displayName, liveID string) string {
	if displayName == "" || displayName == liveID {
		return ""
	}
	return " (" + displayName + ")"
}

// tagSummary renders at most n pairs as "k=v", with a count of the rest.
// n <= 0 renders all of them.
func tagSummary(tags []StatelessTag, n int) string {
	if len(tags) == 0 {
		return "(none)"
	}
	shown, rest := tags, 0
	if n > 0 && len(shown) > n {
		rest = len(shown) - n
		shown = shown[:n]
	}
	parts := make([]string, 0, len(shown))
	for _, t := range shown {
		parts = append(parts, t.Key+"="+t.Value)
	}
	s := strings.Join(parts, ", ")
	if rest > 0 {
		s += fmt.Sprintf(" (+%d more)", rest)
	}
	return s
}
