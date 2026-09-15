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
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The command layer's half of the server-side dry run (GitHub issue
// #1081, item 3): which planned changes are submitted, decided by schema
// shape and action, and what the object sent is - the planned manifest
// with its label, as plain JSON values.

// dryRunStubSweeper records every dry run and refuses the objects named in
// reject.
type dryRunStubSweeper struct {
	liveLsStubSweeper
	submitted []map[string]any
	updates   []bool
	reject    map[string]string
}

func (s *dryRunStubSweeper) DryRun(_ context.Context, manifest map[string]any, update bool) (kubesweep.DryRunResult, error) {
	s.submitted = append(s.submitted, manifest)
	s.updates = append(s.updates, update)
	name, _ := manifest["metadata"].(map[string]any)["name"].(string)
	if msg, refused := s.reject[name]; refused {
		return kubesweep.DryRunResult{Message: msg}, nil
	}
	return kubesweep.DryRunResult{Accepted: true, Defaulted: 1}, nil
}

// manifestShapeSchema is the manifest surface by shape (markers.ManifestSurface):
// a required dynamic manifest, a computed dynamic object, no metadata
// block and no tags map. The type name in the plan is a test's own.
func manifestShapeSchema() *providers.Schema {
	return &providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"manifest": {Type: cty.DynamicPseudoType, Required: true},
		"object":   {Type: cty.DynamicPseudoType, Computed: true},
	}}}
}

// metadataShapeSchema is the label surface by shape (markers.LabelSurface):
// a metadata block with a name and a labels map.
func metadataShapeSchema() *providers.Schema {
	return &providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{"data": {Type: cty.Map(cty.String), Optional: true}},
		BlockTypes: map[string]*configschema.NestedBlock{"metadata": {Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1, Block: configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"name":   {Type: cty.String, Required: true},
				"labels": {Type: cty.Map(cty.String), Optional: true},
			},
		}}},
	}}
}

func metadataShapeChange(t *testing.T, provider addrs.AbsProviderConfig, typeName, name, objectName string) *plans.ResourceInstanceChangeSrc {
	t.Helper()
	val := cty.ObjectVal(map[string]cty.Value{
		"data": cty.NullVal(cty.Map(cty.String)),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":   cty.StringVal(objectName),
			"labels": cty.NullVal(cty.Map(cty.String)),
		})}),
	})
	after, err := plans.NewDynamicValue(val, metadataShapeSchema().Block.ImpliedType())
	if err != nil {
		t.Fatal(err)
	}
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	return &plans.ResourceInstanceChangeSrc{Addr: addr, PrevRunAddr: addr, ProviderAddr: provider, ChangeSrc: plans.ChangeSrc{Action: plans.Create, After: after}}
}

func crontabManifestIn(name, namespace string) cty.Value {
	m := crontabManifest(name, cty.NumberIntVal(1)).AsValueMap()
	meta := m["metadata"].AsValueMap()
	meta["namespace"] = cty.StringVal(namespace)
	m["metadata"] = cty.ObjectVal(meta)
	return cty.ObjectVal(m)
}

func dryRunChange(t *testing.T, provider addrs.AbsProviderConfig, typeName, name string, action plans.Action, schema *providers.Schema, manifest cty.Value) *plans.ResourceInstanceChangeSrc {
	t.Helper()
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	ty := schema.Block.ImpliedType()
	val := cty.NullVal(ty)
	if manifest != cty.NilVal {
		val = cty.ObjectVal(map[string]cty.Value{
			"manifest": manifest,
			"object":   cty.NullVal(cty.DynamicPseudoType),
		})
	}
	after, err := plans.NewDynamicValue(val, ty)
	if err != nil {
		t.Fatal(err)
	}
	before, err := plans.NewDynamicValue(cty.NullVal(ty), ty)
	if err != nil {
		t.Fatal(err)
	}
	return &plans.ResourceInstanceChangeSrc{
		Addr:         addr,
		PrevRunAddr:  addr,
		ProviderAddr: provider,
		ChangeSrc:    plans.ChangeSrc{Action: action, Before: before, After: after},
	}
}

func crontabManifest(name string, replicas cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("stable.example.com/v1"),
		"kind":       cty.StringVal("CronTab"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal(name),
			"namespace": cty.StringVal("smoke-crd"),
			"labels":    cty.ObjectVal(map[string]cty.Value{"tofu-estate": cty.StringVal("smoke-crd")}),
		}),
		"spec": cty.ObjectVal(map[string]cty.Value{
			"cronSpec": cty.StringVal("* * * * */5"),
			"replicas": replicas,
		}),
	})
}

// TestStatelessKubernetesDryRunSubmitsPlannedManifests: a create and an
// update of a manifest-shaped instance are submitted, with the verb the
// plan proposed and the planned manifest as JSON values (the stamped
// label present, a number a number); a delete, a built-in metadata-shaped
// type and an instance under a provider with no client are not; a
// manifest with a value unknown until apply is reported, never sent; and
// the server's rejection is the refusal by name.
func TestStatelessKubernetesDryRunSubmitsPlannedManifests(t *testing.T) {
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	other := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes"), Alias: "unreachable"}
	const manifestType, metadataType, namespaceType = "kubernetes_manifest", "kubernetes_config_map", "kubernetes_namespace_v1"
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		provider.Provider: {ResourceTypes: map[string]providers.Schema{
			manifestType:  *manifestShapeSchema(),
			metadataType:  *metadataShapeSchema(),
			namespaceType: *metadataShapeSchema(),
		}},
	}}
	changes := plans.NewChanges()
	sync := changes.SyncWrapper()
	sync.AppendResourceInstanceChange(dryRunChange(t, provider, manifestType, "crontab", plans.Create, manifestShapeSchema(), crontabManifest("my-crontab", cty.NumberIntVal(3))))
	sync.AppendResourceInstanceChange(dryRunChange(t, provider, manifestType, "changed", plans.Update, manifestShapeSchema(), crontabManifest("changed", cty.StringVal("three"))))
	sync.AppendResourceInstanceChange(dryRunChange(t, provider, manifestType, "gone", plans.Delete, manifestShapeSchema(), cty.NilVal))
	sync.AppendResourceInstanceChange(dryRunChange(t, provider, manifestType, "later", plans.Create, manifestShapeSchema(), crontabManifest("later", cty.UnknownVal(cty.Number))))
	sync.AppendResourceInstanceChange(dryRunChange(t, other, manifestType, "elsewhere", plans.Create, manifestShapeSchema(), crontabManifest("elsewhere", cty.NumberIntVal(1))))
	sync.AppendResourceInstanceChange(metadataShapeChange(t, provider, metadataType, "cm", "cm"))
	// A namespace this same plan creates, through a built-in type, and a
	// manifest inside it: the server would say 404, which is the apply's
	// order and not the object's validity, so it is not submitted.
	sync.AppendResourceInstanceChange(metadataShapeChange(t, provider, namespaceType, "fresh", "fresh"))
	sync.AppendResourceInstanceChange(dryRunChange(t, provider, manifestType, "inside", plans.Create, manifestShapeSchema(), crontabManifestIn("inside", "fresh")))
	// The same through a manifest of kind Namespace.
	sync.AppendResourceInstanceChange(dryRunChange(t, provider, manifestType, "ns", plans.Create, manifestShapeSchema(), cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("v1"), "kind": cty.StringVal("Namespace"),
		"metadata": cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("newer")}),
	})))
	sync.AppendResourceInstanceChange(dryRunChange(t, provider, manifestType, "inside_newer", plans.Create, manifestShapeSchema(), crontabManifestIn("inside-newer", "newer")))
	plan := &plans.Plan{Changes: changes}

	sweeper := &dryRunStubSweeper{reject: map[string]string{"changed": `CronTab.stable.example.com "changed" is invalid: spec.replicas: Invalid value: "string": spec.replicas in body must be of type integer`}}
	provs := &statelessProviders{}
	provs.rememberKubernetesSweeper(provider, sweeper)

	evidence, diags := statelessKubernetesDryRun(context.Background(), provs.kubernetesSweepers(), nil, plan, schemas)

	if len(sweeper.submitted) != 3 {
		t.Fatalf("submitted %d objects, want 3 (the create, the update and the Namespace manifest): %v", len(sweeper.submitted), sweeper.submitted)
	}
	// Address order: changed (update), crontab (create), ns (create).
	if !sweeper.updates[0] || sweeper.updates[1] || sweeper.updates[2] {
		t.Errorf("verbs = %v, want [update create create] in address order", sweeper.updates)
	}
	created := sweeper.submitted[1]
	if labels := created["metadata"].(map[string]any)["labels"].(map[string]any); labels["tofu-estate"] != "smoke-crd" {
		t.Errorf("the submitted object lost the stamped label: %v", created["metadata"])
	}
	if replicas := created["spec"].(map[string]any)["replicas"]; replicas != float64(3) {
		t.Errorf("spec.replicas was sent as %T %v, want the number 3", replicas, replicas)
	}
	if replicas := sweeper.submitted[0]["spec"].(map[string]any)["replicas"]; replicas != "three" {
		t.Errorf("the update's spec.replicas was sent as %T %v, want the string the plan carries", replicas, replicas)
	}

	var addrsSeen []string
	for _, e := range evidence {
		addrsSeen = append(addrsSeen, e.Addr)
	}
	if want := "kubernetes_manifest.changed kubernetes_manifest.crontab kubernetes_manifest.inside kubernetes_manifest.inside_newer kubernetes_manifest.later kubernetes_manifest.ns"; strings.Join(addrsSeen, " ") != want {
		t.Errorf("evidence for %q, want %q: a delete, a built-in type and a provider with no client never appear", strings.Join(addrsSeen, " "), want)
	}
	if e := evidence[1]; e.NotSubmitted != "" || e.Kind != "CronTab" || e.Namespace != "smoke-crd" || e.Name != "my-crontab" || e.Defaulted != 1 {
		t.Errorf("the create's evidence: %+v", e)
	}
	if e := evidence[2]; !strings.Contains(e.NotSubmitted, "namespace fresh is created by this same plan") {
		t.Errorf("the object inside a namespace a built-in type creates: %+v", e)
	}
	if e := evidence[3]; !strings.Contains(e.NotSubmitted, "namespace newer is created by this same plan") {
		t.Errorf("the object inside a namespace a manifest creates: %+v", e)
	}
	if e := evidence[4]; !strings.Contains(e.NotSubmitted, "not known until apply") {
		t.Errorf("the unknown manifest's evidence: %+v", e)
	}
	if e := evidence[5]; e.NotSubmitted != "" || e.Kind != "Namespace" || e.Name != "newer" {
		t.Errorf("the Namespace manifest itself is submitted: %+v", e)
	}
	if !diags.HasErrors() {
		t.Fatalf("the server's rejection did not refuse the plan: %v", diags)
	}
	found := false
	for _, d := range diags {
		if d.Description().Summary == discovery.SummaryKubernetesDryRunRejected {
			found = true
			if detail := d.Description().Detail; !strings.Contains(detail, "kubernetes_manifest.changed") || !strings.Contains(detail, "must be of type integer") {
				t.Errorf("the refusal does not name the instance and quote the server: %s", detail)
			}
		}
	}
	if !found {
		t.Errorf("no %q diagnostic: %v", discovery.SummaryKubernetesDryRunRejected, diags)
	}
}

// TestStatelessKubernetesDryRunNothingToDo: no client kept, or no plan,
// says nothing at all.
func TestStatelessKubernetesDryRunNothingToDo(t *testing.T) {
	if ev, diags := statelessKubernetesDryRun(context.Background(), nil, nil, &plans.Plan{Changes: plans.NewChanges()}, &tofu.Schemas{}); ev != nil || len(diags) != 0 {
		t.Errorf("with no client: %+v %v", ev, diags)
	}
	provs := &statelessProviders{}
	provs.rememberKubernetesSweeper(addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}, &dryRunStubSweeper{})
	if ev, diags := statelessKubernetesDryRun(context.Background(), provs.kubernetesSweepers(), nil, nil, &tofu.Schemas{}); ev != nil || len(diags) != 0 {
		t.Errorf("with no plan: %+v %v", ev, diags)
	}
}
