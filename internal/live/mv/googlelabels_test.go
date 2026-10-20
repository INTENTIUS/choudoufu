// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
)

// live-mv's GCP answer (GitHub issue #1119; googlelabels.go). The fake
// provider below reuses the label tests' cluster ([labelTestCluster]) with a
// hashicorp/google schema, and recomputes terraform_labels and
// effective_labels from labels at plan time the way the provider's
// CustomizeDiff does, so the read-back the verify makes is the provider's
// shape rather than a copy of the request.
//
// The live object starts with a labels map that names none of its markers
// and an effective_labels map that does: hashicorp/google's read keeps only
// the keys the prior names, so that is what an imported object looks like,
// and every case here goes through markers.WithGoogleMarkersMirrored.
//
// Proving them red: drop the relabelsGoogle arm from locateByIdentity and
// every case reads the object through the tag path and fails with
// "Resource type with no tags"; drop the relabelsGoogle arm from checkPlan
// and the rename and the move are refused, the labels write itself read
// as a change outside the tags.

const (
	googleTestType   = "google_storage_bucket"
	googleTestLiveID = "logs-bucket"
)

var googleTestProviderAddr = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("google"),
}

func googleTestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":                              {Type: cty.String, Computed: true},
			"name":                            {Type: cty.String, Required: true},
			"location":                        {Type: cty.String, Required: true},
			markers.GoogleLabelsAttr:          {Type: cty.Map(cty.String), Optional: true},
			markers.GoogleTerraformLabelsAttr: {Type: cty.Map(cty.String), Computed: true},
			markers.GoogleEffectiveLabelsAttr: {Type: cty.Map(cty.String), Computed: true},
		},
	}}
}

// googleTestObject is the live bucket as an import reads it: own is the
// labels map the read kept, effective every label GCP holds.
func googleTestObject(own, effective map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":                              cty.StringVal(googleTestLiveID),
		"name":                            cty.StringVal(googleTestLiveID),
		"location":                        cty.StringVal("EU"),
		markers.GoogleLabelsAttr:          labelTestLabels(own),
		markers.GoogleTerraformLabelsAttr: labelTestLabels(own),
		markers.GoogleEffectiveLabelsAttr: labelTestLabels(effective),
	})
}

// googleTestMarked is googleTestObject for a bucket carrying the given
// markers in effective_labels beside one label of its own and one GCP set.
func googleTestMarked(estate, address string) cty.Value {
	eff := map[string]string{"app": "web", "goog-managed": "x", markers.TagSlot: "0"}
	if estate != "" {
		eff[markers.TagEstate] = estate
	}
	if address != "" {
		eff[markers.TagAddress] = address
	}
	return googleTestObject(map[string]string{"app": "web"}, eff)
}

func googleTestConfig(t *testing.T, blockName string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "`+googleTestType+`" "`+blockName+`" {
  provider = google
  name     = "`+googleTestLiveID+`"
  location = "EU"
}
`)
	return loadConfigDir(t, dir)
}

// newGoogleTestCloud is [newLabelTestCluster] serving the google schema,
// with the provider's label-map recompute run before any extra hook.
func newGoogleTestCloud(t *testing.T, object cty.Value, extra func(cty.Value) cty.Value) *labelTestCluster {
	t.Helper()
	c := newLabelTestCluster(t, googleTestSchema(), object)
	c.GetProviderSchemaResponse.ResourceTypes = map[string]providers.Schema{googleTestType: googleTestSchema()}
	prior := object
	c.planHook = func(v cty.Value) cty.Value {
		attrs := v.AsValueMap()
		own := attrs[markers.GoogleLabelsAttr]
		eff := map[string]cty.Value{}
		for k, e := range prior.GetAttr(markers.GoogleEffectiveLabelsAttr).AsValueMap() {
			eff[k] = e
		}
		if !own.IsNull() {
			for k, e := range own.AsValueMap() {
				eff[k] = e
			}
		}
		attrs[markers.GoogleTerraformLabelsAttr] = own
		attrs[markers.GoogleEffectiveLabelsAttr] = cty.MapVal(eff)
		out := cty.ObjectVal(attrs)
		if extra != nil {
			out = extra(out)
		}
		return out
	}
	return c
}

func googleTestRequest(cloud *labelTestCluster, cfg *configs.Config, old, new addrs.AbsResourceInstance, from, to string) Request {
	return Request{
		Estate:     to,
		FromEstate: from,
		Old:        old,
		New:        new,
		Config:     cfg,
		Resolutions: []identity.Resolution{{
			Addr:     new,
			Class:    identity.ClassConcrete,
			ImportID: googleTestLiveID,
		}},
		Providers: projection.SingleProvider(googleTestProviderAddr, cloud),
	}
}

func googleTestLabels(t *testing.T, c *labelTestCluster) map[string]string {
	t.Helper()
	got, ok := markers.GoogleLabelsOf(c.object)
	if !ok {
		t.Fatal("the live object has no readable labels")
	}
	return got
}

func TestMove_GoogleLabelsRenameWithinEstate(t *testing.T) {
	old := mustAddr(t, googleTestType+".logs")
	renamed := mustAddr(t, googleTestType+".archive")
	cloud := newGoogleTestCloud(t, googleTestMarked("app", markers.GoogleAddressValue(old)), nil)

	res, diags := Move(t.Context(), googleTestRequest(cloud, googleTestConfig(t, "archive"), old, renamed, "", "app"))
	if diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}
	if res.Surface != markers.SurfaceGoogleLabels {
		t.Errorf("Surface = %q, want %q", res.Surface, markers.SurfaceGoogleLabels)
	}
	if !res.Written || !res.Verified || res.AlreadyMarked {
		t.Errorf("Written = %v, Verified = %v, AlreadyMarked = %v; want true, true, false", res.Written, res.Verified, res.AlreadyMarked)
	}
	if res.Path != PathIdentity || res.Swept {
		t.Errorf("Path = %s, Swept = %v; want IDENTITY and no sweep", res.Path, res.Swept)
	}
	if res.OldMarker != markers.GoogleAddressValue(old) || res.NewMarker != markers.GoogleAddressValue(renamed) {
		t.Errorf("markers %q -> %q, want the two address label values", res.OldMarker, res.NewMarker)
	}
	if cloud.plans != 1 || cloud.applies != 1 {
		t.Errorf("planned %d and applied %d times, want 1 and 1", cloud.plans, cloud.applies)
	}
	got := googleTestLabels(t, cloud)
	if got[markers.TagAddress] != markers.GoogleAddressValue(renamed) || got[markers.TagEstate] != "app" {
		t.Errorf("live markers = %v, want tofu-estate=app and the new address", got)
	}
	if got[markers.TagSlot] != "0" || got["app"] != "web" || got["goog-managed"] != "x" {
		t.Errorf("a label beside the address moved: %v", got)
	}
}

func TestMove_GoogleLabelsMoveBetweenEstates(t *testing.T) {
	old := mustAddr(t, googleTestType+".logs")
	moved := mustAddr(t, googleTestType+".archive")
	cloud := newGoogleTestCloud(t, googleTestMarked("app", markers.GoogleAddressValue(old)), nil)

	res, diags := Move(t.Context(), googleTestRequest(cloud, googleTestConfig(t, "archive"), old, moved, "app", "data"))
	if diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if !res.Written || !res.Verified {
		t.Errorf("Written = %v, Verified = %v, want both true", res.Written, res.Verified)
	}
	got := googleTestLabels(t, cloud)
	if got[markers.TagEstate] != "data" || got[markers.TagAddress] != markers.GoogleAddressValue(moved) {
		t.Errorf("live markers = %v, want tofu-estate=data and the new address together", got)
	}
	if got[markers.TagSlot] != "0" {
		t.Errorf("tofu-slot moved: %v", got)
	}
}

func TestMove_GoogleLabelsAlreadyMarked(t *testing.T) {
	old := mustAddr(t, googleTestType+".logs")
	renamed := mustAddr(t, googleTestType+".archive")
	cloud := newGoogleTestCloud(t, googleTestMarked("app", markers.GoogleAddressValue(renamed)), nil)

	res, diags := Move(t.Context(), googleTestRequest(cloud, googleTestConfig(t, "archive"), old, renamed, "", "app"))
	if diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}
	if !res.AlreadyMarked || !res.Verified || res.Written {
		t.Errorf("AlreadyMarked = %v, Verified = %v, Written = %v; want true, true, false", res.AlreadyMarked, res.Verified, res.Written)
	}
	if cloud.plans != 0 || cloud.applies != 0 {
		t.Errorf("an already-marked rename planned %d and applied %d times", cloud.plans, cloud.applies)
	}
}

func TestMove_GoogleLabelsRefusals(t *testing.T) {
	old := mustAddr(t, googleTestType+".logs")
	renamed := mustAddr(t, googleTestType+".archive")
	cases := map[string]struct {
		address  string
		planHook func(cty.Value) cty.Value
		wantCode RefusalCode
		want     string
	}{
		"the object carries a third address": {
			address:  markers.GoogleAddressValue(mustAddr(t, googleTestType+".elsewhere")),
			wantCode: RefusalNothingAtOldAddress, want: "encodes neither",
		},
		"a plan that moves location": {
			address: markers.GoogleAddressValue(old),
			planHook: func(v cty.Value) cty.Value {
				m := v.AsValueMap()
				m["location"] = cty.StringVal("US")
				return cty.ObjectVal(m)
			},
			wantCode: RefusalPlanChangesMoreThanTags, want: "location",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cloud := newGoogleTestCloud(t, googleTestMarked("app", tc.address), tc.planHook)
			_, diags := Move(t.Context(), googleTestRequest(cloud, googleTestConfig(t, "archive"), old, renamed, "", "app"))
			if !diags.HasErrors() {
				t.Fatal("the rename was not refused")
			}
			if !strings.Contains(diags.Err().Error(), tc.want) {
				t.Errorf("the refusal does not say %q:\n%s", tc.want, diags.Err())
			}
			if got := RefusalFrom(diags); got != tc.wantCode {
				t.Errorf("refusal code = %q, want %q", got, tc.wantCode)
			}
			if cloud.applies != 0 {
				t.Errorf("a refused rename still wrote: %d applies", cloud.applies)
			}
			if got := googleTestLabels(t, cloud)[markers.TagAddress]; got != tc.address {
				t.Errorf("the live address label changed under a refusal: %q", got)
			}
		})
	}
}
