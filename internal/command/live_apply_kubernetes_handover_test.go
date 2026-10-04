// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// handoverStubSweeper records the hand-overs asked of it.
type handoverStubSweeper struct {
	liveLsStubSweeper
	transfers []string
	fail      error
}

func (s *handoverStubSweeper) ReadObject(context.Context, kubesweep.ObjectRef) (*unstructured.Unstructured, bool, error) {
	return nil, false, nil
}

func (s *handoverStubSweeper) TransferFields(_ context.Context, ref kubesweep.ObjectRef, from, to string, writes []kubesweep.FieldWrite) (bool, error) {
	var members []string
	for _, w := range writes {
		members = append(members, w.Members...)
	}
	s.transfers = append(s.transfers, fmt.Sprintf("%s %s->%s %v", ref, from, to, members))
	return s.fail == nil, s.fail
}

// labelsShapeSchema is kubernetes_labels' schema at hashicorp/kubernetes
// 3.2.1, as far as the field-granular shape reads it.
func labelsShapeSchema() *providers.Schema {
	return &providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"api_version":   {Type: cty.String, Required: true},
			"kind":          {Type: cty.String, Required: true},
			"labels":        {Type: cty.Map(cty.String), Required: true},
			"field_manager": {Type: cty.String, Optional: true},
			"force":         {Type: cty.Bool, Optional: true},
			"id":            {Type: cty.String, Optional: true, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{"metadata": {Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1, Block: configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"name":      {Type: cty.String, Required: true},
				"namespace": {Type: cty.String, Optional: true},
			},
		}}},
	}}
}

func labelsValue(manager string, labels map[string]string) cty.Value {
	m := map[string]cty.Value{}
	for k, v := range labels {
		m[k] = cty.StringVal(v)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"api_version":   cty.StringVal("v1"),
		"kind":          cty.StringVal("ConfigMap"),
		"labels":        cty.MapVal(m),
		"field_manager": cty.StringVal(manager),
		"force":         cty.NullVal(cty.Bool),
		"id":            cty.StringVal("apiVersion=v1,kind=ConfigMap,namespace=ns,name=shared"),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("shared"),
			"namespace": cty.StringVal("ns"),
		})}),
	})
}

func labelsChange(t *testing.T, provider addrs.AbsProviderConfig, name string, action plans.Action, before, after cty.Value) *plans.ResourceInstanceChangeSrc {
	t.Helper()
	ty := labelsShapeSchema().Block.ImpliedType()
	b, err := plans.NewDynamicValue(before, ty)
	if err != nil {
		t.Fatal(err)
	}
	a, err := plans.NewDynamicValue(after, ty)
	if err != nil {
		t.Fatal(err)
	}
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_labels", Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	return &plans.ResourceInstanceChangeSrc{Addr: addr, PrevRunAddr: addr, ProviderAddr: provider, ChangeSrc: plans.ChangeSrc{Action: action, Before: b, After: a}}
}

// TestFieldGranularHandoversReadOnlyTheStockHandOver (GitHub issue #1863):
// the one planned change that moves field_manager from "Terraform" to the
// estate's is a hand-over, with the object and the keys it writes; an
// update already under the estate's manager, a create, and an update
// through a provider configuration the sweep holds no client for are not.
func TestFieldGranularHandoversReadOnlyTheStockHandOver(t *testing.T) {
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	other := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes"), Alias: "elsewhere"}
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		provider.Provider: {ResourceTypes: map[string]providers.Schema{"kubernetes_labels": *labelsShapeSchema()}},
	}}
	team := map[string]string{"team": "a"}
	null := cty.NullVal(labelsShapeSchema().Block.ImpliedType())
	changes := plans.NewChanges()
	sync := changes.SyncWrapper()
	sync.AppendResourceInstanceChange(labelsChange(t, provider, "migrated", plans.Update, labelsValue("Terraform", team), labelsValue("choudoufu:e", team)))
	sync.AppendResourceInstanceChange(labelsChange(t, provider, "steady", plans.Update, labelsValue("choudoufu:e", team), labelsValue("choudoufu:e", map[string]string{"team": "b"})))
	sync.AppendResourceInstanceChange(labelsChange(t, provider, "fresh", plans.Create, null, labelsValue("choudoufu:e", team)))
	sync.AppendResourceInstanceChange(labelsChange(t, other, "unswept", plans.Update, labelsValue("Terraform", team), labelsValue("choudoufu:e", team)))
	plan := &plans.Plan{Changes: changes}

	sweeper := &handoverStubSweeper{}
	provs := &projectionProviders{}
	provs.rememberKubernetesSweeper(provider, sweeper)
	sweepers := provs.kubernetesSweepers()

	got := fieldGranularHandovers(sweepers, plan, schemas, "e")
	if len(got) != 1 {
		t.Fatalf("hand-overs = %v, want one provider configuration's", got)
	}
	var writes []discovery.FieldGranularWrite
	for _, ws := range got {
		writes = ws
	}
	if len(writes) != 1 || writes[0].Addr.String() != "kubernetes_labels.migrated" || writes[0].HandoverFrom != "Terraform" {
		t.Fatalf("hand-overs = %+v, want kubernetes_labels.migrated alone", writes)
	}
	if want := (kubesweep.ObjectRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns", Name: "shared"}); writes[0].Object != want {
		t.Errorf("object = %+v, want %+v", writes[0].Object, want)
	}

	diags := runFieldGranularHandovers(context.Background(), sweepers, got, "e")
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %s", diags.ErrWithWarnings())
	}
	want := []string{"apiVersion=v1,kind=ConfigMap,namespace=ns,name=shared Terraform->choudoufu:e [f:team]"}
	if !reflect.DeepEqual(sweeper.transfers, want) {
		t.Errorf("transfers = %v, want %v", sweeper.transfers, want)
	}

	if none := fieldGranularHandovers(sweepers, plan, schemas, ""); none != nil {
		t.Errorf("a run with no estate planned hand-overs: %v", none)
	}
}

// TestFieldGranularHandoverFailureIsAWarning: a hand-over that cannot be
// made, by a client that fails or one that cannot move ownership at all,
// leaves the apply to go ahead with the fields shared, and says so.
func TestFieldGranularHandoverFailureIsAWarning(t *testing.T) {
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_labels", Name: "migrated"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	w := discovery.FieldGranularWrite{
		Addr:         addr,
		Object:       kubesweep.ObjectRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns", Name: "shared"},
		Writes:       []kubesweep.FieldWrite{{Root: []string{"f:metadata", "f:labels"}, Members: []string{"f:team"}}},
		HandoverFrom: kubesweep.DefaultFieldManager,
	}
	for name, sweeper := range map[string]kubesweep.Sweeper{
		"fails":  &handoverStubSweeper{fail: errors.New("409 conflict")},
		"cannot": &liveLsStubSweeper{},
	} {
		provs := &projectionProviders{}
		provs.rememberKubernetesSweeper(provider, sweeper)
		sweepers := provs.kubernetesSweepers()
		diags := runFieldGranularHandovers(context.Background(), sweepers, map[string][]discovery.FieldGranularWrite{providerCacheKey(provider): {w}}, "e")
		if len(diags) != 1 || diags.HasErrors() || diags[0].Description().Summary != discovery.SummaryFieldHandoverFailed {
			t.Errorf("%s: diagnostics = %v, want one %q warning", name, diags.ErrWithWarnings(), discovery.SummaryFieldHandoverFailed)
			continue
		}
		if !strings.Contains(diags[0].Description().Detail, "kubernetes_labels.migrated") {
			t.Errorf("%s: the warning does not name the instance: %s", name, diags[0].Description().Detail)
		}
	}
}
