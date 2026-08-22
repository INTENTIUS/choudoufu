// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package strict holds the vocabulary of the live block's "strict" block:
// HANDOFF.md's principles, each expressed as a toggle with a default that is
// today's behavior.
//
// It is deliberately the same shape internal/live/policy has for the
// ownership matrix - a verb type, the valid set, a default, and no
// dependency on internal/configs. The decoder (internal/configs/live.go)
// records the literal string an author wrote; this package says which
// strings mean something; internal/live/lint refuses the ones that do not.
// Keeping the three apart is what lets the decoder stay a decoder.
//
// # What is implemented, and what is not
//
// GitHub issue #365 is the schema for four toggles. This package carries the
// first, marker_repair, and only its grammar: see [Implemented] for the
// reason the two non-default settings are refused by lint rather than
// silently accepted.
package strict

import (
	"sort"
	"strings"
)

// MarkerRepair is what a run does about an ownership marker on a live object
// that disagrees with the marker this configuration would write.
//
// The three settings are HANDOFF.md's, and the one that matters for reading
// this package is that NONE of them is about creating a marker. A resource
// this run creates is stamped whatever the setting: the safety rule ("never
// write a wrong marker") has no converse permitting an unmarked create, and
// a create writes a marker that is new rather than one that disagrees with
// anything. Only an existing, already-well-formed marker whose value differs
// is in scope here.
type MarkerRepair string

const (
	// Repair writes the marker this configuration declares over the one the
	// live object carries, as an ordinary in-place tags update in the plan.
	// This is what every run does today and what a configuration with no
	// strict block keeps doing.
	//
	// live/MARKERS.md is why it is the default: tofu-address is mandatory
	// on every managed resource and "there is exactly one tofu-address
	// value on a resource at any time", so a stale value is a history the
	// spec does not allow, and a mandatory tag that is allowed to be wrong
	// is worse than no tag at all - any tool honoring the three keys would
	// bind the wrong resource.
	Repair MarkerRepair = "repair"

	// Report leaves the live object's marker as it is and says so, naming
	// the address and the value the run would have written. The run
	// proceeds.
	Report MarkerRepair = "report"

	// Never leaves the live object's marker as it is, silently. It is the
	// setting for an estate where something else owns the tags, and
	// HANDOFF.md pairs it with honouring lifecycle { ignore_changes } the
	// way stock honours it.
	//
	// That pairing is not a consequence of the setting, it IS the setting.
	// Nothing in this fork writes a marker onto a live object directly (see
	// [Implemented]), so the only thing "never repair" can name is the
	// ordinary tags diff, and the only way to suppress that diff is
	// lifecycle { ignore_changes } - which internal/live/lint refuses,
	// because a resource whose marker is neither written nor repaired has no
	// identity left. Give it one, with a markers "record" selection, and the
	// refusal has nothing to protect: that is what makes this setting
	// implementable, and it is why it is implemented WITH such a selection
	// and refused without one. See [ImplementedWithSelection].
	Never MarkerRepair = "never"
)

// DefaultMarkerRepair is what an omitted marker_repair argument means, and
// therefore what every configuration written before the strict block existed
// keeps getting. "Compatible out of the box" is this constant, in HANDOFF's
// words: "markers are written on every taggable resource; marker repair is
// on."
const DefaultMarkerRepair = Repair

// markerRepairSupport is one setting's row: whether a build acts on it at
// all, and whether it acts on it for a configuration whose strict block
// carries a markers "record" selection.
//
// Two columns rather than two tables, so [Valid], [Implemented] and
// [ImplementedWithSelection] cannot drift apart - the reason the first two
// shared one table before the second column existed.
type markerRepairSupport struct {
	// always is whether a build acts on the setting for every
	// configuration, selection or no selection.
	always bool

	// withSelection is whether a build acts on it for a configuration whose
	// strict block carries a non-empty markers "record" selection. Implied
	// by always: a setting that works everywhere works there too.
	withSelection bool
}

// markerRepairs is the whole vocabulary, paired with what a build does about
// each.
var markerRepairs = map[MarkerRepair]markerRepairSupport{
	Repair: {always: true, withSelection: true},
	Report: {always: false, withSelection: false},
	Never:  {always: false, withSelection: true},
}

// Valid reports whether v is one of the three settings this fork's schema
// defines. It says nothing about whether a build acts on it - see
// [Implemented].
func Valid(v MarkerRepair) bool {
	_, ok := markerRepairs[v]
	return ok
}

// Implemented reports whether a build acts on v.
//
// Today only [Repair] is implemented, and the gap is deliberate rather than
// an oversight, because of where marker repair actually happens. Nothing in
// this fork writes a marker tag onto a live object directly: internal/live/
// stamp rewrites the CONFIGURATION, injecting the markers into a resource's
// tags argument, and the repair of a drifted live tag is then emergent -
// the provider's ordinary tags diff between what the configuration now
// declares and what the object carries. stamp itself never overwrites a
// marker (see its verify: "It never returns 'overwrite': there is no such
// verdict"), so there is no repair path inside it for a toggle to gate.
//
// Suppressing the repair therefore means suppressing that diff for the
// marker keys, which is what lifecycle { ignore_changes } does and what
// internal/live/lint's RuleIgnoreChanges refuses today. Lifting that refusal
// safely needs somewhere else for the identity to live, because a
// needs-discovery resource whose marker is neither written nor repaired is
// the "created unfindable" failure the safety rule exists to prevent - which
// is the per-type and per-address `markers = record` toggle, the next slice
// of #365.
//
// So the honest state is: the grammar is here, the unconditional mechanism
// is not, and a setting whose mechanism does not exist is refused loudly
// rather than accepted silently. A silent no-op would tell an operator their
// estate's tags are not being touched while the ordinary plan carried on
// touching them, which is precisely the false "you are fine" this fork keeps
// finding in itself.
//
// [Never] is where the paragraph above stopped being the whole story, and it
// is worth being exact about why, because "the bar moved" and "the mechanism
// arrived" look the same from a distance. Nothing changed about repair. What
// arrived is the OTHER identity source that paragraph names as the
// precondition: the per-type and per-address markers = record selection. A
// resource holding its identity in the estate's record store has no marker to
// repair and no marker to lose, so ignoring its tags costs nothing - and
// ignoring its tags is the entire observable content of "never". See
// [ImplementedWithSelection], which is the predicate that says so.
func Implemented(v MarkerRepair) bool {
	return markerRepairs[v].always
}

// ImplementedWithSelection reports whether a build acts on v for a
// configuration whose strict block carries a non-empty markers "record"
// selection.
//
// It is a superset of [Implemented] and differs on exactly one setting,
// [Never]. See that constant for why the selection is what gives it a
// mechanism.
//
// # The half it does not cover, and why that is not a silent no-op
//
// A selection covers some resources and not others, so "never" honoured
// through it reaches some resources and not others - which would be the
// misleading half-truth [Implemented] exists to refuse, if an operator could
// only find the boundary by reading this comment. They cannot miss it:
// "never"'s whole effect is that internal/live/lint stops refusing
// lifecycle { ignore_changes } over the marker tags, and it stops refusing it
// per resource, only for the ones the selection covers. An estate-wide
// "never" therefore meets its limit at the first resource the selection does
// not reach, as a refusal naming that resource, on the first run.
func ImplementedWithSelection(v MarkerRepair) bool {
	return markerRepairs[v].withSelection
}

// Names renders the whole vocabulary for a diagnostic, sorted so the message
// is stable: `"never", "repair", "report"`.
func Names() string {
	out := make([]string, 0, len(markerRepairs))
	for v := range markerRepairs {
		out = append(out, `"`+string(v)+`"`)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// ImplementedNames renders just the settings a build acts on unconditionally,
// in the same shape [Names] uses.
func ImplementedNames() string {
	out := make([]string, 0, len(markerRepairs))
	for v, s := range markerRepairs {
		if s.always {
			out = append(out, `"`+string(v)+`"`)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
