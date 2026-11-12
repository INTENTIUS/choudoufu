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
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/forget"
	"github.com/intentius/choudoufu/internal/engine/forkport/replacecycle"
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
	"moved-live":          cleanFixture + "resource \"test_object\" \"old\" {}\nmoved {\n  from = test_object.old\n  to   = test_object.a\n}\n",
	"lifecycle-destroy":   "resource \"test_object\" \"a\" {\n  lifecycle {\n    destroy = false\n  }\n}\n",
	"checks-conditions":   "variable \"x\" {\n  default = \"a\"\n}\ncheck \"c\" {\n  assert {\n    condition     = var.x != \"\"\n    error_message = \"x\"\n  }\n}\n",
	"variable-validation": "variable \"v\" {\n  validation {\n    condition     = var.v != \"\"\n    error_message = \"x\"\n  }\n}\n",
	"sensitive":           "variable \"v\" {\n  sensitive = true\n}\n",
}

func TestPreflightRefusesEachConfigurationRow(t *testing.T) {
	// A row a fork port fills refuses only with the port off; each port's
	// own test proves it admits its construct while on.
	t.Setenv(forkport.AllOff, "1")
	for id, src := range preflightFixtures {
		t.Run(id, func(t *testing.T) {
			r := row(t, id)
			diags := Preflight(loadConfig(t, src), states.NewState(), r.Scope == ScopeLive)
			if !refusedFor(diags, r) {
				t.Fatalf("construct %q (gap %q) was not refused naming what it waits for (%s); diagnostics:\n%s", r.Construct, r.ID, r.WaitsFor(), diags.ErrWithWarnings())
			}
		})
	}
}

// TestPreflightMovedIsScopedByLiveMode is #2256 U10 (#2374), with the moved
// row closed by #2378. Outside live mode upstream's new runtime executes a
// moved block, so none is refused. In live mode a block internal/live/moved's
// Honourable admits passes, because discovery has already bound the object to
// its new address, and every other block is refused by the moved-live row,
// the same shapes internal/live/lint refuses.
func TestPreflightMovedIsScopedByLiveMode(t *testing.T) {
	movedRow, liveRow := row(t, "moved"), row(t, "moved-live")
	if movedRow.Check != CheckNone || liveRow.Scope != ScopeLive {
		t.Fatalf("row moved has check %q and moved-live scope %q, want %q and %q", movedRow.Check, liveRow.Scope, CheckNone, ScopeLive)
	}

	rename := cleanFixture + "moved {\n  from = test_object.old\n  to   = test_object.a\n}\n"
	for _, live := range []bool{false, true} {
		if diags := Preflight(loadConfig(t, rename), states.NewState(), live); len(diags) != 0 {
			t.Errorf("live=%v: an honourable rename was refused:\n%s", live, diags.ErrWithWarnings())
		}
	}

	for name, src := range map[string]string{
		// The old address is still declared: no vacated address to bind.
		"source still declared": preflightFixtures["moved-live"],
		// A marker names its own resource's type, so it cannot cross types.
		"different types": cleanFixture + "moved {\n  from = other_object.old\n  to   = test_object.a\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			diags := Preflight(loadConfig(t, src), states.NewState(), true)
			if !refusedFor(diags, liveRow) {
				t.Errorf("in live mode a moved block lint refuses was not refused under gap %q; diagnostics:\n%s", liveRow.ID, diags.ErrWithWarnings())
			}
		})
	}
}

func TestParseRefusesUnknownPreflightScope(t *testing.T) {
	src := `{"comment": "", "rows": [{"id": "x", "gap": "x", "check": "preflight", "construct": "moved", "scope": "everywhere", "closed_by": "x", "upstream": []}]}`
	if _, err := parse([]byte(src)); err == nil {
		t.Error("a preflight row with an unknown scope was accepted")
	}
	src = `{"comment": "", "rows": [{"id": "x", "gap": "x", "check": "accepted", "scope": "live", "closed_by": "x", "upstream": []}]}`
	if _, err := parse([]byte(src)); err == nil {
		t.Error("an accepted row with a scope was accepted")
	}
}

func TestPreflightRefusesSensitiveFunctionAndOutputs(t *testing.T) {
	r := row(t, "sensitive")
	for name, src := range map[string]string{
		"sensitive()":      "locals {\n  x = sensitive(\"a\")\n}\n",
		"sensitive output": "output \"o\" {\n  value     = \"a\"\n  sensitive = true\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if diags := Preflight(loadConfig(t, src), nil, false); !refusedFor(diags, r) {
				t.Fatalf("%s was not refused; diagnostics:\n%s", name, diags.ErrWithWarnings())
			}
		})
	}
}

func TestPreflightRefusesConditionsOnResources(t *testing.T) {
	r := row(t, "checks-conditions")
	src := "variable \"x\" {\n  default = \"a\"\n}\nresource \"test_object\" \"a\" {\n  lifecycle {\n    precondition {\n      condition     = var.x != \"\"\n      error_message = \"x\"\n    }\n  }\n}\n"
	if diags := Preflight(loadConfig(t, src), nil, false); !refusedFor(diags, r) {
		t.Fatalf("a precondition was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

// TestPreflightAdmitsDeposedState: the deposed row is closed (#2378), since
// upstream's new runtime plans deposed objects itself. A deposed object
// recording lifecycle destroy = false is forgotten by the forget fork port
// (#2409), and refused by lifecycle-destroy with the port off.
func TestPreflightAdmitsDeposedState(t *testing.T) {
	if r := row(t, "deposed"); r.Check != CheckNone {
		t.Fatalf("row deposed has check %q, want %q", r.Check, CheckNone)
	}
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_object", Name: "a"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	for _, portOn := range []bool{true, false} {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(forget.Name), "")
		if !portOn {
			t.Setenv(forkport.Switch(forget.Name), "1")
		}
		for _, skip := range []bool{false, true} {
			s := states.BuildState(func(ss *states.SyncState) {
				ss.SetResourceInstanceDeposed(addr, states.DeposedKey("00000001"),
					&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{}`), SkipDestroy: skip},
					addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}, addrs.NoKey)
			})
			diags := Preflight(loadConfig(t, cleanFixture), s, false)
			if (!skip || portOn) && len(diags) != 0 {
				t.Errorf("deposed state (destroy = false %t, forget port on %t) was refused:\n%s", skip, portOn, diags.ErrWithWarnings())
			}
			if skip && !portOn && !refusedFor(diags, row(t, "lifecycle-destroy")) {
				t.Errorf("with the forget port off a deposed object with destroy = false was not refused under lifecycle-destroy; diagnostics:\n%s", diags.ErrWithWarnings())
			}
		}
	}
}

// An orphaned object whose state records lifecycle destroy = false: the
// resource block is gone, so only the state says not to delete it. The
// forget fork port (#2409) forgets it; with the port off it is refused.
func TestPreflightRefusesSkipDestroyInState(t *testing.T) {
	r := row(t, "lifecycle-destroy")
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_object", Name: "gone"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	s := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(addr,
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{}`), SkipDestroy: true},
			addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}, addrs.NoKey)
	})
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(forget.Name), "")
	if diags := Preflight(loadConfig(t, cleanFixture), s, false); len(diags) != 0 {
		t.Fatalf("with the forget port on an orphan with destroy = false in state was refused:\n%s", diags.ErrWithWarnings())
	}
	t.Setenv(forkport.Switch(forget.Name), "1")
	if diags := Preflight(loadConfig(t, cleanFixture), s, false); !refusedFor(diags, r) {
		t.Fatalf("with the forget port off an orphan with destroy = false in state was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

// TestPreflightForgetPort (#2409): with the forget port on, removed blocks
// and lifecycle destroy are admitted, except a removed block that deletes
// and has provisioners, which the new runtime would not run; with its switch
// set each is refused under its row again, in and out of live mode.
func TestPreflightForgetPort(t *testing.T) {
	withProvisioner := "removed {\n  from = test_object.gone\n  lifecycle {\n    destroy = true\n  }\n  provisioner \"local-exec\" {\n    when    = destroy\n    command = \"echo\"\n  }\n}\n"
	forgetting := "removed {\n  from = test_object.gone\n  lifecycle {\n    destroy = false\n  }\n}\n"
	cases := []struct {
		row, src  string
		refusedOn bool
	}{
		{"removed", preflightFixtures["removed"], false},
		{"removed", forgetting, false},
		{"removed", withProvisioner, true},
		{"lifecycle-destroy", preflightFixtures["lifecycle-destroy"], false},
	}
	for _, live := range []bool{false, true} {
		for _, c := range cases {
			r := row(t, c.row)
			t.Setenv(forkport.AllOff, "")
			t.Setenv(forkport.Switch(forget.Name), "")
			if diags := Preflight(loadConfig(t, c.src), states.NewState(), live); refusedFor(diags, r) != c.refusedOn {
				t.Errorf("live=%t, port on, %q: refused under %q is %t, want %t; diagnostics:\n%s", live, c.src, c.row, !c.refusedOn, c.refusedOn, diags.ErrWithWarnings())
			}
			t.Setenv(forkport.Switch(forget.Name), "1")
			if diags := Preflight(loadConfig(t, c.src), states.NewState(), live); !refusedFor(diags, r) {
				t.Errorf("live=%t, port off, %q was not refused under %q; diagnostics:\n%s", live, c.src, c.row, diags.ErrWithWarnings())
			}
		}
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
	if diags := Preflight(loadConfig(t, cleanFixture), states.NewState(), false); len(diags) != 0 {
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
	// cycle, in either ordering, while the replacecycle fork port (#2380)
	// is off; while it is on, planning refuses a cycle itself.
	for _, action := range []plans.Action{plans.DeleteThenCreate, plans.CreateThenDelete} {
		p := planOf(changeFor(t, "test_object.a[0]", action))
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(replacecycle.Name), "")
		if diags := PostPlan(p, false); len(diags) != 0 {
			t.Errorf("live=false %s was refused with the port on: %s", action, diags.ErrWithWarnings())
		}
		t.Setenv(forkport.Switch(replacecycle.Name), "1")
		if diags := PostPlan(p, false); !refusedFor(diags, row(t, "replace-cbd-cycle")) {
			t.Errorf("live=false %s was not refused under gap %q with the port off; diagnostics:\n%s", action, "replace-cbd-cycle", diags.ErrWithWarnings())
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

// destroyTargetPlan is a destroy plan over a chain a <- b <- c (c depends
// on b, b on a, as the prior state records), targeting targets and deleting
// deleted.
func destroyTargetPlan(t *testing.T, mode plans.Mode, targets []string, deleted ...string) *plans.Plan {
	t.Helper()
	provider := addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}
	prior := states.BuildState(func(s *states.SyncState) {
		deps := map[string][]addrs.ConfigResource{
			"test_object.a": nil,
			"test_object.b": {addrs.MustParseResourceAddr("test_object.a")},
			"test_object.c": {addrs.MustParseResourceAddr("test_object.b")},
		}
		for name, d := range deps {
			addr, diags := addrs.ParseAbsResourceInstanceStr(name)
			if diags.HasErrors() {
				t.Fatal(diags.Err())
			}
			s.SetResourceInstanceCurrent(addr, &states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{}`), Dependencies: d}, provider, addrs.NoKey)
		}
	})
	var changes []*plans.ResourceInstanceChangeSrc
	for _, d := range deleted {
		changes = append(changes, changeFor(t, d, plans.Delete))
	}
	p := planOf(changes...)
	p.UIMode = mode
	p.PrevRunState = prior
	p.PriorState = prior.DeepCopy()
	for _, tgt := range targets {
		ta, diags := addrs.ParseTargetStr(tgt)
		if diags.HasErrors() {
			t.Fatal(diags.Err())
		}
		p.TargetAddrs = append(p.TargetAddrs, ta.Subject)
	}
	return p
}

// TestPostPlanRefusesTargetedOverDestroy: the destroy-target row (#2387).
// A destroy with -target may delete the targets and, transitively, what the
// prior state records as depending on them, and nothing else.
func TestPostPlanRefusesTargetedOverDestroy(t *testing.T) {
	r := row(t, "destroy-target")
	for _, live := range []bool{false, true} {
		// The new runtime's over-destroy: targeting c also deletes b and a.
		if diags := PostPlan(destroyTargetPlan(t, plans.DestroyMode, []string{"test_object.c"}, "test_object.a", "test_object.b", "test_object.c"), live); !refusedFor(diags, r) || len(diags) != 2 {
			t.Errorf("live=%v: deleting a and b for -target c was not refused twice under %q: %s", live, r.ID, diags.ErrWithWarnings())
		}
		admitted := []struct {
			mode    plans.Mode
			targets []string
			deleted []string
		}{
			{plans.DestroyMode, []string{"test_object.a"}, []string{"test_object.a", "test_object.b", "test_object.c"}},
			{plans.DestroyMode, []string{"test_object.b"}, []string{"test_object.b", "test_object.c"}},
			{plans.DestroyMode, []string{"test_object.c"}, []string{"test_object.c"}},
			{plans.DestroyMode, nil, []string{"test_object.a", "test_object.b", "test_object.c"}},
			{plans.NormalMode, []string{"test_object.c"}, []string{"test_object.a"}},
		}
		for _, c := range admitted {
			if diags := PostPlan(destroyTargetPlan(t, c.mode, c.targets, c.deleted...), live); len(diags) != 0 {
				t.Errorf("live=%v: %s -target %v deleting %v was refused: %s", live, c.mode, c.targets, c.deleted, diags.ErrWithWarnings())
			}
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
	tested := map[string]bool{"test-command": true, "replace-cbd-cycle": true, "deferral": true, "destroy-target": true}
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
