// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/largeset"
)

func liveAffectedGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestLiveAffectedCommand runs the command over the #1750 fixture and
// checks the exit status contract the help documents: 0 for a
// determinate answer, 2 for an indeterminate one, 1 for a bad range.
func TestLiveAffectedCommand(t *testing.T) {
	dir := t.TempDir()
	if _, err := largeset.Write(dir, largeset.Options{Estates: 5, ModuleVersion: largeset.VersionA}); err != nil {
		t.Fatal(err)
	}
	liveAffectedGit(t, dir, "init", "-q", "-b", "main")
	liveAffectedGit(t, dir, "add", "-A")
	liveAffectedGit(t, dir, "commit", "-q", "-m", "base")
	base := liveAffectedGit(t, dir, "rev-parse", "HEAD")

	edit := func(rel, old, new string) string {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		liveAffectedGit(t, dir, "commit", "-q", "-am", rel)
		return liveAffectedGit(t, dir, "rev-parse", "HEAD")
	}
	oneRoot := edit("estates/e01/main.tf", "# Root e01", "# Root e01, edited")
	providerBump := edit("estates/e05/main.tf", `version = "= 6.59.0"`, `version = "= 6.60.0"`)
	t.Chdir(filepath.Join(dir, "estates"))

	run := func(args ...string) (int, string, string) {
		t.Helper()
		view, done := testView(t)
		code := (&LiveAffectedCommand{Meta: Meta{View: view}}).Run(args)
		out := done(t)
		return code, out.Stdout(), out.Stderr()
	}

	t.Run("determinate text", func(t *testing.T) {
		code, stdout, stderr := run(base + ".." + oneRoot)
		if code != 0 {
			t.Fatalf("exit %d, want 0\n%s%s", code, stdout, stderr)
		}
		for _, want := range []string{
			"3 of 5 estate roots affected.",
			"estates/e01 (ls-e01): changed",
			"estates/e02 (ls-e02): reads ls-e01",
			"estates/e03 (ls-e03): reads ls-e02",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("output lacks %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("indeterminate json exits 2", func(t *testing.T) {
		code, stdout, stderr := run("-json", oneRoot+".."+providerBump)
		if code != LiveAffectedExitIndeterminate {
			t.Fatalf("exit %d, want %d\n%s%s", code, LiveAffectedExitIndeterminate, stdout, stderr)
		}
		var doc struct {
			Outcome       string `json:"outcome"`
			Indeterminate []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"indeterminate"`
		}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("not one JSON document: %v\n%s", err, stdout)
		}
		if doc.Outcome != "indeterminate" || len(doc.Indeterminate) != 1 || doc.Indeterminate[0].Kind != "provider-version" ||
			!strings.Contains(doc.Indeterminate[0].Text, `"= 6.59.0" -> "= 6.60.0"`) {
			t.Errorf("unexpected document:\n%s", stdout)
		}
	})

	t.Run("a bad range exits 1", func(t *testing.T) {
		if code, stdout, _ := run("nope..HEAD"); code != 1 {
			t.Errorf("exit %d, want 1\n%s", code, stdout)
		}
	})

	t.Run("no range is a usage error", func(t *testing.T) {
		if code, _, stderr := run(); code != 1 || !strings.Contains(stderr, "exactly one git range") {
			t.Errorf("exit %d, want 1 naming the range\n%s", code, stderr)
		}
	})

	t.Run("help documents the exit status", func(t *testing.T) {
		help := (&LiveAffectedCommand{}).Help()
		for _, want := range []string{"2  indeterminate: plan every root", "modules.json", "\"determinate\" or \"indeterminate\""} {
			if !strings.Contains(help, want) {
				t.Errorf("help lacks %q", want)
			}
		}
	})
}
