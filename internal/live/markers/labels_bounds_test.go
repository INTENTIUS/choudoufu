// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// frameworkNamespaceBlock is kubernetes_namespace_v1 as hashicorp/kubernetes
// 3.3.0 serves it: the same metadata fields as 3.2.1, but the block's
// bounds are undeclared (min_items and max_items both absent from
// `tofu providers schema -json`) and the description says "Exactly one
// metadata block is required" instead. That is how a plugin-framework
// list block renders: its size limit is a validator, which the wire
// schema cannot carry.
func frameworkNamespaceBlock() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":                               {Type: cty.String, Computed: true},
			"wait_for_default_service_account": {Type: cty.Bool, Optional: true, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting: configschema.NestingList,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"annotations":      {Type: cty.Map(cty.String), Optional: true},
						"generate_name":    {Type: cty.String, Optional: true},
						"generation":       {Type: cty.Number, Computed: true},
						"labels":           {Type: cty.Map(cty.String), Optional: true},
						"name":             {Type: cty.String, Optional: true, Computed: true},
						"resource_version": {Type: cty.String, Computed: true},
						"uid":              {Type: cty.String, Computed: true},
					},
				},
			},
			"timeouts": {
				Nesting: configschema.NestingSingle,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"delete": {Type: cty.String, Optional: true},
				}},
			},
		},
	}
}

// TestLabelSurfaceUndeclaredBounds: 3.3.0's kubernetes_namespace_v1 is a
// label surface. Before the fix it was not, which is the one instance
// corpus-quickpizza's migrate stage skipped on 2026-10-02.
func TestLabelSurfaceUndeclaredBounds(t *testing.T) {
	if _, ok := LabelSurface(frameworkNamespaceBlock()); !ok {
		t.Fatal("hashicorp/kubernetes 3.3.0's kubernetes_namespace_v1 metadata block is not a label surface")
	}

	// Controls: undeclared bounds are accepted only for a block that names
	// one object (a settable name and a server-minted uid), and a declared
	// lower bound with no upper one is still a genuinely unbounded list.
	for name, mutate := range map[string]func(*configschema.Block){
		"undeclared bounds, no uid": func(b *configschema.Block) { delete(b.BlockTypes["metadata"].Block.Attributes, "uid") },
		"undeclared bounds, name computed only": func(b *configschema.Block) {
			b.BlockTypes["metadata"].Block.Attributes["name"] = &configschema.Attribute{Type: cty.String, Computed: true}
		},
		"undeclared bounds, no name": func(b *configschema.Block) { delete(b.BlockTypes["metadata"].Block.Attributes, "name") },
		"min 1, no max":              func(b *configschema.Block) { b.BlockTypes["metadata"].MinItems = 1 },
		"max 2":                      func(b *configschema.Block) { b.BlockTypes["metadata"].MaxItems = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			b := frameworkNamespaceBlock()
			mutate(b)
			if _, ok := LabelSurface(b); ok {
				t.Errorf("%s: reported as a label surface", name)
			}
		})
	}
}
