// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1084's unit half. The live pin
// (internal/live/lifecycle's TestTagOnCreateHostedZone) proves the whole
// crossing against the emulator; these are the two things a unit test can
// pin more precisely than a crossing can: that the control flow keys on the
// registry flag and nothing else, and the exact text of the failure.

// tocRoster is a two-row roster: one Terraform type whose CloudFormation
// counterpart cannot take tags at create, one that can. The types are
// synthetic on purpose - nothing here may know a real type name.
func tocRoster(t *testing.T) *registry.Roster {
	t.Helper()
	mapping := []byte(`{"rows":[
		{"tf_type":"aws_after_thing","cfn_type":"AWS::After::Thing","via":"name"},
		{"tf_type":"aws_ordinary_thing","cfn_type":"AWS::Ordinary::Thing","via":"name"}
	]}`)
	reg := []byte(`{"types":[
		{"type_name":"AWS::After::Thing","primary_identifier":["Id"],"tagging":{"declared":true,"taggable":true,"tag_on_create":false,"tag_updatable":true},"handlers":{"list":true}},
		{"type_name":"AWS::Ordinary::Thing","primary_identifier":["Id"],"tagging":{"declared":true,"taggable":true,"tag_on_create":true,"tag_updatable":true},"handlers":{"list":true}}
	]}`)
	r, err := registry.Parse(mapping, reg)
	if err != nil {
		t.Fatalf("registry.Parse: %v", err)
	}
	return r
}

func tocSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"arn":      {Type: cty.String, Computed: true},
			"name":     {Type: cty.String, Required: true},
			"tags":     {Type: cty.Map(cty.String), Optional: true},
			"tags_all": {Type: cty.Map(cty.String), Computed: true},
		},
	}}
}

func tocConfig(tags cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.NullVal(cty.String),
		"arn":      cty.NullVal(cty.String),
		"name":     cty.StringVal("thing"),
		"tags":     tags,
		"tags_all": cty.NullVal(cty.Map(cty.String)),
	})
}

func tocApplied(arn, id string, tags map[string]string) cty.Value {
	var arnVal, idVal cty.Value = cty.NullVal(cty.String), cty.NullVal(cty.String)
	if arn != "" {
		arnVal = cty.StringVal(arn)
	}
	if id != "" {
		idVal = cty.StringVal(id)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":       idVal,
		"arn":      arnVal,
		"name":     cty.StringVal("thing"),
		"tags":     tagMap(tags),
		"tags_all": tagMap(tags),
	})
}

func tocProvider() addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
}

// fakeTagger records the one write and answers with whatever it was told.
type fakeTagger struct {
	calls []fakeTagCall
	err   error
}

type fakeTagCall struct {
	arns []string
	tags map[string]string
}

func (f *fakeTagger) TagResources(_ context.Context, arns []string, tags map[string]string) error {
	f.calls = append(f.calls, fakeTagCall{arns: arns, tags: tags})
	return f.err
}

func tocResolver(t *testing.T, tagger *fakeTagger) *NodeResolver {
	t.Helper()
	n := &NodeResolver{Estate: "prod", Roster: tocRoster(t)}
	if tagger != nil {
		n.Tagger = func(addrs.AbsProviderConfig) MarkerTagger { return tagger }
	}
	return n
}

// TestAdjustCreateConfigValue_withholdsMarkersOnlyWhereTheFlagSaysSo is the
// control-flow pin: the same resolver, the same schema, the same null tags,
// two types that differ only in their registry row. The create of the
// tag-on-create=false type comes back unstamped; the create of the
// ordinary type, and an UPDATE of the tag-on-create=false type, carry both
// markers exactly as before #1084.
func TestAdjustCreateConfigValue_withholdsMarkersOnlyWhereTheFlagSaysSo(t *testing.T) {
	n := tocResolver(t, nil)
	ctx := context.Background()

	after := locatedTestAddr(t, "aws_after_thing", "x")
	ordinary := locatedTestAddr(t, "aws_ordinary_thing", "x")
	nullTags := cty.NullVal(cty.Map(cty.String))

	got, diags := n.AdjustCreateConfigValue(ctx, after, tocConfig(nullTags), tocSchema())
	if diags.HasErrors() {
		t.Fatalf("create of the after-create type: %s", diags.Err())
	}
	if !got.GetAttr("tags").IsNull() {
		t.Errorf("create of the after-create type was stamped: tags = %#v", got.GetAttr("tags"))
	}

	got, diags = n.AdjustCreateConfigValue(ctx, ordinary, tocConfig(nullTags), tocSchema())
	if diags.HasErrors() {
		t.Fatalf("create of the ordinary type: %s", diags.Err())
	}
	if tags := requireTags(t, got); tags[markers.TagEstate] != "prod" || tags[markers.TagAddress] != "aws_ordinary_thing.x" {
		t.Errorf("create of the ordinary type is not stamped as before: %v", tags)
	}

	got, diags = n.AdjustConfigValue(ctx, after, tocConfig(nullTags), tocSchema())
	if diags.HasErrors() {
		t.Fatalf("update of the after-create type: %s", diags.Err())
	}
	if tags := requireTags(t, got); tags[markers.TagEstate] != "prod" || tags[markers.TagAddress] != "aws_after_thing.x" {
		t.Errorf("update of the after-create type is not stamped as before: %v", tags)
	}

	// A resolver with no roster takes the ordinary path for everything.
	noRoster := &NodeResolver{Estate: "prod"}
	got, _ = noRoster.AdjustCreateConfigValue(ctx, after, tocConfig(nullTags), tocSchema())
	if tags := requireTags(t, got); tags[markers.TagEstate] != "prod" {
		t.Errorf("with no roster the create was withheld: %v", tags)
	}
}

// TestAdjustCreateConfigValue_conflictStillRefused: withholding is not a
// way past the marker conflict check. A hand-written tofu-estate naming
// another estate on a tag-on-create=false type is refused on the create
// exactly as it is everywhere else.
func TestAdjustCreateConfigValue_conflictStillRefused(t *testing.T) {
	n := tocResolver(t, nil)
	after := locatedTestAddr(t, "aws_after_thing", "x")
	tags := tagMap(map[string]string{markers.TagEstate: "someone-else"})

	_, diags := n.AdjustCreateConfigValue(context.Background(), after, tocConfig(tags), tocSchema())
	if !diags.HasErrors() {
		t.Fatalf("a conflicting hand-written marker was not refused on the create path")
	}
	if diags[0].Description().Summary != SummaryMarkerConflict {
		t.Errorf("summary = %q, want %q", diags[0].Description().Summary, SummaryMarkerConflict)
	}
}

// TestWriteAppliedMarkers_writesTheWithheldMarkers: after a successful
// create of the tag-on-create=false type, exactly one TagResources call is
// made, to the applied object's ARN, with the markers the create call was
// not given - and nothing is written for the ordinary type, for an update,
// or for a run with no estate.
func TestWriteAppliedMarkers_writesTheWithheldMarkers(t *testing.T) {
	tagger := &fakeTagger{}
	n := tocResolver(t, tagger)
	n.Slots = map[string]string{"aws_after_thing.x": "3"}
	ctx := context.Background()
	applied := tocApplied("arn:aws:after:::thing/T1", "T1", nil)

	after := locatedTestAddr(t, "aws_after_thing", "x")
	if diags := n.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, applied, tocSchema()); diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if len(tagger.calls) != 1 {
		t.Fatalf("want exactly 1 TagResources call, got %d", len(tagger.calls))
	}
	call := tagger.calls[0]
	if len(call.arns) != 1 || call.arns[0] != "arn:aws:after:::thing/T1" {
		t.Errorf("ARN list = %v", call.arns)
	}
	want := map[string]string{markers.TagEstate: "prod", markers.TagAddress: "aws_after_thing.x", markers.TagSlot: "3"}
	if len(call.tags) != len(want) {
		t.Errorf("tags = %v, want %v", call.tags, want)
	}
	for k, v := range want {
		if call.tags[k] != v {
			t.Errorf("tags[%q] = %q, want %q", k, call.tags[k], v)
		}
	}

	tagger.calls = nil
	ordinary := locatedTestAddr(t, "aws_ordinary_thing", "x")
	_ = n.WriteAppliedMarkers(ctx, ordinary, tocProvider(), plans.Create, applied, tocSchema())
	_ = n.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Update, applied, tocSchema())
	noEstate := &NodeResolver{Roster: tocRoster(t), Tagger: n.Tagger}
	_ = noEstate.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, applied, tocSchema())
	if len(tagger.calls) != 0 {
		t.Errorf("a write was made where none was due: %v", tagger.calls)
	}
}

// TestWriteAppliedMarkers_refusedWriteIsAnErrorNamingTheObjectAndTheCommand
// is the failure path: the tagger refuses, and the apply error names the
// unmarked object by ARN and id, names the estate it does not carry, and
// prints the one command that marks it - the same operation this run
// attempted, filled in.
func TestWriteAppliedMarkers_refusedWriteIsAnErrorNamingTheObjectAndTheCommand(t *testing.T) {
	tagger := &fakeTagger{err: errors.New("AccessDeniedException (HTTP 403): refused by test")}
	n := tocResolver(t, tagger)
	after := locatedTestAddr(t, "aws_after_thing", "x")
	applied := tocApplied("arn:aws:after:::thing/T1", "T1", nil)

	diags := n.WriteAppliedMarkers(context.Background(), after, tocProvider(), plans.Create, applied, tocSchema())
	if !diags.HasErrors() {
		t.Fatalf("a refused tag write did not fail the apply")
	}
	if len(diags) != 1 {
		t.Fatalf("want one diagnostic, got %d", len(diags))
	}
	d := diags[0].Description()
	if diags[0].Severity() != tfdiags.Error {
		t.Errorf("severity = %v, want Error", diags[0].Severity())
	}
	if d.Summary != SummaryMarkerNotWritten {
		t.Errorf("summary = %q, want %q", d.Summary, SummaryMarkerNotWritten)
	}
	for _, want := range []string{
		"aws_after_thing.x was created as arn:aws:after:::thing/T1 [id=T1] and could not be marked: AccessDeniedException (HTTP 403): refused by test.",
		"AWS::After::Thing does not take tags in its create call (live/registry.json: tag_on_create false)",
		`carries no marker naming estate "prod"`,
		"aws resourcegroupstaggingapi tag-resources --resource-arn-list arn:aws:after:::thing/T1 --tags 'tofu-address=aws_after_thing.x,tofu-estate=prod'",
	} {
		if !strings.Contains(d.Detail, want) {
			t.Errorf("detail does not carry %q:\n%s", want, d.Detail)
		}
	}
}

// TestWriteAppliedMarkers_noClientAndNoARNAreFailuresToo: a run with no
// tagging client, and an applied object with no arn to address, are each a
// failed write - reported, never silent - and the no-ARN case says what
// the operator has to do without pretending to know the command.
func TestWriteAppliedMarkers_noClientAndNoARNAreFailuresToo(t *testing.T) {
	after := locatedTestAddr(t, "aws_after_thing", "x")
	ctx := context.Background()

	noClient := tocResolver(t, nil)
	diags := noClient.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, tocApplied("arn:aws:after:::thing/T1", "T1", nil), tocSchema())
	if !diags.HasErrors() || !strings.Contains(diags[0].Description().Detail, "this run has no tagging client") {
		t.Errorf("no client: want a failed write naming the missing client, got %v", diags.Err())
	}

	tagger := &fakeTagger{}
	noARN := tocResolver(t, tagger)
	diags = noARN.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, tocApplied("", "T1", nil), tocSchema())
	if !diags.HasErrors() {
		t.Fatalf("no arn: the write was reported as done")
	}
	detail := diags[0].Description().Detail
	if !strings.Contains(detail, "[id=T1]") || !strings.Contains(detail, "no arn attribute") {
		t.Errorf("no arn: detail does not name the id and the missing arn:\n%s", detail)
	}
	if strings.Contains(detail, "--resource-arn-list") {
		t.Errorf("no arn: a tag-resources command was printed with no ARN to put in it:\n%s", detail)
	}
	if len(tagger.calls) != 0 {
		t.Errorf("no arn: a write was attempted anyway: %v", tagger.calls)
	}
}

// TestMarkerTagsArgument_keyedInstanceSurvivesAShell: the printed --tags
// value is single-quoted, so an escaped for_each address's brackets and
// quotes reach the CLI intact.
func TestMarkerTagsArgument_keyedInstanceSurvivesAShell(t *testing.T) {
	n := tocResolver(t, nil)
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_after_thing", Name: "x"}.
		Instance(addrs.StringKey("a")).Absolute(addrs.RootModuleInstance)
	got := markerTagsArgument(n.withheldMarkers(addr))
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("not single-quoted: %s", got)
	}
	if strings.Contains(strings.Trim(got, "'"), "'") {
		t.Errorf("a single quote inside the quoted value would end it: %s", got)
	}
	if !strings.Contains(got, "tofu-address="+markers.EscapeAddress(addr.String())) {
		t.Errorf("the escaped address is not in the argument: %s", got)
	}
}
