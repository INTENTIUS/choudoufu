// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1715, found by #1579's smoke pass on corpus-quickpizza's
// day2_replace: a root with a helm and a kubernetes provider block runs two
// discovery passes. Removing the content-hashed ConfigMap's block left its
// identity record (written for every instance since #364) behind, and the
// helm pass's record-orphan leg proposed it for removal: the type is
// ratified, and the helm schema has no kubernetes_config_map, so the leg's
// "taggable, the tag sweep covers it" skip read false. Merge then attributed
// the removal to the helm pass, and the projection failed:
//
//	Provider provider["registry.opentofu.org/hashicorp/helm"] has no schema
//	for managed resource type "kubernetes_config_map", so a projection
//	cannot be built for it.
//
// The kubernetes pass is the one that found the object (its label sweep
// files it as an orphan), so it is the only pass whose removal may stand.

// schemaOnlyProvider is a provider handle that serves a fixed schema and
// lists nothing: what the record-orphan leg reads of a pass's provider.
type schemaOnlyProvider struct {
	types map[string]providers.Schema
}

func (p schemaOnlyProvider) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{ResourceTypes: p.types, ListResourceTypes: map[string]providers.Schema{}}
}

func (schemaOnlyProvider) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

// configMapIdentitySchema is kubernetes_config_map's shape at provider 3.x, identity schema included: the
// estate's label and address annotation live under metadata.
func configMapIdentitySchema() providers.Schema {
	metadata := configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"name":             {Type: cty.String, Optional: true, Computed: true},
			"namespace":        {Type: cty.String, Optional: true},
			"labels":           {Type: cty.Map(cty.String), Optional: true},
			"annotations":      {Type: cty.Map(cty.String), Optional: true},
			"generation":       {Type: cty.Number, Computed: true},
			"generate_name":    {Type: cty.String, Optional: true},
			"resource_version": {Type: cty.String, Computed: true},
			"uid":              {Type: cty.String, Computed: true},
		},
	}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":          {Type: cty.String, Optional: true, Computed: true},
				"binary_data": {Type: cty.Map(cty.String), Optional: true},
				"data":        {Type: cty.Map(cty.String), Optional: true},
				"immutable":   {Type: cty.Bool, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"metadata": {Block: metadata, Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_version": {Type: cty.String, Required: true},
				"kind":        {Type: cty.String, Required: true},
				"name":        {Type: cty.String, Required: true},
				"namespace":   {Type: cty.String, Optional: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

func helmReleaseSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":        {Type: cty.String, Computed: true},
		"name":      {Type: cty.String, Required: true},
		"namespace": {Type: cty.String, Optional: true},
		"chart":     {Type: cty.String, Required: true},
	}}}
}

// TestRecordOrphanOfAnotherProvidersTypeIsNotThisPasssRemoval is #1715's
// red: two passes, helm and kubernetes, over one record store holding the
// identity of a ConfigMap whose block was removed. Every removal of that
// ConfigMap the merge hands the projection must be attributed to the
// kubernetes provider configuration, and there must be exactly one.
func TestRecordOrphanOfAnotherProvidersTypeIsNotThisPasssRemoval(t *testing.T) {
	ctx := context.Background()
	const estate = "quickpizza"
	const ns = "quickpizza"

	helmProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("helm")}
	kubeProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}

	cmSchema := configMapIdentitySchema()
	helmSchemas, diags := listclient.ListSchemas(ctx, schemaOnlyProvider{types: map[string]providers.Schema{"helm_release": helmReleaseSchema()}})
	if diags.HasErrors() {
		t.Fatalf("helm schemas: %s", diags.Err())
	}
	// The record write-back leaves for the removed block, built the way
	// write-back builds it: from the object's final state.
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
	hashed := mustAddr(t, "kubernetes_config_map.hashed")
	obj := cty.ObjectVal(map[string]cty.Value{
		"id":          cty.StringVal(ns + "/cfg-b"),
		"binary_data": cty.NullVal(cty.Map(cty.String)),
		"data":        cty.MapVal(map[string]cty.Value{"v": cty.StringVal("b")}),
		"immutable":   cty.False,
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":             cty.StringVal("cfg-b"),
			"namespace":        cty.StringVal(ns),
			"labels":           cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal(estate)}),
			"annotations":      cty.MapVal(map[string]cty.Value{"choudoufu.intentius.io/tofu-address": cty.StringVal("kubernetes_config_map.hashed")}),
			"generation":       cty.NumberIntVal(0),
			"generate_name":    cty.NullVal(cty.String),
			"resource_version": cty.StringVal("1"),
			"uid":              cty.StringVal("u-1"),
		})}),
	})
	rec, ok := projection.LocatedRecordFrom("kubernetes_config_map", cmSchema, obj)
	if !ok {
		t.Fatal("precondition: write-back records no identity for this ConfigMap, so the leg has nothing to read and this test measures nothing")
	}
	if _, err := projection.SeedLocatedForInstance(ctx, store, hashed, kubeProv, rec); err != nil {
		t.Fatalf("seeding the record: %s", err)
	}

	// The helm pass is an unclaimed provider's: no family lists through it,
	// and it keeps Request.Sweep on for the removal legs that read the
	// record store (GitHub issue #1707), so its record leg runs. The
	// kubernetes pass's label-list leg turns Request.Sweep off
	// (internal/command's sweepLegBuilders), so its record leg never runs
	// and the label sweep alone files the object: measured on
	// corpus-quickpizza's day2_replace, where it filed cfg-b under the
	// declared type that manages ConfigMaps.
	helmRes := &Result{Estate: estate}
	helmReq := Request{Estate: estate, HintStore: raw, ScopeProvider: helmProv, VouchProvider: helmProv, Sweep: true}
	if d := recordOrphanReadSweep(ctx, helmReq, helmSchemas, helmRes); d.HasErrors() {
		t.Fatalf("record leg through helm: %s", d.Err())
	}

	orphan := mustAddr(t, "kubernetes_config_map_v1.orphan_"+ns+"_cfg-b")
	kubeRes := &Result{Estate: estate}
	kubeRes.Resolutions = append(kubeRes.Resolutions, identity.Resolution{Addr: orphan, Class: identity.ClassConcrete, ImportID: ns + "/cfg-b", Undeclared: true})
	kubeRes.Orphans = append(kubeRes.Orphans, OwnedResource{TypeName: "kubernetes_config_map_v1", ImportID: ns + "/cfg-b", Addr: orphan, Addressable: true, Removal: true, Swept: true})

	merged, providerOf, mdiags := Merge(estate, []Pass{
		{Provider: helmProv, Result: helmRes},
		{Provider: kubeProv, Result: kubeRes},
	}, false)
	if mdiags.HasErrors() {
		t.Fatalf("Merge: %s", mdiags.Err())
	}

	var removals []string
	for _, r := range merged.Resolutions {
		if !r.Undeclared || r.ImportID != ns+"/cfg-b" {
			continue
		}
		removals = append(removals, r.Addr.String())
		got := providerOf[r.Addr.String()]
		if got.String() != kubeProv.String() {
			t.Errorf("%s (a removal of %s) is projected through %s, want %s: the pass that found the object is the kubernetes pass", r.Addr, r.ImportID, got, kubeProv)
		}
	}
	if len(removals) != 1 {
		t.Errorf("the merge proposes %d removals of the one live ConfigMap (%v), want exactly 1", len(removals), removals)
	}
}

// TestRecordOrphanOfThisPasssOwnTypeIsStillProposed is the rule's other
// side: a scoped pass whose provider does serve the recorded type still
// proposes the removal, so #1715's skip reaches only another provider's
// types and not every record a multi-provider root holds.
func TestRecordOrphanOfThisPasssOwnTypeIsStillProposed(t *testing.T) {
	ctx := context.Background()
	const estate = "quickpizza"
	kubeProv := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	kubeSchemas, diags := listclient.ListSchemas(ctx, schemaOnlyProvider{types: map[string]providers.Schema{"kubernetes_config_map": configMapIdentitySchema()}})
	if diags.HasErrors() {
		t.Fatalf("kubernetes schemas: %s", diags.Err())
	}

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estate))
	hashed := mustAddr(t, "kubernetes_config_map.hashed")
	if _, err := projection.SeedLocatedForInstance(ctx, store, hashed, kubeProv, projection.LocatedRecord{ImportID: "quickpizza/cfg-b"}); err != nil {
		t.Fatalf("seeding the record: %s", err)
	}

	res := &Result{Estate: estate}
	req := Request{Estate: estate, HintStore: raw, ScopeProvider: kubeProv, VouchProvider: kubeProv, Sweep: true}
	if d := recordOrphanReadSweep(ctx, req, kubeSchemas, res); d.HasErrors() {
		t.Fatalf("record leg: %s", d.Err())
	}
	if len(res.Resolutions) != 1 || res.Resolutions[0].Addr.String() != hashed.String() || !res.Resolutions[0].Undeclared {
		t.Fatalf("a pass whose provider serves kubernetes_config_map proposed %v, want exactly the removal of %s", res.Resolutions, hashed)
	}
}
