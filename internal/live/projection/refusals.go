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
		Summary: "Cannot encode a deposed object",
		What:    "GitHub issue #361's crash-window recovery read a deposed object discovery matched against this estate's record, but the result could not be encoded against the provider's schema for its type; the deposed object is left recorded but not folded into this plan.",
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
		Summary: "Cannot merge ownership markers into this tags value",
		What:    "GitHub issue #388's node-path stamp (NodeResolver.AdjustConfigValue) found a tags argument it does not know how to add the two ownership markers into - a non-map value, a map of something other than strings, or a map holding a non-string element - so it left the resource's configuration value exactly as evaluated.",
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
		Summary: "Cannot read a recorded deposed object",
		What:    "GitHub issue #361's crash-window recovery could not read, live, a deposed object discovery matched against this estate's record - the provider errored. The deposed object is left recorded but not folded into this plan; a later run tries again.",
	},
	{
		Summary: "Cannot read for projection",
		What:    "The provider refused the read this projection needed to fill in a resource's current state.",
	},
	{
		Summary: "Cannot set ownership markers on a marked configuration value",
		What:    "GitHub issue #388's node-path stamp found a resource instance's whole evaluated configuration value marked as sensitive, a shape ordinary block evaluation does not produce, and declined to unmark it rather than guess; the resource's ownership markers were left for the HCL-level stamp (or an operator) to write.",
	},
	{
		Summary: "Cannot set ownership markers on an unresolved tags value",
		What:    "GitHub issue #388's node-path stamp found a resource's tags argument not yet known at plan time, so it could not merge the two ownership markers into it; the resource's configuration value is used unchanged.",
	},
	{
		Summary: "Could not write the discovery hint",
		What:    "Guided discovery's plan-cost hint could not be written to the estate's record store, so the next run pays a full estate sweep instead of a narrowed one.",
	},
	{
		Summary: "Could not write the state cache",
		What:    "GitHub issue #685's state cache could not be written (default: choudoufu-cache.tfstate under the data dir; CHOUDOUFU_STATE_CACHE overrides the path, the value off disables), so the next plan rebuilds prior state from live reads instead of starting from the cache. This costs API calls and not correctness: a cached entry is a candidate verified against the tag index, never a fact trusted, so an absent cache is the same as a stale one.",
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
		Summary: "Import reported absence as an error",
		What:    "The provider's ImportResourceState call for a resource failed with a diagnostic shaped like a generic not-found response (terraform-plugin-sdk's retry.NotFoundError default message, or the raw AWS ResourceNotFoundException code) rather than an empty ImportedResources list. Treated as an ordinary absence, the same as an empty list or a null read result, not a provider failure.",
	},
	{
		Summary: SummaryListedNotImportable,
		What:    "GitHub issue #596: the provider's own list call returned a live object carrying this estate's tofu-estate marker and a tofu-address marker naming a declared instance, and the provider then answered that nothing exists at the identity that same listing served for it. The two answers contradict each other, so the plan refuses rather than propose creating a duplicate of the object it just listed. A list-served identity is used as the discriminator precisely because a tagging-API sighting is not proof of existence - deleted objects linger in the tag index - so an instance whose only sighting was the tag index still takes the ordinary ABSENT path and a genuine rebuild is never blocked.",
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
		Summary: "No provider for a deposed object",
		What:    "GitHub issue #361's crash-window recovery matched a deposed object against this estate's record, but neither the record nor the current resource block names a provider to read it through. The deposed object is left recorded but unread.",
	},
	{
		Summary: "No provider for an undeclared resource",
		What:    "An owned-but-undeclared resource was found whose provider this run has no handle for, so its current state cannot be read. Reported as a warning: the sweep still knows the resource exists.",
	},
	{
		Summary: "No source for this instance's identity",
		What:    "GitHub issue #388's plan-node seam ([NodeResolver], ruling 4/#365) found no record, no live marker, and no identity this run could derive from the instance's own evaluated configuration. Refused by default, because nothing here can tell a genuinely new instance apart from a real one this run simply cannot see yet; strict { no_source_create = \"create\" } selects stock's own behavior (plan a create) instead.",
	},
	{
		Summary: "No state returned by the provider",
		What:    "A provider read or import returned no object at all, so there is nothing to project for that resource.",
	},
	{
		Summary: SummaryMarkerConflict,
		What:    "GitHub issue #451's node-path stamp (NodeResolver.AdjustConfigValue) found a resource instance's own configuration already declaring a tofu-estate or tofu-address tag that names a different estate or address than this run resolved. A plan never overwrites a marker naming another estate or address: fix the tag, or - for an address conflict - run live-mv. Ports internal/live/stamp's own SummaryMarkerConflict refusal (stamp/summaries.go) to the node path, with matched text.",
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
		Summary: SummaryStaleRecord,
		What:    "GitHub issue #364 unit B's universal record-first read found an object through the estate's record store, but the object's own tofu-address marker does not confirm it - it names a different address, or (for a taggable type) there is no address marker at all. The record is treated as absent rather than as a claim to defend: the instance falls back to marker discovery or static derivation, exactly as if no record existed for it. Always a warning; the plan that follows is correct either way.",
	},
	{
		Summary: "Record-backed instance with no record store",
		What:    "An effect resource that keeps its whole state in a record was projected against an estate with no record store at all - possible only with no live block, since GitHub issue #364 every live block implies one - so there is nowhere to read its prior state from.",
	},
	{
		Summary: SummaryLocatedNoStore,
		What:    "A resource whose live object can carry no ownership marker was projected against an estate with no record store at all - possible only with no live block, since GitHub issue #364 every live block implies one - so nothing can say which live object it is. Adding a live block is the fix, not declaring a record_store: one with no record_store block of its own already gets an implied local store.",
	},
	{
		Summary: SummaryLocatedIdentityNotRecorded,
		What:    "A migration (liveimport's Approve) read the identity of an untaggable, unlistable resource but could not write it into the estate's record store: a write conflict with a different identity already there, or a store failure. The instance stays findable only by hand until this is resolved; nothing in the live system changed.",
	},
	{
		Summary: SummaryProvisionedUnreadable,
		What:    "An estate's provisioner record - the one bit saying a create-time provisioner failed on a live object (GitHub issue #353) - exists but could not be used: the store failed, the payload did not decode, or it names a different resource address. Reading on would report a half-provisioned object as healthy and never run the provisioner again.",
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
		Summary: "Resource type has no classic Importer",
		What:    "A resource type projection needed to read back has no ImportResourceState implementation at all - a fixed property of the provider's own code (GitHub issue #331), not a transient failure. Admitted for naming and reference purposes only; refused here rather than risk proposing a create for an object this run cannot verify.",
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
