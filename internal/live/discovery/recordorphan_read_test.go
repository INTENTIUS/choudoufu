// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
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

// The tests below are gauntlet:sweep-moved-alias's own unit (619ea617ac's
// merge message, #405/#410): before this fix, recordOrphanReadSweep treated
// a record found at an address no config block declares as an
// orphan-destroy candidate WITHOUT consulting moved.Aliases/moved.Honoured,
// so a `moved` block relocating an untaggable record-backed instance left
// its record - still keyed at the OLD address until write-back - reading as
// genuinely undeclared, and the plan destroyed the live object under its
// old address even though the configuration still declared it. Eight
// estates regressed day2_rename on exactly this shape.
//
// aws_iam_role_policy is one of the three types this file's own package
// doc comment names as this leg's whole population; its ratified row
// (identity.LookupType) composes "ROLE:NAME" - see composeImportIDFromComponents.

// TestRecordOrphanReadSweep_MovedAliasIsNotAnOrphan is the positive case:
// a record under the OLD address, a `moved` block saying the old address
// is now the new one, and the new address genuinely declared (already
// present in res.Resolutions, the same "already accounted for" state
// bind/the caller would have left it in). The old-address record must NOT
// become a second, Undeclared destroy resolution.
func TestRecordOrphanReadSweep_MovedAliasIsNotAnOrphan(t *testing.T) {
	ctx := context.Background()
	const estate = "test-estate"
	prefix := projection.RecordKeyPrefix(estate)

	cfg := loadConfig(t, "testdata/moved-record-located")
	oldAddr := mustAddr(t, "aws_iam_role_policy.inline_old")
	newAddr := mustAddr(t, "aws_iam_role_policy.inline")

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, prefix)
	if _, err := projection.SeedLocatedForInstance(ctx, store, oldAddr, addrs.AbsProviderConfig{}, projection.LocatedRecord{
		Components: map[string]string{"role": "app", "name": "deploy"},
	}); err != nil {
		t.Fatalf("seeding the located record under the OLD address: %s", err)
	}

	req := Request{Estate: estate, HintStore: raw, Config: cfg}
	// Simulates the state res.Resolutions is already in by the time this
	// leg runs (discovery.go's own comment above [known]'s construction):
	// the NEW address is a declared block the caller's initial resolution
	// list already carries, Undeclared left at its zero value (false).
	res := &Result{Resolutions: []identity.Resolution{{Addr: newAddr, Class: identity.ClassRecordLocated}}}

	diags := recordOrphanReadSweep(ctx, req, listclient.Schemas{}, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if len(res.Resolutions) != 1 {
		t.Fatalf("got %d resolutions, want exactly 1 (no phantom destroy for the OLD address): %#v", len(res.Resolutions), res.Resolutions)
	}
	if res.Resolutions[0].Addr.String() != newAddr.String() {
		t.Errorf("the only resolution is %s, want it to remain %s unchanged", res.Resolutions[0].Addr, newAddr)
	}
	if res.Resolutions[0].Undeclared {
		t.Errorf("the declared instance's own resolution was marked Undeclared - the moved-alias fix must never touch an entry it did not add")
	}
}

// TestRecordOrphanReadSweep_NoMovedBlockStillOrphans is the mutation check:
// the IDENTICAL fixture (same record, same components, same new-address
// declaration), with the `moved` block removed. Nothing joins the old
// address to the new one any more, so the record at the old address is a
// genuine orphan and this leg's destroy proposal must stand - proving the
// positive test above is actually exercising the moved-alias consult, not
// some unrelated reason the destroy never fired.
func TestRecordOrphanReadSweep_NoMovedBlockStillOrphans(t *testing.T) {
	ctx := context.Background()
	const estate = "test-estate"
	prefix := projection.RecordKeyPrefix(estate)

	cfg := loadConfig(t, "testdata/moved-record-located-nomoved")
	oldAddr := mustAddr(t, "aws_iam_role_policy.inline_old")
	newAddr := mustAddr(t, "aws_iam_role_policy.inline")

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, prefix)
	if _, err := projection.SeedLocatedForInstance(ctx, store, oldAddr, addrs.AbsProviderConfig{}, projection.LocatedRecord{
		Components: map[string]string{"role": "app", "name": "deploy"},
	}); err != nil {
		t.Fatalf("seeding the located record under the OLD address: %s", err)
	}

	req := Request{Estate: estate, HintStore: raw, Config: cfg}
	res := &Result{Resolutions: []identity.Resolution{{Addr: newAddr, Class: identity.ClassRecordLocated}}}

	diags := recordOrphanReadSweep(ctx, req, listclient.Schemas{}, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if len(res.Resolutions) != 2 {
		t.Fatalf("got %d resolutions, want exactly 2 (the declared new-address entry plus a genuine orphan destroy for the old one): %#v", len(res.Resolutions), res.Resolutions)
	}
	var orphan *identity.Resolution
	for i := range res.Resolutions {
		if res.Resolutions[i].Addr.String() == oldAddr.String() {
			orphan = &res.Resolutions[i]
		}
	}
	if orphan == nil {
		t.Fatalf("no resolution for the old address at all; want a genuine-orphan destroy candidate: %#v", res.Resolutions)
	}
	if !orphan.Undeclared {
		t.Errorf("the old-address orphan is not marked Undeclared")
	}
	const want = "app:deploy"
	if orphan.ImportID != want {
		t.Errorf("orphan ImportID = %q, want %q", orphan.ImportID, want)
	}
}

// TestRecordOrphanReadSweep_MovedAliasDoesNotWidenToAnUnrelatedAddress is
// the safety-rule boundary named in the unit's genuine-orphan check
// (HANDOFF's "never write a wrong marker"): an old address that is NOT one
// of the currently declared instance's moved-aliases must still orphan,
// even though a moved block exists in the same configuration for a
// completely different pair of addresses. The alias index must not widen
// past what moved.Aliases itself returns for each declared entry.
func TestRecordOrphanReadSweep_MovedAliasDoesNotWidenToAnUnrelatedAddress(t *testing.T) {
	ctx := context.Background()
	const estate = "test-estate"
	prefix := projection.RecordKeyPrefix(estate)

	cfg := loadConfig(t, "testdata/moved-record-located")
	newAddr := mustAddr(t, "aws_iam_role_policy.inline")
	unrelated := mustAddr(t, "aws_iam_role_policy.some_other_policy_entirely")

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, prefix)
	if _, err := projection.SeedLocatedForInstance(ctx, store, unrelated, addrs.AbsProviderConfig{}, projection.LocatedRecord{
		Components: map[string]string{"role": "somebody-elses-role", "name": "somebody-elses-policy"},
	}); err != nil {
		t.Fatalf("seeding the unrelated record: %s", err)
	}

	req := Request{Estate: estate, HintStore: raw, Config: cfg}
	res := &Result{Resolutions: []identity.Resolution{{Addr: newAddr, Class: identity.ClassRecordLocated}}}

	diags := recordOrphanReadSweep(ctx, req, listclient.Schemas{}, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if len(res.Resolutions) != 2 {
		t.Fatalf("got %d resolutions, want exactly 2 (the declared entry plus the unrelated address's own genuine orphan): %#v", len(res.Resolutions), res.Resolutions)
	}
	var found bool
	for _, r := range res.Resolutions {
		if r.Addr.String() == unrelated.String() {
			found = true
			if !r.Undeclared {
				t.Errorf("the unrelated address's resolution is not marked Undeclared")
			}
		}
	}
	if !found {
		t.Errorf("the unrelated record was not proposed as an orphan; the moved-alias index must not have quietly widened to cover it: %#v", res.Resolutions)
	}
}
