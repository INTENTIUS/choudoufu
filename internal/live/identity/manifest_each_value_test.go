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

	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1962: a kubernetes_manifest written `manifest = each.value`
// reads its natural key out of the for_each element the expansion already
// bound, when that element is wholly known and unmarked. The for_each value
// was resolved to produce the instance keys, so reading apiVersion, kind and
// metadata out of the same value adds no guess.
//
// Proving it red: remove the each.value arm from the Path branch in
// resolve.go ([resolver.eachValuePathParts]) and every resolving case below
// refuses with #1079's "not written as an object" sentence.

func resolveManifestFiles(t *testing.T, files map[string]string, vars map[string]cty.Value, data map[string]cty.Value) (*Result, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := loadConfig(t, dir, vars)
	result, diags := ResolveWith(context.Background(), cfg, Context{
		Schemas:     map[string]providers.Schema{"kubernetes_manifest": manifestSchema()},
		DataResults: data,
	})
	if diags.HasErrors() {
		return result, diags.Err().Error()
	}
	return result, ""
}

const manifestEachValueProviders = `
terraform {
  required_providers {
    kubernetes = { source = "hashicorp/kubernetes" }
    helm       = { source = "hashicorp/helm" }
  }
}
`

func assertManifestIDs(t *testing.T, result *Result, want map[string]string) {
	t.Helper()
	for addr, id := range want {
		got := manifestResolution(t, result, addr)
		if got.Class != ClassConcrete || got.ImportID != id {
			t.Errorf("%s resolved %s, want CONCRETE %s", addr, got.String(), id)
		}
	}
}

// Shape A: a map of object constructors written in configuration.
func TestManifestEachValueOverAMapOfConstructors(t *testing.T) {
	result, refusal := resolveManifestFiles(t, map[string]string{"main.tf": manifestEachValueProviders + `
locals {
  objs = {
    "ConfigMap/default/a" = { apiVersion = "v1", kind = "ConfigMap", metadata = { name = "a", namespace = "default" }, data = { k = "v" } }
    "ClusterRole//a"      = { apiVersion = "rbac.authorization.k8s.io/v1", kind = "ClusterRole", metadata = { name = "a" } }
  }
}

resource "kubernetes_manifest" "m" {
  for_each = local.objs
  manifest = each.value
}
`}, nil, nil)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	assertManifestIDs(t, result, map[string]string{
		`kubernetes_manifest.m["ConfigMap/default/a"]`: "apiVersion=v1,kind=ConfigMap,namespace=default,name=a",
		`kubernetes_manifest.m["ClusterRole//a"]`:      "apiVersion=rbac.authorization.k8s.io/v1,kind=ClusterRole,name=a",
	})
}

// A traversal into each.value reaches the same element.
func TestManifestEachValueTraversal(t *testing.T) {
	result, refusal := resolveManifestFiles(t, map[string]string{"main.tf": manifestEachValueProviders + `
locals {
  objs = {
    a = { obj = { apiVersion = "v1", kind = "Secret", metadata = { name = "s", namespace = "ns1" } } }
  }
}

resource "kubernetes_manifest" "m" {
  for_each = local.objs
  manifest = each.value.obj
}
`}, nil, nil)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	assertManifestIDs(t, result, map[string]string{
		`kubernetes_manifest.m["a"]`: "apiVersion=v1,kind=Secret,namespace=ns1,name=s",
	})
}

const manifestMultiDoc = `---
# Source: chart/templates/crd.yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: rel
  namespace: mon
---
apiVersion: v1
kind: Service
metadata:
  name: rel
  namespace: mon
---
apiVersion: example.com/v1
kind: Widget
metadata:
  name: rel
  namespace: mon
`

// The split-and-yamldecode shape live/kubernetes/COMPATIBILITY.md recommends
// for a rendered chart, over file(): names shared across kinds stay apart
// because kind is in both the key and the identity.
const manifestSplitRoot = `
locals {
  docs = [for d in split("\n---\n", %s) : yamldecode(d) if trimspace(replace(d, "/(?m)^#.*$/", "")) != "" && trimspace(d) != "---"]
}

resource "kubernetes_manifest" "crds" {
  for_each = { for o in local.docs : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o if o.kind == "CustomResourceDefinition" }
  manifest = each.value
}

resource "kubernetes_manifest" "rest" {
  for_each = { for o in [for d in split("\n---\n", %s) : yamldecode(d) if trimspace(replace(d, "/(?m)^#.*$/", "")) != "" && trimspace(d) != "---"] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o if o.kind != "CustomResourceDefinition" }
  manifest = each.value
}
`

var manifestSplitWant = map[string]string{
	`kubernetes_manifest.crds["CustomResourceDefinition//widgets.example.com"]`: "apiVersion=apiextensions.k8s.io/v1,kind=CustomResourceDefinition,name=widgets.example.com",
	`kubernetes_manifest.rest["ServiceAccount/mon/rel"]`:                        "apiVersion=v1,kind=ServiceAccount,namespace=mon,name=rel",
	`kubernetes_manifest.rest["Service/mon/rel"]`:                               "apiVersion=v1,kind=Service,namespace=mon,name=rel",
	`kubernetes_manifest.rest["Widget/mon/rel"]`:                                "apiVersion=example.com/v1,kind=Widget,namespace=mon,name=rel",
}

func TestManifestEachValueSplitYamldecodeOverFile(t *testing.T) {
	src := `file("${path.module}/render.yaml")`
	body := strings.ReplaceAll(manifestSplitRoot, "%s", src)
	result, refusal := resolveManifestFiles(t, map[string]string{
		"main.tf":     manifestEachValueProviders + body,
		"render.yaml": manifestMultiDoc,
	}, nil, nil)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	assertManifestIDs(t, result, manifestSplitWant)
}

// The same shape over data.helm_template.x.manifest, supplied by the #179
// data-read phase.
func TestManifestEachValueSplitYamldecodeOverHelmTemplate(t *testing.T) {
	body := strings.ReplaceAll(manifestSplitRoot, "%s", "data.helm_template.chart.manifest")
	result, refusal := resolveManifestFiles(t, map[string]string{
		"main.tf": manifestEachValueProviders + `
data "helm_template" "chart" {
  name       = "rel"
  namespace  = "mon"
  repository = "https://example.com/charts"
  chart      = "chart"
  version    = "1.0.0"
}
` + body,
	}, nil, map[string]cty.Value{
		"data.helm_template.chart": cty.ObjectVal(map[string]cty.Value{
			"name":     cty.StringVal("rel"),
			"manifest": cty.StringVal(manifestMultiDoc),
		}),
	})
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	assertManifestIDs(t, result, manifestSplitWant)
}

// A marked element still refuses by name: a sensitive value cannot become
// a marker, and the key that would name the object is inside it.
func TestManifestEachValueMarkedStillRefuses(t *testing.T) {
	_, refusal := resolveManifestFiles(t, map[string]string{"main.tf": manifestEachValueProviders + `
variable "ns" {
  type      = string
  sensitive = true
}

locals {
  objs = {
    a = { apiVersion = "v1", kind = "ConfigMap", metadata = { name = "a", namespace = var.ns } }
  }
}

resource "kubernetes_manifest" "m" {
  for_each = local.objs
  manifest = each.value
}
`}, map[string]cty.Value{"ns": cty.StringVal("secret-ns")}, nil)
	if refusal == "" {
		t.Fatal("a manifest whose element carries a sensitive value resolved")
	}
	if strings.Contains(refusal, "secret-ns") {
		t.Errorf("refusal leaks the sensitive value: %s", refusal)
	}
	if !strings.Contains(refusal, "metadata") {
		t.Errorf("refusal does not name the unreadable key: %s", refusal)
	}
}

// A partly unknown element still refuses by name.
func TestManifestEachValuePartlyUnknownStillRefuses(t *testing.T) {
	_, refusal := resolveManifestFiles(t, map[string]string{"main.tf": manifestEachValueProviders + `
data "helm_template" "chart" {
  name  = "rel"
  chart = "chart"
}

locals {
  objs = {
    a = { apiVersion = "v1", kind = "ConfigMap", metadata = { name = "a", namespace = data.helm_template.chart.namespace } }
  }
}

resource "kubernetes_manifest" "m" {
  for_each = local.objs
  manifest = each.value
}
`}, nil, map[string]cty.Value{
		"data.helm_template.chart": cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("rel"),
			"namespace": cty.UnknownVal(cty.String),
		}),
	})
	if refusal == "" {
		t.Fatal("a manifest whose element is partly unknown resolved")
	}
}
