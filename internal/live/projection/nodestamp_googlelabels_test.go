// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1119: the GCP half of the node stamp. The schema is the
// KeyValueLabels shape hashicorp/google 8.6.0 gives a labelled type: a
// settable labels map, with terraform_labels and effective_labels computed
// beside it.

func googleBucketSchema() providers.Schema {
	m := cty.Map(cty.String)
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Required: true},
		"labels":           {Type: m, Optional: true},
		"terraform_labels": {Type: m, Computed: true},
		"effective_labels": {Type: m, Computed: true},
	}}}
}

func googleBucketConfig(labels cty.Value) cty.Value {
	m := cty.Map(cty.String)
	return cty.ObjectVal(map[string]cty.Value{
		"name":             cty.StringVal("logs"),
		"labels":           labels,
		"terraform_labels": cty.NullVal(m),
		"effective_labels": cty.NullVal(m),
	})
}

func googleBucketAddr(key addrs.InstanceKey) addrs.AbsResourceInstance {
	return addrs.AbsResourceInstance{Resource: addrs.Resource{
		Mode: addrs.ManagedResourceMode, Type: "google_storage_bucket", Name: "logs",
	}.Instance(key)}
}

func stampedLabels(t *testing.T, got cty.Value) map[string]string {
	t.Helper()
	out := map[string]string{}
	for k, v := range got.GetAttr("labels").AsValueMap() {
		out[k] = v.AsString()
	}
	return out
}

func TestNodeResolver_AdjustConfigValue_googleLabels(t *testing.T) {
	addr := googleBucketAddr(addrs.IntKey(1))
	resolver := &NodeResolver{
		Estate: "prod-net",
		Slots:  map[string]string{markers.EscapeAddress(addr.String()): "7"},
	}
	own := cty.MapVal(map[string]cty.Value{"team": cty.StringVal("net")})
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, googleBucketConfig(own), googleBucketSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	labels := stampedLabels(t, got)
	want := map[string]string{
		"team":             "net",
		markers.TagEstate:  "prod-net",
		markers.TagAddress: markers.GoogleAddressValue(addr),
		markers.TagSlot:    "7",
	}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("labels[%q] = %q, want %q", k, labels[k], v)
		}
	}
}

func TestNodeResolver_AdjustConfigValue_googleLabelsConflict(t *testing.T) {
	resolver := &NodeResolver{Estate: "prod-net"}
	own := cty.MapVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("other")})
	_, diags := resolver.AdjustConfigValue(context.Background(), googleBucketAddr(addrs.NoKey), googleBucketConfig(own), googleBucketSchema())
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), SummaryMarkerConflict) {
		t.Fatalf("diags = %v, want %q", diags.Err(), SummaryMarkerConflict)
	}
}

func TestNodeResolver_AdjustConfigValue_googleEstateNotALabel(t *testing.T) {
	resolver := &NodeResolver{Estate: "e-" + strings.Repeat("a", 70)}
	_, diags := resolver.AdjustConfigValue(context.Background(), googleBucketAddr(addrs.NoKey), googleBucketConfig(cty.NullVal(cty.Map(cty.String))), googleBucketSchema())
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), SummaryMarkerNotALabel) {
		t.Fatalf("diags = %v, want %q", diags.Err(), SummaryMarkerNotALabel)
	}
}

func gStrMap(kv ...string) cty.Value {
	if len(kv) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	m := map[string]cty.Value{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = cty.StringVal(kv[i+1])
	}
	return cty.MapVal(m)
}

func googleObject(labels, terraformLabels, effective cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"name":             cty.StringVal("logs"),
		"labels":           labels,
		"terraform_labels": terraformLabels,
		"effective_labels": effective,
	})
}

func googleDefaults(attribution bool, keys ...string) cty.Value {
	set := cty.SetValEmpty(cty.String)
	if len(keys) > 0 {
		var vs []cty.Value
		for _, k := range keys {
			vs = append(vs, cty.StringVal(k))
		}
		set = cty.SetVal(vs)
	}
	return cty.ObjectVal(map[string]cty.Value{"keys": set, "attribution": cty.BoolVal(attribution)})
}

func TestMirrorGoogleMarkerLabels(t *testing.T) {
	addr := markers.GoogleAddressValue(googleBucketAddr(addrs.NoKey))
	effective := gStrMap("team", "net", "env", "prod", markers.TagEstate, "prod-net", markers.TagAddress, addr,
		googleAttributionKey, "true", "goog-other", "x")
	cacheLess := googleObject(gStrMap("team", "net"), gStrMap(), effective)

	got := mirrorGoogleMarkerLabels(cacheLess, googleBucketSchema().Block, googleDefaults(true, "env"))
	labels := got.GetAttr("labels").AsValueMap()
	if labels[markers.TagEstate].AsString() != "prod-net" || labels[markers.TagAddress].AsString() != addr || labels["team"].AsString() != "net" {
		t.Errorf("labels = %#v, want team plus the two markers", got.GetAttr("labels"))
	}
	if _, ok := labels["goog-other"]; ok {
		t.Error("a label another client set was put into labels")
	}
	wantTL := gStrMap("team", "net", "env", "prod", markers.TagEstate, "prod-net", markers.TagAddress, addr, googleAttributionKey, "true")
	if !got.GetAttr("terraform_labels").RawEquals(wantTL) {
		t.Errorf("terraform_labels = %#v\nwant %#v", got.GetAttr("terraform_labels"), wantTL)
	}

	// No defaults seeded: terraform_labels is left as the read returned it.
	unseeded := mirrorGoogleMarkerLabels(cacheLess, googleBucketSchema().Block, cty.NilVal)
	if !unseeded.GetAttr("terraform_labels").RawEquals(gStrMap()) {
		t.Errorf("terraform_labels rebuilt with no defaults: %#v", unseeded.GetAttr("terraform_labels"))
	}
}

// TestMirrorGoogleMarkerLabelsMatchesStockState: what a cache-less read
// becomes after the mirror equals what a stock state file's refresh holds,
// so the plan built from each is the same plan.
func TestMirrorGoogleMarkerLabelsMatchesStockState(t *testing.T) {
	addr := markers.GoogleAddressValue(googleBucketAddr(addrs.NoKey))
	effective := gStrMap("team", "net", "env", "prod", markers.TagEstate, "prod-net", markers.TagAddress, addr, googleAttributionKey, "true")
	// A stock apply records labels as configured (stamped) and
	// terraform_labels as labels plus default_labels plus attribution.
	withState := googleObject(
		gStrMap("team", "net", markers.TagEstate, "prod-net", markers.TagAddress, addr),
		gStrMap("team", "net", "env", "prod", markers.TagEstate, "prod-net", markers.TagAddress, addr, googleAttributionKey, "true"),
		effective)
	// A read on a prior that named no keys keeps the configured labels the
	// seed carried (team) and nothing in terraform_labels.
	cacheLess := googleObject(gStrMap("team", "net"), gStrMap(), effective)

	defaults := googleDefaults(true, "env")
	got := mirrorGoogleMarkerLabels(cacheLess, googleBucketSchema().Block, defaults)
	if !got.RawEquals(withState) {
		t.Errorf("cache-less read after the mirror:\n got %#v\nwant %#v", got, withState)
	}
	if again := mirrorGoogleMarkerLabels(withState, googleBucketSchema().Block, defaults); !again.RawEquals(withState) {
		t.Errorf("the mirror changed a stock state's object:\n got %#v", again)
	}
}

func TestBuilderGoogleLabelDefaults(t *testing.T) {
	providerAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("google")}
	load := func(t *testing.T, src string) *builder {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return &builder{cfg: loadConfig(t, dir)}
	}

	b := load(t, `
provider "google" {
  default_labels = { env = "prod", team = "net" }
  add_terraform_attribution_label = false
}
resource "google_storage_bucket" "logs" { name = "logs" }
`)
	got, ok := b.googleLabelDefaults(context.Background(), providerAddr)
	if !ok || !got.RawEquals(googleDefaults(false, "env", "team")) {
		t.Errorf("literal block = %#v, %v; want env and team, attribution off", got, ok)
	}

	b = load(t, `
variable "labels" { type = map(string) }
provider "google" { default_labels = var.labels }
resource "google_storage_bucket" "logs" { name = "logs" }
`)
	if got, ok := b.googleLabelDefaults(context.Background(), providerAddr); ok {
		t.Errorf("a variable with no value read as known: %#v", got)
	}

	b = load(t, `resource "google_storage_bucket" "logs" { name = "logs" }`)
	got, ok = b.googleLabelDefaults(context.Background(), providerAddr)
	if !ok || !got.RawEquals(googleDefaults(true)) {
		t.Errorf("no provider block = %#v, %v; want no keys, attribution on", got, ok)
	}
}

func TestPropagatedMarkerDiag(t *testing.T) {
	m := cty.Map(cty.String)
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Optional: true},
		"labels":           {Type: m, Optional: true, Description: "A set of key/value label pairs to assign to instances created from this template."},
		"terraform_labels": {Type: m, Computed: true},
		"effective_labels": {Type: m, Computed: true},
	}}}
	addr := addrs.AbsResourceInstance{Resource: addrs.Resource{
		Mode: addrs.ManagedResourceMode, Type: "google_compute_instance_template", Name: "web",
	}.Instance(addrs.NoKey)}
	config := func(labels cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("web"), "labels": labels, "terraform_labels": cty.NullVal(m), "effective_labels": cty.NullVal(m)})
	}
	diags := propagatedMarkerDiag(addr, config(gStrMap(markers.TagEstate, "prod-net")), schema)
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), SummaryMarkerConflict) {
		t.Errorf("tofu-estate in a template's labels: %v, want %q", diags.Err(), SummaryMarkerConflict)
	}
	if diags := propagatedMarkerDiag(addr, config(gStrMap("team", "net")), schema); len(diags) != 0 {
		t.Errorf("an ordinary label was refused: %v", diags.Err())
	}
	// Through the stamp itself: the template is not a surface, so nothing
	// is written and the hand-written marker is refused.
	resolver := &NodeResolver{Estate: "prod-net"}
	if _, diags := resolver.AdjustConfigValue(context.Background(), addr, config(gStrMap(markers.TagEstate, "prod-net")), schema); !diags.HasErrors() {
		t.Error("AdjustConfigValue accepted a marker in a template's labels")
	}
}

func TestCarrierMarkersGoogle(t *testing.T) {
	obj := googleObject(
		gStrMap(markers.TagEstate, "mine"),
		gStrMap(markers.TagEstate, "default", "team", "net"),
		gStrMap(markers.TagEstate, "live", "goog-x", "y"))
	got, ok := carrierMarkers("google", obj, googleBucketSchema())
	if !ok || got[markers.TagEstate] != "mine" || got["team"] != "net" || got["goog-x"] != "y" {
		t.Errorf("carrierMarkers = %v, %v", got, ok)
	}
	marked := googleObject(gStrMap(markers.TagEstate, "mine").Mark(marks.Sensitive), gStrMap(), gStrMap())
	if got, ok := carrierMarkers("google", marked, googleBucketSchema()); ok {
		t.Errorf("a marked labels map was read: %v", got)
	}
}
