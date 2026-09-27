// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markerstrip

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1649: the scan read the AWS tags map and nothing else, so a
// Kubernetes object stamped by live-import, whose state-backed plan drops
// the tofu-estate label, passed the guard and the apply un-migrated it.
// Measured on kind (hashicorp/kubernetes 3.2.1): a state-backed plan over a
// stamped kubernetes_config_map_v1 and kubernetes_namespace proposes
// `- "tofu-estate" = "ms1649" -> null` in metadata.labels, exits 0, and the
// apply strips the label from both.

// labelSchema is the object-metadata shape every hashicorp/kubernetes typed
// resource shares: a single metadata block holding a settable labels map.
var labelSchema = &providers.Schema{
	Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Optional: true, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting:  configschema.NestingList,
				MinItems: 1,
				MaxItems: 1,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"name":             {Type: cty.String, Optional: true},
						"labels":           {Type: cty.Map(cty.String), Optional: true},
						"resource_version": {Type: cty.String, Computed: true},
					},
				},
			},
		},
	},
}

// manifestSchema is kubernetes_manifest's shape: the whole object is one
// dynamic argument, with the provider's read-back beside it.
var manifestSchema = &providers.Schema{
	Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest": {Type: cty.DynamicPseudoType, Required: true},
			"object":   {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
		},
	},
}

// labelled builds a label-surface object. A nil labels map is null;
// unknownLabels makes the map unknown; unknownVersion makes a sibling of the
// labels map unknown, which is not the carrier and must not hide it.
func labelled(labels map[string]string, unknownLabels, unknownVersion bool) cty.Value {
	lv := cty.NullVal(cty.Map(cty.String))
	switch {
	case unknownLabels:
		lv = cty.UnknownVal(cty.Map(cty.String))
	case labels != nil:
		lv = strMap(labels)
	}
	rv := cty.StringVal("12")
	if unknownVersion {
		rv = cty.UnknownVal(cty.String)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("ns/cm"),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":             cty.StringVal("cm"),
			"labels":           lv,
			"resource_version": rv,
		})}),
	})
}

// manifested builds a manifest-surface object whose manifest carries labels
// as an object constructor would write them.
func manifested(labels map[string]string, unknownLabels bool) cty.Value {
	var lv cty.Value
	switch {
	case unknownLabels:
		lv = cty.DynamicVal
	default:
		attrs := map[string]cty.Value{}
		for k, v := range labels {
			attrs[k] = cty.StringVal(v)
		}
		lv = cty.ObjectVal(attrs)
	}
	manifest := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("v1"),
		"kind":       cty.StringVal("ConfigMap"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("cm"),
			"namespace": cty.StringVal("ns"),
			"labels":    lv,
		}),
	})
	return cty.ObjectVal(map[string]cty.Value{
		"manifest": manifest,
		"object":   cty.NullVal(cty.DynamicPseudoType),
	})
}

func strMap(m map[string]string) cty.Value {
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	vals := make(map[string]cty.Value, len(m))
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	return cty.MapVal(vals)
}

func TestScan_reportsARemovedLabel(t *testing.T) {
	stamped := map[string]string{"team": "a", "tofu-estate": "e1"}
	plain := map[string]string{"team": "a"}

	cases := []struct {
		name    string
		typ     string
		before  cty.Value
		after   cty.Value
		surface markers.Surface
	}{
		{"metadata labels", "test_labelled", labelled(stamped, false, false), labelled(plain, false, false), markers.SurfaceLabels},
		{"metadata labels, a sibling unknown", "test_labelled", labelled(stamped, false, false), labelled(plain, false, true), markers.SurfaceLabels},
		{"metadata labels removed entirely", "test_labelled", labelled(stamped, false, false), labelled(nil, false, false), markers.SurfaceLabels},
		{"manifest labels", "test_manifest", manifested(stamped, false), manifested(plain, false), markers.SurfaceManifest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Scan([]*plans.ResourceInstanceChangeSrc{changeOfType(t, tc.typ, "a", plans.Update, tc.before, tc.after)}, schemaFor)
			if len(got) != 1 {
				t.Fatalf("got %d removals, want 1: %#v", len(got), got)
			}
			if got[0].Estate != "e1" {
				t.Errorf("estate %q, want e1", got[0].Estate)
			}
			if want := []string{"tofu-estate"}; !reflect.DeepEqual(got[0].Keys, want) {
				t.Errorf("keys %v, want %v", got[0].Keys, want)
			}
			if got[0].Surface != tc.surface {
				t.Errorf("surface %q, want %q", got[0].Surface, tc.surface)
			}
		})
	}
}

// TestScan_anUnknownLabelMapIsNotARemoval is the case the wholly-known check
// exists for. markers.LabelsOf reads an unknown labels map as an empty one,
// ok true, which would make every stamped object whose planned labels are
// unknown read as a marker removal it is not.
func TestScan_anUnknownLabelMapIsNotARemoval(t *testing.T) {
	stamped := map[string]string{"team": "a", "tofu-estate": "e1"}
	cases := []struct {
		name   string
		typ    string
		before cty.Value
		after  cty.Value
	}{
		{"metadata labels unknown", "test_labelled", labelled(stamped, false, false), labelled(nil, true, false)},
		{"manifest labels unknown", "test_manifest", manifested(stamped, false), manifested(nil, true)},
		{"label unchanged", "test_labelled", labelled(stamped, false, false), labelled(stamped, false, true)},
		{"manifest label unchanged", "test_manifest", manifested(stamped, false), manifested(stamped, false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Scan([]*plans.ResourceInstanceChangeSrc{changeOfType(t, tc.typ, "a", plans.Update, tc.before, tc.after)}, schemaFor)
			if len(got) != 0 {
				t.Fatalf("got %#v, want no removal", got)
			}
		})
	}
}

// TestScanCreates_readsTheLabelSurfaces is #716's warning on the Kubernetes
// shapes: a stock-mode create whose configured labels already carry
// tofu-estate.
func TestScanCreates_readsTheLabelSurfaces(t *testing.T) {
	stamped := map[string]string{"tofu-estate": "e1"}
	changes := []*plans.ResourceInstanceChangeSrc{
		changeOfType(t, "test_labelled", "a", plans.Create, cty.NullVal(labelSchema.Block.ImpliedType()), labelled(stamped, false, true)),
		changeOfType(t, "test_manifest", "b", plans.Create, cty.NullVal(manifestSchema.Block.ImpliedType()), manifested(stamped, false)),
		changeOfType(t, "test_labelled", "c", plans.Create, cty.NullVal(labelSchema.Block.ImpliedType()), labelled(nil, true, false)),
	}
	got := ScanCreates(changes, schemaFor)
	var addrs []string
	for _, c := range got {
		addrs = append(addrs, c.Addr.String()+"="+c.Estate)
	}
	if want := []string{"test_labelled.a=e1", "test_manifest.b=e1"}; !reflect.DeepEqual(addrs, want) {
		t.Errorf("creations %v, want %v", addrs, want)
	}
}
