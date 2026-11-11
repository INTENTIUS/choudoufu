// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

func loadConfig(t *testing.T, src string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	mod, diags := configs.NewParser(nil).LoadConfigDir(dir)
	if diags.HasErrors() {
		t.Fatalf("loading fixture: %s", diags.Error())
	}
	cfg, diags := configs.BuildConfig(context.Background(), mod, configs.RootModuleCallForTesting(), configs.DisabledModuleWalker)
	if diags.HasErrors() {
		t.Fatalf("building fixture: %s", diags.Error())
	}
	return cfg
}

// refusedFor reports whether diags hold a refusal naming the row's id and
// what it waits for.
func refusedFor(diags tfdiags.Diagnostics, r Row) bool {
	for _, d := range diags {
		desc := d.Description()
		if d.Severity() == tfdiags.Error && desc.Summary == RefusalSummary &&
			strings.Contains(desc.Detail, `"`+r.ID+`"`) && strings.Contains(desc.Detail, r.WaitsFor()) {
			return true
		}
	}
	return false
}

func row(t *testing.T, id string) Row {
	t.Helper()
	r, ok := RowByID(id)
	if !ok {
		t.Fatalf("gap table has no row %q", id)
	}
	return r
}

const cleanFixture = `
resource "test_object" "a" {
  count       = 2
  test_string = "a"
}
`

// preflightFixtures is one fixture per refusing preflight row of the
// configuration. TestEveryRefusingRowHasAFixture keeps it complete.
var preflightFixtures = map[string]string{
	"data-sources":        `data "test_object" "d" {}`,
	"removed":             "removed {\n  from = test_object.gone\n}\n",
	"moved":               cleanFixture + "moved {\n  from = test_object.old\n  to   = test_object.a\n}\n",
	"lifecycle-destroy":   "resource \"test_object\" \"a\" {\n  lifecycle {\n    destroy = false\n  }\n}\n",
	"checks-conditions":   "check \"c\" {\n  assert {\n    condition     = true\n    error_message = \"x\"\n  }\n}\n",
	"variable-validation": "variable \"v\" {\n  validation {\n    condition     = var.v != \"\"\n    error_message = \"x\"\n  }\n}\n",
	"sensitive":           "variable \"v\" {\n  sensitive = true\n}\n",
}

func TestPreflightRefusesEachConfigurationRow(t *testing.T) {
	for id, src := range preflightFixtures {
		t.Run(id, func(t *testing.T) {
			r := row(t, id)
			diags := Preflight(loadConfig(t, src), states.NewState())
			if !refusedFor(diags, r) {
				t.Fatalf("construct %q (gap %q) was not refused naming what it waits for (%s); diagnostics:\n%s", r.Construct, r.ID, r.WaitsFor(), diags.ErrWithWarnings())
			}
		})
	}
}

func TestPreflightRefusesSensitiveFunctionAndOutputs(t *testing.T) {
	r := row(t, "sensitive")
	for name, src := range map[string]string{
		"sensitive()":      "locals {\n  x = sensitive(\"a\")\n}\n",
		"sensitive output": "output \"o\" {\n  value     = \"a\"\n  sensitive = true\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if diags := Preflight(loadConfig(t, src), nil); !refusedFor(diags, r) {
				t.Fatalf("%s was not refused; diagnostics:\n%s", name, diags.ErrWithWarnings())
			}
		})
	}
}

func TestPreflightRefusesConditionsOnResources(t *testing.T) {
	r := row(t, "checks-conditions")
	src := "resource \"test_object\" \"a\" {\n  lifecycle {\n    precondition {\n      condition     = true\n      error_message = \"x\"\n    }\n  }\n}\n"
	if diags := Preflight(loadConfig(t, src), nil); !refusedFor(diags, r) {
		t.Fatalf("a precondition was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

func TestPreflightRefusesDeposedState(t *testing.T) {
	r := row(t, "deposed")
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_object", Name: "a"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	s := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceDeposed(addr, states.DeposedKey("00000001"),
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{}`)},
			addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}, addrs.NoKey)
	})
	if diags := Preflight(loadConfig(t, cleanFixture), s); !refusedFor(diags, r) {
		t.Fatalf("deposed state was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

func TestRefuseTestCommand(t *testing.T) {
	if diags := RefuseCommand("test"); !refusedFor(diags, row(t, "test-command")) {
		t.Fatalf("the test command was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	if diags := RefuseCommand("plan"); len(diags) != 0 {
		t.Fatalf("plan was refused as a command: %s", diags.ErrWithWarnings())
	}
}

func TestPreflightCleanFixturePasses(t *testing.T) {
	if diags := Preflight(loadConfig(t, cleanFixture), states.NewState()); len(diags) != 0 {
		t.Fatalf("a configuration with none of the refused constructs was refused:\n%s", diags.ErrWithWarnings())
	}
}

func changeFor(t *testing.T, addr string, action plans.Action) *plans.ResourceInstanceChangeSrc {
	t.Helper()
	a, diags := addrs.ParseAbsResourceInstanceStr(addr)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	return &plans.ResourceInstanceChangeSrc{
		Addr:         a,
		PrevRunAddr:  a,
		ProviderAddr: addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule},
		ChangeSrc:    plans.ChangeSrc{Action: action},
	}
}

func planOf(changes ...*plans.ResourceInstanceChangeSrc) *plans.Plan {
	c := plans.NewChanges()
	c.Resources = changes
	return &plans.Plan{Changes: c}
}

func TestPostPlanRefusesReplace(t *testing.T) {
	for _, tc := range []struct {
		live bool
		row  string
	}{{true, "replace-live"}, {false, "replace-cbd-cycle"}} {
		for _, action := range []plans.Action{plans.DeleteThenCreate, plans.CreateThenDelete} {
			p := planOf(changeFor(t, "test_object.a[0]", action))
			diags := PostPlan(p, loadConfig(t, cleanFixture), tc.live, nil)
			if !refusedFor(diags, row(t, tc.row)) {
				t.Errorf("live=%v %s was not refused under gap %q; diagnostics:\n%s", tc.live, action, tc.row, diags.ErrWithWarnings())
			}
		}
	}
	p := planOf(changeFor(t, "test_object.a[0]", plans.Update), changeFor(t, "test_object.a[1]", plans.Create))
	if diags := PostPlan(p, loadConfig(t, cleanFixture), true, nil); len(diags) != 0 {
		t.Errorf("an update and a create were refused: %s", diags.ErrWithWarnings())
	}
}

func TestPostPlanRefusesDeleteWithDestroyOverride(t *testing.T) {
	r := row(t, "destroy-override")
	src := "resource \"test_object\" \"a\" {\n  test_string = \"x\"\n}\n"
	attrs := func(addrs.Provider, string) []string { return []string{"test_string"} }
	p := planOf(changeFor(t, "test_object.a", plans.Delete))
	if diags := PostPlan(p, loadConfig(t, src), false, attrs); !refusedFor(diags, r) {
		t.Fatalf("a delete an override would act on was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	// The configuration does not set the attribute: the override does not act.
	if diags := PostPlan(p, loadConfig(t, "resource \"test_object\" \"a\" {}\n"), false, attrs); len(diags) != 0 {
		t.Fatalf("a delete whose configuration does not set the override's attribute was refused: %s", diags.ErrWithWarnings())
	}
	// No override for the type.
	if diags := PostPlan(p, loadConfig(t, src), false, func(addrs.Provider, string) []string { return nil }); len(diags) != 0 {
		t.Fatalf("a delete with no override was refused: %s", diags.ErrWithWarnings())
	}
}

func TestDeferralRefusal(t *testing.T) {
	addr, _ := addrs.ParseAbsResourceInstanceStr("test_object.a")
	if diags := DeferralRefusal(addr); !refusedFor(diags, row(t, "deferral")) {
		t.Fatalf("deferral was not refused: %s", diags.ErrWithWarnings())
	}
}

// TestEveryRefusingRowHasAFixture keeps the table and its fixtures in step:
// a row that refuses must have a detector and a test, and a construct a
// detector knows must have a row.
func TestEveryRefusingRowHasAFixture(t *testing.T) {
	tested := map[string]bool{"deposed": true, "test-command": true, "replace-live": true, "replace-cbd-cycle": true, "destroy-override": true, "deferral": true}
	for id := range preflightFixtures {
		tested[id] = true
	}
	for _, r := range Rows() {
		switch r.Check {
		case CheckPreflight, CheckPostPlan, CheckEngine:
			if !tested[r.ID] {
				t.Errorf("refusing row %q (%s, construct %q) has no fixture test", r.ID, r.Check, r.Construct)
			}
			if r.Construct == "" {
				t.Errorf("refusing row %q names no construct", r.ID)
			}
		}
	}
	for c := range preflightDetectors {
		found := false
		for _, r := range RowsFor(CheckPreflight) {
			found = found || r.Construct == c
		}
		if !found {
			t.Errorf("preflight detector %q has no row in gaps.json", c)
		}
	}
}

func TestWarningNamesVariableAndAcceptedRows(t *testing.T) {
	w := WarningDetail()
	if !strings.Contains(w, Variable) {
		t.Errorf("warning does not name %s", Variable)
	}
	for _, r := range RowsFor(CheckAccepted) {
		if !strings.Contains(w, r.Warning) {
			t.Errorf("warning does not name accepted row %q", r.ID)
		}
	}
}
