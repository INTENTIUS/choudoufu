// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #364 unit B: [builder.applyRecordFirst] reads the estate's
// record store for every resolution ahead of identity.Class routing, and
// the "In upstream terms" ruling (#389, 2026-08-23) pins one rule with both
// halves named: a TAGGABLE type's record is trusted only while the live
// object's own tofu-address marker still confirms it, and an UNTAGGABLE
// type's record is trusted exactly as [builder.materializeLocated] trusts
// one today, because there is nothing on the object to check it against.
//
// These two tests are that rule, asserted by value rather than by exit
// code: the taggable case must produce the FALLBACK binding (the object the
// configuration's own identity names, not the one the stale record named)
// and the warning; the untaggable case must produce the RECORD's binding,
// with nothing to say about it.

// TestRecordFirst_staleTaggableRecordFallsBackAndWarns is the taggable half.
//
// aws_cloudwatch_log_group.app has a record pointing at "log-stale", a live
// object tagged for a DIFFERENT address (aws_cloudwatch_log_group.other).
// The configuration's own identity (an ordinary ClassConcrete resolution,
// exactly as it would resolve with no record store at all) names
// "log-correct", a live object correctly tagged for app.
//
// A defect that trusted the record unconditionally - the located route's
// own rule, reused without the taggable check - would materialize
// log-stale's attributes under app: the wrong object, silently. This test
// fails on that shape because it asserts the MATERIALIZED VALUE, not merely
// that something materialized.
func TestRecordFirst_staleTaggableRecordFallsBackAndWarns(t *testing.T) {
	ctx := context.Background()
	const estate = ownershipEstate
	appAddr := mustAddr(t, `aws_cloudwatch_log_group.app`)
	otherAddr := `aws_cloudwatch_log_group.other`

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "log-correct",
		map[string]string{"id": "log-correct", "name": "/ours/correct"},
		map[string]string{
			markers.TagEstate:  estate,
			markers.TagAddress: markers.EscapeAddress(appAddr.String()),
		})
	cloud.putTagged("aws_cloudwatch_log_group", "log-stale",
		map[string]string{"id": "log-stale", "name": "/ours/stale"},
		map[string]string{
			markers.TagEstate:  estate,
			markers.TagAddress: markers.EscapeAddress(otherAddr),
		})

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)
	if _, err := located.Put(ctx, appAddr, LocatedRecord{ImportID: "log-stale"}, ""); err != nil {
		t.Fatalf("seeding the stale record: %s", err)
	}

	cfg := loadConfig(t, "testdata/named")
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: appAddr, Class: identity.ClassConcrete, ImportID: "log-correct"}},
		cloud.providers(t),
		Options{
			RecordStore: located.rs,
			Ownership:   &Ownership{Estate: estate},
		})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{"aws_cloudwatch_log_group.app"})
	inst := res.State.ResourceInstance(appAddr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for app")
	}
	obj, err := inst.Current.Decode(fakeSchemas()["aws_cloudwatch_log_group"].Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the materialized object: %s", err)
	}
	if got := obj.Value.GetAttr("id").AsString(); got != "log-correct" {
		t.Fatalf("app materialized from id %q, want %q (the configuration's own identity): a stale record must fall back rather than bind unconditionally",
			got, "log-correct")
	}

	var found bool
	for _, d := range diags {
		if d.Description().Summary == SummaryStaleRecord {
			found = true
			detail := d.Description().Detail
			if !strings.Contains(detail, "log-stale") {
				t.Errorf("the stale-record warning does not name the record's own identity:\n%s", detail)
			}
			if !strings.Contains(detail, otherAddr) {
				t.Errorf("the stale-record warning does not name the address the live object actually carries:\n%s", detail)
			}
		}
	}
	if !found {
		t.Errorf("no %q diagnostic; got %v", SummaryStaleRecord, diags)
	}
}

// TestRecordFirst_untaggableRecordStillBinds is the untaggable half: a type
// with no tags attribute at all has nothing for [builder.checkOwnership] to
// verify the record against, so the record is trusted exactly as
// [builder.materializeLocated] trusts it today - even reached through the
// new universal path, from a resolution whose own identity.Class
// (ClassNeedsDiscovery, with no import identity of its own) could never
// have produced a binding by itself.
func TestRecordFirst_untaggableRecordStillBinds(t *testing.T) {
	ctx := context.Background()
	const estate = ownershipEstate
	addr := mustAddr(t, `aws_s3_bucket_policy.data`)

	cloud := newFakeCloud()
	// No tags at all: aws_s3_bucket_policy is in fakeUntaggable, so
	// fakeSchemas gives it no tags attribute regardless.
	cloud.put("aws_s3_bucket_policy", "policy-from-record", map[string]string{
		"id": "policy-from-record", "bucket": "ownership-unit-data",
	})

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)
	if _, err := located.Put(ctx, addr, LocatedRecord{ImportID: "policy-from-record"}, ""); err != nil {
		t.Fatalf("seeding the record: %s", err)
	}

	cfg := loadConfig(t, "testdata/named")
	res, diags := BuildWith(ctx, cfg,
		// ClassNeedsDiscovery, with no ImportID: nothing about this
		// resolution's own identity.Class could ever materialize it. If
		// this test passes, the binding came from the record.
		[]identity.Resolution{{Addr: addr, Class: identity.ClassNeedsDiscovery, Reason: "server-assigned"}},
		cloud.providers(t),
		Options{
			RecordStore: located.rs,
			Ownership:   &Ownership{Estate: estate},
		})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{"aws_s3_bucket_policy.data"})
	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for aws_s3_bucket_policy.data")
	}
	obj, err := inst.Current.Decode(fakeSchemas()["aws_s3_bucket_policy"].Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the materialized object: %s", err)
	}
	if got := obj.Value.GetAttr("id").AsString(); got != "policy-from-record" {
		t.Fatalf("materialized from id %q, want %q (the record's own identity)", got, "policy-from-record")
	}

	for _, d := range diags {
		if d.Description().Summary == SummaryStaleRecord {
			t.Errorf("an untaggable type's record produced a stale-record warning, which has nothing to verify it against and must always be trusted:\n%s", d.Description().Detail)
		}
	}
}
