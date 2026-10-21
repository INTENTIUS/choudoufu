// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// The third GCP gauntlet run's day2_count on corpus-k8s-infra-gcp-gcve: a
// for_each key of google_project_service left the configuration, this
// estate destroyed it (the record became a tombstone), the destroy left the
// service enabled, and the key came back. Stock planned one add; this
// planned "No changes", because a type with no marker surface found live at
// its configured identity was admitted with nothing asked. The fake cloud's
// untaggable type stands in for google_project_service: the rule reads the
// marker surface off the schema, never a type name.
func releasedFixture(t *testing.T, tombstone bool) (*Result, error) {
	t.Helper()
	ctx := context.Background()
	const estate = ownershipEstate
	addr := mustAddr(t, `aws_s3_bucket_policy.data`)

	cloud := newFakeCloud()
	cloud.put("aws_s3_bucket_policy", "policy-live", map[string]string{
		"id": "policy-live", "bucket": "ownership-unit-data",
	})

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)
	if tombstone {
		if err := SeedTombstoneForInstance(ctx, located.rs, addr, TombstoneRecord{ImportID: "policy-live"}); err != nil {
			return nil, err
		}
	}

	cfg := loadConfig(t, "testdata/named")
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "policy-live"}},
		cloud.providers(t),
		Options{
			RecordStore: located.rs,
			Ownership:   &Ownership{Estate: estate},
		})
	assertNoErrors(t, diags)
	return res, nil
}

// TestReleasedByOwnDestroy_plansTheCreate: the tombstone names the live
// object, so it is not this estate's and the plan proposes a create.
func TestReleasedByOwnDestroy_plansTheCreate(t *testing.T) {
	res, err := releasedFixture(t, true)
	if err != nil {
		t.Fatalf("seeding the tombstone: %s", err)
	}
	assertMaterialized(t, res, nil)
	assertOmitted(t, res, map[string]Reason{"aws_s3_bucket_policy.data": ReasonAbsent})
	// The plan-node seam reads Released through NodeResolver.Unowned; an
	// instance missing here would be imported there and planned as no change.
	if len(res.Released) != 1 || res.Released[0].String() != "aws_s3_bucket_policy.data" {
		t.Errorf("Released = %v, want [aws_s3_bucket_policy.data]", res.Released)
	}
	if d := omissionFor(t, res, "aws_s3_bucket_policy.data").Detail; !strings.Contains(d, "destroyed") {
		t.Errorf("the omission does not say this estate destroyed the object:\n%s", d)
	}
}

// TestReleasedByOwnDestroy_noTombstoneStillAdmits: with no record and no
// tombstone the declaration-carried instance is admitted as before. The
// wider question (an unrecorded object nobody released) is not decided here.
func TestReleasedByOwnDestroy_noTombstoneStillAdmits(t *testing.T) {
	res, err := releasedFixture(t, false)
	if err != nil {
		t.Fatal(err)
	}
	assertMaterialized(t, res, []string{"aws_s3_bucket_policy.data"})
	if len(res.Released) != 0 {
		t.Errorf("Released = %v with no tombstone, want none", res.Released)
	}
}
