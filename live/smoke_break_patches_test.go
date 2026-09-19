// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	breakPatchSource  = regexp.MustCompile(`(?m)^\s*SRC="\$ROOT/([^"]+)"`)
	breakPatchHeredoc = regexp.MustCompile(`(?ms)^\s*python3 - "\$SRC" "[^"\n]+" <<'PYEOF'\n(.*?)\nPYEOF$`)
)

// TestEveryBreakPatchStillMatchesItsSource runs the source patch of each
// smoke scenario whose BREAK arm rebuilds the binary with `go build
// -overlay`.
//
// Such an arm corrupts one Go file with a Python string replacement and
// asserts that the old text is there exactly once. When the Go file changes,
// the arm stops with "the break patch no longer matches" and proves nothing.
// For a scenario that runs in CI that is caught on the next run. For a
// real-AWS scenario nothing runs the arm until somebody does it by hand:
// claim 36's went stale when GitHub issue #1383 swapped the precedence of
// base tags and context tags in S3Store.PutIfVersion, and stayed stale
// across several merges until a maintainer's run on real AWS met it (GitHub
// issue #1379).
//
// The patch needs no AWS and no build, only the file it patches, so it runs
// here against this checkout. It has to exit zero and change the file.
func TestEveryBreakPatchStillMatchesItsSource(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is not on PATH, and every patch below is a python3 program: %v", err)
	}
	scenarios, err := filepath.Glob(filepath.Join("smoke", "scenarios", "*.sh"))
	if err != nil || len(scenarios) == 0 {
		t.Fatalf("no smoke scenarios found: %v", err)
	}
	patched := 0
	for _, path := range scenarios {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		heredocs := breakPatchHeredoc.FindAllStringSubmatch(text, -1)
		if strings.Contains(text, "go build -overlay") && strings.Contains(text, `python3 - "$SRC"`) && len(heredocs) == 0 {
			t.Errorf("%s patches a source file for its BREAK arm in a shape this test cannot extract, so the patch is unguarded", path)
			continue
		}
		if len(heredocs) == 0 {
			continue
		}
		src := breakPatchSource.FindStringSubmatch(text)
		if src == nil {
			t.Errorf("%s has a BREAK patch and no SRC=\"$ROOT/...\" line saying which file it patches", path)
			continue
		}
		source := filepath.Join("..", filepath.FromSlash(src[1]))
		before, err := os.ReadFile(source)
		if err != nil {
			t.Errorf("%s patches %s, which cannot be read: %v", path, src[1], err)
			continue
		}
		for _, h := range heredocs {
			patched++
			out := filepath.Join(t.TempDir(), "patched.go")
			cmd := exec.Command("python3", "-", source, out)
			cmd.Stdin = strings.NewReader(h[1])
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Errorf("%s: the BREAK patch does not apply to %s any more, so that arm stops before it measures anything:\n%s", path, src[1], strings.TrimSpace(stderr.String()))
				continue
			}
			after, err := os.ReadFile(out)
			if err != nil {
				t.Errorf("%s: the BREAK patch exited zero and wrote nothing: %v", path, err)
				continue
			}
			if bytes.Equal(before, after) {
				t.Errorf("%s: the BREAK patch applied and changed nothing in %s, so the arm would test the real binary", path, src[1])
			}
		}
	}
	// The scenarios that patch source today are claims 29, 31, 32, 33 and 36.
	// Fewer than that means the extraction above stopped seeing them.
	if patched < 5 {
		t.Errorf("only %d BREAK patch(es) were found and run; there were five when this test was written", patched)
	}
}
