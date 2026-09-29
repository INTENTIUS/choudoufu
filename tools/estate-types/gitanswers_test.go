// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1220: repoRoot mislabelled every git failure "not in a git checkout:
// exit status 128", destroying git's own message.

func brokenGitOnPATH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" +
		"echo \"fatal: You have not agreed to the Xcode license agreements.\" >&2\n" +
		"exit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(body), 0o755); err != nil { //nolint:gosec // a test fixture on a temp PATH
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRepoRootQuotesGitWhenItCannotAnswer(t *testing.T) {
	brokenGitOnPATH(t)
	root, err := repoRoot()
	if err == nil {
		t.Fatalf("repoRoot returned %q with a git that exits 128", root)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it", err)
	}
}
