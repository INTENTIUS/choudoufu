// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// stubLabelledNamespaceSchema is the label surface every typed hashicorp/kubernetes
// resource shares: a metadata block of exactly one item holding name,
// labels and annotations.
func stubLabelledNamespaceSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting:  configschema.NestingList,
				MinItems: 1,
				MaxItems: 1,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"name":        {Type: cty.String, Optional: true, Computed: true},
					"labels":      {Type: cty.Map(cty.String), Optional: true},
					"annotations": {Type: cty.Map(cty.String), Optional: true},
					"uid":         {Type: cty.String, Computed: true},
				}},
			},
		},
	}}
}

// stubIsInternalKey is hashicorp/kubernetes 3.x's isInternalKey
// (kubernetes/structures.go): any key under a *.kubernetes.io host except
// app.kubernetes.io and service.beta.kubernetes.io.
func stubIsInternalKey(k string) bool {
	u, err := url.Parse("//" + k)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "app.kubernetes.io" || h == "service.beta.kubernetes.io" {
		return false
	}
	return strings.HasSuffix(h, "kubernetes.io")
}

// stubFlattenMap is the provider's removeInternalKeys: a live key that is
// internal survives the read only when the PRIOR state's map names it.
func stubFlattenMap(live map[string]string, prior cty.Value) cty.Value {
	priorHas := func(k string) bool {
		if prior == cty.NilVal || prior.IsNull() || !prior.IsKnown() {
			return false
		}
		return prior.HasIndex(cty.StringVal(k)).True()
	}
	out := map[string]cty.Value{}
	for k, v := range live {
		if stubIsInternalKey(k) && !priorHas(k) {
			continue
		}
		out[k] = cty.StringVal(v)
	}
	if len(out) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	return cty.MapVal(out)
}

// TestMetadataSeedKeepsDeclaredInternalLabels is the corpus-govuk-cluster-
// access and corpus-k8s-metrics-server test_plan failure (#1885): a
// configuration declaring a label or annotation under a *.kubernetes.io
// prefix (pod-security.kubernetes.io/enforce on GOV.UK's three Namespaces,
// kubernetes.io/name on metrics-server's Service) planned that key back on
// every run, because hashicorp/kubernetes's read drops such a key unless the
// prior state's metadata already names it, and an import stub's metadata is
// empty. Seeding the configured labels and annotations into the stub is what
// a state file's prior would have carried.
func TestMetadataSeedKeepsDeclaredInternalLabels(t *testing.T) {
	cfg := loadConfig(t, "testdata/metadata-seed")
	addr := mustAddr(t, `stub_namespace.this`)
	schema := stubLabelledNamespaceSchema()
	metaTy := schema.Block.BlockTypes["metadata"].Block.ImpliedType()

	liveLabels := map[string]string{
		"app.kubernetes.io/managed-by":       "Terraform",
		"pod-security.kubernetes.io/enforce": "restricted",
		"kubernetes.io/cluster-service":      "true",
		// Written by the API server, declared by nobody: must stay out.
		"kubernetes.io/metadata.name": "apps",
	}
	liveAnnotations := map[string]string{
		"argocd.argoproj.io/sync-options":                  "ServerSideApply=true",
		"scheduler.alpha.kubernetes.io/node-selector":      "pool=apps",
		"kubectl.kubernetes.io/last-applied-configuration": "{}",
	}

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_namespace": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		// ImportStatePassthroughContext: the id and nothing else.
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id":       cty.StringVal(r.Target.ID),
				"metadata": cty.ListValEmpty(metaTy),
			}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		priorLabels, priorAnns := cty.NilVal, cty.NilVal
		if m := r.PriorState.GetAttr("metadata"); !m.IsNull() && m.LengthInt() == 1 {
			el := m.Index(cty.NumberIntVal(0))
			priorLabels, priorAnns = el.GetAttr("labels"), el.GetAttr("annotations")
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("apps"),
			"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"name":        cty.StringVal("apps"),
				"labels":      stubFlattenMap(liveLabels, priorLabels),
				"annotations": stubFlattenMap(liveAnnotations, priorAnns),
				"uid":         cty.StringVal("0000"),
			})}),
		})}
	}

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "apps"},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_namespace.this`})

	is := res.State.ResourceInstance(addr)
	if is == nil || is.Current == nil {
		t.Fatal("stub_namespace.this is not in the projection")
	}
	got := string(is.Current.AttrsJSON)
	for _, want := range []string{
		`"pod-security.kubernetes.io/enforce":"restricted"`,
		`"kubernetes.io/cluster-service":"true"`,
		`"scheduler.alpha.kubernetes.io/node-selector":"pool=apps"`,
		`"app.kubernetes.io/managed-by":"Terraform"`,
		`"name":"apps"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the projected prior is missing %s - a declared key the read only keeps when the prior names it:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"kubernetes.io/metadata.name", "last-applied-configuration"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the projected prior carries %q, a server-written key the configuration never declared:\n%s", unwanted, got)
		}
	}
}

// TestWithSeededMetadataKeepsTheStubsOwnLeaves pins the merge: a stub whose
// metadata already carries a value (a provider whose import fills the name)
// keeps it, and only labels and annotations are taken from the seed.
func TestWithSeededMetadataKeepsTheStubsOwnLeaves(t *testing.T) {
	schema := stubLabelledNamespaceSchema()
	metaTy := schema.Block.BlockTypes["metadata"].Block.ImpliedType()
	stubMeta := cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
		"name":        cty.StringVal("apps"),
		"labels":      cty.NullVal(cty.Map(cty.String)),
		"annotations": cty.NullVal(cty.Map(cty.String)),
		"uid":         cty.NullVal(cty.String),
	})})
	seed := map[string]cty.Value{
		"labels": cty.MapVal(map[string]cty.Value{"pod-security.kubernetes.io/enforce": cty.StringVal("restricted")}),
	}
	got, ok := mergeMetadataSeed(stubMeta, seed, metaTy)
	if !ok {
		t.Fatal("mergeMetadataSeed declined a one-item metadata list")
	}
	el := got.Index(cty.NumberIntVal(0))
	if v := el.GetAttr("name"); !v.RawEquals(cty.StringVal("apps")) {
		t.Errorf("name = %#v, want the stub's own \"apps\"", v)
	}
	if v := el.GetAttr("labels"); !v.RawEquals(seed["labels"]) {
		t.Errorf("labels = %#v, want the seed", v)
	}
	if v := el.GetAttr("annotations"); !v.IsNull() {
		t.Errorf("annotations = %#v, want it left null: the seed named none", v)
	}

	empty, ok := mergeMetadataSeed(cty.ListValEmpty(metaTy), seed, metaTy)
	if !ok || empty.LengthInt() != 1 {
		t.Fatalf("an empty stub metadata list was not given one seeded item: %#v", empty)
	}
	if v := empty.Index(cty.NumberIntVal(0)).GetAttr("name"); !v.IsNull() {
		t.Errorf("a seeded item invented a name %#v; identity is never seeded", v)
	}
}
