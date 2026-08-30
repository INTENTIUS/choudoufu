// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/states"
)

// Result is what [Build] produces: the projection itself, plus the record
// of everything that is not in it and why.
//
// The roadmap sketches Build as returning a bare *states.State. It returns
// this instead because the omissions are not debris: "this instance is
// absent from the projection, and here is the reason" is the information a
// caller needs to explain a plan that proposes creating something, and
// P2's discovery pass needs the same list to know what to go looking for.
// Callers that only want the state can use [Result.State].
type Result struct {
	// State is the projection: an in-memory prior state holding one object
	// per instance that was materialized. It is never nil, and is never
	// written anywhere by this package.
	State *states.State

	// Materialized lists the instances present in State, in address order.
	Materialized []addrs.AbsResourceInstance

	// Omitted lists the instances that are not in State, in address order,
	// each with a machine-readable reason and an operator-facing sentence.
	Omitted []Omission

	// Unowned lists the live objects this pass found at a declared identity
	// and refused to admit, because they carry no ownership marker for this
	// estate. Each one also appears in Omitted with [ReasonUnowned]; this is
	// the same set with the live resource's own details on it, for a caller
	// that wants to say "here is what is in the way" rather than "here is
	// what is missing".
	Unowned []Unowned

	// RecordVersions lists the store version read at plan time for every
	// GitHub issue #73 record-backed instance whose record actually
	// existed, in address order. Write-back (WriteBack) uses this as the
	// expected version for each PutIfVersion/Delete call: an instance with
	// no entry here had no prior record, so write-back opens with
	// expectedVersion "" - the store's own create/absence convention.
	RecordVersions []RecordVersion

	// EnvelopeVersions lists the store version read at plan time for every
	// kind=identity instance whose envelope key actually existed, in
	// address order - GitHub issue #364's merge of what used to be three
	// separate lists (LocatedVersions, ResidueVersions,
	// ProvisionedVersions) for GitHub issues #270, #275 and #353.
	//
	// One list rather than three because the three concerns now share one
	// physical key per instance ([RecordKey]): an import identity, argument
	// residue and a provisioner taint bit for the SAME address live in the
	// same envelope, so a version read while resolving any one of them is a
	// version of the whole key, valid for a conditional write touching any
	// of the others. An instance with no entry here had no envelope at all
	// at plan time - it has never been created, its identity was lost
	// (deliberately indistinguishable, see [RecordStore.getIdentity]), or
	// nothing about it needed the store this run.
	EnvelopeVersions []RecordVersion

	// Policy lists every declared instance whose admission or tag handling
	// GitHub issue #67's policy governed with a verb other than that
	// quadrant's [policy.DefaultVerb] - so a run with no policy block, or
	// one that only ever names default verbs, always produces an empty
	// list here, which is what makes "omitted policy = byte-identical
	// current behavior" checkable directly against this field. See
	// [builder.checkPolicy].
	Policy []PolicyOutcome
}

// RecordVersion pairs one record-backed resource instance with the version
// its record carried when this projection read it.
type RecordVersion struct {
	Addr    addrs.AbsResourceInstance
	Version string
}

// DeposedBinding is GitHub issue #361's crash-window recovery output:
// discovery's collision-breaking branch names one live object, already
// found by an ordinary list-and-read pass, as the deposed object recorded
// for addr - not a fresh guess conjured from the record alone (see #361's
// design comment, section 4's safety argument). [BuildWith] folds each one
// into the constructed state's Instances[key].Deposed[dk], which is stock's
// own [states.Resource] shape: from there, stock's completely unmodified
// node_resource_deposed.go / transform_state.go graph machinery re-reads
// the object once more (ReadResource) and proposes its destroy - the
// second, independent verification the design's safety argument rests on.
type DeposedBinding struct {
	// Addr is the current (non-deposed) instance address the deposed
	// object shares, per record.go's own "same physical key" design.
	Addr addrs.AbsResourceInstance

	// DeposedKey is the deposed object's own key, read from the record.
	DeposedKey states.DeposedKey

	// ImportID / Components are the deposed object's own identity, exactly
	// as [LocatedRecord] carries a current object's.
	ImportID   string
	Components map[string]string

	// Provider is the deposed object's own managing provider
	// configuration, parsed from [DeposedRecord.Provider]. The zero value
	// (Provider.Type == "") means none was recorded or it failed to parse;
	// [BuildWith] then falls back to the current instance's own resolved
	// provider, the same rule an ordinary current-object binding uses.
	Provider addrs.AbsProviderConfig
}

// NewDeposedBinding builds a [DeposedBinding] from a record read through
// [RecordStore.GetDeposed] - deposedKey is that map's own string key,
// converted to [states.DeposedKey] here so a caller outside this package
// (discovery.go) never has to import internal/states purely to spell the
// conversion.
func NewDeposedBinding(addr addrs.AbsResourceInstance, deposedKey string, rec DeposedRecord) DeposedBinding {
	provider, diags := addrs.ParseAbsProviderConfigStr(rec.Provider)
	if diags.HasErrors() {
		provider = addrs.AbsProviderConfig{}
	}
	return DeposedBinding{
		Addr:       addr,
		DeposedKey: states.DeposedKey(deposedKey),
		ImportID:   rec.ImportID,
		Components: rec.Components,
		Provider:   provider,
	}
}

// PolicyOutcome is one declared instance whose admission or tag handling a
// non-default policy verb changed.
type PolicyOutcome struct {
	// Addr is the resource instance.
	Addr addrs.AbsResourceInstance

	// TypeName is the resource type.
	TypeName string

	// Tagged is whether the live object carried this estate's ownership
	// marker (declared_tagged when true, declared_untagged when false) -
	// see [checkOwnership]'s doc comment for why this reads TagEstate
	// rather than the policy's own TagKey/TagValue.
	Tagged bool

	// Verb is the policy verb that governed this instance.
	Verb policy.Verb
}

// String renders a policy outcome on one line.
func (p PolicyOutcome) String() string {
	quadrant := "declared_untagged"
	if p.Tagged {
		quadrant = "declared_tagged"
	}
	return p.Addr.String() + " " + quadrant + "=" + string(p.Verb)
}

// Omission is one instance that the projection does not contain.
type Omission struct {
	// Addr is the resource instance address that was not materialized.
	Addr addrs.AbsResourceInstance

	// Reason is the machine-readable classification.
	Reason Reason

	// Detail is one sentence aimed at an operator, explaining the omission
	// in terms of this specific instance.
	Detail string
}

// String renders an omission on a single line, for logs and test failures.
func (o Omission) String() string {
	return o.Addr.String() + " " + string(o.Reason) + ": " + o.Detail
}

// Reason classifies why an instance is absent from a projection.
type Reason string

const (
	// ReasonNeedsDiscovery means the identity package could not name the
	// instance at all, because its identity is server-assigned. Marker
	// discovery (P2) is what fixes this; until then the plan will propose
	// creating a resource that already exists.
	ReasonNeedsDiscovery Reason = "NEEDS_DISCOVERY"

	// ReasonParentUnavailable means the instance's identity is a formula
	// over parents' live IDs, and at least one of those parents is itself
	// absent from the projection, so the formula cannot be rendered.
	ReasonParentUnavailable Reason = "PARENT_UNAVAILABLE"

	// ReasonAbsent means the instance has a usable import identity and the
	// provider reported, normally, that no such object exists. The plan
	// will propose creating it, which is the correct answer.
	ReasonAbsent Reason = "ABSENT"

	// ReasonFailed means the provider errored while being asked about the
	// instance. An error diagnostic accompanies every omission with this
	// reason.
	ReasonFailed Reason = "FAILED"

	// ReasonCycle means the parent-derived instances form a dependency
	// cycle, so no order exists in which to materialize them. An error
	// diagnostic accompanies every omission with this reason.
	ReasonCycle Reason = "CYCLE"

	// ReasonUnowned means a live object does exist at the instance's
	// identity and does not carry this estate's ownership marker. It is
	// not in the prior state, so no plan can update or destroy it, and the
	// plan proposes creating what the configuration declares. A warning
	// diagnostic accompanies every omission with this reason.
	ReasonUnowned Reason = "UNOWNED"

	// ReasonUnreadable means the instance has no live cloud object to read
	// attributes from at all - a GitHub issue #73 record-backed effect,
	// whose prior state is a record rather than a resource. It is produced
	// only by [ReadInstances]; a projection hydrates such an instance from
	// the record store instead, so it is never omitted for this reason.
	ReasonUnreadable Reason = "UNREADABLE"

	// ReasonIncompleteBlock means the instance itself was read, and was then
	// dropped because another instance of the same resource block was not.
	// A resource whose instances are only partly known has no aggregate
	// value - see [dropIncompleteBlocks] for why a partial one is a wrong
	// answer and not merely a smaller one. Produced only by
	// [ReadInstances]: a projection records instances one at a time and has
	// no aggregate to keep whole.
	ReasonIncompleteBlock Reason = "INCOMPLETE_BLOCK"

	// ReasonSuperseded means the instance's own resolution is
	// [identity.Resolution.Undeclared] - a live object this estate owns
	// whose declared block is gone - AND the SAME import identity was
	// independently claimed, in this same pass, by a currently-declared
	// instance of the same type: an untaggable, single-parent-component
	// child of a resource a `moved` block, or ordinary parent-derived
	// re-discovery, relocated (GitHub issue #404). The declared instance is
	// what keeps managing the live object; this address is not proposed for
	// anything, and is never confused with [ReasonAbsent] (no live object)
	// or a genuine, unclaimed removal, which stays a destroy.
	ReasonSuperseded Reason = "SUPERSEDED"

	// ReasonListedNotImportable means the provider's own list call returned
	// a live object for this instance - carrying this estate's tofu-estate
	// marker and a tofu-address marker naming this address - and the
	// provider then reported that nothing exists at the identity that same
	// listing served for it (GitHub issue #596). The instance is NOT
	// proposed for creation: doing so would duplicate an object this run
	// positively identified as the estate's. An error diagnostic
	// ([SummaryListedNotImportable]) accompanies every omission with this
	// reason, so the run stops rather than applying half a plan. See
	// [builder.refuseListedButAbsent] for why a list-served identity is
	// proof of existence where a tag-index sighting is not.
	ReasonListedNotImportable Reason = "LISTED_NOT_IMPORTABLE"
)

// Has reports whether the projection contains an object for the given
// instance address.
func (r *Result) Has(addr addrs.AbsResourceInstance) bool {
	if r == nil || r.State == nil {
		return false
	}
	return r.State.ResourceInstance(addr) != nil
}

// OmissionFor returns the recorded omission for an instance address, and
// whether there was one.
func (r *Result) OmissionFor(addr addrs.AbsResourceInstance) (Omission, bool) {
	want := addr.String()
	for _, o := range r.Omitted {
		if o.Addr.String() == want {
			return o, true
		}
	}
	return Omission{}, false
}

// OmittedBecause returns every omission with the given reason, in address
// order.
func (r *Result) OmittedBecause(reason Reason) []Omission {
	var out []Omission
	for _, o := range r.Omitted {
		if o.Reason == reason {
			out = append(out, o)
		}
	}
	return out
}

// String renders a whole result as a multi-line summary, for logs and test
// failure output.
func (r *Result) String() string {
	var buf strings.Builder
	for _, addr := range r.Materialized {
		buf.WriteString(addr.String())
		buf.WriteString(" MATERIALIZED\n")
	}
	for _, o := range r.Omitted {
		buf.WriteString(o.String())
		buf.WriteString("\n")
	}
	return buf.String()
}

// sortOmissions puts omissions in address order so that Build's output is
// deterministic regardless of the order things were attempted in.
func sortOmissions(oms []Omission) {
	sort.Slice(oms, func(i, j int) bool {
		return oms[i].Addr.String() < oms[j].Addr.String()
	})
}

// sortUnowned puts the refused live objects in address order, for the same
// reason.
func sortUnowned(us []Unowned) {
	sort.Slice(us, func(i, j int) bool {
		return us[i].Addr.String() < us[j].Addr.String()
	})
}
