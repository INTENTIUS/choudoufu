// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// The record-orphan leg's parent rule (maintainer ruling 2026-09-03, found
// by the carve-by-retag claim): an untaggable child's ownership is its
// parent's. These pin the two halves the rule is built from - splitting a
// flat import ID back into the ratified row's attribute values, and the
// "does this pass hold the parent" check - on the IAM shapes the carve
// moves, which the package's fake cloud does not serve and so cannot reach
// through [Discover] here. The Route 53 pair in recordorphan_read_test.go
// runs the same rule end to end through the leg.

func TestSplitImportIDByComponents_IAMInlinePolicy(t *testing.T) {
	entry, ok := identity.LookupType("aws_iam_role_policy")
	if !ok {
		t.Fatal("aws_iam_role_policy has no ratified row")
	}
	parts, ok := splitImportIDByComponents(entry, "tl-team-0001-role:tl-team-0001-inline")
	if !ok {
		t.Fatal("ROLENAME:POLICYNAME did not split")
	}
	if parts["role"] != "tl-team-0001-role" || parts["name"] != "tl-team-0001-inline" {
		t.Errorf("split = %v, want role=tl-team-0001-role name=tl-team-0001-inline", parts)
	}
}

func TestSplitImportIDByComponents_IAMAttachmentKeepsTheARNWhole(t *testing.T) {
	entry, ok := identity.LookupType("aws_iam_role_policy_attachment")
	if !ok {
		t.Fatal("aws_iam_role_policy_attachment has no ratified row")
	}
	const arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
	parts, ok := splitImportIDByComponents(entry, "tl-svc-0000-exec-role/"+arn)
	if !ok {
		t.Fatal("ROLENAME/POLICYARN did not split")
	}
	// The ARN carries its own "/" and ":" characters; the split has to stop
	// at the FIRST separator, which is the only one a role name cannot hold.
	if parts["role"] != "tl-svc-0000-exec-role" || parts["policy_arn"] != arn {
		t.Errorf("split = %v, want role=tl-svc-0000-exec-role policy_arn=%s", parts, arn)
	}
}

func TestSplitImportIDByComponents_RoundTripsCompose(t *testing.T) {
	for _, typeName := range []string{"aws_iam_role_policy", "aws_iam_role_policy_attachment", "aws_iam_user_policy"} {
		entry, ok := identity.LookupType(typeName)
		if !ok {
			t.Fatalf("%s has no ratified row", typeName)
		}
		var id string
		switch typeName {
		case "aws_iam_role_policy_attachment":
			id = "r/arn:aws:iam::123:policy/p"
		default:
			id = "owner:policy"
		}
		parts, ok := splitImportIDByComponents(entry, id)
		if !ok {
			t.Fatalf("%s: %q did not split", typeName, id)
		}
		back, ok := composeImportIDFromComponents(typeName, parts)
		if !ok || back != id {
			t.Errorf("%s: split then compose gave %q (ok=%v), want %q", typeName, back, ok, id)
		}
	}
}

func TestSplitImportIDByComponents_RefusesWhatItCannotSplitSoundly(t *testing.T) {
	entry, ok := identity.LookupType("aws_iam_role_policy")
	if !ok {
		t.Fatal("aws_iam_role_policy has no ratified row")
	}
	for _, id := range []string{"", "no-separator", ":leading", "trailing:"} {
		if parts, ok := splitImportIDByComponents(entry, id); ok {
			t.Errorf("%q split to %v; it has no sound reading under ROLENAME:POLICYNAME", id, parts)
		}
	}
	// Two adjacent attribute components with no literal between them have
	// no boundary to split at; the function refuses rather than guesses.
	adjacent := identity.TypeIdentity{Components: []identity.Component{{Attrs: []string{"a"}}, {Attrs: []string{"b"}}}}
	if parts, ok := splitImportIDByComponents(adjacent, "ab"); ok {
		t.Errorf("adjacent attribute components split to %v; want a refusal", parts)
	}
}

func TestParentHeldByThisPass(t *testing.T) {
	res := &Result{}
	res.Resolutions = []identity.Resolution{
		{Addr: mustAddr(t, "aws_iam_role.kept"), Class: identity.ClassConcrete, ImportID: "kept-role"},
		{Addr: mustAddr(t, "aws_iam_role.unbound"), Class: identity.ClassConcrete},
	}
	if !parentHeldByThisPass(res, "aws_iam_role", "kept-role") {
		t.Error("a resolved parent was not recognized as held")
	}
	if parentHeldByThisPass(res, "aws_iam_role", "moved-role") {
		t.Error("a parent no resolution names was reported as held")
	}
	// An unbound resolution carries no identity and vouches for nothing.
	if parentHeldByThisPass(res, "aws_iam_role", "") {
		t.Error("an empty identity matched an unbound resolution")
	}
	if parentHeldByThisPass(res, "aws_iam_user", "kept-role") {
		t.Error("a parent of another type matched on value alone")
	}
}

// The two signals at the call site, run through the leg itself on the one
// parent-linked shape the package's fake cloud can make taggable: a Route 53
// record whose zone is its parent. Each test seeds the same undeclared
// record and varies only what the pass knows about the zone.
func seedOrphanRecord(t *testing.T, estate string) (staterecord.Store, addrs.AbsResourceInstance) {
	t.Helper()
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
	addr := mustAddr(t, "aws_route53_record.moved")
	if _, err := projection.SeedLocatedForInstance(t.Context(), store, addr, addrs.AbsProviderConfig{}, projection.LocatedRecord{
		Components: map[string]string{"zone_id": "ZMOVED", "name": "moved.example", "type": "A"},
	}); err != nil {
		t.Fatalf("seeding: %s", err)
	}
	return raw, addr
}

func orphanAppended(res *Result, addr addrs.AbsResourceInstance) bool {
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr.String() {
			return true
		}
	}
	return false
}

// The live tag wins: the sweep saw the zone carrying another estate's
// marker, so the record is not proposed even though a resolution in the pass
// still names that zone as this estate's.
func TestRecordOrphanReadSweep_ParentHeldByOtherEstateWinsOverAResolution(t *testing.T) {
	const estate = "test-estate"
	raw, addr := seedOrphanRecord(t, estate)
	res := &Result{}
	res.Resolutions = []identity.Resolution{{Addr: mustAddr(t, "aws_route53_zone.stale"), Class: identity.ClassConcrete, ImportID: "ZMOVED"}}
	res.OtherEstateHeld = map[string]map[string]bool{"aws_route53_zone": {"ZMOVED": true}}

	diags := recordOrphanReadSweep(t.Context(), Request{Estate: estate, HintStore: raw}, destroyParentDependencySchemas(t), res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if orphanAppended(res, addr) {
		t.Fatalf("%s was proposed although the sweep saw its zone held by another estate", addr)
	}
}

// The fallback: nothing recorded the zone's tag and nothing in the pass holds
// it, so the child is skipped on the safe side.
func TestRecordOrphanReadSweep_ParentUnheldIsSkipped(t *testing.T) {
	const estate = "test-estate"
	raw, addr := seedOrphanRecord(t, estate)
	res := &Result{}

	diags := recordOrphanReadSweep(t.Context(), Request{Estate: estate, HintStore: raw}, destroyParentDependencySchemas(t), res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if orphanAppended(res, addr) {
		t.Fatalf("%s was proposed although nothing in the pass holds its zone", addr)
	}
}

// The positive control: the pass holds the zone and the map says nothing,
// so the record is proposed as this estate's orphan, exactly as before.
func TestRecordOrphanReadSweep_ParentHeldByThisPassIsProposed(t *testing.T) {
	const estate = "test-estate"
	raw, addr := seedOrphanRecord(t, estate)
	res := &Result{}
	res.Resolutions = []identity.Resolution{{Addr: mustAddr(t, "aws_route53_zone.kept"), Class: identity.ClassConcrete, ImportID: "ZMOVED"}}

	diags := recordOrphanReadSweep(t.Context(), Request{Estate: estate, HintStore: raw}, destroyParentDependencySchemas(t), res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if !orphanAppended(res, addr) {
		t.Fatalf("%s was not proposed although the pass holds its zone", addr)
	}
}

// The regression the parent rule shipped with, measured on
// corpus-simpleinfra-dns's day2_count (GitHub issues #872/#873, and the same
// shape on #868/#871/#875): dropping a key from a for_each block of
// untaggable, record-backed children proposed NO destroy at all for the
// dropped child, because the check that asks whether this estate holds the
// child's parent only ever accepted a parent this same pass had bound to an
// ImportID.
//
// The foundation-order ruling (HANDOFF.md, "The foundation") is what makes
// that the ordinary case rather than an edge: the record holds every
// instance's identity and a plan reads it first, so a DECLARED parent whose
// identity the estate's own record already answers is never re-derived from
// a marker in this pass and sits in res.Resolutions as ClassNeedsDiscovery
// with an empty ImportID. Instrumenting a real run of that estate showed all
// seven declared aws_route53_zone resolutions in exactly that state at this
// point, and the dropped record's destroy withheld with "not declared, and
// not swept carrying this estate's marker" - about a zone the configuration
// declares on the line above.
//
// The fixture is the failure's own shape: the parent is DECLARED, its
// identity is in the estate's record store, and the cloud lists nothing, so
// no marker sweep can bind anything. Compare
// TestRecordOrphanReadSweep_Route53RecordWithNoParentHeldIsNotProposed,
// which is the ruling's own case and stays red-side: there the parent's
// block is GONE, so nothing declares it and no record arm can reach it.
func TestRecordOrphanReadSweep_DeclaredParentAnsweredOnlyByTheRecordStillHoldsItsChild(t *testing.T) {
	dir := t.TempDir()
	const src = `
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
}

resource "aws_route53_zone" "eu" {
  name = "datacite.eu"
}

resource "aws_route53_record" "cname" {
  for_each = toset(["2022", "2023"])
  zone_id  = aws_route53_zone.eu.zone_id
  name     = "${each.key}.datacite.eu"
  type     = "CNAME"
  ttl      = 300
  records  = ["x.example.com"]
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	resolutions := resolveOrFail(t, cfg).All()

	rawStore, seedStore := recordOrphanHintStore(t)
	const zoneID = "ZJB88OBW3J7TXGA"
	if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, mustAddr(t, "aws_route53_zone.eu"), recordOrphanProviderAddr, projection.LocatedRecord{
		ImportID: zoneID,
	}); err != nil {
		t.Fatalf("seeding the zone's identity record: %s", err)
	}
	// Every key the block ever had, the way an apply leaves them: the two
	// still declared and the one just dropped.
	for _, key := range []string{"2022", "2023", "2024"} {
		addr := mustAddr(t, fmt.Sprintf("aws_route53_record.cname[%q]", key))
		if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, addr, recordOrphanProviderAddr, projection.LocatedRecord{
			Components: map[string]string{
				"zone_id": zoneID,
				"name":    key + ".datacite.eu",
				"type":    "CNAME",
			},
		}); err != nil {
			t.Fatalf("seeding %s: %s", addr, err)
		}
	}

	// Nothing live is listed at all, so no resolution in the pass is ever
	// bound to an ImportID - the state the record-first path leaves a
	// declared parent in, reproduced without needing a marker sweep to be
	// skipped for some other reason.
	cloud := newFakeCloud()

	res, diags := Discover(t.Context(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    cloud,
		Sweep:       true,
		HintStore:   rawStore,
	})
	assertNoErrors(t, diags)

	// The parent really is in the state this test is about: declared, and
	// carrying no ImportID for the old check to have matched on.
	var sawUnboundParent bool
	for _, r := range res.Resolutions {
		if r.Addr.String() == "aws_route53_zone.eu" && !r.Undeclared && r.ImportID == "" {
			sawUnboundParent = true
		}
	}
	if !sawUnboundParent {
		t.Fatalf("the fixture no longer reproduces the shape: aws_route53_zone.eu is not a declared resolution with an empty ImportID in:\n%s", res)
	}

	dropped := mustAddr(t, `aws_route53_record.cname["2024"]`)
	const wantID = "ZJB88OBW3J7TXGA_2024.datacite.eu_CNAME"
	var got *identity.Resolution
	for i, r := range res.Resolutions {
		if r.Addr.String() == dropped.String() {
			got = &res.Resolutions[i]
		}
	}
	if got == nil {
		t.Fatalf("the dropped for_each key %s was not proposed for removal, so its live object would be left orphaned; res:\n%s", dropped, res)
	}
	if !got.Undeclared {
		t.Errorf("resolution for %s is not marked Undeclared: %+v", dropped, *got)
	}
	if got.Class != identity.ClassConcrete {
		t.Errorf("resolution for %s has class %v, want %v", dropped, got.Class, identity.ClassConcrete)
	}
	if got.ImportID != wantID {
		t.Errorf("resolution for %s has ImportID %q, want %q", dropped, got.ImportID, wantID)
	}

	// The two keys the configuration still declares must not be dragged in
	// with it - the scale-down destroys exactly one instance.
	for _, key := range []string{"2022", "2023"} {
		addr := mustAddr(t, fmt.Sprintf("aws_route53_record.cname[%q]", key))
		for _, r := range res.Resolutions {
			if r.Addr.String() == addr.String() && r.Undeclared {
				t.Errorf("%s is still declared but was proposed for removal: %+v", addr, r)
			}
		}
	}
}
