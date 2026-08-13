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
//
// -module-keys (issue #59, 59c) adds to -module-wrap rather than replacing
// it: given a comma-separated list of instance keys, the module call
// carries for_each over that literal set instead of being static, the
// wrapped module gains a "key" variable, and each taggable resource's
// tofu-address becomes a template that reads var.key rather than a fixed
// literal - the ordinary way a value that must vary per module instance
// reaches a child module (see gen.go's tofuAddressLiteral). Two keys is
// the common case: 59c's sibling-stability e2e proof needs exactly two
// instances of one resource so that removing one key's config can be shown
// to propose destroying only that instance's live resource, untouched by
// the other; see live/e2e/estate-module-keyed/ for the fixture this flag
// built.
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
	count := flag.Int("count", 0, "generate N replicas of one resource type via HCL's count meta-argument instead of one resource per admitted type - a scale/benchmark estate (issue #64). Requires -types to name exactly one, schema-simple type (its identity argument the only required one, no required nested blocks, taggable).")
	initBin := flag.String("init-bin", defaultInitBin, "binary that downloads the pinned provider (terraform, tofu or choudoufu)")
	fmtBin := flag.String("fmt-bin", defaultFmtBin, "binary that formats the generated *.tf files (terraform, tofu or choudoufu)")
	moduleWrap := flag.Bool("module-wrap", false, "wrap the cohort's resources in one static module call (module \"wrapped\") instead of writing them at the estate root, to exercise issue #59's traversal")
	moduleKeys := flag.String("module-keys", "", "comma-separated instance keys; requires -module-wrap, and switches it from a static module call to a for_each over these keys (issue #59, 59c). Two keys is the common case for the sibling-stability e2e fixture.")
	flag.Parse()

	var keys []string
	if *moduleKeys != "" {
		for _, k := range strings.Split(*moduleKeys, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				keys = append(keys, k)
			}
		}
	}

	if err := run(*cohort, *types, *out, *count, *initBin, *fmtBin, *moduleWrap, keys); err != nil {
		fmt.Fprintf(os.Stderr, "estate-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(cohort, typesFlag, out string, count int, initBin, fmtBin string, moduleWrap bool, moduleKeys []string) error {
	if cohort == "" {
		return fmt.Errorf("-cohort is required")
	}
	if len(moduleKeys) > 0 && !moduleWrap {
		return fmt.Errorf("-module-keys requires -module-wrap")
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
	if count > 0 {
		if len(requested) != 1 {
			return fmt.Errorf("-count requires -types to name exactly one type, got %d: %s", len(requested), strings.Join(requested, ", "))
		}
	}
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

	if count > 0 {
		if err := writeCountedCohort(out, cohort, requested[0], count, g); err != nil {
			return err
		}
		if err := formatWithBinary(fmtBin, out, runCombined); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "estate-gen: wrote %s (%s x count=%d)\n", out, requested[0], count)
		return nil
	}

	if err := writeCohort(out, cohort, requested, g, moduleWrap, moduleKeys); err != nil {
		return err
	}

	if err := formatWithBinary(fmtBin, out, runCombined); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "estate-gen: wrote %s (%d resource(s): %d coverage, %d supporting)\n",
		out, len(g.order), countKind(g, kindCoverage), countKind(g, kindSupporting))
	return nil
}

// writeCountedCohort is writeCohort's -count sibling: a single type,
// scaled to N instances via renderCounted, written to versions.tf,
// locals.tf and "<cohort>.tf" - no supporting.tf (planCohort adds a
// supporting resource only when a required argument needs one, and
// renderCounted already refuses any type with a required argument beyond
// its own identity, so a counted run can never need one), and a short
// README.md rather than the full provenance table (there is exactly one
// provenance row to show).
func writeCountedCohort(out, cohort, typeName string, count int, g *generator) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	var addr resourceAddr
	for _, p := range g.order {
		if p.Addr.Type == typeName {
			addr = p.Addr
			break
		}
	}
	text, err := g.renderCounted(planned{Addr: addr, Kind: kindCoverage}, count)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(out, "versions.tf"), []byte(versionsTF(cohort)), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "locals.tf"), []byte(localsTF(cohort)), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
		return err
	}
	if err := os.WriteFile(filepath.Join(out, cohort+".tf"), []byte(text), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
		return err
	}
	readme := fmt.Sprintf(`# %s cohort

Generated by `+"`tools/estate-gen -count`"+` (issue #64's scale benchmark): %d
instances of `+"`%s`"+` via HCL's `+"`count`"+` meta-argument, not %d literal
resource blocks.

Regenerate with:

`+"```"+`
go run ./tools/estate-gen -cohort %s -types %s -count %d -out %s
`+"```"+`
`, cohort, count, typeName, count, cohort, typeName, count, out)
	if err := os.WriteFile(filepath.Join(out, "README.md"), []byte(readme), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
		return err
	}
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
//
// With moduleKeys also set (59c, issue #59 phase 3), the module call
// carries for_each over those keys instead of being static
// ([moduleWrapKeyedMainTF]), and the wrapped module gains a variables.tf
// declaring the "key" variable the call passes each.key through as - the
// wiring [gen.go's tofuAddressLiteral] leans on to write a tofu-address
// that reads correctly for whichever instance evaluates it.
func writeCohort(out, cohort string, requested []string, g *generator, moduleWrap bool, moduleKeys []string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	if moduleWrap {
		g.modulePrefix = "module." + wrappedModuleDir + "."
	}
	if len(moduleKeys) > 0 {
		g.moduleKeyVar = "key"
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
		mainTF := moduleWrapMainTF()
		if len(moduleKeys) > 0 {
			mainTF = moduleWrapKeyedMainTF(moduleKeys, g.moduleKeyVar)
			if err := os.WriteFile(filepath.Join(contentDir, "variables.tf"), []byte(wrappedVariablesTF(g.moduleKeyVar)), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(out, "main.tf"), []byte(mainTF), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
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
	readme := readmeMD(cohort, requested, g, moduleWrap, moduleKeys)
	if err := os.WriteFile(filepath.Join(out, "README.md"), []byte(readme), 0o644); err != nil { //nolint:gosec // a committed fixture, not a secret
		return err
	}
	return nil
}

// moduleWrapMainTF is the root main.tf a static -module-wrap run writes:
// the one module call, admitted outright by 59b's traversal since it sets
// neither count nor for_each.
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

// moduleWrapKeyedMainTF is the root main.tf a -module-wrap -module-keys run
// writes: a for_each module call over a literal set of strings, still
// admitted outright (59c, issue #59 phase 3 narrowed RuleChildModule to
// refuse only count and a non-statically-keyed for_each; a literal
// toset(...) is the most static a for_each expression can be). keyVar
// passes each.key through to the wrapped module under that name, which is
// the only way anything inside the module can ever learn its own instance's
// key - see gen.go's doc on [generator.moduleKeyVar].
func moduleWrapKeyedMainTF(keys []string, keyVar string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = fmt.Sprintf("%q", k)
	}
	return fmt.Sprintf(`# Generated by tools/estate-gen -module-wrap -module-keys (issue #59, 59c).
# Every resource this cohort declares lives in ./%s instead of here, so that
# the five walkers' traversal into a keyed module is what this estate
# exercises. The for_each is a literal set of strings, the most static a
# for_each expression can be, so RuleChildModule admits it; each.key is
# passed through as the "%s" variable, which is the only way a resource
# inside the module can learn its own instance's key (see
# %s/variables.tf).

module %q {
  source   = "./%s"
  for_each = toset([%s])
  %s      = each.key
}
`, wrappedModuleDir, keyVar, wrappedModuleDir, wrappedModuleDir, wrappedModuleDir, strings.Join(quoted, ", "), keyVar)
}

func runCombined(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...) //nolint:gosec // caller-provided binary name, the same trust boundary as tools/survey-gen's -init-bin
	return cmd.CombinedOutput()
}
