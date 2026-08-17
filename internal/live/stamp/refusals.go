// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import "sort"

// This file is GitHub issue #110 applied to section C of #101's inventory,
// the stamping pass.
//
// The issue's first acceptance criterion is every hard refusal in the live
// path, not only the two passes internal/live/check can run. Stamping and
// discovery are the two it cannot: both need a cloud, so no corpus run will
// ever rank them, and that is precisely the argument for writing them down.
// A refusal an instrument can measure gets found eventually. One it cannot
// is only ever met by a user in the middle of a migration.
//
// The registry is small - seven summaries - and its value is not the size.
// It is that live/LIMITATIONS.md's generated section can no longer describe
// only the refusals that happen to be cheap to measure.
//
// Three of the seven were added after an audit: they reach the user through
// tfdiags.Sourceless, which the first version of internal/live/refusalscan
// could not see, so the test guarding this registry passed over them in
// silence.

// Refusal is one thing this package can refuse, keyed by the Summary its
// diagnostic carries. Same three fields internal/live/identity's registry
// has, so internal/live/check can fold them into one table.
type Refusal struct {
	// Summary is the hcl.Diagnostic Summary, and this refusal's identity.
	Summary string

	// What is a one-line description of the situation that triggers it, in
	// the voice live/LIMITATIONS.md's entries use.
	What string

	// Doc overrides where it is documented. Empty means the generated
	// entry under its own Summary; see identity.Refusal.Doc.
	Doc string
}

// DocsRef is where a user is sent to read about this refusal.
func (r Refusal) DocsRef() string {
	if r.Doc != "" {
		return r.Doc
	}
	return `live/LIMITATIONS.md, "` + r.Summary + `"`
}

// refusals is the registry. Keep it sorted by Summary.
//
// The first two describe an ownership tag the configuration already sets by
// hand, disagreeing with what this run would write or unreadable to it. That
// is the shape of most stamping trouble: the configuration and the live
// object each hold an opinion about ownership, and this pass will not
// silently pick one.
//
// The last three are two warnings and the error they become. [stamper.unstampableAt]
// picks between them on whether the resource can be found any other way, and
// it swaps the summary along with the severity - so a user who saw the error
// looks it up under its own heading, not under either warning's.
var refusals = []Refusal{
	{
		Summary: SummaryMarkerConflict,
		What:    "The configuration already sets an ownership tag by hand, to a value other than the one this estate's markers require. Overwriting it would move ownership of a live resource without anyone saying so, so the run stops instead.",
	},
	{
		Summary: SummaryMarkerUncheckable,
		What:    "An ownership tag is already set in the configuration to an expression this run cannot evaluate, so whether it agrees with this estate's markers is unknown. A warning: a resource that can only be found by its marker gets the error below instead, under its own heading, because [stamper.unstampableAt] swaps the summary as well as the severity.",
	},
	{
		Summary: SummaryNotStamped,
		What:    "A resource's tags could not be given this estate's ownership markers - most often an untaggable type, or a tags argument this pass cannot append to. Reported as a warning, because the resource is still identifiable from its configuration. Also the form a marker-only resource takes when this run could not read its type's schema at all: whether it can carry a marker is then unknown rather than known to be impossible, and an unknown is never reported as the error below. A third case is a type that HAS a settable tags map whose documented vocabulary an ownership marker cannot be spelled in - a key space the provider defines rather than the configuration, or a character set and length the escaped address does not fit. GCP's resource-manager tag bindings are the found example: keys must name TagKey objects that already exist, and on several types the field forces replacement when mutated. A type with no tag surface at all stays silent, because being identified by an argument instead of a marker is ordinary and hundreds of types are; a tag surface that exists and cannot be used is not, so it is said out loud.",
	},
	{
		Summary: SummaryNoConfig,
		What:    "Stamping was given no configuration to rewrite. A caller error, not a configuration one.",
	},
	{
		Summary: SummaryNoEstateName,
		What:    "Stamping was given no estate name, or one outside the tofu-estate marker grammar, so there is no value to write into the markers.",
	},
	{
		Summary: SummaryNoSchemas,
		What:    "Stamping was given no provider schemas, so which types can carry a marker cannot be read. A caller error, not a configuration one.",
	},
	{
		Summary: SummarySharedBody,
		What:    "Two resources in the configuration reached one HCL body, so the ownership marker written for one of them would be the marker the other carries too. A module source called more than once is parsed once - every call shares the syntax tree - and each call is supposed to get its own body for a resource's arguments; this fires when one did not. It is a defect in how the run loaded the configuration rather than a fault in the configuration, and it is a hard error because a marker shared between two live objects is worse than no marker at all.",
	},
	{
		Summary: SummaryUnmarkedApply,
		What:    "Markers could not be written, on a resource whose instances can only ever be found by their ownership marker. It is the error form of the two warnings above - \"Ownership markers not stamped\" and \"Ownership marker could not be checked\" - because applying this one unmarked would create a live object no later run could recognise as this estate's.",
	},
}

// Refusals returns every refusal this package can produce, sorted by Summary.
func Refusals() []Refusal {
	out := make([]Refusal, len(refusals))
	copy(out, refusals)
	sort.Slice(out, func(i, j int) bool { return out[i].Summary < out[j].Summary })
	return out
}

// LookupRefusal returns the registry entry for a diagnostic Summary.
func LookupRefusal(summary string) (Refusal, bool) {
	for _, r := range refusals {
		if r.Summary == summary {
			return r, true
		}
	}
	return Refusal{}, false
}
