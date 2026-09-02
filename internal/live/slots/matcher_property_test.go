// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package slots

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The adversarial audit threw twenty thousand random cases at Match and could
// not break it. That result was reported in prose and then lost: the cases
// were generated in a scratch file that no longer exists, so the only thing
// left of it was the audit's one-line note that the matcher held under
// attack. A property that held once and is not pinned is a property nobody will
// notice losing.
//
// This is that sweep, made permanent. It is deliberately a property test
// rather than more table cases: the table in slots_test.go already covers the
// shapes somebody thought of, and what the audit's sweep was worth was
// covering the shapes nobody did. Every property below is one sentence of the
// package doc, checked against inputs chosen to be awkward rather than
// representative — empty sets, scale-to-zero, scale-from-zero, sparse slots
// with large gaps, slots crowding the ceiling, and live sets shuffled into
// orders no cloud would return them in.

// matchProperties is every invariant Match promises, checked against one
// case. Each failure names the property, not just the mismatch, because the
// point of a random case is that nobody will recognize it on sight.
func matchProperties(t *testing.T, declared int, live []Live, res *Result) {
	t.Helper()

	what := fmt.Sprintf("declared=%d live=%s", declared, describeLive(live))

	// The set is partitioned: every live member is either bound or surplus,
	// exactly once. A member that fell out of both would be a resource the
	// plan simply stopped knowing about, and one in both would be kept and
	// destroyed in the same run.
	seen := map[string]int{}
	for _, b := range res.Bound {
		seen[b.Live.ID]++
	}
	for _, b := range res.Surplus {
		seen[b.Live.ID]++
	}
	for _, l := range live {
		if seen[l.ID] != 1 {
			t.Fatalf("%s: live member %s appears %d times across Bound+Surplus, want exactly 1", what, l.ID, seen[l.ID])
		}
	}
	if len(seen) != len(live) {
		t.Fatalf("%s: Bound+Surplus covers %d members, live has %d", what, len(seen), len(live))
	}

	// The declared count is covered exactly once: bound members plus minted
	// ones equal it, and Slots has one entry per index.
	if got := len(res.Bound) + len(res.Deficit); got != declared {
		t.Fatalf("%s: %d bound + %d minted = %d, want the declared count %d",
			what, len(res.Bound), len(res.Deficit), got, declared)
	}
	if len(res.Slots) != declared {
		t.Fatalf("%s: Slots has %d entries, want the declared count %d", what, len(res.Slots), declared)
	}

	// Indices are the contiguous range [0, declared), split between bound and
	// minted with no overlap and no hole.
	indices := make([]int, 0, declared)
	for _, b := range res.Bound {
		indices = append(indices, b.Index)
	}
	for _, m := range res.Deficit {
		indices = append(indices, m.Index)
	}
	sort.Ints(indices)
	for i, idx := range indices {
		if idx != i {
			t.Fatalf("%s: the covered indices are %v, want 0..%d contiguous", what, indices, declared-1)
		}
	}

	// Surplus is the HIGHEST slots, and nothing else. This is the property a
	// scale-down rests on: the audit's own count-scale-down assertion is
	// "the destroyed member is the one whose live tofu-slot is the highest",
	// and here it is as an invariant rather than one worked example.
	wantSurplus := len(live) - declared
	if wantSurplus < 0 {
		wantSurplus = 0
	}
	if len(res.Surplus) != wantSurplus {
		t.Fatalf("%s: %d surplus, want %d", what, len(res.Surplus), wantSurplus)
	}
	for _, s := range res.Surplus {
		for _, b := range res.Bound {
			if s.Slot <= b.Slot {
				t.Fatalf("%s: surplus slot %s is not above bound slot %s — a scale-down would delete the wrong member",
					what, s.Slot, b.Slot)
			}
		}
	}

	// Bound pairs ascending slot to ascending index, which is what makes the
	// binding a function of the markers rather than of list order.
	for i := 1; i < len(res.Bound); i++ {
		if res.Bound[i-1].Slot >= res.Bound[i].Slot {
			t.Fatalf("%s: Bound is not ascending by slot: %s then %s", what, res.Bound[i-1].Slot, res.Bound[i].Slot)
		}
		if res.Bound[i-1].Index >= res.Bound[i].Index {
			t.Fatalf("%s: Bound is not ascending by index: %d then %d", what, res.Bound[i-1].Index, res.Bound[i].Index)
		}
	}
	for i := 1; i < len(res.Surplus); i++ {
		if res.Surplus[i-1].Slot >= res.Surplus[i].Slot {
			t.Fatalf("%s: Surplus is not ascending by slot: %s then %s", what, res.Surplus[i-1].Slot, res.Surplus[i].Slot)
		}
	}

	// A minted slot is above every slot in play, including the surplus ones
	// about to be destroyed, and no two minted slots collide. Reusing a slot
	// that a still-live member carries is the one way this could attach a
	// create to somebody else's resource.
	inPlay := map[Slot]string{}
	for _, l := range live {
		s, err := Parse(l.Slot)
		if err != nil {
			t.Fatalf("%s: a live slot this test generated does not parse: %v", what, err)
		}
		inPlay[s] = l.ID
	}
	minted := map[Slot]bool{}
	for _, m := range res.Deficit {
		if id, clash := inPlay[m.Slot]; clash {
			t.Fatalf("%s: minted slot %s is already carried by live member %s", what, m.Slot, id)
		}
		if minted[m.Slot] {
			t.Fatalf("%s: minted slot %s twice", what, m.Slot)
		}
		minted[m.Slot] = true
		for s := range inPlay {
			if m.Slot <= s {
				t.Fatalf("%s: minted slot %s is not above the live slot %s", what, m.Slot, s)
			}
		}
	}

	// Slots is the assignment stamping writes: index i's slot, whether that
	// index bound or was minted.
	want := make([]Slot, declared)
	for _, b := range res.Bound {
		want[b.Index] = b.Slot
	}
	for _, m := range res.Deficit {
		want[m.Index] = m.Slot
	}
	for i := range want {
		if res.Slots[i] != want[i] {
			t.Fatalf("%s: Slots[%d] = %s, but index %d bound/minted %s", what, i, res.Slots[i], i, want[i])
		}
	}

	// HighWater is the greatest slot anywhere in the picture, which is what
	// the next mint counts from.
	var high Slot
	for s := range inPlay {
		if s > high {
			high = s
		}
	}
	for s := range minted {
		if s > high {
			high = s
		}
	}
	if res.HighWater != high {
		t.Errorf("%s: HighWater is %s, want %s", what, res.HighWater, high)
	}
}

// TestMatchPropertiesUnderRandomInput is the audit's twenty-thousand-case
// sweep, pinned. The seed is fixed so a failure is reproducible; the case
// generator is the part worth reading.
func TestMatchPropertiesUnderRandomInput(t *testing.T) {
	rng := rand.New(rand.NewSource(0xA4C0FFEE)) //nolint:gosec // test input generation, not cryptography

	const cases = 20000
	ceilings := 0
	for i := 0; i < cases; i++ {
		declared, live := randomCase(rng)

		res, err := Match(declared, live)
		if err != nil {
			// One refusal is legitimate over a well-formed set, and the
			// generator produces it on purpose: a set already crowding
			// MaxSlot with a deficit to mint has nowhere left to count to.
			// Refusing is the right answer - reusing a retired slot would
			// attach a create to a slot some external reader still has
			// cached - so the case asserts the refusal rather than skipping
			// it, and asserts that it happens ONLY when it must.
			if !ceilingExhausted(declared, live) {
				t.Fatalf("case %d (declared=%d live=%s): Match refused a well-formed set: %v",
					i, declared, describeLive(live), err)
			}
			if !strings.Contains(err.Error(), "ceiling") {
				t.Fatalf("case %d: the ceiling refusal does not say so: %v", i, err)
			}
			ceilings++
			continue
		}
		if ceilingExhausted(declared, live) {
			t.Fatalf("case %d (declared=%d live=%s): the slot space is exhausted and Match minted anyway: %s",
				i, declared, describeLive(live), renderResult(res))
		}
		matchProperties(t, declared, live, res)
	}
	t.Logf("%d random well-formed sets: every invariant held (%d of them correctly refused as slot-space exhausted)",
		cases, ceilings)
}

// TestMatchIsDeterministicUnderShuffling is the property the fixed table
// cannot express: the same live set in a different order is the same match.
// The cloud does not promise a list order, so anything that depended on one
// would be a binding that changes between runs for no reason a marker
// explains.
func TestMatchIsDeterministicUnderShuffling(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5107533)) //nolint:gosec // test input generation

	for i := 0; i < 2000; i++ {
		declared, live := randomCase(rng)
		if len(live) < 2 {
			continue
		}

		if ceilingExhausted(declared, live) {
			continue // legitimately refused; TestMatchPropertiesUnderRandomInput covers that
		}
		first, err := Match(declared, live)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}

		shuffled := append([]Live(nil), live...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		second, err := Match(declared, shuffled)
		if err != nil {
			t.Fatalf("case %d, shuffled: %v", i, err)
		}
		if a, b := renderResult(first), renderResult(second); a != b {
			t.Fatalf("case %d (declared=%d): the match depends on the order the live set arrived in\n  as listed: %s\n  shuffled:  %s",
				i, declared, a, b)
		}
	}
}

// TestMatchIsIdempotentUnderRandomInput: feeding a match's own assignment
// back in, as the next run's live set, must change nothing. slots_test.go
// checks this on one worked example; the property is what the audit's sweep
// was really testing, and a count that churned on every plan while nothing
// changed would be exactly the churn this mechanism exists to avoid.
func TestMatchIsIdempotentUnderRandomInput(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1DE3907)) //nolint:gosec // test input generation

	for i := 0; i < 2000; i++ {
		declared, live := randomCase(rng)

		if ceilingExhausted(declared, live) {
			continue // legitimately refused; TestMatchPropertiesUnderRandomInput covers that
		}
		first, err := Match(declared, live)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}

		// The world after an apply: the bound members stay as they are, the
		// minted ones exist now carrying the slot they were minted with, and
		// the surplus is gone.
		next := make([]Live, 0, declared)
		for _, b := range first.Bound {
			next = append(next, b.Live)
		}
		for _, m := range first.Deficit {
			next = append(next, Live{ID: "minted-" + m.Slot.String(), Slot: m.Slot.String()})
		}

		second, err := Match(declared, next)
		if err != nil {
			t.Fatalf("case %d, second pass: %v", i, err)
		}
		if len(second.Surplus) != 0 || len(second.Deficit) != 0 {
			t.Fatalf("case %d (declared=%d live=%s): a second match over the first one's own result is not a no-op: %s",
				i, declared, describeLive(live), renderResult(second))
		}
		if len(second.Slots) != len(first.Slots) {
			t.Fatalf("case %d: the assignment changed length between runs", i)
		}
		for j := range first.Slots {
			if first.Slots[j] != second.Slots[j] {
				t.Fatalf("case %d: index %d moved from slot %s to %s between runs",
					i, j, first.Slots[j], second.Slots[j])
			}
		}
	}
}

// TestMatchRefusesEveryMalformedSlotSpelling is the other half: Parse's
// grammar is strict on purpose, because a slot is compared as a number and
// written as a string, so two spellings of one number would be two members
// claiming one slot with nothing to point at as wrong. Every rejected
// spelling is checked through Match, not only through Parse, because Match is
// what a run actually calls.
func TestMatchRefusesEveryMalformedSlotSpelling(t *testing.T) {
	for _, bad := range []string{
		"007",                  // leading zeros: same number, different tag
		"0x1f",                 // another base
		"1e3",                  // scientific
		" 1",                   // leading space (AWS tag values allow it)
		"1 ",                   // trailing space
		"+1",                   // signed
		"-1",                   // negative
		"1.0",                  // decimal point
		"१",                    // a digit that is not an ASCII digit
		"4294967296",           // one past the ceiling
		"99999999999",          // over the digit cap
		"18446744073709551616", // past uint64 entirely
		"1_000",                // Go's own numeric separator
		"",                     // absent, in a set where others carry one
	} {
		t.Run(strconv.Quote(bad), func(t *testing.T) {
			live := []Live{{ID: "good", Slot: "0"}, {ID: "bad", Slot: bad}}
			if _, err := Match(2, live); err == nil {
				t.Fatalf("Match accepted the slot spelling %q", bad)
			} else if !strings.Contains(err.Error(), "bad") && !strings.Contains(err.Error(), "slot") {
				t.Errorf("the refusal of %q names neither the resource nor what a slot is: %v", bad, err)
			}
		})
	}
}

// TestMatchRefusesADuplicateAndNamesEveryClaimant: two members carrying one
// slot is not a thing to resolve. Which live resource is which member of the
// set is exactly what the markers were supposed to answer, so picking one
// would attach a plan to an arbitrary resource. The refusal has to name every
// claimant, or an operator fixes one collision and is told about the next.
func TestMatchRefusesADuplicateAndNamesEveryClaimant(t *testing.T) {
	live := []Live{
		{ID: "eip-c", Slot: "1"},
		{ID: "eip-a", Slot: "0"},
		{ID: "eip-b", Slot: "1"},
		{ID: "eip-d", Slot: "1"},
		{ID: "eip-e", Slot: "3"},
		{ID: "eip-f", Slot: "3"},
	}
	_, err := Match(4, live)
	if err == nil {
		t.Fatal("Match accepted a set with two duplicate slots")
	}
	msg := err.Error()
	for _, id := range []string{"eip-b", "eip-c", "eip-d", "eip-e", "eip-f"} {
		if !strings.Contains(msg, id) {
			t.Errorf("the refusal does not name %s, which claims a duplicated slot: %v", id, err)
		}
	}
	// Both collisions at once, not just the first one found.
	if !strings.Contains(msg, "1") || !strings.Contains(msg, "3") {
		t.Errorf("the refusal does not name both duplicated slots: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Case generation
// ---------------------------------------------------------------------------

// randomCase produces a declared count and a well-formed live set: unique
// slots, canonically spelled, under the ceiling. The distribution is chosen
// to hit the awkward shapes often rather than the average one — a uniform
// generator would almost never produce an empty set, a scale to zero, or a
// slot near MaxSlot, which are where the interesting failures live.
func randomCase(rng *rand.Rand) (int, []Live) {
	var declared int
	switch rng.Intn(6) {
	case 0:
		declared = 0 // scale to zero
	case 1:
		declared = 1
	case 2, 3:
		declared = rng.Intn(8)
	default:
		declared = rng.Intn(40)
	}

	var n int
	switch rng.Intn(6) {
	case 0:
		n = 0 // a fresh set: everything is minted
	case 1:
		n = declared // the steady state
	case 2:
		n = declared + 1 + rng.Intn(4) // a scale-down
	case 3:
		if declared > 0 {
			n = rng.Intn(declared) // a scale-up
		}
	default:
		n = rng.Intn(40)
	}

	// Slot values: dense from zero, sparse with big gaps, or crowding the
	// ceiling. The last one matters because minting counts upward from the
	// high-water mark, and a set already near MaxSlot is where that runs out
	// of room.
	used := make(map[Slot]bool, n)
	live := make([]Live, 0, n)
	shape := rng.Intn(3)
	var next Slot
	if shape == 2 && n > 0 {
		next = MaxSlot - Slot(rng.Intn(200)) - Slot(n)
	}
	for i := 0; i < n; i++ {
		var s Slot
		switch shape {
		case 0: // dense
			s = Slot(i)
		case 1: // sparse
			next += Slot(1 + rng.Intn(1000))
			s = next
		default: // near the ceiling
			next++
			s = next
		}
		if used[s] {
			continue
		}
		used[s] = true
		live = append(live, Live{ID: fmt.Sprintf("id-%d", i), Slot: s.String()})
	}

	// The cloud does not promise an order, so neither does this.
	rng.Shuffle(len(live), func(a, b int) { live[a], live[b] = live[b], live[a] })
	return declared, live
}

// ceilingExhausted says whether this case needs to mint a slot that cannot
// exist: a deficit to fill, and a high-water mark close enough to MaxSlot
// that counting up past it would leave the marker spec's ten-digit grammar.
// Minting counts from the high-water mark, never from a gap below it, because
// a slot below the mark may be one an external reader still has cached
// against a resource that has since been deleted.
func ceilingExhausted(declared int, live []Live) bool {
	need := declared - len(live)
	if need <= 0 || len(live) == 0 {
		return false
	}
	var high Slot
	for _, l := range live {
		s, err := Parse(l.Slot)
		if err != nil {
			return false
		}
		if s > high {
			high = s
		}
	}
	return uint64(high)+uint64(need) > uint64(MaxSlot)
}

func describeLive(live []Live) string {
	if len(live) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(live))
	for _, l := range live {
		parts = append(parts, l.ID+"@"+l.Slot)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// renderResult is a canonical rendering of a whole result, for comparing two
// matches of the same set. Deliberately its own function rather than
// slots_test.go's render, which takes a *testing.T and covers less: this one
// includes Slots, and the shuffling property is about that assignment above
// all.
func renderResult(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "declared=%d high=%s bound=[", r.Declared, r.HighWater)
	for _, x := range r.Bound {
		fmt.Fprintf(&b, "%d:%s:%s ", x.Index, x.Slot, x.Live.ID)
	}
	b.WriteString("] surplus=[")
	for _, x := range r.Surplus {
		fmt.Fprintf(&b, "%d:%s:%s ", x.Index, x.Slot, x.Live.ID)
	}
	b.WriteString("] minted=[")
	for _, x := range r.Deficit {
		fmt.Fprintf(&b, "%d:%s ", x.Index, x.Slot)
	}
	b.WriteString("] slots=[")
	for _, s := range r.Slots {
		fmt.Fprintf(&b, "%s ", s)
	}
	b.WriteString("]")
	return b.String()
}
