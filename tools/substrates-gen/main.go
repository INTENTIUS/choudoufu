// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	render := flag.Bool("render", false,
		"copy the committed live/substrates.json byte-for-byte to site/data/substrates.json instead of regenerating the artifact")
	flag.Parse()

	if *render {
		if err := runRender(); err != nil {
			fmt.Fprintf(os.Stderr, "substrates-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "substrates-gen: %v\n", err)
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
	fmt.Printf("%s: %d rows\n", OutputJSONRel, len(artifact.Rows))
	return nil
}

// runRender copies the already-committed live/substrates.json to
// site/data/substrates.json byte-for-byte, the same split
// tools/readiness-gen's -render mode and live/smoke/claims.json's site copy
// both use: it needs no provider, no network, and does not recompute the
// artifact.
func runRender() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	src := filepath.Join(root, filepath.FromSlash(OutputJSONRel))
	data, err := os.ReadFile(src) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", OutputJSONRel, err)
	}
	dst := filepath.Join(root, filepath.FromSlash(SiteDataJSONRel))
	if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return fmt.Errorf("writing %s: %w", SiteDataJSONRel, err)
	}
	fmt.Printf("%s: copied from %s\n", SiteDataJSONRel, OutputJSONRel)
	return nil
}
