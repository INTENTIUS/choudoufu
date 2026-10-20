// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// TestGoogleAddressValueVectors pins the published test vectors of
// live/MARKERS.md's GCP address label. They were computed outside Go (a
// Python sha256 and base32 over the same strings), so a change to the
// encoding fails here before it reaches a live object.
func TestGoogleAddressValueVectors(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{`google_compute_network.main`, "pbto3ksmyaajifkcwdi2kvn7ls-pbto3ksmyaajifkcwdi2kvn7ls"},
		{`google_storage_bucket.logs["eu"]`, "woy7j4jfjdy7ijvlye5awlh43u-6qxg2rx7xztmivjddbzzxh2tul"},
		{`module.net["a"].google_compute_subnetwork.this[2]`, "kmnho6kggxe3eeoegodokage4s-ztdls6xmj5l4i2t2scn3mq3ic7"},
	} {
		addr, diags := addrs.ParseAbsResourceInstanceStr(tc.addr)
		if diags.HasErrors() {
			t.Fatalf("parse %s: %s", tc.addr, diags.Err())
		}
		got := GoogleAddressValue(addr)
		if got != tc.want {
			t.Errorf("GoogleAddressValue(%s) = %q, want %q", tc.addr, got, tc.want)
		}
		if len(got) != GoogleAddressLen || !ValidGoogleAddressValue(got) || !ValidGoogleLabelValue(got) {
			t.Errorf("GoogleAddressValue(%s) = %q is not a legal address label value", tc.addr, got)
		}
		block, ok := GoogleAddressBlock(got)
		if !ok || block != GoogleBlockValue(addr.ContainingResource()) {
			t.Errorf("GoogleAddressBlock(%q) = %q, %v; want the block part of %s", got, block, ok, addr.ContainingResource())
		}
	}
}

// TestGoogleAddressBlockIgnoresTheInstanceKey: every instance of one
// block shares the block part, and differs in the instance part.
func TestGoogleAddressBlockIgnoresTheInstanceKey(t *testing.T) {
	a := mustInstance(t, `google_compute_address.ip[0]`)
	b := mustInstance(t, `google_compute_address.ip[7]`)
	va, vb := GoogleAddressValue(a), GoogleAddressValue(b)
	if va[:GoogleAddressPartLen] != vb[:GoogleAddressPartLen] {
		t.Errorf("block parts differ: %q, %q", va, vb)
	}
	if va == vb {
		t.Errorf("two instances encode to one value %q", va)
	}
	other := mustInstance(t, `module.m["k"].google_compute_address.ip[0]`)
	if GoogleAddressValue(other)[:GoogleAddressPartLen] == va[:GoogleAddressPartLen] {
		t.Error("a module key does not change the block part")
	}
}

func mustInstance(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("parse %s: %s", s, diags.Err())
	}
	return addr
}

func TestValidGoogleLabelValue(t *testing.T) {
	for s, want := range map[string]bool{
		"prod-networking":         true,
		"a_b":                     true,
		"":                        false,
		"Prod":                    false,
		"a.b":                     false,
		strings.Repeat("a", 63):   true,
		strings.Repeat("a", 64):   false,
		"module.x:a.google_thing": false,
	} {
		if got := ValidGoogleLabelValue(s); got != want {
			t.Errorf("ValidGoogleLabelValue(%q) = %v, want %v", s, got, want)
		}
	}
	if ValidGoogleAddressValue("pbto3ksmyaajifkcwdi2kvn7ls") || ValidGoogleAddressValue(strings.Repeat("a", 26)+"-"+strings.Repeat("1", 26)) {
		t.Error("ValidGoogleAddressValue accepted a value outside its grammar")
	}
}

// googleBlock is the KeyValueLabels shape: a settable labels map with the
// computed terraform_labels and effective_labels beside it.
func googleBlock() *configschema.Block {
	m := cty.Map(cty.String)
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Required: true},
		"labels":           {Type: m, Optional: true},
		"terraform_labels": {Type: m, Computed: true},
		"effective_labels": {Type: m, Computed: true},
	}}
}

func TestGoogleLabelSurface(t *testing.T) {
	if _, ok := GoogleLabelSurface(googleBlock()); !ok {
		t.Error("the KeyValueLabels shape is not the google-labels surface")
	}
	noEffective := googleBlock()
	delete(noEffective.Attributes, "effective_labels")
	if _, ok := GoogleLabelSurface(noEffective); ok {
		t.Error("a labels map with no effective_labels beside it is the surface (google_monitoring_notification_channel's shape)")
	}
	computedOnly := googleBlock()
	computedOnly.Attributes["labels"] = &configschema.Attribute{Type: cty.Map(cty.String), Computed: true}
	if _, ok := GoogleLabelSurface(computedOnly); ok {
		t.Error("a computed-only labels map is the surface")
	}
	if Taggable(googleBlock()) {
		t.Error("the google-labels shape is also the AWS tag surface")
	}
	if _, ok := LabelSurface(googleBlock()); ok {
		t.Error("the google-labels shape is also the Kubernetes label surface")
	}
}

// TestGoogleLabelsOfPrecedence: effective_labels, then terraform_labels,
// then labels, the explicit map winning.
func TestGoogleLabelsOfPrecedence(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"labels":           cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("mine")}),
		"terraform_labels": cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("default"), "team": cty.StringVal("net")}),
		"effective_labels": cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("live"), "goog-x": cty.StringVal("y")}),
	})
	got, ok := GoogleLabelsOf(obj)
	if !ok || got["tofu-estate"] != "mine" || got["team"] != "net" || got["goog-x"] != "y" {
		t.Errorf("GoogleLabelsOf = %v, %v", got, ok)
	}
	if _, ok := GoogleLabelsOf(cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("n")})); ok {
		t.Error("an object with no labels maps read as labelled")
	}
	nullLabels := cty.ObjectVal(map[string]cty.Value{"labels": cty.NullVal(cty.Map(cty.String))})
	if got, ok := GoogleLabelsOf(nullLabels); !ok || len(got) != 0 {
		t.Errorf("null labels = %v, %v; want an empty answer that is still an answer", got, ok)
	}
}
