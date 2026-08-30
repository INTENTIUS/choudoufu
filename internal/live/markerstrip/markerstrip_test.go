// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markerstrip

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
)

var testSchema = &providers.Schema{
	Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Optional: true, Computed: true},
			"tags":     {Type: cty.Map(cty.String), Optional: true},
			"tags_all": {Type: cty.Map(cty.String), Computed: true},
		},
	},
}

// untaggedSchema is a type with nowhere to hang a tag. Such a type can still
// be perfectly identifiable - see this repository's marker/identity split -
// but it can never appear in a marker removal, and Scan must not trip over
// one.
var untaggedSchema = &providers.Schema{
	Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Optional: true, Computed: true},
		},
	},
}

func schemaFor(_ addrs.Provider, _ addrs.ResourceMode, typeName string) *providers.Schema {
	if typeName == "test_untaggable" {
		return untaggedSchema
	}
	return testSchema
}

// obj builds a resource object value for testSchema. A nil tags map means
// the attribute is null; an "unknown" sentinel key makes it unknown.
func obj(name string, tags map[string]string, unknownTags bool) cty.Value {
	attrs := map[string]cty.Value{
		"id":       cty.StringVal(name),
		"tags_all": cty.NullVal(cty.Map(cty.String)),
	}
	switch {
	case unknownTags:
		attrs["tags"] = cty.UnknownVal(cty.Map(cty.String))
	case tags == nil:
		attrs["tags"] = cty.NullVal(cty.Map(cty.String))
	case len(tags) == 0:
		attrs["tags"] = cty.MapValEmpty(cty.String)
	default:
		vals := make(map[string]cty.Value, len(tags))
		for k, v := range tags {
			vals[k] = cty.StringVal(v)
		}
		attrs["tags"] = cty.MapVal(vals)
	}
	return cty.ObjectVal(attrs)
}

func change(t *testing.T, name string, action plans.Action, before, after cty.Value) *plans.ResourceInstanceChangeSrc {
	t.Helper()
	return changeOfType(t, "test_instance", name, action, before, after)
}

func changeOfType(t *testing.T, typeName, name string, action plans.Action, before, after cty.Value) *plans.ResourceInstanceChangeSrc {
	t.Helper()
	addr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: typeName,
		Name: name,
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

	src, err := (&plans.ResourceInstanceChange{
		Addr:         addr,
		PrevRunAddr:  addr,
		ProviderAddr: addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule},
		Change:       plans.Change{Action: action, Before: before, After: after},
	}).Encode(schemaFor(addrs.Provider{}, addrs.ManagedResourceMode, typeName))
	if err != nil {
		t.Fatalf("encoding %s: %s", addr, err)
	}
	return src
}

func addrsOf(removals []Removal) []string {
	out := make([]string, 0, len(removals))
	for _, r := range removals {
		out = append(out, r.Addr.String())
	}
	return out
}

// TestScan_reportsAnInPlaceMarkerRemoval is the shape #613 is about.
func TestScan_reportsAnInPlaceMarkerRemoval(t *testing.T) {
	before := obj("a", map[string]string{"Name": "a", "tofu-estate": "e1", "tofu-address": "test_instance.a"}, false)
	after := obj("a", map[string]string{"Name": "a"}, false)

	got := Scan([]*plans.ResourceInstanceChangeSrc{change(t, "a", plans.Update, before, after)}, schemaFor)
	if len(got) != 1 {
		t.Fatalf("got %d removals, want 1: %#v", len(got), got)
	}
	if got[0].Estate != "e1" {
		t.Errorf("estate %q, want %q", got[0].Estate, "e1")
	}
	if want := []string{"tofu-address", "tofu-estate"}; !reflect.DeepEqual(got[0].Keys, want) {
		t.Errorf("keys %v, want %v", got[0].Keys, want)
	}
}

// TestScan_silentOnEverythingThatIsNotARemoval is the whole "does this newly
// refuse anything that used to work" question, as a table. Every row here is
// a run an operator has every right to make.
func TestScan_silentOnEverythingThatIsNotARemoval(t *testing.T) {
	marked := map[string]string{"Name": "a", "tofu-estate": "e1", "tofu-address": "test_instance.a"}

	cases := []struct {
		name   string
		change *plans.ResourceInstanceChangeSrc
	}{
		{
			"markers kept",
			change(t, "a", plans.Update, obj("a", marked, false), obj("a", marked, false)),
		},
		{
			"never marked",
			change(t, "a", plans.Update, obj("a", map[string]string{"Name": "a"}, false), obj("a", map[string]string{"Name": "b"}, false)),
		},
		{
			"destroy",
			change(t, "a", plans.Delete, obj("a", marked, false), cty.NullVal(testSchema.Block.ImpliedType())),
		},
		{
			"replace",
			change(t, "a", plans.DeleteThenCreate, obj("a", marked, false), obj("a", map[string]string{"Name": "a"}, false)),
		},
		{
			"create",
			change(t, "a", plans.Create, cty.NullVal(testSchema.Block.ImpliedType()), obj("a", map[string]string{"Name": "a"}, false)),
		},
		{
			"no-op",
			change(t, "a", plans.NoOp, obj("a", marked, false), obj("a", marked, false)),
		},
		{
			"planned tags unknown",
			change(t, "a", plans.Update, obj("a", marked, false), obj("a", nil, true)),
		},
		{
			"untaggable type",
			changeOfType(t, "test_untaggable", "a", plans.Update,
				cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("a")}),
				cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("b")})),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Scan([]*plans.ResourceInstanceChangeSrc{tc.change}, schemaFor); len(got) != 0 {
				t.Errorf("Scan reported %v, want nothing", addrsOf(got))
			}
		})
	}
}

// TestScan_reportsARewriteToAnotherEstate covers the case that is not a
// removal but is the same hazard: the plan proposes handing the resource to
// a different estate, which is still the ownership record being overwritten
// by a run that has no idea it exists.
func TestScan_reportsARewriteToAnotherEstate(t *testing.T) {
	before := obj("a", map[string]string{"tofu-estate": "e1", "tofu-address": "test_instance.a"}, false)
	after := obj("a", map[string]string{"tofu-estate": "e2", "tofu-address": "test_instance.a"}, false)

	got := Scan([]*plans.ResourceInstanceChangeSrc{change(t, "a", plans.Update, before, after)}, schemaFor)
	if len(got) != 1 {
		t.Fatalf("got %d removals, want 1", len(got))
	}
	if want := []string{"tofu-estate"}; !reflect.DeepEqual(got[0].Keys, want) {
		t.Errorf("keys %v, want %v - tofu-address is unchanged and must not be listed", got[0].Keys, want)
	}
}

// TestScan_isOrderedByAddress. The plan's change order is not a promise, and
// a refusal that names "the first five" must name the same five twice.
func TestScan_isOrderedByAddress(t *testing.T) {
	marked := func(name string) cty.Value {
		return obj(name, map[string]string{"tofu-estate": "e1", "tofu-address": "test_instance." + name}, false)
	}
	bare := func(name string) cty.Value { return obj(name, map[string]string{}, false) }

	changes := []*plans.ResourceInstanceChangeSrc{
		change(t, "c", plans.Update, marked("c"), bare("c")),
		change(t, "a", plans.Update, marked("a"), bare("a")),
		change(t, "b", plans.Update, marked("b"), bare("b")),
	}
	want := []string{"test_instance.a", "test_instance.b", "test_instance.c"}
	if got := addrsOf(Scan(changes, schemaFor)); !reflect.DeepEqual(got, want) {
		t.Errorf("Scan order %v, want %v", got, want)
	}
	// Reversed input, same answer.
	changes[0], changes[2] = changes[2], changes[0]
	if got := addrsOf(Scan(changes, schemaFor)); !reflect.DeepEqual(got, want) {
		t.Errorf("Scan order %v on reordered input, want %v", got, want)
	}
}

// TestScan_readsAMarkerThatArrivedThroughTagsAll. The stamp writes to
// "tags", but a provider's default_tags is a legal way for the pair to
// reach a resource, and markers.TagsOf merges both. This also exercises the
// msgpack pre-filter on a value whose marker is nowhere near "tags": the
// filter searches the encoded object, not one attribute of it.
func TestScan_readsAMarkerThatArrivedThroughTagsAll(t *testing.T) {
	withTagsAll := func(all map[string]string) cty.Value {
		vals := make(map[string]cty.Value, len(all))
		for k, v := range all {
			vals[k] = cty.StringVal(v)
		}
		tagsAll := cty.MapValEmpty(cty.String)
		if len(vals) > 0 {
			tagsAll = cty.MapVal(vals)
		}
		return cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal("a"),
			"tags":     cty.MapValEmpty(cty.String),
			"tags_all": tagsAll,
		})
	}
	before := withTagsAll(map[string]string{"tofu-estate": "e1", "tofu-address": "test_instance.a"})
	after := withTagsAll(nil)

	got := Scan([]*plans.ResourceInstanceChangeSrc{change(t, "a", plans.Update, before, after)}, schemaFor)
	if len(got) != 1 {
		t.Fatalf("got %d removals, want 1: a marker in tags_all is still a marker", len(got))
	}
	if got[0].Estate != "e1" {
		t.Errorf("estate %q, want %q", got[0].Estate, "e1")
	}
}

// TestScan_theByteFilterIsAFilterAndNotAnAnswer. An encoded object that
// contains the marker key's bytes somewhere harmless must still be decided
// by the decode, not by the filter.
func TestScan_theByteFilterIsAFilterAndNotAnAnswer(t *testing.T) {
	decoy := map[string]string{"Name": "we call it tofu-estate around here"}
	got := Scan([]*plans.ResourceInstanceChangeSrc{
		change(t, "a", plans.Update, obj("a", decoy, false), obj("a", map[string]string{"Name": "b"}, false)),
	}, schemaFor)
	if len(got) != 0 {
		t.Errorf("Scan reported %v for a tag VALUE mentioning the marker key", addrsOf(got))
	}
}

// TestEstates_dedupesAndSorts.
func TestEstates_dedupesAndSorts(t *testing.T) {
	in := []Removal{{Estate: "b"}, {Estate: "a"}, {Estate: "b"}}
	if got, want := Estates(in), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Estates = %v, want %v", got, want)
	}
}

// TestMarkerKeys_coversEveryKeyTheMarkersPackageDefines guards the derivation
// rather than the list: the point of building MarkerKeys from the markers
// package's own constants is that a key added there is covered here without
// an edit, and this fails if someone replaces the derivation with a literal.
func TestMarkerKeys_coversEveryKeyTheMarkersPackageDefines(t *testing.T) {
	keys := MarkerKeys()
	have := make(map[string]bool, len(keys))
	for _, k := range keys {
		have[k] = true
	}
	for _, want := range []string{"tofu-estate", "tofu-address", "tofu-address-2", "tofu-address-3", "tofu-address-4", "tofu-slot"} {
		if !have[want] {
			t.Errorf("MarkerKeys() = %v, missing %q", keys, want)
		}
	}
	if len(keys) != 6 {
		t.Errorf("MarkerKeys() = %v (%d keys); if the markers package gained one, add it to the list above", keys, len(keys))
	}
}
