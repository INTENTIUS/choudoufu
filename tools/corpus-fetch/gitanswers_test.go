// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1220: headCommit and gitOutput returned exec's bare error, so a
// corpus-fetch on a machine whose git cannot run reported "exit status
// 128" and nothing else.

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

func TestHeadCommitQuotesGitWhenItCannotAnswer(t *testing.T) {
	brokenGitOnPATH(t)
	at, err := headCommit(t.TempDir())
	if err == nil {
		t.Fatalf("headCommit returned %q with a git that exits 128", at)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it", err)
	}
}

func TestGitOutputQuotesGitWhenItCannotAnswer(t *testing.T) {
	brokenGitOnPATH(t)
	out, err := gitOutput(context.Background(), t.TempDir(), "rev-parse", "HEAD")
	if err == nil {
		t.Fatalf("gitOutput returned %q with a git that exits 128", out)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it", err)
	}
}
