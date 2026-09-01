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
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/projection"
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

	// VerifiedDeclared is issue #692's other half of the vouching set:
	// declared instances whose identity came out of the configuration (so
	// no Binding was ever created for them), but whose live object the
	// estate sweep saw carrying exactly their marker, with no displacement
	// verdict against it. Without this, the sweep's sighting of a
	// client-named instance was discarded at the "already declared" branch
	// (see displaced.go), and the state cache - forbidden from serving
	// anything the sweep has not vouched for - could never serve the
	// client-named majority of an IAM-heavy estate: measured on the
	// terralith, 6 of 38 instances vouched, every miss IAM, which the
	// tagging leg can never see (GetResources does not index IAM even on
	// real AWS; probed, recorded on #692). Populated by the three sighting
	// classifiers via [declared.vouchAddr]; consumed through
	// [Result.MarkerVerified].
	VerifiedDeclared []addrs.AbsResourceInstance

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

	// ParentReads lists the untaggable children a marked, admitted parent's
	// own identity led this pass to, with no marker and no configuration of
	// their own to find them by (issue #60). Each says whether it also
	// became a removal, the same way [OwnedResource.Removal] does for a
	// swept orphan - see [identity.ParentReadRemovable] for which types
	// this pass trusts to remove rather than only report.
	ParentReads []ParentReadFinding

	// Guided is true when this pass actually consumed a hint:
	// Request.Guided was set, a record store was configured, and a fresh,
	// well-formed hint was read from it. False whenever Request.Guided
	// was never set, and also false when it was set but the pass fell back
	// to full enumeration - see GuidedFallback for why. Scan metadata only;
	// nothing else in this Result depends on how Guided came to be true.
	Guided bool

	// GuidedFallback is empty whenever Guided is true, or whenever
	// Request.Guided was never set. Otherwise it names, in one sentence, why
	// a requested guided pass fell back to today's full enumeration: no
	// record store configured, no hint recorded yet, a corrupted one, or one
	// older than Request.GuidedMaxAge. Falling back is never an error and
	// never changes what the plan proposes - see Request.Guided - only how
	// many calls it cost to compute.
	GuidedFallback string

	// GuidedSweepSkipped lists the sweep types this pass skipped, sorted,
	// because a fresh hint reported no evidence of them and this was not a
	// verification pass (Request.GuidedVerify). Empty whenever Guided is
	// false. A type here is not swept this run; an orphan of it surfaces at
	// the next full or verification sweep instead of this one.
	GuidedSweepSkipped []string

	// NativeSweepSkipped is how many admitted types the estate-wide sweep's
	// native per-type leg did not list this pass because
	// [Request.CollectUnclaimed] was unset and this estate's own record
	// store gave a narrower universe to sweep - see nativesweep.go for the
	// rule and for exactly what it gives up. Zero means the native leg
	// listed everything [partitionSweepTypes] routed to it, which is the
	// case for every pass that asked the account-inventory question, every
	// pass with no record store, and every pass whose store is empty or
	// will not list.
	//
	// It is scan metadata, the same way [Guided] is: nothing else in this
	// Result depends on it, and the plan a narrowed pass proposes is the
	// plan an unnarrowed one proposes for every removal either can see. It
	// is reported so an operator reading "Foreign resources: nothing was
	// swept" learns that this run did not ask the account-wide question
	// rather than that the question came back empty.
	NativeSweepSkipped int

	// DeposedBindings is GitHub issue #361's crash-window recovery: every
	// address whose collision (two-or-more claimants for one declared
	// address - the shape a create-before-destroy crash produces while the
	// new and old object both still carry the address's marker) was broken
	// by matching exactly one claimant against a deposed object this
	// estate's record names for that address ([Request.DeposedRecords]).
	// The matched claimant is excluded from collision consideration
	// entirely; the remaining single claimant is bound through the
	// ordinary case-1 path and appears in [Result.Bindings], not here.
	//
	// [projection.BuildWith] folds each one into the constructed state's
	// own Instances[key].Deposed[dk] - see [projection.DeposedBinding]'s
	// own doc comment for what happens from there. Empty whenever
	// [Request.DeposedRecords] is nil (every caller before this existed),
	// or whenever it named a candidate that zero or two-or-more claimants
	// matched: those cases still raise [ProblemCollision] exactly as
	// before this existed - see bind()'s own collision-breaking code.
	DeposedBindings []projection.DeposedBinding

	// sweepPrefetchWasted and sweepPrefetchMismatched are GitHub issue #605's
	// own self-check, and both are always zero.
	//
	// The sweep's list calls are issued concurrently, ahead of the loop that
	// consumes them, by a planner that mirrors the sweep=true branches
	// through [scanType]'s and [scanTypeCloudControl]'s heads. A mirror can
	// drift, and the two ways it can drift are the two fields here: a call
	// planned that the scan never asks for (wasted - the sweep spent a list
	// call the sequential loop would not have spent, breaking issue #605's
	// "call counts must be identical" acceptance), and an answer fetched with
	// a list configuration the scan then disagreed with (mismatched - refused
	// and re-listed rather than used, because a listing of the wrong scope is
	// the one divergence a call count cannot see).
	//
	// Unexported: this is evidence for the package's own tests, not a fact
	// about the estate. See TestSweepPrefetchPlansExactlyTheCallsTheScanMakes.
	sweepPrefetchWasted     []string
	sweepPrefetchMismatched int
}

// ParentReadFinding is one live child a parent read found: an untaggable,
// admitted type whose whole identity [identity.SingleParentComponent] says
// is composed from a bound, admitted parent's own identity, discovered by
// reading that parent's value rather than by a marker or a declared
// resource block of the child's own.
type ParentReadFinding struct {
	// TypeName is the child's resource type.
	TypeName string

	// Parent is the admitted type whose identity led this pass to it.
	Parent string

	// ParentAddr is the parent's own resolved address in this estate.
	ParentAddr addrs.AbsResourceInstance

	// ParentValue is the parent identity value the read was scoped to -
	// the bucket name, the topic ARN, the queue URL, and so on - which is
	// also the child's own whole identity for this shape.
	ParentValue string

	// ImportID is the child's live identity as the read found it.
	ImportID string

	// IdentityAttr names the attribute ImportID was read from.
	IdentityAttr string

	// Identity is the full identity object as the provider sent it.
	Identity cty.Value

	// DisplayName is the provider's label for it. Display only.
	DisplayName string

	// Removal is true when this finding enters the prior state as an
	// instance with no configuration, the same way a swept orphan does, so
	// the plan proposes destroying it. See [identity.ParentReadRemovable].
	Removal bool

	// Withheld is why Removal is false: the one sentence an operator gets
	// instead of a destroy. Empty when Removal is true.
	Withheld string
}

// String renders a parent-read finding on one line.
func (f ParentReadFinding) String() string {
	s := f.TypeName + " " + f.ImportID + " via " + f.Parent + " " + f.ParentAddr.String()
	if f.Removal {
		return s + " REMOVAL"
	}
	if f.Withheld != "" {
		return s + " WITHHELD (" + f.Withheld + ")"
	}
	return s
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

	// PolicyVerb is the GitHub issue #67 undeclared_tagged verb that decided
	// this orphan's fate, when a policy block governed it. The zero value
	// means no policy block was in play (today's fixed sweep) or this
	// resource never reached policy at all (already withheld for a possible
	// rename before policy ever saw it).
	PolicyVerb policy.Verb
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
// noRegistryRowOrUntaggable builds the gap for a type the sweep skips
// because the roster reports it untaggable, choosing between the two facts a
// bare false collapses (issue #168).
//
// known=false means live/registry.json has no row for the CFN type at all,
// so it recorded nothing and the old message - "live/registry.json records X
// as untaggable" - would have been claiming otherwise. That case is a skew
// between two artifacts rather than a property of the resource, and it says
// which commands fix it.
func noRegistryRowOrUntaggable(typeName, cfnType string, known bool) SweepGap {
	if !known {
		return SweepGap{
			TypeName: typeName,
			Reason:   SweepGapNoRegistryRow,
			Detail: fmt.Sprintf(
				"live/registry.json has no row for %s (Cloud Control type %s), so whether it can carry an ownership marker is unknown and the sweep skipped it. "+
					"That is a skew between live/mapping.json and live/registry.json, not a property of the type: regenerate both (`just registry`, `just mapping`).",
				typeName, cfnType),
		}
	}
	return SweepGap{
		TypeName: typeName,
		Reason:   SweepGapNotTaggable,
		Detail: fmt.Sprintf(
			"live/registry.json records %s (Cloud Control type %s) as untaggable, so it can carry no ownership marker and the sweep has nothing to search on.",
			typeName, cfnType),
	}
}

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

	// SweepGapNoRegistryRow is an admitted type whose CFN type
	// live/mapping.json names and live/registry.json has no row for. It is
	// a skew between two artifacts regenerated from different upstreams at
	// different times, not a fact about the resource - which is why it is
	// not [SweepGapNotTaggable]: that one says the registry recorded a
	// false, and this one says it recorded nothing (issue #168). The fix is
	// `just registry` and `just mapping`, not a change to the type.
	SweepGapNoRegistryRow SweepGapReason = "NO_REGISTRY_ROW"

	// SweepGapObjectUntagged is an admitted, schema-taggable type
	// ([markerCapable] said yes) that nonetheless listed at least one live
	// object carrying no readable tags at all - a provider or emulator bug
	// on that specific object, distinct from [SweepGapNotTaggable]'s
	// type-wide "this type has no tags argument at all". Downgraded from a
	// hard [Problem] to a gap only for the estate-wide sweep: a type
	// nothing in configuration declares is best-effort coverage by
	// definition, so one malformed object in it must not abort a plan that
	// depends on none of it, the same reasoning [SweepGapListFailed]
	// already applies to a list call that errors outright. A declared
	// instance hitting the identical condition stays a hard Problem,
	// because the operator's own configuration is waiting on it.
	SweepGapObjectUntagged SweepGapReason = "OBJECT_UNTAGGED"

	// SweepGapNoARNJoin is a type the tagging sweep ([Request.TaggingSweep],
	// issue #51) cannot recognize from an ARN alone: either live/mapping.json
	// names no CFN type for it, or the ARN join table
	// (internal/live/discovery/tagging.go) has no entry able to produce that
	// CFN type. Specific to the tagging sweep - the per-type sweep this
	// package runs otherwise has no such gap, because it lists by CFN type
	// directly rather than joining backward from an ARN.
	//
	// [partitionSweepTypes] routes a type failing this test to the native
	// per-type sweep BEFORE [sweepViaTagging] ever runs (found via
	// corpus-rds-complete-postgres's day2_remove unit: aws_db_instance has
	// no arnJoinTable row, and once its estate's only declared instance
	// became record-backed the config-driven scan stopped covering it too,
	// so this reason used to mean "invisible to every leg" for any type
	// outside the ARN join table, not merely "invisible to this one"), so
	// this reason now reaches [res.SweepGaps] only for a type genuinely
	// unreachable BOTH ways - see [SweepGapNotListable] for that other leg's
	// own gap when the native fallback is what actually failed.
	SweepGapNoARNJoin SweepGapReason = "NO_ARN_JOIN"

	// SweepGapScopeUnavailable is a type whose CFN listing needs a
	// parent-scoped ResourceModel (live/registry.json's
	// handlers.list_required_input, internal/live/cloudcontrol's
	// ListResourcesScoped) that this leg could not build safely: either the
	// required input does not match the single scoping property this leg
	// was given, or it could not be positioned inside the type's own
	// primary_identifier to verify a result actually belongs to the parent
	// it was scoped to. Reported rather than silently skipped, and rather
	// than sent with an unverifiable ResourceModel and trusted blind - a
	// Cloud Control backend that ignores scoping (floci's own
	// ListResources does, confirmed against its source) would otherwise let
	// one parent's children be attributed to another's.
	SweepGapScopeUnavailable SweepGapReason = "PARENT_SCOPE_UNAVAILABLE"
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

// DeposedBindingsList returns [Result.DeposedBindings], nil-safely - the
// same "a nil Result behaves like an empty one" convention [SlotTable]
// already follows, for a caller (internal/command's [projection.BuildWith]
// call sites) that may not have run discovery at all this pass.
func (r *Result) DeposedBindingsList() []projection.DeposedBinding {
	if r == nil {
		return nil
	}
	return r.DeposedBindings
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
	out := make(map[string]bool, len(r.Bindings)+len(r.Surplus)+len(r.Orphans)+len(r.VerifiedDeclared))
	for _, a := range r.VerifiedDeclared {
		out[a.String()] = true
	}
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

	// ProblemUndeclaredCrossTypeMarker is the estate-wide sweep finding a
	// live resource of a type the configuration declares no instance of,
	// carrying this estate's marker for an address of some OTHER type.
	//
	// A warning, not an error, and it proposes nothing. The same shape on a
	// type the configuration DOES declare is [ProblemMalformedMarker] and
	// still fails the run: there an instance of that very type is waiting
	// to be found, so a marker naming another type's address is a conflict
	// a human has to settle. Under the sweep, with the type absent from the
	// configuration entirely, there is no instance the object could ever
	// bind to and no address it could be an orphan of - a destroy at an
	// address of another type is what classifyOrphans refuses outright -
	// so nothing in the run acts on it either way, and failing every plan
	// the estate ever runs over it is the same over-reaction
	// [SweepGapObjectUntagged] already declines to make one branch away.
	//
	// The ordinary cause is not a hand-edited tag at all: AWS copies a
	// resource's tags onto the dependent objects it creates for it, and
	// those objects are types of their own. An autoscaling group's
	// propagate_at_launch tags land on the instances it launches; an ECS
	// service with propagate_tags = SERVICE puts its tags on the tasks and
	// the network interfaces created for them. The marker on such an object
	// is a COPY of the marked resource's own marker, and the resource it
	// names is elsewhere in this estate, correctly marked and correctly
	// bound.
	ProblemUndeclaredCrossTypeMarker ProblemKind = "UNDECLARED_CROSS_TYPE_MARKER"

	// ProblemDisplacedMarker is a live resource carrying this estate's
	// marker for an address the configuration still declares, whose own
	// identity is not the identity that address resolves to - so a second,
	// different live resource is what the configuration means by it.
	//
	// That is GitHub issue #244's half 2, and the population is renumbering:
	// deleting a middle element of a count list moves every later instance's
	// identity down one while the live resources keep the markers they were
	// stamped with. Before this kind existed, such a resource was in no
	// section of the result at all - not bound, not an orphan, not a
	// problem, not a removal.
	//
	// A warning, not an error, and it proposes nothing. The identity
	// comparison behind it is inexact by construction (see
	// internal/live/discovery/displaced.go), so its false positives have to
	// cost a line of output and nothing more. The resource stays outside
	// removal coverage, which is the same safe direction
	// [ProblemUnsweepableOwnedType] and a [SweepGap] fail in.
	ProblemDisplacedMarker ProblemKind = "DISPLACED_MARKER"

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

	// ProblemLocatedRecordUnreadable is [scanTypeLocatedFallback]'s own
	// failure: a type with no tags argument and no list route of any kind
	// has nowhere to be found except the estate's record store, and reading
	// that store failed - a corrupt record, a store the process can no
	// longer reach, or a version this build does not understand
	// ([projection.RecordStore.GetIdentity]'s own refusals). It is never "no
	// record exists yet" - that answer is not a problem, it is an ordinary
	// absence, and the instance is left unbound so the plan proposes a
	// create.
	//
	// [recordOrphanReadSweep] reuses the same kind for the same failure on
	// its own, undeclared side of the record store: a key it cannot read
	// while deciding whether a destroy is safe to propose.
	ProblemLocatedRecordUnreadable ProblemKind = "LOCATED_RECORD_UNREADABLE"

	// ProblemUnresolvedAccount is the owner-id trap: every listed identity
	// of a type came back with an empty account ID, which means the
	// provider resolved none and the owner-id filter it appends to a
	// server-side filtered list went out empty. Harmless against an
	// emulator, matches nothing against real AWS.
	ProblemUnresolvedAccount ProblemKind = "UNRESOLVED_ACCOUNT"

	// ProblemListFailed is a provider error while listing a type.
	ProblemListFailed ProblemKind = "LIST_FAILED"

	// ProblemRecordStoreListFailed is [recordOrphanReadSweep]'s own list
	// failure: the estate's record store could not be listed at all, so
	// this run cannot say whether any untaggable, multi-component-identity
	// resource (an inline aws_iam_role_policy/aws_iam_user_policy/
	// aws_iam_group_policy, today) whose configuration block was removed
	// exists to propose destroying.
	ProblemRecordStoreListFailed ProblemKind = "RECORD_STORE_LIST_FAILED"

	// ProblemUncomposableIdentifier is a Cloud Control ListResources
	// identifier this package refuses to hand out as an import ID: a
	// multi-part ("|"-joined) identifier whose parts cannot be composed
	// through the identity table's Components, either because the type has
	// no table entry or because the table's components do not line up with
	// the parts Cloud Control sent. The raw "|"-joined string is never used
	// as a substitute - see [composeCloudControlIdentifier].
	ProblemUncomposableIdentifier ProblemKind = "UNCOMPOSABLE_IDENTIFIER"

	// ProblemAmbiguousUniqueName is a name bind (issue #272,
	// internal/live/discovery/uniquename.go) refused because the match was
	// not unique, from either side: several live objects carrying one
	// declared name, or several declared instances stating one name.
	//
	// It is an ERROR and it binds nothing. The whole justification for
	// matching on a name at all is that AWS guarantees the name identifies
	// one object; a run that finds two has just been shown the guarantee
	// does not hold the way this fork read it, and picking one of them would
	// be exactly the content-match guess internal/live/foreign refuses to
	// make. See uniquename.go's own doc comment.
	ProblemAmbiguousUniqueName ProblemKind = "AMBIGUOUS_UNIQUE_NAME"

	// ProblemUnreadableUniqueName is a listed object of a name-bound type
	// carrying no readable string where live/registry.json's own schema for
	// the type says its name lives.
	//
	// It is diagnosed rather than skipped because the object may BE the one
	// a declared instance is waiting for. Passing over it silently would
	// leave that instance unbound, and an unbound instance is proposed for
	// creation - so a listing this leg could not read would turn into a
	// second live object beside the first.
	ProblemUnreadableUniqueName ProblemKind = "UNREADABLE_UNIQUE_NAME"

	// ProblemUnresolvedTaggedARN is a resource the tagging sweep
	// ([Request.TaggingSweep], issue #51) found carrying this estate's
	// marker, whose ResourceARN could not be joined to a (TF type,
	// identifier) pair: the ARN's service and resource segments name no CFN
	// type the join table knows, name more than one and nothing in the ARN
	// says which, or the CFN type they do name has no unique TF type in
	// live/mapping.json. Named by the ARN's own segments - see
	// [joinTaggedResource] - and never guessed at. A warning rather than an
	// error: the resource simply stays outside this pass's removal
	// coverage, the same safe direction a [SweepGap] fails in.
	ProblemUnresolvedTaggedARN ProblemKind = "UNRESOLVED_TAGGED_ARN"

	// ProblemUnsweepableOwnedType is a resource the tagging sweep
	// ([Request.TaggingSweep], issue #51) found carrying this estate's
	// marker, of a type the sweep's own universe does not contain and the
	// configuration no longer declares.
	//
	// That is GitHub issue #107's population. A type absent from the
	// admission table can still be admitted, when the provider's identity
	// schema or the configuration's own arguments settle it
	// (internal/live/lint/admission.go), but the sweep draws its universe
	// from [identity.AdmittedTypes] - the table's keys. So deleting the last
	// block of such a type left the live resource in the account with
	// nothing said about it: not swept, not reported, not a residue note.
	//
	// The tagging sweep is the one path that can see it at all, because it
	// asks the cloud for everything carrying the marker rather than asking
	// per type. It cannot destroy it - there is no table row to build an
	// import identity from - but it can refuse to be silent about it, which
	// is what #107 says is the one unacceptable outcome. A warning, not an
	// error: nothing about the run in front of the operator is wrong, and
	// the resource simply sits outside removal coverage.
	ProblemUnsweepableOwnedType ProblemKind = "UNSWEEPABLE_OWNED_TYPE"

	// ProblemAmbiguousTagJoin is a listed resource whose own list call
	// returned no ownership marker, and whose identifier matched more than
	// one resource in the estate's tag index carrying a tofu-address that
	// names this very type (issue #266, [markerIndex.join]).
	//
	// The join exists to attach the tags a list call dropped to the object
	// they belong to. Two candidates means the data does not say which
	// object was listed, and binding either would risk adopting the other's
	// live resource - the one outcome worse than the defect the join fixes.
	// So nothing is bound and the ARNs are named. An error, because two
	// resources of one type sharing one identifier means the estate's own
	// records disagree.
	ProblemAmbiguousTagJoin ProblemKind = "AMBIGUOUS_TAG_JOIN"

	// ProblemUnreadableMarker is a declared instance that went unbound
	// while the run listed at least one live resource of its type whose
	// ownership marker it could not read (issue #266).
	//
	// Unbound means the plan will propose creating it. That is the right
	// answer for a greenfield instance and the wrong one for an instance
	// whose live resource was on the table all along with its tags stripped
	// off by the list call - and the two look identical in the output,
	// which is how #266 shipped a run that created a duplicate per apply
	// and reported the original as foreign in the same breath.
	//
	// The predicate is deliberately not "which types lose their tags on the
	// list path". An honestly untagged resource of a type that keeps its
	// tags produces byte-identical evidence, and that case is arguably
	// correct - an unmarked object is not this estate's. The run cannot
	// tell them apart, so it says what it saw.
	//
	// A warning: nothing here is known to be wrong, and refusing would
	// refuse every genuine greenfield create in an account that has any
	// untagged resource of the same type. When [markerIndex] can settle it
	// - the tag index holds a resource marked for this exact address that
	// no listed object matched - the detail says so outright.
	ProblemUnreadableMarker ProblemKind = "UNREADABLE_MARKER"

	// ProblemAmbiguousContentMatch is issue #272's content-match leg
	// finding more than one live candidate carrying the same value a
	// declared instance's own identity-bearing argument names
	// ([scanTypeContentMatch]). This is the leg's own version of
	// [ProblemAmbiguousTagJoin]'s question - which live object is this
	// declared instance's - and it is answered the same way: not at all.
	// Binding any one of the candidates would risk adopting a different
	// instance's resource, so none is bound and every candidate's
	// identifier is named. An error, because two live objects with the
	// same client-supplied name in one account is the situation the
	// two-source uniqueness proof (tools/row-gen's contentMatchRoster) was
	// supposed to make impossible - seeing it anyway means the evidence
	// was wrong for this account, not that a winner should be guessed.
	ProblemAmbiguousContentMatch ProblemKind = "AMBIGUOUS_CONTENT_MATCH"
)

// Severity is the diagnostic severity a problem of this kind carries.
// Everything that could make a plan act on the wrong resource is an error;
// the account-ID smoke alarm is a warning because the run in front of the
// operator may be perfectly correct.
func (k ProblemKind) Severity() Severity {
	switch k {
	case ProblemUnresolvedAccount, ProblemUnresolvedTaggedARN, ProblemUnsweepableOwnedType, ProblemDisplacedMarker, ProblemUnreadableMarker,
		ProblemUndeclaredCrossTypeMarker:
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

// EnumerationSource is which mechanism a scan used to enumerate a type's
// live population (issue #47).
type EnumerationSource string

const (
	// SourceProvider is the provider's own native list resource - the
	// primary source wherever it exists, because it returns the
	// provider-shaped objects the rest of discovery already consumes.
	SourceProvider EnumerationSource = "PROVIDER"

	// SourceCloudControl is AWS Cloud Control's ListResources on the CFN
	// type live/mapping.json joins the TF type to, used only when the
	// provider offers no native list resource for the type and
	// live/registry.json says the mapped CFN type is listable with no
	// required input. See [registry.Roster.EnumerationSource].
	SourceCloudControl EnumerationSource = "CLOUD_CONTROL"

	// SourceTagging is the Resource Groups Tagging API's GetResources,
	// filtered to this estate's tofu-estate tag - the tagging sweep's
	// candidate source ([Request.TaggingSweep], issue #51), reached only
	// through the sweep and only when the flag is set. Unlike
	// SourceCloudControl, one call covers every swept type at once; each
	// type's own [TypeScan] still records what its share of that one call
	// produced.
	SourceTagging EnumerationSource = "TAGGING_API"

	// SourceRecordStore is the estate's own record store, consulted by
	// [scanTypeLocatedFallback] for a type with no tags argument and no list
	// route of any kind - the one population none of the other three
	// sources can ever reach, because there is no tag to index and nothing
	// to list. It never enumerates: each declared instance is a single
	// point lookup by its own address, so "Listed" here counts records
	// found, not objects returned by one call.
	SourceRecordStore EnumerationSource = "RECORD_STORE"
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

	// Source says which enumeration source this scan used: the provider's
	// native list resource, or Cloud Control's ListResources on a mapped
	// CFN type (issue #47). Empty for a scan that never started (the
	// provider cannot list the type and no Cloud Control fallback applied);
	// see [Result.Problems] for why.
	Source EnumerationSource

	// CFNType is the CFN type name Cloud Control was asked to list, set
	// only when Source is [SourceCloudControl].
	CFNType string

	// Refined is the number of listed resources whose tags could not be
	// read from Cloud Control's ResourceDescriptions.Properties directly
	// (no Tags property came back with the list) and needed an individual
	// GetResource call to refine - the cost the client-side tag-filtering
	// rule in issue #47 says to surface rather than hide. Always zero for a
	// native-provider scan, whose tags always ride along with the listed
	// object.
	Refined int

	// Joined is the number of listed resources that came back with no
	// ownership marker of their own and had one attached from the estate's
	// tag index instead (issue #266, [markerIndex.join]). It is the count
	// of instances that would have gone unbound - and been proposed for
	// creation over a live resource that already exists - on the pre-#266
	// path, so it is worth seeing rather than hiding.
	Joined int

	// NameBound is the number of declared instances bound by the name AWS
	// documents as unique for their type rather than by an ownership marker
	// (issue #272, uniquename.go). It is reported rather than folded into
	// Bound because it is the count of binds made on an exception to the
	// marker rule, and an operator reading a scan line should be able to see
	// how many of them a run made.
	NameBound int

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
	source := ""
	switch s.Source {
	case SourceCloudControl:
		source = fmt.Sprintf(" source=cloudcontrol(%s)", s.CFNType)
		if s.Refined > 0 {
			source += fmt.Sprintf(" refined=%d", s.Refined)
		}
	case SourceTagging:
		source = fmt.Sprintf(" source=tagging(%s)", s.CFNType)
	case SourceProvider:
		source = " source=provider"
	case SourceRecordStore:
		source = " source=record-store"
	}
	joined := ""
	if s.Joined > 0 {
		joined = fmt.Sprintf(" joined=%d", s.Joined)
	}
	if s.NameBound > 0 {
		joined += fmt.Sprintf(" name-bound=%d", s.NameBound)
	}
	return fmt.Sprintf("%s%s %s/%s declared=%d listed=%d bound=%d other-estate=%d unclaimed=%d%s%s",
		s.TypeName, kind, s.Filtering, s.Scope, s.Declared, s.Listed, s.Bound, s.OtherEstate, s.Unclaimed, source, joined)
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
	for _, f := range r.ParentReads {
		b.WriteString("PARENTREAD " + f.String() + "\n")
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
	sort.Slice(r.ParentReads, func(i, j int) bool {
		if r.ParentReads[i].TypeName != r.ParentReads[j].TypeName {
			return r.ParentReads[i].TypeName < r.ParentReads[j].TypeName
		}
		return r.ParentReads[i].ImportID < r.ParentReads[j].ImportID
	})
	sort.Strings(r.SweepCovered)
	sort.Strings(r.GuidedSweepSkipped)
	sort.Slice(r.Resolutions, func(i, j int) bool {
		return r.Resolutions[i].Addr.String() < r.Resolutions[j].Addr.String()
	})
}
