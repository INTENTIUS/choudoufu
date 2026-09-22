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

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1116: a sibling naming itself after a kubernetes_manifest
// object reads kubernetes_manifest.x.object.metadata.name (or .namespace).
// object is the computed read-back, but those two keys are the ones the
// manifest itself wrote - the same keys the manifest's own identity is
// built from - so they resolve from configuration, the way #1064's
// kubernetes_x.y.metadata[0].name does for the object-metadata carrier.
//
// Before this, the reference was "Identity not resolvable from
// configuration", which the marker fallback turned into a discovery the
// label cannot answer (it carries no address): measured on kind, a
// ConfigMap named that way was planned as a create on every run while the
// sweep proposed destroying the object the last apply made under an
// orphan address, and alternate applies destroyed and recreated it.
//
// Proving it red: drop the manifestObjectMetaTraversal branch from
// resolveTraversal and the first case below no longer resolves CONCRETE.

func resolveManifestSibling(t *testing.T, body string) (*Result, string) {
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
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: map[string]providers.Schema{
		"kubernetes_manifest":      manifestSchema(),
		"kubernetes_config_map_v1": objectMetaSchema(true),
	}})
	if diags.HasErrors() {
		return result, diags.Err().Error()
	}
	return result, ""
}

const crontabManifest = `
resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "default"
    }
    spec = { image = "my-awesome-cron-image" }
  }
}
`

func TestManifestObjectNameAndNamespaceResolveFromTheManifest(t *testing.T) {
	result, refusal := resolveManifestSibling(t, crontabManifest+`
resource "kubernetes_config_map_v1" "reader" {
  metadata {
    name      = "${kubernetes_manifest.crontab.object.metadata.name}-reader"
    namespace = kubernetes_manifest.crontab.object.metadata.namespace
  }
  data = { image = kubernetes_manifest.crontab.object.spec.image }
}
`)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	got := manifestResolution(t, result, "kubernetes_config_map_v1.reader")
	if got.Class != ClassConcrete || got.ImportID != "default/my-crontab-reader" {
		t.Errorf("resolved %s, want CONCRETE default/my-crontab-reader: object.metadata.name and .namespace are the manifest's own keys", got.String())
	}
}

// The boundary: only the two keys that name the object and that the
// manifest itself writes. Anything else under object is the server's, and a
// cluster-scoped manifest has no namespace to read.
func TestManifestObjectOtherPathsStayUnresolved(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest string
		ref      string
	}{
		"a spec field":                  {crontabManifest, "kubernetes_manifest.crontab.object.spec.image"},
		"a server-written metadata key": {crontabManifest, "kubernetes_manifest.crontab.object.metadata.uid"},
		// The manifest writes this one, so only the leaf rule keeps it out:
		// it is a key of metadata, and still not the object's name.
		"a metadata key the manifest writes but the server replaces": {`
resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata   = { name = "my-crontab", generateName = "gen-", namespace = "default" }
  }
}
`, "kubernetes_manifest.crontab.object.metadata.generateName"},
		"the namespace of a cluster-scoped kind": {`
resource "kubernetes_manifest" "crontab" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "ClusterCronTab"
    metadata   = { name = "my-crontab" }
  }
}
`, "kubernetes_manifest.crontab.object.metadata.namespace"},
		"a manifest whose keys are not written in configuration": {`
resource "kubernetes_manifest" "crontab" {
  manifest = yamldecode(file("${path.module}/crontab.yaml"))
}
`, "kubernetes_manifest.crontab.object.metadata.name"},
	} {
		t.Run(name, func(t *testing.T) {
			result, refusal := resolveManifestSibling(t, tc.manifest+`
resource "kubernetes_config_map_v1" "reader" {
  metadata {
    name      = "reader-${`+tc.ref+`}"
    namespace = "default"
  }
}
`)
			if refusal != "" {
				// The refusal has to be the reader's own, not the parent
				// failing to resolve and taking the reader down with it,
				// or this case would pass without reaching the rule.
				if !strings.Contains(refusal, "kubernetes_config_map_v1.reader.name") {
					t.Errorf("refused, but not on the reader's reference: %s", refusal)
				}
				return
			}
			if got := manifestResolution(t, result, "kubernetes_config_map_v1.reader"); got.Class == ClassConcrete {
				t.Errorf("%s resolved CONCRETE %s; only object.metadata.name and .namespace the manifest itself writes are the object's own keys", tc.ref, got.ImportID)
			}
		})
	}
}

func TestManifestObjectMetaTraversal(t *testing.T) {
	attr := func(names ...string) hcl.Traversal {
		out := hcl.Traversal{}
		for _, n := range names {
			out = append(out, hcl.TraverseAttr{Name: n})
		}
		return out
	}
	for name, tc := range map[string]struct {
		rest hcl.Traversal
		want string
		ok   bool
	}{
		"object.metadata.name":      {attr("object", "metadata", "name"), "name", true},
		"object.metadata.namespace": {attr("object", "metadata", "namespace"), "namespace", true},
		"object.metadata.uid":       {attr("object", "metadata", "uid"), "", false},
		"object.spec.name":          {attr("object", "spec", "name"), "", false},
		"manifest.metadata.name":    {attr("manifest", "metadata", "name"), "", false},
		"object.metadata":           {attr("object", "metadata"), "", false},
		"object.metadata[0].name":   {hcl.Traversal{hcl.TraverseAttr{Name: "object"}, hcl.TraverseAttr{Name: "metadata"}, hcl.TraverseIndex{Key: cty.NumberIntVal(0)}, hcl.TraverseAttr{Name: "name"}}, "", false},
	} {
		got, ok := manifestObjectMetaTraversal(tc.rest)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: manifestObjectMetaTraversal = %q, %v; want %q, %v", name, got, ok, tc.want, tc.ok)
		}
	}
}
