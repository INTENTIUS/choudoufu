// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AddEstate appends a manifest entry and writes a script stub. It refuses to
// overwrite an existing script so a half-written one is never lost.
func AddEstate(root string, m *Manifest, e Estate) error {
	if _, ok := m.ByName(e.Name); ok {
		return fmt.Errorf("estate %q is already in %s", e.Name, ManifestPath)
	}
	m.Estates = append(m.Estates, e)
	if err := m.Validate(); err != nil {
		return err
	}
	script := filepath.Join(root, e.ScriptPath())
	if _, err := os.Stat(script); err == nil {
		return fmt.Errorf("%s already exists; add the manifest entry by hand or pick another name", e.ScriptPath())
	}
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(script, []byte(scriptStub(e)), 0o755); err != nil {
		return err
	}
	return SaveManifest(root, m)
}

// scriptStub is the starting point for a new crossing script. Every active
// stage is wired to the protocol and reports not_run until the author fills
// it in, so a freshly added estate is honest on the site from its first run.
func scriptStub(e Estate) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...); b.WriteString("\n") }
	w("#!/usr/bin/env bash")
	w("set -uo pipefail")
	w("")
	w("# %s", e.Source)
	if e.URL != "" {
		w("# Source: %s at %s", e.URL, e.Pin)
	}
	w("#")
	w("# Gauntlet crossing script. Each stage below reports through")
	w("# live/e2e/lib/gauntlet.sh; see live/GAUNTLET.md for what each stage must")
	w("# prove and how it is compared with stock OpenTofu. Replace every")
	w("# `gauntlet_stage <id> not_run` with the real check as you implement it;")
	w("# a stage left not_run shows as such on the site rather than as a pass.")
	w("#")
	w("# Env overrides, same as every other crossing:")
	w("#   TOFU_BIN      a prebuilt choudoufu binary; skips `go build`.")
	w("#   TF_COLD_BIN   the stock binary for the cold deploy (default: terraform).")
	w("#   FLOCI_PORT    host port for the emulator (pick one no other script uses).")
	w("#   FLOCI_IMAGE   the emulator image; defaults to the digest in live/floci-image.")
	w("#   BREAK         set to 1 to corrupt an assertion and prove it is load-bearing.")
	w("")
	w("ROOT=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")/../../..\" && pwd)\"")
	w("# shellcheck source=live/e2e/lib/gauntlet.sh")
	w("source \"$ROOT/live/e2e/lib/gauntlet.sh\"")
	w("gauntlet_begin")
	w("")
	w("# TODO: start the emulator, copy the estate out, pin the provider, as")
	w("# live/e2e/corpus-vpc-complete/run.sh does.")
	w("")
	for _, s := range Stages() {
		w("# %d. %s: %s", s.Order, s.Title, firstSentence(s.Proves))
		w("gauntlet_stage %s not_run", s.ID)
		w("")
	}
	w("gauntlet_end")
	return b.String()
}
