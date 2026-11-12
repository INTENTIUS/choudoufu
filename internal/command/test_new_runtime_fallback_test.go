// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"path"
	"strings"
	"testing"

	testing_command "github.com/intentius/choudoufu/internal/command/testing"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/newruntime"
)

// TestTestCommandFallsBackOnNewRuntime (#2397, gap "test-command"): with the
// toggle on, the test command runs on the default runtime, where its harness
// evaluates, and prints the fallback warning naming the row, not the
// new-runtime warning. CHOUDOUFU_NEW_RUNTIME_STRICT=1 refuses it, as before.
func TestTestCommandFallsBackOnNewRuntime(t *testing.T) {
	for _, strict := range []bool{false, true} {
		name := "fallback"
		if strict {
			name = "strict"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(newruntime.Variable, "1")
			t.Setenv(newruntime.StrictVariable, "")
			if strict {
				t.Setenv(newruntime.StrictVariable, "1")
			}
			td := t.TempDir()
			testCopyDir(t, testFixturePath(path.Join("test", "simple_pass")), td)
			t.Chdir(td)
			provider := testing_command.NewProvider(nil)
			view, done := testView(t)
			meta := Meta{
				WorkingDir:       workdir.NewDir("."),
				testingOverrides: metaOverridesForProvider(provider.Provider),
				View:             view,
			}
			code := RunCommander(t, TestCommander(), meta, nil)
			out := done(t)
			all := strings.Join(strings.Fields(out.All()), " ")

			if strict {
				if code != 1 || !strings.Contains(all, newruntime.RefusalSummary) || !strings.Contains(all, `gap "test-command"`) {
					t.Fatalf("strict: want exit 1 and the test-command refusal, got exit %d:\n%s", code, out.All())
				}
				return
			}
			if code != 0 || !strings.Contains(out.Stdout(), "1 passed, 0 failed.") {
				t.Fatalf("want the tests to pass on the default runtime, got exit %d:\n%s", code, out.All())
			}
			if !strings.Contains(all, newruntime.FallbackSummary) || !strings.Contains(all, "- test-command:") {
				t.Errorf("no fallback warning naming test-command:\n%s", out.All())
			}
			if strings.Contains(all, newruntime.WarningSummary) {
				t.Errorf("the test command printed the new-runtime warning, so part of it ran there:\n%s", out.All())
			}
		})
	}
}
