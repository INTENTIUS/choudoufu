// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// oracle-bump-report is issue #544's movement report, the same shape #441
// built for a provider bump (tools/provider-bump-report): "a bump is a
// report, not an event". It reads live/gauntlet.json as committed at a git
// ref (-old-ref, HEAD by default) and as it stands on disk right now, and
// prints what moved: the pinned stock terraform/tofu versions themselves,
// each set's clear/estate counts, every estate whose stage verdicts or
// clear flag changed, and which rows' last_run.oracle actually reflects the
// new pin versus which were not touched by this run.
//
// It is the last step of `just oracle-bump` (see that recipe in the
// justfile), run after a real `go run ./tools/gauntlet run` has
// re-measured against a hand-edited live/oracle-versions.json - this tool
// reads live/gauntlet.json, it does not regenerate it:
//
//	go run ./tools/oracle-bump-report
//
// Run alone, against an unchanged tree (nothing re-measured since -old-ref),
// it reports zero movement - the same self-test provider-bump-report's own
// doc comment describes: it proves the report itself works before ever
// pointing it at a real oracle bump, with no re-run required to prove it.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GauntletArtifactRel is live/gauntlet.json, repo-relative - the one
// artifact this report reads, both sides.
const GauntletArtifactRel = "live/gauntlet.json"

// repoRoot resolves the checkout's root from this file's own location, the
// same trick tools/provider-bump-report's repoRoot uses.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	oldRef := flag.String("old-ref", "HEAD", "git ref to read the BEFORE artifact from; the AFTER artifact is always read off disk")
	flag.Parse()

	if err := run(*oldRef); err != nil {
		fmt.Fprintf(os.Stderr, "oracle-bump-report: %v\n", err)
		os.Exit(1)
	}
}

func run(oldRef string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	old, err := loadArtifactAtRef(root, oldRef)
	if err != nil {
		return fmt.Errorf("reading the BEFORE artifact at %s: %w", oldRef, err)
	}
	newArtifact, err := loadArtifactFromDisk(root)
	if err != nil {
		return fmt.Errorf("reading the AFTER artifact from disk: %w", err)
	}

	fmt.Print(buildReport(old, newArtifact))
	return nil
}

// loadArtifactAtRef reads live/gauntlet.json as it stood at ref, via
// `git show`.
func loadArtifactAtRef(root, ref string) (gauntletArtifact, error) {
	var out gauntletArtifact
	data, err := gitShow(root, ref, GauntletArtifactRel)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decoding %s@%s: %w", GauntletArtifactRel, ref, err)
	}
	return out, nil
}

// loadArtifactFromDisk reads live/gauntlet.json as it stands in the working
// tree right now - the shape it is in immediately after a real
// `go run ./tools/gauntlet run` re-measured against a bumped
// live/oracle-versions.json.
func loadArtifactFromDisk(root string) (gauntletArtifact, error) {
	var out gauntletArtifact
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(GauntletArtifactRel))) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decoding %s: %w", GauntletArtifactRel, err)
	}
	return out, nil
}

// gitShow reads rel as it stood at ref, via `git show ref:rel` run with cwd
// root - the same way a reviewer would read the pre-change artifact without
// touching the working tree.
func gitShow(root, ref, rel string) ([]byte, error) {
	cmd := exec.Command("git", "show", ref+":"+rel)
	cmd.Dir = root
	cmd.Env = envWithoutPWD()
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w\n%s", ref, rel, err, stderr.String())
	}
	return out.Bytes(), nil
}

// envWithoutPWD is os.Environ() with PWD stripped, the same discipline
// HANDOFF.md's "env -u PWD on every go command" applies to every go
// invocation this checkout makes: this process may itself have been
// launched under a shell that set PWD over a symlinked checkout path, and
// the git subprocess spawned here should resolve its own working directory
// from getwd(2) rather than trust an inherited, possibly-stale value.
func envWithoutPWD() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "PWD=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
