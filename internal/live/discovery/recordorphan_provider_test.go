// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1721, found fixing #1715: in a root with two configurations
// of one provider (aws and aws.west), both scoped passes serve every aws
// type, so #1715's schema check lets both propose the same record-orphan
// removal. Merge kept both and attributed the address to whichever pass
// sorted last, so a record written through the default configuration was
// read back and destroyed through the alias.
//
// The record names the configuration that managed the object at its last
// write (recordEnvelope.Provider, #389). A scoped pass proposes the record
// only when that is its own configuration; a record that names none falls
// back to the schema check, and Merge keeps one removal of it.

var (
	awsDefaultProv = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	awsWestProv    = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws"), Alias: "west"}
)

// targetGroupAttachmentSchemas is what an aws pass's provider reads for an
// untaggable, record-located type: aws_lb_target_group_attachment has no
// tags argument, so no tag sweep can find it and the record leg is the only
// route to its removal.
func targetGroupAttachmentSchemas(t *testing.T) listclient.Schemas {
	t.Helper()
	schemas, diags := listclient.ListSchemas(context.Background(), schemaOnlyProvider{types: map[string]providers.Schema{
		"aws_lb_target_group_attachment": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":               {Type: cty.String, Computed: true},
			"target_group_arn": {Type: cty.String, Required: true},
			"target_id":        {Type: cty.String, Required: true},
			"port":             {Type: cty.Number, Optional: true},
		}}},
	}})
	if diags.HasErrors() {
		t.Fatalf("aws schemas: %s", diags.Err())
	}
	return schemas
}

// twoAWSPassMergeDiags seeds one record for a removed attachment block, written
// through recordedBy (the zero value writes no Provider, the shape of every
// envelope older than #389), runs the record leg once per aws configuration
// and merges the two passes the way live-plan does.
func twoAWSPassMergeDiags(t *testing.T, recordedBy addrs.AbsProviderConfig) (*Result, map[string]addrs.AbsProviderConfig, tfdiags.Diagnostics) {
	t.Helper()
	ctx := context.Background()
	const estate = "alb"

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
	addr := mustAddr(t, "aws_lb_target_group_attachment.other")
	if _, err := projection.SeedLocatedForInstance(ctx, store, addr, recordedBy, projection.LocatedRecord{
		Components: map[string]string{"target_group_arn": tgaARN, "target_id": tgaTID, "port": "80"},
	}); err != nil {
		t.Fatalf("seeding the record: %s", err)
	}

	schemas := targetGroupAttachmentSchemas(t)
	var passes []Pass
	for _, prov := range []addrs.AbsProviderConfig{awsDefaultProv, awsWestProv} {
		res := &Result{Estate: estate}
		req := Request{Estate: estate, HintStore: raw, ScopeProvider: prov, VouchProvider: prov, Sweep: true}
		if d := recordOrphanReadSweep(ctx, req, schemas, res); d.HasErrors() {
			t.Fatalf("record leg through %s: %s", prov, d.Err())
		}
		passes = append(passes, Pass{Provider: prov, Result: res})
	}
	return Merge(estate, passes, false)
}

func twoAWSPassMerge(t *testing.T, recordedBy addrs.AbsProviderConfig) (*Result, map[string]addrs.AbsProviderConfig) {
	t.Helper()
	merged, providerOf, mdiags := twoAWSPassMergeDiags(t, recordedBy)
	if mdiags.HasErrors() {
		t.Fatalf("Merge: %s", mdiags.Err())
	}
	return merged, providerOf
}

// assertOneRemovalThrough checks the merge proposes exactly one removal of
// the attachment and reads it through want.
func assertOneRemovalThrough(t *testing.T, merged *Result, providerOf map[string]addrs.AbsProviderConfig, want addrs.AbsProviderConfig) {
	t.Helper()
	var removals []string
	for _, r := range merged.Resolutions {
		if !r.Undeclared {
			continue
		}
		removals = append(removals, r.Addr.String()+" via "+providerOf[r.Addr.String()].String())
		t.Logf("%s undeclared=%v via %s", r.Addr, r.Undeclared, providerOf[r.Addr.String()])
	}
	if len(removals) != 1 {
		t.Fatalf("the merge proposes %d removals of the one recorded attachment (%v), want exactly 1", len(removals), removals)
	}
	if got := providerOf["aws_lb_target_group_attachment.other"]; got.String() != want.String() {
		t.Errorf("the removal is read through %s, want %s", got, want)
	}
}

// TestRecordOrphanTwoConfigsOfOneProviderProposesOnce is #1721's red: the
// record names the default configuration, and both aws passes serve the
// type. Exactly one removal must survive the merge, read through the
// default configuration.
func TestRecordOrphanTwoConfigsOfOneProviderProposesOnce(t *testing.T) {
	merged, providerOf := twoAWSPassMerge(t, awsDefaultProv)
	assertOneRemovalThrough(t, merged, providerOf, awsDefaultProv)
}

// TestRecordOrphanNamingTheAliasIsTheAliasPasss is the control on the
// other configuration: a record written through aws.west is proposed by
// the alias pass alone, so the removal is read through the alias.
func TestRecordOrphanNamingTheAliasIsTheAliasPasss(t *testing.T) {
	merged, providerOf := twoAWSPassMerge(t, awsWestProv)
	assertOneRemovalThrough(t, merged, providerOf, awsWestProv)
}

// TestRecordOrphanWithNoRecordedProviderProposesOnce is the fallback: an
// envelope older than #389 names no configuration, both passes still pass
// the schema check, and Merge keeps one removal, read through the default
// configuration since nothing says otherwise.
func TestRecordOrphanWithNoRecordedProviderProposesOnce(t *testing.T) {
	merged, providerOf := twoAWSPassMerge(t, addrs.AbsProviderConfig{})
	assertOneRemovalThrough(t, merged, providerOf, awsDefaultProv)
}

// TestRecordOrphanNamingNoRunningConfigIsProposedByNoPass pins the case the
// issue left for decision: the record names aws.east, an alias since
// removed, so no pass is the one it names. No pass proposes it, because
// reading its identity through aws or aws.west could reach a different
// object of the same name in another region or account. The plan refuses
// instead, once for the record rather than once per pass, so the missed
// removal is never silent.
func TestRecordOrphanNamingNoRunningConfigIsProposedByNoPass(t *testing.T) {
	east := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws"), Alias: "east"}
	merged, _, diags := twoAWSPassMergeDiags(t, east)
	for _, r := range merged.Resolutions {
		if r.Undeclared {
			t.Errorf("%s is proposed for removal although the record names %s, which no pass reads through", r.Addr, east)
		}
	}
	var refusals []string
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == problemSummaries[ProblemRecordedProviderAbsent] {
			refusals = append(refusals, d.Description().Detail)
			t.Logf("%s: %s", d.Description().Summary, d.Description().Detail)
		}
	}
	if len(refusals) != 1 {
		t.Fatalf("the merge raised %d refusals naming the absent configuration, want exactly 1 (one record, two passes): %v", len(refusals), diags.Err())
	}
	for _, want := range []string{"aws_lb_target_group_attachment.other", east.String()} {
		if !strings.Contains(refusals[0], want) {
			t.Errorf("the refusal does not name %q:\n%s", want, refusals[0])
		}
	}
	var problems int
	for _, p := range merged.Problems {
		if p.Kind == ProblemRecordedProviderAbsent {
			problems++
		}
	}
	if problems != 1 {
		t.Errorf("the merged result records %d %s problems, want 1", problems, ProblemRecordedProviderAbsent)
	}
}

// TestRecordOrphanSinglePassIgnoresTheRecordedProvider is the single-pass
// control: an unscoped pass (one provider configuration, the path every
// caller took before #69) proposes the record whatever configuration it
// names, as it did before #1721.
func TestRecordOrphanSinglePassIgnoresTheRecordedProvider(t *testing.T) {
	ctx := context.Background()
	const estate = "alb"
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
	addr := mustAddr(t, "aws_lb_target_group_attachment.other")
	if _, err := projection.SeedLocatedForInstance(ctx, store, addr, awsWestProv, projection.LocatedRecord{
		Components: map[string]string{"target_group_arn": tgaARN, "target_id": tgaTID, "port": "80"},
	}); err != nil {
		t.Fatalf("seeding the record: %s", err)
	}
	res := &Result{Estate: estate}
	req := Request{Estate: estate, HintStore: raw, VouchProvider: awsDefaultProv, Sweep: true}
	if d := recordOrphanReadSweep(ctx, req, targetGroupAttachmentSchemas(t), res); d.HasErrors() {
		t.Fatalf("record leg: %s", d.Err())
	}
	merged, providerOf, mdiags := Merge(estate, []Pass{{Provider: awsDefaultProv, Result: res}}, false)
	if mdiags.HasErrors() {
		t.Fatalf("Merge: %s", mdiags.Err())
	}
	assertOneRemovalThrough(t, merged, providerOf, awsDefaultProv)
}
