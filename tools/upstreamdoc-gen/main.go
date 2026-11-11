// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// upstreamdoc-gen renders how choudoufu differs from upstream OpenTofu
// (#2256) into two files:
//
//   - the "base" span of the site page
//     site/src/content/docs/how-it-works/upstream.md: the upstream OpenTofu
//     commit choudoufu is built on, from the committed
//     live/fork-surface.json, which scripts/upstream-sync.sh --finish
//     regenerates on every sync;
//   - live/NEW-RUNTIME.md, whole: the same base and the new runtime's gap
//     tables, from internal/live/newruntime/gaps.json through that
//     package's own parser, so the file shows exactly the rows the refusal
//     checks read, and the fork ports registered in internal/engine/forkport
//     with their switches and expiry issues. The tables live in the repository, not on the site,
//     because the site is held to a word limit (live/site_size_test.go).
//
// Like tools/toggles-gen it reads only committed sources: no git, no
// network. The page's span uses internal/live/mdspan's markers, so the hand-written
// prose around it passes through byte for byte. render_test.go fails when
// either committed file differs from what this tool would write.
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

	"github.com/intentius/choudoufu/internal/live/newruntime"
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
	fs, err := loadForkSurface(root)
	if err != nil {
		return err
	}
	page, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pageRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", pageRel, err)
	}
	out, err := renderPage(fs, string(page))
	if err != nil {
		return err
	}
	if err := write(root, pageRel, out); err != nil {
		return err
	}
	return write(root, repoDocRel, renderRepoDoc(fs, newruntime.Rows()))
}

// write writes content to rel only if it changed.
func write(root, rel, content string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if old, err := os.ReadFile(path); err == nil && string(old) == content { //nolint:gosec // a fixed path in the checkout
		fmt.Fprintf(os.Stderr, "upstreamdoc-gen: %s is already current\n", rel)
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // a committed doc
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	fmt.Fprintf(os.Stderr, "upstreamdoc-gen: wrote %s\n", rel)
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
