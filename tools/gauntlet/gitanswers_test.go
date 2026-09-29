// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// #1220's tier 3 inside this package: resolveRev, resolveCommit and
// changedPaths each ran exec.Cmd.Output() with no stderr buffer, so their
// errors named the operation and a bare "exit status 128" and nothing git
// itself said. changedPaths backs checkNoProductCodeMoved, the safety
// precondition every other merge-artifact check assumes.

func TestGitBackedResolversQuoteGitWhenItCannotAnswer(t *testing.T) {
	root := initTestRepo(t)
	brokenGitOnPATH(t)

	if got, err := resolveRev(root, "HEAD"); err == nil || !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("resolveRev = (%q, %v); want an error quoting git's stderr", got, err)
	}
	if got, err := resolveCommit(root, "HEAD"); err == nil || !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("resolveCommit = (%q, %v); want an error quoting git's stderr", got, err)
	}
	if got, err := changedPaths(root, "HEAD", "HEAD"); err == nil || !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("changedPaths = (%v, %v); want an error quoting git's stderr", got, err)
	}
}
