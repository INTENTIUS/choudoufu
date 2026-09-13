// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
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
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// live-mv's Kubernetes answer (GitHub issue #1081's fifth item; label.go).
// The type below is deliberately one with NO row in identity.DefaultTable
// (kubernetes_config_map_v1 - only the unsuffixed name is ratified), so
// the same fixture proves the admission gate now accepts a resolution the
// schema fallback produced: before, Move refused every such type as
// "outside the live-markers subset" before reading anything.
//
// Proving them red: make Move's surface switch treat SurfaceLabel as
// SurfaceTags and TestMove_LabelSurfaceMovesBetweenEstates fails with
// "Resource type with no tags" and TestMove_LabelSurfaceRenameHasNothingToWrite
// with a read the object never asked for; make changedOutsideLabels skip
// the whole metadata block and the "a rename inside the metadata block"
// case below applies the rename.

const (
	labelTestType   = "kubernetes_config_map_v1"
	labelTestLiveID = "boundary/database"
)

var labelTestProviderAddr = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("kubernetes"),
}

// labelTestSchema is the provider's object-metadata shape narrowed to what
// the label surface needs, the same block internal/live/liveimport's
// label tests carry.
func labelTestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"data": {Type: cty.Map(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"name":             {Type: cty.String, Optional: true, Computed: true},
					"namespace":        {Type: cty.String, Optional: true},
					"labels":           {Type: cty.Map(cty.String), Optional: true},
					"annotations":      {Type: cty.Map(cty.String), Optional: true},
					"uid":              {Type: cty.String, Computed: true},
					"resource_version": {Type: cty.String, Computed: true},
					"generation":       {Type: cty.Number, Computed: true},
				}},
			},
		},
	}}
}

// manifestTestSchema is the manifest shape: one required dynamic manifest,
// one computed dynamic object, no metadata block.
func manifestTestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest": {Type: cty.DynamicPseudoType, Required: true},
			"object":   {Type: cty.DynamicPseudoType, Computed: true},
		},
	}}
}

func labelTestLabels(m map[string]string) cty.Value {
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	vals := make(map[string]cty.Value, len(m))
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	return cty.MapVal(vals)
}

// labelTestObject is the live ConfigMap as the provider reads it back.
func labelTestObject(labels map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal(labelTestLiveID),
		"data": cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("database")}),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":             cty.StringVal("database"),
			"namespace":        cty.StringVal("boundary"),
			"labels":           labelTestLabels(labels),
			"annotations":      cty.NullVal(cty.Map(cty.String)),
			"uid":              cty.StringVal("6bc3dcc0"),
			"resource_version": cty.StringVal("575"),
			"generation":       cty.NumberIntVal(1),
		})}),
	})
}

// labelTestConfig declares one object-metadata block under the given name,
// so a test can declare the destination of a rename or the same address
// as the move it makes.
func labelTestConfig(t *testing.T, typeName, blockName string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
terraform {
  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}

resource "`+typeName+`" "`+blockName+`" {
  provider = kubernetes
  metadata {
    name      = "database"
    namespace = "boundary"
  }
}
`)
	return loadConfigDir(t, dir)
}

// labelTestCluster is one live object behind a provider that speaks the
// calls a move makes - import, read, plan, apply - and counts each, so a
// test can say "nothing was read" as well as "nothing was written".
type labelTestCluster struct {
	*tofu.MockProvider
	object   cty.Value
	reads    int
	plans    int
	applies  int
	planHook func(cty.Value) cty.Value
}

func newLabelTestCluster(t *testing.T, schema providers.Schema, object cty.Value) *labelTestCluster {
	t.Helper()
	c := &labelTestCluster{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{labelTestType: schema},
		},
	}, object: object}
	c.ConfigureProviderCalled = true
	c.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		c.reads++
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: c.object}},
		}
	}
	c.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		c.reads++
		return providers.ReadResourceResponse{NewState: c.object}
	}
	c.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		if r.PriorState.IsNull() {
			// The projection's own identity-attribute normalization asks a
			// create-shaped plan of every materialized instance; not a
			// write-preparation call (see internal/command's mvCloud).
			return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
		}
		c.plans++
		planned := r.ProposedNewState
		if c.planHook != nil {
			planned = c.planHook(planned)
		}
		return providers.PlanResourceChangeResponse{PlannedState: planned}
	}
	c.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		c.applies++
		c.object = r.PlannedState
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

// ListResourceStream satisfies the list protocol the same way
// recordFallbackProvider does: the provider serves no list schema for the
// type, so listclient.ListSchemas reports it unlistable before any call.
func (*labelTestCluster) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

func (c *labelTestCluster) labels(t *testing.T) map[string]string {
	t.Helper()
	got, ok := markers.LabelsOf(c.object)
	if !ok {
		t.Fatal("the live object has no readable labels")
	}
	return got
}

func labelTestRequest(t *testing.T, cluster *labelTestCluster, cfg *configs.Config, old, new addrs.AbsResourceInstance, from, to string) Request {
	t.Helper()
	anchor := new
	return Request{
		Estate:     to,
		FromEstate: from,
		Old:        old,
		New:        new,
		Config:     cfg,
		Resolutions: []identity.Resolution{{
			Addr:     anchor,
			Class:    identity.ClassConcrete,
			ImportID: labelTestLiveID,
		}},
		Providers: projection.SingleProvider(labelTestProviderAddr, cluster),
	}
}

func TestMove_LabelSurfaceMovesBetweenEstates(t *testing.T) {
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObject(map[string]string{"app": "web", markers.TagEstate: "app"}))
	addr := mustAddr(t, labelTestType+".database")
	res, diags := Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database"), addr, addr, "app", "data"))
	if diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if res.Surface != SurfaceLabel {
		t.Errorf("Surface = %q, want %q", res.Surface, SurfaceLabel)
	}
	if !res.Written || !res.Verified {
		t.Errorf("Written = %v, Verified = %v, want both true", res.Written, res.Verified)
	}
	if res.NothingToWrite {
		t.Error("a cross-estate move reported nothing to write")
	}
	if res.LiveID != labelTestLiveID || res.Path != PathIdentity {
		t.Errorf("LiveID = %q (Path %s), want %q found by identity", res.LiveID, res.Path, labelTestLiveID)
	}
	if cluster.applies != 1 || cluster.plans != 1 {
		t.Errorf("planned %d and applied %d times, want 1 and 1", cluster.plans, cluster.applies)
	}
	got := cluster.labels(t)
	if got[markers.TagEstate] != "data" || got["app"] != "web" || len(got) != 2 {
		t.Errorf("live labels = %v, want app=web plus tofu-estate=data and nothing else", got)
	}
	if _, hasAddr := got[markers.TagAddress]; hasAddr {
		t.Errorf("a tofu-address label was written; the Kubernetes marker is the estate alone (#1016)")
	}
	if d := cluster.object.GetAttr("data"); !d.RawEquals(labelTestObject(nil).GetAttr("data")) {
		t.Errorf("data changed across a labels-only write: %#v", d)
	}
	meta := cluster.object.GetAttr("metadata").Index(cty.NumberIntVal(0))
	if meta.GetAttr("name").AsString() != "database" || meta.GetAttr("namespace").AsString() != "boundary" {
		t.Errorf("the object's name or namespace moved: %#v", meta)
	}

	// Run again: the object now carries the destination, so the move
	// reports that it already ran, with the same code the tag path uses.
	cluster2 := newLabelTestCluster(t, labelTestSchema(), cluster.object)
	_, diags = Move(t.Context(), labelTestRequest(t, cluster2, labelTestConfig(t, labelTestType, "database"), addr, addr, "app", "data"))
	if got := RefusalFrom(diags); got != RefusalNewAddressClaimed {
		t.Errorf("the second move's refusal code is %q, want %q: %s", got, RefusalNewAddressClaimed, diags.Err())
	}
	if cluster2.applies != 0 {
		t.Errorf("the second move wrote again: %d applies", cluster2.applies)
	}
}

func TestMove_LabelSurfaceRenameHasNothingToWrite(t *testing.T) {
	ctx := t.Context()
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObject(map[string]string{markers.TagEstate: "app"}))
	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")

	// A migration records an identity for every stamped instance, label
	// surface included (internal/live/liveimport's seedIdentityFor), keyed
	// by address; the rename has to carry it, or the key goes stale the
	// way GitHub issue #412 found on the tag surface.
	store := recordFallbackStore(t)
	if _, err := projection.SeedLocatedForInstance(ctx, store, old, labelTestProviderAddr, projection.LocatedRecord{ImportID: labelTestLiveID}); err != nil {
		t.Fatalf("seeding the record fixture: %s", err)
	}

	req := labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database_renamed"), old, renamed, "", "app")
	req.RecordStore = store
	res, diags := Move(ctx, req)
	if diags.HasErrors() {
		t.Fatalf("a same-estate rename on the label surface was refused: %s", diags.Err())
	}
	if _, _, atOld, _, err := store.GetIdentity(ctx, old); err != nil || atOld {
		t.Errorf("the record is still keyed at the old address (err %v)", err)
	}
	if rec, _, atNew, found, err := store.GetIdentity(ctx, renamed); err != nil || !atNew || !found || rec.ImportID != labelTestLiveID {
		t.Errorf("the record did not follow the rename to %s: exists=%v found=%v import id %q (err %v)", renamed, atNew, found, rec.ImportID, err)
	}
	if !res.NothingToWrite {
		t.Fatal("NothingToWrite is false: the marker carries no address, so a rename has nothing governed to write")
	}
	if res.Surface != SurfaceLabel {
		t.Errorf("Surface = %q, want %q", res.Surface, SurfaceLabel)
	}
	if res.Written || res.Verified {
		t.Errorf("Written = %v, Verified = %v on a rename with nothing to write", res.Written, res.Verified)
	}
	if cluster.reads != 0 || cluster.plans != 0 || cluster.applies != 0 {
		t.Errorf("a rename with nothing to write still reached the cluster: %d reads, %d plans, %d applies", cluster.reads, cluster.plans, cluster.applies)
	}
	if got := cluster.labels(t); got[markers.TagEstate] != "app" {
		t.Errorf("the live label moved: %v", got)
	}
}

func TestMove_LabelSurfaceRefusals(t *testing.T) {
	addr := mustAddr(t, labelTestType+".database")
	cases := map[string]struct {
		labels   map[string]string
		to       string
		planHook func(cty.Value) cty.Value
		wantCode RefusalCode
		want     string
	}{
		"the object carries no label at all": {
			labels: nil, to: "data",
			wantCode: RefusalNothingAtOldAddress, want: "no tofu-estate label",
		},
		"the object belongs to a third estate": {
			labels: map[string]string{markers.TagEstate: "somebody-else"}, to: "data",
			want: "Live resource owned by another estate",
		},
		"the destination estate is not a legal label value": {
			labels: map[string]string{markers.TagEstate: "app"}, to: strings.Repeat("a", markers.LabelMaxValue+1),
			want: "not a legal Kubernetes label value",
		},
		"a rename inside the metadata block": {
			labels: map[string]string{markers.TagEstate: "app"}, to: "data",
			planHook: func(v cty.Value) cty.Value {
				m := v.AsValueMap()
				elem := m["metadata"].Index(cty.NumberIntVal(0)).AsValueMap()
				elem["name"] = cty.StringVal("database-renamed")
				m["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(elem)})
				return cty.ObjectVal(m)
			},
			wantCode: RefusalPlanChangesMoreThanTags, want: "metadata.name",
		},
		"a data change outside the metadata block": {
			labels: map[string]string{markers.TagEstate: "app"}, to: "data",
			planHook: func(v cty.Value) cty.Value {
				m := v.AsValueMap()
				m["data"] = cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("tampered")})
				return cty.ObjectVal(m)
			},
			wantCode: RefusalPlanChangesMoreThanTags, want: "data",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObject(tc.labels))
			cluster.planHook = tc.planHook
			_, diags := Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database"), addr, addr, "app", tc.to))
			if !diags.HasErrors() {
				t.Fatal("the move was not refused")
			}
			if !strings.Contains(diags.Err().Error(), tc.want) {
				t.Errorf("the refusal does not say %q:\n%s", tc.want, diags.Err())
			}
			if got := RefusalFrom(diags); got != tc.wantCode {
				t.Errorf("refusal code = %q, want %q", got, tc.wantCode)
			}
			if cluster.applies != 0 {
				t.Errorf("a refused move still wrote: %d applies", cluster.applies)
			}
			if got := cluster.labels(t)[markers.TagEstate]; got != tc.labels[markers.TagEstate] {
				t.Errorf("the live label changed under a refusal: %q", got)
			}
		})
	}
}

func TestMove_ManifestSurfaceMoveIsRefusedByName(t *testing.T) {
	addr := mustAddr(t, labelTestType+".database")
	cluster := newLabelTestCluster(t, manifestTestSchema(), cty.NullVal(manifestTestSchema().Block.ImpliedType()))
	res, diags := Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database"), addr, addr, "app", "data"))
	if !diags.HasErrors() {
		t.Fatal("a cross-estate move of a manifest-declared object was not refused")
	}
	var found bool
	for _, d := range diags {
		if d.Description().Summary == SummaryManifestMoveUnsupported {
			found = true
			if !strings.Contains(d.Description().Detail, "kubectl label") || !strings.Contains(d.Description().Detail, "tofu-estate=data") {
				t.Errorf("the refusal does not name the equivalent kubectl write:\n%s", d.Description().Detail)
			}
		}
	}
	if !found {
		t.Errorf("no %q diagnostic; got %s", SummaryManifestMoveUnsupported, diags.Err())
	}
	if res.Surface != SurfaceManifest {
		t.Errorf("Surface = %q, want %q", res.Surface, SurfaceManifest)
	}
	if cluster.reads != 0 || cluster.applies != 0 {
		t.Errorf("a by-name refusal still reached the cluster: %d reads, %d applies", cluster.reads, cluster.applies)
	}

	// A same-estate rename on the manifest surface has nothing to write
	// either, exactly as on the metadata-block surface.
	renamed := mustAddr(t, labelTestType+".database_renamed")
	res, diags = Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database_renamed"), addr, renamed, "", "app"))
	if diags.HasErrors() || !res.NothingToWrite {
		t.Errorf("a same-estate rename of a manifest-declared object: NothingToWrite = %v, diags = %v", res.NothingToWrite, diags.Err())
	}
}
