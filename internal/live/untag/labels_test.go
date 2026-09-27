// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1644: undeclared_tagged = "untag" on a Kubernetes orphan.
// The sweep files a labelled, undeclared object as an untag target the same
// way the AWS legs do, and before this file's fix releaseOne asked only
// whether the type had a tags map, answered "nothing to release" with OK
// false, and left tofu-estate on the object for every later sweep to find.

const testKey = markers.TagEstate

// configMapSchema is kubernetes_config_map_v1 narrowed to what the label
// surface needs: a metadata block of exactly one element beside a data map
// and the provider's id.
func configMapSchema() providers.Schema {
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
				}},
			},
		},
	}}
}

// manifestSchema is kubernetes_manifest's shape as [markers.ManifestSurface]
// reads it.
func manifestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			markers.ManifestSurfaceAttr: {Type: cty.DynamicPseudoType, Required: true},
			markers.ManifestLiveAttr:    {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
		},
	}}
}

func labelMap(m map[string]string) cty.Value {
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	vals := make(map[string]cty.Value, len(m))
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	return cty.MapVal(vals)
}

func configMapObject(labels map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal("orphans/stale-config"),
		"data": cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("hello")}),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":             cty.StringVal("stale-config"),
			"namespace":        cty.StringVal("orphans"),
			"labels":           labelMap(labels),
			"annotations":      cty.NullVal(cty.Map(cty.String)),
			"uid":              cty.StringVal("6bc3dcc0"),
			"resource_version": cty.StringVal("575"),
		})}),
	})
}

// fakeCluster is a provider over one live object: import and read answer
// with it, a plan proposes what it is handed (through planHook when a test
// sets one), and an apply replaces it.
type fakeCluster struct {
	*tofu.MockProvider
	live     cty.Value
	applied  int
	planHook func(cty.Value) cty.Value
}

func newFakeCluster(typeName string, schema providers.Schema, live cty.Value) *fakeCluster {
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	c := &fakeCluster{MockProvider: p, live: live}
	p.GetProviderSchemaResponse = &providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{typeName: schema},
	}
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: c.live}}}
	}
	p.ReadResourceFn = func(providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: c.live}
	}
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		planned := r.ProposedNewState
		if c.planHook != nil {
			planned = c.planHook(planned)
		}
		return providers.PlanResourceChangeResponse{PlannedState: planned}
	}
	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		c.applied++
		c.live = r.PlannedState
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

func configMapTarget() Target {
	return Target{TypeName: "kubernetes_config_map_v1", ImportID: "orphans/stale-config", Marker: "smoke-k8s"}
}

func TestRelease_LabelSurfaceReleasesTheEstateLabel(t *testing.T) {
	c := newFakeCluster("kubernetes_config_map_v1", configMapSchema(),
		configMapObject(map[string]string{"app": "web", markers.TagEstate: "smoke-k8s"}))

	res, diags := Release(context.Background(), c, testKey, []Target{configMapTarget()})
	if len(res.Outcomes) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(res.Outcomes))
	}
	out := res.Outcomes[0]
	if !out.OK || diags.HasErrors() {
		t.Fatalf("outcome = %s (diags %v), want RELEASED", out, diags.Err())
	}
	if c.applied != 1 {
		t.Fatalf("applied %d times, want 1", c.applied)
	}
	got, ok := markers.LabelsOf(c.live)
	if !ok {
		t.Fatal("the object read back has no readable labels")
	}
	if _, still := got[markers.TagEstate]; still || got["app"] != "web" || len(got) != 1 {
		t.Errorf("labels after release = %v, want app=web alone", got)
	}
	if d := c.live.GetAttr("data"); !d.RawEquals(configMapObject(nil).GetAttr("data")) {
		t.Errorf("data changed across a labels-only release: %#v", d)
	}
	if strings.Contains(out.Detail, "tag") {
		t.Errorf("a label release reports itself in tag words: %s", out.Detail)
	}
}

func TestRelease_LabelSurfaceAlreadyReleasedWritesNothing(t *testing.T) {
	c := newFakeCluster("kubernetes_config_map_v1", configMapSchema(), configMapObject(map[string]string{"app": "web"}))
	res, _ := Release(context.Background(), c, testKey, []Target{configMapTarget()})
	if out := res.Outcomes[0]; !out.OK || c.applied != 0 {
		t.Fatalf("outcome = %s, applied %d; want OK with no write", out, c.applied)
	}
}

// A plan that also renames the object is refused before any apply: the
// release is a labels-only write, the label twin of the tags-only guard.
func TestRelease_LabelSurfaceRefusesAPlanThatChangesMoreThanLabels(t *testing.T) {
	cases := map[string]func(cty.Value) cty.Value{
		"metadata.name": func(v cty.Value) cty.Value {
			return setMeta(v, "name", cty.StringVal("renamed"))
		},
		"data": func(v cty.Value) cty.Value {
			m := v.AsValueMap()
			m["data"] = cty.MapValEmpty(cty.String)
			return cty.ObjectVal(m)
		},
	}
	for name, hook := range cases {
		t.Run(name, func(t *testing.T) {
			c := newFakeCluster("kubernetes_config_map_v1", configMapSchema(),
				configMapObject(map[string]string{markers.TagEstate: "smoke-k8s"}))
			c.planHook = hook
			res, _ := Release(context.Background(), c, testKey, []Target{configMapTarget()})
			out := res.Outcomes[0]
			if out.OK || c.applied != 0 {
				t.Fatalf("outcome = %s, applied %d; want a refusal with no write", out, c.applied)
			}
			if !strings.Contains(out.Detail, name) {
				t.Errorf("the refusal does not name %s: %s", name, out.Detail)
			}
		})
	}
}

func setMeta(v cty.Value, attr string, val cty.Value) cty.Value {
	m := v.AsValueMap()
	meta := m["metadata"].Index(cty.NumberIntVal(0)).AsValueMap()
	meta[attr] = val
	m["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(meta)})
	return cty.ObjectVal(m)
}

// The manifest shape's write is an API patch, not a provider plan (#1109);
// until untag holds a cluster client it refuses by name, reading and writing
// nothing, rather than reporting "no tags argument".
func TestRelease_ManifestSurfaceIsRefusedByName(t *testing.T) {
	c := newFakeCluster("kubernetes_manifest", manifestSchema(), cty.NullVal(cty.DynamicPseudoType))
	res, _ := Release(context.Background(), c, testKey, []Target{{
		TypeName: "kubernetes_manifest",
		ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=orphans,name=stale",
	}})
	out := res.Outcomes[0]
	if out.OK || c.applied != 0 || c.ImportResourceStateCalled {
		t.Fatalf("outcome = %s, applied %d, imported %v; want a refusal that touches nothing", out, c.applied, c.ImportResourceStateCalled)
	}
	for _, want := range []string{"kubectl label", "tofu-estate-", "kind=CronTab"} {
		if !strings.Contains(out.Detail, want) {
			t.Errorf("the refusal does not carry %q: %s", want, out.Detail)
		}
	}
}
