// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// The six field-granular schemas, transcribed from `tofu providers schema
// -json` against hashicorp/kubernetes 3.2.1 on 2026-10-03 (GitHub issue
// #1191): every one carries a top-level optional string field_manager and
// optional bool force, and a metadata block of exactly one holding name
// (and namespace, except kubernetes_node_taint) with no uid and no labels.

func fgMetadata(namespaced bool) *configschema.NestedBlock {
	attrs := map[string]*configschema.Attribute{
		"name": {Type: cty.String, Required: true},
	}
	if namespaced {
		attrs["namespace"] = &configschema.Attribute{Type: cty.String, Optional: true}
	}
	return &configschema.NestedBlock{
		Nesting:  configschema.NestingList,
		MinItems: 1,
		MaxItems: 1,
		Block:    configschema.Block{Attributes: attrs},
	}
}

func fgBase(namespaced bool, extra map[string]*configschema.Attribute, blocks map[string]*configschema.NestedBlock) *configschema.Block {
	attrs := map[string]*configschema.Attribute{
		"field_manager": {Type: cty.String, Optional: true},
		"force":         {Type: cty.Bool, Optional: true},
		"id":            {Type: cty.String, Optional: true, Computed: true},
	}
	for k, v := range extra {
		attrs[k] = v
	}
	bt := map[string]*configschema.NestedBlock{"metadata": fgMetadata(namespaced)}
	for k, v := range blocks {
		bt[k] = v
	}
	return &configschema.Block{Attributes: attrs, BlockTypes: bt}
}

func fieldGranularSchemas() map[string]*configschema.Block {
	kindAttrs := func(more map[string]*configschema.Attribute) map[string]*configschema.Attribute {
		out := map[string]*configschema.Attribute{
			"api_version": {Type: cty.String, Required: true},
			"kind":        {Type: cty.String, Required: true},
		}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	return map[string]*configschema.Block{
		"kubernetes_labels": fgBase(true, kindAttrs(map[string]*configschema.Attribute{
			"labels": {Type: cty.Map(cty.String), Required: true},
		}), nil),
		"kubernetes_annotations": fgBase(true, kindAttrs(map[string]*configschema.Attribute{
			"annotations":          {Type: cty.Map(cty.String), Optional: true},
			"template_annotations": {Type: cty.Map(cty.String), Optional: true},
		}), nil),
		"kubernetes_env": fgBase(true, kindAttrs(map[string]*configschema.Attribute{
			"container":      {Type: cty.String, Optional: true},
			"init_container": {Type: cty.String, Optional: true},
		}), map[string]*configschema.NestedBlock{
			"env": {Nesting: configschema.NestingList, MinItems: 1, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"name":  {Type: cty.String, Required: true},
				"value": {Type: cty.String, Optional: true},
			}}},
		}),
		"kubernetes_config_map_v1_data": fgBase(true, map[string]*configschema.Attribute{
			"data": {Type: cty.Map(cty.String), Required: true},
		}, nil),
		"kubernetes_secret_v1_data": fgBase(true, map[string]*configschema.Attribute{
			"data": {Type: cty.Map(cty.String), Required: true, Sensitive: true},
		}, nil),
		"kubernetes_node_taint": fgBase(false, nil, map[string]*configschema.NestedBlock{
			"taint": {Nesting: configschema.NestingList, MinItems: 1, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"effect": {Type: cty.String, Required: true},
				"key":    {Type: cty.String, Required: true},
				"value":  {Type: cty.String, Required: true},
			}}},
		}),
	}
}

// TestFieldGranularShapeAdmitsTheSixAndNothingElse pins the predicate both
// ways: the six schemas match, an object-metadata schema (a uid and a
// labels map in metadata) and a manifest schema do not, and removing either
// top-level attribute that makes the shape takes a type out of it.
func TestFieldGranularShapeAdmitsTheSixAndNothingElse(t *testing.T) {
	for name, block := range fieldGranularSchemas() {
		namespaced, ok := FieldGranularShape(block)
		if !ok {
			t.Errorf("%s: not the field-granular shape", name)
			continue
		}
		if want := name != "kubernetes_node_taint"; namespaced != want {
			t.Errorf("%s: namespaced = %v, want %v", name, namespaced, want)
		}
		if _, meta := ObjectMetaShape(block); meta {
			t.Errorf("%s: also the object-metadata shape; the two predicates must never both answer", name)
		}
		for _, drop := range []string{FieldManagerAttr, FieldForceAttr} {
			mutated := *block
			mutated.Attributes = map[string]*configschema.Attribute{}
			for k, v := range block.Attributes {
				if k != drop {
					mutated.Attributes[k] = v
				}
			}
			if _, ok := FieldGranularShape(&mutated); ok {
				t.Errorf("%s without %s is still the field-granular shape", name, drop)
			}
		}
	}

	objectMeta := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"field_manager": {Type: cty.String, Optional: true},
			"force":         {Type: cty.Bool, Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {Nesting: configschema.NestingList, MaxItems: 1, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"name":   {Type: cty.String, Required: true},
				"uid":    {Type: cty.String, Computed: true},
				"labels": {Type: cty.Map(cty.String), Optional: true},
			}}},
		},
	}
	if _, ok := FieldGranularShape(objectMeta); ok {
		t.Error("a metadata block carrying uid and labels is an object's own metadata, not a patched object's name")
	}
	manifest := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"manifest": {Type: cty.DynamicPseudoType, Required: true},
		"object":   {Type: cty.DynamicPseudoType, Computed: true},
	}}
	if _, ok := FieldGranularShape(manifest); ok {
		t.Error("kubernetes_manifest names its field manager in a nested block and is not the field-granular shape")
	}
	if _, ok := FieldGranularShape(nil); ok {
		t.Error("a nil schema matched")
	}
}

// TestFieldGranularIdentityIsThePatchedObject pins the synthesized entry
// per type: the kind-naming three render the env-parseable
// apiVersion=...,kind=...,[namespace=...,]name=..., the two data types
// NAMESPACE/NAME, and a node's taints NAME.
func TestFieldGranularIdentityIsThePatchedObject(t *testing.T) {
	want := map[string]string{
		"kubernetes_labels":             FieldGranularImportSyntax,
		"kubernetes_annotations":        FieldGranularImportSyntax,
		"kubernetes_env":                FieldGranularImportSyntax,
		"kubernetes_config_map_v1_data": "NAMESPACE/NAME",
		"kubernetes_secret_v1_data":     "NAMESPACE/NAME",
		"kubernetes_node_taint":         "NAME",
	}
	for name, block := range fieldGranularSchemas() {
		synth, ok := Kubernetes.SynthesizeIdentity(name, providers.Schema{Block: block})
		if !ok || synth.FromIdentitySchema {
			t.Errorf("%s: the Kubernetes family did not synthesize an identity (ok=%v, fromIdentitySchema=%v)", name, ok, synth.FromIdentitySchema)
			continue
		}
		if synth.ImportSyntax != want[name] {
			t.Errorf("%s: import syntax %q, want %q", name, synth.ImportSyntax, want[name])
		}
		if !synth.NonAWSProvider {
			t.Errorf("%s: not marked a non-AWS provider", name)
		}
		if len(synth.IdentityAttrs) != 0 {
			t.Errorf("%s: claims identity attributes %v; these resources' attributes are the fields they write", name, synth.IdentityAttrs)
		}
		var read []string
		for _, c := range synth.Components {
			if len(c.Attrs) == 0 {
				continue
			}
			where := c.Attrs[0]
			if c.Block != "" {
				where = c.Block + "." + where
			}
			read = append(read, where)
		}
		var wantRead []string
		switch want[name] {
		case FieldGranularImportSyntax:
			wantRead = []string{"api_version", "kind", "metadata.namespace", "metadata.name"}
		case "NAMESPACE/NAME":
			wantRead = []string{"metadata.namespace", "metadata.name"}
		case "NAME":
			wantRead = []string{"metadata.name"}
		}
		if !reflect.DeepEqual(read, wantRead) {
			t.Errorf("%s: components read %v, want %v", name, read, wantRead)
		}
	}
}

// TestFieldGranularFamilyIsAskedBeforeTheCatchAll: the dispatch over All
// reaches the Kubernetes answer for a field-granular schema rather than
// AWS's identity-schema catch-all, which would refuse it (the provider
// serves no identity schema for any of the six).
func TestFieldGranularFamilyIsAskedBeforeTheCatchAll(t *testing.T) {
	block := fieldGranularSchemas()["kubernetes_config_map_v1_data"]
	synth, ok := SynthesizeIdentity("kubernetes_config_map_v1_data", providers.Schema{Block: block})
	if !ok || synth.FromIdentitySchema {
		t.Fatalf("SynthesizeIdentity fell through to the identity-schema route: %+v", synth)
	}
}
