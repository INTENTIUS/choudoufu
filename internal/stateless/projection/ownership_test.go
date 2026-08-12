// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/opentofu/opentofu/internal/stateless/identity"
	"github.com/opentofu/opentofu/internal/stateless/markers"
)

// These are audit finding C1's regression: a client-named resource's identity
// comes out of the configuration, so nothing about it ever passed through
// discovery or the foreign classifier, and the projection used to read
// whatever was at that identity straight into prior state. Naming an existing
// bucket, log group or role in a configuration was therefore enough to adopt
// it - and a later run deleting the block was enough to destroy it.
//
// The rule now is the marker spec's: a live object that can carry a marker
// enters prior state only when it carries this estate's.

const ownershipEstate = "ownership-unit"

// TestOwnership_unmarkedLiveResourceIsNotAdopted is the attack itself. A log
// group already exists at the name this configuration declares, owned by
// nobody who has heard of this estate. The projection must not contain it.
func TestOwnership_unmarkedLiveResourceIsNotAdopted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/somebody-elses/logs", map[string]string{
		"id": "/somebody-elses/logs", "name": "/somebody-elses/logs",
	}, nil)

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/somebody-elses/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)

	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatal("an unmarked live resource was adopted into the prior state, which is what makes a later run destroy it")
	}
	if len(res.Materialized) != 0 {
		t.Errorf("materialized %v, want nothing", res.Materialized)
	}

	// Absent for the right reason, and reported in a form the run can act on:
	// "the plan proposes creating this" has to be explained by "something
	// else is holding the name", not by "it does not exist".
	om := omissionFor(t, res, `aws_cloudwatch_log_group.app`)
	if om.Reason != ReasonUnowned {
		t.Errorf("omitted as %s, want %s", om.Reason, ReasonUnowned)
	}
	for _, want := range []string{"/somebody-elses/logs", markers.TagEstate, ownershipEstate} {
		if !strings.Contains(om.Detail, want) {
			t.Errorf("the omission does not mention %q:\n%s", want, om.Detail)
		}
	}
	if len(res.Unowned) != 1 {
		t.Fatalf("the refusal is not on the result: %+v", res.Unowned)
	}
	if got := res.Unowned[0]; got.ImportID != "/somebody-elses/logs" || got.Estate != "" {
		t.Errorf("the refusal does not describe the live resource: %+v", got)
	}
	if !hasDiag(diags, "Live resource outside this estate", "/somebody-elses/logs") {
		t.Errorf("the refusal produced no warning an operator would see:\n%s", renderDiags(diags))
	}
}

// TestOwnership_markedLiveResourceIsAdmitted is the other half: the estate's
// own resource, carrying its own marker, is read into prior state exactly as
// before. A rule that refused everything would be safe and useless.
func TestOwnership_markedLiveResourceIsAdmitted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: "aws_cloudwatch_log_group.app",
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("the estate's own marked resource was refused:\n%s", res)
	}
	if len(res.Unowned) != 0 {
		t.Errorf("a marked resource was reported as unowned: %+v", res.Unowned)
	}
}

// TestOwnership_anotherEstatesResourceIsNotAdopted: owned, but not here.
// Moving a resource between estates is a retag somebody performs, never a
// plan noticing it could.
func TestOwnership_anotherEstatesResourceIsNotAdopted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  "the-other-estate",
		markers.TagAddress: "aws_cloudwatch_log_group.app",
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatal("another estate's resource was adopted into the prior state")
	}
	if len(res.Unowned) != 1 || res.Unowned[0].Estate != "the-other-estate" {
		t.Fatalf("the refusal does not name the owning estate: %+v", res.Unowned)
	}
}

// TestOwnership_discoveryVerifiedInstancesRideThrough: an instance marker
// discovery already bound was found *by* its marker, so it is admitted
// without a second reading of the same tags. This is the path every
// server-assigned resource takes, and it must not be re-litigated here -
// notably not for a provider whose read does not serve tags back.
func TestOwnership_discoveryVerifiedInstancesRideThrough(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)

	cloud := newFakeCloud()
	cloud.putTagged("aws_route_table", "rtb-1", map[string]string{
		"id": "rtb-1", "vpc_id": "vpc-1",
	}, nil)

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-1"},
	}, cloud.providers(t), Options{Ownership: &Ownership{
		Estate:   ownershipEstate,
		Verified: map[string]bool{rtb.String(): true},
	}})

	assertNoErrors(t, diags)
	if !res.Has(rtb) {
		t.Fatalf("a marker-bound instance was refused by the ownership check:\n%s", res)
	}
}

// TestOwnership_noEstateNameVerifiesNothing: a run that established no estate
// name has nothing to check a marker against, so it adopts nothing. The cost
// is a plan full of creates; the alternative is the adoption this whole check
// exists to prevent, performed by the run least able to notice.
func TestOwnership_noEstateNameVerifiesNothing(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: "aws_cloudwatch_log_group.app",
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{}})

	assertNoErrors(t, diags)
	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatal("a run with no estate name adopted a live resource anyway")
	}
	om := omissionFor(t, res, `aws_cloudwatch_log_group.app`)
	if !strings.Contains(om.Detail, "no estate name") {
		t.Errorf("the omission does not say why nothing could be verified:\n%s", om.Detail)
	}
}

// TestOwnership_untaggableTypesAreAdmitted: the marker contract is defined
// over taggable types. A bucket policy or a route has nowhere to carry a
// marker and is identified by a composite of its parents, so refusing it
// would be a false accusation rather than a protection. The boundary is
// stated in stateless/MARKERS.md and asserted here so that it stays a
// decision rather than an oversight.
func TestOwnership_untaggableTypesAreAdmitted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_s3_bucket_policy", "ownership-unit-data", map[string]string{
		"id": "ownership-unit-data", "bucket": "ownership-unit-data",
		"policy": `{"Version":"2012-10-17"}`,
	}, nil)

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_s3_bucket_policy.data`), Class: identity.ClassConcrete, ImportID: "ownership-unit-data"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_s3_bucket_policy.data`)) {
		t.Fatalf("an untaggable type was refused for carrying no marker, which it cannot:\n%s", res)
	}
}

// TestOwnership_absentIsStillACreate: nothing exists at the identity, which
// is the ordinary "not created yet" answer and must stay distinct from
// "something is in the way".
func TestOwnership_absentIsStillACreate(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/nothing/here"},
	}, newFakeCloud().providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	om := omissionFor(t, res, `aws_cloudwatch_log_group.app`)
	if om.Reason != ReasonAbsent {
		t.Errorf("omitted as %s, want %s", om.Reason, ReasonAbsent)
	}
	if len(res.Unowned) != 0 {
		t.Errorf("a nonexistent resource was reported as somebody else's: %+v", res.Unowned)
	}
}
