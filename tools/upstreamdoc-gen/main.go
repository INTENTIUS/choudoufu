// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// upstreamdoc-gen renders the generated spans of the site's upstream page,
// site/src/content/docs/how-it-works/upstream.md (#2256):
//
//   - "base", the upstream OpenTofu commit choudoufu is built on, from the
//     committed live/fork-surface.json, which scripts/upstream-sync.sh
//     --finish regenerates on every sync;
//   - "gaps", the new runtime's gap table, from
//     internal/live/newruntime/gaps.json through that package's own parser,
//     so the page shows exactly the rows the refusal checks read.
//
// Like tools/toggles-gen it reads only committed sources: no git, no
// network. The spans use internal/live/mdspan's markers, so the hand-written
// prose around them passes through byte for byte. render_test.go fails when
// the committed page differs from what this tool would write.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/upstreamdoc-gen
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	root, err := repoRoot()
	if err == nil {
		err = run(root)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "upstreamdoc-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(root string) error {
	path := filepath.Join(root, filepath.FromSlash(pageRel))
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", pageRel, err)
	}
	out, err := renderPage(root, string(doc))
	if err != nil {
		return err
	}
	if out == string(doc) {
		fmt.Fprintf(os.Stderr, "upstreamdoc-gen: %s is already current\n", pageRel)
		return nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc
		return fmt.Errorf("writing %s: %w", pageRel, err)
	}
	fmt.Fprintf(os.Stderr, "upstreamdoc-gen: wrote %s\n", pageRel)
	return nil
}

// repoRoot resolves the checkout's root from this file's own location, the
// same way every other tools/*-gen does.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/upstreamdoc-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}
