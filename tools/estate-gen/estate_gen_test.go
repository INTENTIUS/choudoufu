// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// testLogWriter adapts *testing.T to io.Writer, the same small helper
// tools/survey-gen's own tests use for acquireSchemas's log parameter.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

var _ io.Writer = testLogWriter{}

// lambdaTypes is the registry-ratified Lambda roster, the same list
// live/e2e/estates/lambda/lambda.tf carries by hand - the first batch's
// five types (see internal/live/lint/admission.go's "Registry-ratified ...
// first batch, Lambda" section) plus aws_lambda_permission, ratified with
// the 2026-08-15 reversal batch (#175).
var lambdaTypes = []string{
	"aws_lambda_capacity_provider",
	"aws_lambda_code_signing_config",
	"aws_lambda_event_source_mapping",
	"aws_lambda_function",
	// The markerless-veto two-source exception (issue #274): CloudFormation
	// and the provider's own import docs, read independently of row-gen's
	// classifier bucket, agree this type's identity (function_name +
	// qualifier) is argument-built rather than server-minted. Joins the
	// lambda cohort by the same defaultCohortTypes rule every other member
	// does.
	"aws_lambda_function_event_invoke_config",
	// aws_lambda_layer_version was here until the markerless retraction
	// (#249). It is server-assigned and carries no tags argument, so every
	// instance would need marker discovery to be found again and there is
	// nowhere to write the marker; tools/row-gen's -emit no longer emits a
	// row for it, and defaultCohortTypes reads the admission table, so it
	// leaves this list by the same derivation that put the rest here.
	//
	// Admitted by wall/rejected3's parity batch, which verified 65 rejected
	// types against the v6.59.0 doc cache and admitted 28. This type joins the
	// lambda cohort by the same defaultCohortTypes rule every other member
	// does - the list is derived from the admission table, so admitting a
	// aws_lambda_* type necessarily grows it.
	"aws_lambda_layer_version_permission",
	"aws_lambda_permission",
}

// s3Types is the second cohort this generator was run against for issue
// #56's verification bar: aws_s3_bucket and its four fold-child
// configuration types plus its bucket policy, every one of them already in
// internal/live/lint's admittedTypesV0 and internal/live/identity's
// DefaultTable by way of live/e2e/estate/storage.tf - no new admission, a
// second demonstration of the generator over an unrelated already-admitted
// family with real cross-resource references (every child's "bucket"
// argument points at aws_s3_bucket.app).
var s3Types = []string{
	"aws_s3_bucket",
	"aws_s3_bucket_lifecycle_configuration",
	"aws_s3_bucket_policy",
	"aws_s3_bucket_public_access_block",
	"aws_s3_bucket_server_side_encryption_configuration",
	"aws_s3_bucket_versioning",
}

// TestDefaultCohortTypesLambda pins defaultCohortTypes's derivation (no
// network: it only reads live/mapping.json and the identity table) against
// the one cohort issue #56's own admission batch actually ratified.
func TestDefaultCohortTypesLambda(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	got, err := defaultCohortTypes(root, "lambda")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, lambdaTypes) {
		t.Errorf("defaultCohortTypes(lambda) = %v, want %v", got, lambdaTypes)
	}
}

// TestDefaultCohortTypesUnknown makes sure an unratified/unknown cohort
// name fails loudly rather than silently generating an empty directory.
func TestDefaultCohortTypesUnknown(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := defaultCohortTypes(root, "no-such-cohort"); err == nil {
		t.Error("defaultCohortTypes(no-such-cohort) succeeded, want an error naming -types explicit")
	}
}

// generateCohort is the test-only equivalent of run(), returning the
// generator so callers can inspect g.order without re-parsing the files it
// wrote.
func generateCohort(t *testing.T, cohort string, types []string, out string) *generator {
	t.Helper()
	return generateCohortWith(t, cohort, types, out, false, nil)
}

// generateCohortWith is [generateCohort] with the -module-wrap and
// -module-keys switches, for the tests that need the wrapped (static or
// keyed, 59c) shape.
func generateCohortWith(t *testing.T, cohort string, types []string, out string, moduleWrap bool, moduleKeys []string) *generator {
	t.Helper()
	flocitest.Gate(t, "estate-gen schema acquisition")
	flocitest.RequireBinary(t, defaultInitBin)

	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring provider schemas: %v", err)
	}
	g, err := planCohort(cohort, schemas, types)
	if err != nil {
		t.Fatalf("planCohort(%s): %v", cohort, err)
	}
	if err := writeCohort(out, cohort, types, g, moduleWrap, moduleKeys); err != nil {
		t.Fatalf("writeCohort: %v", err)
	}
	if _, err := exec.LookPath(defaultFmtBin); err == nil {
		if err := formatWithBinary(defaultFmtBin, out, runCombined); err != nil {
			t.Fatalf("formatting %s: %v", out, err)
		}
	}
	return g
}

// TestDeterminism regenerates the same cohort twice into separate
// directories and requires byte-identical output: no timestamps, no map-
// order nondeterminism - the verification bar issue #56 sets explicitly.
func TestDeterminism(t *testing.T) {
	flocitest.Gate(t, "estate-gen determinism")
	flocitest.RequireBinary(t, defaultInitBin)

	out1 := filepath.Join(t.TempDir(), "run1")
	out2 := filepath.Join(t.TempDir(), "run2")
	generateCohort(t, "lambda", lambdaTypes, out1)
	generateCohort(t, "lambda", lambdaTypes, out2)

	names, err := dirFiles(out1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		a, err := os.ReadFile(filepath.Join(out1, name)) //nolint:gosec // fixed test-generated paths
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(out2, name)) //nolint:gosec // fixed test-generated paths
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs between two regenerations of the same cohort", name)
		}
	}
}

// TestValidateGeneratedCohorts is the issue's headline verification bar: a
// regenerated lambda cohort and a second cohort of already-admitted types
// (s3) both `terraform validate` clean against the pinned provider release.
//
// The third case, "s3-module-wrap", is 59b's own bar for the -module-wrap
// flag: the same s3 types, generated wrapped, validate clean too - proving
// the generated module call and the child module's own files are legal
// HCL together, independent of whether stateless mode can plan them (that
// is live/e2e/estate-module/'s job, gated on floci). The fourth,
// "s3-module-keyed", is 59c's own bar for -module-keys: the same shape,
// keyed over two instances, validating clean with the wrapped module's
// variables.tf and the root's for_each module call both present.
func TestValidateGeneratedCohorts(t *testing.T) {
	flocitest.Gate(t, "estate-gen terraform validate")
	flocitest.RequireBinary(t, defaultInitBin)

	for _, tc := range []struct {
		cohort     string
		types      []string
		moduleWrap bool
		moduleKeys []string
	}{
		{cohort: "lambda", types: lambdaTypes},
		{cohort: "s3", types: s3Types},
		{cohort: "s3-module-wrap", types: s3Types, moduleWrap: true},
		{cohort: "s3-module-keyed", types: s3Types, moduleWrap: true, moduleKeys: []string{"a", "b"}},
	} {
		t.Run(tc.cohort, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), tc.cohort)
			generateCohortWith(t, tc.cohort, tc.types, out, tc.moduleWrap, tc.moduleKeys)

			run := func(args ...string) {
				t.Helper()
				cmd := exec.Command(defaultInitBin, args...) //nolint:gosec // fixed binary name, test-only
				cmd.Dir = out
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s %s: %v\n%s", defaultInitBin, strings.Join(args, " "), err, out)
				}
			}
			run("init", "-backend=false", "-input=false", "-no-color")
			run("validate", "-no-color")
		})
	}
}

// TestCloseToHandWrittenLambda retired with issue #699.
//
// It regenerated the lambda cohort and diffed it against
// live/e2e/estates/lambda, "proving closeness without replacing the
// hand-written cohort (issue #56's explicit non-goal)". That non-goal had
// already lapsed: issue #294 regenerated the lambda cohort in place, iam.tf
// - the hand-written cohort's own file, which the test still looked for -
// went with it, and the drift check confirmed on 2026-09-06 that the
// committed tree was byte-identical to the generator's own output. The test
// was comparing the generator against itself. The committed tree is gone now
// as well, so there is nothing left to be close to.
//
// What it was really guarding - that a regeneration is deterministic and
// yields the addresses the roster asks for - is
// TestGeneratedCohortsMatchTheRecordedRoster (drift_test.go) and
// TestDeterministic above.

// dirFiles lists the regular files directly inside dir, sorted.
func dirFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// resourceAddrsInDir parses every *.tf file directly inside dir and returns
// every "resource TYPE LABEL" block's address (TYPE.LABEL), sorted. A thin,
// schema-free HCL scan - just enough to compare two directories' resource
// sets without pulling in the full internal/configs loader tools/estate-gen
// otherwise has no reason to depend on.
func resourceAddrsInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // caller-controlled test/fixture paths
		if err != nil {
			return nil, err
		}
		f, diags := hclwrite.ParseConfig(data, e.Name(), hcl.InitialPos)
		if diags.HasErrors() {
			return nil, diags
		}
		for _, blk := range f.Body().Blocks() {
			if blk.Type() != "resource" {
				continue
			}
			labels := blk.Labels()
			if len(labels) != 2 {
				continue
			}
			out = append(out, labels[0]+"."+labels[1])
		}
	}
	sort.Strings(out)
	return out, nil
}
