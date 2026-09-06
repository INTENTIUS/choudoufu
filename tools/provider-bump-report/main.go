// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// provider-bump-report is issue #441's movement report: "a bump is a report,
// not an event". It reads live/survey-full.json, live/readiness.json,
// live/schema-precedence.json and live/rowgen-mismatches.json as committed
// at a git ref (-old-ref, HEAD by default) and the same four files as they
// stand on disk right now, and prints what moved: types added or removed,
// tier/status movement, the #387 schema-precedence verdicts' own
// before/after, whether any classifier mismatch became unruled, and whether
// internal/live/check's golden identity table moved.
//
// It is the last step of `just provider-bump <version>` (see that recipe in
// the justfile), run after survey-gen, readiness-gen and row-gen's
// -mismatches and -schema-precedence modes have already regenerated the
// four artifacts at the new release - this tool reads them, it does not run
// them:
//
//	go run ./tools/provider-bump-report
//
// Run alone, against an unchanged tree (nothing regenerated since -old-ref),
// it reports zero movement - which is exactly what proves the pipeline
// itself works before ever pointing it at a real newer release: `just
// provider-bump 6.59.0` against the pin already at 6.59.0 exercises every
// stage for real (a real provider download, a real classification pass) and
// has to report zero movement, because nothing changed.
//
// See report.go's buildReport for the report itself - the only exported
// surface this tool's own tests exercise, with no git, no filesystem and no
// subprocess.
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

// Committed artifact paths, repo-relative - the same four tools/survey-gen's,
// tools/readiness-gen's and tools/row-gen's own generators already write.
const (
	surveyFullJSONRel       = "live/survey-full.json"
	readinessJSONRel        = "live/readiness.json"
	schemaPrecedenceJSONRel = "live/schema-precedence.json"
	mismatchesJSONRel       = "live/rowgen-mismatches.json"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick every other tools/*-gen's repoRoot uses.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	oldRef := flag.String("old-ref", "HEAD", "git ref to read the BEFORE artifacts from; the AFTER artifacts are always read off disk")
	skipGolden := flag.Bool("skip-golden-test", false, "skip `go test ./internal/live/check/ -run TestIdentityGolden` for the golden-diff section (that section then reads \"not run\")")
	flag.Parse()

	if err := run(*oldRef, *skipGolden); err != nil {
		fmt.Fprintf(os.Stderr, "provider-bump-report: %v\n", err)
		os.Exit(1)
	}
}

func run(oldRef string, skipGolden bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	old, err := loadArtifactsAtRef(root, oldRef)
	if err != nil {
		return fmt.Errorf("reading the BEFORE artifacts at %s: %w", oldRef, err)
	}
	newArtifacts, err := loadArtifactsFromDisk(root)
	if err != nil {
		return fmt.Errorf("reading the AFTER artifacts from disk: %w", err)
	}

	var golden goldenResult
	if !skipGolden {
		golden = runGoldenTest(root)
	}

	fmt.Print(buildReport(old, newArtifacts, golden))
	return nil
}

// loadArtifactsAtRef reads the three committed artifacts as they stood at
// ref, via `git show`.
func loadArtifactsAtRef(root, ref string) (bumpArtifacts, error) {
	var out bumpArtifacts

	surveyData, err := gitShow(root, ref, surveyFullJSONRel)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(surveyData, &out.Survey); err != nil {
		return out, fmt.Errorf("decoding %s@%s: %w", surveyFullJSONRel, ref, err)
	}

	readinessData, err := gitShow(root, ref, readinessJSONRel)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(readinessData, &out.Readiness); err != nil {
		return out, fmt.Errorf("decoding %s@%s: %w", readinessJSONRel, ref, err)
	}

	spData, err := gitShow(root, ref, schemaPrecedenceJSONRel)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(spData, &out.SchemaPrecedence); err != nil {
		return out, fmt.Errorf("decoding %s@%s: %w", schemaPrecedenceJSONRel, ref, err)
	}

	misData, err := gitShow(root, ref, mismatchesJSONRel)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(misData, &out.Mismatches); err != nil {
		return out, fmt.Errorf("decoding %s@%s: %w", mismatchesJSONRel, ref, err)
	}
	return out, nil
}

// loadArtifactsFromDisk reads the four committed artifacts as they stand in
// the working tree right now - the shape they are in immediately after
// survey-gen, readiness-gen and row-gen's two measuring modes have
// regenerated them.
func loadArtifactsFromDisk(root string) (bumpArtifacts, error) {
	var out bumpArtifacts

	surveyData, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(surveyFullJSONRel))) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(surveyData, &out.Survey); err != nil {
		return out, fmt.Errorf("decoding %s: %w", surveyFullJSONRel, err)
	}

	readinessData, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(readinessJSONRel))) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(readinessData, &out.Readiness); err != nil {
		return out, fmt.Errorf("decoding %s: %w", readinessJSONRel, err)
	}

	spData, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(schemaPrecedenceJSONRel))) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(spData, &out.SchemaPrecedence); err != nil {
		return out, fmt.Errorf("decoding %s: %w", schemaPrecedenceJSONRel, err)
	}

	misData, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(mismatchesJSONRel))) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(misData, &out.Mismatches); err != nil {
		return out, fmt.Errorf("decoding %s: %w", mismatchesJSONRel, err)
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

// runGoldenTest runs internal/live/check's TestIdentityGolden against the
// working tree and reports whether it passed - see report.go's buildReport
// for what this section means for a plain version bump.
func runGoldenTest(root string) goldenResult {
	cmd := exec.Command("go", "test", "./internal/live/check/", "-run", "^TestIdentityGolden$", "-count=1", "-v")
	cmd.Dir = root
	cmd.Env = envWithoutPWD()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return goldenResult{Ran: true, Passed: err == nil, Output: out.String()}
}

// envWithoutPWD is os.Environ() with PWD stripped, the same discipline
// HANDOFF.md's "env -u PWD on every go command" applies to every go
// invocation this checkout makes: this process may itself have been
// launched under a shell that set PWD over a symlinked checkout path, and
// the git/go subprocesses spawned here should resolve their own working
// directory from getwd(2) rather than trust an inherited, possibly-stale
// value.
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
