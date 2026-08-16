// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// analyzeDir loads and analyzes a temporary configuration the way the
// live-check command does, attribution included.
func analyzeDir(t *testing.T, files map[string]string) Report {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	load := Load(context.Background(), dir)
	if load.Config == nil {
		t.Fatalf("configuration did not load: %v", load.Diags)
	}
	report := Analyze(context.Background(), load.Config, Context{})
	report.Load = load
	report.AttributeUnsetVariables(load.UnsetVariables(), load.Sources())
	return report
}

func findingByTitle(r Report, title string) *Finding {
	for i := range r.Findings {
		if r.Findings[i].Title == title {
			return &r.Findings[i]
		}
	}
	return nil
}

// TestUnsetVariableRefusalIsAttributed is issue #161's case, as measured in
// the issue: a bucket name interpolating a required variable refuses only
// because the variable has no value.
func TestUnsetVariableRefusalIsAttributed(t *testing.T) {
	rep := analyzeDir(t, map[string]string{
		"main.tf": `
variable "account_id" { type = string }

resource "aws_s3_bucket" "dist" {
  bucket = "acme-dist-${var.account_id}"
}
`,
	})

	requireUnset(t, rep, "account_id")

	f := findingByTitle(rep, "Non-static identity argument")
	if f == nil {
		t.Fatalf("expected a non-static identity refusal, got %d finding(s)", len(rep.Findings))
	}
	if f.UnsetVarSites != len(f.Sites) {
		t.Errorf("marked %d of %d sites; every site here reads var.account_id", f.UnsetVarSites, len(f.Sites))
	}
	if len(f.UnsetVarRefs) != 1 || f.UnsetVarRefs[0] != "account_id" {
		t.Errorf("UnsetVarRefs = %v, want [account_id]", f.UnsetVarRefs)
	}
}

// TestRefusalNotReadingAnUnsetVariableIsNotAttributed is the half that keeps
// the caveat worth reading.
//
// uuid() refuses whatever the variables are. If attribution were per-report
// rather than per-site - or if it read the whole file rather than the
// refusal's own range - this refusal would be excused by a variable it never
// touches, and the caveat would become noise attached to everything.
func TestRefusalNotReadingAnUnsetVariableIsNotAttributed(t *testing.T) {
	rep := analyzeDir(t, map[string]string{
		"main.tf": `
variable "account_id" { type = string }

resource "aws_s3_bucket" "named" {
  bucket = "acme-dist-${var.account_id}"
}

resource "aws_s3_bucket" "impure" {
  bucket = "acme-${uuid()}"
}
`,
	})

	requireUnset(t, rep, "account_id")

	impure := findingByTitle(rep, "Identity derived from an impure function")
	if impure == nil {
		t.Fatalf("expected an impure-function refusal; findings were %v", titles(rep))
	}
	if impure.UnsetVarSites != 0 {
		t.Errorf("the impure-function refusal was marked as depending on %v, but uuid() refuses "+
			"whatever the variables are. Attribution must read the refusal's own range, not the file.",
			impure.UnsetVarRefs)
	}
}

// TestAttributionIsScopedToTheRefusalsOwnRange is the specific claim the
// implementation's doc comment makes: a refusal is not excused by an unset
// variable read somewhere else in the same file.
//
// The fixture has to make the variable actually reach the unset set, and the
// first version of this test did not. It put var.account_id in a locals
// block nothing referenced; the static evaluator is lazy, so the variable
// was never read, UnsetVariables() was empty, and AttributeUnsetVariables
// returned before doing any work. The test passed against a deliberately
// broken implementation that scanned whole files. requireUnset below is what
// stops that from being possible again - a test of attribution that runs
// with nothing to attribute is not testing attribution.
func TestAttributionIsScopedToTheRefusalsOwnRange(t *testing.T) {
	rep := analyzeDir(t, map[string]string{
		"main.tf": `
variable "account_id" { type = string }

resource "aws_s3_bucket" "far_away" {
  bucket = "acme-dist-${var.account_id}"
}

resource "aws_s3_bucket" "impure" {
  bucket = "acme-${uuid()}"
}
`,
	})
	requireUnset(t, rep, "account_id")

	impure := findingByTitle(rep, "Identity derived from an impure function")
	if impure == nil {
		t.Fatalf("expected an impure-function refusal; findings were %v", titles(rep))
	}
	if impure.UnsetVarSites != 0 {
		t.Errorf("the uuid() refusal was attributed to %v, which is read by a different resource in the "+
			"same file. Attribution is scoped to the refusal's own range for exactly this reason: a "+
			"file-wide scan excuses refusals that have nothing to do with the variable.",
			impure.UnsetVarRefs)
	}
}

// requireUnset fails when a fixture does not actually produce the unset
// variable it was written to exercise. Without it these tests can pass by
// doing nothing.
func requireUnset(t *testing.T, rep Report, name string) {
	t.Helper()
	for _, got := range rep.Load.UnsetVariables() {
		if got == name {
			return
		}
	}
	t.Fatalf("fixture does not leave var.%s unset (UnsetVariables() = %v), so this test would pass "+
		"without attribution running at all", name, rep.Load.UnsetVariables())
}

// TestAttributionIsAllOrNothingPerSite checks the count, not just the flag.
// A refusal that fires in two places, one of which reads an unset variable,
// is still a real refusal in the other - and the report says "2 of these
// site(s)" on the strength of this number.
func TestAttributionCountsSitesNotFindings(t *testing.T) {
	rep := analyzeDir(t, map[string]string{
		"main.tf": `
variable "account_id" { type = string }

resource "aws_s3_bucket" "from_var" {
  bucket = "acme-${var.account_id}"
}

resource "aws_s3_bucket" "from_uuid" {
  bucket = "acme-${uuid()}"
}
`,
	})
	for _, f := range rep.Findings {
		if f.UnsetVarSites > len(f.Sites) {
			t.Errorf("%s: marked %d sites of %d", f.Title, f.UnsetVarSites, len(f.Sites))
		}
	}
}

// TestNoUnsetVariablesMarksNothing: with values supplied there is nothing to
// attribute, and the function must not invent marks.
func TestNoUnsetVariablesMarksNothing(t *testing.T) {
	rep := analyzeDir(t, map[string]string{
		"main.tf": `
variable "account_id" { type = string }

resource "aws_s3_bucket" "impure" {
  bucket = "acme-${uuid()}"
}
`,
		"terraform.tfvars": "account_id = \"123456789012\"\n",
	})
	if n := rep.AttributeUnsetVariables(rep.Load.UnsetVariables(), rep.Load.Sources()); n != 0 {
		t.Errorf("marked %d site(s) with every variable supplied", n)
	}
}

// TestAttributionFollowsModuleBoundary is #178's "unresolvable identity"
// scoping counterexample: a child module's site reads its OWN declared
// variable (var.name), whose value arrives from the parent's module call
// argument, which is itself a local, which reads the root variable that is
// actually unset. Matching the site's own text against the root unset set
// finds nothing; the walk this test exercises must find the root variable
// three hops up.
func TestAttributionFollowsModuleBoundary(t *testing.T) {
	rep := analyzeDir(t, map[string]string{
		"main.tf": `
variable "govuk_environment" { type = string }

locals {
  govuk_environment_alias = var.govuk_environment
}

locals {
  bucket_env = local.govuk_environment_alias
}

module "mobile_backend" {
  source = "./modules/backend"
  name   = local.bucket_env
}
`,
		"modules/backend/main.tf": `
variable "name" { type = string }

resource "aws_s3_bucket" "dist" {
  bucket = var.name
}
`,
	})

	requireUnset(t, rep, "govuk_environment")

	f := findingByTitle(rep, "Non-static identity argument")
	if f == nil {
		t.Fatalf("expected a non-static identity refusal on the child module's site; findings were %v", titles(rep))
	}
	if f.UnsetVarSites != len(f.Sites) {
		t.Errorf("marked %d of %d sites; the one site here (modules/backend's var.name) chains to "+
			"govuk_environment through the module call argument and two locals", f.UnsetVarSites, len(f.Sites))
	}
	if len(f.UnsetVarRefs) != 1 || f.UnsetVarRefs[0] != "govuk_environment" {
		t.Errorf("UnsetVarRefs = %v, want [govuk_environment] - the site's own text reads var.name, "+
			"a child-module variable, not the root variable this should chase to", f.UnsetVarRefs)
	}
}

func titles(r Report) []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.Title)
	}
	return out
}
