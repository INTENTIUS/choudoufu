// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EngineVersionPin is the engine's upstream base, repo-relative (#1778
// ruling 6): the OpenTofu version/VERSION the choudoufu binary embeds. It is
// the same file live/fork-surface.json's base_opentofu_version is derived
// from, so the board and the fork-surface page name one base.
const EngineVersionPin = "version/VERSION"

// LegacyEngineBase is the base every row written before LastRun recorded
// UpstreamVersion was measured on. Until #1778 every choudoufu tree was
// built on upstream main 03743ce6e8, whose version/VERSION reads exactly
// this, so a row with no recorded base is known to have been measured on it
// - but only by this constant, not by the row. IsEngineStale uses it to
// decide how such a row reads.
const LegacyEngineBase = "1.13.0-dev"

// engineVersion reads EngineVersionPin - the CONFIGURATION half of #1778
// ruling 6, a.UpstreamVersion's value on every Rebuild. A missing file reads
// as "", the same graceful-empty behaviour oracleVersions and emulatorPin
// have.
func engineVersion(root string) string {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(EngineVersionPin)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// IsEngineStale reports whether r was measured on a different OpenTofu
// engine base than current, the engine's counterpart to IsStale (emulator)
// and IsProviderStale (provider pin).
//
// Three cases:
//
//   - no last_run: not stale, the same carve-out IsStale makes.
//   - a recorded base: stale when it differs from current. A recorded base
//     against an unreadable pin ("") compares unequal and reads stale, as
//     IsStale's own empty comparison does.
//   - no recorded base (every row written before this field existed): the
//     row was measured on LegacyEngineBase, so it is UNKNOWN while that is
//     still the base and stale once the base has moved. An unreadable pin
//     is no evidence the base moved, so it reads unknown too.
func IsEngineStale(r EstateResult, current string) bool {
	if r.LastRun == nil {
		return false
	}
	if r.LastRun.UpstreamVersion == "" {
		return current != "" && current != LegacyEngineBase
	}
	return r.LastRun.UpstreamVersion != current
}

// engineVersionProbe is overridden in tests so RunEstates never builds a
// binary under `go test`.
var engineVersionProbe = probeEngine

// probeEngine is the EVIDENCE half of #1778 ruling 6: the OpenTofu base of
// the choudoufu binary this run's scripts use, asked of that binary through
// `version -json`. Called once per RunEstates call, like probeOracle.
//
// Which binary: TOFU_BIN when the run sets it (opts.Env first, then the
// environment - the same precedence the script sees), which is how CI runs
// every estate (.github/workflows/gauntlet.yml builds one and passes it).
// Without TOFU_BIN every script builds ./cmd/choudoufu from root itself, so
// this builds the same package from the same tree and asks that. "" when
// neither yields an answer: nothing is recorded rather than the pin being
// copied in, so the row reads by IsEngineStale's missing-field rule.
func probeEngine(root string, env []string) string {
	bin := lookupEnv(env, "TOFU_BIN")
	if bin == "" {
		if _, err := os.Stat(filepath.Join(root, "cmd", "choudoufu")); err != nil {
			return ""
		}
		dir, err := os.MkdirTemp("", "gauntlet-engine-")
		if err != nil {
			return ""
		}
		defer os.RemoveAll(dir)
		bin = filepath.Join(dir, "choudoufu")
		build := exec.Command("go", "build", "-o", bin, "./cmd/choudoufu")
		build.Dir = root
		// env -u PWD: CLAUDE.md's symlinked-checkout trap, the same
		// spelling every script's own `go build` uses.
		build.Env = withoutEnv(os.Environ(), "PWD")
		if err := build.Run(); err != nil {
			return ""
		}
	}
	return reconcileEngineVersion(binaryEngineVersion(bin), engineVersion(root))
}

// binaryEngineVersion runs `<bin> version -json` and returns its
// terraform_version: choudoufu prints its upstream base under stock's key
// (internal/command/views/version.go), beside choudoufu_version.
func binaryEngineVersion(bin string) string {
	out, err := exec.Command(bin, "version", "-json").Output()
	if err != nil {
		return ""
	}
	var v struct {
		Version string `json:"terraform_version"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return ""
	}
	return v.Version
}

// reconcileEngineVersion folds what the binary reported with the tree's
// pin. A development build prints "-dev" whatever version/VERSION says
// (version/version.go sets Prerelease to "dev" unless linked with dev=no,
// and nothing in the gauntlet links that), so after the upgrade a dev build
// of a 1.13.0 tree prints 1.13.0-dev - the string the pre-upgrade engine
// printed. Recorded as is, every re-measured row would read stale against
// the 1.13.0 pin forever, and a pin taken in dev form would make the
// upgrade invisible. So the binary is believed for the core version, and
// when it is a dev build of the tree's own core the tree settles the
// prerelease the dev flag erased. A binary from another core is recorded
// exactly as it reported.
//
// The one case this cannot see: a TOFU_BIN built from a different tree
// with the same core (a pre-upgrade 1.13.0-dev binary run against a 1.13.0
// tree) records the tree's base. CI builds TOFU_BIN from the checkout it
// runs, so that case needs a hand-set TOFU_BIN pointing at an old binary.
func reconcileEngineVersion(reported, tree string) string {
	if reported == "" || tree == "" {
		return reported
	}
	core, isDev := strings.CutSuffix(reported, "-dev")
	if isDev && core == engineCore(tree) {
		return tree
	}
	return reported
}

// engineCore is v without any prerelease.
func engineCore(v string) string {
	core, _, _ := strings.Cut(v, "-")
	return core
}

// lookupEnv returns key's last value in env, else the process environment.
func lookupEnv(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return os.Getenv(key)
}

// withoutEnv is env with every key= entry removed.
func withoutEnv(env []string, key string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// engineNote is the engine-provenance sentence (#1778 ruling 6), beside
// oracleNote and providerNote on the estate page: silent for a row with no
// gauntlet run, and otherwise the base the run used, with **Stale** when
// IsEngineStale says so. A row that predates the field says so in words
// rather than leaving the reader to infer it from silence.
func engineNote(r EstateResult, a *Artifact) string {
	if r.LastRun == nil || r.Protocol != ProtocolGauntlet {
		return ""
	}
	got := r.LastRun.UpstreamVersion
	switch {
	case got == "" && !IsEngineStale(r, a.UpstreamVersion):
		return fmt.Sprintf("Engine: OpenTofu base not recorded (the run predates `last_run.upstream_version`); every such run was on `%s`, the current base.", LegacyEngineBase)
	case got == "":
		return fmt.Sprintf("Engine: OpenTofu base not recorded (the run predates `last_run.upstream_version`, so it was on `%s`). **Stale**: the current base is `%s`.", LegacyEngineBase, a.UpstreamVersion)
	case !IsEngineStale(r, a.UpstreamVersion):
		return fmt.Sprintf("Engine: OpenTofu base `%s` (matches the current base).", got)
	}
	return fmt.Sprintf("Engine: OpenTofu base `%s`. **Stale**: the current base is `%s`.", got, a.UpstreamVersion)
}

// engineBanner is the board-wide engine sentence (#1778 ruling 6), beside
// boardBanner (emulator) and providerBanner. It always names the current
// base, as the emulator banner always names the current pin, and counts the
// rows IsEngineStale marks, so the count is the same rule `gauntlet next`
// enqueues stale_pin units by. Rows that predate last_run.upstream_version
// are counted separately while they read unknown, so the board never claims
// a row recorded a base it did not record.
func engineBanner(a *Artifact) string {
	measured, stale, unknown := 0, 0, 0
	for _, r := range a.Estates {
		if r.LastRun == nil {
			continue
		}
		measured++
		switch {
		case IsEngineStale(r, a.UpstreamVersion):
			stale++
		case r.LastRun.UpstreamVersion == "":
			unknown++
		}
	}
	if a.UpstreamVersion == "" {
		return ""
	}
	if measured == 0 {
		return fmt.Sprintf("The engine is built on OpenTofu `%s` (`%s`, configuration for the next run); no estate has recorded a run yet.", a.UpstreamVersion, EngineVersionPin)
	}
	current := measured - stale - unknown
	parts := []string{fmt.Sprintf("%d recorded running on it", current)}
	if unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d predate `last_run.upstream_version` and are read as measured on `%s`, which is still the base", unknown, LegacyEngineBase))
	}
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("%d are engine-stale: measured on a different base, or predating `last_run.upstream_version` and so measured on `%s` - stale evidence, not a failure, and `go run ./tools/gauntlet next` surfaces it as work", stale, LegacyEngineBase))
	}
	return fmt.Sprintf("The engine is built on OpenTofu `%s` (`%s`). Of the %d estates measured so far, %s.", a.UpstreamVersion, EngineVersionPin, measured, strings.Join(parts, "; "))
}
