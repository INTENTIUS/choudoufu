// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is [gauntlet:corpus-alb-complete/day2_replace] and
// [gauntlet:corpus-ec2-instance-complete/day2_replace]'s own unit: the
// declared-address half of the terminated-shadow wall
// internal/live/discovery/supersededclaimant.go describes. Two live objects
// carry one DECLARED address's marker after a destroy-then-create replace -
// the object the apply created, and the tag shadow of the one it destroyed -
// and every declared binding path refused rather than reading the record the
// same apply had already updated.
//
// Every assertion below is written from what the two APIs promise, not from
// the implementation: [projection.WriteBack] promises the record names the
// object the address owns now, and live/MARKERS.md promises a marker
// identifies the resource it is written on. Each check was proven able to
// fail - see TestSupersededClaimant_theGuardsCanFail's own comment for the
// mutations run against them.

// supersededHintStore opens a real local record store at an arbitrary
// estate's key prefix and returns both the raw store ([Request.HintStore]'s
// own type) and the [*projection.RecordStore] wrapping it, for seeding. The
// estate is a parameter because the count fixture runs under its own
// ([countEstate]) rather than under [estateName].
func supersededHintStore(t *testing.T, estate string) (staterecord.Store, *projection.RecordStore) {
	t.Helper()
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return raw, projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
}

// seedCurrentIdentity writes the record an apply's own write-back leaves
// behind: this address owns THIS live object, right now.
func seedCurrentIdentity(t *testing.T, store *projection.RecordStore, addr string, rec projection.LocatedRecord) {
	t.Helper()
	if _, err := projection.SeedLocatedForInstance(t.Context(), store, mustAddr(t, addr), recordOrphanProviderAddr, rec); err != nil {
		t.Fatalf("seeding the current-identity record for %s: %s", addr, err)
	}
}

// seedTombstone writes the record a replace's own write-back leaves behind
// alongside the current identity: this address's apply destroyed THIS
// object. [projection.SeedTombstoneForInstance] round-trips the same
// envelope member [projection.supersedeIdentity] writes on a replace and
// [projection.RecordStore.tombstone] writes on a destroy, so what these
// tests read back is the real wire shape rather than a stand-in.
func seedTombstone(t *testing.T, store *projection.RecordStore, addr string, rec projection.TombstoneRecord) {
	t.Helper()
	if err := projection.SeedTombstoneForInstance(t.Context(), store, mustAddr(t, addr), rec); err != nil {
		t.Fatalf("seeding the tombstone for %s: %s", addr, err)
	}
}

// seedDeposedIntoStore writes a deposed entry into the envelope an already
// seeded current identity created, through the RAW store rather than through
// any helper in this repository - there is no exported seeder for a deposed
// record, and the wire shape ({"deposed": {"<key>": {"identity": {...}}}}) is
// the same one reference-ec2-vpc's own run.sh reads with jq. Writing it by
// hand here is deliberate: this test's whole subject is what
// [projection.RecordStore.GetDeposed] returns for an address the caller never
// collected into [Request.DeposedRecords], so it must not go through the
// collection path.
func seedDeposedIntoStore(t *testing.T, raw staterecord.Store, estate, deposedKey, importID string) {
	t.Helper()
	ctx := t.Context()
	keys, err := raw.List(ctx, projection.RecordKeyPrefix(estate))
	if err != nil {
		t.Fatalf("listing the record store: %s", err)
	}
	if len(keys) != 1 {
		t.Fatalf("want exactly one seeded record key to patch, got %d: %v", len(keys), keys)
	}
	payload, version, exists, err := raw.Get(ctx, keys[0])
	if err != nil || !exists {
		t.Fatalf("reading %s: exists=%v err=%v", keys[0], exists, err)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("unmarshalling %s: %s", keys[0], err)
	}
	deposed, err := json.Marshal(map[string]any{
		deposedKey: map[string]any{"identity": map[string]any{"import_id": importID}},
	})
	if err != nil {
		t.Fatalf("marshalling the deposed member: %s", err)
	}
	env["deposed"] = deposed
	next, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshalling the patched envelope: %s", err)
	}
	if _, err := raw.PutIfVersion(ctx, keys[0], next, version); err != nil {
		t.Fatalf("writing the patched envelope: %s", err)
	}
}

// displacedIDs is every live identity reported as displaced from the address
// it is marked for, which is the kind this pass reuses for a superseded
// claimant (see supersededclaimant.go's "Why the superseded object is
// reported rather than dropped").
func displacedIDs(res *Result) []string {
	var out []string
	for _, p := range res.ProblemsOfKind(ProblemDisplacedMarker) {
		out = append(out, p.LiveIDs...)
	}
	return out
}

// TestDiscover_supersededScalarBindsTheRecordedObject is the headline, and
// corpus-alb-complete's own failure in miniature: aws_vpc.main is declared
// and needs discovery, two live objects carry its marker, and the estate's
// record names vpc-new. The address must bind vpc-new - asserted BY VALUE,
// through both the Binding and the merged resolution the plan actually
// consumes - with no collision raised, and vpc-old must be reported rather
// than silently dropped.
func TestDiscover_supersededScalarBindsTheRecordedObject(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-new"})
	seedTombstone(t, seedStore, `aws_vpc.main`, projection.TombstoneRecord{ImportID: "vpc-old"})

	addr := mustAddr(t, `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{HintStore: rawStore})
	assertNoErrors(t, diags)

	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 0 {
		t.Fatalf("the record named one of the two claimants and a collision was still raised:\n%s", res)
	}

	b, bound := res.BindingFor(addr)
	if !bound {
		t.Fatalf("%s did not bind at all:\n%s", addr, res)
	}
	// BY VALUE, both halves. A binding to vpc-old here would be the wrong
	// marker HANDOFF ranks above a missing one: the plan would read, update
	// and destroy the object the previous apply already destroyed, while the
	// object it created stayed live and unmanaged.
	if b.ImportID != "vpc-new" {
		t.Errorf("%s bound to %q, want the object the record names, vpc-new", addr, b.ImportID)
	}
	var resolved string
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr.String() {
			resolved = r.ImportID
		}
	}
	if resolved != "vpc-new" {
		t.Errorf("the merged resolution for %s carries import ID %q, want vpc-new - that value is what the plan reads", addr, resolved)
	}

	if got := displacedIDs(res); len(got) != 1 || got[0] != "vpc-old" {
		t.Errorf("the superseded object was not reported as displaced (got %v), so a live marked resource would be acted on by nothing and mentioned by nothing:\n%s", got, res)
	}
}

// TestDiscover_supersededRecordBackedScalarStaysSilent is
// corpus-alb-complete's own shape once edge 3 is in play: the address's
// identity is already answered from the record
// ([Request.RecordBackedAddrs]), so it is filed under decl.recordBacked and
// the surviving claimant is confirmation rather than a Binding - the same
// silence TestDiscoverDeposedDisambiguationRecordBacked pins for the deposed
// leg. What must NOT survive is the collision.
func TestDiscover_supersededRecordBackedScalarStaysSilent(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-new"})
	seedTombstone(t, seedStore, `aws_vpc.main`, projection.TombstoneRecord{ImportID: "vpc-old"})

	addr := mustAddr(t, `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{
		HintStore:         rawStore,
		RecordBackedAddrs: map[string]bool{addr.String(): true},
		Sweep:             true,
		CollectUnclaimed:  true,
	})
	assertNoErrors(t, diags)

	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 0 {
		t.Fatalf("a record-backed address whose record names one of the two claimants still raised a collision:\n%s", res)
	}
	if _, bound := res.BindingFor(addr); bound {
		t.Errorf("a record-backed address's surviving claimant was bound, which a record-backed address never is:\n%s", res)
	}
	if got := displacedIDs(res); len(got) != 1 || got[0] != "vpc-old" {
		t.Errorf("the superseded object was not reported as displaced (got %v):\n%s", got, res)
	}
}

// TestDiscover_supersededLiveDuplicateWithNoTombstoneRefuses is GitHub
// issue #670's own headline, and the refusal the record-first prune took
// away. The input is the one the prune cannot otherwise read: the record
// names vpc-new and vpc-new is live, and a SECOND, genuinely live object
// wears the same estate and address marker. Nothing in the record says that
// second object is dead, because nothing destroyed it.
//
// "The record names someone else" is true of a terminated shadow and of a
// live duplicate alike, so it cannot be what a prune turns on. This must
// refuse, by the rendered summary and by the identities the refusal names -
// binding past it would leave a live, marked object that nothing in the run
// reads, changes, destroys or even mentions.
func TestDiscover_supersededLiveDuplicateWithNoTombstoneRefuses(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-duplicate", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-new"})

	res, diags := discoverFixture(t, cloud, Request{HintStore: rawStore})
	if !diags.HasErrors() {
		t.Fatalf("a second LIVE resource wearing this address's marker was resolved away with nothing recording it as destroyed:\n%s", res)
	}
	// The rendered summary, not a kind constant: this is the string an
	// operator reads, and it is the one #643's change replaced with a
	// warning.
	var summaries []string
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			summaries = append(summaries, d.Description().Summary)
		}
	}
	if len(summaries) != 1 || summaries[0] != "Two live resources claiming one address" {
		t.Fatalf("the run failed with %v, want exactly [Two live resources claiming one address]", summaries)
	}

	problems := res.ProblemsOfKind(ProblemCollision)
	if len(problems) != 1 {
		t.Fatalf("want exactly one collision problem for a live duplicate, got:\n%s", res)
	}
	// BY VALUE: the refusal must name both live objects, since the whole
	// point is that this run cannot tell which of them the address owns.
	if got := sortedStrings(problems[0].LiveIDs); fmt.Sprint(got) != fmt.Sprint([]string{"vpc-duplicate", "vpc-new"}) {
		t.Errorf("the collision names %v, want both live objects [vpc-duplicate vpc-new]", got)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("something bound despite an unresolved live duplicate:\n%s", res)
	}
	if got := displacedIDs(res); len(got) != 0 {
		t.Errorf("a live duplicate was reported as a displaced marker (%v), which says nothing is proposed for it while it is live and marked:\n%s", got, res)
	}
}

// TestDiscover_supersededCountLiveDuplicateWithNoTombstoneRefuses is the
// test above on the count path, which is where corpus-ec2-instance-complete
// measured the loss: a second live member carrying the same slot as the
// recorded one. The prune runs before [bindCountBlock] classifies the set,
// so a claimant dropped here changes which binder runs at all - which is
// exactly why an un-tombstoned one must not be dropped.
func TestDiscover_supersededCountLiveDuplicateWithNoTombstoneRefuses(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-duplicate", "0")
	cloud.slotted("eipalloc-new", "0")

	rawStore, seedStore := supersededHintStore(t, countEstate)
	seedCurrentIdentity(t, seedStore, `aws_eip.pool[0]`, projection.LocatedRecord{ImportID: "eipalloc-new"})

	res, diags := discoverCountWith(t, cloud, 1, Request{HintStore: rawStore})
	if !diags.HasErrors() {
		t.Fatalf("a second LIVE count member wearing the recorded member's slot was resolved away with nothing recording it as destroyed:\n%s", res)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a count member bound despite a live duplicate:\n%s", res)
	}
	if got := displacedIDs(res); len(got) != 0 {
		t.Errorf("a live duplicate count member was reported as a displaced marker (%v):\n%s", got, res)
	}
}

// TestDiscover_supersededShadowIsPrunedOnlyWithATombstone is the pair to the
// two above, and the reason the fix is a recording rather than a refusal:
// the SAME two-claimant shape, with the estate's own record saying it
// destroyed vpc-old, must still bind vpc-new and still report vpc-old - the
// day2_replace behaviour issue #643 landed, unchanged.
//
// It differs from TestDiscover_supersededScalarBindsTheRecordedObject in
// exactly one line, the tombstone seed, so the two together are the control
// pair: same cloud, same record, opposite outcomes, and the tombstone is the
// only difference between them.
func TestDiscover_supersededShadowIsPrunedOnlyWithATombstone(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-new"})
	seedTombstone(t, seedStore, `aws_vpc.main`, projection.TombstoneRecord{ImportID: "vpc-old"})

	addr := mustAddr(t, `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{HintStore: rawStore})
	assertNoErrors(t, diags)

	b, bound := res.BindingFor(addr)
	if !bound {
		t.Fatalf("%s did not bind at all:\n%s", addr, res)
	}
	if b.ImportID != "vpc-new" {
		t.Errorf("%s bound to %q, want the object the record names, vpc-new", addr, b.ImportID)
	}
	if got := displacedIDs(res); len(got) != 1 || got[0] != "vpc-old" {
		t.Fatalf("the tombstoned shadow was not reported as displaced (got %v):\n%s", got, res)
	}
	// The report must say WHY this one was dropped, not merely that it was:
	// an operator reading it has to be able to tell a superseded object from
	// a duplicate the run refused on.
	problems := res.ProblemsOfKind(ProblemDisplacedMarker)
	if !strings.Contains(problems[0].Detail, "destroyed by an earlier apply of this estate") {
		t.Errorf("the displaced report does not say the object was recorded destroyed:\n%s", problems[0].Detail)
	}
}

// sortedStrings is a copy of in, sorted, so a test asserts on a set the
// scan does not promise an order for without mutating what it was given.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// TestDiscover_supersededScalarRecordMatchingNeitherStillCollides is the
// safe default [matchDeposedClaimant]'s own doc comment insists on, and the
// reason this pass is not a guess: a record naming an object neither
// claimant is - stale, hand-edited, or from another estate - is evidence for
// nothing, and the collision must survive exactly as it does with no record
// at all.
func TestDiscover_supersededScalarRecordMatchingNeitherStillCollides(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-2", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-neither-claimant"})

	res, diags := discoverFixture(t, cloud, Request{HintStore: rawStore})
	if !diags.HasErrors() {
		t.Fatalf("a record matching neither claimant silently resolved a declared collision:\n%s", res)
	}
	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 1 {
		t.Fatalf("want exactly one collision problem when the record matches nothing live, got:\n%s", res)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("something bound despite an unresolved collision:\n%s", res)
	}
}

// TestDiscover_supersededScalarWithNoRecordStoreStillCollides is the
// existing-behaviour control: an estate with no record store at all must see
// the byte-identical refusal it saw before this pass existed.
func TestDiscover_supersededScalarWithNoRecordStoreStillCollides(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-2", `aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("a two-claimant declared collision with no record store was silently resolved:\n%s", res)
	}
	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 1 {
		t.Fatalf("want exactly one collision problem with no record store, got:\n%s", res)
	}
	if got := displacedIDs(res); len(got) != 0 {
		t.Errorf("a displaced report was produced with no record to displace anything against: %v", got)
	}
}

// TestDiscover_supersededCountMemberBindsTheRecordedObject is
// corpus-ec2-instance-complete's own failure: the shadow of a replaced count
// member carries the SAME tofu-slot as the object that replaced it, which
// live/MARKERS.md explicitly permits ("a slot whose resource has been
// deleted may be assigned again later"), so the refusal's own suggested
// discriminator cannot break the tie. Two claimants for one count instance
// must bind the object the record names, and the set matcher must never see
// the shadow at all - the reason this pass runs before [bindCountBlock]
// rather than at its refusal site.
func TestDiscover_supersededCountMemberBindsTheRecordedObject(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-shadow", "0")
	cloud.slotted("eipalloc-new", "0")

	rawStore, seedStore := supersededHintStore(t, countEstate)
	seedCurrentIdentity(t, seedStore, `aws_eip.pool[0]`, projection.LocatedRecord{ImportID: "eipalloc-new"})
	seedTombstone(t, seedStore, `aws_eip.pool[0]`, projection.TombstoneRecord{ImportID: "eipalloc-shadow"})

	res, diags := discoverCountWith(t, cloud, 1, Request{HintStore: rawStore})
	assertNoErrors(t, diags)

	for _, kind := range []ProblemKind{ProblemDuplicateSlot, ProblemNeedsSlotMarkers, ProblemCollision} {
		if problems := res.ProblemsOfKind(kind); len(problems) != 0 {
			t.Fatalf("%s survived a record that names one of the two claimants:\n%s", kind, res)
		}
	}

	addr := mustAddr(t, `aws_eip.pool[0]`)
	b, bound := res.BindingFor(addr)
	if !bound {
		t.Fatalf("%s did not bind at all:\n%s", addr, res)
	}
	if b.ImportID != "eipalloc-new" {
		t.Errorf("%s bound to %q, want the object the record names, eipalloc-new", addr, b.ImportID)
	}
	if got := displacedIDs(res); len(got) != 1 || got[0] != "eipalloc-shadow" {
		t.Errorf("the superseded count member was not reported as displaced (got %v):\n%s", got, res)
	}
}

// TestDiscover_supersededCountMemberRecordMatchingNeitherStillRefuses is the
// count path's own safe default. Two live members carrying one slot is
// [slots.Match]'s DuplicateError, and a record naming neither of them must
// leave that refusal exactly where it was.
func TestDiscover_supersededCountMemberRecordMatchingNeitherStillRefuses(t *testing.T) {
	cloud := newFakeCloud()
	cloud.slotted("eipalloc-1", "0")
	cloud.slotted("eipalloc-2", "0")

	rawStore, seedStore := supersededHintStore(t, countEstate)
	seedCurrentIdentity(t, seedStore, `aws_eip.pool[0]`, projection.LocatedRecord{ImportID: "eipalloc-neither"})

	res, diags := discoverCountWith(t, cloud, 1, Request{HintStore: rawStore})
	if !diags.HasErrors() {
		t.Fatalf("a record matching neither count claimant silently resolved the set:\n%s", res)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a count member bound despite an unresolved duplicate slot:\n%s", res)
	}
}

// TestSupersededClaimant_compositeIdentityMatchesByComponent is the
// genericity leg: nothing about this pass is about a server-minted string.
// A type whose identity is several named components matches when every
// component the record names matches, and fails when any one differs - the
// property [identity.TypeIdentity.Components] gives every admitted composite
// type, with no type name anywhere in the rule.
func TestSupersededClaimant_compositeIdentityMatchesByComponent(t *testing.T) {
	live := func(attrs map[string]string) cty.Value {
		vals := map[string]cty.Value{}
		for k, v := range attrs {
			vals[k] = cty.StringVal(v)
		}
		return cty.ObjectVal(vals)
	}
	rec := projection.LocatedRecord{Components: map[string]string{"group": "devs", "policy_arn": "arn:aws:iam::aws:policy/ReadOnly"}}

	tests := []struct {
		name string
		c    claimant
		want bool
	}{
		{"every component agrees", claimant{identity: live(map[string]string{"group": "devs", "policy_arn": "arn:aws:iam::aws:policy/ReadOnly"})}, true},
		{"one component differs", claimant{identity: live(map[string]string{"group": "devs", "policy_arn": "arn:aws:iam::aws:policy/AdminAccess"})}, false},
		{"a component the live identity does not carry", claimant{identity: live(map[string]string{"group": "devs"})}, false},
		{"no live identity at all", claimant{}, false},
		{"a marked component is refused, never unmarked", claimant{identity: cty.ObjectVal(map[string]cty.Value{
			"group":      cty.StringVal("devs").Mark("sensitive"),
			"policy_arn": cty.StringVal("arn:aws:iam::aws:policy/ReadOnly"),
		})}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claimantMatchesRecord(rec, tt.c); got != tt.want {
				t.Errorf("claimantMatchesRecord = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDiscover_crashWindowDeposedClaimantSurvivesThePrune is
// [gauntlet:reference-ec2-vpc/day2_crash]'s own unit, and the regression this
// pass caused the day it landed. GitHub issue #361's crash window leaves TWO
// GENUINELY LIVE objects and one write-back naming both: current=the new
// object, deposed=the old one. Reading only the current identity made the
// deposed object look exactly like a terminated tag shadow, so this pass
// pruned it before [matchDeposedClaimant] could ever see it and the recovery
// plan proposed nothing at all - the old instance stayed running, billed and
// unmanaged forever.
//
// The deposed claimant must survive the prune, be pulled out as a
// DeposedBinding (which is what makes the next apply destroy it), and the
// address must still bind the NEW object BY VALUE.
func TestDiscover_crashWindowDeposedClaimantSurvivesThePrune(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-new"})

	addr := mustAddr(t, `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{
		HintStore: rawStore,
		DeposedRecords: map[string]map[string]projection.DeposedRecord{
			addr.String(): {
				"deadbeef": {ImportID: "vpc-old", Provider: `provider["registry.opentofu.org/hashicorp/aws"]`},
			},
		},
	})
	assertNoErrors(t, diags)

	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 0 {
		t.Fatalf("the crash window raised a collision:\n%s", res)
	}
	if len(res.DeposedBindings) != 1 {
		t.Fatalf("want exactly one deposed binding, got %d - the deposed object was pruned as a tag shadow and the recovery plan would propose nothing:\n%s", len(res.DeposedBindings), res)
	}
	// BY VALUE. A deposed binding naming vpc-new would destroy the object the
	// crashed apply had just successfully created.
	if db := res.DeposedBindings[0]; db.ImportID != "vpc-old" {
		t.Errorf("the deposed binding names %q, want the old object vpc-old", db.ImportID)
	}

	b, bound := res.BindingFor(addr)
	if !bound {
		t.Fatalf("%s did not bind at all:\n%s", addr, res)
	}
	// BY VALUE, both halves, exactly as the superseded scalar test asserts
	// them: binding to vpc-old here is the wrong marker HANDOFF ranks above a
	// missing one.
	if b.ImportID != "vpc-new" {
		t.Errorf("%s bound to %q, want the object the record names, vpc-new", addr, b.ImportID)
	}
	var resolved string
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr.String() {
			resolved = r.ImportID
		}
	}
	if resolved != "vpc-new" {
		t.Errorf("the merged resolution for %s carries import ID %q, want vpc-new - that value is what the plan reads", addr, resolved)
	}

	if got := displacedIDs(res); len(got) != 0 {
		t.Errorf("a live deposed object was reported as a displaced marker (%v), which says nothing is proposed for it - the opposite of what the recovery must do:\n%s", got, res)
	}
}

// TestDiscover_crashWindowRecordBackedDeposedClaimantSurvivesThePrune is the
// test above for the population issue #415 added, and the one a real crash
// actually lands in: WriteBack commits the new object as the address's
// CURRENT identity in the same pass it records the old one as deposed, so the
// very next plan finds the address record-backed rather than needs-discovery
// (see TestDiscoverDeposedDisambiguationRecordBacked's own comment). The
// prune reaches decl.recordBacked too, so it broke this leg as well.
func TestDiscover_crashWindowRecordBackedDeposedClaimantSurvivesThePrune(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-new"})

	addr := mustAddr(t, `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{
		HintStore:         rawStore,
		RecordBackedAddrs: map[string]bool{addr.String(): true},
		Sweep:             true,
		CollectUnclaimed:  true,
		DeposedRecords: map[string]map[string]projection.DeposedRecord{
			addr.String(): {
				"deadbeef": {ImportID: "vpc-old", Provider: `provider["registry.opentofu.org/hashicorp/aws"]`},
			},
		},
	})
	assertNoErrors(t, diags)

	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 0 {
		t.Fatalf("the record-backed crash window raised a collision:\n%s", res)
	}
	if len(res.DeposedBindings) != 1 {
		t.Fatalf("want exactly one deposed binding, got %d:\n%s", len(res.DeposedBindings), res)
	}
	if db := res.DeposedBindings[0]; db.ImportID != "vpc-old" {
		t.Errorf("the deposed binding names %q, want the old object vpc-old", db.ImportID)
	}
	if got := displacedIDs(res); len(got) != 0 {
		t.Errorf("a live deposed object was reported as a displaced marker (%v):\n%s", got, res)
	}
}

// TestDiscover_crashWindowDeposedOnlyInTheStoreStillRefuses is the union's
// store leg, and the reason this pass reads the record store's own Deposed
// member rather than trusting [Request.DeposedRecords] alone.
// collectDeposedRecords (internal/command/live_plan.go) collects one entry per
// NEEDS-DISCOVERY address; the record store answers for every declared
// address there is. An address the caller did not collect but the store
// records a deposed object for must NOT have that object silently pruned:
// dropping it binds the survivor and leaves a live, marked, running object
// that nothing in the run mentions. Refusing is the safe rung, and it is what
// this shape did before the superseded pass existed at all.
func TestDiscover_crashWindowDeposedOnlyInTheStoreStillRefuses(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-new"})
	seedDeposedIntoStore(t, rawStore, estateName, "deadbeef", "vpc-old")

	res, diags := discoverFixture(t, cloud, Request{HintStore: rawStore})
	if !diags.HasErrors() {
		t.Fatalf("a live deposed object recorded only in the store was silently pruned:\n%s", res)
	}
	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 1 {
		t.Fatalf("want exactly one collision problem, got:\n%s", res)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("something bound despite an unresolved collision:\n%s", res)
	}
	if got := displacedIDs(res); len(got) != 0 {
		t.Errorf("the recorded deposed object was reported as a displaced marker (%v), which claims nothing is proposed for it while it is still live:\n%s", got, res)
	}
}

// TestSupersededClaimant_theGuardsCanFail is the "prove the check can fail"
// obligation, run as a mutation rather than described. Each case feeds
// [pruneSupersededEntry] the exact input the corresponding end-to-end test
// feeds Discover, and asserts the outcome the API promises - so a rule that
// disambiguated on anything weaker than "exactly one claimant matches the
// record" is caught here even if no fixture reaches it.
func TestSupersededClaimant_theGuardsCanFail(t *testing.T) {
	addr := `aws_vpc.main`
	entryWith := func(ids ...string) *declaredEntry {
		if len(ids) == 0 {
			ids = []string{"vpc-old", "vpc-new"}
		}
		cs := make([]claimant, 0, len(ids))
		for _, id := range ids {
			cs = append(cs, claimant{importID: id})
		}
		return &declaredEntry{
			res:       identity.Resolution{Addr: mustAddr(t, addr), Class: identity.ClassNeedsDiscovery},
			escaped:   EscapeAddress(addr),
			claimants: cs,
		}
	}

	tests := []struct {
		name string
		rec  projection.LocatedRecord
		// claimants overrides the default vpc-old/vpc-new pair.
		claimants []string
		// deposed is what the crashed apply's own write-back recorded
		// alongside rec, keyed by deposed key.
		deposed map[string]projection.DeposedRecord
		// tombstones is what a replace's own write-back recorded
		// alongside rec: the identities this estate's apply destroyed.
		tombstones  []string
		wantKept    []string
		wantReports int
	}{
		{
			name: "one match and a tombstone for the other prunes it",
			rec:  projection.LocatedRecord{ImportID: "vpc-new"}, tombstones: []string{"vpc-old"},
			wantKept: []string{"vpc-new"}, wantReports: 1,
		},
		{
			// Issue #670's whole point: the record naming someone else is
			// not evidence this claimant is dead. A second, genuinely live
			// object wearing this address's marker produces exactly this
			// input, and it must reach collisionProblem.
			name:     "one match but no tombstone keeps both",
			rec:      projection.LocatedRecord{ImportID: "vpc-new"},
			wantKept: []string{"vpc-old", "vpc-new"}, wantReports: 0,
		},
		{
			// A tombstone naming an object neither claimant is settles
			// nothing either - the same "evidence for nothing" the
			// no-match record case below is.
			name: "a tombstone matching neither claimant keeps both",
			rec:  projection.LocatedRecord{ImportID: "vpc-new"}, tombstones: []string{"vpc-elsewhere"},
			wantKept: []string{"vpc-old", "vpc-new"}, wantReports: 0,
		},
		{
			name: "no match keeps both", rec: projection.LocatedRecord{ImportID: "vpc-elsewhere"}, tombstones: []string{"vpc-old"},
			wantKept: []string{"vpc-old", "vpc-new"}, wantReports: 0,
		},
		{
			name: "an empty record keeps both", rec: projection.LocatedRecord{}, tombstones: []string{"vpc-old"},
			wantKept: []string{"vpc-old", "vpc-new"}, wantReports: 0,
		},
		{
			// The crash window: the other claimant is a LIVE deposed
			// object, so nothing is pruned and nothing is reported - the
			// set is left for matchDeposedClaimant. Deposed WINS over a
			// tombstone naming the same object, because a live object
			// awaiting destruction is the one the next apply must act on.
			name:     "a recorded deposed claimant is kept, not pruned",
			rec:      projection.LocatedRecord{ImportID: "vpc-new"},
			deposed:  map[string]projection.DeposedRecord{"deadbeef": {ImportID: "vpc-old"}},
			wantKept: []string{"vpc-old", "vpc-new"}, wantReports: 0,
		},
		{
			// A deposed record naming an object neither claimant is
			// settles nothing: the ordinary prune stands unchanged.
			name:       "a deposed record matching neither claimant changes nothing",
			rec:        projection.LocatedRecord{ImportID: "vpc-new"},
			deposed:    map[string]projection.DeposedRecord{"deadbeef": {ImportID: "vpc-elsewhere"}},
			tombstones: []string{"vpc-old"},
			wantKept:   []string{"vpc-new"}, wantReports: 1,
		},
		{
			// Both at once, which is what makes the rule "is recorded
			// destroyed" rather than "is not the survivor": a crash window
			// whose old object's own earlier shadow is still tag-visible.
			// The shadow is pruned; the deposed object is kept.
			name:       "a shadow is pruned while the deposed claimant is kept",
			rec:        projection.LocatedRecord{ImportID: "vpc-new"},
			claimants:  []string{"vpc-dead", "vpc-old", "vpc-new"},
			deposed:    map[string]projection.DeposedRecord{"deadbeef": {ImportID: "vpc-old"}},
			tombstones: []string{"vpc-dead"},
			wantKept:   []string{"vpc-old", "vpc-new"}, wantReports: 1,
		},
		{
			// And the mixture issue #670 exists for: one claimant this
			// estate destroyed, one it did not. The dead one is pruned and
			// reported; the unexplained one stays, so the entry still
			// refuses rather than binding past a live duplicate.
			name:       "a live duplicate survives beside a pruned shadow",
			rec:        projection.LocatedRecord{ImportID: "vpc-new"},
			claimants:  []string{"vpc-dead", "vpc-duplicate", "vpc-new"},
			tombstones: []string{"vpc-dead"},
			wantKept:   []string{"vpc-duplicate", "vpc-new"}, wantReports: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawStore, seedStore := supersededHintStore(t, estateName)
			if !tt.rec.Empty() {
				seedCurrentIdentity(t, seedStore, addr, tt.rec)
			}
			for _, id := range tt.tombstones {
				seedTombstone(t, seedStore, addr, projection.TombstoneRecord{ImportID: id})
			}
			entry := entryWith(tt.claimants...)
			res := &Result{}
			store := projection.NewRecordEnvelopeStore(rawStore, projection.RecordKeyPrefix(estateName))
			req := Request{Estate: estateName}
			if len(tt.deposed) > 0 {
				req.DeposedRecords = map[string]map[string]projection.DeposedRecord{entry.res.Addr.String(): tt.deposed}
			}
			diags := pruneSupersededEntry(context.Background(), store, req, res, "aws_vpc", entry.escaped, entry)
			assertNoErrors(t, diags)

			var kept []string
			for _, c := range entry.claimants {
				kept = append(kept, c.importID)
			}
			if fmt.Sprint(kept) != fmt.Sprint(tt.wantKept) {
				t.Errorf("kept claimants %v, want %v", kept, tt.wantKept)
			}
			if got := len(res.ProblemsOfKind(ProblemDisplacedMarker)); got != tt.wantReports {
				t.Errorf("%d displaced reports, want %d", got, tt.wantReports)
			}
		})
	}
}

// discoverCountWith is [discoverCount] with the caller's own extra Request
// fields, so a count fixture can be run against a record store.
func discoverCountWith(t *testing.T, cloud *fakeCloud, size int, req Request) (*Result, tfdiags.Diagnostics) {
	t.Helper()

	cfg := loadCountConfig(t, size)
	req.Estate = countEstate
	req.Config = cfg
	req.Resolutions = resolveOrFail(t, cfg).All()
	req.Provider = cloud
	return Discover(context.Background(), req)
}

// TestDiscover_supersededUntombstonedClaimantIsNeverCalledDestroyed is
// GitHub issue #854's read half: the record an import or a live-mv leaves
// behind, and the wording it must not produce.
//
// After #854's write-side change, an `import` block that points an address
// at a second live object, and a `live-mv` onto an address that already held
// a record, write the new identity and NO tombstone - the plan scheduled no
// replace, so nothing was destroyed. That is exactly the record seeded here:
// aws_vpc.main names vpc-imported, vpc-previous is still live and still
// wearing the address's marker, and nothing records it as destroyed.
//
// Two things are asserted, and the second is the one this issue is about.
// The address must refuse, because vpc-previous may well be alive. And no
// diagnostic this run produces may describe vpc-previous as destroyed:
// before #854 the write side recorded it, this pass pruned it, and the
// operator was told "records this one as destroyed by an earlier apply of
// this estate" about a running resource.
func TestDiscover_supersededUntombstonedClaimantIsNeverCalledDestroyed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-previous", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-imported", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-imported"})

	res, diags := discoverFixture(t, cloud, Request{HintStore: rawStore})
	if !diags.HasErrors() {
		t.Fatalf("an import at an occupied address resolved the displaced object away with nothing recording it as destroyed:\n%s", res)
	}

	// The rendered summary an operator reads, by value.
	var summaries []string
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			summaries = append(summaries, d.Description().Summary)
		}
	}
	if len(summaries) != 1 || summaries[0] != "Two live resources claiming one address" {
		t.Fatalf("the run failed with %v, want exactly [Two live resources claiming one address]", summaries)
	}

	// The wording. Every rendered detail this run produced, checked for the
	// sentence that would be a lie about a live object.
	for _, p := range res.Problems {
		if strings.Contains(p.Detail, "destroyed by an earlier apply of this estate") {
			t.Errorf("a run that recorded no destroy still told the operator one happened:\n%s", p.Detail)
		}
	}
	for _, d := range diags {
		if strings.Contains(d.Description().Detail, "destroyed by an earlier apply of this estate") {
			t.Errorf("a diagnostic describes a live object as destroyed:\n%s", d.Description().Detail)
		}
	}
	if got := displacedIDs(res); len(got) != 0 {
		t.Errorf("the displaced object was reported as superseded (%v) rather than refused, which says nothing is proposed for it while it is live and marked:\n%s", got, res)
	}
}

// TestDiscover_supersededReportNamesWhatDoesNotWriteAnEntry is the pair to
// the test above, on the branch that DOES prune. The message an operator
// reads there has to distinguish the two, or the report is unreadable
// exactly where it matters: it must say that this object was recorded
// destroyed, and it must say that an import or a live-mv records nothing, so
// an operator can tell why the object beside it in the same estate refused
// instead.
//
// Asserted on the rendered Detail, by substring, because that string is the
// whole of what the operator has.
func TestDiscover_supersededReportSaysWhatDoesNotWriteAnEntry(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	rawStore, seedStore := supersededHintStore(t, estateName)
	seedCurrentIdentity(t, seedStore, `aws_vpc.main`, projection.LocatedRecord{ImportID: "vpc-new"})
	seedTombstone(t, seedStore, `aws_vpc.main`, projection.TombstoneRecord{ImportID: "vpc-old"})

	res, diags := discoverFixture(t, cloud, Request{HintStore: rawStore})
	assertNoErrors(t, diags)

	problems := res.ProblemsOfKind(ProblemDisplacedMarker)
	if len(problems) != 1 {
		t.Fatalf("want exactly one displaced-marker report for the tombstoned shadow, got:\n%s", res)
	}
	for _, want := range []string{
		"destroyed by an earlier apply of this estate",
		"a replace this estate's own plan scheduled",
		"An import or a live-mv that re-points this address at a different object records nothing",
	} {
		if !strings.Contains(problems[0].Detail, want) {
			t.Errorf("the displaced report does not say %q, so an operator cannot tell this case from the one that refuses:\n%s", want, problems[0].Detail)
		}
	}
}
