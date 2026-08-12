// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// Result is everything one discovery pass learned.
//
// Resolutions is the field the run needs; the rest is what an operator, and
// the phases built on this one, need in order to explain it.
type Result struct {
	// Estate is the estate name that was searched for.
	Estate string

	// Resolutions is the caller's resolution list with every bound instance
	// rewritten as [identity.ClassConcrete] carrying the live import ID,
	// in address order. Instances that stayed unbound keep the class they
	// arrived with, so a projection built from this list still records them
	// as needing discovery and the plan still proposes creating them.
	//
	// This is the input to projection.BuildFrom.
	Resolutions []identity.Resolution

	// Bindings lists every declared instance that a live resource claimed,
	// in address order.
	Bindings []Binding

	// Unbound lists the declared needs-discovery instances that nothing
	// claimed, in address order. Absence is not an error: the resource does
	// not exist and the plan will propose creating it.
	Unbound []addrs.AbsResourceInstance

	// Unclaimed lists the live resources of the scanned types that carry no
	// tofu-estate tag at all, in type and identity order. This package
	// draws no conclusion about them; P2.4 classifies them.
	//
	// It is empty unless [Request.CollectUnclaimed] was set, because the
	// server-side estate filter hides them. Read [Result.ScanFor] to tell
	// "none exist" from "nothing looked".
	Unclaimed []UnclaimedResource

	// Orphans lists live resources that carry this estate's tofu-estate tag
	// and a well-formed tofu-address that no declared instance matches -
	// the resource block was removed, or renamed without a marker rewrite.
	// Deleting a resource block is legal, so this is not an error here.
	//
	// Each one says whether it is a removal candidate. The ones that are
	// have a matching concrete resolution in [Result.Resolutions], so that
	// they reach the projection as prior-state instances with no
	// configuration and the plan engine proposes destroying them. The ones
	// that are not say why in [OwnedResource.Withheld], and a rename hint is
	// the reason that matters: an orphan sitting beside an unclaimed
	// declared instance of the same block is a key that moved until the
	// classifier says otherwise, and turning that into a destroy and a
	// create would replace a resource that only needed a new tag.
	Orphans []OwnedResource

	// SweepGaps lists the resource types the estate-wide sweep could not
	// enumerate: types the provider cannot list, and types whose list call
	// failed. An orphan of one of them is invisible to this run, so its
	// resource block being deleted plans nothing. That is a hole in removal
	// coverage and is reported as one rather than left to look like an empty
	// result.
	SweepGaps []SweepGap

	// SweepCovered lists the resource types the estate-wide sweep did
	// enumerate, sorted. It is the counterpart of SweepGaps: "these types
	// were searched for resources this estate owns but no longer declares".
	// It covers the sweep only, and is not the same list as
	// [foreign.Result.Swept], which is about the types whose unclaimed
	// population was enumerated.
	SweepCovered []string

	// Surplus lists the live members of a count set that are past its
	// declared count: the highest slots, which a scale-down deletes.
	//
	// Each carries the instance address just above the declared count that
	// it occupies in the projection, so that the prior state has the shape a
	// stock run's shrunken count has and the plan engine's ordinary orphan
	// handling proposes destroying it. They are in [Result.Resolutions] as
	// concrete resolutions for that reason, and are deliberately not in
	// [Result.Bindings], which is the declared instances only.
	Surplus []Binding

	// Slots is the tofu-slot value every declared count instance carries
	// after this pass: the slot it was found with, the slot it is migrating
	// to, or the slot minted for the create that will fill it. It is the
	// input to marker stamping, which writes these values as tags.
	Slots []SlotAssignment

	// Problems are the named ambiguities: collisions, malformed markers,
	// and the conditions this package refuses to guess through. Every
	// problem also produced a diagnostic, at the severity given by
	// [ProblemKind.Severity].
	Problems []Problem

	// Scans records what happened per resource type: how it was filtered,
	// how wide the scan was, and what came back.
	Scans []TypeScan
}

// Binding is one declared instance matched to one live resource.
type Binding struct {
	// Addr is the declared resource instance.
	Addr addrs.AbsResourceInstance

	// TypeName is the resource type that was listed.
	TypeName string

	// ImportID is the live identity value that becomes the concrete
	// resolution's import ID.
	ImportID string

	// IdentityAttr names the identity attribute ImportID was read from,
	// normally "id".
	IdentityAttr string

	// Marker is the tofu-address tag value exactly as the resource carries
	// it, before normalization.
	Marker string

	// Normalized is true when Marker had to be run through [EscapeAddress]
	// before it matched, meaning the resource carries a pre-spec unescaped
	// address (see the package doc).
	Normalized bool

	// Slot is the tofu-slot tag value, empty when the resource carries
	// none.
	Slot string

	// SlotBound is true when the slot, rather than the address, is what
	// decided which declared instance this resource is.
	SlotBound bool

	// AddressStale is true when a slot-bound resource's tofu-address tag
	// names an index other than the one it bound to. The tag is not wrong
	// about who owns the resource, only about where in the set it sits, and
	// marker stamping writes the current index back over it - so this is a
	// repair the plan makes visible, not an error. Only meaningful when
	// SlotBound is set: without slots the address is what bound, so it
	// cannot disagree with the binding.
	AddressStale bool

	// Surplus is true when this is a live member past the declared count of
	// its count set, occupying an instance address the configuration does
	// not expand to.
	Surplus bool

	// DisplayName is the provider's label for the listed resource, for
	// operator-facing output only.
	DisplayName string

	// Identity is the full identity object of the listed resource.
	Identity cty.Value
}

// String renders a binding on one line, for logs and test failures.
func (b Binding) String() string {
	s := b.Addr.String() + " <- " + b.ImportID
	if b.SlotBound {
		s += " (slot " + b.Slot + ")"
	}
	if b.Surplus {
		s += " SURPLUS"
	}
	if b.AddressStale {
		s += " (address " + b.Marker + " stale)"
	}
	if b.Normalized {
		s += " (marker " + b.Marker + " normalized)"
	}
	return s
}

// SlotOrigin says where a declared count instance's slot came from.
type SlotOrigin string

const (
	// SlotCarried is a slot the live resource already carries. The common
	// case, and the one that produces no change at all.
	SlotCarried SlotOrigin = "CARRIED"

	// SlotMigrated is a slot assigned to a live resource that had none: a
	// pre-slot estate's count set gaining slot markers, frozen at the
	// assignment its per-instance addresses already express so the migration
	// moves nothing.
	SlotMigrated SlotOrigin = "MIGRATED"

	// SlotMinted is a freshly minted slot for an instance that has no live
	// resource yet, so that the create carries its slot from birth.
	SlotMinted SlotOrigin = "MINTED"
)

// SlotAssignment is the tofu-slot value one declared count instance carries
// after a discovery pass.
type SlotAssignment struct {
	// Addr is the declared instance.
	Addr addrs.AbsResourceInstance

	// Key is the escaped instance address - the tofu-address value - which
	// is the key marker stamping looks this assignment up by.
	Key string

	// Slot is the value, spelled as MARKERS.md spells it.
	Slot string

	// Origin says where it came from.
	Origin SlotOrigin
}

// String renders a slot assignment on one line.
func (s SlotAssignment) String() string {
	return s.Addr.String() + " slot " + s.Slot + " " + string(s.Origin)
}

// SlotTable is the assignment as marker stamping consumes it: escaped
// instance address to slot value. Nil when this pass assigned no slots, which
// is what a configuration with no count blocks produces and what tells the
// stamping pass to write no tofu-slot tags at all.
func (r *Result) SlotTable() map[string]string {
	if r == nil || len(r.Slots) == 0 {
		return nil
	}
	out := make(map[string]string, len(r.Slots))
	for _, s := range r.Slots {
		out[s.Key] = s.Slot
	}
	return out
}

// UnclaimedResource is a live resource of a scanned type carrying no
// tofu-estate tag. It is raw material for P2.4: everything needed to
// classify it as foreign, or to offer it as a bind candidate for a declared
// address, without listing anything a second time.
type UnclaimedResource struct {
	// TypeName is the resource type it was listed as.
	TypeName string

	// ImportID is the identity value that would be its import ID, empty if
	// the provider sent no usable identity.
	ImportID string

	// IdentityAttr names the attribute ImportID came from.
	IdentityAttr string

	// Identity is the full identity object as the provider sent it.
	Identity cty.Value

	// DisplayName is the provider's label for it. Display only.
	DisplayName string

	// Tags are the resource's tags, decoded from the listed object. Nil
	// when the object carried no tags attribute at all.
	Tags map[string]string

	// Resource is the full listed object, so a consumer can match on
	// content without listing again. It is cty.NilVal when the provider
	// did not send one.
	Resource cty.Value
}

// String renders an unclaimed resource on one line.
func (u UnclaimedResource) String() string {
	id := u.ImportID
	if id == "" {
		id = "(no identity)"
	}
	if u.DisplayName != "" {
		return u.TypeName + " " + id + " " + u.DisplayName
	}
	return u.TypeName + " " + id
}

// OwnedResource is a live resource carrying this estate's marker whose
// address matches no declared instance.
type OwnedResource struct {
	// TypeName is the resource type it was listed as.
	TypeName string

	// ImportID is the identity value that would be its import ID.
	ImportID string

	// IdentityAttr names the attribute ImportID was read from.
	IdentityAttr string

	// Identity is the full identity object as the provider sent it.
	Identity cty.Value

	// Marker is the tofu-address tag value as carried.
	Marker string

	// Normalized is the escaped form of Marker, which is the string that
	// was compared against declared addresses.
	Normalized string

	// Addr is Normalized turned back into an address, and Addressable says
	// whether it could be. See [UnescapeAddress] for the two values that
	// cannot. An orphan with no address is reported and never planned:
	// removal proposes destroying a prior-state instance, and an instance
	// needs somewhere to sit.
	Addr        addrs.AbsResourceInstance
	Addressable bool

	// Slot is the tofu-slot tag value, empty when it carries none.
	Slot string

	// DisplayName is the provider's label. Display only.
	DisplayName string

	// Tags are the resource's tags as listed.
	Tags map[string]string

	// Resource is the full listed object, so that a consumer can match on
	// content without listing again - which is what strengthens a rename
	// pairing. cty.NilVal when the provider sent no object.
	Resource cty.Value

	// Swept is true when this resource was found by the estate-wide sweep
	// rather than by a scan of a type the configuration declares. It is the
	// difference between "a key of a block that still exists is gone" and
	// "the block is gone", and only the sweep can see the second one.
	Swept bool

	// Removal is true when this resource enters the prior state as an
	// instance with no configuration, so that the plan engine proposes
	// destroying it the way it destroys any resource whose block was
	// deleted.
	Removal bool

	// Withheld is why Removal is false: the one sentence an operator gets
	// instead of a destroy. Empty when Removal is true.
	Withheld string
}

// String renders an owned-but-undeclared resource on one line.
func (o OwnedResource) String() string {
	s := o.TypeName + " " + o.ImportID + " claims " + o.Normalized
	if o.Removal {
		return s + " REMOVAL"
	}
	if o.Withheld != "" {
		return s + " WITHHELD (" + o.Withheld + ")"
	}
	return s
}

// SweepGapReason is why one resource type could not be swept for this
// estate's undeclared resources.
type SweepGapReason string

const (
	// SweepGapNotListable is a type the provider offers no list resource
	// for, so nothing of it can be enumerated at all.
	SweepGapNotListable SweepGapReason = "TYPE_NOT_LISTABLE"

	// SweepGapListFailed is a type whose list call errored.
	SweepGapListFailed SweepGapReason = "LIST_FAILED"

	// SweepGapConfigFailed is a type whose list configuration could not be
	// built from its schema, which is a provider or fork bug rather than a
	// live-system condition.
	SweepGapConfigFailed SweepGapReason = "LIST_CONFIG_FAILED"

	// SweepGapNotTaggable is an admitted type whose objects carry no tags,
	// so it can hold no ownership marker and the sweep has nothing to search
	// on. Its identity comes out of configuration, which means deleting its
	// resource block deletes the only record of which resource it was.
	SweepGapNotTaggable SweepGapReason = "TYPE_NOT_TAGGABLE"
)

// SweepGap is one resource type the removal sweep could not cover.
type SweepGap struct {
	// TypeName is the type.
	TypeName string

	// Reason is the machine-readable why.
	Reason SweepGapReason

	// Detail is one sentence for an operator.
	Detail string
}

// String renders a sweep gap on one line.
func (g SweepGap) String() string {
	return g.TypeName + " [" + string(g.Reason) + "]: " + g.Detail
}

// Removals returns the orphans this pass proposes destroying, in the order
// they sit in [Result.Orphans].
func (r *Result) Removals() []OwnedResource {
	var out []OwnedResource
	for _, o := range r.Orphans {
		if o.Removal {
			out = append(out, o)
		}
	}
	return out
}

// MarkerVerified is the set of instance addresses this pass established
// ownership of by reading a live tofu-estate marker, keyed by address string.
//
// It exists for the projection, which has to know which of the resolutions it
// is about to materialize already carry proof of ownership. A resolution that
// came out of this package was produced by matching a live marker to a
// declared address, so the proof is the binding itself; a resolution that came
// out of static analysis carries no such proof, and the projection reads the
// live object's own tags before admitting it (audit finding C1). Everything
// this pass turned into a concrete resolution is in here: bound instances,
// the surplus members of a count set, and the owned-but-undeclared resources
// removal planning acts on.
func (r *Result) MarkerVerified() map[string]bool {
	if r == nil {
		return nil
	}
	out := make(map[string]bool, len(r.Bindings)+len(r.Surplus)+len(r.Orphans))
	for _, b := range r.Bindings {
		out[b.Addr.String()] = true
	}
	for _, s := range r.Surplus {
		out[s.Addr.String()] = true
	}
	for _, o := range r.Orphans {
		if o.Removal {
			out[o.Addr.String()] = true
		}
	}
	return out
}

// ProblemKind is the machine-readable classification of a discovery
// problem.
type ProblemKind string

const (
	// ProblemCollision is two or more live resources carrying the same
	// estate and the same address. The marker path assumes at most one live
	// resource per address per estate; a collision needs a human.
	ProblemCollision ProblemKind = "MARKER_COLLISION"

	// ProblemMalformedMarker is a live resource carrying tofu-estate with a
	// missing or unparseable tofu-address. Not foreign and not owned.
	ProblemMalformedMarker ProblemKind = "MALFORMED_MARKER"

	// ProblemNeedsSlotMarkers is several live resources sharing one count
	// instance's address, with no slot markers to tell them apart. Guessing
	// here would attach a plan to an arbitrary member of a fungible set.
	ProblemNeedsSlotMarkers ProblemKind = "NEEDS_SLOT_MARKERS"

	// ProblemMixedSlots is a count set where some live members carry a
	// tofu-slot and some do not, so the set has two answers to "which live
	// resource is which instance" and no rule for choosing between them.
	ProblemMixedSlots ProblemKind = "MIXED_SLOT_MARKERS"

	// ProblemMalformedSlot is a tofu-slot tag value outside the grammar in
	// MARKERS.md: not an unsigned base-10 integer, or carrying leading
	// zeros, or past the ten-digit ceiling.
	ProblemMalformedSlot ProblemKind = "MALFORMED_SLOT"

	// ProblemDuplicateSlot is two live resources in one count set carrying
	// the same slot: the ownership collision of a fungible set.
	ProblemDuplicateSlot ProblemKind = "DUPLICATE_SLOT"

	// ProblemSlotExhausted is a count set that cannot be given another slot
	// without going past the ten-digit ceiling the marker grammar allows.
	ProblemSlotExhausted ProblemKind = "SLOT_EXHAUSTED"

	// ProblemNoTags is a listed resource whose object came back with no
	// tags attribute at all, so its markers cannot be read. Either the
	// provider did not honor the request for full objects, or a type that
	// cannot carry tags was admitted as marker-discoverable.
	ProblemNoTags ProblemKind = "NO_TAGS"

	// ProblemNoIdentity is a live resource whose marker binds it to a
	// declared address, but which the provider sent no usable identity for,
	// so there is no import ID to hand the projection.
	ProblemNoIdentity ProblemKind = "NO_IDENTITY"

	// ProblemTypeNotListable is a needs-discovery type the provider cannot
	// list. Its instances cannot be discovered at all, so reporting them as
	// merely absent would be a lie.
	ProblemTypeNotListable ProblemKind = "TYPE_NOT_LISTABLE"

	// ProblemUnresolvedAccount is the owner-id trap: every listed identity
	// of a type came back with an empty account ID, which means the
	// provider resolved none and the owner-id filter it appends to a
	// server-side filtered list went out empty. Harmless against an
	// emulator, matches nothing against real AWS.
	ProblemUnresolvedAccount ProblemKind = "UNRESOLVED_ACCOUNT"

	// ProblemListFailed is a provider error while listing a type.
	ProblemListFailed ProblemKind = "LIST_FAILED"
)

// Severity is the diagnostic severity a problem of this kind carries.
// Everything that could make a plan act on the wrong resource is an error;
// the account-ID smoke alarm is a warning because the run in front of the
// operator may be perfectly correct.
func (k ProblemKind) Severity() Severity {
	if k == ProblemUnresolvedAccount {
		return SeverityWarning
	}
	return SeverityError
}

// Severity is a problem's severity.
type Severity string

// The two severities a [Problem] can carry.
const (
	SeverityError   Severity = "ERROR"
	SeverityWarning Severity = "WARNING"
)

// Problem is one named thing discovery refused to guess through.
type Problem struct {
	// Kind is the classification.
	Kind ProblemKind

	// TypeName is the resource type involved.
	TypeName string

	// Addr is the declared instance involved, when the problem is about
	// one. The zero value means the problem is about a live resource that
	// matched no declared instance.
	Addr addrs.AbsResourceInstance

	// Marker is the tofu-address value involved, escaped as compared.
	Marker string

	// LiveIDs are the import identities of the live resources involved,
	// sorted: both sides of a collision, or the one malformed resource.
	LiveIDs []string

	// Detail is one sentence for an operator.
	Detail string
}

// String renders a problem on one line.
func (p Problem) String() string {
	var b strings.Builder
	b.WriteString(string(p.Kind))
	if p.TypeName != "" {
		b.WriteString(" " + p.TypeName)
	}
	if p.Marker != "" {
		b.WriteString(" " + p.Marker)
	}
	if len(p.LiveIDs) > 0 {
		b.WriteString(" [" + strings.Join(p.LiveIDs, ", ") + "]")
	}
	b.WriteString(": " + p.Detail)
	return b.String()
}

// FilterMode is where the estate filter was applied for one type.
type FilterMode string

const (
	// FilterServerSide means the tag filter went to the provider, so only
	// this estate's resources crossed the wire.
	FilterServerSide FilterMode = "SERVER_SIDE"

	// FilterClientSide means everything the provider could list came back
	// and the estate filter was applied here.
	FilterClientSide FilterMode = "CLIENT_SIDE"
)

// Scope is how wide one type's list was.
type Scope string

const (
	// ScopeEstate means only this estate's resources were asked for, so
	// unclaimed resources were not looked at.
	ScopeEstate Scope = "ESTATE"

	// ScopeAll means every resource of the type was listed, so unclaimed
	// resources in [Result.Unclaimed] are the complete set.
	ScopeAll Scope = "ALL"
)

// TypeScan is what happened for one resource type.
type TypeScan struct {
	// TypeName is the type that was listed.
	TypeName string

	// Filtering is where the estate filter was applied.
	Filtering FilterMode

	// FilterReason explains a client-side filter: what the type's list
	// schema does not offer, or that a wider scope was asked for.
	FilterReason string

	// Scope is how wide the list was.
	Scope Scope

	// Declared is the number of needs-discovery instances of this type in
	// configuration.
	Declared int

	// Listed is the number of live resources the provider returned.
	Listed int

	// Bound is the number of declared instances that got a binding.
	Bound int

	// OtherEstate is the number of listed resources carrying a different
	// estate's tag, which were ignored.
	OtherEstate int

	// Unclaimed is the number of listed resources carrying no estate tag.
	Unclaimed int

	// AccountID is the account ID the listed identities carried, empty when
	// the provider resolved none. See [ProblemUnresolvedAccount].
	AccountID string

	// Sweep is true when this scan is part of the estate-wide sweep: a type
	// the configuration declares nothing of, listed anyway because this
	// estate may still own resources of it. Such a scan looks for markers
	// and for nothing else - it never collects unclaimed resources, because
	// a type the configuration does not mention has no declared instance a
	// foreign resource could be offered for.
	Sweep bool
}

// String renders a scan on one line.
func (s TypeScan) String() string {
	kind := ""
	if s.Sweep {
		kind = " SWEEP"
	}
	return fmt.Sprintf("%s%s %s/%s declared=%d listed=%d bound=%d other-estate=%d unclaimed=%d",
		s.TypeName, kind, s.Filtering, s.Scope, s.Declared, s.Listed, s.Bound, s.OtherEstate, s.Unclaimed)
}

// BindingFor returns the binding for one declared instance.
func (r *Result) BindingFor(addr addrs.AbsResourceInstance) (Binding, bool) {
	want := addr.String()
	for _, b := range r.Bindings {
		if b.Addr.String() == want {
			return b, true
		}
	}
	return Binding{}, false
}

// ScanFor returns what happened for one resource type.
func (r *Result) ScanFor(typeName string) (TypeScan, bool) {
	for _, s := range r.Scans {
		if s.TypeName == typeName {
			return s, true
		}
	}
	return TypeScan{}, false
}

// instanceIndex is a count instance's index, or -1 for an instance that has
// no integer key.
func instanceIndex(addr addrs.AbsResourceInstance) int {
	if k, ok := addr.Resource.Key.(addrs.IntKey); ok {
		return int(k)
	}
	return -1
}

// SlotFor returns the slot assigned to one declared instance.
func (r *Result) SlotFor(addr addrs.AbsResourceInstance) (SlotAssignment, bool) {
	want := addr.String()
	for _, s := range r.Slots {
		if s.Addr.String() == want {
			return s, true
		}
	}
	return SlotAssignment{}, false
}

// ProblemsOfKind returns every problem of one kind, in the order found.
func (r *Result) ProblemsOfKind(kind ProblemKind) []Problem {
	var out []Problem
	for _, p := range r.Problems {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// String renders a whole result as a multi-line summary, for logs and test
// failure output.
func (r *Result) String() string {
	var b strings.Builder
	for _, s := range r.Scans {
		b.WriteString("SCAN      " + s.String() + "\n")
	}
	for _, bd := range r.Bindings {
		b.WriteString("BOUND     " + bd.String() + "\n")
	}
	for _, sp := range r.Surplus {
		b.WriteString("SURPLUS   " + sp.String() + "\n")
	}
	for _, s := range r.Slots {
		b.WriteString("SLOT      " + s.String() + "\n")
	}
	for _, a := range r.Unbound {
		b.WriteString("UNBOUND   " + a.String() + "\n")
	}
	for _, o := range r.Orphans {
		b.WriteString("ORPHAN    " + o.String() + "\n")
	}
	for _, u := range r.Unclaimed {
		b.WriteString("UNCLAIMED " + u.String() + "\n")
	}
	for _, g := range r.SweepGaps {
		b.WriteString("SWEEPGAP  " + g.String() + "\n")
	}
	for _, p := range r.Problems {
		b.WriteString("PROBLEM   " + p.String() + "\n")
	}
	return b.String()
}

func (r *Result) sortEverything() {
	sort.Slice(r.Bindings, func(i, j int) bool {
		return r.Bindings[i].Addr.String() < r.Bindings[j].Addr.String()
	})
	sort.Slice(r.Surplus, func(i, j int) bool {
		return r.Surplus[i].Addr.String() < r.Surplus[j].Addr.String()
	})
	// Slot assignments sort by address and then by the slot as a number, so
	// aws_eip.pool[9] never sorts after aws_eip.pool[10] on the strength of
	// its spelling. See MARKERS.md on comparing slots numerically.
	sort.Slice(r.Slots, func(i, j int) bool {
		if r.Slots[i].Addr.Resource.Resource.String() != r.Slots[j].Addr.Resource.Resource.String() {
			return r.Slots[i].Addr.Resource.Resource.String() < r.Slots[j].Addr.Resource.Resource.String()
		}
		return instanceIndex(r.Slots[i].Addr) < instanceIndex(r.Slots[j].Addr)
	})
	sort.Slice(r.Unbound, func(i, j int) bool {
		return r.Unbound[i].String() < r.Unbound[j].String()
	})
	sort.Slice(r.Unclaimed, func(i, j int) bool {
		if r.Unclaimed[i].TypeName != r.Unclaimed[j].TypeName {
			return r.Unclaimed[i].TypeName < r.Unclaimed[j].TypeName
		}
		return r.Unclaimed[i].ImportID < r.Unclaimed[j].ImportID
	})
	sort.Slice(r.Orphans, func(i, j int) bool {
		if r.Orphans[i].TypeName != r.Orphans[j].TypeName {
			return r.Orphans[i].TypeName < r.Orphans[j].TypeName
		}
		return r.Orphans[i].ImportID < r.Orphans[j].ImportID
	})
	sort.Slice(r.SweepGaps, func(i, j int) bool {
		return r.SweepGaps[i].TypeName < r.SweepGaps[j].TypeName
	})
	sort.Strings(r.SweepCovered)
	sort.Slice(r.Resolutions, func(i, j int) bool {
		return r.Resolutions[i].Addr.String() < r.Resolutions[j].Addr.String()
	})
}
