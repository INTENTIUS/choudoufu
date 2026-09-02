// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #415: the collision-outcome matrix.
//
// # Why this exists
//
// GitHub issue #411 existed because one cell of an implicit matrix was
// silent: a manufactured marker collision on a ServerAssigned/ARN type
// (aws_eip) under a fungible count set, once the instance was record-backed
// (its identity already answered by the estate record, the shape a
// migrated count member takes - see [declaredEntry.recordBacked]'s own doc
// comment), produced a bare "No changes." at rc=0 rather than a refusal.
// #411 fixed that one cell. This file makes the whole matrix explicit -
// every {identity shape} x {instance shape} combination this package's
// binding logic distinguishes - so the NEXT silent cell is a red test here,
// not a field report against a real estate.
//
// # The two axes
//
// Identity shape is how a type's identity is determined, matching the three
// rungs HANDOFF.md's foundation section names ("tag-governable, derived
// from configuration, or record-only"):
//
//   - config-identified: the identity is built from configuration
//     arguments ([identity.TypeIdentity.Components]). aws_sns_topic is used
//     throughout this file - its ARN is name plus region plus a
//     Cloud-derived account ID, so it is config-identified end to end and
//     STILL needs discovery, because the account ID is not knowable from
//     static configuration alone. A type whose identity is FULLY static
//     (no Cloud component, no ServerAssignedIfAbsent gap) resolves
//     [identity.ClassConcrete] and never reaches this package's collision
//     machinery at all - it is not a coherent fixture for this matrix.
//   - ServerAssigned/ARN: the provider mints the identity; nothing in
//     configuration names it ([identity.TypeIdentity.ServerAssigned]).
//     aws_vpc, aws_eip and aws_subnet are used throughout this file.
//   - record-only: the instance's identity for THIS discovery pass comes
//     from the estate record rather than from configuration or a cloud
//     read ([Request.RecordBackedAddrs], [declaredEntry.recordBacked]) -
//     the rung a migrated instance drops to. This is orthogonal to the
//     other two: a record-backed instance can be either a config-identified
//     or a ServerAssigned/ARN type underneath, which is why #411's own
//     fixture (aws_eip, ServerAssigned) and this file's record-only cells
//     use ServerAssigned types too - the record answering identity is what
//     changes the routing, not the type shape.
//
// A DIFFERENT thing this file deliberately does NOT test as a "record-only"
// cell is [identity.TypeIdentity.RecordBacked] - the type-level property of
// GitHub issue #73's logical types (null_resource, terraform_data, the
// random_* and time_* families). See
// TestCollisionOutcomeMatrix_RecordBackedTypesNeverReachDiscovery below for
// why every instance shape of THAT axis value is N/A.
//
// Instance shape is how many declared addresses one resource block owns at
// once, and whether they form a fungible set:
//
//   - scalar: one address, no count or for_each.
//   - count set: N addresses sharing one block, told apart only by
//     position - a fungible set, which is why a collision on it asks for
//     tofu-slot rather than simply naming the winner.
//   - for_each set: N addresses sharing one block, each with its own
//     stable, configuration-supplied key. NOT fungible: a for_each
//     instance's key IS its identity within the block, so - as this file's
//     own results pin - a collision on one keyed instance is an ordinary
//     two-objects-one-address collision, the same outcome a scalar
//     collision gets, not the "which position" question a count set asks.
//
// # The fixture
//
// testdata/collision-matrix declares one resource block per {config-
// identified, ServerAssigned/ARN} x {scalar, count set, for_each set} cell -
// six blocks, all of which resolve identity.ClassNeedsDiscovery (see
// TestCollisionMatrixFixtureNeedsDiscovery). The three record-only cells
// reuse the ServerAssigned/ARN blocks (aws_vpc.scalar_server,
// aws_eip.count_server, aws_subnet.foreach_server) with one instance's
// address added to Request.RecordBackedAddrs, exactly as a migrated
// estate's own record store would populate it.
//
// # What this file's fix changes
//
// Writing the record-only x scalar and record-only x for_each cells found
// the same silence #411 fixed for record-only x count set, never closed for
// the other two instance shapes: [declared.entryFor]'s own fallback was
// deliberately scoped to `entry.inCount` (see its prior doc comment, still
// in git history), so a scalar or for_each record-backed entry fell through
// to [declared.declares]+[declared.displacedFrom], which declines to answer
// for any non-ClassConcrete resolution - and a record-backed entry's own
// resolution never becomes ClassConcrete, so it declined every time. Two
// live objects manufacturing a collision on such an address were silently
// dropped: no Binding, no Unbound, no Orphan, no Problem. This is fixed
// generically in entryFor and in [bind]'s own new pass over
// decl.recordBacked's non-count entries - see both functions' doc comments.
// TestCollisionOutcomeMatrix's record-only x scalar and record-only x
// for_each cells are RED against the pre-fix code and green against this
// commit; that is the guard this issue asked for.

const collisionMatrixDir = "testdata/collision-matrix"

// TestCollisionMatrixFixtureNeedsDiscovery pins the fixture's own
// precondition: every instance this matrix collides resolves
// [identity.ClassNeedsDiscovery]. If any of them resolved ClassConcrete
// instead (a config-identified type whose identity turned out to be fully
// static after all), it would never reach discovery's binding demand and
// every cell built on it would silently test nothing - see this file's own
// "config-identified" paragraph above for why that is a real risk for this
// identity shape specifically, unlike ServerAssigned/ARN.
func TestCollisionMatrixFixtureNeedsDiscovery(t *testing.T) {
	cfg := loadConfig(t, collisionMatrixDir)
	all := resolveOrFail(t, cfg).All()

	want := []string{
		`aws_vpc.scalar_server`,
		`aws_sns_topic.scalar_config`,
		`aws_eip.count_server[0]`,
		`aws_eip.count_server[1]`,
		`aws_sns_topic.count_config[0]`,
		`aws_sns_topic.count_config[1]`,
		`aws_subnet.foreach_server["a"]`,
		`aws_subnet.foreach_server["b"]`,
		`aws_sns_topic.foreach_config["a"]`,
		`aws_sns_topic.foreach_config["b"]`,
	}
	got := make(map[string]identity.Class, len(all))
	for _, r := range all {
		got[r.Addr.String()] = r.Class
	}
	for _, addr := range want {
		class, ok := got[addr]
		if !ok {
			t.Errorf("the fixture no longer declares %s; this file's premise is stale", addr)
			continue
		}
		if class != identity.ClassNeedsDiscovery {
			t.Errorf("%s resolved %s, want NEEDS_DISCOVERY - it would never reach discovery's collision machinery", addr, class)
		}
	}
}

// collisionCell is one {identity shape} x {instance shape} cell: a resource
// address in testdata/collision-matrix, and two live objects manufactured
// to carry its marker at once.
type collisionCell struct {
	name          string
	identityShape string // "config-identified", "ServerAssigned/ARN" or "record-only"
	instanceShape string // "scalar", "count set" or "for_each set"

	typeName string
	addr     string // the declared address, e.g. `aws_eip.count_server[0]`
	marker   string // the escaped tofu-address value both claimants carry
	id1, id2 string

	// recordOnly marks addr in Request.RecordBackedAddrs and requests the
	// sweep+CollectUnclaimed shape statelessDiscover's real callers always
	// use (internal/command/live_plan.go) - the shape that makes a
	// record-backed instance's own live objects still get scanned at all
	// (see TestDiscover_recordBackedWholeTypeStillCollectsUnclaimed in
	// recordbacked_demand_test.go).
	recordOnly bool
	// slotted additionally tags both claimants with a matching tofu-slot,
	// mirroring what a migrated estate's own count members actually carry
	// (live-import stamps tofu-slot on every count member) - see the
	// record-only x count set cell below for why this matters.
	slotted bool

	wantKind           ProblemKind
	wantDetailContains []string
}

func (c collisionCell) discover(t *testing.T) (*Result, tfdiags.Diagnostics) {
	t.Helper()

	cfg := loadConfig(t, collisionMatrixDir)
	resolutions := resolveOrFail(t, cfg).All()

	tags := map[string]string{TagEstate: estateName, TagAddress: c.marker}
	if c.slotted {
		tags[TagSlot] = "0"
	}
	cloud := newFakeCloud()
	cloud.obj(c.typeName, c.id1, cloneTags(tags))
	cloud.obj(c.typeName, c.id2, cloneTags(tags))

	req := Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    cloud,
	}
	if c.recordOnly {
		req.RecordBackedAddrs = map[string]bool{c.addr: true}
		req.Sweep = true
		req.CollectUnclaimed = true
	}
	return Discover(context.Background(), req)
}

func cloneTags(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// TestCollisionOutcomeMatrix is the matrix itself: nine cells, three
// identity shapes by three instance shapes, each a manufactured two-object
// collision on one declared address with its exact, pinned outcome. Every
// assertion is on message text (kind, severity and the detail sentence),
// never a boolean predicate - a cell that regresses to silence fails on
// "want exactly one problem, got 0", and a cell that regresses to the wrong
// message fails on the Contains checks, not merely on HasErrors.
func TestCollisionOutcomeMatrix(t *testing.T) {
	cells := []collisionCell{
		// ---------------------------------------------------------------
		// config-identified
		// ---------------------------------------------------------------
		{
			name:          "config-identified x scalar",
			identityShape: "config-identified",
			instanceShape: "scalar",
			typeName:      "aws_sns_topic",
			addr:          `aws_sns_topic.scalar_config`,
			marker:        `aws_sns_topic.scalar_config`,
			id1:           "arn:aws:sns:us-east-1:000000000000:collide-scalar-a",
			id2:           "arn:aws:sns:us-east-1:000000000000:collide-scalar-b",
			wantKind:      ProblemCollision,
			wantDetailContains: []string{
				"2 live aws_sns_topic resources carry estate",
				`address "aws_sns_topic.scalar_config"`,
				"collide-scalar-a", "collide-scalar-b",
				"A human has to resolve the collision",
			},
		},
		{
			name:          "config-identified x count set",
			identityShape: "config-identified",
			instanceShape: "count set",
			typeName:      "aws_sns_topic",
			addr:          `aws_sns_topic.count_config[0]`,
			marker:        `aws_sns_topic.count_config:0`,
			id1:           "arn:aws:sns:us-east-1:000000000000:collide-count-a",
			id2:           "arn:aws:sns:us-east-1:000000000000:collide-count-b",
			wantKind:      ProblemNeedsSlotMarkers,
			wantDetailContains: []string{
				"2 live aws_sns_topic resources claim the count instance aws_sns_topic.count_config[0]",
				"collide-count-a", "collide-count-b",
				"fungible set",
			},
		},
		{
			name:          "config-identified x for_each set",
			identityShape: "config-identified",
			instanceShape: "for_each set",
			typeName:      "aws_sns_topic",
			addr:          `aws_sns_topic.foreach_config["a"]`,
			marker:        `aws_sns_topic.foreach_config:a`,
			id1:           "arn:aws:sns:us-east-1:000000000000:collide-foreach-a1",
			id2:           "arn:aws:sns:us-east-1:000000000000:collide-foreach-a2",
			wantKind:      ProblemCollision,
			wantDetailContains: []string{
				"2 live aws_sns_topic resources carry estate",
				`address "aws_sns_topic.foreach_config:a"`,
				"collide-foreach-a1", "collide-foreach-a2",
				"A human has to resolve the collision",
			},
		},

		// ---------------------------------------------------------------
		// ServerAssigned/ARN
		// ---------------------------------------------------------------
		{
			name:          "ServerAssigned/ARN x scalar",
			identityShape: "ServerAssigned/ARN",
			instanceShape: "scalar",
			typeName:      "aws_vpc",
			addr:          `aws_vpc.scalar_server`,
			marker:        `aws_vpc.scalar_server`,
			id1:           "vpc-collide-a",
			id2:           "vpc-collide-b",
			wantKind:      ProblemCollision,
			wantDetailContains: []string{
				"2 live aws_vpc resources carry estate",
				`address "aws_vpc.scalar_server"`,
				"vpc-collide-a", "vpc-collide-b",
				"A human has to resolve the collision",
			},
		},
		{
			name:          "ServerAssigned/ARN x count set",
			identityShape: "ServerAssigned/ARN",
			instanceShape: "count set",
			typeName:      "aws_eip",
			addr:          `aws_eip.count_server[0]`,
			marker:        `aws_eip.count_server:0`,
			id1:           "eipalloc-collide-a",
			id2:           "eipalloc-collide-b",
			wantKind:      ProblemNeedsSlotMarkers,
			wantDetailContains: []string{
				"2 live aws_eip resources claim the count instance aws_eip.count_server[0]",
				"eipalloc-collide-a", "eipalloc-collide-b",
				"fungible set",
			},
		},
		{
			name:          "ServerAssigned/ARN x for_each set",
			identityShape: "ServerAssigned/ARN",
			instanceShape: "for_each set",
			typeName:      "aws_subnet",
			addr:          `aws_subnet.foreach_server["a"]`,
			marker:        `aws_subnet.foreach_server:a`,
			id1:           "subnet-collide-a1",
			id2:           "subnet-collide-a2",
			wantKind:      ProblemCollision,
			wantDetailContains: []string{
				"2 live aws_subnet resources carry estate",
				`address "aws_subnet.foreach_server:a"`,
				"subnet-collide-a1", "subnet-collide-a2",
				"A human has to resolve the collision",
			},
		},

		// ---------------------------------------------------------------
		// record-only (GitHub issue #415's own fix: scalar and for_each
		// were silent before this commit - see this file's header comment)
		// ---------------------------------------------------------------
		{
			name:          "record-only x scalar",
			identityShape: "record-only",
			instanceShape: "scalar",
			typeName:      "aws_vpc",
			addr:          `aws_vpc.scalar_server`,
			marker:        `aws_vpc.scalar_server`,
			id1:           "vpc-record-a",
			id2:           "vpc-record-b",
			recordOnly:    true,
			wantKind:      ProblemCollision,
			wantDetailContains: []string{
				"2 live aws_vpc resources carry estate",
				`address "aws_vpc.scalar_server"`,
				"vpc-record-a", "vpc-record-b",
				"A human has to resolve the collision",
			},
		},
		{
			// Mirrors TestDiscover_recordBackedCollisionOnCountBlockIsReported
			// (recordbacked_demand_test.go, GitHub issue #411/#409) against
			// this file's own fixture, for the matrix's own record: the
			// claimants carry a MATCHING tofu-slot (slotted: true), the
			// shape a real migrated-then-corrupted estate leaves behind,
			// which would classify slots.ModeAll and reach
			// [ProblemDuplicateSlot] if the block held no record-backed
			// entry. It does, so [countBlock.hasRecordBackedEntry] routes
			// the whole block through bindCountByAddress unconditionally
			// (GitHub issue #409) before the slot tags are ever consulted,
			// and the outcome is ProblemNeedsSlotMarkers instead - the
			// exact reconciliation this issue's own Note describes. See
			// TestCollisionOutcomeMatrix_CountSetDuplicateSlotIsTheOtherHalf
			// below for the sibling case where the SAME matching-slot
			// shape, on a block with NO record-backed entry, really does
			// produce ProblemDuplicateSlot.
			name:          "record-only x count set",
			identityShape: "record-only",
			instanceShape: "count set",
			typeName:      "aws_eip",
			addr:          `aws_eip.count_server[0]`,
			marker:        `aws_eip.count_server:0`,
			id1:           "eipalloc-record-a",
			id2:           "eipalloc-record-b",
			recordOnly:    true,
			slotted:       true,
			wantKind:      ProblemNeedsSlotMarkers,
			wantDetailContains: []string{
				"2 live aws_eip resources claim the count instance aws_eip.count_server[0]",
				"eipalloc-record-a", "eipalloc-record-b",
				"fungible set",
			},
		},
		{
			name:          "record-only x for_each set",
			identityShape: "record-only",
			instanceShape: "for_each set",
			typeName:      "aws_subnet",
			addr:          `aws_subnet.foreach_server["a"]`,
			marker:        `aws_subnet.foreach_server:a`,
			id1:           "subnet-record-a1",
			id2:           "subnet-record-a2",
			recordOnly:    true,
			wantKind:      ProblemCollision,
			wantDetailContains: []string{
				"2 live aws_subnet resources carry estate",
				`address "aws_subnet.foreach_server:a"`,
				"subnet-record-a1", "subnet-record-a2",
				"A human has to resolve the collision",
			},
		},
	}

	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			res, diags := c.discover(t)

			if !diags.HasErrors() {
				t.Fatalf("%s x %s: a manufactured collision produced no error - the silent #411/#415 shape:\n%s", c.identityShape, c.instanceShape, res)
			}

			problems := res.ProblemsOfKind(c.wantKind)
			if len(problems) != 1 {
				t.Fatalf("%s x %s: want exactly one %s problem, got %d:\n%s", c.identityShape, c.instanceShape, c.wantKind, len(problems), res)
			}
			p := problems[0]

			if p.TypeName != c.typeName {
				t.Errorf("%s x %s: problem names type %q, want %q", c.identityShape, c.instanceShape, p.TypeName, c.typeName)
			}
			if p.Kind.Severity() != SeverityError {
				t.Errorf("%s x %s: %s is severity %s, want ERROR - this collision would not actually block a plan", c.identityShape, c.instanceShape, c.wantKind, p.Kind.Severity())
			}
			for _, want := range c.wantDetailContains {
				if !strings.Contains(p.Detail, want) {
					t.Errorf("%s x %s: detail does not contain %q:\n%s", c.identityShape, c.instanceShape, want, p.Detail)
				}
			}

			// The collision must not be quietly resolved into a Binding, and
			// the sibling problem kind (whichever of ProblemCollision /
			// ProblemNeedsSlotMarkers / ProblemDuplicateSlot this cell does
			// NOT want) must be absent - this is the refusal shape, not a
			// guess about which claimant is real, and it pins the routing
			// rather than merely tolerating whichever problem fires first.
			if len(res.Bindings) != 0 {
				t.Errorf("%s x %s: a contested set bound anyway:\n%s", c.identityShape, c.instanceShape, res)
			}
			for _, other := range []ProblemKind{ProblemCollision, ProblemNeedsSlotMarkers, ProblemDuplicateSlot} {
				if other == c.wantKind {
					continue
				}
				if got := res.ProblemsOfKind(other); len(got) != 0 {
					t.Errorf("%s x %s: unexpected %s problem alongside %s: %v", c.identityShape, c.instanceShape, other, c.wantKind, got)
				}
			}
		})
	}
}

// TestCollisionOutcomeMatrix_CountSetDuplicateSlotIsTheOtherHalf is this
// issue's Note made concrete: [ProblemDuplicateSlot] ("Two live resources
// claiming one slot") is not dead code. It is exactly what a matching-slot
// collision on a count set produces when NEITHER declared member is
// record-backed - the "old" pre-#409 message is still the real answer for
// an estate that has never migrated a record-backed instance into this
// block. Compare against the record-only x count set cell above, which
// manufactures the identical live-object shape (matching tofu-slot) on a
// block that DOES carry a record-backed entry and gets
// ProblemNeedsSlotMarkers instead - the two tests together pin that the
// record-backed flag, not the slot tags on the colliding claimants
// themselves, is what selects the message.
func TestCollisionOutcomeMatrix_CountSetDuplicateSlotIsTheOtherHalf(t *testing.T) {
	c := collisionCell{
		typeName: "aws_eip",
		addr:     `aws_eip.count_server[0]`,
		marker:   `aws_eip.count_server:0`,
		id1:      "eipalloc-dup-a",
		id2:      "eipalloc-dup-b",
		slotted:  true,
		// recordOnly deliberately false: no record-backed entry in this
		// block at all, so bindCountBlock classifies the live set by slot
		// instead of routing through bindCountByAddress unconditionally.
	}
	res, diags := c.discover(t)

	if !diags.HasErrors() {
		t.Fatalf("a matching-slot collision on a non-record-backed count set produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemDuplicateSlot)
	if len(problems) != 1 {
		t.Fatalf("want exactly one duplicate-slot problem, got %d:\n%s", len(problems), res)
	}
	p := problems[0]
	if p.TypeName != "aws_eip" {
		t.Errorf("problem names type %q, want aws_eip", p.TypeName)
	}
	if p.Kind.Severity() != SeverityError {
		t.Errorf("DUPLICATE_SLOT is severity %s, want ERROR", p.Kind.Severity())
	}
	for _, want := range []string{"eipalloc-dup-a", "eipalloc-dup-b", "same tofu-slot"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail does not contain %q:\n%s", want, p.Detail)
		}
	}
	if got := res.ProblemsOfKind(ProblemNeedsSlotMarkers); len(got) != 0 {
		t.Errorf("unexpected needs-slot-markers problem alongside duplicate-slot: %v", got)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a contested set bound anyway:\n%s", res)
	}
}

// TestCollisionOutcomeMatrix_RecordBackedTypesNeverReachDiscovery documents
// the N/A the Accept criteria asked for: [identity.TypeIdentity.RecordBacked]
// (GitHub issue #73's logical types - null_resource, terraform_data, the
// random_* and time_* families) is a DIFFERENT axis value than this
// matrix's own "record-only" row (see this file's header comment for the
// distinction), and every one of its instance shapes - scalar, count set,
// for_each set - is genuinely impossible to manufacture a marker collision
// for: such a type's instances always classify [identity.ClassRecordBacked],
// never ClassNeedsDiscovery (so they never enter this package's binding
// demand - declaredInstances' own indexing loop filters on
// ClassNeedsDiscovery before anything else runs), AND [cloudObservable]
// excludes them from the sweep too, so no provider list call is ever made
// for one either. There is no code path in this package by which a live
// object could be found carrying such a type's marker at all, which is what
// "collision" presupposes. This is pinned directly against the admission
// table and the function that reads it, rather than asserted as a comment
// nobody re-checks.
func TestCollisionOutcomeMatrix_RecordBackedTypesNeverReachDiscovery(t *testing.T) {
	for _, typeName := range []string{"null_resource", "terraform_data", "random_password", "time_sleep"} {
		ti, ok := identity.LookupType(typeName)
		if !ok {
			t.Errorf("%s is no longer in the admission table; this N/A's premise is stale", typeName)
			continue
		}
		if !ti.RecordBacked {
			t.Errorf("%s no longer sets TypeIdentity.RecordBacked; this N/A's premise is stale", typeName)
		}
		if ti.ServerAssigned {
			t.Errorf("%s sets both RecordBacked and ServerAssigned, which TypeIdentity's own doc comments treat as mutually exclusive rungs", typeName)
		}
		if cloudObservable(typeName) {
			t.Errorf("cloudObservable(%q) = true, want false - a RecordBacked type must never reach a provider list call, or a marker collision on it stops being N/A", typeName)
		}
	}
}
