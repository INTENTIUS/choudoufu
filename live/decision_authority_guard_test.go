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
	"strings"
	"testing"
)

// Decisions in this repository live in exactly two places: the code that
// enforces them (a guard test, a schema default, a fixture that fails red)
// and the tracker. Never in a prose directory that comments cite as
// authority.
//
// This is a mechanism guard, not a directory-name guard, because the
// directory-name version already failed once: the rfc/ directory was
// retired 2026-08-30 by renaming it to rulings/, and within two days the
// new directory had grown from six documents and 75 citations to thirteen
// documents and 126 citations, with a README defending the pattern. The
// dissolve that followed (issue #685's ruling record, branch
// live/rulings-dissolve) moved every document onto the issue named in its
// own header and rewrote every citation to name the issue, the guard or
// the fixture that holds the decision.
//
// What this guard refuses, so it cannot happen a third time:
//
//   - a tracked directory named rfc/, rulings/ or decisions/;
//   - any tracked file citing a dated decision document by path
//     (rfc/YYYYMMDD-*, rulings/YYYYMMDD-*, decisions/YYYYMMDD-*), with
//     three history exceptions: CHANGELOG.md; live/fork-surface.json,
//     whose rows record the deleted upstream rfc/ paths as inventory; and
//     live/history/, whose files are frozen release snapshots that are
//     never rewritten (cmdSnapshot byte-copies deliberately);
//   - a local or remote git branch under rfc/ or rulings/.
//
// Citing an upstream OpenTofu RFC by full URL is fine - that is
// upstream's document about upstream's decision. Mentioning the words
// "rfc" or "rulings" in prose (as this file just did) is fine. What is
// refused is the mechanism: a repo-local prose path standing where a
// guard or an issue number belongs.
func TestNoProseDecisionAuthority(t *testing.T) {
	root := repoRoot(t)

	for _, dir := range []string{"rfc", "rulings", "decisions"} {
		if st, err := os.Stat(filepath.Join(root, dir)); err == nil && st.IsDir() {
			t.Errorf("tracked directory %s/ exists; decisions live in guards and the tracker, not in a prose directory - see this test's doc comment for how the last two attempts to keep one went", dir)
		}
	}

	cite := regexp.MustCompile(`(?:rfc|rulings|decisions)/[0-9]{8}-[A-Za-z0-9-]+`)
	out, err := exec.Command("git", "-C", root, "grep", "-Il", "-E", `(rfc|rulings|decisions)/[0-9]{8}-`, "--", ".", ":!CHANGELOG.md", ":!live/fork-surface.json", ":!live/history/").Output()
	if err == nil { // git grep exits nonzero when nothing matches, which is the pass
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f == "" {
				continue
			}
			b, rerr := os.ReadFile(filepath.Join(root, f))
			if rerr != nil {
				t.Fatalf("reading %s: %v", f, rerr)
			}
			flagged := 0
			for _, line := range strings.Split(string(b), "\n") {
				// An upstream RFC cited by its full upstream URL is
				// upstream's document about upstream's decision; only a
				// repo-local path is a prose authority here.
				if strings.Contains(line, "opentofu/opentofu") {
					continue
				}
				for _, m := range cite.FindAllString(line, 2) {
					t.Errorf("%s cites %q as decision authority; cite the issue, the guard test or the fixture that holds the decision instead", f, m)
					flagged++
				}
				if flagged >= 3 {
					break
				}
			}
		}
	}

	br, err := exec.Command("git", "-C", root, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes/origin").Output()
	if err != nil {
		t.Fatalf("listing branches: %v", err)
	}
	for _, b := range strings.Split(strings.TrimSpace(string(br)), "\n") {
		name := strings.TrimPrefix(b, "origin/")
		if strings.HasPrefix(name, "rfc/") || strings.HasPrefix(name, "rulings/") {
			t.Errorf("branch %q writes decisions under a prose-authority namespace; use live/<topic> and put the decision on an issue", b)
		}
	}
}
