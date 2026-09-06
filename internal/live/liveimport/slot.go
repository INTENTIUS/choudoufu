// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/slots"
)

// slotMember is one instance of a count block as this migration sees it: the
// index the state file records it at, and the tofu-slot tag the live object
// carries right now (empty when it carries none).
type slotMember struct {
	addr  addrs.AbsResourceInstance
	index int
	slot  string
}

// migrationSlots is the tofu-slot value each eligible instance may carry away
// from this migration, keyed by instance address. An instance not in the map
// gets no slot tag written, exactly as before this existed.
//
// # Why a migration can compute this at all
//
// The package doc used to say flatly that it could not: a slot is a fact about
// a live count set, only a discovery pass constructs one, and a migration runs
// no discovery pass. Both halves of that are still true, and the conclusion was
// still wrong for the one case that covers nearly every migrated estate.
//
// What discovery does for a set whose live members carry no slot at all is not
// a computation over the live set - it is
// [github.com/intentius/choudoufu/internal/live/slots.Sequential], slot i for
// index i, and its own doc comment says why that is the only assignment
// possible: index k's tofu-address is the only thing that says which live
// resource is index k, so freezing that as the slot changes nothing about
// which resource is which. See discovery's bindCountByAddress, which is the
// branch [slots.Classify] sends a set with no slots down.
//
// A migration is writing exactly those addresses. So `tofu-slot = "0"` beside
// `tofu-address = "aws_x.y:0"` makes no claim the address does not already
// make, on the same object, in the same write. That is the whole safety
// argument, and it is why this is not a guess dressed up as a computation:
// there is no second candidate assignment to be wrong about.
//
// Without it, every count-expanded instance of a migrated estate plans one tag
// addition on the first stateless replan - 25 of corpus-ecs-fargate's 29, 22 of
// corpus-rds-complete-postgres's, 27 of corpus-vpc-complete's 29 - and the
// estate needs a convergence apply before a replan is honestly empty. GitHub
// issue #372.
//
// # What it deliberately declines to compute
//
// Four gates, each closing a case where the assignment above is not the one
// discovery would reach:
//
//  1. Not a count instance. A slot names a member of a fungible set, and only
//     a `count` block has one: discovery builds a countBlock only for a
//     resource config whose Count is set, and stamp writes no tofu-slot for a
//     for_each block, whose instances are named by their keys. The state's own
//     instance key is that distinction, exactly - count expands to
//     [addrs.IntKey], for_each to a string key, an unexpanded resource to
//     [addrs.NoKey] - so this reads the key rather than guessing at the
//     configuration it does not have in hand.
//
//  2. The set already carries slots, or disagrees with itself. [slots.Classify]
//     over the live tags this run already read: ModeAll means an established
//     scheme this migration has no business renumbering, and ModeMixed is the
//     named error discovery refuses on rather than picking a side. Only
//     ModeNone and ModeEmpty - the pre-slot estate and the empty set, the two
//     cases bindCountByAddress covers - are written.
//
//  3. The eligible indices are not 0..n-1. A count block's state instances are
//     contiguous from zero by construction, so a gap means an instance of the
//     set did not reach a marker write this run (a live object deleted out of
//     band, most realistically). Slot binding is by ascending slot against
//     ascending index, so writing slots 0 and 2 for a declared count of three
//     would bind the live object at index 2 to index 1 - a defensible thing for
//     a fungible set and NOT what the address binding it replaces would have
//     done. Left alone; the first live-plan's discovery pass still assigns it.
//
//  4. The instance does not classify [identity.ClassNeedsDiscovery]. This is
//     the gate that keeps the write from being SUPERFLUOUS rather than
//     wrong, and it was found by running it: discovery indexes a count
//     block only for instances it classifies ClassNeedsDiscovery
//     (discovery.go's declared walk skips every other class), so a count
//     instance whose identity IS computable from configuration gets no slot
//     assignment, the stamping pass writes none, and a tofu-slot on such an
//     object is a tag the very next plan proposes REMOVING. Measured, not
//     reasoned: with this gate absent, corpus-overture-tiles'
//     aws_s3_bucket.tiles[0] and corpus-dynamodb-table-basic's
//     aws_dynamodb_table.this[0] each planned `- "tofu-slot" = "0" -> null`
//     right after a clean migration.
//
//     [identity.TypeIdentity.ServerAssigned] answers this for a WHOLE type,
//     with no configuration needed: an instance of a server-assigned type
//     "always classifies as ClassNeedsDiscovery, whatever their arguments
//     say", so a slot written from that fact alone is always one the plan
//     wants. 448 of the table's 1049 rows carry it, and [serverAssignedType]
//     is that half of the gate.
//
//     The complement used to be left unsettled outright: a client-named
//     type whose name happens NOT to be statically computable (an
//     aws_iam_role named through name_prefix, an aws_sqs_queue URL that
//     carries the account id) is ClassNeedsDiscovery too, but WHICH
//     instances of it are is a question about each instance's own
//     declaration, and a migration reads a state file, not configuration.
//     GitHub issue #372's remainder settles it where the caller has one:
//     [Request.Config], when given, is resolved once through
//     [identity.ResolveWith] into [Ratification.resolved] - the exact
//     function, and for the types this matters to (already table-admitted,
//     so [identity.LookupType] settles them without ever consulting
//     Schemas) the exact ANSWER, that the subsequent stateless live-plan's
//     own [identity.Result] would produce for the identical configuration.
//     [instanceNeedsDiscovery] asks that Result by address; when it agrees
//     the instance is ClassNeedsDiscovery, a slot written here is one that
//     SAME resolution will independently want a moment later, which is
//     [serverAssignedType]'s safety argument again, just asked per instance
//     instead of per type. A caller with no Config (every test that built a
//     Ratification by hand before this existed, and any future one that
//     still does) gets [Ratification.resolved] nil, [instanceNeedsDiscovery]
//     answers false for everything, and the gate is exactly what it was: the
//     complement stays blocked, unsettled, one tag addition on the first
//     replan.
func (r *Ratification) migrationSlots() map[string]string {
	groups := make(map[string][]slotMember)
	blocked := make(map[string]bool)

	for _, entry := range r.Entries {
		elig, ok := r.eligible[entry.Addr.String()]
		if !ok {
			continue
		}
		block := entry.Addr.ContainingResource().String()
		index, isCount := countIndex(entry.Addr)
		if !isCount || !(serverAssignedType(entry.TypeName) || r.instanceNeedsDiscovery(entry.Addr)) {
			blocked[block] = true
			continue
		}
		tags, hasTags := tagsFromObject(elig.schema, elig.applied)
		if !hasTags {
			// Unreadable tags means the live slot state of this member is
			// unknown, so the set cannot be classified. approveOne fails this
			// instance for the same reason a moment later.
			blocked[block] = true
			continue
		}
		groups[block] = append(groups[block], slotMember{
			addr:  entry.Addr,
			index: index,
			slot:  tags[discovery.TagSlot],
		})
	}

	out := make(map[string]string)
	for block, members := range groups {
		if blocked[block] || len(members) == 0 {
			continue
		}

		set := make([]slots.Live, 0, len(members))
		for _, m := range members {
			set = append(set, slots.Live{ID: m.addr.String(), Slot: m.slot})
		}
		switch slots.Classify(set) {
		case slots.ModeNone, slots.ModeEmpty:
		default:
			continue
		}

		sort.Slice(members, func(i, j int) bool { return members[i].index < members[j].index })
		contiguous := true
		for i, m := range members {
			if m.index != i {
				contiguous = false
				break
			}
		}
		if !contiguous || slots.Slot(len(members)-1) > slots.MaxSlot {
			continue
		}

		seq := slots.Sequential(len(members))
		for i, m := range members {
			out[m.addr.String()] = seq[i].String()
		}
	}
	return out
}

// serverAssignedType is gate 4: whether every instance of this type
// classifies [identity.ClassNeedsDiscovery] whatever its arguments say, which
// is what makes a slot written here one the stamping pass will also write.
//
// Read from [identity.TypeIdentity.ServerAssigned], the same table
// [recordBackedType] and [secretMaterialType] next door read, by the same
// lookup. There is no list of type names here and there must never be one:
// the rule is the property, and a provider release whose types row-gen newly
// classifies server-assigned is covered the moment the table regenerates.
// An unknown type - one admitted only by the provider's own schema, through
// [admittedByProviderSchema] - answers false, which is the safe direction.
func serverAssignedType(typeName string) bool {
	ti, ok := identity.LookupType(typeName)
	return ok && ti.ServerAssigned
}

// instanceNeedsDiscovery is gate 4's other half: whether THIS instance -
// not its type - classifies [identity.ClassNeedsDiscovery] once its own
// declaration is resolved, per [Request.Config]'s doc comment and this
// function's own doc comment above.
//
// r.resolved is nil for every caller that gave no Config, which is every
// caller before this existed and any future one that still does not - the
// lookup then reports false for everything, exactly gate 4's old
// server-assigned-only behavior. It also reports false for an address
// [identity.ResolveWith] could not resolve at all (an unadmitted type, a
// refused expression, a module instance the walk never reached): "not
// found" and "found but some other class" are both "not proven safe to
// write", which is the direction this gate has to fail in.
func (r *Ratification) instanceNeedsDiscovery(addr addrs.AbsResourceInstance) bool {
	if r.resolved == nil {
		return false
	}
	res, ok := r.resolved.Get(addr)
	return ok && classTable[res.Class].needsDiscovery && causeStableWithoutManagedResults(res.Cause)
}

// causeStableWithoutManagedResults is the safeguard [Request.Config]'s doc
// comment does not spell out, found by running this change against
// corpus-ecs-fargate rather than by reading resolve.go: a bare
// [identity.ResolveWith] call - what Ratify makes, with no ManagedResults -
// is NOT always the same answer a stateless live-plan's own resolution
// settles on, because [statelessResolve] (internal/command/live_plan.go) is
// a TWO-PASS process. Its first pass is exactly what Ratify's bare call
// reproduces; its second pass, run only when the first refuses something,
// supplies ManagedResults - values a real provider PLAN call fills in for a
// sibling resource - and can turn a first-pass ClassNeedsDiscovery into
// ClassParentDerived or ClassConcrete once that value is in hand. It never
// goes the other way (statelessResolve's own downgradedToDiscovery check
// refuses a second pass that would turn a class BACK into NeedsDiscovery),
// so a bare resolution's ClassNeedsDiscovery is only trustworthy for a cause
// that could never have depended on a sibling's live value in the first
// place.
//
// Measured, not reasoned: module.ecs_service.aws_ecs_service.this[0] in
// corpus-ecs-fargate resolves ClassNeedsDiscovery/DiscoveryMarkerFallback
// from a bare call - its "cluster" identity component reads
// module.ecs_cluster.arn through a split()/element() transform GitHub issue
// #368 made expressible, but only once a real ARN is in hand - while the
// estate's actual live-plan resolves it ClassParentDerived once ManagedResults
// supplies that ARN. Before this function existed, gate 4's Config-driven
// half wrote this instance a tofu-slot tag, and the very next plan proposed
// removing it: this repository's specific description of a wrong marker.
//
// DiscoverySiblingApply is the other cause resolve.go documents as existing
// FOR this exact second-pass mechanism (the ACM/Route53 validation shape,
// #187) and is excluded for the identical reason.
//
// The remaining causes a [ClassNeedsDiscovery] resolution can carry -
// DiscoveryServerAssigned and its DiscoveryUniqueName refinement (a
// whole-type fact, entirely independent of any pass), DiscoveryCloudUnknown
// (depends only on [identity.Context.Cloud], which every real caller
// including this one leaves at its zero value - see [Request.Config]'s doc
// comment), DiscoveryNameOmitted and DiscoveryNamePrefix (a syntactic fact
// about which argument THIS resource's own body sets, consulting no other
// resource's value at all) - none of them can change between a bare call and
// a two-pass one, because none of them is answered by anything a second pass
// adds. DiscoveryCauseUnspecified is never what [identity.ResolveWith]
// itself produces (it marks a resolution assembled by a caller instead), so
// it is excluded along with everything the switch does not name, which is
// the direction an allowlist fails in in the first place.
func causeStableWithoutManagedResults(cause identity.DiscoveryCause) bool {
	switch cause {
	case identity.DiscoveryServerAssigned,
		identity.DiscoveryUniqueName,
		identity.DiscoveryCloudUnknown,
		identity.DiscoveryNameOmitted,
		identity.DiscoveryNamePrefix:
		return true
	default:
		return false
	}
}

// countIndex is the count index of an instance address, and whether it has
// one at all. A count block expands to [addrs.IntKey] and nothing else does,
// which is the property gate 1 above keys on.
func countIndex(addr addrs.AbsResourceInstance) (int, bool) {
	k, ok := addr.Resource.Key.(addrs.IntKey)
	if !ok || int(k) < 0 {
		return 0, false
	}
	return int(k), true
}
