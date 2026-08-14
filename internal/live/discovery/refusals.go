// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import "sort"

// This file is GitHub issue #110 applied to section D of #101's inventory,
// the discovery pass. See internal/live/stamp/refusals.go for why a pass no
// corpus run can measure is the one most worth writing down.
//
// The first version of this registry held two entries and claimed they were
// "the whole of this pass's own refusal surface". An adversarial audit
// counted twenty-one. The two it had were the two built as hcl.Diagnostic
// literals; everything else reaches the user through tfdiags.Sourceless or
// through [problemSummaries], and the scanner meant to enforce the registry
// could see neither - so it reported that everything was registered because
// it could see almost nothing. internal/live/refusalscan is strict now, and
// this is what it found.
//
// Three groups, and they fail for different reasons:
//
//   - Caller errors. A discovery request missing its estate name, its
//     configuration or its provider handle. A user never causes one; they
//     are here because they are diagnostics, and a registry that held only
//     the interesting ones would be a curated list rather than a complete
//     one.
//   - Marker trouble. An address that will not fit a tag, two addresses
//     escaping to one marker value, a marker that does not parse. These are
//     the pass's own subject matter.
//   - Problems, from [problemSummaries]. Fourteen findings about what was
//     listed in the cloud: a resource with no identity, a type that cannot be
//     listed, a slot exhausted. Several are warnings. They are refusals in
//     the sense that matters here - a user sees them and has to act - and
//     they carry the same documentation obligation.

// Refusal is one thing this package can refuse, keyed by the Summary its
// diagnostic carries.
type Refusal struct {
	// Summary is the hcl.Diagnostic Summary, and this refusal's identity.
	Summary string

	// What is a one-line description of the situation that triggers it.
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
var refusals = []Refusal{
	{
		Summary: "Address too long to carry an ownership marker",
		What:    "A resource instance's address, escaped for a tag value, is longer than the tofu-address tag and its continuations can hold, so no live object can carry it.",
		Doc:     `live/LIMITATIONS.md, "overlong-address"`,
	},
	{
		Summary: "Cloud Control identifier could not be composed",
		What:    "A live resource was listed, but the primary identifier Cloud Control needs to describe it could not be assembled from what the list returned.",
	},
	{
		Summary: "Failed to list a resource type",
		What:    "Listing one resource type failed - most often a permission the run does not have, or a service not available in the region. Discovery continues with the types it could list, so an estate spanning that type is only partly seen.",
	},
	{
		Summary: "Incomplete sweep for undeclared resources",
		What:    "The estate-wide sweep could not cover every admitted type, so an owned-but-undeclared resource may exist that this run did not find. A removal plan built on it is not a complete reconciliation.",
	},
	{
		Summary: "Indistinguishable instances without per-instance markers",
		What:    "Several live resources carry one address marker for a count-expanded or for_each-expanded block, with no tofu-slot marker to tell them apart, so which instance is which cannot be decided.",
	},
	{
		Summary: "Invalid estate name",
		What:    "The estate name does not match the tofu-estate marker grammar (a lowercase letter, then letters, digits or hyphens, at most 128 characters).",
	},
	{
		Summary: "Listed resource with no identity",
		What:    "A live resource carries this estate's markers but the listing returned nothing that identifies it, so it cannot be bound to a configuration address.",
	},
	{
		Summary: "Listed resource with no tags",
		What:    "A live resource was listed with no tags at all where markers were expected, so ownership cannot be read from it.",
	},
	{
		Summary: "Malformed ownership marker",
		What:    "A live resource carries a tofu-address or tofu-estate tag whose value is not in the marker grammar - hand-edited, truncated, or written by something other than this tool.",
	},
	{
		Summary: "Malformed slot marker",
		What:    "A live resource's tofu-slot tag is not a slot value this run can read.",
	},
	{
		Summary: "No AWS account ID from the provider",
		What:    "The account this run is against could not be resolved, so identities embedding the account cannot be computed and marker discovery has to stand in for them.",
	},
	{
		Summary: "No configuration to discover against",
		What:    "Discovery was given no configuration to match markers against. A caller error, not a configuration one.",
	},
	{
		Summary: "No provider access",
		What:    "Discovery was given no configured provider handle to list live resources with. A caller error, not a configuration one.",
	},
	{
		Summary: "No slot left to mint",
		What:    "Every slot value for a fungible set is taken, so a new instance has nothing to be marked with.",
	},
	{
		Summary: "One marker value for two declared addresses",
		What:    "Two declared instances escape to the same tofu-address value, so a marker cannot say which of them a live object belongs to. Binding either would be a guess.",
	},
	{
		Summary: "Owned resource of a type the sweep cannot cover",
		What:    "A live resource carries this estate's ownership marker, the configuration no longer declares it, and its type is outside the sweep's universe - admitted by the provider's identity schema rather than by the generated admission table. It is not planned for destruction and no later run will propose one.",
	},
	{
		Summary: "Partial slot markers on a count set",
		What:    "Some instances of a count-expanded resource carry tofu-slot markers and some do not, so the set cannot be read either as slotted or as positional.",
	},
	{
		Summary: "Resolved resource missing from the configuration",
		What:    "Discovery was asked to find a resource the configuration it was given does not declare. The resolutions and the configuration came from different runs; a bug in whatever assembled them, not in the configuration.",
	},
	{
		Summary: "Tagged resource's ARN could not be joined to a resource type",
		What:    "A resource carrying this estate's markers was found by tag, but its ARN does not map to a resource type this run knows, so nothing further can be read about it.",
	},
	{
		Summary: SummaryUnclassifiedProblem,
		What:    "Discovery reported a problem whose kind this package has no summary for. A gap in this package rather than anything the configuration did; the kind is named in the detail.",
	},
	{
		Summary: "Unlistable marker-discovered type",
		What:    "A type that can only be found by its ownership marker has no listing this run can perform, so resources of that type cannot be discovered at all.",
	},
	{
		Summary: "Unscoped account reconciliation refused",
		What:    "Account reconciliation was asked to run with no scope narrowing what it may reach. Refused here as well as at lint time, so a caller that skipped lint does not get an account-wide purge.",
		Doc:     `live/LIMITATIONS.md, "policy-scope"`,
	},
	{
		Summary: "Two live resources claiming one address",
		What:    "Two live resources carry the same tofu-address marker, so both claim one configuration address. Binding either would be a guess.",
	},
	{
		Summary: "Two live resources claiming one slot",
		What:    "Two live resources carry the same tofu-slot marker within one fungible set.",
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
