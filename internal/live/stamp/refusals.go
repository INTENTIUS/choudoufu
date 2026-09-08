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
// GitHub issue #644 took the registry from eight entries to three. The five
// that went were the ones only the HCL-rewriting engine in this package
// could produce, and the engine is gone:
//
//   - SummaryNoConfig, SummaryNoEstateName, SummaryNoSchemas were guards on
//     that engine's own Request struct - a nil configuration, an estate name
//     outside the marker grammar, nil provider schemas. There is no Request
//     any more. Zero corpus sites at every measurement this project ever
//     took, because they were caller errors and not configuration shapes.
//   - SummarySharedBody was a diagnostic about two resource blocks reaching
//     one *hclsyntax.Body during that rewrite (#280). The node path is
//     called once per already-concrete addrs.AbsResourceInstance with its
//     own evaluated value and never rewrites a body at all, so the failure
//     mode is structurally absent rather than merely unmeasured.
//   - SummaryMarkerUncheckable said "an ownership tag is already set to an
//     expression this run cannot evaluate". That was a property of reading
//     HCL text statically. The node path receives the tag ALREADY
//     EVALUATED, so an expression it could not read does not exist there;
//     a value that disagrees is SummaryMarkerConflict below, and one that
//     agrees is a no-op.
//
// The three that stay are the three a run still raises, all of them from
// outside this package now: internal/live/check's node-path port
// (nodestamp.go, GitHub issue #454) for the two marker-only summaries, and
// internal/live/projection's own SummaryMarkerConflict, which carries the
// identical text by construction so that one registry entry documents both
// seams. The registry stays here, under RaisedByStamp, because that is the
// LAYER a reader of live/LIMITATIONS.md is looking the refusal up under.

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
// The first describes an ownership tag the configuration already sets by
// hand to a value other than the one this run resolved: the configuration
// and the live object each hold an opinion about ownership, and no seam
// here will silently pick one. The last two are the marker-only pair - a
// warning for a resource that can still be found another way, and the
// error it becomes for one that cannot.
var refusals = []Refusal{
	{
		Summary: SummaryMarkerConflict,
		What:    "The configuration already sets an ownership tag by hand, to a value other than the one this estate's markers require. Overwriting it would move ownership of a live resource without anyone saying so, so the run stops instead.",
	},
	{
		Summary: SummaryNotStamped,
		What:    "A resource's tags could not be given this estate's ownership markers - most often an untaggable type. Reported as a warning, because the resource is still identifiable from its configuration. Also the form a marker-only resource takes when this run could not read its type's schema at all: whether it can carry a marker is then unknown rather than known to be impossible, and an unknown is never reported as the error below. A third case is a type that HAS a settable tags map whose documented vocabulary an ownership marker cannot be spelled in - a key space the provider defines rather than the configuration, or a character set and length the escaped address does not fit. GCP's resource-manager tag bindings are the found example: keys must name TagKey objects that already exist, and on several types the field forces replacement when mutated. A type with no tag surface at all stays silent, because being identified by an argument instead of a marker is ordinary and hundreds of types are; a tag surface that exists and cannot be used is not, so it is said out loud.",
	},
	{
		Summary: SummaryUnmarkedApply,
		What:    "Markers could not be written, on a resource whose instances can only ever be found by their ownership marker. It is the error form of the warning above - \"Ownership markers not stamped\" - because applying this one unmarked would create a live object no later run could recognise as this estate's. Fires at two seams that answer the identical question from the same schema predicate (markers.Taggable): as a plan-time error, before a plan is ever approved (internal/command's live_plan.go and live_mode.go - GitHub issue #950, the node-path equivalent of the HCL-rewriting stamp's own plan-time refusal, which issue #644/#944 retired with no replacement until #950 restored one), and as this same finding in the offline `choudoufu live-check` report (internal/live/check's NodeStampUnmarkedApply, issue #454's port), for a configuration nobody has planned yet. A needs-discovery instance whose estate record already holds an identity (issue #364) is exempt from the plan-time form.",
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
