// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Issue #1316: a nightly whose failures nobody sees is not better than no
// nightly. nightly-watch.yml is the one workflow that reacts to another
// finishing, and a workflow_run trigger that names a workflow which was
// renamed or never existed is silent - so the list it watches is derived
// here from every workflow file that carries a cron, and the two have to
// agree exactly.
func TestNightlyWatchNamesEveryScheduledWorkflow(t *testing.T) {
	root := repoRoot(t)
	wfDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		t.Fatalf("reading %s: %v", wfDir, err)
	}
	nameLine := regexp.MustCompile(`(?m)^name:\s*(.+?)\s*$`)
	var scheduled []string
	var watch string
	for _, e := range entries {
		b, rerr := os.ReadFile(filepath.Join(wfDir, e.Name()))
		if rerr != nil {
			t.Fatalf("reading workflow %s: %v", e.Name(), rerr)
		}
		s := string(b)
		if e.Name() == "nightly-watch.yml" {
			watch = s
			continue
		}
		if !strings.Contains(s, "cron:") {
			continue
		}
		m := nameLine.FindStringSubmatch(s)
		if m == nil {
			t.Fatalf("%s has a cron but no top-level name:, so nothing can watch it by name", e.Name())
		}
		scheduled = append(scheduled, strings.Trim(m[1], `"'`))
	}
	if len(scheduled) < 2 {
		t.Fatalf("found only %d scheduled workflows (%v); the derivation is broken rather than the repository having one nightly", len(scheduled), scheduled)
	}
	if watch == "" {
		t.Fatal(".github/workflows/nightly-watch.yml is missing; a red nightly is once again a thing only the Actions page knows (#1316)")
	}

	listLine := regexp.MustCompile(`(?m)^\s*workflows:\s*\[([^\]]*)\]`)
	m := listLine.FindStringSubmatch(watch)
	if m == nil {
		t.Fatal("nightly-watch.yml has no `workflows: [...]` list under workflow_run")
	}
	var watched []string
	for _, f := range strings.Split(m[1], ",") {
		if f = strings.Trim(strings.TrimSpace(f), `"'`); f != "" {
			watched = append(watched, f)
		}
	}
	sort.Strings(scheduled)
	sort.Strings(watched)
	if strings.Join(scheduled, "|") != strings.Join(watched, "|") {
		t.Errorf("nightly-watch.yml watches %v but the workflows with a cron are named %v; a nightly that is not on the list fails in silence", watched, scheduled)
	}
	if !strings.Contains(watch, "issues: write") {
		t.Error("nightly-watch.yml does not request issues: write, so it cannot open or close the nightly-red issue")
	}
	if !strings.Contains(watch, "scripts/nightly-watch.sh") {
		t.Error("nightly-watch.yml does not run scripts/nightly-watch.sh, the behaviour the test below proves")
	}
}

// TestNightlyWatchScriptOpensExtendsAndCloses drives scripts/nightly-watch.sh
// against a stubbed gh and checks which GitHub calls each conclusion
// produces. The stub records every invocation; the assertions read that
// record, so a script that quietly did nothing (the failure mode #1316 is
// about) fails here rather than in production.
func TestNightlyWatchScriptOpensExtendsAndCloses(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "nightly-watch.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("%s: %v", script, err)
	}

	cases := []struct {
		name       string
		conclusion string
		existing   string // the open issue number the stub reports, "" for none
		want       []string
		wantNot    []string
	}{
		{
			name: "first red night opens an issue", conclusion: "failure", existing: "",
			want:    []string{"label create nightly-red", "issue create", "--title nightly red: floci-tier", "--label nightly-red"},
			wantNot: []string{"issue comment", "issue close"},
		},
		{
			name: "another red night extends the open issue", conclusion: "failure", existing: "1316",
			want:    []string{"issue comment 1316"},
			wantNot: []string{"issue create", "issue close"},
		},
		{
			name: "a timeout counts as red", conclusion: "timed_out", existing: "",
			want:    []string{"issue create"},
			wantNot: []string{"issue close"},
		},
		{
			name: "the next green closes it", conclusion: "success", existing: "1316",
			want:    []string{"issue comment 1316", "issue close 1316"},
			wantNot: []string{"issue create"},
		},
		{
			name: "green with nothing open does nothing", conclusion: "success", existing: "",
			wantNot: []string{"issue create", "issue comment", "issue close"},
		},
		{
			name: "a cancelled run is not a verdict", conclusion: "cancelled", existing: "",
			wantNot: []string{"issue create", "issue comment", "issue close"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "gh.log")
			stub := filepath.Join(dir, "gh")
			// The stub answers the three reads the script makes and records
			// everything. `issue list` prints the open issue number the case
			// configures (the script's --jq already reduces the real answer
			// to that); `run view` prints one failed-job line; `issue create`
			// prints a URL the way the real command does.
			body := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GH_LOG"
case "$1 $2" in
  "issue list") printf '%s' "$STUB_EXISTING" ;;
  "run view") printf -- '- job test-floci: failure (failed step: Run the floci tier)\n' ;;
  "issue create") printf 'https://github.com/INTENTIUS/choudoufu/issues/9999\n' ;;
esac
`
			if err := os.WriteFile(stub, []byte(body), 0o755); err != nil { //nolint:gosec // a test stub
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GH_LOG="+log,
				"STUB_EXISTING="+tc.existing,
				"REPO=INTENTIUS/choudoufu",
				"WF_NAME=floci-tier",
				"RUN_ID=35587562578",
				"RUN_URL=https://github.com/INTENTIUS/choudoufu/actions/runs/35587562578",
				"CONCLUSION="+tc.conclusion,
				"HEAD_SHA=efe86e7db9abcdef0123",
				"RUN_EVENT=schedule",
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("nightly-watch.sh exited %v:\n%s", err, out)
			}
			logged, _ := os.ReadFile(log)
			calls := string(logged)
			for _, w := range tc.want {
				if !strings.Contains(calls, w) {
					t.Errorf("expected a gh call containing %q; calls were:\n%s\nscript output:\n%s", w, calls, out)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(calls, w) {
					t.Errorf("did not expect a gh call containing %q; calls were:\n%s", w, calls)
				}
			}
			if tc.conclusion == "failure" && tc.existing == "" {
				for _, w := range []string{"actions/runs/35587562578", "failed step: Run the floci tier", "efe86e7db9"} {
					if !strings.Contains(calls, w) {
						t.Errorf("the opened issue's body does not carry %q; calls were:\n%s", w, calls)
					}
				}
			}
		})
	}
}
