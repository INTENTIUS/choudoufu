// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package policy

import (
	"sort"
	"strings"
)

// Verb is one action a policy quadrant may be assigned. The full set, from
// GitHub issue #67's design: converge, adopt, refuse, untag, keep, report,
// delete. Not every verb is valid in every quadrant - see [ValidVerbs].
type Verb string

const (
	// Converge is ordinary management: plan and apply against the
	// configuration's intended state, the way a resource with no policy
	// block behaves today.
	Converge Verb = "converge"

	// Adopt claims a declared-but-unmarked resource explicitly, writing
	// this estate's ownership marker onto it.
	Adopt Verb = "adopt"

	// Refuse declines to act on a declared-but-unmarked resource until it
	// is adopted, today's fixed behavior for that quadrant.
	Refuse Verb = "refuse"

	// Untag removes the policy's tag from a resource without deleting it:
	// "source owns it now" for a declared-and-tagged resource, or "I no
	// longer want to manage this, but it stays" for an undeclared-and-
	// tagged one.
	Untag Verb = "untag"

	// Keep leaves a resource alone: no tag change, no deletion, no plan
	// action. The quiet verb.
	Keep Verb = "keep"

	// Report surfaces a resource in plan output without acting on it -
	// visibility without a side effect, available in every quadrant.
	Report Verb = "report"

	// Delete is scoped account reconciliation - aws-nuke semantics - and
	// gets the strictest treatment: see internal/live/lint's refusal of a
	// delete quadrant with no scope block.
	Delete Verb = "delete"
)

// Quadrant identifies one cell of the ownership matrix: whether a resource
// is declared in the configuration and whether it carries the policy's tag.
type Quadrant int

const (
	// DeclaredTagged: in source, carries the tag. Already owned twice over.
	DeclaredTagged Quadrant = iota

	// DeclaredUntagged: in source, no tag yet. The ordinary "about to be
	// managed" case.
	DeclaredUntagged

	// UndeclaredTagged: not in source, carries the tag. The quadrant the
	// maintainer's motivating policy (issue #67) inverts: the tag can mean
	// "protected" instead of "sweep this".
	UndeclaredTagged

	// UndeclaredUntagged: not in source, no tag. Foreign under today's
	// fixed behavior, or the account-scope reconciliation target the
	// maintainer's motivating policy turns it into.
	UndeclaredUntagged
)

// String renders the quadrant the way issue #67's design table names it.
func (q Quadrant) String() string {
	switch q {
	case DeclaredTagged:
		return "declared+tagged"
	case DeclaredUntagged:
		return "declared+untagged"
	case UndeclaredTagged:
		return "undeclared+tagged"
	case UndeclaredUntagged:
		return "undeclared+untagged"
	default:
		return "unknown quadrant"
	}
}

// Attribute is the policy block's attribute name for this quadrant, exactly
// as an author writes it: "declared_tagged", and so on.
func (q Quadrant) Attribute() string {
	switch q {
	case DeclaredTagged:
		return "declared_tagged"
	case DeclaredUntagged:
		return "declared_untagged"
	case UndeclaredTagged:
		return "undeclared_tagged"
	case UndeclaredUntagged:
		return "undeclared_untagged"
	default:
		return "unknown"
	}
}

// Quadrants lists all four quadrants in the policy block's own attribute
// order, for callers that need to walk every one.
var Quadrants = []Quadrant{DeclaredTagged, DeclaredUntagged, UndeclaredTagged, UndeclaredUntagged}

// ValidVerbs is the per-quadrant verb-validity matrix: which of the seven
// verbs a policy block may legally assign to which quadrant.
// internal/live/lint is the only caller that turns a missing entry here into
// a lint refusal; this map is pure data so that every quadrant's ruling is
// written down, and reasoned about, in exactly one place rather than
// scattered across conditionals.
//
// The rulings, one quadrant at a time - deliberately conservative, in the
// sense that a verb is only listed when there is a coherent reading of what
// it does in that quadrant; anything else is refused at lint rather than
// left to run and surprise someone:
//
//   - declared+tagged: the resource is declared in source and already
//     carries the tag, so it is owned twice over. converge (ordinary
//     management), untag (the maintainer's own example: "remove the tag;
//     source owns it now"), keep and report are all coherent answers to
//     "what happens to something already owned". adopt makes no sense -
//     there is nothing left to adopt. refuse makes no sense - a declared,
//     tagged resource is not awaiting a decision the way an unmarked one
//     is. delete is refused here the same as everywhere but the two
//     undeclared quadrants below: deleting a resource the source still
//     declares contradicts the source being the intended state, which is
//     not a contradiction this fork resolves by silently deleting the
//     resource.
//
//   - declared+untagged: declared in source, not yet marked - the moment
//     before ordinary management, or ordinary management itself. converge
//     is the maintainer's own example ("ordinary management"). adopt claims
//     it explicitly; refuse is today's fixed default (declined until
//     adopted); keep and report are quieter variants of refuse. untag makes
//     no sense - there is no tag to remove. delete makes no sense for the
//     same reason as the previous quadrant: it is declared, and this fork
//     does not delete what the source still declares.
//
//   - undeclared+tagged: not in source, but carries the tag - the quadrant
//     the maintainer's motivating policy inverts. keep reads the tag as
//     "protected" (the maintainer's own example). delete reads it as
//     "nothing declares this any more, sweep it", today's fixed default.
//     untag also makes sense: release the marker without destroying the
//     object, for "the tag protects it, but I no longer want to manage it".
//     report is always available. converge and adopt make no sense - there
//     is nothing declared to converge against or adopt into.
//
//   - undeclared+untagged: not in source, no tag - foreign under today's
//     fixed behavior (keep, today's default), or the account-scope
//     reconciliation target the maintainer's motivating policy turns it
//     into (delete, the maintainer's own example). report is always
//     available. adopt is refused here specifically, even though the verb
//     exists for the sibling declared+untagged quadrant: adopting a
//     resource with neither a declaration nor a marker would pull it into
//     management on the strength of nothing at all, the opposite of how
//     adoption works everywhere else in this fork. converge, refuse and
//     untag make no sense for the same reasons as the tagged sibling.
var ValidVerbs = map[Quadrant]map[Verb]bool{
	DeclaredTagged: {
		Converge: true,
		Untag:    true,
		Keep:     true,
		Report:   true,
	},
	DeclaredUntagged: {
		Converge: true,
		Adopt:    true,
		Refuse:   true,
		Keep:     true,
		Report:   true,
	},
	UndeclaredTagged: {
		Delete: true,
		Keep:   true,
		Untag:  true,
		Report: true,
	},
	UndeclaredUntagged: {
		Keep:   true,
		Delete: true,
		Report: true,
	},
}

// DefaultVerb is today's fixed behavior, read as the preset every quadrant a
// policy block omits - or the whole block, when a configuration has none at
// all - resolves to:
//
//	declared+tagged     -> converge   (ordinary management)
//	declared+untagged   -> refuse     (declined until adopted)
//	undeclared+tagged   -> delete     (today's sweep)
//	undeclared+untagged -> keep       (today's "ignore, foreign")
//
// [Build] fills every unset quadrant from this map, so "omitted policy block
// = today's exact behavior" is true by construction: a configuration with no
// policy block and one whose policy block spells out these same four verbs
// produce the identical [Policy] value.
var DefaultVerb = map[Quadrant]Verb{
	DeclaredTagged:     Converge,
	DeclaredUntagged:   Refuse,
	UndeclaredTagged:   Delete,
	UndeclaredUntagged: Keep,
}

// Valid reports whether v is a legal assignment for q, per [ValidVerbs].
func Valid(q Quadrant, v Verb) bool {
	return ValidVerbs[q][v]
}

// ValidVerbNames returns q's valid verbs as a sorted, comma-separated list,
// for a lint message that names the set an invalid verb was refused against.
func ValidVerbNames(q Quadrant) string {
	verbs := ValidVerbs[q]
	names := make([]string, 0, len(verbs))
	for v := range verbs {
		names = append(names, string(v))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
