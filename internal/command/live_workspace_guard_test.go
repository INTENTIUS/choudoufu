// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"
)

// TestStatelessWorkspaceGuard_workspaceCommandsRefused is the regression for audit finding
// F-WS. A live directory refused a non-default workspace at plan and apply
// time only, so "choudoufu workspace new staging" succeeded, selected the new
// workspace on the way out, and left a directory where nothing could run.
//
// Each row asserts the same three things the other stateless guards assert:
// exit 1, the command's own named refusal, and no state artifact anywhere
// under the working directory - including the terraform.tfstate.d workspace
// directory, which is what "workspace new" creates and what assertNoStateArtifacts
// looks for by name.
func TestStatelessWorkspaceGuard_workspaceCommandsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary string
		// names is the quoted command, which the detail carries now that
		// both subcommands share one summary.
		names       string
		replacement string
		run         func(m Meta) int
	}{
		{
			name:        "workspace new",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu workspace new" is not available here`,
			replacement: "the directory would be left unable to do anything",
			run: func(m Meta) int {
				c := &WorkspaceNewCommand{Meta: m}
				return c.Run([]string{"-no-color", "staging"})
			},
		},
		{
			name:        "workspace select",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu workspace select" is not available here`,
			replacement: `"choudoufu workspace select default"`,
			run: func(m Meta) int {
				c := &WorkspaceSelectCommand{Meta: m}
				return c.Run([]string{"-no-color", "staging"})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			code := tc.run(liveBlockMeta(view, liveBlockCloud()))
			output := done(t)

			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			got := strings.Join(strings.Fields(output.Stderr()+output.Stdout()), " ")
			if !strings.Contains(got, tc.summary) {
				t.Errorf("missing summary %q\nstderr:\n%s\nstdout:\n%s", tc.summary, output.Stderr(), output.Stdout())
			}
			if !strings.Contains(got, tc.names) {
				t.Errorf("the refusal does not name %s\nstderr:\n%s\nstdout:\n%s", tc.names, output.Stderr(), output.Stdout())
			}
			if !strings.Contains(got, tc.replacement) {
				t.Errorf("missing replacement %q\nstderr:\n%s\nstdout:\n%s", tc.replacement, output.Stderr(), output.Stdout())
			}
			assertNoStateArtifacts(t, td)
		})
	}
}

// TestStatelessWorkspaceGuard_workspaceSelectDefaultAllowed is the other half of the guard,
// and the reason it is not symmetric: selecting the default workspace is the
// way out of a directory that is already stranded in another one, so it must
// never be the thing that is refused. The command is allowed to fail for its
// own ordinary reasons here; what it must not do is refuse on stateless
// grounds.
func TestStatelessWorkspaceGuard_workspaceSelectDefaultAllowed(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	view, done := testView(t)
	c := &WorkspaceSelectCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
	c.Run([]string{"-no-color", "default"})
	output := done(t)

	if got := output.Stderr() + output.Stdout(); strings.Contains(got, "Command not available under live resource markers") {
		t.Errorf("selecting the default workspace was refused, which would strand a directory rather than unstrand it:\n%s", got)
	}
}

// TestStatelessWorkspaceGuard_workspaceCommandsUnguarded checks the guard says nothing in a
// configuration with no live block: a stateless refusal appearing in an
// ordinary working directory would be a worse bug than the one being fixed.
func TestStatelessWorkspaceGuard_workspaceCommandsUnguarded(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(m Meta) int
	}{
		{"workspace new", func(m Meta) int {
			c := &WorkspaceNewCommand{Meta: m}
			return c.Run([]string{"-no-color", "staging"})
		}},
		{"workspace select", func(m Meta) int {
			c := &WorkspaceSelectCommand{Meta: m}
			return c.Run([]string{"-no-color", "staging"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("apply"), td)
			t.Chdir(td)

			view, done := testView(t)
			tc.run(liveBlockMeta(view, newStatelessTestCloud()))
			output := done(t)

			if strings.Contains(output.Stderr()+output.Stdout(), "live resource markers") {
				t.Errorf("a configuration with no live block was refused:\nstderr:\n%s\nstdout:\n%s", output.Stderr(), output.Stdout())
			}
		})
	}
}
