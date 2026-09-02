// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/policy"
)

// A record-backed type has no cloud object: choudoufu persists it itself,
// per estate, and internal/live/projection materializes an instance from
// that record without any cloud read. Both type universes that drive a
// provider list call - the estate-wide sweep's and scoped account
// reconciliation's - therefore exclude it, and these tests are what say so.
//
// The external source they check against is internal/live/lint's
// [lint.ClassifyLogicalType], which classifies RECORD_ADMITTED from its own
// hand-audited table of provider documentation and knows nothing about
// [identity.TypeIdentity.RecordBacked]. Editing the identity table alone
// cannot make these tests agree with themselves.

// lintRecordAdmittedTypes is every type in the admission table that lint
// independently classifies RECORD_ADMITTED, from its own hand-audited table
// of provider documentation. This is the external floor: a type on this list
// must be excluded from every list universe, and no edit to the identity
// table can make that true by itself.
//
// It is a floor rather than the whole set because the two sources disagree
// today by four rows. identity.DefaultTable marks fourteen types
// RecordBacked (row-gen derives them from the effects providers' schemas);
// lint's logicalTypes table has hand-audited rows for ten of them and
// defaults random_string, random_uuid, random_uuid4 and random_uuid7 to
// ClassOtherRefused on the "random_" prefix, which means lint still refuses
// those four outright. That skew is row-gen's and lint's to settle. It does
// not change what this file asserts either way: a type with no cloud object
// is not swept whether or not a configuration can declare it.
func lintRecordAdmittedTypes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, typeName := range identity.AdmittedTypes() {
		lt, ok := lint.ClassifyLogicalType(typeName)
		if ok && lt.Class == lint.ClassRecordAdmitted {
			out = append(out, typeName)
		}
	}
	if len(out) == 0 {
		t.Fatal("no admitted type classifies RECORD_ADMITTED; this test can see nothing")
	}
	return out
}

// recordBackedAdmittedTypes is the exclusion set itself: every admission
// table row marked RecordBacked. [lintRecordAdmittedTypes] must be a subset
// of it, which is the containment the tests below check before using it.
func recordBackedAdmittedTypes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, typeName := range identity.AdmittedTypes() {
		if entry, ok := identity.LookupType(typeName); ok && entry.RecordBacked {
			out = append(out, typeName)
		}
	}
	if len(out) == 0 {
		t.Fatal("no admitted type is RecordBacked; this test can see nothing")
	}
	in := make(map[string]bool, len(out))
	for _, typeName := range out {
		in[typeName] = true
	}
	for _, typeName := range lintRecordAdmittedTypes(t) {
		if !in[typeName] {
			t.Errorf("lint classifies %s RECORD_ADMITTED but the admission table does not mark it RecordBacked", typeName)
		}
	}
	return out
}

// TestSweepUniverseExcludesRecordBackedTypes: the estate-wide sweep never
// asks a provider to list a type that has no cloud object.
//
// sweepTypes is the single choke point for all three sweep paths - the
// per-type list loop, [sweepViaTagging]'s estate-wide GetResources, and
// [guidedSweepUniverse] - so excluding a type here excludes it from every
// one of them.
func TestSweepUniverseExcludesRecordBackedTypes(t *testing.T) {
	empty := &declared{types: map[string]map[string]*declaredEntry{}}
	universe := sweepTypes(Request{}, empty)

	inUniverse := make(map[string]bool, len(universe))
	for _, typeName := range universe {
		inUniverse[typeName] = true
	}

	// The external floor first: nothing lint calls RECORD_ADMITTED is swept.
	for _, typeName := range lintRecordAdmittedTypes(t) {
		if inUniverse[typeName] {
			t.Errorf("%s is RECORD_ADMITTED but the sweep would still list it", typeName)
		}
	}

	excluded := recordBackedAdmittedTypes(t)
	for _, typeName := range excluded {
		if inUniverse[typeName] {
			t.Errorf("%s is RecordBacked but the sweep would still list it", typeName)
		}
	}

	// The exclusion is exactly that set and nothing wider: every other
	// admitted type is still swept.
	isExcluded := make(map[string]bool, len(excluded))
	for _, typeName := range excluded {
		isExcluded[typeName] = true
	}
	for _, typeName := range identity.AdmittedTypes() {
		if !isExcluded[typeName] && !inUniverse[typeName] {
			t.Errorf("%s is not RecordBacked but the sweep dropped it", typeName)
		}
	}

	if want := len(identity.AdmittedTypes()) - len(excluded); len(universe) != want {
		t.Errorf("sweep universe is %d types, want %d (%d admitted minus %d record-backed)",
			len(universe), want, len(identity.AdmittedTypes()), len(excluded))
	}
}

// TestSweepUniverseExcludesRecordBackedTypesWhenAskedByName: the exclusion is
// a property of the type, not of who asked. An explicit Request.SweepTypes
// naming one gets the same answer, because there is still no cloud object to
// list.
func TestSweepUniverseExcludesRecordBackedTypesWhenAskedByName(t *testing.T) {
	excluded := recordBackedAdmittedTypes(t)
	empty := &declared{types: map[string]map[string]*declaredEntry{}}

	got := sweepTypes(Request{SweepTypes: append([]string{"aws_s3_bucket"}, excluded...)}, empty)
	if len(got) != 1 || got[0] != "aws_s3_bucket" {
		t.Errorf("explicit SweepTypes universe is %v, want only [aws_s3_bucket]", got)
	}
}

// TestReconcileTypeUniverseExcludesRecordBackedTypes: scoped account
// reconciliation lists through the same provider for the same reason, and
// files a NOT_ENUMERABLE gap for every type it cannot list. A record-backed
// type would have produced one on every scoped pass.
func TestReconcileTypeUniverseExcludesRecordBackedTypes(t *testing.T) {
	excluded := recordBackedAdmittedTypes(t)

	t.Run("unnarrowed scope", func(t *testing.T) {
		universe := reconcileTypeUniverse(&policy.Scope{Regions: []string{"us-east-1"}})
		in := make(map[string]bool, len(universe))
		for _, typeName := range universe {
			in[typeName] = true
		}
		for _, typeName := range excluded {
			if in[typeName] {
				t.Errorf("%s is RecordBacked but reconciliation would still list it", typeName)
			}
		}
		if want := len(identity.AdmittedTypes()) - len(excluded); len(universe) != want {
			t.Errorf("reconcile universe is %d types, want %d", len(universe), want)
		}
	})

	t.Run("scope naming one explicitly", func(t *testing.T) {
		scope := &policy.Scope{Types: append([]string{"aws_s3_bucket"}, excluded...)}
		got := reconcileTypeUniverse(scope)
		if len(got) != 1 || got[0] != "aws_s3_bucket" {
			t.Errorf("scoped universe is %v, want only [aws_s3_bucket]", got)
		}
	})
}

// TestCloudObservableAdmitsAnUnknownType is the boundary [cloudObservable]
// draws: a type outside the admission table is not thereby record-backed.
// The sweep's own universe cannot contain one, but an explicit
// Request.SweepTypes can, and dropping it silently would be a wider
// exclusion than the rule this change makes.
func TestCloudObservableAdmitsAnUnknownType(t *testing.T) {
	if _, ok := identity.LookupType("aws_not_a_real_type"); ok {
		t.Fatal("aws_not_a_real_type is in the admission table; pick another name")
	}
	if !cloudObservable("aws_not_a_real_type") {
		t.Error("an unadmitted type was treated as record-backed")
	}
}

// TestSweepReportsNoRecordBackedGap is the end-to-end half: a full swept
// pass against the fixture cloud files no gap for a record-backed type.
// Before [cloudObservable], every one of them produced a
// [SweepGapNotListable] telling the operator that removal coverage had a
// hole where no cloud resource could ever have existed.
func TestSweepReportsNoRecordBackedGap(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	if len(res.SweepGaps) == 0 {
		t.Fatal("the fixture produced no sweep gaps at all; this test can see nothing")
	}
	for _, g := range res.SweepGaps {
		if lt, ok := lint.ClassifyLogicalType(g.TypeName); ok && lt.Class == lint.ClassRecordAdmitted {
			t.Errorf("sweep gap %s filed for RECORD_ADMITTED type %s", g.Reason, g.TypeName)
		}
		if entry, ok := identity.LookupType(g.TypeName); ok && entry.RecordBacked {
			t.Errorf("sweep gap %s filed for RecordBacked type %s", g.Reason, g.TypeName)
		}
	}
}
