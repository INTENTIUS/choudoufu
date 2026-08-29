// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command readiness-gen writes live/readiness.json, issue #418's partition:
// every provider resource type this fork's roster knows about, assigned
// exactly one of rfc/20260828-readiness-tiers.md's four tiers and exactly
// one of six statuses, plus the input facts that decided it.
//
//	go run ./tools/readiness-gen
//
// It reads only committed artifacts and in-process Go rosters - no
// provider, no network, no other generator's process. Run twice with
// nothing else changed, it writes byte-identical output; see build.go's
// package doc comment for the join this performs and what it approximates.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "readiness-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	artifact, err := Build(root)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", OutputJSONRel, err)
	}
	data = append(data, '\n')
	path := filepath.Join(root, filepath.FromSlash(OutputJSONRel))
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return fmt.Errorf("writing %s: %w", OutputJSONRel, err)
	}
	fmt.Printf("%s: %d types, %d in-contract\n", OutputJSONRel, artifact.Counts.Types, artifact.Counts.Statuses["in-contract"])
	return nil
}
