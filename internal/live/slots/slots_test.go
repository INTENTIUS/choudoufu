// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package slots

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The tag value grammar
// ---------------------------------------------------------------------------

func TestParse(t *testing.T) {
	valid := map[string]Slot{
		"0":          0,
		"1":          1,
		"9":          9,
		"10":         10,
		"4294967295": MaxSlot,
	}
	for in, want := range valid {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) errored: %s", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", in, got, want)
		}
		if got.String() != in {
			t.Errorf("Parse/String does not round-trip %q: got %q", in, got.String())
		}
	}

	invalid := []string{
		"",           // MARKERS.md: a slot is present or the marker is not
		"00",         // the spec spells zero as "0"
		"007",        // leading zeros would give one number two spellings
		"-1",         // unsigned
		"1.0",        //
		"1e3",        //
		" 1",         //
		"1 ",         //
		"0x10",       //
		"4294967296", // one past the ceiling
		"99999999999",
	}
	for _, in := range invalid {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %d, want an error", in, got)
		}
	}
}

// TestParseRejectsLeadingZeros is the reason the grammar is strict, spelled
// out: two spellings of one number in a set would be two members claiming one
// slot, with nothing in the tag values to point at as the wrong one.
func TestParseRejectsLeadingZeros(t *testing.T) {
	_, err := Parse("07")
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Parse(\"07\") returned %v, want a *ParseError", err)
	}
	if !strings.Contains(pe.Error(), "leading zero") {
		t.Errorf("the error does not say what is wrong: %s", pe)
	}
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		live []Live
		want Mode
	}{
		{"nothing live", nil, ModeEmpty},
		{"all slotted", []Live{{ID: "a", Slot: "0"}, {ID: "b", Slot: "1"}}, ModeAll},
		{"none slotted", []Live{{ID: "a"}, {ID: "b"}}, ModeNone},
		{"mixed", []Live{{ID: "a", Slot: "0"}, {ID: "b"}}, ModeMixed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.live); got != c.want {
				t.Errorf("Classify = %v, want %v", got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The set matcher
// ---------------------------------------------------------------------------

// TestMatchEqual: as many live members as declared instances, so every index
// binds and nothing is minted or retired.
func TestMatchEqual(t *testing.T) {
	res := mustMatch(t, 3, live("eipalloc-a", "0"), live("eipalloc-b", "1"), live("eipalloc-c", "2"))

	assertBound(t, res, map[int]string{0: "eipalloc-a", 1: "eipalloc-b", 2: "eipalloc-c"})
	if len(res.Surplus) != 0 || len(res.Deficit) != 0 {
		t.Errorf("an equal match produced surplus %v / deficit %v", res.Surplus, res.Deficit)
	}
	assertSlots(t, res, "0", "1", "2")
	if res.HighWater != 2 {
		t.Errorf("high water is %s, want 2", res.HighWater)
	}
}

// TestMatchIsBySlotNotByListOrder: the slots are not the list order and not
// contiguous, and the pairing follows the slots.
func TestMatchIsBySlotNotByListOrder(t *testing.T) {
	res := mustMatch(t, 3, live("eipalloc-c", "9"), live("eipalloc-a", "1"), live("eipalloc-b", "4"))

	assertBound(t, res, map[int]string{0: "eipalloc-a", 1: "eipalloc-b", 2: "eipalloc-c"})
	assertSlots(t, res, "1", "4", "9")
}

// TestMatchComparesNumerically is MARKERS.md's explicit warning: slots are
// carried as strings and "9" is below "10" even though it sorts after it.
func TestMatchComparesNumerically(t *testing.T) {
	res := mustMatch(t, 3, live("ten", "10"), live("nine", "9"), live("hundred", "100"))

	assertBound(t, res, map[int]string{0: "nine", 1: "ten", 2: "hundred"})
}

// TestMatchSurplusIsTheHighestSlots is the scale-down rule and the whole of
// the no-churn claim: 3 live, 2 declared, and the survivors do not move.
func TestMatchSurplusIsTheHighestSlots(t *testing.T) {
	full := mustMatch(t, 3, live("eipalloc-a", "0"), live("eipalloc-b", "1"), live("eipalloc-c", "2"))
	shrunk := mustMatch(t, 2, live("eipalloc-a", "0"), live("eipalloc-b", "1"), live("eipalloc-c", "2"))

	if len(shrunk.Surplus) != 1 {
		t.Fatalf("want exactly one surplus member, got %v", shrunk.Surplus)
	}
	s := shrunk.Surplus[0]
	if s.Live.ID != "eipalloc-c" || s.Slot != 2 {
		t.Errorf("surplus is %s (slot %s), want eipalloc-c at slot 2", s.Live.ID, s.Slot)
	}
	if s.Index != 2 {
		t.Errorf("the surplus member sits at index %d, want 2 - the first index past the declared count", s.Index)
	}

	// No churn: every survivor is on the index it already occupied.
	for i, b := range shrunk.Bound {
		if full.Bound[i].Live.ID != b.Live.ID {
			t.Errorf("index %d moved from %s to %s across the scale-down", i, full.Bound[i].Live.ID, b.Live.ID)
		}
	}
	if len(shrunk.Deficit) != 0 {
		t.Errorf("a scale-down minted slots: %v", shrunk.Deficit)
	}
}

// TestMatchSurplusOrderIsAscending: several surplus members land at the
// consecutive indices above the declared count, lowest slot first, which is
// the shape prior state needs for the plan engine to see them as orphans.
func TestMatchSurplusOrderIsAscending(t *testing.T) {
	res := mustMatch(t, 1, live("a", "0"), live("b", "5"), live("c", "3"))

	if len(res.Surplus) != 2 {
		t.Fatalf("want two surplus members, got %v", res.Surplus)
	}
	if res.Surplus[0].Live.ID != "c" || res.Surplus[0].Index != 1 {
		t.Errorf("first surplus is %s at %d, want c at 1", res.Surplus[0].Live.ID, res.Surplus[0].Index)
	}
	if res.Surplus[1].Live.ID != "b" || res.Surplus[1].Index != 2 {
		t.Errorf("second surplus is %s at %d, want b at 2", res.Surplus[1].Live.ID, res.Surplus[1].Index)
	}
}

// TestMatchScaleToZero: `count = var.enabled ? 1 : 0` flipped to false. Every
// live member is surplus, starting at index 0.
func TestMatchScaleToZero(t *testing.T) {
	res := mustMatch(t, 0, live("only", "7"))

	if len(res.Bound) != 0 || len(res.Deficit) != 0 {
		t.Errorf("a zero count bound or minted something: %v / %v", res.Bound, res.Deficit)
	}
	if len(res.Surplus) != 1 || res.Surplus[0].Index != 0 {
		t.Fatalf("want the one live member surplus at index 0, got %v", res.Surplus)
	}
}

// TestMatchDeficitMintsAboveTheHighWaterMark: fewer live than declared, so
// the empty indices plan as creates and each carries a fresh slot.
func TestMatchDeficitMintsAboveTheHighWaterMark(t *testing.T) {
	res := mustMatch(t, 4, live("a", "0"), live("b", "5"))

	assertBound(t, res, map[int]string{0: "a", 1: "b"})
	if len(res.Deficit) != 2 {
		t.Fatalf("want two minted slots, got %v", res.Deficit)
	}
	if res.Deficit[0].Index != 2 || res.Deficit[0].Slot != 6 {
		t.Errorf("first mint is index %d slot %s, want index 2 slot 6", res.Deficit[0].Index, res.Deficit[0].Slot)
	}
	if res.Deficit[1].Index != 3 || res.Deficit[1].Slot != 7 {
		t.Errorf("second mint is index %d slot %s, want index 3 slot 7", res.Deficit[1].Index, res.Deficit[1].Slot)
	}
	assertSlots(t, res, "0", "5", "6", "7")
	if res.HighWater != 7 {
		t.Errorf("high water after minting is %s, want 7", res.HighWater)
	}
}

// TestMatchMintingNeverReusesALiveSlot is the never-reuse rule as far as a
// stateless run can enforce it: nothing minted may collide with anything
// live, including a member that is on its way out.
func TestMatchMintingNeverReusesALiveSlot(t *testing.T) {
	// One live member holding a high slot, and three indices to fill.
	res := mustMatch(t, 3, live("survivor", "12"))

	seen := map[Slot]bool{12: true}
	for _, d := range res.Deficit {
		if seen[d.Slot] {
			t.Errorf("minted slot %s is already in use", d.Slot)
		}
		seen[d.Slot] = true
		if d.Slot <= 12 {
			t.Errorf("minted slot %s is not above the high-water mark of 12", d.Slot)
		}
	}
}

// TestMatchMintsFromZeroForANewSet: nothing live at all, so the counter
// MARKERS.md describes starts where it says it starts.
func TestMatchMintsFromZeroForANewSet(t *testing.T) {
	res := mustMatch(t, 3)

	assertSlots(t, res, "0", "1", "2")
	if len(res.Bound) != 0 || len(res.Surplus) != 0 {
		t.Errorf("an empty live set bound something: %v / %v", res.Bound, res.Surplus)
	}
}

// TestMatchIsDeterministic: the same set in any order produces the same
// answer, which is what makes a plan reproducible over a fungible set.
func TestMatchIsDeterministic(t *testing.T) {
	base := []Live{live("a", "3"), live("b", "0"), live("c", "11"), live("d", "7"), live("e", "2")}
	want := render(t, mustMatchSlice(t, 3, base))

	rng := rand.New(rand.NewSource(1)) //nolint:gosec // shuffling test input, not cryptography
	for i := 0; i < 50; i++ {
		shuffled := append([]Live(nil), base...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got := render(t, mustMatchSlice(t, 3, shuffled)); got != want {
			t.Fatalf("shuffle %d produced a different match:\n got %s\nwant %s", i, got, want)
		}
	}
}

// TestMatchIsIdempotentOverItsOwnOutput: match, apply the answer, match
// again. A stable binding means the second run proposes nothing.
func TestMatchIsIdempotentOverItsOwnOutput(t *testing.T) {
	first := mustMatch(t, 4, live("a", "0"), live("b", "1"))

	// The two creates happened; the estate now has four members carrying the
	// slots the first match handed out.
	var after []Live
	for i, s := range first.Slots {
		after = append(after, Live{ID: fmt.Sprintf("member-%d", i), Slot: s.String()})
	}
	second := mustMatchSlice(t, 4, after)

	if len(second.Deficit) != 0 || len(second.Surplus) != 0 {
		t.Errorf("the second match is not a no-op: deficit %v, surplus %v", second.Deficit, second.Surplus)
	}
	for i := range first.Slots {
		if second.Slots[i] != first.Slots[i] {
			t.Errorf("index %d's slot moved from %s to %s", i, first.Slots[i], second.Slots[i])
		}
	}
}

// ---------------------------------------------------------------------------
// What the matcher refuses
// ---------------------------------------------------------------------------

func TestMatchRejectsMixedSlots(t *testing.T) {
	_, err := Match(2, []Live{live("slotted", "0"), live("bare", "")})

	var mixed *MixedError
	if !errors.As(err, &mixed) {
		t.Fatalf("Match returned %v, want a *MixedError", err)
	}
	if len(mixed.Slotted) != 1 || mixed.Slotted[0] != "slotted" {
		t.Errorf("the error's slotted side is %v", mixed.Slotted)
	}
	if len(mixed.Bare) != 1 || mixed.Bare[0] != "bare" {
		t.Errorf("the error's bare side is %v", mixed.Bare)
	}
}

func TestMatchRejectsDuplicateSlots(t *testing.T) {
	_, err := Match(3, []Live{live("a", "1"), live("b", "1"), live("c", "2")})

	var dup *DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("Match returned %v, want a *DuplicateError", err)
	}
	if len(dup.Duplicates) != 1 || dup.Duplicates[0].Slot != 1 {
		t.Fatalf("the error does not name slot 1: %v", dup.Duplicates)
	}
	if got := dup.Duplicates[0].IDs; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("the duplicate names %v, want both claimants sorted", got)
	}
}

func TestMatchRejectsAMalformedSlot(t *testing.T) {
	_, err := Match(2, []Live{live("a", "0"), live("b", "one")})

	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Match returned %v, want a *ParseError", err)
	}
	if pe.ID != "b" {
		t.Errorf("the error blames %q, want b", pe.ID)
	}
}

func TestMatchRejectsANegativeCount(t *testing.T) {
	if _, err := Match(-1, nil); err == nil {
		t.Error("a negative count was accepted")
	}
}

func TestMatchRefusesToMintPastTheCeiling(t *testing.T) {
	_, err := Match(2, []Live{live("a", MaxSlot.String())})
	if err == nil {
		t.Fatal("minting past the ten-digit ceiling was allowed")
	}
	if !strings.Contains(err.Error(), MaxSlot.String()) {
		t.Errorf("the error does not name the ceiling: %s", err)
	}
}

// ---------------------------------------------------------------------------
// The migration assignment
// ---------------------------------------------------------------------------

// TestSequentialPreservesTheExistingBinding: the migration a pre-slot estate
// makes has to be the identity permutation, or it is a rename of every member
// of the set dressed up as adding a tag.
func TestSequentialPreservesTheExistingBinding(t *testing.T) {
	got := Sequential(4)
	if len(got) != 4 {
		t.Fatalf("Sequential(4) has %d entries", len(got))
	}
	for i, s := range got {
		if s != Slot(i) {
			t.Errorf("index %d migrates to slot %s, want %d", i, s, i)
		}
	}

	// And once migrated, matching it back reproduces exactly the same
	// binding - which is what "the migration moves nothing" means.
	var live []Live
	for i, s := range got {
		live = append(live, Live{ID: fmt.Sprintf("member-%d", i), Slot: s.String()})
	}
	res := mustMatchSlice(t, 4, live)
	for i, b := range res.Bound {
		if b.Live.ID != fmt.Sprintf("member-%d", i) {
			t.Errorf("after migration index %d holds %s", i, b.Live.ID)
		}
	}

	if len(Sequential(0)) != 0 {
		t.Error("Sequential(0) is not empty")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func live(id, slot string) Live { return Live{ID: id, Slot: slot} }

func mustMatch(t *testing.T, declared int, set ...Live) *Result {
	t.Helper()
	return mustMatchSlice(t, declared, set)
}

func mustMatchSlice(t *testing.T, declared int, set []Live) *Result {
	t.Helper()
	res, err := Match(declared, set)
	if err != nil {
		t.Fatalf("Match(%d, %v): %s", declared, set, err)
	}
	return res
}

func assertBound(t *testing.T, res *Result, want map[int]string) {
	t.Helper()
	if len(res.Bound) != len(want) {
		t.Fatalf("bound %v, want %d entries", res.Bound, len(want))
	}
	for _, b := range res.Bound {
		id, ok := want[b.Index]
		if !ok {
			t.Errorf("index %d bound unexpectedly, to %s", b.Index, b.Live.ID)
			continue
		}
		if b.Live.ID != id {
			t.Errorf("index %d bound to %s, want %s", b.Index, b.Live.ID, id)
		}
	}
}

func assertSlots(t *testing.T, res *Result, want ...string) {
	t.Helper()
	if len(res.Slots) != len(want) {
		t.Fatalf("Slots is %v, want %d entries", res.Slots, len(want))
	}
	for i, w := range want {
		if res.Slots[i].String() != w {
			t.Errorf("index %d is assigned slot %s, want %s", i, res.Slots[i], w)
		}
	}
}

// render is a whole result as one comparable string.
func render(t *testing.T, res *Result) string {
	t.Helper()
	var b strings.Builder
	for _, x := range res.Bound {
		fmt.Fprintf(&b, "bound %d=%s(%s);", x.Index, x.Live.ID, x.Slot)
	}
	for _, x := range res.Surplus {
		fmt.Fprintf(&b, "surplus %d=%s(%s);", x.Index, x.Live.ID, x.Slot)
	}
	for _, x := range res.Deficit {
		fmt.Fprintf(&b, "mint %d=%s;", x.Index, x.Slot)
	}
	fmt.Fprintf(&b, "high=%s", res.HighWater)
	return b.String()
}
