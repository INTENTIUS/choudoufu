// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Verify builds the whole module and runs the fast test tiers that cover
// everything REGENERATE can touch: the *-gen tools themselves, the
// admission machinery under internal/live, and live/'s own residue tests. A
// nonzero exit from either command fails the run.
func Verify(root string, log io.Writer) error {
	if err := runLogged(root, log, "go", "build", "./..."); err != nil {
		return fmt.Errorf("go build ./...: %w", err)
	}
	if err := runLogged(root, log, "go", "test", "./tools/...", "./internal/live/...", "./live/..."); err != nil {
		return fmt.Errorf("go test ./tools/... ./internal/live/... ./live/...: %w", err)
	}
	return nil
}

// runLogged runs a command from root with both stdout and stderr streamed
// to log.
func runLogged(root string, log io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec // a fixed, non-attacker-controlled command list
	cmd.Dir = root
	cmd.Env = os.Environ()
	cmd.Stdout = log
	cmd.Stderr = log
	fmt.Fprintf(log, "admission-pipeline: running %s %s\n", name, strings.Join(args, " "))
	return cmd.Run()
}
