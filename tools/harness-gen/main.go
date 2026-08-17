// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command harness-gen renders live/HARNESS.md's generated spans from the
// burndown and assumptions registries in internal/live/harness.
//
// It runs every measurement and every assumption check, so a run that
// succeeds is also a run in which the whole harness held. It reads only
// committed artifacts and in-process Go rosters: no provider, no network,
// no generator.
//
//	go run ./tools/harness-gen
package main

import (
	"fmt"
	"os"

	"github.com/intentius/choudoufu/internal/live/harness"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "harness-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	repo, err := harness.Open(".")
	if err != nil {
		return err
	}
	path := repo.Path(harness.DocRel)
	before, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", harness.DocRel, err)
	}
	after, err := harness.Render(repo, string(before))
	if err != nil {
		return err
	}
	if after == string(before) {
		fmt.Printf("%s unchanged\n", harness.DocRel)
		return nil
	}
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", harness.DocRel, err)
	}
	fmt.Printf("%s rewritten\n", harness.DocRel)
	return nil
}
