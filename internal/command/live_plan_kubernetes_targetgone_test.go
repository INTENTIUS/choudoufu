// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestFieldGranularMissingObjectRefusedUnlessThePlanCreatesIt (#1885,
// ruled 2026-10-04): a planned create of a field-granular block whose
// patched object discovery found missing is refused by name - reference-
// k8s-shared-objects' teardown, where platform deleted app's objects -
// unless the same plan creates the object. corpus-govuk-cluster-services'
// greenfield is the second case: kubernetes_labels.argocd_secret patches a
// Secret its own root's kubernetes_secret_v1.dex_client creates, and the
// refusal stopped a fresh apply on an empty cluster. A planned object whose
// name is not known yet could be the one, so it suppresses the refusal; a
// destroy (no create) never refuses.
func TestFieldGranularMissingObjectRefusedUnlessThePlanCreatesIt(t *testing.T) {
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	labels := *labelsShapeSchema()
	cm := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Optional: true, Computed: true},
			"data": {Type: cty.Map(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{"metadata": {Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1, Block: configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"name":      {Type: cty.String, Optional: true, Computed: true},
				"namespace": {Type: cty.String, Optional: true},
				"labels":    {Type: cty.Map(cty.String), Optional: true},
			},
		}}},
	}}
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		provider.Provider: {ResourceTypes: map[string]providers.Schema{
			"kubernetes_labels":        labels,
			"kubernetes_config_map_v1": cm,
		}},
	}}
	mk := func(typeName, name string, schema providers.Schema, action plans.Action, after cty.Value) *plans.ResourceInstanceChangeSrc {
		ty := schema.Block.ImpliedType()
		b, err := plans.NewDynamicValue(cty.NullVal(ty), ty)
		if err != nil {
			t.Fatal(err)
		}
		before := b
		if action != plans.Create {
			if before, err = plans.NewDynamicValue(after, ty); err != nil {
				t.Fatal(err)
			}
		}
		a, err := plans.NewDynamicValue(after, ty)
		if err != nil {
			t.Fatal(err)
		}
		addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
		return &plans.ResourceInstanceChangeSrc{Addr: addr, PrevRunAddr: addr, ProviderAddr: provider, ChangeSrc: plans.ChangeSrc{Action: action, Before: before, After: a}}
	}
	configMap := func(name cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"id":   cty.UnknownVal(cty.String),
			"data": cty.NullVal(cty.Map(cty.String)),
			"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"name":      name,
				"namespace": cty.StringVal("ns"),
				"labels":    cty.NullVal(cty.Map(cty.String)),
			})}),
		})
	}
	write := labelsValue("choudoufu:e", map[string]string{"team": "a"}) // ConfigMap ns/shared
	missing := map[string]string{"kubernetes_labels.web": "ConfigMap ns/shared"}

	cases := map[string]struct {
		labelsAction plans.Action
		object       cty.Value // a kubernetes_config_map_v1 create, or NilVal for none
		refused      bool
	}{
		"object gone, nothing creates it":          {plans.Create, cty.NilVal, true},
		"the same plan creates the object":         {plans.Create, configMap(cty.StringVal("shared")), false},
		"the plan creates an object, name unknown": {plans.Create, configMap(cty.UnknownVal(cty.String)), false},
		"the plan creates another object":          {plans.Create, configMap(cty.StringVal("other")), true},
		"not a create (a destroy plan's no-op)":    {plans.NoOp, cty.NilVal, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			changes := plans.NewChanges()
			sync := changes.SyncWrapper()
			sync.AppendResourceInstanceChange(mk("kubernetes_labels", "web", labels, tc.labelsAction, write))
			if tc.object != cty.NilVal {
				sync.AppendResourceInstanceChange(mk("kubernetes_config_map_v1", "shared", cm, plans.Create, tc.object))
			}
			diags := collectKubernetesFieldOwners(context.Background(), nil, nil, &plans.Plan{Changes: changes}, schemas, "e", missing)
			refused := false
			for _, d := range diags {
				if d.Description().Summary == discovery.SummaryFieldGranularTargetMissing {
					refused = true
					if detail := d.Description().Detail; !strings.Contains(detail, "kubernetes_labels.web writes fields of ConfigMap ns/shared") {
						t.Errorf("the refusal does not name the block and the object: %s", detail)
					}
				}
			}
			if refused != tc.refused {
				t.Fatalf("refused = %v, want %v (diagnostics: %v)", refused, tc.refused, diags.ErrWithWarnings())
			}
		})
	}
}
