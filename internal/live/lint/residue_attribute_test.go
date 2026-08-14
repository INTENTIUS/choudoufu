// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// residueTestSchemas is a hand-built stand-in for the provider schemas the
// live entry points hand [CheckResidueAttributes] at runtime, shaped after
// the real hashicorp/aws 6.59.0 flags issue #126's wo-sweep measured:
// aws_ssm_parameter.value_wo is WriteOnly, aws_db_instance.password is
// sensitive and settable, and aws_s3_object.content carries no schema
// signal whatsoever - which is the founding example's whole problem.
func residueTestSchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		"aws_ssm_parameter": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"name":             {Type: cty.String, Required: true},
				"type":             {Type: cty.String, Required: true},
				"value_wo":         {Type: cty.String, Optional: true, WriteOnly: true},
				"value_wo_version": {Type: cty.Number, Optional: true},
			},
		}},
		"aws_db_instance": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"identifier": {Type: cty.String, Optional: true},
				"password":   {Type: cty.String, Optional: true, Sensitive: true},
			},
		}},
		"aws_s3_object": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"bucket":  {Type: cty.String, Required: true},
				"key":     {Type: cty.String, Required: true},
				"content": {Type: cty.String, Optional: true},
			},
		}},
		// The nested-block shape: aws_fsx_windows_file_system's
		// self_managed_active_directory block carries password_wo one
		// level down.
		"aws_fsx_windows_file_system": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"storage_capacity": {Type: cty.Number, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"self_managed_active_directory": {
					Nesting: configschema.NestingSingle,
					Block: configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"domain_name": {Type: cty.String, Required: true},
							"password_wo": {Type: cty.String, Optional: true, WriteOnly: true},
						},
					},
				},
			},
		}},
	}
}

// residueConfig loads a one-file configuration from source, the same way
// TestIgnoreChangesSkipsResourcesWithNoMarkers builds its fixture.
func residueDiags(t *testing.T, src string) tfdiags.Diagnostics {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing the fixture: %s", err)
	}
	return CheckResidueAttributes(loadConfigDir(t, dir), residueTestSchemas())
}

func TestResidueAttributesWarnsOnWriteOnly(t *testing.T) {
	diags := residueDiags(t, `
resource "aws_ssm_parameter" "secret" {
  name     = "/app/secret"
  type     = "String"
  value_wo = "hunter2"
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(diags), diags)
	}
	diag := diags[0]
	if diag.Severity() != tfdiags.Warning {
		t.Errorf("severity = %v, want Warning; #126 ruled this a warning, never a refusal", diag.Severity())
	}
	desc := diag.Description()
	if want := "Attribute value cannot round-trip a stateless replan"; desc.Summary != want {
		t.Errorf("summary = %q, want %q", desc.Summary, want)
	}
	for _, substr := range []string{
		"aws_ssm_parameter.secret",
		`"value_wo"`,
		"write-only",
		"terraform import",
		`live/LIMITATIONS.md, "Attribute-level residue"`,
	} {
		if !strings.Contains(desc.Detail, substr) {
			t.Errorf("detail = %q, want it to contain %q", desc.Detail, substr)
		}
	}
	if diag.Source().Subject == nil {
		t.Error("warning has no source subject; it must point at the attribute")
	}
}

func TestResidueAttributesWarnsOnSensitiveSettable(t *testing.T) {
	diags := residueDiags(t, `
resource "aws_db_instance" "db" {
  identifier = "app-db"
  password   = "placeholder"
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(diags), diags)
	}
	desc := diags[0].Description()
	for _, substr := range []string{"aws_db_instance.db", `"password"`, "sensitive", "no-secrets"} {
		if !strings.Contains(desc.Detail, substr) {
			t.Errorf("detail = %q, want it to contain %q", desc.Detail, substr)
		}
	}
}

func TestResidueAttributesWarnsInsideNestedBlock(t *testing.T) {
	diags := residueDiags(t, `
resource "aws_fsx_windows_file_system" "fs" {
  storage_capacity = 32

  self_managed_active_directory {
    domain_name = "corp.example.com"
    password_wo = "hunter2"
  }
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(diags), diags)
	}
	desc := diags[0].Description()
	if want := `"self_managed_active_directory.password_wo"`; !strings.Contains(desc.Detail, want) {
		t.Errorf("detail = %q, want the full attribute path %s", desc.Detail, want)
	}
}

// TestResidueAttributesSilentWhenNothingFlaggedIsSet pins both silences: a
// resource of a flagged type that sets only ordinary arguments (including
// value_wo_version, the plain bookkeeping companion the schema does not
// flag), and a resource whose type has no schema here at all.
func TestResidueAttributesSilentWhenNothingFlaggedIsSet(t *testing.T) {
	diags := residueDiags(t, `
resource "aws_ssm_parameter" "plain" {
  name             = "/app/plain"
  type             = "String"
  value_wo_version = 1
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
`)
	if len(diags) != 0 {
		t.Errorf("expected no warnings, got %d: %v", len(diags), diags)
	}
}

// TestResidueAttributesCannotSeeS3ObjectContent documents the blind spot
// rather than papering over it: aws_s3_object.content is the founding
// example of issue #126 - the provider's Read never fetches an object body,
// so a stateless replan re-proposes `content` forever - and it produces NO
// warning here, asserted on purpose. Its schema reads
// optional/not-sensitive/not-write-only, indistinguishable from any
// ordinary argument; the unreadability is provider behavior the schema
// carries no trace of, so no schema-derived check can catch it. The
// LIMITATIONS entry names it by name instead. If this test ever fails with
// a warning, the schema started carrying a signal and the entry should be
// rewritten, not the assertion relaxed to keep a stale claim.
func TestResidueAttributesCannotSeeS3ObjectContent(t *testing.T) {
	diags := residueDiags(t, `
resource "aws_s3_object" "doc" {
  bucket  = "app-bucket"
  key     = "doc.txt"
  content = "hello"
}
`)
	if len(diags) != 0 {
		t.Errorf("expected no warnings for aws_s3_object.content (schema-invisible, documented in LIMITATIONS), got %d: %v", len(diags), diags)
	}
}

func TestResidueAttributesNilConfigAndNoSchemas(t *testing.T) {
	if diags := CheckResidueAttributes(nil, residueTestSchemas()); len(diags) != 0 {
		t.Errorf("expected no warnings for a nil config, got %d", len(diags))
	}
	if diags := CheckResidueAttributes(loadConfigDir(t, "testdata/clean"), nil); len(diags) != 0 {
		t.Errorf("expected no warnings with no schemas to consult, got %d", len(diags))
	}
}
