// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1649. The guard read the AWS tags map alone, so a
// state-backed plan that drops the tofu-estate label from a Kubernetes
// object live-import stamped passed it, and the apply un-migrated the
// object. Measured on kind with hashicorp/kubernetes 3.2.1: the plan
// proposes `- "tofu-estate" = "ms1649" -> null` in metadata.labels on a
// kubernetes_config_map_v1 and a kubernetes_namespace, exits 0, and the
// apply removes both labels.
//
// The mock provider's type has the object-metadata shape every
// hashicorp/kubernetes typed resource shares; the surface is read off the
// schema, never the provider or type name, so the shape is the whole of
// what makes this a label surface.

func markerStripLabelProvider(stamped bool) *tofu.MockProvider {
	p := testProvider()
	p.GetProviderSchemaResponse = &providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"test_instance": {
				Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id": {Type: cty.String, Optional: true, Computed: true},
					},
					BlockTypes: map[string]*configschema.NestedBlock{
						"metadata": {
							Nesting:  configschema.NestingList,
							MinItems: 1,
							MaxItems: 1,
							Block: configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"name":   {Type: cty.String, Optional: true},
									"labels": {Type: cty.Map(cty.String), Optional: true},
								},
							},
						},
					},
				},
			},
		},
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		labels := map[string]cty.Value{"team": cty.StringVal("a")}
		if stamped {
			labels["tofu-estate"] = cty.StringVal(markerStripEstate)
		}
		return providers.ReadResourceResponse{
			NewState: cty.ObjectVal(map[string]cty.Value{
				"id": cty.StringVal("default/foo"),
				"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
					"name":   cty.StringVal("foo"),
					"labels": cty.MapVal(labels),
				})}),
			}),
		}
	}
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}
	return p
}

func markerStripLabelState() *states.State {
	return states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: "test_instance",
				Name: "foo",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			&states.ResourceInstanceObjectSrc{
				AttrsJSON:    []byte(`{"id":"default/foo","metadata":[{"name":"foo","labels":{"team":"a"}}]}`),
				Status:       states.ObjectReady,
				Dependencies: []addrs.ConfigResource{},
			},
			addrs.AbsProviderConfig{
				Provider: addrs.NewDefaultProvider("test"),
				Module:   addrs.RootModule,
			},
			addrs.NoKey,
		)
	}).DeepCopy()
}

func TestPlan_statefulPlanStrippingALabelIsRefused(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("plan-marker-strip-labels"), td)
	t.Chdir(td)
	statePath := testStateFile(t, markerStripLabelState())
	view, done := testView(t)
	c := &PlanCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(markerStripLabelProvider(true)),
		View:             view,
	}}

	code := c.Run([]string{"-state", statePath, "-no-color"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit status %d, want 1\n\n%s", code, output.All())
	}
	for _, want := range []string{
		`- "tofu-estate" = "team-estate" -> null`,
		"1 to change",
	} {
		if !saidInAStream(output, want) {
			t.Errorf("plan output does not contain %q\n\n%s", want, output.All())
		}
	}
	for _, want := range []string{
		summaryUnmigrateRefused,
		`"team-estate"`,
		"tofu-estate label from",
		"labels are what",
		"does not declare those labels",
		"test_instance.foo",
		"CHOUDOUFU_UNMIGRATE=team-estate",
	} {
		if !saidInAStream(output, want) {
			t.Errorf("refusal does not contain %q\n\n%s", want, output.All())
		}
	}
	if saidInAStream(output, "Those tags") || saidInAStream(output, "tofu-estate tag ") {
		t.Errorf("the refusal calls a Kubernetes label a tag\n\n%s", output.All())
	}
}

// The control: the same run with no estate label on the live object.
func TestPlan_statefulPlanOnAnUnstampedLabelEstateIsNotRefused(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("plan-marker-strip-labels"), td)
	t.Chdir(td)
	statePath := testStateFile(t, markerStripLabelState())
	view, done := testView(t)
	c := &PlanCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(markerStripLabelProvider(false)),
		View:             view,
	}}

	code := c.Run([]string{"-state", statePath, "-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit status %d, want 0\n\n%s", code, output.All())
	}
	if saidInAStream(output, summaryUnmigrateRefused) {
		t.Errorf("unstamped estate was refused\n\n%s", output.All())
	}
}

func TestPlan_stockModeLabelledCreateFromNothingWarns(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("plan-marker-create-labels"), td)
	t.Chdir(td)
	view, done := testView(t)
	c := &PlanCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(markerStripLabelProvider(false)),
		View:             view,
	}}

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit status %d, want 0 (a warning, never a refusal)\n\n%s", code, output.All())
	}
	for _, want := range []string{
		"This plan creates resources already stamped with ownership markers",
		`"team-estate"`,
	} {
		if !saidInAStream(output, want) {
			t.Errorf("output does not contain %q\n\n%s", want, output.All())
		}
	}
}
