// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/gcpsweep"
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
// as a change outside the tags; drop findGoogle's checkDestinationFree and
// the claimed-destination and search-unavailable cases write; make
// isGoogleSelf answer false and the already-marked case refuses the object
// as its own claimant.

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
		Providers:   projection.SingleProvider(googleTestProviderAddr, cloud),
		AssetSearch: &googleTestSearch{project: "p1"},
	}
}

// googleTestSearch stands in for Cloud Asset Inventory: assets is the
// index, searched by both labels the way gcpsweep.Client.SearchAddress
// filters; buildErr fails building the client and err every search.
type googleTestSearch struct {
	project  string
	assets   []gcpsweep.Asset
	buildErr error
	err      error
	calls    []string
}

func (s *googleTestSearch) AssetSearcher(context.Context, addrs.AbsProviderConfig) (gcpsweep.AddressSearcher, string, error) {
	if s.buildErr != nil {
		return nil, "", s.buildErr
	}
	return s, s.project, nil
}

func (s *googleTestSearch) SearchAddress(_ context.Context, project, estate, address string) ([]gcpsweep.Asset, error) {
	s.calls = append(s.calls, project+" "+gcpsweep.AddressQuery(estate, address))
	if s.err != nil {
		return nil, s.err
	}
	var out []gcpsweep.Asset
	for _, a := range s.assets {
		if a.Labels[markers.TagEstate] == estate && a.Labels[markers.TagAddress] == address {
			out = append(out, a)
		}
	}
	return out, nil
}

// googleTestAsset is a bucket in the index carrying estate and address.
func googleTestAsset(bucket, estate, address string) gcpsweep.Asset {
	return gcpsweep.Asset{
		AssetType: "storage.googleapis.com/Bucket",
		Name:      "//storage.googleapis.com/projects/_/buckets/" + bucket,
		Project:   "projects/111111111111",
		Labels:    map[string]string{markers.TagEstate: estate, markers.TagAddress: address},
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

	req := googleTestRequest(cloud, googleTestConfig(t, "archive"), old, renamed, "", "app")
	search := req.AssetSearch.(*googleTestSearch)
	res, diags := Move(t.Context(), req)
	if diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}
	if want := "p1 " + gcpsweep.AddressQuery("app", markers.GoogleAddressValue(renamed)); len(search.calls) != 1 || search.calls[0] != want {
		t.Errorf("destination searches %q, want one: %q", search.calls, want)
	}
	if res.Surface != markers.SurfaceGoogleLabels {
		t.Errorf("Surface = %q, want %q", res.Surface, markers.SurfaceGoogleLabels)
	}
	if !res.Written || !res.Verified || res.AlreadyMarked {
		t.Errorf("Written = %v, Verified = %v, AlreadyMarked = %v; want true, true, false", res.Written, res.Verified, res.AlreadyMarked)
	}
	if res.Path != PathIdentity || !res.Swept {
		t.Errorf("Path = %s, Swept = %v; want IDENTITY and the destination searched", res.Path, res.Swept)
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

	// The source estate's own copy of the address is no claim on the
	// destination: only tofu-estate=data is searched.
	req := googleTestRequest(cloud, googleTestConfig(t, "archive"), old, moved, "app", "data")
	search := req.AssetSearch.(*googleTestSearch)
	search.assets = []gcpsweep.Asset{googleTestAsset("other-bucket", "app", markers.GoogleAddressValue(moved))}
	res, diags := Move(t.Context(), req)
	if diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if want := "p1 " + gcpsweep.AddressQuery("data", markers.GoogleAddressValue(moved)); len(search.calls) != 1 || search.calls[0] != want {
		t.Errorf("destination searches %q, want one: %q", search.calls, want)
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

	// The index lists the object itself under the new address; it is the
	// object being moved, and no claim against it.
	req := googleTestRequest(cloud, googleTestConfig(t, "archive"), old, renamed, "", "app")
	req.AssetSearch.(*googleTestSearch).assets = []gcpsweep.Asset{googleTestAsset(googleTestLiveID, "app", markers.GoogleAddressValue(renamed))}
	res, diags := Move(t.Context(), req)
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

func TestMove_GoogleLabelsDestinationClaimed(t *testing.T) {
	old := mustAddr(t, googleTestType+".logs")
	renamed := mustAddr(t, googleTestType+".archive")
	cloud := newGoogleTestCloud(t, googleTestMarked("app", markers.GoogleAddressValue(old)), nil)

	req := googleTestRequest(cloud, googleTestConfig(t, "archive"), old, renamed, "", "app")
	req.AssetSearch.(*googleTestSearch).assets = []gcpsweep.Asset{googleTestAsset("other-bucket", "app", markers.GoogleAddressValue(renamed))}
	_, diags := Move(t.Context(), req)
	if got := RefusalFrom(diags); got != RefusalNewAddressClaimed {
		t.Fatalf("refusal code = %q, want %q: %v", got, RefusalNewAddressClaimed, diags.Err())
	}
	if !strings.Contains(diags.Err().Error(), "other-bucket") {
		t.Errorf("the refusal does not name the other claimant:\n%s", diags.Err())
	}
	if cloud.plans != 0 || cloud.applies != 0 {
		t.Errorf("a claimed destination still planned %d and applied %d times", cloud.plans, cloud.applies)
	}
}

func TestMove_GoogleLabelsDestinationSearchUnavailable(t *testing.T) {
	old := mustAddr(t, googleTestType+".logs")
	renamed := mustAddr(t, googleTestType+".archive")
	cases := map[string]struct {
		search *googleTestSearch
		want   string
	}{
		"no client": {
			search: &googleTestSearch{project: "p1", buildErr: errors.New("no credentials found")},
			want:   "no credentials found",
		},
		"the API is disabled": {
			search: &googleTestSearch{project: "p1", err: &gcpsweep.ServiceDisabledError{Project: "p1", Service: gcpsweep.Service, Err: errors.New("403")}},
			want:   "is not enabled there",
		},
		"permission denied": {
			search: &googleTestSearch{project: "p1", err: &gcpsweep.PermissionDeniedError{Project: "p1", Permission: gcpsweep.Permission, Err: errors.New("403")}},
			want:   gcpsweep.Permission,
		},
		"a partial page": {
			search: &googleTestSearch{project: "p1", err: &gcpsweep.PartialError{Project: "p1", PagesRead: 1, Err: errors.New("503")}},
			want:   "a page after the first failed",
		},
		"no project": {
			search: &googleTestSearch{},
			want:   "no GCP project is known",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cloud := newGoogleTestCloud(t, googleTestMarked("app", markers.GoogleAddressValue(old)), nil)
			req := googleTestRequest(cloud, googleTestConfig(t, "archive"), old, renamed, "", "app")
			req.AssetSearch = tc.search
			_, diags := Move(t.Context(), req)
			if !diags.HasErrors() {
				t.Fatal("the rename went ahead without its destination search")
			}
			err := diags.Err().Error()
			if !strings.Contains(err, SummaryGCPDestinationSearchFailed) || !strings.Contains(err, tc.want) {
				t.Errorf("the refusal does not say %q under %q:\n%s", tc.want, SummaryGCPDestinationSearchFailed, err)
			}
			if cloud.plans != 0 || cloud.applies != 0 {
				t.Errorf("planned %d and applied %d times with no destination search", cloud.plans, cloud.applies)
			}
		})
	}
}

// TestIsGoogleSelfMatchesTheWholePath (GitHub issue #2134, item 8): a
// same-named object in another region, or of another type, is not the one
// being moved; only an exact path is, or, for a bucket being moved, its
// projects/_/buckets/ form.
func TestIsGoogleSelfMatchesTheWholePath(t *testing.T) {
	m := &mover{googleSelf: []string{"edge", "projects/p/regions/us-east1/subnetworks/edge"}}
	for name, want := range map[string]bool{
		"//compute.googleapis.com/projects/p/regions/us-east1/subnetworks/edge":     true,
		"//compute.googleapis.com/projects/p/regions/europe-west1/subnetworks/edge": false,
		"//compute.googleapis.com/projects/p/global/networks/edge":                  false,
		"//storage.googleapis.com/projects/_/buckets/edge":                          false,
		"//pubsub.googleapis.com/projects/p/topics/edge":                            false,
		"projects/p/regions/us-east1/subnetworks/edge":                              false,
	} {
		if got := m.isGoogleSelf(name); got != want {
			t.Errorf("subnetwork: isGoogleSelf(%q) = %v, want %v", name, got, want)
		}
	}
	bucket := &mover{googleSelf: []string{"logs"}}
	if !bucket.isGoogleSelf("//storage.googleapis.com/projects/_/buckets/logs") {
		t.Error("a bucket is not matched by its projects/_/buckets/ asset name")
	}
}
