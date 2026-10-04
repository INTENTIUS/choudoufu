// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// discardedRetry is the shape #1208 found: an init run quietly, and on
// failure the same init run AGAIN only to print, with the retry's own exit
// status thrown away (its pipeline ends in `tail`, and the subshell is
// followed by `; fail`, not `|| fail`). A transient first failure then
// records verdict=fail directly under a retry that printed "successfully
// initialized".
var discardedRetry = regexp.MustCompile(`\| tail -[0-9]+ \); fail "`)

// honouredRetry is the shape every such site uses since #1208: the retry's
// subshell exits with the init's own status, and only a second failure
// fails the stage.
var honouredRetry = regexp.MustCompile(`\| tail -[0-9]+; exit "\$\{PIPESTATUS\[0\]\}" \) \|\| fail "`)

// TestNoRetryDiscardsItsOwnExit (#1208): no estate or live-cert script may
// fail a stage while ignoring whether its own diagnostic retry succeeded.
// It also requires the honoured shape to be present, so a refactor that
// renames every site cannot leave this guard scanning for nothing.
func TestNoRetryDiscardsItsOwnExit(t *testing.T) {
	root := testRoot(t)
	var paths []string
	for _, pat := range []string{
		filepath.Join(root, "live", "e2e", "*", "run.sh"),
		filepath.Join(root, "live", "e2e", "*", "*.sh"),
		filepath.Join(root, "live", "live-cert", "*.sh"),
	} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, m...)
	}
	if len(paths) == 0 {
		t.Fatal("no estate scripts found under live/e2e or live/live-cert; this guard would pass vacuously")
	}
	seen := map[string]bool{}
	honoured := 0
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if discardedRetry.MatchString(line) {
				rel, _ := filepath.Rel(root, p)
				t.Errorf("%s:%d: a diagnostic retry's exit status is discarded, so a retry that succeeds still fails the stage (#1208); end the retry's subshell with `exit \"${PIPESTATUS[0]}\" ) || fail \"...\"`:\n\t%s", rel, i+1, strings.TrimSpace(line))
			}
			if honouredRetry.MatchString(line) {
				honoured++
			}
		}
	}
	if honoured == 0 {
		t.Fatal("found no init retry in the #1208 shape in any script; the patterns above no longer describe the tree and this guard proves nothing")
	}
}

// TestRetryPatternsAreDistinct proves the two patterns above can tell the
// shapes apart, so the guard can actually fail: the pre-#1208 line from
// corpus-vpc-complete matches discardedRetry and not honouredRetry, and its
// rewrite the other way round.
func TestRetryPatternsAreDistinct(t *testing.T) {
	before := `  ( cd "$D" && "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -20 ); fail "the day2_count oracle's init failed"; }`
	after := `  ( cd "$D" && "$TF_COLD_BIN" init -input=false -no-color 2>&1 | tail -20; exit "${PIPESTATUS[0]}" ) || fail "the day2_count oracle's init failed"; }`
	if !discardedRetry.MatchString(before) || honouredRetry.MatchString(before) {
		t.Errorf("the pre-#1208 line is not classified as discarding its retry")
	}
	if discardedRetry.MatchString(after) || !honouredRetry.MatchString(after) {
		t.Errorf("the #1208 rewrite is not classified as honouring its retry")
	}
}
