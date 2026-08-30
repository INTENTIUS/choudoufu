// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #613. Everything in this file asserts on RENDERED CLI output,
// never on a predicate, because the defect it guards is precisely a marker
// being wrong while every boolean in sight reads fine.
//
// The scenario each test builds is the one #611 measured against the
// emulator, shrunk to one resource:
//
//  1. an estate applied and recorded in a state file, before any migration;
//  2. "choudoufu live-import -approve" stamps tofu-estate and tofu-address
//     onto the LIVE resource and does not touch the state file or the .tf;
//  3. a state-backed plan in that directory refreshes, reads the two tags
//     the configuration does not declare, and proposes removing them.
//
// The mock provider below is step 2: its ReadResource returns the stamped
// object, which is what the refresh in step 3 sees.

const (
	markerStripEstate  = "team-estate"
	markerStripAddress = "test_instance.foo"
)

// markerStripProvider is a provider for testdata/plan-marker-strip whose
// live object carries ownership markers the configuration does not declare.
//
// stamped=false gives the same provider with an unstamped live object, which
// is the control: the identical run against an estate that was never
// migrated must not be refused.
func markerStripProvider(stamped bool) *tofu.MockProvider {
	p := testProvider()
	p.GetProviderSchemaResponse = &providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"test_instance": {
				Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id":   {Type: cty.String, Optional: true, Computed: true},
						"ami":  {Type: cty.String, Optional: true},
						"tags": {Type: cty.Map(cty.String), Optional: true},
					},
				},
			},
		},
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		tags := map[string]cty.Value{"Name": cty.StringVal("foo")}
		if stamped {
			tags["tofu-estate"] = cty.StringVal(markerStripEstate)
			tags["tofu-address"] = cty.StringVal(markerStripAddress)
		}
		return providers.ReadResourceResponse{
			NewState: cty.ObjectVal(map[string]cty.Value{
				"id":   cty.StringVal("bar"),
				"ami":  cty.StringVal("bar"),
				"tags": cty.MapVal(tags),
			}),
		}
	}
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}
	return p
}

// markerStripState is the state file as it stands after a stock apply and
// before any migration: it records the tag the configuration declares and
// has no idea the markers exist. That absence is the whole defect - it is
// why a state-backed refresh reads the markers as drift.
func markerStripState() *states.State {
	return states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: "test_instance",
				Name: "foo",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			&states.ResourceInstanceObjectSrc{
				AttrsJSON:    []byte(`{"id":"bar","ami":"bar","tags":{"Name":"foo"}}`),
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

// markerStripDir copies the fixture into a temporary working directory,
// chdirs into it, writes the pre-migration state file and returns its path.
func markerStripDir(t *testing.T) string {
	t.Helper()
	td := t.TempDir()
	testCopyDir(t, testFixturePath("plan-marker-strip"), td)
	t.Chdir(td)
	return testStateFile(t, markerStripState())
}

// TestPlan_statefulPlanStrippingMarkersIsRefused is the reproduction and the
// refusal in one run. It asserts three things about what the operator sees:
// the honest diff is still printed in full, the refusal names the estate,
// and the exit status is a failure.
func TestPlan_statefulPlanStrippingMarkersIsRefused(t *testing.T) {
	statePath := markerStripDir(t)
	p := markerStripProvider(true)
	view, done := testView(t)
	c := &PlanCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(p),
		View:             view,
	}}

	code := c.Run([]string{"-state", statePath, "-no-color"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit status %d, want 1\n\n%s", code, output.All())
	}

	// The reproduction: the plan really does propose removing both markers,
	// and the operator is shown it rather than having it hidden by the
	// refusal. Asserted on the rendered diff, not on the plan object.
	stdout := output.Stdout()
	for _, want := range []string{
		`~ resource "test_instance" "foo"`,
		`- "tofu-address" = "test_instance.foo" -> null`,
		`- "tofu-estate"  = "team-estate" -> null`,
		"1 to change",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan output does not contain %q\n\n%s", want, stdout)
		}
	}

	// The refusal. Every wanted string here is short enough to survive the
	// diagnostic renderer's word wrapping intact; a longer phrase would be
	// asserting on where the wrap happens to fall.
	all := output.All()
	for _, want := range []string{
		summaryUnmigrateRefused,
		`"team-estate"`,
		"tofu-address and tofu-estate tags",
		"test_instance.foo",
		"CHOUDOUFU_UNMIGRATE=team-estate",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("refusal does not contain %q\n\n%s", want, all)
		}
	}

	// A refused plan must not invite the operator to apply it.
	if strings.Contains(all, "choudoufu apply") && strings.Contains(all, "Saved the plan") {
		t.Errorf("refused plan still printed a next-step hint\n\n%s", all)
	}
}

// TestPlan_statefulPlanOnAnUnstampedEstateIsNotRefused is the control that
// keeps the refusal honest: the same fixture, the same command, the same
// drift-shaped refresh, with no markers on the live object. A guard that
// fired here would be refusing working configurations.
func TestPlan_statefulPlanOnAnUnstampedEstateIsNotRefused(t *testing.T) {
	statePath := markerStripDir(t)
	p := markerStripProvider(false)
	view, done := testView(t)
	c := &PlanCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(p),
		View:             view,
	}}

	code := c.Run([]string{"-state", statePath, "-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit status %d, want 0\n\n%s", code, output.All())
	}
	if strings.Contains(output.All(), summaryUnmigrateRefused) {
		t.Errorf("unstamped estate was refused\n\n%s", output.All())
	}
}

// TestPlan_statefulMarkerStripApprovedByEnvVar proves the deliberate revert
// is still possible, and that it is loud when it happens.
func TestPlan_statefulMarkerStripApprovedByEnvVar(t *testing.T) {
	statePath := markerStripDir(t)
	t.Setenv(UnmigrateEnvVar, markerStripEstate)
	p := markerStripProvider(true)
	view, done := testView(t)
	c := &PlanCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(p),
		View:             view,
	}}

	code := c.Run([]string{"-state", statePath, "-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit status %d, want 0\n\n%s", code, output.All())
	}
	all := output.All()
	if strings.Contains(all, summaryUnmigrateRefused) {
		t.Errorf("%s named the estate and the run was still refused\n\n%s", UnmigrateEnvVar, all)
	}
	if !strings.Contains(all, summaryUnmigrateApproved) {
		t.Errorf("approved revert was silent; want %q\n\n%s", summaryUnmigrateApproved, all)
	}
}

// TestPlan_statefulMarkerStripEnvVarNamingAnotherEstateStillRefuses is why
// the variable takes an estate name rather than a bare on/off value: a
// setting left in a CI environment covers the estate someone was looking at
// then, and must not cover an estate migrated later.
func TestPlan_statefulMarkerStripEnvVarNamingAnotherEstateStillRefuses(t *testing.T) {
	statePath := markerStripDir(t)
	t.Setenv(UnmigrateEnvVar, "some-other-estate")
	p := markerStripProvider(true)
	view, done := testView(t)
	c := &PlanCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(p),
		View:             view,
	}}

	code := c.Run([]string{"-state", statePath, "-no-color"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit status %d, want 1\n\n%s", code, output.All())
	}
	if !strings.Contains(output.All(), summaryUnmigrateRefused) {
		t.Errorf("run was not refused\n\n%s", output.All())
	}
}

// TestApply_statefulApplyStrippingMarkersIsRefused is the test that matters
// most: -auto-approve is the form of this run that destroys a migration with
// nobody watching, and there is no prompt in it for a warning to appear
// before. The assertion is that the provider was never asked to apply
// anything - the markers are still on the live resource afterwards.
func TestApply_statefulApplyStrippingMarkersIsRefused(t *testing.T) {
	statePath := markerStripDir(t)
	p := markerStripProvider(true)
	view, done := testView(t)
	c := &ApplyCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(p),
		View:             view,
	}}

	code := c.Run([]string{"-state", statePath, "-auto-approve", "-no-color"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit status %d, want 1\n\n%s", code, output.All())
	}
	if !strings.Contains(output.All(), summaryUnmigrateRefused) {
		t.Errorf("apply was not refused\n\n%s", output.All())
	}
	if p.ApplyResourceChangeCalled {
		t.Error("ApplyResourceChange was called: the markers were stripped from the live resource")
	}
}

// TestApply_statefulSavedPlanStrippingMarkersIsRefused closes the route
// around the plan-time refusal. "plan -out" is not refused on the file it
// writes until the plan is rendered, so a file does get written; applying it
// is where the estate would actually be un-migrated, and that is refused by
// the second of the guard's two call sites in opApply.
func TestApply_statefulSavedPlanStrippingMarkersIsRefused(t *testing.T) {
	statePath := markerStripDir(t)
	planPath := filepath.Join(t.TempDir(), "tfplan")

	// Write the plan file with the guard deliberately out of the way, so
	// that this test exercises the apply-side refusal rather than the
	// plan-side one. A run that reaches "apply tfplan" with a file this old
	// is exactly the case the second call site exists for.
	t.Setenv(UnmigrateEnvVar, markerStripEstate)
	planProvider := markerStripProvider(true)
	planView, planDone := testView(t)
	pc := &PlanCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(planProvider),
		View:             planView,
	}}
	if code := pc.Run([]string{"-state", statePath, "-out", planPath, "-no-color"}); code != 0 {
		t.Fatalf("plan -out exit status %d, want 0\n\n%s", code, planDone(t).All())
	}
	planDone(t)
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("plan file was not written: %s", err)
	}

	// Now apply it in the ordinary way, with no approval in the environment.
	os.Unsetenv(UnmigrateEnvVar)
	p := markerStripProvider(true)
	view, done := testView(t)
	c := &ApplyCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(p),
		View:             view,
	}}

	code := c.Run([]string{"-state", statePath, "-auto-approve", "-no-color", planPath})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit status %d, want 1\n\n%s", code, output.All())
	}
	if !strings.Contains(output.All(), summaryUnmigrateRefused) {
		t.Errorf("saved-plan apply was not refused\n\n%s", output.All())
	}
	if p.ApplyResourceChangeCalled {
		t.Error("ApplyResourceChange was called: the markers were stripped from the live resource")
	}
}

// TestApply_statefulDestroyOfAStampedResourceIsNotRefused bounds the
// refusal. A destroy removes the resource, marker and all, and that is what
// the operator asked for; refusing it would be the guard deciding it knows
// better than a stated intent. Only an in-place update that drops the marker
// while keeping the resource is a silent un-migration.
func TestApply_statefulDestroyOfAStampedResourceIsNotRefused(t *testing.T) {
	statePath := markerStripDir(t)
	p := markerStripProvider(true)
	view, done := testView(t)
	c := &ApplyCommand{Meta: Meta{
		WorkingDir:       workdir.NewDir("."),
		testingOverrides: metaOverridesForProvider(p),
		View:             view,
	}, Destroy: true}

	code := c.Run([]string{"-state", statePath, "-auto-approve", "-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit status %d, want 0\n\n%s", code, output.All())
	}
	if strings.Contains(output.All(), summaryUnmigrateRefused) {
		t.Errorf("destroy was refused\n\n%s", output.All())
	}
}
