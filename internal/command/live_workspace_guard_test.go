// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// GitHub issue #2221: under a live block a workspace is accepted and ignored,
// with a warning, and the estate's records are used exactly as in the default
// workspace. These tests replace the #2215 refusals.

func wsFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

// recordTree returns every file under dir/.tofu-records with its content,
// leaving out the workspace registry (the one key a non-default workspace is
// allowed to add) and the hint (it carries a timestamp).
func recordTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(dir, ".tofu-records")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if strings.Contains(rel, "tofu-hints") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

func recordNames(m map[string]string) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

// TestLiveWorkspace_commandsAccepted: workspace new and select succeed under
// a live block for a non-default name, warn that it is ignored, create no
// state, and leave "workspace show" returning what was selected.
func TestLiveWorkspace_commandsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(m Meta) int
	}{
		{"workspace new", func(m Meta) int {
			return (&WorkspaceNewCommand{Meta: m}).Run([]string{"-no-color", "plat-ue2-dev"})
		}},
		{"workspace select", func(m Meta) int {
			return (&WorkspaceSelectCommand{Meta: m}).Run([]string{"-no-color", "plat-ue2-dev"})
		}},
		{"workspace select -or-create", func(m Meta) int {
			return (&WorkspaceSelectCommand{Meta: m}).Run([]string{"-no-color", "-or-create", "plat-ue2-dev"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			code := tc.run(liveBlockMeta(view, liveBlockCloud()))
			output := done(t)
			if code != 0 {
				t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			got := wsFlat(output.Stderr() + output.Stdout())
			for _, want := range []string{
				`Workspace "plat-ue2-dev" ignored under live resource markers; this estate is "live-unit". Separate environments need separate estate names.`,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			assertNoStateArtifacts(t, td)

			// show returns what the tool selected.
			view, done = testView(t)
			if code := (&WorkspaceShowCommand{Meta: liveBlockMeta(view, liveBlockCloud())}).Run([]string{"-no-color"}); code != 0 {
				t.Fatalf("workspace show exit %d", code)
			}
			if got := strings.TrimSpace(done(t).Stdout()); got != "plat-ue2-dev" {
				t.Errorf("workspace show printed %q, want the selected name", got)
			}

			// list shows default plus the selected name, current marked.
			view, done = testView(t)
			if code := (&WorkspaceListCommand{Meta: liveBlockMeta(view, liveBlockCloud())}).Run([]string{"-no-color"}); code != 0 {
				t.Fatalf("workspace list exit %d", code)
			}
			list := done(t).Stdout()
			if !strings.Contains(list, "default") || !strings.Contains(list, "* plat-ue2-dev") {
				t.Errorf("workspace list lacks default or the selected name:\n%s", list)
			}
			assertNoStateArtifacts(t, td)
		})
	}
}

// TestLiveWorkspace_listDefaultOnly: with nothing selected, list is just
// default.
func TestLiveWorkspace_listDefaultOnly(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	view, done := testView(t)
	if code := (&WorkspaceListCommand{Meta: liveBlockMeta(view, liveBlockCloud())}).Run([]string{"-no-color"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := strings.Fields(done(t).Stdout()); !reflect.DeepEqual(got, []string{"*", "default"}) {
		t.Errorf("list is %v, want only the default workspace", got)
	}
}

// TestLiveWorkspace_selectDefaultIsSilent: the way back to default carries no
// warning.
func TestLiveWorkspace_selectDefaultIsSilent(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	view, done := testView(t)
	code := (&WorkspaceSelectCommand{Meta: liveBlockMeta(view, liveBlockCloud())}).Run([]string{"-no-color", "default"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, output.Stdout(), output.Stderr())
	}
	if got := output.Stderr() + output.Stdout(); strings.Contains(got, "ignored") {
		t.Errorf("selecting default warned:\n%s", got)
	}
}

// TestLiveWorkspace_invalidNameStillRefused: a name that is not a valid
// workspace name is an error as it always was.
func TestLiveWorkspace_invalidNameStillRefused(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	view, done := testView(t)
	code := (&WorkspaceNewCommand{Meta: liveBlockMeta(view, liveBlockCloud())}).Run([]string{"-no-color", "a/b"})
	output := done(t)
	if code != 1 || !strings.Contains(output.Stderr()+output.Stdout(), "Invalid workspace name") {
		t.Fatalf("exit %d, want 1 with an invalid-name error\n%s%s", code, output.Stdout(), output.Stderr())
	}
}

// TestLiveWorkspace_unguarded: a configuration with no live block gets no
// live-mode message from the workspace commands.
func TestLiveWorkspace_unguarded(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(m Meta) int
	}{
		{"workspace new", func(m Meta) int {
			return (&WorkspaceNewCommand{Meta: m}).Run([]string{"-no-color", "staging"})
		}},
		{"workspace select", func(m Meta) int {
			return (&WorkspaceSelectCommand{Meta: m}).Run([]string{"-no-color", "staging"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("apply"), td)
			t.Chdir(td)

			view, done := testView(t)
			tc.run(liveBlockMeta(view, newLiveTestCloud()))
			output := done(t)
			if strings.Contains(output.Stderr()+output.Stdout(), "live resource markers") {
				t.Errorf("a configuration with no live block was told about live markers:\n%s%s", output.Stderr(), output.Stdout())
			}
		})
	}
}

// TestLiveWorkspace_tfWorkspacePlan: TF_WORKSPACE (how Atmos and most wrappers
// pass a workspace) no longer fails a plan; it warns and plans.
func TestLiveWorkspace_tfWorkspacePlan(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)
	t.Setenv("TF_WORKSPACE", "plat-ue2-dev")

	c, done := newLiveBlockPlanCommand(t, liveBlockCloud())
	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	got := wsFlat(output.Stderr() + output.Stdout())
	if want := `Workspace "plat-ue2-dev" ignored under live resource markers; this estate is "live-unit".`; !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	if strings.Count(got, `Workspace "plat-ue2-dev" ignored`) != 1 {
		t.Errorf("the warning should appear once:\n%s", got)
	}
	assertNoStateArtifacts(t, td)
}

func applyInWorkspace(t *testing.T, td, workspace string) string {
	t.Helper()
	t.Chdir(td)
	if workspace != "" {
		t.Setenv("TF_WORKSPACE", workspace)
	} else {
		t.Setenv("TF_WORKSPACE", "")
	}
	view, done := testView(t)
	c := &ApplyCommand{Meta: liveBlockMeta(view, newLiveTestCloud())}
	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	if code != 0 {
		t.Fatalf("apply in workspace %q: exit %d\nstdout:\n%s\nstderr:\n%s", workspace, code, output.Stdout(), output.Stderr())
	}
	if !strings.Contains(output.Stdout(), "Apply complete! Resources: 2 added") {
		t.Fatalf("apply in workspace %q did not create both resources:\n%s", workspace, output.Stdout())
	}
	return wsFlat(output.Stderr() + output.Stdout())
}

// TestLiveWorkspace_applyRecordsMatchDefault: an apply under a selected
// workspace leaves the estate's records exactly as the default workspace's
// apply does, plus nothing keyed by the workspace.
func TestLiveWorkspace_applyRecordsMatchDefault(t *testing.T) {
	def := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), def)
	applyInWorkspace(t, def, "")

	ws := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), ws)
	out := applyInWorkspace(t, ws, "plat-ue2-dev")
	if !strings.Contains(out, `Workspace "plat-ue2-dev" ignored`) {
		t.Errorf("no ignore warning:\n%s", out)
	}

	a, b := recordTree(t, def), recordTree(t, ws)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("records differ between default and workspace runs:\ndefault: %v\nworkspace: %v", recordNames(a), recordNames(b))
	}
	if len(a) == 0 {
		t.Error("the default run wrote no records, so the comparison proves nothing")
	}
	for k := range b {
		if strings.Contains(k, "plat-ue2-dev") {
			t.Errorf("a record is keyed by the workspace: %s", k)
		}
	}
	assertNoStateArtifacts(t, ws)
}

// TestLiveWorkspace_sharedEstateWarns: a second workspace applying to the same
// estate is told, naming both; a repeat of the same workspace is not.
func TestLiveWorkspace_sharedEstateWarns(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), td)

	first := applyInWorkspace(t, td, "plat-ue2-dev")
	if strings.Contains(first, "share this estate") {
		t.Errorf("the first workspace was warned about sharing:\n%s", first)
	}
	again := applyInWorkspace(t, td, "plat-ue2-dev")
	if strings.Contains(again, "share this estate") {
		t.Errorf("the same workspace twice was warned about sharing:\n%s", again)
	}

	second := applyInWorkspace(t, td, "plat-ue2-prod")
	for _, want := range []string{`Workspace "plat-ue2-prod" and workspace "plat-ue2-dev" both run against estate "live-unit"`, "Workspaces share this estate's records"} {
		if !strings.Contains(second, want) {
			t.Errorf("missing %q in:\n%s", want, second)
		}
	}
}
