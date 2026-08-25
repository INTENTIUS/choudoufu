// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// This file is gauntlet:record-located-destroy's own unit
// (corpus-alb-complete's day2_remove wall, 2026-08-25): before this unit,
// [composeImportIDFromComponents] refused EVERY aws_lb_target_group_attachment
// record outright, the instant it reached the row's port component
// (Attrs: []string{"port"}, OmitIfAbsent: true) - whether or not the
// record's own Components map actually carried a port value - because the
// old guard tested the ratified ROW's shape ("does this type have an
// OmitIfAbsent component anywhere") rather than the RECORD's content
// ("does this instance's value happen to be absent"). So the attachment's
// record never reached [recordOrphanReadSweep]'s materialize/import-and-read
// call at all: not the located.go exclusion (that governs a DIFFERENT,
// direct record-key sweep in the projection package, and never ran for
// this leg), not a blockKey scoping issue, not the sweep's taggability or
// pending-rename filters - the string composer refused before any of that
// mattered.
//
// These tests are value-asserted against the exact composed identity
// string, both directions: present and absent for each OmitIfAbsent
// component, independently, plus the unaffected IAM-policy shapes that
// motivated this file's original narrow guard.

// targetGroupAttachmentComponents is corpus-alb-complete's own two live
// shapes (terraform-aws-modules/terraform-aws-alb's `this` and `additional`
// for_each populations): an instance-target attachment always carries a
// port, a lambda-target attachment never does (botocore's elbv2 model, see
// internal/live/identity/targetgroupattachment_omitifabsent_test.go).
const (
	tgaARN = "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/inst-tg/def456"
	tgaTID = "i-0123456789abcdef0"
)

func TestComposeImportIDFromComponents_TargetGroupAttachment_PortPresent(t *testing.T) {
	got, ok := composeImportIDFromComponents("aws_lb_target_group_attachment", map[string]string{
		"target_group_arn": tgaARN,
		"target_id":        tgaTID,
		"port":             "80",
	})
	if !ok {
		t.Fatal("composeImportIDFromComponents refused an instance-target attachment with target_group_arn, target_id and port all present - this is the exact record shape aws_instance.other_renamed's own attachment writes, and refusing it is corpus-alb-complete's day2_remove wall")
	}
	want := tgaARN + "," + tgaTID + ",80"
	if got != want {
		t.Errorf("composed %q, want %q", got, want)
	}
}

func TestComposeImportIDFromComponents_TargetGroupAttachment_PortAbsent(t *testing.T) {
	// The lambda-target shape: port, availability_zone and quic_server_id
	// are all absent from the record's Components map - not empty strings,
	// genuinely absent keys, the same shape LocatedRecordFrom writes for a
	// lambda target (internal/live/projection/located_targetgroupattachment_test.go).
	got, ok := composeImportIDFromComponents("aws_lb_target_group_attachment", map[string]string{
		"target_group_arn": tgaARN,
		"target_id":        tgaTID,
	})
	if !ok {
		t.Fatal("composeImportIDFromComponents refused a lambda-target attachment whose only two required components are present - a null port must never be a reason to withhold the whole identity")
	}
	want := tgaARN + "," + tgaTID
	if got != want {
		t.Errorf("composed %q, want %q - the two-field form, no dangling separator for the omitted port", got, want)
	}
}

// TestComposeImportIDFromComponents_TargetGroupAttachment_EachOptionalIndependent
// proves the three trailing OmitIfAbsent components (port,
// availability_zone, quic_server_id) are each decided from the record's own
// content, not as a single present/absent block - the shape a compose that
// merely special-cased "no optional components at all" would still get
// wrong.
func TestComposeImportIDFromComponents_TargetGroupAttachment_EachOptionalIndependent(t *testing.T) {
	got, ok := composeImportIDFromComponents("aws_lb_target_group_attachment", map[string]string{
		"target_group_arn":  tgaARN,
		"target_id":         tgaTID,
		"availability_zone": "us-east-1a",
		// port and quic_server_id both absent.
	})
	if !ok {
		t.Fatal("composeImportIDFromComponents refused a record with only availability_zone set among the three optional components")
	}
	want := tgaARN + "," + tgaTID + ",us-east-1a"
	if got != want {
		t.Errorf("composed %q, want %q - availability_zone must ride along on its own, independent of port and quic_server_id", got, want)
	}
}

func TestComposeImportIDFromComponents_TargetGroupAttachment_MissingRequiredComponentRefuses(t *testing.T) {
	// The boundary this fix must not cost: target_group_arn and target_id
	// are REQUIRED (no OmitIfAbsent on either), so a record missing one of
	// them is not a valid identity and must still refuse rather than
	// compose a truncated string.
	_, ok := composeImportIDFromComponents("aws_lb_target_group_attachment", map[string]string{
		"target_id": tgaTID,
	})
	if ok {
		t.Fatal("composeImportIDFromComponents composed an identity with target_group_arn missing - a required component's absence must still refuse, exactly as before this fix")
	}
}

// TestComposeImportIDFromComponents_IAMPolicyShapesUnaffected is the
// wrong-marker-risk check this unit's brief names by name: harbor's
// aws_iam_user_policy, labelbox's and a bare aws_iam_role_policy/
// aws_iam_group_policy record must compose byte-identically to before this
// change, because none of their ratified components carries OmitIfAbsent -
// only ServerAssignedIfAbsent on "name", which this function has never
// treated specially in either direction - so the fix (which only widens
// what an OmitIfAbsent component does on absence) is a structural no-op for
// this whole population.
func TestComposeImportIDFromComponents_IAMPolicyShapesUnaffected(t *testing.T) {
	cases := []struct {
		typeName string
		attr     string
		want     string
	}{
		{"aws_iam_role_policy", "role", "harbor-role:harbor-inline-policy"},
		{"aws_iam_user_policy", "user", "labelbox-user:labelbox-inline-policy"},
		{"aws_iam_group_policy", "group", "some-group:some-inline-policy"},
	}
	for _, tc := range cases {
		t.Run(tc.typeName, func(t *testing.T) {
			parts := map[string]string{}
			switch tc.typeName {
			case "aws_iam_role_policy":
				parts["role"], parts["name"] = "harbor-role", "harbor-inline-policy"
			case "aws_iam_user_policy":
				parts["user"], parts["name"] = "labelbox-user", "labelbox-inline-policy"
			case "aws_iam_group_policy":
				parts["group"], parts["name"] = "some-group", "some-inline-policy"
			}
			got, ok := composeImportIDFromComponents(tc.typeName, parts)
			if !ok {
				t.Fatalf("composeImportIDFromComponents refused %s with both components present", tc.typeName)
			}
			if got != tc.want {
				t.Errorf("%s composed %q, want %q", tc.typeName, got, tc.want)
			}
		})
	}
}

// TestComposeImportIDFromComponents_BlockComponentStillRefuses and its
// Default sibling below prove the fix stayed narrow: only OmitIfAbsent
// widened. A component naming a nested Block or a documented Default
// substitute still refuses composition outright, exactly as before -
// composeImportIDFromComponents has no way to read a nested value or a
// substitute out of a flat Components map, and approximating either would
// be exactly the wrong-marker-shaped guess HANDOFF's safety rule forbids.
// These register a synthetic row rather than searching for a real one,
// mirroring internal/live/identity/targetgroupattachment_omitifabsent_test.go's
// own DefaultTable-swap idiom, restored via t.Cleanup.
func TestComposeImportIDFromComponents_BlockComponentStillRefuses(t *testing.T) {
	const typeName = "test_synthetic_block_component_type"
	identity.DefaultTable[typeName] = identity.TypeIdentity{
		Type: typeName,
		Components: []identity.Component{
			{Attrs: []string{"parent"}, IdentityAttr: "*"},
			{Literal: ":"},
			{Block: "nested", IdentityAttr: "*"},
		},
	}
	t.Cleanup(func() { delete(identity.DefaultTable, typeName) })

	_, ok := composeImportIDFromComponents(typeName, map[string]string{"parent": "p"})
	if ok {
		t.Fatal("composeImportIDFromComponents composed an identity through a Block component - it has no nested value to read from a flat Components map, so this must still refuse")
	}
}

func TestComposeImportIDFromComponents_DefaultComponentStillRefuses(t *testing.T) {
	const typeName = "test_synthetic_default_component_type"
	identity.DefaultTable[typeName] = identity.TypeIdentity{
		Type: typeName,
		Components: []identity.Component{
			{Attrs: []string{"parent"}, IdentityAttr: "*"},
			{Literal: ":", Attrs: []string{"bus"}, Default: "default", IdentityAttr: "*"},
		},
	}
	t.Cleanup(func() { delete(identity.DefaultTable, typeName) })

	_, ok := composeImportIDFromComponents(typeName, map[string]string{"parent": "p"})
	if ok {
		t.Fatal("composeImportIDFromComponents composed an identity through an absent Default component without substituting the documented default - this must still refuse rather than guess")
	}
}

// TestRecordOrphanReadSweep_TargetGroupAttachmentReachesMaterialize is the
// end-to-end proof that a record-located, composite-identity, untaggable
// instance now clears the WHOLE leg - not just the string composer in
// isolation - the same path corpus-alb-complete's day2_remove exercises
// for real: a real local record store, a real kind=identity envelope
// written the way [liveimport]'s seedIdentityFor writes one, and the
// sweep's own filters (known/pending/taggable) all left at their ordinary
// defaults so nothing here is hand-picked to dodge them.
func TestRecordOrphanReadSweep_TargetGroupAttachmentReachesMaterialize(t *testing.T) {
	ctx := context.Background()
	const estate = "test-estate"
	prefix := projection.RecordKeyPrefix(estate)

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, prefix)

	addr := mustAddr(t, "aws_lb_target_group_attachment.other")
	if _, err := projection.SeedLocatedForInstance(ctx, store, addr, addrs.AbsProviderConfig{}, projection.LocatedRecord{
		Components: map[string]string{
			"target_group_arn": tgaARN,
			"target_id":        tgaTID,
			"port":             "80",
		},
	}); err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	req := Request{Estate: estate, HintStore: raw}
	res := &Result{}
	diags := recordOrphanReadSweep(ctx, req, listclient.Schemas{}, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}

	if len(res.Resolutions) != 1 {
		t.Fatalf("got %d resolutions, want exactly 1 (the attachment's record): %#v", len(res.Resolutions), res.Resolutions)
	}
	got := res.Resolutions[0]
	if got.Addr.String() != addr.String() {
		t.Errorf("resolved address = %s, want %s", got.Addr, addr)
	}
	if got.Class != identity.ClassConcrete {
		t.Errorf("Class = %s, want ClassConcrete - this is what feeds builder.materialize's ordinary import-and-read path", got.Class)
	}
	if !got.Undeclared {
		t.Error("Undeclared = false, want true - nothing in configuration declares this instance any more, the whole point of this leg")
	}
	wantImportID := tgaARN + "," + tgaTID + ",80"
	if got.ImportID != wantImportID {
		t.Errorf("ImportID = %q, want %q", got.ImportID, wantImportID)
	}
}

// TestRecordOrphanReadSweep_TargetGroupAttachmentLambdaShapeReachesMaterialize
// is the same proof for the OTHER real shape in the corpus: a lambda-target
// attachment whose record carries no port at all.
func TestRecordOrphanReadSweep_TargetGroupAttachmentLambdaShapeReachesMaterialize(t *testing.T) {
	ctx := context.Background()
	const estate = "test-estate"
	prefix := projection.RecordKeyPrefix(estate)

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, prefix)

	addr := mustAddr(t, `aws_lb_target_group_attachment.lambda["ex-lambda-with-trigger"]`)
	if _, err := projection.SeedLocatedForInstance(ctx, store, addr, addrs.AbsProviderConfig{}, projection.LocatedRecord{
		Components: map[string]string{
			"target_group_arn": "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/lambda-tg/abc123",
			"target_id":        "arn:aws:lambda:us-east-1:123456789012:function:my-function",
		},
	}); err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	req := Request{Estate: estate, HintStore: raw}
	res := &Result{}
	diags := recordOrphanReadSweep(ctx, req, listclient.Schemas{}, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if len(res.Resolutions) != 1 {
		t.Fatalf("got %d resolutions, want exactly 1: %#v", len(res.Resolutions), res.Resolutions)
	}
	want := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/lambda-tg/abc123,arn:aws:lambda:us-east-1:123456789012:function:my-function"
	if got := res.Resolutions[0].ImportID; got != want {
		t.Errorf("ImportID = %q, want %q - before this fix, EVERY aws_lb_target_group_attachment record was silently dropped here, lambda-shaped or not", got, want)
	}
}

// TestRecordOrphanReadSweep_TargetGroupAttachmentAlreadyKnownIsNotDuplicated
// is the sibling boundary: an address the caller already resolved (a
// declared block, a tag-found orphan, another leg's own finding) must not
// be proposed a second time by this leg, whether or not its type is one
// this fix newly reaches.
func TestRecordOrphanReadSweep_TargetGroupAttachmentAlreadyKnownIsNotDuplicated(t *testing.T) {
	ctx := context.Background()
	const estate = "test-estate"
	prefix := projection.RecordKeyPrefix(estate)

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, prefix)

	addr := mustAddr(t, "aws_lb_target_group_attachment.other")
	if _, err := projection.SeedLocatedForInstance(ctx, store, addr, addrs.AbsProviderConfig{}, projection.LocatedRecord{
		Components: map[string]string{"target_group_arn": tgaARN, "target_id": tgaTID, "port": "80"},
	}); err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	req := Request{Estate: estate, HintStore: raw}
	res := &Result{Resolutions: []identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "already-found-elsewhere"}}}
	diags := recordOrphanReadSweep(ctx, req, listclient.Schemas{}, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if len(res.Resolutions) != 1 {
		t.Fatalf("got %d resolutions, want exactly 1 (no duplicate added): %#v", len(res.Resolutions), res.Resolutions)
	}
	if res.Resolutions[0].ImportID != "already-found-elsewhere" {
		t.Errorf("the pre-existing resolution was overwritten: ImportID = %q", res.Resolutions[0].ImportID)
	}
}

// ---------------------------------------------------------------------------
// gauntlet:parent-scoped-sweep's own addition (corpus-mastino-dns's
// day2_remove unit, 2026-08-25): the OTHER real ratified row this same fix
// reaches, aws_route53_record. Found independently, against the real
// on-disk record store a migrate run wrote (kind=identity, Components
// {name, type, zone_id}, no set_identifier for an ordinary apex NS record) -
// see the fix's own doc comment above and recordorphan_read.go's function
// comment for the shared root cause. These tests exercise a DIFFERENT
// Components shape than the target-group-attachment tests above (three
// REQUIRED leading components, one OmitIfAbsent TRAILING one, rather than
// two required plus three trailing optionals) and, for the sweep-level
// pair below, go through the full [Discover] pipeline against a real
// parsed configuration rather than calling [recordOrphanReadSweep]
// directly - independent proof the fix reaches all the way through
// identity resolution and binding, not only the sweep leg in isolation.

// recordOrphanProviderAddr is the provider configuration every fixture
// below resolves to - the same constant shape recordFallbackProviderAddr
// (internal/live/mv) and awsProvider (internal/live/projection) use.
var recordOrphanProviderAddr = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

// TestComposeImportIDFromComponents_Route53Record_OmitIfAbsent is the
// table-test twin of TestComposeImportIDFromComponents_TargetGroupAttachment_*
// above, against aws_route53_record's own ratified row instead
// (Components: zone_id, "_", name, "_", type, "_"+set_identifier
// OmitIfAbsent) - the exact row corpus-mastino-dns's day2_remove found
// composing to nothing for every one of its 59 untaggable record sets.
func TestComposeImportIDFromComponents_Route53Record_OmitIfAbsent(t *testing.T) {
	tests := []struct {
		name       string
		components map[string]string
		wantID     string
		wantOK     bool
	}{
		{
			name: "no set_identifier: the OmitIfAbsent segment and its separator are both dropped",
			components: map[string]string{
				"zone_id": "ZJB88OBW3J7TXGA",
				"name":    "datacite.eu",
				"type":    "NS",
			},
			wantID: "ZJB88OBW3J7TXGA_datacite.eu_NS",
			wantOK: true,
		},
		{
			name: "set_identifier present: the OmitIfAbsent segment is included",
			components: map[string]string{
				"zone_id":        "ZJB88OBW3J7TXGA",
				"name":           "www.datacite.org",
				"type":           "A",
				"set_identifier": "primary",
			},
			wantID: "ZJB88OBW3J7TXGA_www.datacite.org_A_primary",
			wantOK: true,
		},
		{
			name: "a required (non-OmitIfAbsent) component missing entirely refuses",
			components: map[string]string{
				"zone_id": "ZJB88OBW3J7TXGA",
				"type":    "NS",
				// "name" missing: not OmitIfAbsent, so this must refuse
				// rather than compose a string with a hole in it.
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := composeImportIDFromComponents("aws_route53_record", tt.components)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (id=%q)", ok, tt.wantOK, got)
			}
			if ok && got != tt.wantID {
				t.Errorf("id = %q, want %q", got, tt.wantID)
			}
		})
	}
}

// route53RecordOrphanFixture writes a minimal configuration: one taggable,
// admitted aws_route53_zone, with its apex NS aws_route53_record either
// still declared or (the day2_remove shape) removed entirely - the same
// "block deleted, anchor left standing" shape parentReadFixture and
// foldReadFixture use for their own legs, mirrored here for the leg
// neither of those can reach (aws_route53_record has no native list schema
// and is not Cloud-Control-listable, so it cannot go through
// [parentReadSweep] or [foldChildReadSweep] at all - see the header of
// live/e2e/corpus-mastino-dns/run.sh's PART E).
func route53RecordOrphanFixture(t *testing.T, withRecord bool) string {
	t.Helper()
	dir := t.TempDir()
	src := `
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
`
	if withRecord {
		src += `
resource "aws_route53_record" "eu-ns" {
  zone_id         = aws_route53_zone.eu.zone_id
  name            = "datacite.eu"
  type            = "NS"
  ttl             = 172800
  records         = ["ns-1.example.com"]
  allow_overwrite = true
}
`
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// recordOrphanHintStore opens a real local record store - the same
// staterecord.Store shape a live block's record_store "local" backend
// opens - and returns both the raw store ([Request.HintStore]'s own type)
// and a [*projection.RecordStore] wrapping it at this estate's own key
// prefix, for seeding.
func recordOrphanHintStore(t *testing.T) (staterecord.Store, *projection.RecordStore) {
	t.Helper()
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return raw, projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estateName))
}

// TestRecordOrphanReadSweep_UndeclaredRoute53RecordProposesDestroy is the
// full-pipeline headline: aws_route53_record.eu-ns's block is gone, but
// migrate (or an earlier apply) already wrote its identity into the
// estate's own record store - the SAME kind=identity write
// [projection.SeedLocatedForInstance] performs, with the provider's real
// composite identity shape (zone_id, name, type; no set_identifier,
// matching an ordinary apex NS record). Discover must find it, compose its
// import ID correctly, and feed it into res.Resolutions as the same
// Undeclared, ClassConcrete shape classifyOrphans already produces for a
// tag-found orphan - what [builder.run]'s existing materialize/import-and-
// read/destroy path consumes, so a plan actually proposes the destroy
// rather than merely reporting one.
func TestRecordOrphanReadSweep_UndeclaredRoute53RecordProposesDestroy(t *testing.T) {
	cfg := loadConfig(t, route53RecordOrphanFixture(t, false))
	resolutions := resolveOrFail(t, cfg).All()

	rawStore, seedStore := recordOrphanHintStore(t)
	recordAddr := mustAddr(t, "aws_route53_record.eu-ns")
	if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, recordAddr, recordOrphanProviderAddr, projection.LocatedRecord{
		Components: map[string]string{
			"zone_id": "ZJB88OBW3J7TXGA",
			"name":    "datacite.eu",
			"type":    "NS",
		},
	}); err != nil {
		t.Fatalf("seeding the record fixture: %s", err)
	}

	cloud := newFakeCloud()
	cloud.own("aws_route53_zone", "ZJB88OBW3J7TXGA", "aws_route53_zone.eu")

	res, diags := Discover(t.Context(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    cloud,
		Sweep:       true,
		HintStore:   rawStore,
	})
	assertNoErrors(t, diags)

	var found bool
	for _, r := range res.Resolutions {
		if r.Addr.String() != recordAddr.String() {
			continue
		}
		found = true
		if !r.Undeclared {
			t.Errorf("resolution for %s is not marked Undeclared: %+v", recordAddr, r)
		}
		if r.Class != identity.ClassConcrete {
			t.Errorf("resolution for %s has class %v, want ClassConcrete", recordAddr, r.Class)
		}
		const wantID = "ZJB88OBW3J7TXGA_datacite.eu_NS"
		if r.ImportID != wantID {
			t.Errorf("resolution for %s has ImportID %q, want %q", recordAddr, r.ImportID, wantID)
		}
	}
	if !found {
		t.Fatalf("no resolution produced for the orphaned %s; the record-orphan-read leg did not find it:\n%s", recordAddr, res)
	}
}

// TestRecordOrphanReadSweep_DeclaredRoute53RecordIsLeftAlone is the
// positive control, through the same full [Discover] pipeline: the SAME
// record store entry as above, but the block is still declared. The leg
// must never add a second, Undeclared resolution for an address the
// configuration itself still owns - that would be a duplicate claim on one
// live object, the same "materializes last and supersedes" hazard issue
// #404 already guards elsewhere in this package.
func TestRecordOrphanReadSweep_DeclaredRoute53RecordIsLeftAlone(t *testing.T) {
	cfg := loadConfig(t, route53RecordOrphanFixture(t, true))
	resolutions := resolveOrFail(t, cfg).All()

	rawStore, seedStore := recordOrphanHintStore(t)
	recordAddr := mustAddr(t, "aws_route53_record.eu-ns")
	if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, recordAddr, recordOrphanProviderAddr, projection.LocatedRecord{
		Components: map[string]string{
			"zone_id": "ZJB88OBW3J7TXGA",
			"name":    "datacite.eu",
			"type":    "NS",
		},
	}); err != nil {
		t.Fatalf("seeding the record fixture: %s", err)
	}

	cloud := newFakeCloud()
	cloud.own("aws_route53_zone", "ZJB88OBW3J7TXGA", "aws_route53_zone.eu")

	res, diags := Discover(t.Context(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    cloud,
		Sweep:       true,
		HintStore:   rawStore,
	})
	assertNoErrors(t, diags)

	var undeclaredCount int
	for _, r := range res.Resolutions {
		if r.Addr.String() == recordAddr.String() && r.Undeclared {
			undeclaredCount++
		}
	}
	if undeclaredCount != 0 {
		t.Errorf("%s is still declared but got %d Undeclared resolution(s) from the record-orphan-read leg; a still-declared block must never be treated as an orphan of itself", recordAddr, undeclaredCount)
	}
}
