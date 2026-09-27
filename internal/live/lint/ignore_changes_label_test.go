// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1645, ruled 2026-09-27: refuse, same as AWS. checkIgnoreChanges
// (#103) already refuses ignore_changes over the AWS tags surface, but it
// returned at its schema check for any type that is not markers.Taggable -
// which is every Kubernetes type - so the label and manifest carriers
// (markers.LabelSurfacePath, markers.ManifestLabelPath) passed lint silently.
//
// kubernetes_config_map is a ratified DefaultTable row (so admission needs no
// schema), but the schema is what lets checkIgnoreChanges see it carries the
// label surface at all - without one this resource gets the AWS-only,
// schema-less answer, which is asserted below too.

func labelSurfaceTestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"data": {Type: cty.Map(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"name":      {Type: cty.String, Optional: true, Computed: true},
					"namespace": {Type: cty.String, Optional: true},
					"labels":    {Type: cty.Map(cty.String), Optional: true},
				}},
			},
		},
	}}
}

func manifestSurfaceTestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest": {Type: cty.DynamicPseudoType, Required: true},
			"object":   {Type: cty.DynamicPseudoType, Computed: true},
		},
	}}
}

// ignoreChangesIssues filters got down to RuleIgnoreChanges, so a test is
// not tripped up by an unrelated admission or naming issue the fixture may
// also carry.
func ignoreChangesIssues(issues []Issue) []Issue {
	var out []Issue
	for _, issue := range issues {
		if issue.Rule == RuleIgnoreChanges {
			out = append(out, issue)
		}
	}
	return out
}

func writeMainTF(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const labelSurfaceConfig = `
resource "kubernetes_config_map" "cfg" {
  metadata {
    name      = "cfg"
    namespace = "default"
  }

  lifecycle {
    ignore_changes = [%s]
  }
}
`

const labelSurfaceAllConfig = `
resource "kubernetes_config_map" "cfg" {
  metadata {
    name      = "cfg"
    namespace = "default"
  }

  lifecycle {
    ignore_changes = all
  }
}
`

func TestIgnoreChangesLabelSurfaceAll(t *testing.T) {
	dir := writeMainTF(t, labelSurfaceAllConfig)
	cfg := loadConfigDir(t, dir)
	schemas := map[string]providers.Schema{"kubernetes_config_map": labelSurfaceTestSchema()}

	got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: schemas}))
	if len(got) != 1 {
		t.Fatalf("ignore_changes = all on a label-surface type: got %d RuleIgnoreChanges issues, want 1: %v", len(got), got)
	}
}

func TestIgnoreChangesLabelSurfaceWholeMetadata(t *testing.T) {
	dir := writeMainTF(t, "resource \"kubernetes_config_map\" \"cfg\" {\n  metadata {\n    name      = \"cfg\"\n    namespace = \"default\"\n  }\n\n  lifecycle {\n    ignore_changes = [metadata]\n  }\n}\n")
	cfg := loadConfigDir(t, dir)
	schemas := map[string]providers.Schema{"kubernetes_config_map": labelSurfaceTestSchema()}

	got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: schemas}))
	if len(got) != 1 {
		t.Fatalf("ignore_changes = [metadata]: got %d RuleIgnoreChanges issues, want 1: %v", len(got), got)
	}
}

func TestIgnoreChangesLabelSurfaceWholeLabelsMap(t *testing.T) {
	dir := writeMainTF(t, "resource \"kubernetes_config_map\" \"cfg\" {\n  metadata {\n    name      = \"cfg\"\n    namespace = \"default\"\n  }\n\n  lifecycle {\n    ignore_changes = [metadata[0].labels]\n  }\n}\n")
	cfg := loadConfigDir(t, dir)
	schemas := map[string]providers.Schema{"kubernetes_config_map": labelSurfaceTestSchema()}

	got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: schemas}))
	if len(got) != 1 {
		t.Fatalf("ignore_changes = [metadata[0].labels]: got %d RuleIgnoreChanges issues, want 1: %v", len(got), got)
	}
}

func TestIgnoreChangesLabelSurfaceMarkerKey(t *testing.T) {
	dir := writeMainTF(t, `resource "kubernetes_config_map" "cfg" {
  metadata {
    name      = "cfg"
    namespace = "default"
  }

  lifecycle {
    ignore_changes = [metadata[0].labels["tofu-estate"]]
  }
}
`)
	cfg := loadConfigDir(t, dir)
	schemas := map[string]providers.Schema{"kubernetes_config_map": labelSurfaceTestSchema()}

	got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: schemas}))
	if len(got) != 1 {
		t.Fatalf(`ignore_changes = [metadata[0].labels["tofu-estate"]]: got %d RuleIgnoreChanges issues, want 1: %v`, len(got), got)
	}
}

// TestIgnoreChangesLabelSurfaceOtherKeyIsLeftAlone is the over-refusal
// guard: a label key this mode does not write is an ordinary thing to
// want to ignore, exactly as tags["Owner"] is on the AWS surface.
func TestIgnoreChangesLabelSurfaceOtherKeyIsLeftAlone(t *testing.T) {
	dir := writeMainTF(t, `resource "kubernetes_config_map" "cfg" {
  metadata {
    name      = "cfg"
    namespace = "default"
  }

  lifecycle {
    ignore_changes = [metadata[0].labels["team"]]
  }
}
`)
	cfg := loadConfigDir(t, dir)
	schemas := map[string]providers.Schema{"kubernetes_config_map": labelSurfaceTestSchema()}

	got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: schemas}))
	if len(got) != 0 {
		t.Fatalf(`ignore_changes = [metadata[0].labels["team"]] must not be refused: got %v`, got)
	}
}

// TestIgnoreChangesLabelSurfaceNeedsSchema pins the schema-less asymmetry
// this rule already has on the AWS surface (see checkIgnoreChanges' own
// doc comment): without a schema there is nothing that says this type
// carries its marker in metadata[0].labels rather than in tags, so the
// rule gives the same answer it always gave a Kubernetes type - none.
func TestIgnoreChangesLabelSurfaceNeedsSchema(t *testing.T) {
	dir := writeMainTF(t, "resource \"kubernetes_config_map\" \"cfg\" {\n  metadata {\n    name      = \"cfg\"\n    namespace = \"default\"\n  }\n\n  lifecycle {\n    ignore_changes = [metadata]\n  }\n}\n")
	cfg := loadConfigDir(t, dir)

	got := ignoreChangesIssues(CheckContext(t.Context(), cfg))
	if len(got) != 0 {
		t.Fatalf("schema-less run: got %v, want no RuleIgnoreChanges issues", got)
	}
}

func TestIgnoreChangesManifestSurfaceWholeManifest(t *testing.T) {
	dir := writeMainTF(t, `resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "smoke-crd"
    }
  }

  lifecycle {
    ignore_changes = [manifest]
  }
}
`)
	cfg := loadConfigDir(t, dir)
	schemas := map[string]providers.Schema{"kubernetes_manifest": manifestSurfaceTestSchema()}

	got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: schemas}))
	if len(got) != 1 {
		t.Fatalf("ignore_changes = [manifest]: got %d RuleIgnoreChanges issues, want 1: %v", len(got), got)
	}
}

func TestIgnoreChangesManifestSurfaceLabelsPath(t *testing.T) {
	dir := writeMainTF(t, `resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "smoke-crd"
    }
  }

  lifecycle {
    ignore_changes = [manifest.metadata.labels]
  }
}
`)
	cfg := loadConfigDir(t, dir)
	schemas := map[string]providers.Schema{"kubernetes_manifest": manifestSurfaceTestSchema()}

	got := ignoreChangesIssues(CheckWith(t.Context(), cfg, Context{Schemas: schemas}))
	if len(got) != 1 {
		t.Fatalf("ignore_changes = [manifest.metadata.labels]: got %d RuleIgnoreChanges issues, want 1: %v", len(got), got)
	}
}
