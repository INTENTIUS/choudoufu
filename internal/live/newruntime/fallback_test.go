// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// assertFallsBackFor checks that refusal, a refusing row's diagnostics, names
// exactly that row to RefusedRows, and that the fallback warning built from
// it names the row, what it waits for, the toggle and the strict switch
// (#2397). The refusal itself is what the strict switch keeps.
func assertFallsBackFor(t *testing.T, refusal tfdiags.Diagnostics, id string) {
	t.Helper()
	r := row(t, id)
	if !refusedFor(refusal, r) {
		t.Fatalf("not refused under %q, so strict would not refuse it; diagnostics:\n%s", id, refusal.ErrWithWarnings())
	}
	got := RefusedRows(refusal)
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("RefusedRows = %v, want [%s]", got, id)
	}
	w := FallbackWarning(got)
	desc := w.Description()
	if w.Severity() != tfdiags.Warning || desc.Summary != FallbackSummary {
		t.Fatalf("fallback warning is %v %q", w.Severity(), desc.Summary)
	}
	for _, want := range []string{"- " + id + ":", r.WaitsFor(), Variable, StrictVariable + "=1", "default runtime"} {
		if !strings.Contains(desc.Detail, want) {
			t.Errorf("fallback warning does not contain %q:\n%s", want, desc.Detail)
		}
	}
	for _, u := range r.Upstream {
		if !strings.Contains(desc.Detail, u) {
			t.Errorf("fallback warning does not name upstream %s", u)
		}
	}
}

// TestFallbackNamesEachRefusingRow runs every refusing row's fixture: each
// refusal names its row to the fallback, and the warning names it.
func TestFallbackNamesEachRefusingRow(t *testing.T) {
	t.Setenv(forkport.AllOff, "1")
	tested := map[string]bool{}
	for id, src := range preflightFixtures {
		t.Run(id, func(t *testing.T) {
			r := row(t, id)
			assertFallsBackFor(t, Preflight(loadConfig(t, src), states.NewState(), r.Scope == ScopeLive), id)
		})
		tested[id] = true
	}
	t.Run("replace-cbd-cycle", func(t *testing.T) {
		assertFallsBackFor(t, PostPlan(planOf(changeFor(t, "test_object.a", plans.DeleteThenCreate)), false), "replace-cbd-cycle")
	})
	t.Run("destroy-target", func(t *testing.T) {
		p := destroyTargetPlan(t, plans.DestroyMode, []string{"test_object.c"}, "test_object.b", "test_object.c")
		assertFallsBackFor(t, PostPlan(p, false), "destroy-target")
	})
	t.Run("deferral", func(t *testing.T) {
		addr, _ := addrs.ParseAbsResourceInstanceStr("test_object.a")
		assertFallsBackFor(t, DeferralRefusal(addr), "deferral")
	})
	t.Run("test-command", func(t *testing.T) {
		assertFallsBackFor(t, RefuseCommand("test"), "test-command")
	})
	for _, id := range []string{"replace-cbd-cycle", "destroy-target", "deferral", "test-command"} {
		tested[id] = true
	}
	for _, r := range Rows() {
		switch r.Check {
		case CheckPreflight, CheckPostPlan, CheckEngine:
			if !tested[r.ID] {
				t.Errorf("refusing row %q has no fallback fixture", r.ID)
			}
		}
	}
}

func TestRefusedRowsIgnoresOtherDiagnostics(t *testing.T) {
	var diags tfdiags.Diagnostics
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, RefusalSummary, `looks like one, gap "data-sources"`))
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "w", "w"))
	if got := RefusedRows(diags); len(got) != 0 {
		t.Errorf("RefusedRows = %v for diagnostics this package did not raise", got)
	}
	addr, _ := addrs.ParseAbsResourceInstanceStr("test_object.a")
	diags = diags.Append(DeferralRefusal(addr)).Append(DeferralRefusal(addr))
	if got := RefusedRows(diags); len(got) != 1 || got[0].ID != "deferral" {
		t.Errorf("RefusedRows = %v, want [deferral] once", got)
	}
}

func TestStrict(t *testing.T) {
	t.Setenv(StrictVariable, "")
	if Strict() {
		t.Error("Strict() with the variable empty")
	}
	t.Setenv(StrictVariable, "1")
	if !Strict() {
		t.Error("!Strict() with the variable set")
	}
}
