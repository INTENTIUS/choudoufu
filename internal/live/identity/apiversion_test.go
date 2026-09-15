// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
)

// TestAPIVersionSpellingsRenderOneImportID pins GitHub issue #1081's
// second item: hashicorp/kubernetes ships most kinds under two type names,
// a plain one and one with an API-version suffix (kubernetes_config_map
// and kubernetes_config_map_v1, kubernetes_ingress and
// kubernetes_ingress_v1), and both name the same object, because
// uniqueness on a cluster is group, kind, namespace and name and the
// version is a representation. A block that changes spelling with the
// same metadata and no moved block therefore resolves to the same import
// id - whether the spelling has a ratified row (the plain ConfigMap) or is
// synthesized by the object-metadata rule (every other one here) - and
// the replan finds the object the old spelling created instead of
// proposing a destroy and a create. live/smoke/scenarios/k8s-greenfield.sh
// step 5 measures that on a kind cluster (an empty plan, 2026-09-12);
// internal/live/kubesweep's KindOfType is the sweep's half of the same
// fact, filing both spellings under one kind.
//
// Proving it red: give synthesizeMetadataIdentity a trailing literal
// component for a type name ending in _v1 and every versioned spelling
// below renders a different id from its plain one.
func TestAPIVersionSpellingsRenderOneImportID(t *testing.T) {
	for _, pair := range []struct {
		plain, versioned, namespace, name, want string
	}{
		{"kubernetes_config_map", "kubernetes_config_map_v1", "smoke-k8s", "app-config", "smoke-k8s/app-config"},
		{"kubernetes_ingress", "kubernetes_ingress_v1", "web", "front", "web/front"},
		{"kubernetes_namespace", "kubernetes_namespace_v1", "", "smoke-k8s", "smoke-k8s"},
	} {
		if _, has := LookupType(pair.plain); has != (pair.plain == "kubernetes_config_map" || pair.plain == "kubernetes_namespace") {
			t.Fatalf("%s: the ratified-row set moved; this test wants one spelling per pair to be a row and the other synthesized, re-pick the pairs", pair.plain)
		}
		ids := map[string]string{}
		for _, typeName := range []string{pair.plain, pair.versioned} {
			got := resolveOneObject(t, typeName, pair.namespace, pair.name)
			if got.Class != ClassConcrete {
				t.Errorf("%s: resolved %s, want CONCRETE", typeName, got.String())
			}
			ids[typeName] = got.ImportID
			if got.ImportID != pair.want {
				t.Errorf("%s: import id %q, want %q", typeName, got.ImportID, pair.want)
			}
		}
		if ids[pair.plain] != ids[pair.versioned] {
			t.Errorf("%s renders %q and %s renders %q for the same metadata; an api_version change would plan as a destroy and a create", pair.plain, ids[pair.plain], pair.versioned, ids[pair.versioned])
		}
	}
}

// resolveOneObject resolves a root holding one block of typeName with the
// given metadata, under the provider's object-metadata schema for that
// scope, and returns its resolution.
func resolveOneObject(t *testing.T, typeName, namespace, name string) Resolution {
	t.Helper()
	dir := t.TempDir()
	ns := ""
	if namespace != "" {
		ns = fmt.Sprintf("    namespace = %q\n", namespace)
	}
	src := fmt.Sprintf(`
terraform {
  required_providers {
    kubernetes = { source = "hashicorp/kubernetes" }
  }
}

resource %q "app" {
  metadata {
    name      = %q
%s  }
}
`, typeName, name, ns)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir, nil)
	schemas := map[string]providers.Schema{typeName: objectMetaSchema(namespace != "")}
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	if diags.HasErrors() {
		t.Fatalf("%s: resolution refused: %s", typeName, diags.Err())
	}
	for _, r := range result.All() {
		if r.Addr.String() == typeName+".app" {
			return r
		}
	}
	t.Fatalf("%s: no resolution for %s.app", typeName, typeName)
	return Resolution{}
}
