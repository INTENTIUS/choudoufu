// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
	"github.com/opentofu/opentofu/internal/stateless/identity"
	"github.com/opentofu/opentofu/internal/tfdiags"
)

// countDir is a fixture whose count is a variable, so one configuration can
// be discovered at three, two and four instances - which is the only way to
// make a claim about what a change in cardinality does.
const countDir = "testdata/count-pool"

const countEstate = "count-e2e"

// ---------------------------------------------------------------------------
// Binding by slot
// ---------------------------------------------------------------------------

// TestCountBindsBySlot is the happy path: as many live members as declared
// instances, matched by slot and not by the order the provider listed them.
func TestCountBindsBySlot(t *testing.T) {
	cloud := newFakeCloud()
	// Deliberately listed out of slot order, and with the address tags
	// agreeing with the slots so nothing else is in play.
	cloud.slotted("eipalloc-c", "2")
	cloud.slotted("eipalloc-a", "0")
	cloud.slotted("eipalloc-b", "1")

	res, diags := discoverCount(t, cloud, 3)
	assertNoErrors(t, diags)

	assertCountBindings(t, res, "eipalloc-a", "eipalloc-b", "eipalloc-c")
	if len(res.Surplus) != 0 || len(res.Unbound) != 0 || len(res.Orphans) != 0 {
		t.Errorf("an exact match produced leftovers:\n%s", res)
	}
	for _, b := range res.Bindings {
		if !b.SlotBound {
			t.Errorf("%s bound by address even though the set carries slots", b)
		}
		if b.AddressStale {
			t.Errorf("%s reports a stale address even though its tag agrees with its index", b)
		}
	}
	assertSlotTable(t, res, map[string]string{
		"aws_eip.pool:0": "0", "aws_eip.pool:1": "1", "aws_eip.pool:2": "2",
	})
	for _, s := range res.Slots {
		if s.Origin != SlotCarried {
			t.Errorf("%s: an already-slotted member's slot is reported as %s", s.Addr, s.Origin)
		}
	}
}

// TestCountSlotsAreComparedNumerically: MARKERS.md is explicit that "9" is a
// lower slot than "10" even though it sorts after it as a string, and a
// matcher that got this wrong would silently permute the whole set.
func TestCountSlotsAreComparedNumerically(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-ten", "10")
	cloud.slotted("eipalloc-nine", "9")
	cloud.slotted("eipalloc-hundred", "100")

	res, diags := discoverCount(t, cloud, 3)
	assertNoErrors(t, diags)

	assertCountBindings(t, res, "eipalloc-nine", "eipalloc-ten", "eipalloc-hundred")
}

// TestCountScaleDownDeletesTheHighestSlot is the P3.5 acceptance in miniature:
// three live, two declared, one leftover and the two survivors exactly where
// they were.
func TestCountScaleDownDeletesTheHighestSlot(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-a", "0")
	cloud.slotted("eipalloc-b", "1")
	cloud.slotted("eipalloc-c", "2")

	res, diags := discoverCount(t, cloud, 2)
	assertNoErrors(t, diags)

	// No churn: the survivors are on the indices they already held.
	assertCountBindings(t, res, "eipalloc-a", "eipalloc-b")

	// The surplus is the highest slot, and it sits at the first instance
	// address past the declared count - which is exactly where a shrunken
	// count's leftovers appear in a stock run's prior state.
	if len(res.Surplus) != 1 {
		t.Fatalf("want exactly one surplus member:\n%s", res)
	}
	s := res.Surplus[0]
	if s.ImportID != "eipalloc-c" {
		t.Errorf("the surplus member is %s, want the highest slot eipalloc-c", s.ImportID)
	}
	if s.Slot != "2" {
		t.Errorf("the surplus member carries slot %q, want 2", s.Slot)
	}
	if got := s.Addr.String(); got != "aws_eip.pool[2]" {
		t.Errorf("the surplus member sits at %s, want aws_eip.pool[2]", got)
	}
	if !s.Surplus || !s.SlotBound {
		t.Errorf("the surplus binding does not say what it is: %s", s)
	}

	// And it reaches the projection: a concrete resolution at that address is
	// the whole mechanism by which the plan proposes destroying it.
	found := false
	for _, r := range res.Resolutions {
		if r.Addr.String() != "aws_eip.pool[2]" {
			continue
		}
		found = true
		if r.Class != identity.ClassConcrete || r.ImportID != "eipalloc-c" {
			t.Errorf("the surplus resolution is %s, want CONCRETE eipalloc-c", r)
		}
	}
	if !found {
		t.Errorf("the surplus member is not in the merged resolutions, so nothing will destroy it:\n%s", res)
	}

	// It is not an orphan and not a binding of a declared instance: those two
	// lists are about addresses the configuration declares.
	if len(res.Orphans) != 0 {
		t.Errorf("the surplus member was also reported as an orphan:\n%s", res)
	}
	if len(res.Bindings) != 2 {
		t.Errorf("Bindings holds %d entries, want the two declared instances", len(res.Bindings))
	}
	assertSlotTable(t, res, map[string]string{"aws_eip.pool:0": "0", "aws_eip.pool:1": "1"})
}

// TestCountScaleUpMintsAboveTheHighWaterMark: two live members holding slots
// 0 and 3, four declared, so the two new indices carry 4 and 5 - never 1 or
// 2, which nothing live can prove are free of an ordering the estate still
// depends on.
func TestCountScaleUpMintsAboveTheHighWaterMark(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-a", "0")
	cloud.slotted("eipalloc-b", "3")

	res, diags := discoverCount(t, cloud, 4)
	assertNoErrors(t, diags)

	assertCountBindings(t, res, "eipalloc-a", "eipalloc-b")

	var unbound []string
	for _, a := range res.Unbound {
		unbound = append(unbound, a.String())
	}
	if strings.Join(unbound, ",") != "aws_eip.pool[2],aws_eip.pool[3]" {
		t.Errorf("unbound is %v, want the two indices with no live member", unbound)
	}

	assertSlotTable(t, res, map[string]string{
		"aws_eip.pool:0": "0", "aws_eip.pool:1": "3",
		"aws_eip.pool:2": "4", "aws_eip.pool:3": "5",
	})
	for _, s := range res.Slots {
		want := SlotCarried
		if s.Addr.String() == "aws_eip.pool[2]" || s.Addr.String() == "aws_eip.pool[3]" {
			want = SlotMinted
		}
		if s.Origin != want {
			t.Errorf("%s's slot origin is %s, want %s", s.Addr, s.Origin, want)
		}
	}
}

// TestCountMintingDoesNotReuseASurplusSlot: shrink and grow in one breath.
// The high-water mark counts the member on its way out, so the arriving
// member cannot be handed the departing one's slot.
func TestCountMintingDoesNotReuseASurplusSlot(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-a", "0")
	cloud.slotted("eipalloc-gone", "9")

	// Two declared: one binds, one mints. The live member holding 9 binds
	// too, so there is no surplus here - the point is the mark.
	res, diags := discoverCount(t, cloud, 3)
	assertNoErrors(t, diags)

	table := res.SlotTable()
	if got := table["aws_eip.pool:2"]; got != "10" {
		t.Errorf("the minted slot is %q, want 10 - one past the highest live slot", got)
	}
}

// TestCountFreshSetMintsFromZero: nothing live at all. Every index plans as a
// create and carries a slot from birth, counting from zero the way MARKERS.md
// says the per-address counter starts.
func TestCountFreshSetMintsFromZero(t *testing.T) {
	res, diags := discoverCount(t, newFakeCloud(), 3)
	assertNoErrors(t, diags)

	if len(res.Bindings) != 0 || len(res.Surplus) != 0 {
		t.Errorf("an empty cloud produced bindings:\n%s", res)
	}
	if len(res.Unbound) != 3 {
		t.Errorf("want three unbound instances:\n%s", res)
	}
	assertSlotTable(t, res, map[string]string{
		"aws_eip.pool:0": "0", "aws_eip.pool:1": "1", "aws_eip.pool:2": "2",
	})
	for _, s := range res.Slots {
		if s.Origin != SlotMinted {
			t.Errorf("%s's slot origin is %s, want MINTED", s.Addr, s.Origin)
		}
	}
}

// ---------------------------------------------------------------------------
// Slots take precedence, and addresses follow
// ---------------------------------------------------------------------------

// TestCountSlotBindingIgnoresStaleAddresses is the targeted-destroy shape: the
// lowest slot was deleted out of band, so the survivors' address tags name
// indices they no longer occupy. The slots bind them anyway, and the stale
// addresses are reported rather than obeyed.
func TestCountSlotBindingIgnoresStaleAddresses(t *testing.T) {
	cloud := newFakeCloud()
	// Slots 1 and 2 survive; their address tags still say pool:1 and pool:2.
	cloud.slottedAt("eipalloc-b", "1", "aws_eip.pool:1")
	cloud.slottedAt("eipalloc-c", "2", "aws_eip.pool:2")

	res, diags := discoverCount(t, cloud, 2)
	assertNoErrors(t, diags)

	assertCountBindings(t, res, "eipalloc-b", "eipalloc-c")
	for _, b := range res.Bindings {
		if !b.AddressStale {
			t.Errorf("%s bound to an index its address tag does not name, without saying so", b)
		}
	}
	// The stale addresses must not produce a second identity for the same
	// resource: not an orphan, not a collision, not an unbound instance.
	if len(res.Orphans) != 0 || len(res.Problems) != 0 || len(res.Unbound) != 0 {
		t.Errorf("a stale address was treated as a fact about ownership:\n%s", res)
	}
	// The assignment carries the slots the members already hold, so the plan
	// rewrites the addresses and leaves the slots alone.
	assertSlotTable(t, res, map[string]string{"aws_eip.pool:0": "1", "aws_eip.pool:1": "2"})
}

// TestCountSlotTakesPrecedenceOverAddress: every member carries both markers
// and they disagree about the order. The slot decides, because the address of
// a count instance is a position in an expansion and the slot is the member's
// name.
func TestCountSlotTakesPrecedenceOverAddress(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slottedAt("eipalloc-first", "0", "aws_eip.pool:2")
	cloud.slottedAt("eipalloc-second", "1", "aws_eip.pool:0")
	cloud.slottedAt("eipalloc-third", "2", "aws_eip.pool:1")

	res, diags := discoverCount(t, cloud, 3)
	assertNoErrors(t, diags)

	assertCountBindings(t, res, "eipalloc-first", "eipalloc-second", "eipalloc-third")
	for _, b := range res.Bindings {
		if !b.SlotBound || !b.AddressStale {
			t.Errorf("%s did not bind by slot over a disagreeing address", b)
		}
	}
}

// TestCountBlockAddressBindsWhenSlotted is the condition
// ProblemNeedsSlotMarkers describes, resolved: three live resources sharing
// the bare block address bind cleanly once they carry slots.
func TestCountBlockAddressBindsWhenSlotted(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slottedAt("eipalloc-a", "0", "aws_eip.pool")
	cloud.slottedAt("eipalloc-b", "1", "aws_eip.pool")
	cloud.slottedAt("eipalloc-c", "2", "aws_eip.pool")

	res, diags := discoverCount(t, cloud, 3)
	assertNoErrors(t, diags)

	assertCountBindings(t, res, "eipalloc-a", "eipalloc-b", "eipalloc-c")
	if len(res.ProblemsOfKind(ProblemNeedsSlotMarkers)) != 0 {
		t.Errorf("slot markers were stamped and the condition they resolve still fires:\n%s", res)
	}
}

// ---------------------------------------------------------------------------
// The compatibility path
// ---------------------------------------------------------------------------

// TestCountAddressFallback: no member carries a slot, so the per-instance
// addresses bind exactly as they did before slots existed - and the one new
// thing is the migration assignment, which freezes the binding the addresses
// already express.
func TestCountAddressFallback(t *testing.T) {
	cloud := newFakeCloud()
	cloud.addressed("eipalloc-a", "aws_eip.pool:0")
	cloud.addressed("eipalloc-b", "aws_eip.pool:1")
	cloud.addressed("eipalloc-c", "aws_eip.pool:2")

	res, diags := discoverCount(t, cloud, 3)
	assertNoErrors(t, diags)

	assertCountBindings(t, res, "eipalloc-a", "eipalloc-b", "eipalloc-c")
	for _, b := range res.Bindings {
		if b.SlotBound {
			t.Errorf("%s claims to have bound by slot, but nothing carries one", b)
		}
	}
	assertSlotTable(t, res, map[string]string{
		"aws_eip.pool:0": "0", "aws_eip.pool:1": "1", "aws_eip.pool:2": "2",
	})
	for _, s := range res.Slots {
		if s.Origin != SlotMigrated {
			t.Errorf("%s's slot origin is %s, want MIGRATED", s.Addr, s.Origin)
		}
	}
}

// TestCountAddressFallbackUnchangedOnShrink pins the compatibility promise:
// with no slots, a shrunken count behaves exactly as it did before this task -
// the leftover is an owned resource with no declared address, which is
// removal planning's business, and not a surplus member with a destroy
// waiting for it.
func TestCountAddressFallbackUnchangedOnShrink(t *testing.T) {
	cloud := newFakeCloud()
	cloud.addressed("eipalloc-a", "aws_eip.pool:0")
	cloud.addressed("eipalloc-b", "aws_eip.pool:1")
	cloud.addressed("eipalloc-c", "aws_eip.pool:2")

	res, diags := discoverCount(t, cloud, 2)
	assertNoErrors(t, diags)

	assertCountBindings(t, res, "eipalloc-a", "eipalloc-b")
	if len(res.Surplus) != 0 {
		t.Errorf("a slotless set produced a surplus member, which would propose a destroy no marker justifies:\n%s", res)
	}
	if len(res.Orphans) != 1 || res.Orphans[0].ImportID != "eipalloc-c" {
		t.Fatalf("want the third EIP reported as an orphan:\n%s", res)
	}
	if res.Orphans[0].Normalized != "aws_eip.pool:2" {
		t.Errorf("the orphan does not carry the address it claims: %s", res.Orphans[0])
	}
}

// TestCountAddressFallbackPartial: an estate whose count grew but whose new
// instance was never applied. The addresses that exist bind, the one that
// does not plans as a create, and the migration covers both.
func TestCountAddressFallbackPartial(t *testing.T) {
	cloud := newFakeCloud()
	cloud.addressed("eipalloc-a", "aws_eip.pool:0")
	cloud.addressed("eipalloc-c", "aws_eip.pool:2")

	res, diags := discoverCount(t, cloud, 3)
	assertNoErrors(t, diags)

	if _, ok := res.BindingFor(mustAddr(t, "aws_eip.pool[0]")); !ok {
		t.Errorf("index 0 did not bind:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, "aws_eip.pool[2]")); !ok {
		t.Errorf("index 2 did not bind:\n%s", res)
	}
	if len(res.Unbound) != 1 || res.Unbound[0].String() != "aws_eip.pool[1]" {
		t.Errorf("unbound is %v, want only index 1:\n%s", res.Unbound, res)
	}
	assertSlotTable(t, res, map[string]string{
		"aws_eip.pool:0": "0", "aws_eip.pool:1": "1", "aws_eip.pool:2": "2",
	})
}

// ---------------------------------------------------------------------------
// What the count path refuses
// ---------------------------------------------------------------------------

// TestCountMixedSlotsIsNamed: half the set stamped, half not - which is what a
// partially applied migration looks like. Two answers to "which member is
// this", so the run stops rather than picking one.
func TestCountMixedSlotsIsNamed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-a", "0")
	cloud.addressed("eipalloc-b", "aws_eip.pool:1")
	cloud.addressed("eipalloc-c", "aws_eip.pool:2")

	res, diags := discoverCount(t, cloud, 3)
	if !diags.HasErrors() {
		t.Fatalf("a half-migrated set produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemMixedSlots)
	if len(problems) != 1 {
		t.Fatalf("want one mixed-slots problem:\n%s", res)
	}
	if !strings.Contains(problems[0].Detail, "eipalloc-a") || !strings.Contains(problems[0].Detail, "eipalloc-b") {
		t.Errorf("the problem does not name both sides: %s", problems[0].Detail)
	}
	if len(res.Bindings) != 0 || len(res.Surplus) != 0 {
		t.Errorf("something bound anyway:\n%s", res)
	}
}

// TestCountDuplicateSlotIsNamed: two members claiming one slot is the
// ownership collision of a fungible set.
func TestCountDuplicateSlotIsNamed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-a", "0")
	cloud.slotted("eipalloc-b", "1")
	cloud.slotted("eipalloc-dup", "1")

	res, diags := discoverCount(t, cloud, 3)
	if !diags.HasErrors() {
		t.Fatalf("a duplicated slot produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemDuplicateSlot)
	if len(problems) != 1 {
		t.Fatalf("want one duplicate-slot problem:\n%s", res)
	}
	if !strings.Contains(problems[0].Detail, "eipalloc-b") || !strings.Contains(problems[0].Detail, "eipalloc-dup") {
		t.Errorf("the problem does not name both claimants: %s", problems[0].Detail)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a contested set bound anyway:\n%s", res)
	}
}

// TestCountMalformedSlotIsNamed: a tag value outside the grammar is neither
// guessed at nor ignored.
func TestCountMalformedSlotIsNamed(t *testing.T) {
	for _, bad := range []string{"one", "01", "-1", "99999999999"} {
		t.Run(bad, func(t *testing.T) {
			cloud := newFakeCloud()
			cloud.slotted("eipalloc-a", "0")
			cloud.slottedAt("eipalloc-bad", bad, "aws_eip.pool:1")

			res, diags := discoverCount(t, cloud, 2)
			if !diags.HasErrors() {
				t.Fatalf("the slot value %q was accepted:\n%s", bad, res)
			}
			if len(res.ProblemsOfKind(ProblemMalformedSlot)) != 1 {
				t.Fatalf("want one malformed-slot problem:\n%s", res)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Determinism
// ---------------------------------------------------------------------------

// TestCountBindingIsDeterministic: whatever order the provider lists a
// fungible set in, the binding is the same. Anything else would make a plan
// over a count block a coin toss.
func TestCountBindingIsDeterministic(t *testing.T) {
	ids := []string{"eipalloc-a", "eipalloc-b", "eipalloc-c", "eipalloc-d"}
	slots := []string{"7", "0", "12", "3"}

	var want string
	for shift := 0; shift < len(ids); shift++ {
		cloud := newFakeCloud()
		for i := range ids {
			j := (i + shift) % len(ids)
			cloud.slotted(ids[j], slots[j])
		}
		res, diags := discoverCount(t, cloud, 3)
		assertNoErrors(t, diags)

		got := res.String()
		if shift == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("list order %d produced a different result:\n got:\n%s\nwant:\n%s", shift, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// The estate fixture, which carries no slots
// ---------------------------------------------------------------------------

// TestEstateFixtureBindsOnTheCompatPath is the compatibility claim against the
// real P0.1 estate: its EIPs carry per-index addresses and no slots, they bind
// exactly as they did, and the only new thing is the migration assignment the
// plan will make visible.
func TestEstateFixtureBindsOnTheCompatPath(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_eip", "eipalloc-0", `aws_eip.pool:0`)
	cloud.own("aws_eip", "eipalloc-1", `aws_eip.pool:1`)
	cloud.own("aws_eip", "eipalloc-2", `aws_eip.pool:2`)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	for i, id := range []string{"eipalloc-0", "eipalloc-1", "eipalloc-2"} {
		addr := mustAddr(t, fmt.Sprintf("aws_eip.pool[%d]", i))
		b, ok := res.BindingFor(addr)
		if !ok {
			t.Errorf("%s did not bind:\n%s", addr, res)
			continue
		}
		if b.ImportID != id {
			t.Errorf("%s bound to %s, want %s", addr, b.ImportID, id)
		}
		if b.SlotBound {
			t.Errorf("%s claims a slot binding over a pre-slot estate", addr)
		}
	}
	assertSlotTable(t, res, map[string]string{
		"aws_eip.pool:0": "0", "aws_eip.pool:1": "1", "aws_eip.pool:2": "2",
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// slotted adds a live EIP carrying a slot and the address that agrees with it.
func (c *fakeCloud) slotted(id, slot string) {
	c.slottedAt(id, slot, "aws_eip.pool:"+slot)
}

// slottedAt adds a live EIP carrying a slot and an arbitrary address, which is
// how a member whose address has gone stale is spelled.
func (c *fakeCloud) slottedAt(id, slot, address string) {
	c.obj("aws_eip", id, map[string]string{
		TagEstate:  countEstate,
		TagAddress: address,
		TagSlot:    slot,
	})
}

// addressed adds a live EIP carrying no slot at all: the pre-slot estate.
func (c *fakeCloud) addressed(id, address string) {
	c.obj("aws_eip", id, map[string]string{
		TagEstate:  countEstate,
		TagAddress: address,
	})
}

// discoverCount runs a discovery pass over the count fixture at a given
// cardinality.
func discoverCount(t *testing.T, cloud *fakeCloud, size int) (*Result, tfdiags.Diagnostics) {
	t.Helper()

	cfg := loadCountConfig(t, size)
	return Discover(context.Background(), Request{
		Estate:      countEstate,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	})
}

// loadCountConfig loads the count fixture with its pool_size variable set,
// which is what lets one configuration stand in for a cardinality change.
func loadCountConfig(t *testing.T, size int) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) {
			if v.Name == "pool_size" {
				return cty.NumberIntVal(int64(size)), nil
			}
			return v.Default, nil
		},
		countDir,
		"default",
	)

	mod, diags := parser.LoadConfigDir(countDir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", countDir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("fixture %s unexpectedly calls module %q", countDir, req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", countDir, cfgDiags.Error())
	}
	return cfg
}

// assertCountBindings checks that index i of aws_eip.pool bound to want[i],
// and that nothing else of that block bound.
func assertCountBindings(t *testing.T, res *Result, want ...string) {
	t.Helper()

	for i, id := range want {
		addr := mustAddr(t, fmt.Sprintf("aws_eip.pool[%d]", i))
		b, ok := res.BindingFor(addr)
		if !ok {
			t.Errorf("%s did not bind:\n%s", addr, res)
			continue
		}
		if b.ImportID != id {
			t.Errorf("%s bound to %s, want %s", addr, b.ImportID, id)
		}
	}
	var got []string
	for _, b := range res.Bindings {
		if b.TypeName == "aws_eip" {
			got = append(got, b.Addr.String())
		}
	}
	if len(got) != len(want) {
		t.Errorf("the count block has %d bindings (%v), want %d", len(got), got, len(want))
	}
}

func assertSlotTable(t *testing.T, res *Result, want map[string]string) {
	t.Helper()

	got := res.SlotTable()
	for key, slot := range want {
		if got[key] != slot {
			t.Errorf("slot table has %s = %q, want %q (whole table: %v)", key, got[key], slot, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("slot table is %v, want %d entries", got, len(want))
	}
}
