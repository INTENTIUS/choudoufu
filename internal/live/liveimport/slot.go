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
//  4. The type's identity is not server-assigned. This is the gate that keeps
//     the write from being SUPERFLUOUS rather than wrong, and it was found by
//     running it: discovery indexes a count block only for instances it
//     classifies [identity.ClassNeedsDiscovery] (discovery.go's declared walk
//     skips every other class), so a count instance whose identity IS
//     computable from configuration gets no slot assignment, the stamping pass
//     writes none, and a tofu-slot on such an object is a tag the very next
//     plan proposes REMOVING. Measured, not reasoned: with this gate absent,
//     corpus-overture-tiles' aws_s3_bucket.tiles[0] and
//     corpus-dynamodb-table-basic's aws_dynamodb_table.this[0] each planned
//     `- "tofu-slot" = "0" -> null` right after a clean migration.
//
//     Whether one INSTANCE is ClassNeedsDiscovery is a question about its
//     configuration, and a migration has none in hand - it reads a state file.
//     [identity.TypeIdentity.ServerAssigned] is the half of that question the
//     type table answers on its own, and it answers it in the safe direction:
//     an instance of a server-assigned type "always classifies as
//     ClassNeedsDiscovery, whatever their arguments say", so a slot written
//     here is always one the plan wants. 448 of the table's 1049 rows carry
//     it. The complement is not a wrong answer, only an unsettled one: a
//     client-named type whose name happens NOT to be statically computable
//     (an aws_sqs_queue URL, which carries the account id) is
//     ClassNeedsDiscovery too, wants a slot, and does not get one here - its
//     estate keeps exactly today's behavior, one tag addition on the first
//     replan. Settling those needs the per-instance class, which means a
//     resolution pass this command deliberately does not run; that is the
//     remainder of GitHub issue #372 and it is named in the PR, not guessed
//     at here.
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
		if !isCount || !serverAssignedType(entry.TypeName) {
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
