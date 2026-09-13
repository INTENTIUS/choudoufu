// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// The manifest carrier (GitHub issue #1079): a kubernetes_manifest block
// binds by the natural key written inside its manifest argument, rendered
// as the provider's own import id.
//
// Proving it red: delete the synthesizeManifestIdentity call from
// synthesizeTypeIdentity and every case below that expects a resolution
// reports unadmitted; drop the OmitIfAbsent from the namespace component
// and the cluster-scoped case is refused.

// manifestSchema is kubernetes_manifest's shape at hashicorp/kubernetes
// 3.2.1: one required dynamic manifest, one computed dynamic object, and
// the provider's own knobs beside them.
func manifestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest":        {Type: cty.DynamicPseudoType, Required: true},
			"object":          {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
			"computed_fields": {Type: cty.List(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"field_manager": {Nesting: configschema.NestingList, MaxItems: 1, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"name":            {Type: cty.String, Optional: true},
				"force_conflicts": {Type: cty.Bool, Optional: true},
			}}},
		},
	}}
}

func TestManifestShape(t *testing.T) {
	if !ManifestShape(manifestSchema().Block) {
		t.Fatal("the kubernetes_manifest shape is not recognised")
	}
	if ManifestShape(objectMetaSchema(true).Block) {
		t.Error("an object-metadata schema read as a manifest shape; the two carriers must be disjoint")
	}
	noObject := manifestSchema()
	delete(noObject.Block.Attributes, "object")
	if ManifestShape(noObject.Block) {
		t.Error("a schema with no computed object read as a manifest shape")
	}
	optional := manifestSchema()
	optional.Block.Attributes["manifest"] = &configschema.Attribute{Type: cty.DynamicPseudoType, Optional: true}
	if ManifestShape(optional.Block) {
		t.Error("a schema whose manifest is optional read as a manifest shape")
	}
	if ManifestShape(nil) {
		t.Error("a nil block read as a manifest shape")
	}
}

func TestManifestIdentityIsSynthesizedFromTheSchema(t *testing.T) {
	if _, has := LookupType("kubernetes_manifest"); has {
		t.Fatal("kubernetes_manifest gained a ratified row; this rule exists so it needs none")
	}
	ti, ok := SynthesizeTypeIdentity("kubernetes_manifest", map[string]providers.Schema{"kubernetes_manifest": manifestSchema()}, nil)
	if !ok {
		t.Fatal("SynthesizeTypeIdentity refused the manifest shape")
	}
	if ti.ImportSyntax != ManifestImportSyntax || !ti.NonAWSProvider || !ti.Synthesized {
		t.Errorf("entry = %+v", ti)
	}
	var paths []string
	for _, c := range ti.Components {
		if len(c.Path) > 0 {
			paths = append(paths, strings.Join(c.Path, "."))
		}
	}
	if got := strings.Join(paths, " "); got != "apiVersion kind metadata.namespace metadata.name" {
		t.Errorf("component paths = %q, want the natural key in import-id order", got)
	}
	if len(ti.IdentityAttrs) != 0 {
		t.Errorf("the manifest entry claims identity attributes %v; nothing flat on the resource is one", ti.IdentityAttrs)
	}
}

func resolveManifest(t *testing.T, body string) (*Result, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    kubernetes = { source = "hashicorp/kubernetes" }
  }
}
`+body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir, nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: map[string]providers.Schema{"kubernetes_manifest": manifestSchema()}})
	if diags.HasErrors() {
		return result, diags.Err().Error()
	}
	return result, ""
}

func manifestResolution(t *testing.T, result *Result, addr string) Resolution {
	t.Helper()
	for _, r := range result.All() {
		if r.Addr.String() == addr {
			return r
		}
	}
	t.Fatalf("no resolution for %s", addr)
	return Resolution{}
}

func TestManifestBindsByTheNaturalKeyInsideManifest(t *testing.T) {
	result, refusal := resolveManifest(t, `
resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "default"
      labels    = { app = "demo" }
    }
    spec = { cronSpec = "* * * * */5" }
  }
}
`)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	got := manifestResolution(t, result, "kubernetes_manifest.crontab")
	if got.Class != ClassConcrete || got.ImportID != "apiVersion=stable.example.com/v1,kind=CronTab,namespace=default,name=my-crontab" {
		t.Errorf("resolved %s, want CONCRETE apiVersion=stable.example.com/v1,kind=CronTab,namespace=default,name=my-crontab (the provider's own import id)", got.String())
	}
}

func TestManifestClusterScopedOmitsTheNamespaceSegment(t *testing.T) {
	result, refusal := resolveManifest(t, `
resource "kubernetes_manifest" "crd" {
  manifest = {
    apiVersion = "apiextensions.k8s.io/v1"
    kind       = "CustomResourceDefinition"
    metadata   = { name = "crontabs.stable.example.com" }
    spec       = { group = "stable.example.com" }
  }
}
`)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	got := manifestResolution(t, result, "kubernetes_manifest.crd")
	if got.ImportID != "apiVersion=apiextensions.k8s.io/v1,kind=CustomResourceDefinition,name=crontabs.stable.example.com" {
		t.Errorf("resolved %s, want the documented cluster-scoped form with no namespace segment", got.String())
	}
}

func TestManifestReadsThroughALocalAndAVariable(t *testing.T) {
	result, refusal := resolveManifest(t, `
variable "ns" {
  default = "team-a"
}

locals {
  crontab = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata   = { name = "shared", namespace = var.ns }
  }
}

resource "kubernetes_manifest" "crontab" {
  manifest = local.crontab
}
`)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	got := manifestResolution(t, result, "kubernetes_manifest.crontab")
	if got.Class != ClassConcrete || got.ImportID != "apiVersion=stable.example.com/v1,kind=CronTab,namespace=team-a,name=shared" {
		t.Errorf("resolved %s, want CONCRETE ...namespace=team-a,name=shared through the local and the variable", got.String())
	}
}

func TestManifestComputedWholeIsRefusedByName(t *testing.T) {
	_, refusal := resolveManifest(t, `
resource "kubernetes_manifest" "fromfile" {
  manifest = yamldecode(file("${path.module}/crontab.yaml"))
}
`)
	if refusal == "" {
		t.Fatal("a manifest computed by yamldecode(file(...)) resolved; the key that names the object is not known until the value exists")
	}
	if !strings.Contains(refusal, "Identity not resolvable from configuration") || !strings.Contains(refusal, "metadata.name") {
		t.Errorf("refusal does not name the unreadable key: %s", refusal)
	}
}
