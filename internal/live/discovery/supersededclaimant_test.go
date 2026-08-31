// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
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

// TestSupersededClaimant_theGuardsCanFail is the "prove the check can fail"
// obligation, run as a mutation rather than described. Each case feeds
// [pruneSupersededEntry] the exact input the corresponding end-to-end test
// feeds Discover, and asserts the outcome the API promises - so a rule that
// disambiguated on anything weaker than "exactly one claimant matches the
// record" is caught here even if no fixture reaches it.
func TestSupersededClaimant_theGuardsCanFail(t *testing.T) {
	addr := `aws_vpc.main`
	twoClaimants := func() *declaredEntry {
		return &declaredEntry{
			res:     identity.Resolution{Addr: mustAddr(t, addr), Class: identity.ClassNeedsDiscovery},
			escaped: EscapeAddress(addr),
			claimants: []claimant{
				{importID: "vpc-old"},
				{importID: "vpc-new"},
			},
		}
	}

	tests := []struct {
		name        string
		rec         projection.LocatedRecord
		wantKept    []string
		wantReports int
	}{
		{"exactly one match prunes the other", projection.LocatedRecord{ImportID: "vpc-new"}, []string{"vpc-new"}, 1},
		{"no match keeps both", projection.LocatedRecord{ImportID: "vpc-elsewhere"}, []string{"vpc-old", "vpc-new"}, 0},
		{"an empty record keeps both", projection.LocatedRecord{}, []string{"vpc-old", "vpc-new"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawStore, seedStore := supersededHintStore(t, estateName)
			if !tt.rec.Empty() {
				seedCurrentIdentity(t, seedStore, addr, tt.rec)
			}
			entry := twoClaimants()
			res := &Result{}
			store := projection.NewRecordEnvelopeStore(rawStore, projection.RecordKeyPrefix(estateName))
			diags := pruneSupersededEntry(context.Background(), store, Request{Estate: estateName}, res, "aws_vpc", entry.escaped, entry)
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
