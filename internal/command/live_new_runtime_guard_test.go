// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/tofu"
)

// liveGuardCommandVariables are the opt-in variables a live run must refuse
// at every entry point. Since upstream's split (`#4823`, taken in #2256 U1)
// TOFU_X_EXPERIMENTAL_RUNTIME switches plan and apply in internal/tofu and
// TOFU_X_EXPERIMENTAL_RUNTIME_NEXT switches this layer (Meta.NewRuntimeEnabled).
var liveGuardCommandVariables = []string{"TOFU_X_EXPERIMENTAL_RUNTIME", "TOFU_X_EXPERIMENTAL_RUNTIME_NEXT"}

// liveNewRuntimeRefusals are the two summaries #2256 G produces: the
// command-layer check here, and the runtime-decision check in internal/tofu.
var liveNewRuntimeRefusals = []string{
	liveCommandLayerOnNewRuntimeSummary,
	tofu.LiveOnNewRuntimeSummary,
}

// liveGuardEntryPoints are the four live entry points #2339 names. Each
// sets up its own fixture in the current directory and runs the command.
func liveGuardEntryPoints() []struct {
	name string
	run  func(t *testing.T, td string, view func() Meta) int
} {
	return []struct {
		name string
		run  func(t *testing.T, td string, view func() Meta) int
	}{
		{
			name: "apply",
			run: func(t *testing.T, td string, meta func() Meta) int {
				testCopyDir(t, testFixturePath("live-block"), td)
				c := &ApplyCommand{Meta: meta()}
				return c.Run([]string{"-no-color", "-auto-approve"})
			},
		},
		{
			name: "composed root",
			run: func(t *testing.T, td string, meta func() Meta) int {
				composeFixture(t, td, "")
				c := &ApplyCommand{Meta: meta()}
				wireBuiltinTerraform(&c.Meta)
				return c.Run([]string{"-no-color", "-auto-approve"})
			},
		},
		{
			name: "live-plan",
			run: func(t *testing.T, td string, meta func() Meta) int {
				testCopyDir(t, testFixturePath("live-block-record-store"), td)
				installResourcePathFakes(t, &fakeRecordStore{}, &fakeSweeper{})
				c := &LivePlanCommand{Meta: meta()}
				return c.Run([]string{"-no-color"})
			},
		},
		{
			name: "per-resource live plan",
			run: func(t *testing.T, td string, meta func() Meta) int {
				testCopyDir(t, testFixturePath("live-block-record-store"), td)
				installResourcePathFakes(t, &fakeRecordStore{}, &fakeSweeper{})
				c := &LivePlanCommand{Meta: meta()}
				return c.Run([]string{"-no-color", "-resource=aws_s3_bucket.data"})
			},
		},
	}
}

func liveGuardRun(t *testing.T, run func(t *testing.T, td string, meta func() Meta) int) (int, string) {
	t.Helper()
	td := t.TempDir()
	t.Chdir(td)
	view, done := testView(t)
	code := run(t, td, func() Meta { return liveBlockMeta(view, newLiveTestCloud()) })
	out := done(t)
	return code, strings.Join(strings.Fields(out.Stderr()+out.Stdout()), " ")
}

// TestLiveRefusesNewRuntime_variables: with an opt-in variable set, every
// live entry point is refused with one of #2256 G's two refusals, and
// exits 1.
func TestLiveRefusesNewRuntime_variables(t *testing.T) {
	for _, variable := range liveGuardCommandVariables {
		for _, ep := range liveGuardEntryPoints() {
			t.Run(variable+"/"+ep.name, func(t *testing.T) {
				t.Setenv(variable, "1")
				code, got := liveGuardRun(t, ep.run)
				if code != 1 {
					t.Errorf("exit code %d, want 1", code)
				}
				refused := false
				for _, s := range liveNewRuntimeRefusals {
					refused = refused || strings.Contains(got, s)
				}
				if !refused {
					t.Fatalf("%s with %s set was not refused as live mode on the new runtime; output:\n%s", ep.name, variable, got)
				}
			})
		}
	}
}

// TestLiveRefusesNewRuntime_testHook switches the new runtime on through the
// test hook in internal/tofu with no variable set. The command-layer check
// cannot see that, so the refusal has to come from the runtime-decision
// check: that is what shows G does not depend on a variable's name.
func TestLiveRefusesNewRuntime_testHook(t *testing.T) {
	for _, ep := range liveGuardEntryPoints() {
		t.Run(ep.name, func(t *testing.T) {
			t.Cleanup(tofu.SetExperimentalRuntimeForcedForTesting(true))
			code, got := liveGuardRun(t, ep.run)
			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
			if !strings.Contains(got, tofu.LiveOnNewRuntimeSummary) {
				t.Fatalf("%s with the new runtime forced on was not refused at the runtime decision; output:\n%s", ep.name, got)
			}
		})
	}
}
