// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// estate-gen generates live/e2e/estates/<cohort>/, the minimal-HCL
// per-cohort verification estate internal/live/flocitest.FixtureDirs picks
// up through the #48 union pin (issue #56).
//
// Given a cohort name and a list of admitted provider-local resource types,
// it emits one resource block per type: required arguments only, with
// deterministic placeholder values, cross-resource references where a
// required argument names another resource in the same run, estate tags
// where the type is taggable, and identity arguments named from
// internal/live/identity/table.go. Everything the wire schema alone cannot
// settle - a provider-side "one of X or Y" requirement, an enum member, a
// JSON-shaped string - is the small, explicit typeOverrides table in
// overrides.go, not a guess.
//
//	go run ./tools/estate-gen -cohort lambda -out /tmp/lambda-regen
//	go run ./tools/estate-gen -cohort s3 -out live/e2e/estates/s3
//	go run ./tools/estate-gen -cohort lambda -types aws_lambda_function,aws_lambda_layer_version -out /tmp/x
//
// With no -types, the cohort's types are read off live/mapping.json and
// internal/live/identity.AdmittedTypes (see cohort.go's defaultCohortTypes):
// every admitted type whose CFN service matches the cohort name - what
// issue #56 calls "the admission table's registry-ratified sections".
//
// -module-wrap (issue #59, 59b) puts every generated resource inside one
// static module call instead of at the estate root: the root directory
// gets versions.tf (provider wiring, unchanged) and a main.tf holding
// nothing but `module "wrapped" { source = "./wrapped" }`, and everything
// else - locals.tf, the coverage file, supporting.tf - moves into a
// "wrapped" subdirectory one level down. It exists so a generated cohort
// can exercise the five walkers' module traversal end to end, the same
// resources either way; see live/e2e/estate-module/ for the fixture this
// flag built.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	providerSource  = "hashicorp/aws"
	providerVersion = "6.58.0"

	// defaultInitBin downloads the provider for the schema read. Stock
	// terraform, the same binary tools/survey-gen defaults to and the
	// gated test tier drives.
	defaultInitBin = "terraform"

	// defaultFmtBin canonicalizes the generated HCL's formatting
	// (column-aligned "=" signs) after this tool writes it. Same binary
	// as defaultInitBin by default; a caller with only tofu or choudoufu
	// on PATH can point either flag at it.
	defaultFmtBin = "terraform"
)

func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/estate-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	cohort := flag.String("cohort", "", "cohort name (live/e2e/estates/<cohort>/); required")
	types := flag.String("types", "", "comma-separated provider-local types; empty reads the cohort's registry-ratified types off live/mapping.json and the identity table")
	out := flag.String("out", "", "output directory; empty defaults to live/e2e/estates/<cohort>")
	initBin := flag.String("init-bin", defaultInitBin, "binary that downloads the pinned provider (terraform, tofu or choudoufu)")
	fmtBin := flag.String("fmt-bin", defaultFmtBin, "binary that formats the generated *.tf files (terraform, tofu or choudoufu)")
	moduleWrap := flag.Bool("module-wrap", false, "wrap the cohort's resources in one static module call (module \"wrapped\") instead of writing them at the estate root, to exercise issue #59's traversal")
	flag.Parse()

	if err := run(*cohort, *types, *out, *initBin, *fmtBin, *moduleWrap); err != nil {
		fmt.Fprintf(os.Stderr, "estate-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(cohort, typesFlag, out, initBin, fmtBin string, moduleWrap bool) error {
	if cohort == "" {
		return fmt.Errorf("-cohort is required")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join(root, "live", "e2e", "estates", cohort)
	}

	var requested []string
	if typesFlag != "" {
		for _, t := range strings.Split(typesFlag, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				requested = append(requested, t)
			}
		}
	} else {
		requested, err = defaultCohortTypes(root, cohort)
		if err != nil {
			return err
		}
	}
	sort.Strings(requested)
	fmt.Fprintf(os.Stderr, "estate-gen: cohort %q, %d requested type(s): %s\n", cohort, len(requested), strings.Join(requested, ", "))

	workdir, err := os.MkdirTemp("", "estate-gen-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	schemas, err := acquireSchemas(initBin, workdir, os.Stderr)
	if err != nil {
		return err
	}

	g, err := planCohort(cohort, schemas, requested)
	if err != nil {
		return err
	}

	if err := writeCohort(out, cohort, requested, g, moduleWrap); err != nil {
		return err
	}

	if err := formatWithBinary(fmtBin, out, runCombined); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "estate-gen: wrote %s (%d resource(s): %d coverage, %d supporting)\n",
		out, len(g.order), countKind(g, kindCoverage), countKind(g, kindSupporting))
	return nil
}

func countKind(g *generator, k resourceKind) int {
	n := 0
	for _, p := range g.order {
		if p.Kind == k {
			n++
		}
	}
	return n
}

// wrappedModuleDir is the child directory a -module-wrap run puts every
// resource-bearing file into, and the local name of the module call the
// root's generated main.tf points at it with.
const wrappedModuleDir = "wrapped"

// writeCohort renders every planned resource and writes the cohort
// directory's files: versions.tf, locals.tf, README.md, "<cohort>.tf" for
// coverage rows and, only when this run added any, "supporting.tf".
//
// With moduleWrap, everything except versions.tf and README.md moves one
// level down into wrappedModuleDir/, and the root gains a main.tf holding
// nothing but the module call - see [moduleWrapMainTF]. Provider wiring
// stays at the root either way: a static module call with no provider
// block of its own inherits the root's default (unaliased) "aws"
// configuration, the ordinary rule, and repeating it in the child would
// only invite the two copies drifting apart.
func writeCohort(out, cohort string, requested []string, g *generator, moduleWrap bool) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	if moduleWrap {
		g.modulePrefix = "module." + wrappedModuleDir + "."
	}

	var coverage, supporting []string
	for _, p := range g.order {
		text, _ := g.render(p)
		switch p.Kind {
		case kindCoverage:
			coverage = append(coverage, text)
		case kindSupporting:
			supporting = append(supporting, text)
		}
	}

	contentDir := out
	if moduleWrap {
		contentDir = filepath.Join(out, wrappedModuleDir)
		if err := os.MkdirAll(contentDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, "main.tf"), []byte(moduleWrapMainTF()), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
			return err
		}
	}

	if err := os.WriteFile(filepath.Join(out, "versions.tf"), []byte(versionsTF(cohort)), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
		return err
	}
	if err := os.WriteFile(filepath.Join(contentDir, "locals.tf"), []byte(localsTF(cohort)), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
		return err
	}
	if len(coverage) > 0 {
		body := strings.Join(coverage, "\n")
		if err := os.WriteFile(filepath.Join(contentDir, cohort+".tf"), []byte(body), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
			return err
		}
	}
	if len(supporting) > 0 {
		body := strings.Join(supporting, "\n")
		if err := os.WriteFile(filepath.Join(contentDir, "supporting.tf"), []byte(body), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
			return err
		}
	}
	readme := readmeMD(cohort, requested, g, moduleWrap)
	if err := os.WriteFile(filepath.Join(out, "README.md"), []byte(readme), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
		return err
	}
	return nil
}

// moduleWrapMainTF is the root main.tf a -module-wrap run writes: the one
// module call, admitted outright by 59b's traversal since it sets neither
// count nor for_each.
func moduleWrapMainTF() string {
	return fmt.Sprintf(`# Generated by tools/estate-gen -module-wrap (issue #59, 59b). Every
# resource this cohort declares lives in ./%s instead of here, so that the
# five walkers' traversal into a static module is what this estate
# exercises; the module call itself carries neither count nor for_each, the
# one shape RuleChildModule admits.

module %q {
  source = "./%s"
}
`, wrappedModuleDir, wrappedModuleDir, wrappedModuleDir)
}

func runCombined(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...) //nolint:gosec // caller-provided binary name, the same trust boundary as tools/survey-gen's -init-bin
	return cmd.CombinedOutput()
}
