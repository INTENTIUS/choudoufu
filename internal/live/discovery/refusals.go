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
		Summary: "Cannot list the record store",
		What:    "The record-orphan-read leg (issue #364 ruling item 1) could not list the estate's record store to find untaggable resources whose configuration block was removed - an unreachable store, or a permissions problem underneath it.",
	},
	{
		Summary: "Cloud Control identifier could not be composed",
		What:    "A live resource was listed, but the primary identifier Cloud Control needs to describe it could not be assembled from what the list returned.",
	},
	{
		Summary: "Content match found more than one live candidate",
		What:    "A declared instance of a type with no tags argument (issue #272) has more than one live object carrying the same value its own identity-bearing argument names, so content match cannot tell which one is this instance's. Binding either would risk adopting the other's resource, so none was bound.",
	},
	{
		Summary: "Cross-type marker on an undeclared type",
		What:    "The estate-wide sweep found a live resource of a type this configuration declares no instance of, carrying this estate's ownership marker for an address of another type - ordinarily a tag AWS copied from a marked resource onto a dependent object it created for it. A warning: nothing in the run binds it, destroys it or retags it.",
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
		Summary: "Listed resource matched more than one tagged resource",
		What:    "A live resource was listed with no ownership marker of its own, and its identifier matched more than one resource in the estate's tag index whose marker names this very type. Attaching either one's tags would risk adopting the other's resource, so none was attached.",
	},
	{
		Summary: "Listed resource with no readable name",
		What:    "A live resource of a type this fork recognises by its account-unique name was listed with no readable name at the property the CloudFormation schema says carries it, so it cannot be compared against the configuration - and the type has no tags argument to fall back on.",
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
		Summary: "Live resource displaced from the address it is marked for",
		What:    "A live resource carries this estate's marker for an address the configuration still declares, but the identity that address resolves to names a different live resource - so two resources answer to one address. Nothing is proposed for it; a human says which is which.",
	},
	{
		Summary: "Located identity record unreadable",
		What:    "A type with no tags argument and no list route of any kind can only be found again through the estate's record store, and reading its stored identity for one declared instance failed - a corrupt record, an unreachable store, or a record format this build does not understand.",
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
		Summary: "Marked resource abandoned by a provider configuration change",
		What:    "The same situation the refusal above names, under `strict { provider_change = \"recreate\" }`: the operator has said in the configuration that the old provider configuration's object is to be left behind. A warning, and the only notice there will ever be - the object stays live, keeps this estate's markers, and no plan will propose anything for it.",
		Doc:     `live/LIMITATIONS.md, "strict-provider-change"`,
	},
	{
		Summary: "Marked resource outside its address's provider configuration",
		What:    "A live resource carries this estate's marker for an address the configuration declares under a provider configuration that never listed it, and only passes that address does not belong to found it - a region or account change that left the old region's object behind. Proceeding would create a second live resource carrying one address's marker, so the plan refuses instead.",
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
		Summary: "Unbound instance with unreadable live markers of its type",
		What:    "A declared instance bound to nothing, so the plan proposes creating it, while the run listed live resources of its type whose ownership markers it could not read. One of them may be this instance's own resource, in which case applying creates a duplicate carrying the same marker instead of adopting it.",
	},
	{
		Summary: "Unique name matched more than one resource",
		What:    "A resource type recognised by a name AWS documents as unique per account and region turned out not to match one thing: either several live resources carry the declared name, or several declared instances state it. Binding on either would be a guess, so nothing was bound.",
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

// SeverityForRefusal is the severity of the diagnostic this package raises
// under one registry Summary, so that a report about refusals can tell a
// blocker from a note.
//
// [Refusal] itself carries no severity field, and adding one would have
// meant hand-writing sixteen answers next to sixteen entries with nothing
// checking them against the code. This reads them off instead: a Summary
// that belongs to a [ProblemKind] answers with that kind's own
// [ProblemKind.Severity], which is the switch [problemDiag] uses to build
// the diagnostic. It is the same call, not a parallel copy of it -
// problemDiag and sweepGapDiag both route their severity through this
// function, so a wrong answer here is a wrong diagnostic rather than a
// wrong document.
//
// Everything else is an error: the caller errors (a request with no estate
// name, no configuration or no provider handle) and the marker-trouble
// refusals (an address too long to carry, two addresses escaping to one
// value) all stop the run. TestEveryRegisteredRefusalHasAStatedSeverity is
// what keeps that from being an assumption - a Summary added to the registry
// that is neither a problem kind's nor the sweep gap's has to be accounted
// for there before it can silently render as a blocker.
func SeverityForRefusal(summary string) Severity {
	if kind, ok := problemKindForSummary(summary); ok {
		return kind.Severity()
	}
	if summary == SummaryIncompleteSweep {
		// A gap in removal coverage, never a wrong plan: the run in front
		// of the operator is correct and simply did not see everything.
		return SeverityWarning
	}
	return SeverityError
}

// problemKindForSummary is [problemSummaries] read backwards. The map is
// injective - TestProblemSummariesAreDistinct - so the reverse lookup is
// well defined rather than dependent on Go's map order.
func problemKindForSummary(summary string) (ProblemKind, bool) {
	for kind, s := range problemSummaries {
		if s == summary {
			return kind, true
		}
	}
	return "", false
}
