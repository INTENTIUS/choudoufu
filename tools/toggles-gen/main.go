// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// toggles-gen renders site/content/docs/use/reference.md's `strict` block
// argument table from internal/live/strict's own [strict.Toggles] registry
// (GitHub issue #365's consolidation).
//
// Unlike tools/tagverbs-gen, this generator needs no network fetch and
// writes no separate JSON artifact: [strict.Toggles] IS the committed
// source, a Go value in the tree already, so rendering it is a pure
// in-process transform. That is also why docspan_test.go's drift guard
// needs no companion artifact-reading step the way
// tools/tagverbs-gen/docspan_test.go's TestSpansAreCurrent does - it calls
// [renderToggleTable] on [strict.Toggles] directly.
//
// A 2026-08-24 audit of issue #365 found the table this generator now owns
// entirely hand-maintained, with strict.Toggle.Doc (and the rest of the
// registry) read by nothing: the "reference page is rendered from the
// schema" claim in HANDOFF.md's default section was false. This closes
// that gap for the `strict` block table specifically; the `record_store`
// and `policy` block tables above it in reference.md are still
// hand-written, because neither has a registry like [strict.Toggles] to
// render from yet.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/toggles-gen
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/intentius/choudoufu/internal/live/mdspan"
	"github.com/intentius/choudoufu/internal/live/strict"
)

var markers = mdspan.For("toggles-gen")

const (
	referenceMDRel  = "site/content/docs/use/reference.md"
	spanStrictTable = "strict-toggles"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "toggles-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(log *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if err := renderToggleSpan(root, strict.Toggles); err != nil {
		return err
	}
	fmt.Fprintf(log, "toggles-gen: wrote %s (%d toggles)\n", referenceMDRel, len(strict.Toggles))
	return nil
}

// repoRoot resolves the checkout's root from this file's own location, the
// same trick every other tools/*-gen's repoRoot uses.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/toggles-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}
