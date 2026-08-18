// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import "sort"

// This file completes GitHub issue #110's first acceptance criterion: every
// hard refusal in the live path, not four of the five passes.
//
// Projection was the one left out, and the omission was written down as a
// fact - commit c26bfcde2 said "stamping and discovery are the other two"
// when internal/live/check's own UncheckedLayers returns three. An audit
// counted twenty-six diagnostics here with no registry at all, which made
// AllRefusals smaller than its doc comment claimed and left the criterion
// unmet while every test passed.
//
// Projection is the stage that turns what discovery bound into prior state:
// it imports or reads each live object through the provider, hydrates
// record-backed effects from the record store, and encodes the result. Its
// refusals are mostly about that machinery failing rather than about the
// configuration being outside the subset, which is why they read differently
// from lint's or identity's. They are still refusals a user sees and has to
// act on, and until now none of them could be looked up.

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
		Summary: "Argument values could not be recorded",
		What:    "An apply could not classify or store the argument values a provider's read never gives back (GitHub issue #275) - no provider access, a failing read, or a store that refused the write. Nothing in the live system changed; the arguments involved will be proposed for update again on the next plan.",
	},
	{
		Summary: "Cannot decode a persisted record",
		What:    "A record read from the record store could not be decoded into the type it describes - a record written by a different version of this tool, or one edited by hand.",
	},
	{
		Summary: "Cannot encode a projected object",
		What:    "A live object read from the cloud could not be encoded against the provider's schema for its type.",
	},
	{
		Summary: "Cannot import for projection",
		What:    "The provider refused the import this projection needed to read a resource's current state.",
	},
	{
		Summary: "Cannot list the record store",
		What:    "The record store could not be listed, so record-backed resources whose configuration block was removed cannot be found.",
	},
	{
		Summary: "Cannot persist a record",
		What:    "Writing a record for an effect back to the record store failed.",
	},
	{
		Summary: "Cannot read a parent's identity from the projection",
		What:    "A resource whose identity is derived from its parent's could not read that parent, because the parent is not in this projection.",
	},
	{
		Summary: "Cannot read a persisted record",
		What:    "The record store could not be read.",
	},
	{
		Summary: "Cannot read for projection",
		What:    "The provider refused the read this projection needed to fill in a resource's current state.",
	},
	{
		Summary: "Could not write the discovery hint",
		What:    "Guided discovery's plan-cost hint could not be written to the estate's record store, so the next run pays a full estate sweep instead of a narrowed one.",
	},
	{
		Summary: "Cyclic parent-derived identities",
		What:    "Two or more resources derive their identities from each other, directly or transitively, so none of them can be built first.",
	},
	{
		Summary: "Empty import identity",
		What:    "A resource resolved to an import identity with no content, which no provider can import.",
	},
	{
		Summary: "Ignoring an additional imported object",
		What:    "An import returned more than one object where one was expected; the extra objects are dropped and this says so rather than choosing silently.",
	},
	{
		Summary: "Live resource marked for another address",
		What:    "A live object at the identity a declared instance names carries this estate's marker under a different resource address, or under no address at all, so it is another instance's object (or a malformed marker) and is not projected.",
	},
	{
		Summary: "Live resource outside this estate",
		What:    "A live object bound by discovery carries an estate marker other than this run's, so it belongs to a different estate and is not projected.",
	},
	{
		Summary: "No configuration to project",
		What:    "Projection was given no configuration. A caller error, not a configuration one.",
	},
	{
		Summary: "No identity resolutions to project",
		What:    "Projection was given no identity resolutions to build from. A caller error, not a configuration one.",
	},
	{
		Summary: "No provider access",
		What:    "Projection was given no configured provider handle to read live state with. A caller error, not a configuration one.",
	},
	{
		Summary: "No provider for an undeclared resource",
		What:    "An owned-but-undeclared resource was found whose provider this run has no handle for, so its current state cannot be read. Reported as a warning: the sweep still knows the resource exists.",
	},
	{
		Summary: "No state returned by the provider",
		What:    "A provider read or import returned no object at all, so there is nothing to project for that resource.",
	},
	{
		Summary: "Parent-derived identity with no formula",
		What:    "A resource's identity is meant to be derived from its parent's, and the identity table carries no formula saying how.",
	},
	{
		Summary: "Persisted record does not match the current schema",
		What:    "A record in the record store was written against a different schema for its type than the installed provider now offers.",
	},
	{
		Summary: "Provider produced an invalid object",
		What:    "A provider returned an object that does not conform to its own declared schema.",
	},
	{
		Summary: "Provider unavailable",
		What:    "The provider configuration a resource needs could not be started or configured.",
	},
	{
		Summary: "Record store write conflict",
		What:    "Two runs wrote the same record concurrently, so this run's write was rejected rather than overwriting the other's.",
	},
	{
		Summary: "Cannot record a located identity",
		What:    "An applied resource whose live object carries no ownership marker had no identity that could be written to the record store, so no later run could find it again and the next plan would propose creating a second one.",
	},
	{
		Summary: "Cannot read a located record",
		What:    "The record saying which live object a markerless resource owns could not be read: the store failed, the payload did not decode, or it names a different resource address. Reading on would bind the instance to another object's identity.",
	},
	{
		Summary: "Record-backed instance with no record store",
		What:    "An effect resource that keeps its whole state in a record was projected with no record_store configured, so there is nowhere to read its prior state from.",
	},
	{
		Summary: SummaryLocatedNoStore,
		What:    "A resource whose live object can carry no ownership marker was projected with no record_store configured, so nothing can say which live object it is. Declaring a record_store in the live block is the fix.",
	},
	{
		Summary: SummaryResidueUnreadable,
		What:    "An estate's residue record - the argument values an earlier apply sent that the provider's read never gives back (GitHub issue #275) - exists but could not be used: the store failed, the payload did not decode, or it names a different resource address. The plan continues from what the provider returned, so those arguments are proposed for update again.",
	},
	{
		Summary: "Resolved instance missing from the configuration",
		What:    "Projection was handed a resolution for an address the configuration does not declare. The resolutions and the configuration came from different runs; a bug in whatever assembled them.",
	},
	{
		Summary: "Unsupported resource type for the provider",
		What:    "A resource's type is not one the configured provider serves.",
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
