// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/live/substrate"
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
	// writes is every post-create write the resolver asked a client for.
	writes []substrate.Write
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
		n.MarkerWriter = func(_ addrs.AbsProviderConfig, write substrate.Write) (MarkerWriter, error) {
			tagger.writes = append(tagger.writes, write)
			return TaggingAPIWriter{Tagger: tagger}, nil
		}
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
	stored, diags := n.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, applied, tocSchema())
	if diags.HasErrors() {
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

	// #1316: the object core stores is the object as it now stands. The
	// provider returned it from a create with no marker; the state and the
	// cache a -refresh=false plan reads must carry what the write stored,
	// in both maps, or that plan proposes the markers all over again.
	for _, attr := range []string{"tags", "tags_all"} {
		got := stored.GetAttr(attr)
		if got.IsNull() || !got.IsKnown() {
			t.Fatalf("stored %s is %#v, want the written markers", attr, got)
		}
		gm := got.AsValueMap()
		for k, v := range want {
			if e, ok := gm[k]; !ok || e.AsString() != v {
				t.Errorf("stored %s[%q] = %#v, want %q: the state would contradict the object it describes", attr, k, e, v)
			}
		}
	}
	if stored.GetAttr("arn").AsString() != "arn:aws:after:::thing/T1" {
		t.Errorf("the rest of the object changed: arn = %#v", stored.GetAttr("arn"))
	}
	if !stored.Type().Equals(tocSchema().Block.ImpliedType()) {
		t.Errorf("the stored object no longer conforms to the schema: %#v", stored.Type())
	}

	tagger.calls = nil
	ordinary := locatedTestAddr(t, "aws_ordinary_thing", "x")
	_, _ = n.WriteAppliedMarkers(ctx, ordinary, tocProvider(), plans.Create, applied, tocSchema())
	_, _ = n.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Update, applied, tocSchema())
	noEstate := &NodeResolver{Roster: tocRoster(t), MarkerWriter: n.MarkerWriter}
	_, _ = noEstate.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, applied, tocSchema())
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

	_, diags := n.WriteAppliedMarkers(context.Background(), after, tocProvider(), plans.Create, applied, tocSchema())
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
	_, diags := noClient.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, tocApplied("arn:aws:after:::thing/T1", "T1", nil), tocSchema())
	if !diags.HasErrors() || !strings.Contains(diags[0].Description().Detail, "this run has no tagging client") {
		t.Errorf("no client: want a failed write naming the missing client, got %v", diags.Err())
	}

	tagger := &fakeTagger{}
	noARN := tocResolver(t, tagger)
	_, diags = noARN.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, tocApplied("", "T1", nil), tocSchema())
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
// quotes reach the CLI intact. GitHub issue #1653 moved the renderer to
// markers.TagsArgument; this pin moved with the call site.
func TestMarkerTagsArgument_keyedInstanceSurvivesAShell(t *testing.T) {
	n := tocResolver(t, nil)
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_after_thing", Name: "x"}.
		Instance(addrs.StringKey("a")).Absolute(addrs.RootModuleInstance)
	got := markers.TagsArgument(n.withheldMarkers(addr))
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

// TestWithWrittenMarkers_keepsExistingTagsAndNeverTouchesAMark: the merge
// adds the written markers to whatever tags the provider returned, and a
// tag map carrying a mark (a sensitive value) is left exactly as it was,
// since nothing here may read inside it.
func TestWithWrittenMarkers_keepsExistingTagsAndNeverTouchesAMark(t *testing.T) {
	written := map[string]string{markers.TagEstate: "prod"}

	obj := tocApplied("arn:aws:after:::thing/T1", "T1", map[string]string{"team": "platform"})
	got := withWrittenMarkers(obj, written)
	tags := got.GetAttr("tags").AsValueMap()
	if tags["team"].AsString() != "platform" || tags[markers.TagEstate].AsString() != "prod" {
		t.Errorf("merged tags = %#v, want team=platform and the written marker", tags)
	}

	sensitive := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("T1"),
		"arn":      cty.StringVal("arn:aws:after:::thing/T1"),
		"name":     cty.StringVal("thing"),
		"tags":     cty.MapVal(map[string]cty.Value{"team": cty.StringVal("platform")}).Mark("sensitive"),
		"tags_all": cty.NullVal(cty.Map(cty.String)),
	})
	out := withWrittenMarkers(sensitive, written)
	if !out.GetAttr("tags").HasMark("sensitive") {
		t.Fatal("the sensitive mark on tags was lost")
	}
	if v, _ := out.GetAttr("tags").Unmark(); len(v.AsValueMap()) != 1 {
		t.Errorf("a marked tags map was rewritten: %#v", v)
	}
	if ta := out.GetAttr("tags_all"); ta.IsNull() || ta.AsValueMap()[markers.TagEstate].AsString() != "prod" {
		t.Errorf("an unmarked null tags_all should take the written markers, got %#v", ta)
	}
}

// TestWriteAppliedMarkers_aRefusedWriteStoresTheProvidersObject: when the
// write fails, nothing landed, so the stored object must be the one the
// provider returned - claiming markers the cloud refused would be a worse
// lie than the one #1316 fixes.
func TestWriteAppliedMarkers_aRefusedWriteStoresTheProvidersObject(t *testing.T) {
	n := tocResolver(t, &fakeTagger{err: errors.New("AccessDeniedException (HTTP 403): refused by test")})
	applied := tocApplied("arn:aws:after:::thing/T1", "T1", nil)
	stored, diags := n.WriteAppliedMarkers(context.Background(), locatedTestAddr(t, "aws_after_thing", "x"), tocProvider(), plans.Create, applied, tocSchema())
	if !diags.HasErrors() {
		t.Fatal("a refused write must be an error")
	}
	if !stored.RawEquals(applied) {
		t.Errorf("a refused write stored %#v, want the provider's object unchanged", stored)
	}
}

// TestWriteAppliedMarkers_theSurfaceChoosesTheWriter (GitHub issue #1587):
// the resolver asks the command layer for the writer the tag surface's
// own post-create write names, and a writer the command layer refuses is
// a failed write carrying the command layer's reason, never a skip.
func TestWriteAppliedMarkers_theSurfaceChoosesTheWriter(t *testing.T) {
	after := locatedTestAddr(t, "aws_after_thing", "x")
	ctx := context.Background()

	tagger := &fakeTagger{}
	n := tocResolver(t, tagger)
	if _, diags := n.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, tocApplied("arn:aws:after:::thing/T1", "T1", nil), tocSchema()); diags.HasErrors() {
		t.Fatalf("write failed: %v", diags.Err())
	}
	if len(tagger.writes) != 1 || tagger.writes[0] != substrate.WriteTaggingAPI {
		t.Errorf("asked the command layer for %v, want exactly [%s]", tagger.writes, substrate.WriteTaggingAPI)
	}

	refused := tocResolver(t, nil)
	refused.MarkerWriter = func(addrs.AbsProviderConfig, substrate.Write) (MarkerWriter, error) {
		return nil, errors.New(`provider family graph declares the "graph-binding" post-create marker write and this build has no writer for it`)
	}
	_, diags := refused.WriteAppliedMarkers(ctx, after, tocProvider(), plans.Create, tocApplied("arn:aws:after:::thing/T1", "T1", nil), tocSchema())
	if !diags.HasErrors() {
		t.Fatal("a refused writer was reported as a marked object")
	}
	if d := diags[0].Description(); d.Summary != SummaryMarkerNotWritten || !strings.Contains(d.Detail, `"graph-binding"`) || !strings.Contains(d.Detail, "family graph") {
		t.Errorf("a refused writer's reason is not in the diagnostic:\n%s\n%s", d.Summary, d.Detail)
	}
}

// TestWriteAppliedMarkers_theWriterReceivesTheCreatedInstance (GitHub issue
// #1638): the post-create writer is handed the created instance - its
// address, the provider configuration it was applied under, and the object
// the provider returned - never a pre-derived ARN. A non-AWS family's
// writer (a GCP tag binding, say) addresses the object by something other
// than an arn, so an applied object with no arn attribute at all must still
// reach it, and whatever it needs it reads off the instance itself.
func TestWriteAppliedMarkers_theWriterReceivesTheCreatedInstance(t *testing.T) {
	n := tocResolver(t, nil)
	graph := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("graph")}
	after := locatedTestAddr(t, "aws_after_thing", "x")

	writer := &fakeObjectWriter{}
	n.MarkerWriter = func(provider addrs.AbsProviderConfig, _ substrate.Write) (MarkerWriter, error) {
		if !provider.Provider.Equals(graph.Provider) {
			t.Errorf("writer built for %s, want %s", provider, graph)
		}
		return writer, nil
	}

	// The object a non-AWS provider returns: an id and a name, no arn.
	applied := tocApplied("", "projects/p/things/T1", nil)
	stored, diags := n.WriteAppliedMarkers(context.Background(), after, graph, plans.Create, applied, tocSchema())
	if diags.HasErrors() {
		t.Fatalf("a non-AWS writer never got the object: %v", diags.Err())
	}
	if len(writer.calls) != 1 {
		t.Fatalf("want exactly 1 write, got %d", len(writer.calls))
	}
	got := writer.calls[0]
	if !got.created.Addr.Equal(after) {
		t.Errorf("writer got address %s, want %s", got.created.Addr, after)
	}
	if !got.created.Provider.Provider.Equals(graph.Provider) {
		t.Errorf("writer got provider %s, want %s", got.created.Provider, graph)
	}
	if !got.created.Object.RawEquals(applied) {
		t.Errorf("writer got object %#v, want the provider's object %#v", got.created.Object, applied)
	}
	if got.tags[markers.TagEstate] != "prod" || got.tags[markers.TagAddress] != "aws_after_thing.x" {
		t.Errorf("writer got markers %v, want the withheld ones", got.tags)
	}
	if tags := stored.GetAttr("tags"); tags.IsNull() || tags.AsValueMap()[markers.TagEstate].AsString() != "prod" {
		t.Errorf("a successful write did not store the written markers: %#v", tags)
	}
}

// fakeObjectWriter is a non-AWS family's post-create writer: it records the
// created instance it was handed.
type fakeObjectWriter struct {
	calls []fakeObjectWrite
}

type fakeObjectWrite struct {
	created CreatedInstance
	tags    map[string]string
}

func (f *fakeObjectWriter) WriteMarkers(_ context.Context, created CreatedInstance, tags map[string]string) error {
	f.calls = append(f.calls, fakeObjectWrite{created: created, tags: tags})
	return nil
}

// bindingSurface is the fake family's marker surface: a synthetic value no
// real family owns, so nothing here may be answered by AWS's registry.
const bindingSurface markers.Surface = "graph-binding-surface"

// bindingFamily is a non-AWS family whose marker cannot ride its types'
// create calls and is written by a binding once the create returns (GitHub
// issue #1642). It carries the surface on a schema with a "binding_target"
// attribute, and declares the post-create write for its own types by its
// own rule, reading no registry.
type bindingFamily struct{ substrate.Substrate }

func (bindingFamily) Name() string                                         { return "graph" }
func (bindingFamily) Surfaces() []markers.Surface                          { return []markers.Surface{bindingSurface} }
func (bindingFamily) CarriesAddress() bool                                 { return true }
func (bindingFamily) MarkerWriter(addrs.AbsProviderConfig) substrate.Write { return "graph-binding" }
func (bindingFamily) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if _, ok := block.Attributes["binding_target"]; ok {
		return bindingSurface, true
	}
	return "", false
}
func (f bindingFamily) OwnershipSurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	return f.SurfaceOf(block)
}
func (bindingFamily) Writes(surface markers.Surface) substrate.Writes {
	if surface == bindingSurface {
		return substrate.Writes{Create: substrate.WriteInCreate, Adopt: "graph-binding", PostCreate: "graph-binding"}
	}
	return substrate.Writes{}
}
func (bindingFamily) PostCreateNeeded(surface markers.Surface, typeName string, _ substrate.CreateTagFacts) (string, bool) {
	if surface == bindingSurface && strings.HasPrefix(typeName, "graph_") {
		return typeName + " takes its marker as a binding after the create", true
	}
	return "", false
}

// TestWriteAppliedMarkers_aFamilyDeclaresItsOwnPostCreate (GitHub issue
// #1642): whether a create needs the post-create write is the family's
// answer, not the AWS CloudFormation registry's. A non-AWS type with no
// registry row at all, whose family declares the write, reaches that
// family's writer with the created instance; the same family's answer of
// false, and the Kubernetes family's never, leave the create alone.
func TestWriteAppliedMarkers_aFamilyDeclaresItsOwnPostCreate(t *testing.T) {
	saved := substrate.All
	substrate.All = append(append([]substrate.Substrate(nil), saved...), bindingFamily{substrate.AWS})
	t.Cleanup(func() { substrate.All = saved })

	schema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":             {Type: cty.String, Computed: true},
			"name":           {Type: cty.String, Required: true},
			"binding_target": {Type: cty.String, Computed: true},
		},
	}}
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":             cty.StringVal("projects/p/things/T1"),
		"name":           cty.StringVal("thing"),
		"binding_target": cty.StringVal("//graph/projects/p/things/T1"),
	})
	graph := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("graph")}

	n := tocResolver(t, nil)
	writer := &fakeObjectWriter{}
	var asked []substrate.Write
	n.MarkerWriter = func(_ addrs.AbsProviderConfig, write substrate.Write) (MarkerWriter, error) {
		asked = append(asked, write)
		return writer, nil
	}

	thing := locatedTestAddr(t, "graph_thing", "x")
	if _, diags := n.WriteAppliedMarkers(context.Background(), thing, graph, plans.Create, applied, schema); diags.HasErrors() {
		t.Fatalf("write failed: %v", diags.Err())
	}
	if len(writer.calls) != 1 {
		t.Fatalf("the family declared a post-create write for graph_thing and its writer got %d writes, want 1", len(writer.calls))
	}
	if len(asked) != 1 || asked[0] != "graph-binding" {
		t.Errorf("asked the command layer for %v, want exactly [graph-binding]", asked)
	}
	if got := writer.calls[0]; !got.created.Addr.Equal(thing) || got.tags[markers.TagEstate] != "prod" || got.tags[markers.TagAddress] != "graph_thing.x" {
		t.Errorf("writer got %s with %v", got.created.Addr, got.tags)
	}

	// The family's own false: a type it does not declare takes the
	// create-call path.
	other := locatedTestAddr(t, "notgraph_thing", "x")
	if _, diags := n.WriteAppliedMarkers(context.Background(), other, graph, plans.Create, applied, schema); diags.HasErrors() {
		t.Fatalf("write failed: %v", diags.Err())
	}
	if len(writer.calls) != 1 {
		t.Errorf("a type the family does not declare reached the writer: %d writes", len(writer.calls))
	}
}

// TestPostCreateNeeded_eachFamilyAnswers pins the three answers #1642
// moves onto the families: AWS reads the registry exactly as the
// projection did (the tag_on_create false type only, and only on the tags
// surface), Kubernetes never, and the zero surface nothing.
func TestPostCreateNeeded_eachFamilyAnswers(t *testing.T) {
	r := tocRoster(t)
	if why, ok := substrate.PostCreateNeeded(markers.SurfaceTags, "aws_after_thing", r); !ok || why != "AWS::After::Thing does not take tags in its create call (live/registry.json: tag_on_create false)" {
		t.Errorf("aws_after_thing: %v %q", ok, why)
	}
	for _, typ := range []string{"aws_ordinary_thing", "aws_unmapped_thing"} {
		if _, ok := substrate.PostCreateNeeded(markers.SurfaceTags, typ, r); ok {
			t.Errorf("%s needs a post-create write, want the create-call path", typ)
		}
	}
	var nilRoster *registry.Roster
	if _, ok := substrate.PostCreateNeeded(markers.SurfaceTags, "aws_after_thing", nilRoster); ok {
		t.Error("a run with no roster needs a post-create write")
	}
	for _, surface := range []markers.Surface{markers.SurfaceLabels, markers.SurfaceManifest, ""} {
		if _, ok := substrate.PostCreateNeeded(surface, "aws_after_thing", r); ok {
			t.Errorf("surface %q needs a post-create write", surface)
		}
	}
}

// widgetSurface is a non-AWS family's marker surface, synthetic on purpose:
// nothing here may be answered by AWS's registry or the Tagging API.
const widgetSurface markers.Surface = "widget-binding-surface"

// widgetFamily is a non-AWS family (GitHub issue #1653) whose created
// objects happen to carry an "arn" attribute - the point of this fake: an
// object having an arn says nothing about which family it belongs to, and
// before this issue's fix the failure diagnostic printed the AWS Tagging
// API command whenever one was present, whatever the family. widgetFamily's
// own [substrate.Substrate.ManualMarkFix] names a wholly different remedy.
type widgetFamily struct{ substrate.Substrate }

func (widgetFamily) Name() string                { return "widget" }
func (widgetFamily) Surfaces() []markers.Surface { return []markers.Surface{widgetSurface} }
func (widgetFamily) CarriesAddress() bool        { return true }
func (widgetFamily) MarkerWriter(addrs.AbsProviderConfig) substrate.Write {
	return "widget-binding"
}
func (widgetFamily) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if _, ok := block.Attributes["widget_target"]; ok {
		return widgetSurface, true
	}
	return "", false
}
func (f widgetFamily) OwnershipSurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	return f.SurfaceOf(block)
}
func (widgetFamily) Writes(surface markers.Surface) substrate.Writes {
	if surface == widgetSurface {
		return substrate.Writes{Create: substrate.WriteInCreate, Adopt: "widget-binding", PostCreate: "widget-binding"}
	}
	return substrate.Writes{}
}
func (widgetFamily) PostCreateNeeded(surface markers.Surface, typeName string, _ substrate.CreateTagFacts) (string, bool) {
	if surface == widgetSurface {
		return typeName + " takes its marker as a widget binding after the create", true
	}
	return "", false
}
func (widgetFamily) ManualMarkFix(typeName, arn string, want map[string]string, _ substrate.CreateTagFacts) string {
	return fmt.Sprintf("Run: widgetctl adopt --type %s --arn %s --tags %s", typeName, arn, markers.TagsArgument(want))
}

// TestWriteAppliedMarkers_theFixHintIsTheFamilysAnswer (GitHub issue
// #1653): the manual-remedy sentence a failed post-create write prints is
// asked of the created instance's own family, not chosen by whether the
// applied object happens to carry an "arn" attribute. widgetFamily's
// objects carry one, and before this fix the diagnostic printed the AWS
// resourcegroupstaggingapi command for them anyway.
func TestWriteAppliedMarkers_theFixHintIsTheFamilysAnswer(t *testing.T) {
	saved := substrate.All
	substrate.All = append(append([]substrate.Substrate(nil), saved...), widgetFamily{substrate.AWS})
	t.Cleanup(func() { substrate.All = saved })

	schema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":            {Type: cty.String, Computed: true},
			"arn":           {Type: cty.String, Computed: true},
			"name":          {Type: cty.String, Required: true},
			"widget_target": {Type: cty.String, Computed: true},
		},
	}}
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal("W1"),
		"arn":           cty.StringVal("arn:widget:1"),
		"name":          cty.StringVal("thing"),
		"widget_target": cty.StringVal("//widget/W1"),
	})
	widget := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("widget")}

	n := tocResolver(t, nil)
	n.MarkerWriter = func(addrs.AbsProviderConfig, substrate.Write) (MarkerWriter, error) {
		return failingWriter{err: errors.New("refused by test")}, nil
	}

	thing := locatedTestAddr(t, "widget_thing", "x")
	_, diags := n.WriteAppliedMarkers(context.Background(), thing, widget, plans.Create, applied, schema)
	if !diags.HasErrors() {
		t.Fatal("a refused widget write did not fail the apply")
	}
	detail := diags[0].Description().Detail
	if !strings.Contains(detail, "widgetctl adopt --type widget_thing --arn arn:widget:1") {
		t.Errorf("the family's own fix is not in the diagnostic:\n%s", detail)
	}
	if strings.Contains(detail, "aws resourcegroupstaggingapi") {
		t.Errorf("the AWS Tagging API command was printed for a non-AWS family just because the object carried an arn:\n%s", detail)
	}
}

// failingWriter is a [MarkerWriter] that always refuses.
type failingWriter struct{ err error }

func (f failingWriter) WriteMarkers(context.Context, CreatedInstance, map[string]string) error {
	return f.err
}
