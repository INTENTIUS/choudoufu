// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
)

// #1120 unit 12: the Azure family's post-create write, end to end through
// the resolver, with the Tags API faked. The type names are azurerm's own
// because the answer is the generated facts' (internal/live/armtypes), not
// a roster this test could stand up.

type fakeARMTagger struct {
	ids  []string
	tags []map[string]string
	err  error
}

func (f *fakeARMTagger) MergeTags(_ context.Context, id string, tags map[string]string) error {
	f.ids = append(f.ids, id)
	f.tags = append(f.tags, tags)
	return f.err
}

func armSchema(extra ...string) providers.Schema {
	attrs := map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Computed: true},
		"name": {Type: cty.String, Required: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}
	for _, a := range extra {
		attrs[a] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
}

func armCreatedObject(attrs map[string]cty.Value, tags cty.Value) cty.Value {
	m := map[string]cty.Value{"tags": tags}
	for k, v := range attrs {
		m[k] = v
	}
	return cty.ObjectVal(m)
}

func azurermProvider() addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
}

// armResolver is a resolver over a configuration declaring the three
// azurerm blocks these tests create, so that the stamp reads their
// provider (and so their family) off the configuration as a run does.
func armResolver(t *testing.T, tagger *fakeARMTagger, writes *[]substrate.Write) *NodeResolver {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "azurerm_app_service_managed_certificate" "c" {
  custom_hostname_binding_id = "x"
}
resource "azurerm_resource_group" "rg" {
  name     = "rg"
  location = "westeurope"
}
resource "azurerm_subscription" "s" {
  subscription_name = "s"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &NodeResolver{Estate: "prod", Config: loadConfig(t, dir), MarkerWriter: func(_ addrs.AbsProviderConfig, write substrate.Write) (MarkerWriter, error) {
		*writes = append(*writes, write)
		return ARMTagsWriter{Tagger: tagger}, nil
	}}
}

// TestARMPostCreateWithholdsAndMerges: an after-create azurerm type's create
// is left unstamped, and once it returns, one Tags API merge puts the
// markers on its ARM id and the stored object carries them. An in-create
// type is stamped in its create and never reaches the writer.
func TestARMPostCreateWithholdsAndMerges(t *testing.T) {
	ctx := context.Background()
	tagger := &fakeARMTagger{}
	var writes []substrate.Write
	n := armResolver(t, tagger, &writes)
	config := armCreatedObject(map[string]cty.Value{"id": cty.NullVal(cty.String), "name": cty.StringVal("c")}, tagMap(map[string]string{"env": "prod"}))

	cert := locatedTestAddr(t, "azurerm_app_service_managed_certificate", "c")
	got, diags := n.AdjustCreateConfigValue(ctx, cert, config, armSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if tags := requireTags(t, got); len(tags) != 1 || tags["env"] != "prod" {
		t.Errorf("the after-create type's create carried %v; want its own tags alone", tags)
	}
	rg := locatedTestAddr(t, "azurerm_resource_group", "rg")
	got, diags = n.AdjustCreateConfigValue(ctx, rg, config, armSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if tags := requireTags(t, got); tags[markers.TagEstate] != "prod" || tags[markers.TagAddress] != "azurerm_resource_group.rg" {
		t.Errorf("an in-create type's create is not stamped: %v", tags)
	}

	id := "/subscriptions/0/resourceGroups/rg/providers/Microsoft.Web/certificates/c"
	applied := armCreatedObject(map[string]cty.Value{"id": cty.StringVal(id), "name": cty.StringVal("c")}, tagMap(map[string]string{"env": "prod"}))
	stored, diags := n.WriteAppliedMarkers(ctx, cert, azurermProvider(), plans.Create, applied, armSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if len(tagger.ids) != 1 || tagger.ids[0] != id || len(writes) != 1 || writes[0] != substrate.WriteARMTagsAPI {
		t.Fatalf("merges %v through %v; want one onto %s through %s", tagger.ids, writes, id, substrate.WriteARMTagsAPI)
	}
	want := map[string]string{markers.TagEstate: "prod", markers.TagAddress: "azurerm_app_service_managed_certificate.c"}
	if len(tagger.tags[0]) != len(want) || tagger.tags[0][markers.TagEstate] != "prod" || tagger.tags[0][markers.TagAddress] != want[markers.TagAddress] {
		t.Errorf("merged %v, want %v", tagger.tags[0], want)
	}
	if tags := requireTags(t, stored); tags["env"] != "prod" || tags[markers.TagEstate] != "prod" || tags[markers.TagAddress] != want[markers.TagAddress] {
		t.Errorf("stored tags %v: the state would contradict the object", tags)
	}

	_, _ = n.WriteAppliedMarkers(ctx, rg, azurermProvider(), plans.Create, applied, armSchema())
	_, _ = n.WriteAppliedMarkers(ctx, cert, azurermProvider(), plans.Update, applied, armSchema())
	if len(tagger.ids) != 1 {
		t.Errorf("a write was made where none was due: %v", tagger.ids)
	}
}

// TestARMPostCreateTagsTheSubscription: azurerm_subscription's id is its
// Microsoft.Subscription alias, which holds no tags; the merge goes to the
// subscription, as azurerm's own tag write does.
func TestARMPostCreateTagsTheSubscription(t *testing.T) {
	tagger := &fakeARMTagger{}
	var writes []substrate.Write
	n := armResolver(t, tagger, &writes)
	sub := locatedTestAddr(t, "azurerm_subscription", "s")
	applied := armCreatedObject(map[string]cty.Value{
		"id":              cty.StringVal("/providers/Microsoft.Subscription/aliases/s"),
		"name":            cty.StringVal("s"),
		"subscription_id": cty.StringVal("1111"),
	}, cty.NullVal(cty.Map(cty.String)))
	if _, diags := n.WriteAppliedMarkers(context.Background(), sub, azurermProvider(), plans.Create, applied, armSchema("subscription_id")); diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if len(tagger.ids) != 1 || tagger.ids[0] != "/subscriptions/1111" {
		t.Errorf("merged onto %v, want /subscriptions/1111", tagger.ids)
	}
}

// TestARMPostCreateFailureNamesTheFix: a refused merge (the boundary
// template's deny, say) fails the apply naming the object, the generated
// fact behind the withholding, and the az command that marks it.
func TestARMPostCreateFailureNamesTheFix(t *testing.T) {
	tagger := &fakeARMTagger{err: errors.New("the Tags API answered 403 RequestDisallowedByPolicy")}
	var writes []substrate.Write
	n := armResolver(t, tagger, &writes)
	cert := locatedTestAddr(t, "azurerm_app_service_managed_certificate", "c")
	id := "/subscriptions/0/resourceGroups/rg/providers/Microsoft.Web/certificates/c"
	applied := armCreatedObject(map[string]cty.Value{"id": cty.StringVal(id), "name": cty.StringVal("c")}, cty.NullVal(cty.Map(cty.String)))
	_, diags := n.WriteAppliedMarkers(context.Background(), cert, azurermProvider(), plans.Create, applied, armSchema())
	if !diags.HasErrors() {
		t.Fatal("a refused merge did not fail the apply")
	}
	d := diags[0].Description()
	for _, want := range []string{
		"[id=" + id + "]",
		"RequestDisallowedByPolicy",
		"azurerm_app_service_managed_certificate does not send tags in its create call (client.CreateOrUpdate) and writes them afterwards with client.Update",
		"az tag update --resource-id " + id + " --operation Merge --tags",
	} {
		if !strings.Contains(d.Detail, want) {
			t.Errorf("detail does not carry %q:\n%s", want, d.Detail)
		}
	}
	if d.Summary != SummaryMarkerNotWritten {
		t.Errorf("summary %q", d.Summary)
	}

	if err := (ARMTagsWriter{Tagger: tagger}).WriteMarkers(context.Background(), CreatedInstance{Object: cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("x")})}, nil); err == nil {
		t.Error("an object with no ARM id was written to")
	}
}

// TestARMPostCreateConvergesOnTheNextPlan is the convergence half of the
// after-create seam, for the one azurerm type whose create PUT refuses tags
// at the pin: the object core stores once the Tags API merge has landed is
// exactly what the next plan's stamped configuration asks for, so that plan
// proposes no change to tags - in particular, never the removal of a
// marker the merge wrote. It is the AWS path's #1316 convergence, through
// the same two seams (AdjustCreateConfigValue withholds, WriteAppliedMarkers
// stores what the write left on the object), plus ARM's case rule: a
// configuration's own Tofu-Estate is the marker, the Merge updates it in
// place, and the stored map holds one key for it, the spelling the next
// stamp keeps.
func TestARMPostCreateConvergesOnTheNextPlan(t *testing.T) {
	const id = "/subscriptions/0/resourceGroups/rg/providers/Microsoft.Web/certificates/c"
	const address = "azurerm_app_service_managed_certificate.c"
	for name, tc := range map[string]struct {
		// own is the configuration's own tags.
		own map[string]string
		// stored is what the stored object's tags must be after the write.
		stored map[string]string
	}{
		"the operator's tags and the markers": {
			own:    map[string]string{"env": "prod", "owner": "team-a"},
			stored: map[string]string{"env": "prod", "owner": "team-a", markers.TagEstate: "prod", markers.TagAddress: address},
		},
		"no tags of its own": {
			stored: map[string]string{markers.TagEstate: "prod", markers.TagAddress: address},
		},
		"the configuration spells the estate marker itself": {
			own:    map[string]string{"env": "prod", "Tofu-Estate": "prod"},
			stored: map[string]string{"env": "prod", "Tofu-Estate": "prod", markers.TagAddress: address},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tagger := &fakeARMTagger{}
			var writes []substrate.Write
			n := armResolver(t, tagger, &writes)
			cert := locatedTestAddr(t, "azurerm_app_service_managed_certificate", "c")
			own := cty.NullVal(cty.Map(cty.String))
			if tc.own != nil {
				own = tagMap(tc.own)
			}
			config := armCreatedObject(map[string]cty.Value{"id": cty.NullVal(cty.String), "name": cty.StringVal("c")}, own)

			// The create: unstamped, so the provider returns the object
			// with the configuration's own tags and no marker.
			sent, diags := n.AdjustCreateConfigValue(ctx, cert, config, armSchema())
			if diags.HasErrors() {
				t.Fatal(diags.Err())
			}
			applied := armCreatedObject(map[string]cty.Value{"id": cty.StringVal(id), "name": cty.StringVal("c")}, sent.GetAttr("tags"))
			stored, diags := n.WriteAppliedMarkers(ctx, cert, azurermProvider(), plans.Create, applied, armSchema())
			if diags.HasErrors() {
				t.Fatal(diags.Err())
			}
			if len(tagger.ids) != 1 {
				t.Fatalf("merges %v, want one", tagger.ids)
			}
			storedTags := requireTags(t, stored)
			if !mapsEqual(storedTags, tc.stored) {
				t.Errorf("stored tags %v, want %v", storedTags, tc.stored)
			}

			// The next plan: the same configuration, not a create, is
			// stamped in full, and must ask for exactly what is stored.
			planned, diags := n.AdjustConfigValue(ctx, cert, config, armSchema())
			if diags.HasErrors() {
				t.Fatal(diags.Err())
			}
			plannedTags := requireTags(t, planned)
			if !mapsEqual(plannedTags, storedTags) {
				t.Errorf("the next plan does not converge: it asks for tags %v over the stored %v", plannedTags, storedTags)
			}
			for k := range storedTags {
				if _, ok := plannedTags[k]; !ok {
					t.Errorf("the next plan would remove %q, which the post-create write put there", k)
				}
			}
		})
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
