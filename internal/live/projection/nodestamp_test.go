// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// nodeStampTestConfig is a bare-bones evaluated configuration value for
// markersRecordTestType (aws_ebs_volume, markersRecordTypeSchema - the same
// taggable fixture markers_record_test.go already uses), with tags set to
// whatever the caller passes.
func nodeStampTestConfig(tags cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":                cty.NullVal(cty.String),
		"availability_zone": cty.StringVal("us-east-1a"),
		"size":              cty.NumberIntVal(8),
		"tags":              tags,
	})
}

// requireTags reads back the "tags" attribute of an AdjustConfigValue
// result as a plain map[string]string, failing the test if the shape is not
// what stampedTags always produces (a known, non-null map of strings).
func requireTags(t *testing.T, config cty.Value) map[string]string {
	t.Helper()
	tagsVal := config.GetAttr("tags")
	if tagsVal.IsMarked() {
		tagsVal, _ = tagsVal.Unmark()
	}
	if tagsVal.IsNull() || !tagsVal.IsKnown() {
		t.Fatalf("tags is null or unknown: %#v", tagsVal)
	}
	out := map[string]string{}
	for it := tagsVal.ElementIterator(); it.Next(); {
		k, v := it.Element()
		out[k.AsString()] = v.AsString()
	}
	return out
}

// TestNodeResolver_AdjustConfigValue_stampsUntaggedResource is the ordinary
// case: a taggable instance with no tags argument at all gets exactly the
// two ownership markers, and no tofu.marker_module_prefix or any other
// evaluator symbol is anywhere in sight - addr is already concrete, so
// [markers.EscapeAddress] is applied to its own String() directly.
func TestNodeResolver_AdjustConfigValue_stampsUntaggedResource(t *testing.T) {
	addr := locatedTestAddr(t, markersRecordTestType, "main")
	resolver := &NodeResolver{Estate: "test-estate"}

	got, diags := resolver.AdjustConfigValue(context.Background(), addr, nodeStampTestConfig(cty.NullVal(cty.Map(cty.String))), markersRecordTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}

	tags := requireTags(t, got)
	want := map[string]string{
		markers.TagEstate:  "test-estate",
		markers.TagAddress: markersRecordTestType + ".main",
	}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("tags[%q] = %q, want %q", k, tags[k], v)
		}
	}

	// Every other attribute is untouched.
	if !got.GetAttr("availability_zone").RawEquals(cty.StringVal("us-east-1a")) {
		t.Errorf("availability_zone was touched: %#v", got.GetAttr("availability_zone"))
	}
}

// TestNodeResolver_AdjustConfigValue_keyedModuleInstanceNoTemplate is
// GitHub issue #388's headline claim for the node seam: a resource under a
// keyed module call gets the correct per-instance tofu-address with no
// tofu.marker_module_prefix template involved, because addr already names
// the one concrete instance this call is for.
func TestNodeResolver_AdjustConfigValue_keyedModuleInstanceNoTemplate(t *testing.T) {
	addr := addrs.AbsResourceInstance{
		Module: addrs.ModuleInstance{
			{Name: "container_definition", InstanceKey: addrs.StringKey("fluent-bit")},
		},
		Resource: addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: markersRecordTestType,
			Name: "data",
		}.Instance(addrs.StringKey("a")),
	}

	resolver := &NodeResolver{Estate: "test-estate"}
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, nodeStampTestConfig(cty.NullVal(cty.Map(cty.String))), markersRecordTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}

	tags := requireTags(t, got)

	// The literal escaped form MARKERS.md's spec describes: a "[...]"
	// instance key, module or resource, becomes ":" followed by its
	// (identifier-safe here, so unchanged) key text.
	const wantAddress = `module.container_definition:fluent-bit.` + markersRecordTestType + `.data:a`
	if got := markers.EscapeAddress(addr.String()); got != wantAddress {
		t.Fatalf("test's own assumption about EscapeAddress is wrong: EscapeAddress(%q) = %q, want %q - fix the literal before trusting the rest of this test", addr.String(), got, wantAddress)
	}
	if tags[markers.TagAddress] != wantAddress {
		t.Errorf("tofu-address = %q, want %q (addr.String() = %q)", tags[markers.TagAddress], wantAddress, addr.String())
	}
}

// TestNodeResolver_AdjustConfigValue_keepsOwnTags is the merge()-tagged
// case: by the time AdjustConfigValue runs, an expression like
// merge(local.common_tags, {Owner = "platform"}) has already evaluated to a
// plain cty map, indistinguishable here from a literal object - the
// operator's own entries are preserved and the two markers are added
// alongside them.
func TestNodeResolver_AdjustConfigValue_keepsOwnTags(t *testing.T) {
	addr := locatedTestAddr(t, markersRecordTestType, "main")
	resolver := &NodeResolver{Estate: "test-estate"}

	existing := cty.MapVal(map[string]cty.Value{"Owner": cty.StringVal("platform")})
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, nodeStampTestConfig(existing), markersRecordTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}

	tags := requireTags(t, got)
	want := map[string]string{
		"Owner":            "platform",
		markers.TagEstate:  "test-estate",
		markers.TagAddress: markersRecordTestType + ".main",
	}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("tags[%q] = %q, want %q", k, tags[k], v)
		}
	}
}

// TestNodeResolver_AdjustConfigValue_setsSlotWhenAssigned is GitHub issue
// #388's slot decision: threaded through as a plain map lookup, keyed by
// the same escaped address tofu-address already carries, rather than left
// for the HCL path.
func TestNodeResolver_AdjustConfigValue_setsSlotWhenAssigned(t *testing.T) {
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: markersRecordTestType, Name: "pool"}.
		Instance(addrs.IntKey(2)).Absolute(addrs.RootModuleInstance)

	resolver := &NodeResolver{
		Estate: "test-estate",
		Slots: map[string]string{
			markersRecordTestType + ".pool:2": "0",
		},
	}
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, nodeStampTestConfig(cty.NullVal(cty.Map(cty.String))), markersRecordTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}

	tags := requireTags(t, got)
	if tags[markers.TagSlot] != "0" {
		t.Errorf("tofu-slot = %q, want %q (tags: %v)", tags[markers.TagSlot], "0", tags)
	}

	// An instance the sweep never assigned a slot to gets no tofu-slot tag
	// at all, not an empty one.
	unassigned := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: markersRecordTestType, Name: "pool"}.
		Instance(addrs.IntKey(7)).Absolute(addrs.RootModuleInstance)
	got2, diags2 := resolver.AdjustConfigValue(context.Background(), unassigned, nodeStampTestConfig(cty.NullVal(cty.Map(cty.String))), markersRecordTypeSchema())
	if diags2.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags2.Err())
	}
	tags2 := requireTags(t, got2)
	if _, ok := tags2[markers.TagSlot]; ok {
		t.Errorf("unassigned instance got a tofu-slot tag: %v", tags2)
	}
}

// TestNodeResolver_AdjustConfigValue_recordSelectionSetsNothing is GitHub
// issue #388's stamp half honouring strict { markers "record" }: a
// selected, record-eligible instance's configuration value comes back
// completely unchanged - not "unchanged tags", the identical cty.Value -
// because nothing about this resource's ownership marker is this pass's to
// write.
//
// markersRecordTestType/markersRecordTypeSchema are markers_record_test.go's
// own fixture, chosen there for being both taggable and record-eligible
// (identity.SelectedLocatedType); reused here so this test and that one
// can never quietly disagree about which types the selection reaches.
func TestNodeResolver_AdjustConfigValue_recordSelectionSetsNothing(t *testing.T) {
	addr := locatedTestAddr(t, markersRecordTestType, "data")
	sel, problems := strict.ParseSelection([]string{markersRecordTestType}, nil)
	if len(problems) != 0 {
		t.Fatalf("unexpected selection problems: %v", problems)
	}

	resolver := &NodeResolver{Estate: "test-estate", Selection: sel}
	config := nodeStampTestConfig(cty.NullVal(cty.Map(cty.String)))

	got, diags := resolver.AdjustConfigValue(context.Background(), addr, config, markersRecordTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if !got.RawEquals(config) {
		t.Errorf("a record-selected instance's configuration was changed:\ngot:  %#v\nwant: %#v", got, config)
	}
}

// TestNodeResolver_AdjustConfigValue_noEstateSetsNothing is the flip's own
// regression, caught by TestLivePlan_stampingNeedsAnEstateName once
// CHOUDOUFU_NODE_RESOLVE defaulted on (2026-08-25): a resolver with no
// estate name - the ordinary "-estate not given, no live block names one"
// shape internal/command/live_plan.go's statelessStamp already degrades
// gracefully for on the HCL path (its own estate=="" branch: a single
// "Ownership markers not stamped" warning, config untouched) - must leave
// the configuration exactly as it found it, not write
// tags["tofu-estate"] = "" into the plan. An empty ownership tag is not a
// smaller version of a real one: cty.StringVal("") is a value a later run
// reads back as "owned by an estate named the empty string," which is
// worse than no tag at all, the same class of failure HANDOFF's "never
// write a wrong marker" rule names on the read side.
func TestNodeResolver_AdjustConfigValue_noEstateSetsNothing(t *testing.T) {
	addr := locatedTestAddr(t, markersRecordTestType, "main")
	resolver := &NodeResolver{} // Estate deliberately left as the zero value.
	config := nodeStampTestConfig(cty.NullVal(cty.Map(cty.String)))

	got, diags := resolver.AdjustConfigValue(context.Background(), addr, config, markersRecordTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if !got.RawEquals(config) {
		t.Errorf("a no-estate run's configuration was changed:\ngot:  %#v\nwant: %#v", got, config)
	}
}

// TestNodeResolver_AdjustConfigValue_recordSelectionOnlyAffectsSelected is
// claim 2 of markers_record_test.go's own numbering, over this seam: a
// selection over one type must not widen to a sibling resource of a
// DIFFERENT type that merely happens to share the same run.
func TestNodeResolver_AdjustConfigValue_recordSelectionOnlyAffectsSelected(t *testing.T) {
	sel, _ := strict.ParseSelection([]string{markersRecordTestType}, nil)
	resolver := &NodeResolver{Estate: "test-estate", Selection: sel}

	other := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_vpc", Name: "main"}.
		Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	otherSchema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"tags": {Type: cty.Map(cty.String), Optional: true},
		},
	}}
	config := cty.ObjectVal(map[string]cty.Value{
		"id":   cty.NullVal(cty.String),
		"tags": cty.NullVal(cty.Map(cty.String)),
	})

	got, diags := resolver.AdjustConfigValue(context.Background(), other, config, otherSchema)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	tags := requireTags(t, got)
	if tags[markers.TagAddress] != "aws_vpc.main" || tags[markers.TagEstate] != "test-estate" {
		t.Errorf("the unselected sibling was not stamped normally: %v", tags)
	}
}

// TestNodeResolver_AdjustConfigValue_untaggableTypeUnchanged is the ordinary
// "nothing to do" case: a type with no settable tags map ([markers.
// Taggable]'s false answer) is left byte for byte alone, the same as a
// resource whose schema this run holds no block for.
func TestNodeResolver_AdjustConfigValue_untaggableTypeUnchanged(t *testing.T) {
	addr := locatedTestAddr(t, "aws_route", "r")
	resolver := &NodeResolver{Estate: "test-estate"}

	schema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"route_table_id": {Type: cty.String, Required: true},
		},
	}}
	config := cty.ObjectVal(map[string]cty.Value{
		"route_table_id": cty.StringVal("rtb-0123456789abcdef0"),
	})

	got, diags := resolver.AdjustConfigValue(context.Background(), addr, config, schema)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if !got.RawEquals(config) {
		t.Errorf("an untaggable type's configuration was changed:\ngot:  %#v\nwant: %#v", got, config)
	}
}

// TestNodeResolver_AdjustConfigValue_preservesMarkOnTagsValue is the
// marksafe proof: a tags argument built from a sensitive source (a
// merge() call folding in a `sensitive()`-wrapped local, however unusual
// that is for a tags map in practice) survives with its mark intact and its
// entries - including the two freshly-added markers - present underneath
// it. Nothing here ever reads a marked value's content: [cty.Value.Unmark]
// strips only the outer mark ([markers.EscapeAddress] and the literal
// marker values never pass through anything derived from the marked input),
// and the mark is put back with WithMarks before this function returns.
func TestNodeResolver_AdjustConfigValue_preservesMarkOnTagsValue(t *testing.T) {
	addr := locatedTestAddr(t, markersRecordTestType, "main")
	resolver := &NodeResolver{Estate: "test-estate"}

	existing := cty.MapVal(map[string]cty.Value{"Owner": cty.StringVal("platform")}).Mark("sensitive")
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, nodeStampTestConfig(existing), markersRecordTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}

	tagsVal := got.GetAttr("tags")
	if !tagsVal.IsMarked() {
		t.Fatalf("the marker on the tags value was dropped: %#v", tagsVal)
	}
	if _, ok := tagsVal.Marks()["sensitive"]; !ok {
		t.Errorf("the tags value carries a mark, but not the one it started with: %#v", tagsVal.Marks())
	}

	tags := requireTags(t, got)
	want := map[string]string{
		"Owner":            "platform",
		markers.TagEstate:  "test-estate",
		markers.TagAddress: markersRecordTestType + ".main",
	}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("tags[%q] = %q, want %q", k, tags[k], v)
		}
	}
}
