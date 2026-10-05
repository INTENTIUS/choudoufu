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
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestSameObjectRefusalCountsUnchangedBlocks (epic #1885, measured on kind
// by reference-k8s-shared-objects' plan_approval): a new field-granular
// block on an object another of the estate's blocks already patches - a
// kubernetes_annotations added to the Deployment kubernetes_env.web writes
// into - planned "1 to add" at exit 0. The existing block had nothing to
// change, so its change was a no-op, and the same-object refusal only
// counted planned creates and updates. Both blocks write under the one
// field manager whatever either's action is, so every field-granular
// instance the plan keeps counts; one being destroyed does not.
func TestSameObjectRefusalCountsUnchangedBlocks(t *testing.T) {
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	schema := *labelsShapeSchema()
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		provider.Provider: {ResourceTypes: map[string]providers.Schema{
			"kubernetes_labels":      schema,
			"kubernetes_annotations": schema,
		}},
	}}
	ty := schema.Block.ImpliedType()
	change := func(typeName, name string, action plans.Action, before, after cty.Value) *plans.ResourceInstanceChangeSrc {
		b, err := plans.NewDynamicValue(before, ty)
		if err != nil {
			t.Fatal(err)
		}
		a, err := plans.NewDynamicValue(after, ty)
		if err != nil {
			t.Fatal(err)
		}
		addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
		return &plans.ResourceInstanceChangeSrc{Addr: addr, PrevRunAddr: addr, ProviderAddr: provider, ChangeSrc: plans.ChangeSrc{Action: action, Before: b, After: a}}
	}
	null := cty.NullVal(ty)
	held := labelsValue("choudoufu:e", map[string]string{"team": "a"})
	fresh := labelsValue("choudoufu:e", map[string]string{"extra": "app"})

	cases := map[string]struct {
		existing plans.Action
		refused  bool
	}{
		"unchanged":       {plans.NoOp, true},
		"updated":         {plans.Update, true},
		"being destroyed": {plans.Delete, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			after := held
			if tc.existing == plans.Delete {
				after = null
			}
			changes := plans.NewChanges()
			sync := changes.SyncWrapper()
			sync.AppendResourceInstanceChange(change("kubernetes_labels", "web", tc.existing, held, after))
			sync.AppendResourceInstanceChange(change("kubernetes_annotations", "web_extra", plans.Create, null, fresh))
			diags := collectKubernetesFieldOwners(context.Background(), nil, nil, &plans.Plan{Changes: changes}, schemas, "e", nil)
			refused := false
			for _, d := range diags {
				if d.Description().Summary == discovery.SummaryFieldGranularSameObject {
					refused = true
					detail := d.Description().Detail
					if !strings.Contains(detail, "kubernetes_labels.web") || !strings.Contains(detail, "kubernetes_annotations.web_extra") {
						t.Errorf("the refusal does not name both blocks: %s", detail)
					}
				}
			}
			if refused != tc.refused {
				t.Fatalf("refused = %v, want %v (diagnostics: %v)", refused, tc.refused, diags.ErrWithWarnings())
			}
		})
	}
}
