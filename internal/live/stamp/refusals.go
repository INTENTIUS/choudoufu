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
// The registry is small - four summaries - and its value is not the size.
// It is that live/LIMITATIONS.md's generated section can no longer describe
// only the refusals that happen to be cheap to measure.

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
// silently pick one. The last two are one failure at two severities, which
// [stamper.unstampableAt] decides from whether the resource can be found any
// other way.
var refusals = []Refusal{
	{
		Summary: SummaryMarkerConflict,
		What:    "The configuration already sets an ownership tag by hand, to a value other than the one this estate's markers require. Overwriting it would move ownership of a live resource without anyone saying so, so the run stops instead.",
	},
	{
		Summary: SummaryMarkerUncheckable,
		What:    "An ownership tag is already set in the configuration to an expression this run cannot evaluate, so whether it agrees with this estate's markers is unknown. A warning for a resource that can be found another way; an error for one that can only be found by its marker.",
	},
	{
		Summary: SummaryNotStamped,
		What:    "A resource's tags could not be given this estate's ownership markers - most often an untaggable type, or a tags argument this pass cannot append to. Reported as a warning, because the resource is still identifiable from its configuration.",
	},
	{
		Summary: SummaryUnmarkedApply,
		What:    "The same failure as the entries above, on a resource whose instances can only ever be found by their ownership marker. Applying it unmarked would create a live object no later run could recognise as this estate's, so this one is an error rather than a warning.",
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
