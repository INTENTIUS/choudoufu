// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// moduleFixture is one committed fixture this generator writes and a
// floci-driven test applies (INTENTIUS/choudoufu#2268): the directory under
// the repository root, and the arguments GENERATED.md records for it.
type moduleFixture struct {
	dir    string
	cohort string
	types  []string
	keys   []string
}

// moduleFixtures are the two module fixtures. Every other cohort is rendered
// into a scratch directory and never committed (issue #699).
var moduleFixtures = []moduleFixture{
	{dir: "live/e2e/estate-module", cohort: "ec2-module", types: []string{"aws_eip"}},
	{dir: "live/e2e/estate-module-keyed", cohort: "ec2-module-keyed", types: []string{"aws_eip"}, keys: []string{"a", "b"}},
}

func (f moduleFixture) command() string {
	cmd := "go run ./tools/estate-gen -cohort " + f.cohort + " -types " + strings.Join(f.types, ",") + " -module-wrap"
	if len(f.keys) > 0 {
		cmd += " -module-keys=" + strings.Join(f.keys, ",")
	}
	return cmd + " -live-block -out <dir>"
}

// generatorOwned lists dir's files the generator owns, relative and
// slash-separated: everything but README.md, which is hand-owned.
func generatorOwned(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "README.md" || strings.HasPrefix(filepath.Base(rel), ".") {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// TestModuleFixturesRecordTheGeneratorsPin needs no provider: each module
// fixture's versions.tf pins this generator's provider release and names
// its estate in a live block, and its GENERATED.md records the command
// TestModuleFixturesMatchTheGenerator runs. A providerVersion bump without
// a regeneration fails here, in the ordinary test tier.
func TestModuleFixturesRecordTheGeneratorsPin(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range moduleFixtures {
		t.Run(f.cohort, func(t *testing.T) {
			dir := filepath.Join(root, filepath.FromSlash(f.dir))
			versions, err := os.ReadFile(filepath.Join(dir, "versions.tf")) //nolint:gosec // fixed fixture paths
			if err != nil {
				t.Fatal(err)
			}
			if want := fmt.Sprintf("version = \"= %s\"", providerVersion); !strings.Contains(string(versions), want) {
				t.Errorf("%s/versions.tf does not pin %s (%s); regenerate it with %s", f.dir, providerVersion, want, f.command())
			}
			if want := fmt.Sprintf("estate = %q", estateTag(f.cohort)); !strings.Contains(string(versions), want) {
				t.Errorf("%s/versions.tf has no live block with %s", f.dir, want)
			}
			generated, err := os.ReadFile(filepath.Join(dir, "GENERATED.md")) //nolint:gosec // fixed fixture paths
			if err != nil {
				t.Fatalf("%s has no GENERATED.md: %v", f.dir, err)
			}
			if !strings.Contains(string(generated), f.command()) {
				t.Errorf("%s/GENERATED.md does not record %q", f.dir, f.command())
			}
		})
	}
}

// TestModuleFixturesMatchTheGenerator (INTENTIUS/choudoufu#2268) renders
// each module fixture with the command its GENERATED.md records and
// requires the committed directory to be that output: the same
// generator-owned files, byte for byte. README.md is hand-owned and left
// out. Gated, since it reads the provider's schema through the pinned
// release:
//
//	TF_FLOCI_TEST=1 go test ./tools/estate-gen -run TestModuleFixturesMatchTheGenerator -v
func TestModuleFixturesMatchTheGenerator(t *testing.T) {
	flocitest.Gate(t, "estate-gen module fixtures")
	flocitest.RequireBinary(t, defaultInitBin)
	if _, err := exec.LookPath(defaultFmtBin); err != nil {
		t.Skipf("%s is not on PATH; the committed fixtures are %s fmt's output", defaultFmtBin, defaultFmtBin)
	}
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring provider schemas: %v", err)
	}
	for _, f := range moduleFixtures {
		t.Run(f.cohort, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), f.cohort)
			g, err := planCohort(f.cohort, schemas, f.types)
			if err != nil {
				t.Fatalf("planCohort(%s): %v", f.cohort, err)
			}
			g.liveBlock = true
			if err := writeCohort(out, f.cohort, f.types, g, true, f.keys); err != nil {
				t.Fatalf("writeCohort: %v", err)
			}
			if err := formatWithBinary(defaultFmtBin, out, runCombined); err != nil {
				t.Fatalf("formatting %s: %v", out, err)
			}

			committed := filepath.Join(root, filepath.FromSlash(f.dir))
			got, want := generatorOwned(t, out), generatorOwned(t, committed)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s holds %v; the generator writes %v. Regenerate it with %s", f.dir, want, got, f.command())
			}
			for _, rel := range got {
				a, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel))) //nolint:gosec // test-generated paths
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(committed, filepath.FromSlash(rel))) //nolint:gosec // fixed fixture paths
				if err != nil {
					continue // reported by the file-list comparison above
				}
				if string(a) != string(b) {
					t.Errorf("%s/%s is not the generator's output. Regenerate it with %s\n--- committed\n%s\n--- generated\n%s", f.dir, rel, f.command(), b, a)
				}
			}
		})
	}
}
