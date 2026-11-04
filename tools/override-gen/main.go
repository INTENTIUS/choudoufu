// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command override-gen writes live/overrides.json, the table of provider
// behaviour overrides (GitHub issue #2309), from internal/live/override's
// registry. Run it from the repository root after adding or changing an
// override; internal/live/override's TestGeneratedTableIsCurrent fails
// until it has been run.
package main

import (
	"fmt"
	"os"

	"github.com/intentius/choudoufu/internal/live/override"
)

func main() {
	out, err := override.TableJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(override.TablePath, out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s: %d overrides\n", override.TablePath, len(override.All()))
}
