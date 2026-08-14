// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/slots"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// countBlock is one `count` resource block and everything live that claimed
// it. It is the unit the set matcher works on, because a count block's
// instances are a set rather than a list of addresses: which live resource is
// index 2 is a question about the whole block, never about one address.
type countBlock struct {
	// addr is the escaped block address, e.g. "aws_eip.pool".
	addr string

	// resource is the block itself, used to synthesize the instance
	// addresses of surplus members - the ones past the declared count.
	resource addrs.Resource

	// module is the static module instance the block is declared in: the
	// root module instance for a root resource, or the module-qualified
	// path for one inside a static module. It is what turns [resource] -
	// which carries no module of its own - into an address that matches
	// what identity resolution and the marker actually carry.
	module addrs.ModuleInstance

	// typeName is the resource type, cached from resource.
	typeName string

	// entries are the declared instances in ascending index order. Empty when
	// the block expands to nothing (count = 0), which is still a block with
	// live members to account for.
	entries []*declaredEntry

	// extra are live resources carrying this estate's marker that named this
	// block but not one of its current declared instances: a marker naming
	// the bare block address, and - the case that matters - an instance
	// address the configuration no longer expands to, which is what a live
	// member of a shrunken or renumbered set carries.
	extra []claimant
}

// instanceAddr is the absolute address of one index of this block, including
// indices past the declared count.
func (cb *countBlock) instanceAddr(i int) addrs.AbsResourceInstance {
	return cb.resource.Instance(addrs.IntKey(i)).Absolute(cb.module)
}

// claimants is every live resource in this block's set, in a deterministic
// order: the declared instances' claimants by ascending index, then the
// extras in the order the scan found them.
func (cb *countBlock) claimants() []claimant {
	var out []claimant
	for _, entry := range cb.entries {
		out = append(out, entry.claimants...)
	}
	out = append(out, cb.extra...)
	return out
}

// countBlockFor finds the count block a marker value names, if any.
//
// A count instance's marker is the block address with a numeric index -
// "aws_eip.pool:2" - and the whole point of slot binding is that the index in
// that value is not trusted to say which instance it is. So the lookup is by
// block: strip a trailing numeric index and see whether what is left is a
// count block of this type. A marker naming the bare block address matches
// too, which is what pre-instance-key writers produced.
//
// Nothing is decoded here, in the sense the package doc means: the index is
// recognized and discarded, never turned back into an address.
func countBlockFor(blocks map[string]*countBlock, escaped string) *countBlock {
	if blocks == nil {
		return nil
	}
	if cb, ok := blocks[escaped]; ok {
		return cb
	}
	i := strings.LastIndex(escaped, ":")
	if i < 0 {
		return nil
	}
	index := escaped[i+1:]
	if index == "" {
		return nil
	}
	for j := 0; j < len(index); j++ {
		if index[j] < '0' || index[j] > '9' {
			// A quoted for_each key, not a count index. It cannot belong to
			// a count block, and treating it as one would let a for_each
			// resource's marker join somebody else's fungible set.
			return nil
		}
	}
	return blocks[escaped[:i]]
}

// ---------------------------------------------------------------------------
// Binding one count block
// ---------------------------------------------------------------------------

// bindCountBlock binds one count block's declared instances to its live
// members, by slot where the estate carries slots and by address where it does
// not.
//
// The two paths are chosen by what the live set carries, never by a flag:
// slot markers take precedence when they are there, address binding is the
// compatibility path for an estate that predates them, and a set that carries
// both at once is a named error rather than a guess about which half to
// believe.
func bindCountBlock(req Request, cb *countBlock, res *Result, bound map[string]Binding) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	live := cb.claimants()
	set := make([]slots.Live, 0, len(live))
	for _, c := range live {
		set = append(set, slots.Live{ID: c.displayID(), Slot: c.slot})
	}

	switch slots.Classify(set) {
	case slots.ModeMixed:
		mixed := slots.MixedFrom(set)
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemMixedSlots,
			TypeName: cb.typeName,
			Marker:   cb.addr,
			LiveIDs:  claimantIDs(live),
			Detail: fmt.Sprintf(
				"The live members of the count set %s disagree about slot markers: %s. A count block's instances are a fungible set, and which live resource is which instance is answered by tofu-slot when the set carries slots and by tofu-address when it does not - so a set carrying both at once has two answers and this run will not pick between them. Give every member of the set a tofu-slot tag, or none of them.",
				cb.addr, mixed),
		}))

	case slots.ModeAll:
		return bindCountBySlot(req, cb, res, bound, live, set)

	default:
		// ModeNone and ModeEmpty. An estate whose count instances are told
		// apart by their per-instance addresses, exactly as before slots
		// existed - and the empty case, a set being created for the first
		// time, which the same code covers because both assign slot i to
		// index i.
		return bindCountByAddress(req, cb, res, bound)
	}
}

// bindCountByAddress is the compatibility path: the estate's count instances
// carry per-instance tofu-address markers and no slots, so the address is
// what binds, exactly as it did before this task.
//
// The one thing that is new is what comes out the other side: every declared
// index is assigned the slot equal to its index, which the stamping pass then
// writes. That migration is a no-op on the binding - freezing the assignment
// the addresses already express - and it is visible in the plan as a
// tofu-slot tag being added to each member, which is the only place a
// one-time change to an estate's ownership records should ever be visible.
func bindCountByAddress(req Request, cb *countBlock, res *Result, bound map[string]Binding) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	for i, entry := range cb.entries {
		switch len(entry.claimants) {
		case 0:
			res.Unbound = append(res.Unbound, entry.res.Addr)
			res.Slots = append(res.Slots, SlotAssignment{
				Addr: entry.res.Addr, Key: entry.escaped,
				Slot: slots.Slot(i).String(), Origin: SlotMinted,
			})
		case 1:
			c := entry.claimants[0]
			b, ok := bindOne(cb.typeName, entry.res.Addr, entry.escaped, c, res, &diags)
			if !ok {
				continue
			}
			res.Bindings = append(res.Bindings, b)
			bound[entry.res.Addr.String()] = b
			res.Slots = append(res.Slots, SlotAssignment{
				Addr: entry.res.Addr, Key: entry.escaped,
				Slot: slots.Slot(i).String(), Origin: SlotMigrated,
			})
		default:
			diags = diags.Append(problemDiag(res, collisionProblem(req, cb.typeName, entry)))
		}
	}

	// Live members the addresses do not account for. Without slots there is
	// nothing to bind them by, so they are reported exactly as they were
	// before: a marker naming the whole block is the case slot markers exist
	// to answer, and a marker naming an index the configuration no longer
	// expands to is an owned resource with no declared address, which is
	// removal planning's business (P5.1) rather than something to bind.
	var blockLevel []claimant
	for _, c := range cb.extra {
		if c.escaped == cb.addr {
			blockLevel = append(blockLevel, c)
			continue
		}
		res.Orphans = append(res.Orphans, OwnedResource{
			TypeName:    cb.typeName,
			ImportID:    c.importID,
			Marker:      c.marker,
			Normalized:  c.escaped,
			Slot:        c.slot,
			DisplayName: c.displayName,
			Tags:        c.tags,
		})
	}
	if len(blockLevel) > 0 {
		diags = diags.Append(problemDiag(res, Problem{
			Kind:     ProblemNeedsSlotMarkers,
			TypeName: cb.typeName,
			Marker:   cb.addr,
			LiveIDs:  claimantIDs(blockLevel),
			Detail: fmt.Sprintf(
				"%d live %s resource(s) carry the marker %q, which is the address of a count block with %d expanded instances rather than the address of any one of them, and none of them carries a tofu-slot either. Nothing distinguishes which live resource is which instance, and binding them in list order would attach a plan to an arbitrary member of a fungible set. Stamp tofu-slot markers on them - the values this run would assign the declared instances are 0 upward - or give them per-instance tofu-address tags.",
				len(blockLevel), cb.typeName, cb.addr, len(cb.entries)),
		}))
	}

	return diags
}

// bindCountBySlot is the real thing: N declared instances against M live
// slots, matched as a set.
//
// The index a member lands on comes from the order of its slot and nothing
// else, so a member's tofu-address tag has no say in where it binds. A tag
// that names a different index than the member ended up on is stale rather
// than wrong-in-a-way-that-matters, and it is repaired by the ordinary plan:
// the stamping pass writes tofu-address from the index the slot bound to, the
// plan shows the tag moving, and an apply writes it. See the package doc
// ("Slots bind; addresses follow") for why that is a repair rather than the
// plan silently changing which resource a marker points at.
func bindCountBySlot(req Request, cb *countBlock, res *Result, bound map[string]Binding, live []claimant, set []slots.Live) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	match, err := slots.Match(len(cb.entries), set)
	if err != nil {
		return diags.Append(problemDiag(res, slotProblem(req, cb, live, err)))
	}

	// Slot values are unique after a successful match, and Parse accepts only
	// the canonical spelling, so the tag value is a key.
	bySlot := make(map[string]claimant, len(live))
	for _, c := range live {
		bySlot[c.slot] = c
	}

	for _, b := range match.Bound {
		entry := cb.entries[b.Index]
		c := bySlot[b.Slot.String()]
		binding, ok := bindOne(cb.typeName, entry.res.Addr, entry.escaped, c, res, &diags)
		if !ok {
			continue
		}
		binding.SlotBound = true
		binding.AddressStale = c.escaped != entry.escaped
		res.Bindings = append(res.Bindings, binding)
		bound[entry.res.Addr.String()] = binding
	}

	// Surplus: the highest slots, past the declared count. They enter the
	// result at the instance addresses just above the count, which is exactly
	// where a shrunken count's leftovers sit in a stock run's prior state -
	// and the plan engine's orphan transformer destroy-plans them from there
	// with no special case anywhere.
	for _, b := range match.Surplus {
		c := bySlot[b.Slot.String()]
		addr := cb.instanceAddr(b.Index)
		escaped := EscapeAddress(addr.String())
		binding, ok := bindOne(cb.typeName, addr, escaped, c, res, &diags)
		if !ok {
			continue
		}
		binding.SlotBound = true
		binding.Surplus = true
		binding.AddressStale = c.escaped != escaped
		res.Surplus = append(res.Surplus, binding)
	}

	for _, d := range match.Deficit {
		res.Unbound = append(res.Unbound, cb.entries[d.Index].res.Addr)
	}

	for i, entry := range cb.entries {
		origin := SlotCarried
		if i >= len(match.Bound) {
			origin = SlotMinted
		}
		res.Slots = append(res.Slots, SlotAssignment{
			Addr: entry.res.Addr, Key: entry.escaped,
			Slot: match.Slots[i].String(), Origin: origin,
		})
	}

	return diags
}

// bindOne turns one claimant into a binding, or records the one thing that
// can stop it: a live resource the provider sent no usable identity for,
// which there is no import ID to build a projection entry from.
func bindOne(typeName string, addr addrs.AbsResourceInstance, escaped string, c claimant, res *Result, diags *tfdiags.Diagnostics) (Binding, bool) {
	if c.noIdentity {
		*diags = diags.Append(problemDiag(res, Problem{
			Kind:     ProblemNoIdentity,
			TypeName: typeName,
			Addr:     addr,
			Marker:   escaped,
			Detail: fmt.Sprintf(
				"The live %s bound to %s came back from the list call with no usable identity, so there is no import ID to build a projection from. The identity this type is looked up by (%s) was not among the attributes the list call returned. A provider that serves no identity at all cannot be discovered by marker; one that serves a different set is issue #105.",
				typeName, addr, identityAttrNames(typeName)),
		}))
		return Binding{}, false
	}
	return Binding{
		Addr:         addr,
		TypeName:     typeName,
		ImportID:     c.importID,
		IdentityAttr: c.identityAttr,
		Marker:       c.marker,
		Normalized:   c.escaped != c.marker,
		Slot:         c.slot,
		DisplayName:  c.displayName,
		Identity:     c.identity,
	}, true
}

// slotProblem turns a set matcher failure into the named problem for it. The
// matcher's errors are typed precisely so this mapping is a switch rather
// than string matching on a message.
func slotProblem(req Request, cb *countBlock, live []claimant, err error) Problem {
	base := Problem{
		TypeName: cb.typeName,
		Marker:   cb.addr,
		LiveIDs:  claimantIDs(live),
	}

	var parse *slots.ParseError
	if errors.As(err, &parse) {
		base.Kind = ProblemMalformedSlot
		base.Detail = fmt.Sprintf(
			"A live %s in the count set %s carries a tofu-slot tag that is not a slot: %s. Per live/MARKERS.md a slot is an unsigned base-10 integer with no leading zeros, compared numerically; a value outside that grammar is malformed, and which member of the set the resource is cannot be worked out from it. A human has to fix the tag.",
			cb.typeName, cb.addr, parse)
		return base
	}

	var dup *slots.DuplicateError
	if errors.As(err, &dup) {
		base.Kind = ProblemDuplicateSlot
		base.Detail = fmt.Sprintf(
			"Two or more live %s resources in the count set %s carry the same tofu-slot: %s. A slot names one member of a fungible set, so a repeated slot means two resources claim to be the same member - the same class of ownership collision as two resources claiming one address, and the same answer: something upstream needs a human before this estate can be planned.",
			cb.typeName, cb.addr, dup)
		return base
	}

	base.Kind = ProblemSlotExhausted
	base.Detail = fmt.Sprintf(
		"The count set %s of estate %q could not be matched against its live members: %s.",
		cb.addr, req.Estate, err)
	return base
}

// sortedCountBlocks returns a type's count blocks in address order.
func sortedCountBlocks(m map[string]*countBlock) []*countBlock {
	out := make([]*countBlock, 0, len(m))
	for _, cb := range m {
		out = append(out, cb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].addr < out[j].addr })
	return out
}
