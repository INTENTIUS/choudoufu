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
	"checks-conditions":   "variable \"x\" {\n  default = \"a\"\n}\ncheck \"c\" {\n  assert {\n    condition     = var.x != \"\"\n    error_message = \"x\"\n  }\n}\n",
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
	src := "variable \"x\" {\n  default = \"a\"\n}\nresource \"test_object\" \"a\" {\n  lifecycle {\n    precondition {\n      condition     = var.x != \"\"\n      error_message = \"x\"\n    }\n  }\n}\n"
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

// An orphaned object whose state records lifecycle destroy = false: the
// resource block is gone, so only the state says not to delete it.
func TestPreflightRefusesSkipDestroyInState(t *testing.T) {
	r := row(t, "lifecycle-destroy")
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_object", Name: "gone"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	s := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(addr,
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{}`), SkipDestroy: true},
			addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}, addrs.NoKey)
	})
	if diags := Preflight(loadConfig(t, cleanFixture), s); !refusedFor(diags, r) {
		t.Fatalf("an orphan with destroy = false in state was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
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
	// Outside live mode a replace waits on upstream's create_before_destroy
	// cycle, in either ordering.
	for _, action := range []plans.Action{plans.DeleteThenCreate, plans.CreateThenDelete} {
		p := planOf(changeFor(t, "test_object.a[0]", action))
		if diags := PostPlan(p, false); !refusedFor(diags, row(t, "replace-cbd-cycle")) {
			t.Errorf("live=false %s was not refused under gap %q; diagnostics:\n%s", action, "replace-cbd-cycle", diags.ErrWithWarnings())
		}
	}
	// In live mode U7 (#2343) lifted the replace refusal: no row refuses it,
	// the cbd-cycle row included.
	for _, action := range []plans.Action{plans.DeleteThenCreate, plans.CreateThenDelete} {
		p := planOf(changeFor(t, "test_object.a[0]", action))
		if diags := PostPlan(p, true); len(diags) != 0 {
			t.Errorf("live=true %s was refused: %s", action, diags.ErrWithWarnings())
		}
	}
	p := planOf(changeFor(t, "test_object.a[0]", plans.Update), changeFor(t, "test_object.a[1]", plans.Create))
	for _, live := range []bool{true, false} {
		if diags := PostPlan(p, live); len(diags) != 0 {
			t.Errorf("live=%v: an update and a create were refused: %s", live, diags.ErrWithWarnings())
		}
	}
}

// TestReplaceLiveRowIsLifted: U7 (#2343) closed the replace-live row, and
// the remaining replace row is scoped away from live mode.
func TestReplaceLiveRowIsLifted(t *testing.T) {
	if r := row(t, "replace-live"); r.Check != CheckNone {
		t.Errorf("row replace-live has check %q, want %q", r.Check, CheckNone)
	}
	if r := row(t, "replace-cbd-cycle"); r.Check != CheckPostPlan || r.Scope != ScopeNonLive {
		t.Errorf("row replace-cbd-cycle has check %q scope %q, want %q scope %q", r.Check, r.Scope, CheckPostPlan, ScopeNonLive)
	}
}

func TestParseRefusesUnknownPostPlanScope(t *testing.T) {
	for _, scope := range []string{"", "everywhere"} {
		src := `{"comment": "", "rows": [{"id": "x", "gap": "x", "check": "post_plan", "construct": "replace", "scope": "` + scope + `", "closed_by": "x", "upstream": []}]}`
		if _, err := parse([]byte(src)); err == nil {
			t.Errorf("a post-plan row with scope %q was accepted", scope)
		}
	}
	for _, scope := range []string{ScopeLive, ScopeNonLive, ScopeAll} {
		src := `{"comment": "", "rows": [{"id": "x", "gap": "x", "check": "post_plan", "construct": "replace", "scope": "` + scope + `", "closed_by": "x", "upstream": []}]}`
		if _, err := parse([]byte(src)); err != nil {
			t.Errorf("a post-plan row with scope %q was refused: %s", scope, err)
		}
	}
}

// TestPostPlanDoesNotRefuseDelete: the destroy-override row was lifted
// when U8 (#2344) ported destroy-from-config overrides to the new runtime,
// so a delete is no longer refused in or out of live mode, and the row
// stays in the table as a closed gap with no check.
func TestPostPlanDoesNotRefuseDelete(t *testing.T) {
	if r := row(t, "destroy-override"); r.Check != CheckNone {
		t.Fatalf("row destroy-override has check %q, want %q", r.Check, CheckNone)
	}
	p := planOf(changeFor(t, "test_object.a", plans.Delete))
	for _, live := range []bool{false, true} {
		if diags := PostPlan(p, live); len(diags) != 0 {
			t.Errorf("live=%v: a delete was refused: %s", live, diags.ErrWithWarnings())
		}
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
	tested := map[string]bool{"deposed": true, "test-command": true, "replace-cbd-cycle": true, "deferral": true}
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
