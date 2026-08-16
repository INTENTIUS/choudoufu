// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/command/format"
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

	// ParentReads are the untaggable children a parent read found (issue
	// #60): resources with no marker and no declared block of their own,
	// found by reading a marked, admitted parent's identity instead. Each
	// says whether it also became a removal - the plan's own resource diff
	// carries the destroy itself for those, and this list is what says a
	// parent read is why.
	ParentReads []StatelessParentRead
}

// StatelessParentRead is one live child a parent read found.
type StatelessParentRead struct {
	TypeName    string
	Parent      string
	ParentAddr  string
	ParentValue string
	LiveID      string
	DisplayName string

	// Removal is true when this finding also entered the prior state as a
	// destroy.
	Removal bool

	// Withheld is why Removal is false, empty when Removal is true.
	Withheld string
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

// StatelessLookalike is one lookalike guard warning: a declared instance the
// plan actually proposes to create, beside a live resource this estate does
// not own that might be the very thing being duplicated. The fields
// correspond to [foreign.Lookalike].
type StatelessLookalike struct {
	// Addr is the declared instance the plan proposes to create - the same
	// address the resource diff's own "will be created" line names.
	Addr     string
	TypeName string

	// LiveID is the unowned live resource's identity, empty when the
	// provider sent no usable one.
	LiveID      string
	DisplayName string

	// Matched are the identity-bearing arguments that confirmed the match,
	// empty for the generic, cardinality-only warning.
	Matched []StatelessTag

	// MarkerEstate and MarkerAddress are the tofu-estate and tofu-address
	// values that adopt the live resource instead of creating a duplicate.
	MarkerEstate  string
	MarkerAddress string

	// Hint is the one-line adoption command, empty for a type this fork has
	// no composable tagging verb for.
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

// StatelessUnowned is one live resource the projection refused to admit: it
// sits at the identity a declared resource names and carries no ownership
// marker for this estate. The fields correspond to projection.Unowned, plus
// the two tag values that would adopt it, worked out by the caller.
type StatelessUnowned struct {
	// Addr is the declared instance whose identity found it.
	Addr string

	// TypeName is the resource type and LiveID the identity the live
	// resource was read with, which is the handle a human needs to go look
	// at it.
	TypeName string
	LiveID   string

	// HeldBy is the tofu-estate marker the live resource carries, empty when
	// it carries none. A non-empty value means the resource is owned, just
	// not by this run.
	HeldBy string

	// MarkerEstate and MarkerAddress are the tofu-estate and tofu-address
	// values that would adopt the resource, both empty when adoption is not
	// this run's to offer: the resource belongs to another estate, or this
	// run has no estate name to write.
	MarkerEstate  string
	MarkerAddress string
}

// StatelessPolicyDeclared is one declared instance whose admission or tag
// handling GitHub issue #67's policy governed with a non-default verb -
// projection.PolicyOutcome, in a form this package can render without
// importing the projection builder.
type StatelessPolicyDeclared struct {
	Addr     string
	TypeName string

	// Tagged says which declared quadrant: declared_tagged when true,
	// declared_untagged when false.
	Tagged bool

	Verb string
}

// StatelessPolicyWithheld is one owned-but-undeclared resource
// (undeclared_tagged) a non-default policy verb kept out of the removal
// sweep - discovery.OwnedResource with a PolicyVerb set, mirrored into
// StatelessRemoval's shape plus the verb and the withheld sentence.
type StatelessPolicyWithheld struct {
	TypeName    string
	LiveID      string
	DisplayName string
	Marker      string
	Verb        string
	Withheld    string
}

// StatelessUntagged is one resource block a declared_tagged = "untag" verb
// released a tag key from - stamp.Untagged, in this package's own shape.
type StatelessUntagged struct {
	Addr         string
	Key          string
	EstateMarker bool
}

// StatelessReleased is one owned-but-undeclared resource GitHub issue #67's
// undeclared_tagged = "untag" verb released a tag key from for real, during
// apply - internal/live/untag.Outcome, in this package's own shape.
//
// Distinct from [StatelessUntagged]: that type reports a declared_tagged
// block's tag removal from the plan that proposes it, since the ordinary
// apply graph performs the write and a plan showing it is a true
// prediction. This resource has no configuration block and no graph node,
// so there is nothing to predict - only [statelessRunner.AfterApply]'s own
// report, after a real apply, of what actually happened. OK false means
// the resource was left exactly as it was found; nothing here ever falls
// back to destroying it.
type StatelessReleased struct {
	TypeName     string
	LiveID       string
	DisplayName  string
	Marker       string
	Key          string
	EstateMarker bool
	OK           bool
	Detail       string
}

// StatelessReconcileCandidate is one live resource GitHub issue #67's
// undeclared_untagged = "delete" scoped account reconciliation would
// destroy - discovery.ReconcileCandidate, rendered with identity evidence
// per the issue's "no aggregate count without the roster" rule.
type StatelessReconcileCandidate struct {
	TypeName    string
	LiveID      string
	DisplayName string
}

// StatelessReconcileGap is one scope-selected type the reconciliation pass
// could not enumerate.
type StatelessReconcileGap struct {
	TypeName string
	Reason   string
	Detail   string
}

// StatelessReconcile is the outcome of one scoped account-reconciliation
// pass, when the resolved policy asked for one.
type StatelessReconcile struct {
	Ran               bool
	Roster            []StatelessReconcileCandidate
	Gaps              []StatelessReconcileGap
	Threshold         int
	ThresholdExceeded bool
}

// StatelessPolicyReport is everything GitHub issue #67's policy block did
// this run beyond today's fixed behavior. Every field is empty on a run
// with no policy block, or one that only ever names default verbs - which
// is what makes "omitted policy = byte-identical current behavior" visible
// in the rendered output and not only in the underlying data.
type StatelessPolicyReport struct {
	Declared  []StatelessPolicyDeclared
	Withheld  []StatelessPolicyWithheld
	Untagged  []StatelessUntagged
	Released  []StatelessReleased
	Reconcile StatelessReconcile
}

// Empty reports whether there is nothing to render.
func (r StatelessPolicyReport) Empty() bool {
	return len(r.Declared) == 0 && len(r.Withheld) == 0 && len(r.Untagged) == 0 && len(r.Released) == 0 && !r.Reconcile.Ran
}

// StatelessProgress is one discovery heartbeat, already throttled by the
// caller: how many resource types have been scanned in total and how many
// live resources scanning has found, as of the type named in TypeName. It
// mirrors [discovery.ProgressEvent] rather than importing that package,
// the same way every other type in this file carries the projection and
// foreign packages' data across without importing them - see this file's
// other Stateless* types.
type StatelessProgress struct {
	TypeName       string
	TypesScanned   int
	ResourcesFound int
}

// StatelessPlan renders the parts of live-plan's output that have no
// equivalent in a stock plan. The plan itself is rendered by the ordinary
// [Plan] view, so that live-plan and plan produce identical output for
// the part they have in common.
type StatelessPlan interface {
	// Progress reports one discovery heartbeat. It is the only method on
	// this interface that writes to stderr rather than stdout: a heartbeat
	// exists to prove a slow, silent sweep is still running, not to become
	// part of the plan's own output, and it must never appear in anything a
	// script reads from this command's stdout. The caller decides how often
	// to call it; every call here is rendered.
	Progress(p StatelessProgress)

	// Omissions reports the instances that are missing from the projection,
	// which is why the plan that follows proposes to create them.
	Omissions(oms []StatelessOmission)

	// Unowned reports the live resources found at declared identities
	// without this estate's marker: which of them a tag write adopts, and
	// which are simply in the way of the create the plan proposes.
	Unowned(items []StatelessUnowned)

	// Foreign reports the live resources the estate does not own: what was
	// found, what could be adopted, and which types the sweep covered.
	Foreign(rep StatelessForeign)

	// Policy reports what GitHub issue #67's policy block did this run:
	// which declared instances a non-default verb governed, which
	// undeclared_tagged resources a non-default verb withheld from the
	// sweep, which resources released a tag key, and the scoped
	// account-reconciliation roster when undeclared_untagged = "delete"
	// ran. A no-op when rep.Empty().
	Policy(rep StatelessPolicyReport)

	// GuidedFallback reports why a pass that had guided discovery
	// configured (issue #64) fell back to today's full sweep instead of
	// using it - a stale, missing or unreadable hint in the estate's
	// record store, in one sentence from
	// [discovery.Result.GuidedFallback]. reason is empty whenever guided
	// discovery was never configured for this pass, or whenever it engaged
	// successfully, and an empty reason renders nothing: this is
	// informational only, never a warning that something is wrong with the
	// plan itself, which the fallback's own safety argument (a stale or
	// missing hint costs one full re-read, never a wrong plan) is what
	// makes true.
	GuidedFallback(reason string)

	// Lookalikes reports the lookalike guard's findings: planned creates
	// that might duplicate a live resource this estate does not own, each
	// naming the resource and the adoption remedy. Printed last, immediately
	// above the plan itself, so the warning sits right next to the create it
	// is about.
	Lookalikes(items []StatelessLookalike)
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

// Progress writes one heartbeat line to stderr, dark-grey like the
// horizontal rules elsewhere in this package (see format.HorizontalRule) so
// it reads as ambient status rather than as a result. It is deliberately
// plain: no section header, no word wrap, one line that a scrolling
// terminal simply carries away - the throttling that keeps this from
// becoming a log is the caller's job, not this method's.
//
// The wording says "so far" and names the type still being scanned rather
// than reading as a finished tally (issue #229): the caller's throttle
// (statelessProgress) always lets the very first event through
// unconditionally, and a fast run - a small emulator estate finishes well
// under the 500ms throttle window - can end up printing only that one
// event. Before this fix that line was "discovering: 1 type scanned, 1 live
// resource found (aws_acm_certificate)", which reads exactly like a final
// count and sent a #229 investigation down the wrong path chasing why
// discovery only looked at one type, when in fact hundreds more had been
// scanned by the time the plan finished - this is the running count as of
// one type partway through, not the total.
func (v *StatelessPlanHuman) Progress(p StatelessProgress) {
	noun := "resource"
	if p.ResourcesFound != 1 {
		noun = "resources"
	}
	typeNoun := "type"
	if p.TypesScanned != 1 {
		typeNoun = "types"
	}
	v.view.streams.Eprint(v.view.colorize.Color(fmt.Sprintf(
		"[dark_gray]discovering: %d %s scanned so far, %d live %s found so far (currently on %s)[reset]\n",
		p.TypesScanned, typeNoun, p.ResourcesFound, noun, p.TypeName,
	)))
}

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

// GuidedFallback renders the one-sentence reason a configured guided-discovery
// pass fell back to a full sweep, as a small informational note rather than a
// titled, itemized section like the ones around it: there is exactly one
// sentence to say, about the run as a whole rather than about any particular
// resource, so a heading-plus-intro-plus-list shape would be a lot of
// scaffolding around one line.
func (v *StatelessPlanHuman) GuidedFallback(reason string) {
	if reason == "" {
		return
	}

	cols := v.view.outputColumns()

	v.view.streams.Print(v.view.colorize.Color(
		"\n[reset][bold]Guided discovery: fell back to a full sweep[reset]\n\n",
	))
	v.view.streams.Print(format.WordWrap(reason, cols) + "\n")

	v.view.outputHorizRule()
}

const statelessUnownedIntro = `Each of these is a live resource sitting at the identity a declared resource names, without this estate's ownership marker on it. They are the plan's [UNOWNED] omissions, gathered here by what resolves each one. None of them is in the prior state this plan ran against, so nothing in the plan changes or destroys them, and the plan proposes creating what the configuration declares - a create the cloud will refuse while the live resource holds the identity. An [ADOPTABLE] entry becomes this estate's by writing the two tags shown, on purpose; an [IN_THE_WAY] entry is not this run's to claim.`

// Unowned renders the projection's refusals as their own section, between the
// omissions and the foreign report, so that "this needs adopting" and
// "something else is in the way of this address" read at a glance instead of
// out of the omission prose. Nothing renders when there is nothing to say:
// unlike the sweep behind the foreign section, this check runs on every
// instance the projection reads, so an empty list is not a coverage question.
func (v *StatelessPlanHuman) Unowned(items []StatelessUnowned) {
	if len(items) == 0 {
		return
	}

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

	adoptable := 0
	for _, u := range items {
		if u.MarkerEstate != "" {
			adoptable++
		}
	}
	var parts []string
	if adoptable > 0 {
		parts = append(parts, fmt.Sprintf("%d adoptable", adoptable))
	}
	if n := len(items) - adoptable; n > 0 {
		parts = append(parts, fmt.Sprintf("%d in the way", n))
	}

	colored("\n[reset][bold]Unowned: %d live %s this configuration declares (%s)[reset]\n\n",
		len(items),
		noun(len(items), "resource holds an identity", "resources hold identities"),
		strings.Join(parts, ", "))
	wrapped(statelessUnownedIntro, 0)
	out("\n")

	for _, u := range items {
		switch {
		case u.MarkerEstate != "":
			colored("  [bold]%s[reset] [ADOPTABLE] <- %s %s\n", u.Addr, u.TypeName, liveIDOrNone(u.LiveID))
			// Deliberately not word-wrapped, like the adoption hint in the
			// foreign section: this line exists to be copied.
			out("      adopt by writing: tofu-estate=" + u.MarkerEstate + " tofu-address=" + u.MarkerAddress + "\n")
			wrapped("Write both tags with any tool that honors live/MARKERS.md, then re-run; the next plan binds it instead of proposing a duplicate.", 6)
		case u.HeldBy != "":
			colored("  [bold]%s[reset] [IN_THE_WAY] <- %s %s\n", u.Addr, u.TypeName, liveIDOrNone(u.LiveID))
			wrapped(fmt.Sprintf("held by estate %q. Moving a resource between estates is a deliberate retag by its owner, never a side effect of this estate planning. Otherwise, point the declared resource at an identity nobody is using.", u.HeldBy), 6)
		default:
			colored("  [bold]%s[reset] [IN_THE_WAY] <- %s %s\n", u.Addr, u.TypeName, liveIDOrNone(u.LiveID))
			wrapped("Whether this estate owns it cannot be checked, because this run has no estate name. Pass -estate=<name>, or name the estate in the live block, and re-run.", 6)
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

const statelessParentReadIntro = `Each of these carries no tags and no ownership marker of its own, and no resource block declares it either. It was found by reading a marked, admitted parent's own identity instead: a bucket policy's identity is the bucket's own name, and so on for the other types below (see live/LIMITATIONS.md, "Some untaggable types are swept via a parent read instead"). REPORT ONLY means this pass can see it but does not yet trust the read to remove it; WILL BE DESTROYED means it does, and the destroy itself is in the resource diff above this section, the same as any other planned removal.`

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
			wrapped(fmt.Sprintf("or write %s=%s and %s=%s onto it with any tool that honors live/MARKERS.md.",
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
		// A list call that failed is this run's own news and is never
		// collapsed: it is itemized above regardless of -verbose. The
		// standing-fact groups below - a provider version's fixed inability
		// to list or tag a type, true of every run against it - are what
		// makes a fresh estate's first plan "337 types deep" (GitHub issue
		// #78), so those are the ones a summary line stands in for by
		// default.
		for _, g := range itemized {
			colored("  [bold]%s[reset] [%s]\n", g.TypeName, g.Reason)
			wrapped(g.Detail, 6)
		}
		switch {
		case v.view.verbose:
			for _, reason := range order {
				colored("  [bold]%s[reset] [%s]\n", strings.Join(byReason[reason], ", "), reason)
				wrapped(statelessSweepGapReasons[reason], 6)
			}
		case len(order) > 0:
			var collapsed int
			for _, reason := range order {
				collapsed += len(byReason[reason])
			}
			wrapped(fmt.Sprintf(
				"%d of them are %s: standing facts about what this provider version can list and tag, true of every run against it and not something this run discovered, so they are named here by count instead of one at a time. Rerun with -verbose to print every type by name, or see live/LIMITATIONS.md, \"Removal coverage is the admission table\", for the same list.",
				collapsed, strings.Join(order, " or "),
			), 0)
		}
	}

	if len(rep.ParentReads) > 0 {
		colored("\n[reset][bold]Swept via parent read: %d resource %s[reset]\n\n",
			len(rep.ParentReads), noun(len(rep.ParentReads), "type", "types"))
		wrapped(statelessParentReadIntro, 0)
		out("\n")
		for _, f := range rep.ParentReads {
			status := "REPORT ONLY"
			if f.Removal {
				status = "WILL BE DESTROYED"
			}
			colored("  [bold]%s[reset] %s via [bold]%s[reset] %s [%s]\n",
				f.TypeName, liveIDOrNone(f.LiveID), f.Parent, f.ParentAddr, status)
			if f.Removal {
				wrapped(fmt.Sprintf("found by reading %s's own identity (%s); see the resource diff above for the destroy.", f.Parent, f.ParentValue), 6)
			} else {
				wrapped(f.Withheld, 6)
			}
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

const statelessPolicyDeclaredIntro = `GitHub issue #67's policy block assigned a non-default verb to these declared instances. Each line names the quadrant the instance fell in (declared_tagged: already carries this estate's marker; declared_untagged: does not) and the verb that governed it.`

const statelessPolicyWithheldIntro = `These live resources carry this estate's ownership marker for an address the configuration no longer declares, and a policy verb kept them out of the removal sweep: they are not in the prior state this plan ran against, and nothing below proposes destroying them.`

const statelessUntaggedIntro = `declared_tagged = "untag" released the named tag key from these resources' desired configuration. Each is otherwise stamped and managed exactly as it would be under converge; only the named key is affected. A resource marked "leaves management" released its own tofu-estate marker: this run's marker discovery can no longer find it by that marker, so a later plan will treat it as declared_untagged rather than converging it.`

const statelessReleasedIntro = `undeclared_tagged = "untag" released the named tag key from these live resources after a real apply changed the cloud - an orphan has no configuration block for the ordinary plan graph to hang an update off of, so this happens once, outside the graph, and is reported here rather than predicted in the plan above. RELEASED means the tag is confirmed gone by a read that followed the write; the resource itself was never destroyed or replaced. FAILED means the resource was left exactly as it was found - read the detail line for why.`

const statelessReconcileIntro = `undeclared_untagged = "delete" is scoped account reconciliation: every live resource of an admitted, enumerable type in this policy's scope that carries no estate marker and no preservation tag is planned for destruction, individually, with its identity evidence. This list is never a claim that the account is clean - see the gaps below for what this pass could not look at.`

// Policy renders GitHub issue #67's policy report as its own section,
// between Foreign and the plan. A no-op when rep.Empty(), so a run with no
// policy block - or one that only ever names default verbs - prints nothing
// new at all.
func (v *StatelessPlanHuman) Policy(rep StatelessPolicyReport) {
	if rep.Empty() {
		return
	}

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

	if len(rep.Declared) > 0 {
		colored("\n[reset][bold]Policy: %d declared %s governed by a non-default verb[reset]\n\n",
			len(rep.Declared), noun(len(rep.Declared), "instance", "instances"))
		wrapped(statelessPolicyDeclaredIntro, 0)
		out("\n")
		for _, d := range rep.Declared {
			quadrant := "declared_untagged"
			if d.Tagged {
				quadrant = "declared_tagged"
			}
			colored("  [bold]%s[reset] <- %s [%s=%s]\n", d.Addr, d.TypeName, quadrant, d.Verb)
		}
	}

	if len(rep.Withheld) > 0 {
		colored("\n[reset][bold]Policy kept: %d owned %s withheld from the sweep[reset]\n\n",
			len(rep.Withheld), noun(len(rep.Withheld), "resource", "resources"))
		wrapped(statelessPolicyWithheldIntro, 0)
		out("\n")
		for _, w := range rep.Withheld {
			colored("  [bold]%s %s[reset]%s [undeclared_tagged=%s]\n",
				w.TypeName, liveIDOrNone(w.LiveID), displaySuffix(w.DisplayName, w.LiveID), w.Verb)
			if w.Withheld != "" {
				wrapped(w.Withheld, 6)
			}
		}
	}

	if len(rep.Untagged) > 0 {
		colored("\n[reset][bold]Policy untag: %d resource %s releasing a tag[reset]\n\n",
			len(rep.Untagged), noun(len(rep.Untagged), "block", "blocks"))
		wrapped(statelessUntaggedIntro, 0)
		out("\n")
		for _, u := range rep.Untagged {
			status := ""
			if u.EstateMarker {
				status = " [LEAVES MANAGEMENT]"
			}
			colored("  [bold]%s[reset] releases %q%s\n", u.Addr, u.Key, status)
		}
	}

	if len(rep.Released) > 0 {
		failed := 0
		for _, u := range rep.Released {
			if !u.OK {
				failed++
			}
		}
		if failed > 0 {
			colored("\n[reset][bold]Policy untag (applied): %d of %d %s NOT released[reset]\n\n",
				failed, len(rep.Released), noun(len(rep.Released), "resource", "resources"))
		} else {
			colored("\n[reset][bold]Policy untag (applied): %d %s released a tag[reset]\n\n",
				len(rep.Released), noun(len(rep.Released), "resource", "resources"))
		}
		wrapped(statelessReleasedIntro, 0)
		out("\n")
		for _, u := range rep.Released {
			status := "[bold][green]RELEASED[reset]"
			if !u.OK {
				status = "[bold][red]FAILED[reset]"
			}
			colored("  %s [bold]%s %s[reset]%s releases %q\n",
				status, u.TypeName, liveIDOrNone(u.LiveID), displaySuffix(u.DisplayName, u.LiveID), u.Key)
			if u.Detail != "" {
				wrapped(u.Detail, 6)
			}
		}
	}

	if rep.Reconcile.Ran {
		n := len(rep.Reconcile.Roster)
		if rep.Reconcile.ThresholdExceeded {
			colored("\n[reset][bold]Policy delete REFUSED: %d candidate %s exceeds the threshold of %d[reset]\n\n",
				n, noun(n, "resource", "resources"), rep.Reconcile.Threshold)
			wrapped("Review the roster below, and raise policy.threshold deliberately once it has been reviewed. Nothing was deleted.", 0)
		} else {
			colored("\n[reset][bold]Policy delete: %d live %s will be destroyed (scoped account reconciliation)[reset]\n\n",
				n, noun(n, "resource", "resources"))
			wrapped(statelessReconcileIntro, 0)
		}
		out("\n")
		for _, c := range rep.Reconcile.Roster {
			colored("  [bold]%s %s[reset]%s\n", c.TypeName, liveIDOrNone(c.LiveID), displaySuffix(c.DisplayName, c.LiveID))
		}
		if len(rep.Reconcile.Gaps) > 0 {
			out("\n")
			wrapped(fmt.Sprintf("Not reconciled: %d resource %s this pass could not enumerate. \"delete\" never implies the account is clean.",
				len(rep.Reconcile.Gaps), noun(len(rep.Reconcile.Gaps), "type", "types")), 0)
			for _, g := range rep.Reconcile.Gaps {
				colored("  [bold]%s[reset] [%s]\n", g.TypeName, g.Reason)
				wrapped(g.Detail, 6)
			}
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

const statelessLookalikeIntro = `Each of these is an instance the plan below proposes to create, beside a live resource this estate does not own that might be the very thing being duplicated - most often because its tofu-estate and tofu-address tags were stripped or never written. This is a warning, not a block: the create may be genuinely intended, and nothing about the plan below is changed by it. If the create does duplicate the live resource, adopt it instead of applying this plan: write the two tags shown, or run the command, then re-run.`

// Lookalikes renders the lookalike guard's findings, last of the
// live-plan-only sections and immediately above the plan diff itself, so
// that a warning about a create sits as close as this report gets to the
// create it is about.
//
// Printed only when there is something to say: unlike Foreign, this section
// is not a sweep-coverage question with its own thing to report when
// empty - a plan with nothing to warn about is simply a plan with nothing to
// warn about.
func (v *StatelessPlanHuman) Lookalikes(items []StatelessLookalike) {
	if len(items) == 0 {
		return
	}

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

	colored("\n[reset][bold]Possible duplicates: %d planned %s may duplicate a live resource this estate does not own[reset]\n\n",
		len(items), noun(len(items), "create", "creates"))
	wrapped(statelessLookalikeIntro, 0)
	out("\n")

	for _, l := range items {
		colored("  [bold]%s[reset] [POSSIBLE DUPLICATE] ~ %s %s%s\n",
			l.Addr, l.TypeName, liveIDOrNone(l.LiveID), displaySuffix(l.DisplayName, l.LiveID))
		if len(l.Matched) > 0 {
			out("      matched on: " + tagSummary(l.Matched, 0) + "\n")
			wrapped(fmt.Sprintf(
				"a live %s this estate does not own matches this create exactly (%s); if this create duplicates it, adopt instead:",
				l.TypeName, liveIDOrNone(l.LiveID)), 6)
		} else {
			wrapped(fmt.Sprintf(
				"a live %s this estate does not own exists (%s); if this create duplicates it, adopt instead:",
				l.TypeName, liveIDOrNone(l.LiveID)), 6)
		}
		if l.Hint != "" {
			// Deliberately not word-wrapped, like every other adoption
			// command in this view: this line exists to be copied.
			out("      adopt with: " + l.Hint + "\n")
		} else {
			out("      adopt by writing: tofu-estate=" + l.MarkerEstate + " tofu-address=" + l.MarkerAddress + "\n")
		}
	}

	v.view.outputHorizRule()
}
