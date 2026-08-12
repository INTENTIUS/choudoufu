// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package slots

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Slot is one tofu-slot value: the opaque identifier of a member of a
// fungible count set.
type Slot uint64

// MaxSlot is the ceiling stateless/MARKERS.md puts on a slot value: ten
// digits, 4294967295. No realistic count approaches it, and refusing to mint
// past it is cheaper than discovering later that a marker no longer parses
// under the spec's own grammar.
const MaxSlot Slot = 4294967295

// String renders a slot as the tag value MARKERS.md specifies: base 10, ASCII
// digits, no leading zeros.
func (s Slot) String() string { return strconv.FormatUint(uint64(s), 10) }

// Parse reads a tofu-slot tag value.
//
// The grammar is deliberately strict, because a slot is compared numerically
// and written as a string: "007" and "7" would be the same number and
// different tags, and a set that carried both would have two members claiming
// one slot with nothing to point at as wrong. Only the canonical spelling is
// accepted, so the tag and the number are in bijection.
func Parse(s string) (Slot, error) {
	switch {
	case s == "":
		return 0, &ParseError{Value: s, Reason: "it is empty"}
	case len(s) > 10:
		return 0, &ParseError{Value: s, Reason: fmt.Sprintf("it is %d digits long, over the ten-digit ceiling", len(s))}
	case len(s) > 1 && s[0] == '0':
		return 0, &ParseError{Value: s, Reason: "it has a leading zero, and the spec's spelling of a slot has none"}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, &ParseError{Value: s, Reason: "it is not an unsigned base-10 integer"}
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, &ParseError{Value: s, Reason: "it is not an unsigned base-10 integer"}
	}
	if Slot(n) > MaxSlot {
		return 0, &ParseError{Value: s, Reason: fmt.Sprintf("it is above the ceiling of %s", MaxSlot)}
	}
	return Slot(n), nil
}

// Live is one live member of a fungible set, as the caller found it.
type Live struct {
	// ID is the caller's handle on the resource - in practice its live
	// import identity. It is carried through so a result can name what it
	// matched, and it never influences the matching itself.
	ID string

	// Slot is the tofu-slot tag value exactly as the resource carries it,
	// empty when it carries none.
	Slot string
}

// Mode says what the live set as a whole carries.
type Mode int

const (
	// ModeEmpty is a set with no live members at all.
	ModeEmpty Mode = iota

	// ModeNone is a set where no member carries a slot: an estate written
	// before slot markers existed, whose members are told apart by their
	// per-instance tofu-address tags instead.
	ModeNone

	// ModeAll is a set where every member carries a slot.
	ModeAll

	// ModeMixed is a set where some members carry a slot and some do not.
	// Never matched and never guessed at - see [MixedError].
	ModeMixed
)

// Classify reports whether a live set carries slots, does not, or disagrees
// with itself.
func Classify(live []Live) Mode {
	var slotted, bare int
	for _, l := range live {
		if l.Slot == "" {
			bare++
			continue
		}
		slotted++
	}
	switch {
	case slotted == 0 && bare == 0:
		return ModeEmpty
	case slotted == 0:
		return ModeNone
	case bare == 0:
		return ModeAll
	default:
		return ModeMixed
	}
}

// Bound is one live member paired with the declared index it occupies.
type Bound struct {
	// Index is the count index this member binds to. For a surplus member it
	// is at or above the declared count: the position it occupies in prior
	// state, which is exactly where an orphan of a shrunken count lives.
	Index int

	// Live is the member, as the caller supplied it.
	Live Live

	// Slot is its parsed slot.
	Slot Slot
}

// Minted is a declared index with no live member, and the slot the create
// that fills it will carry from birth.
type Minted struct {
	// Index is the count index that plans as a create.
	Index int

	// Slot is the freshly minted slot.
	Slot Slot
}

// Result is one matching of a declared count against a live set.
type Result struct {
	// Declared is the count the configuration expands to.
	Declared int

	// Bound pairs a live member with each index the set covers, ascending by
	// index, and holds min(declared, live) entries.
	Bound []Bound

	// Surplus holds the live members past the declared count - the highest
	// slots - ascending by slot. These are the deletion candidates of a
	// scale-down, and their Index is where prior state has to carry them for
	// the plan engine to see them as orphans.
	Surplus []Bound

	// Deficit holds the declared indices no live member covers, ascending,
	// each with the slot minted for it.
	Deficit []Minted

	// Slots is the slot of every declared index, from 0 to Declared-1: the
	// carried slot of a bound index and the minted slot of a deficit one.
	// This is the assignment the marker stamping pass writes.
	Slots []Slot

	// HighWater is the highest slot in play after this match: the greatest
	// of every live slot, surplus included, and every slot minted here. It
	// is what the next mint would count from.
	HighWater Slot
}

// Match pairs a declared count with a live set, per the rule in the package
// doc: ascending slot to ascending index, surplus at the top, deficit minted
// above the high-water mark.
//
// Every live member must carry a parseable slot, and no two may carry the
// same one. Both failures are errors rather than a best effort, because both
// mean that which live resource is which member of the set is not something
// the markers answer, and picking one would attach a plan to an arbitrary
// resource. Use [Classify] first to tell a set that has no slots at all -
// a legitimate pre-slot estate - from one that disagrees with itself.
//
// The never-reuse guarantee this implements is bounded by what a stateless
// run can see. The high-water mark is computed from the live set, so a slot
// whose resource has been deleted is not remembered: shrink a count and grow
// it again and the retired slot comes back around. What that costs is an
// external tool that cached "slot 2 is eipalloc-abc" reading the cache as
// still true; what it does not cost is any decision stateless mode makes, because
// nothing here ever reads a slot from anywhere but a live tag in the run that
// is using it.
func Match(declared int, live []Live) (*Result, error) {
	if declared < 0 {
		return nil, fmt.Errorf("a declared count cannot be negative, got %d", declared)
	}

	if mode := Classify(live); mode == ModeMixed {
		return nil, MixedFrom(live)
	}

	parsed := make([]Bound, 0, len(live))
	bySlot := make(map[Slot][]string, len(live))
	for _, l := range live {
		slot, err := Parse(l.Slot)
		if err != nil {
			pe := err.(*ParseError) //nolint:errcheck,errorlint // Parse only ever returns *ParseError
			pe.ID = l.ID
			return nil, pe
		}
		parsed = append(parsed, Bound{Live: l, Slot: slot})
		bySlot[slot] = append(bySlot[slot], l.ID)
	}

	// Duplicates are reported all at once and in slot order, so an operator
	// with a badly hand-edited estate sees the whole picture rather than
	// fixing one collision to be told about the next.
	var dups []Duplicate
	for slot, ids := range bySlot {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		dups = append(dups, Duplicate{Slot: slot, IDs: ids})
	}
	if len(dups) > 0 {
		sort.Slice(dups, func(i, j int) bool { return dups[i].Slot < dups[j].Slot })
		return nil, &DuplicateError{Duplicates: dups}
	}

	// The whole of the ordering rule. Slots are unique by now, so this is a
	// total order and the pairing below is a function of the inputs alone.
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].Slot < parsed[j].Slot })

	res := &Result{Declared: declared}

	var haveHigh bool
	for _, b := range parsed {
		if !haveHigh || b.Slot > res.HighWater {
			res.HighWater = b.Slot
			haveHigh = true
		}
	}

	res.Slots = make([]Slot, declared)
	for i, b := range parsed {
		b.Index = i
		if i < declared {
			res.Bound = append(res.Bound, b)
			res.Slots[i] = b.Slot
			continue
		}
		res.Surplus = append(res.Surplus, b)
	}

	next := res.HighWater
	for i := len(parsed); i < declared; i++ {
		switch {
		case !haveHigh:
			// Nothing live at all: the set is being born, and MARKERS.md's
			// per-address counter starts at 0.
			next = 0
			haveHigh = true
		case next >= MaxSlot:
			return nil, fmt.Errorf("minting a slot for index %d would go past the ten-digit ceiling of %s; this estate has cycled through every slot the marker spec can express", i, MaxSlot)
		default:
			next++
		}
		res.Deficit = append(res.Deficit, Minted{Index: i, Slot: next})
		res.Slots[i] = next
		res.HighWater = next
	}

	return res, nil
}

// Sequential is the assignment a pre-slot count set migrates to: slot i for
// index i, for a set that is currently told apart by its per-instance
// tofu-address tags.
//
// It is the only assignment that preserves the binding the estate already
// has. Those addresses are the estate's existing identity for the set, and
// index k's address is the only thing that says which live resource is
// index k; freezing that as the slot changes nothing about which resource is
// which, and every run after it binds by slot instead. Any other assignment
// would be a permutation of the set dressed up as a migration.
func Sequential(declared int) []Slot {
	out := make([]Slot, declared)
	for i := range out {
		out[i] = Slot(i)
	}
	return out
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ParseError is a tofu-slot tag value that is not a slot.
type ParseError struct {
	// Value is the tag value as carried.
	Value string

	// ID is the live resource carrying it, when the caller supplied one.
	ID string

	// Reason is the clause naming what is wrong with it.
	Reason string
}

func (e *ParseError) Error() string {
	who := "a live resource"
	if e.ID != "" {
		who = e.ID
	}
	return fmt.Sprintf("%s carries the tofu-slot value %q, which is not a slot: %s", who, e.Value, e.Reason)
}

// Duplicate is one slot claimed by more than one live resource.
type Duplicate struct {
	Slot Slot
	IDs  []string
}

// DuplicateError is two or more live resources carrying the same slot.
type DuplicateError struct {
	Duplicates []Duplicate
}

func (e *DuplicateError) Error() string {
	parts := make([]string, 0, len(e.Duplicates))
	for _, d := range e.Duplicates {
		parts = append(parts, fmt.Sprintf("slot %s is claimed by %s", d.Slot, strings.Join(d.IDs, " and ")))
	}
	return strings.Join(parts, "; ")
}

// MixedError is a set where some live members carry a slot and some do not.
type MixedError struct {
	// Slotted and Bare are the live IDs on each side, sorted.
	Slotted []string
	Bare    []string
}

func (e *MixedError) Error() string {
	return fmt.Sprintf("%s carry a tofu-slot and %s do not",
		strings.Join(e.Slotted, ", "), strings.Join(e.Bare, ", "))
}

// MixedFrom builds the error for a set that disagrees with itself, so a
// caller that has already called [Classify] can report the two sides without
// running the match to be told what it already knows. It returns nil for a
// set that is not mixed.
func MixedFrom(live []Live) *MixedError {
	if Classify(live) != ModeMixed {
		return nil
	}
	return mixedError(live)
}

func mixedError(live []Live) *MixedError {
	e := &MixedError{}
	for _, l := range live {
		id := l.ID
		if id == "" {
			id = "(no identity)"
		}
		if l.Slot == "" {
			e.Bare = append(e.Bare, id)
			continue
		}
		e.Slotted = append(e.Slotted, id)
	}
	sort.Strings(e.Slotted)
	sort.Strings(e.Bare)
	return e
}
