// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/strict"
)

// GitHub issue #1963, found by the reference-k8s-helm-template estate: a
// kubernetes_manifest block whose for_each is a rendered Helm chart, split
// and yamldecoded INSIDE the for_each expression, with
// `manifest = each.value`. Identity resolution binds these instances from
// the data-read phase's results (#1962), but [builder.seedRepetition] read
// each.value with the bare module evaluator, which cannot answer
// data.helm_template, so each.value stayed unbound, the manifest was never
// seeded, and a cache-less plan proposed every instance as an update with
// the provider's "Apply needed after 'import'" warning.
//
// Proved red by reverting seedrepetition.go's ForEachElementsWithData call:
// the "known" arm then reports no manifest in the seed.
func TestDataDerivedEachValueManifestSeeds(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
data "helm_template" "kps" {
  name  = "rel"
  chart = "x"
}

resource "kubernetes_manifest" "rest" {
  for_each = {
    for o in [
      for d in split("\n---\n", data.helm_template.kps.manifest) : yamldecode(d)
      if length(regexall("(?m)^kind:", d)) > 0
    ] : "${o.kind}/${try(o.metadata.namespace, "")}/${o.metadata.name}" => o
  }
  manifest = each.value
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	mod := cfg.Module
	rc := mod.ManagedResources["kubernetes_manifest.rest"]
	if rc == nil {
		t.Fatalf("fixture does not declare the block; it declares %v", keysOfResources(cfg))
	}
	schema := manifestSeedSchema()
	render := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: shared\n  namespace: monitoring\ndata:\n  a: b\n" +
		"\n---\n" +
		"apiVersion: v1\nkind: Service\nmetadata:\n  name: shared\n  namespace: monitoring\nspec:\n  ports:\n  - port: 80\n"

	seedFor := func(t *testing.T, manifest cty.Value, key string) (map[string]cty.Value, bool) {
		t.Helper()
		results := map[string]cty.Value{
			"data.helm_template.kps": cty.ObjectVal(map[string]cty.Value{"manifest": manifest}),
		}
		b := &builder{opts: Options{DataResults: results}}
		rd, ok := b.seedRepetition(ctx, cfg.Path, mod, rc, addrs.StringKey(key))
		if !ok {
			t.Fatalf("seedRepetition declined %q", key)
		}
		eval := mod.StaticEvaluator
		if lookup, _ := identity.DataLookupFor(results, cfg.Path); lookup != nil {
			eval = eval.WithDataResults(lookup)
		}
		seed, _ := configuredAttrsSeed(ctx, eval.WithRepetitionData(rd), cfg.Path, rc, schema, nil, strict.DefaultSecrets)
		return seed, rd.EachValue != cty.NilVal
	}

	t.Run("known", func(t *testing.T) {
		for _, tc := range []struct{ key, wantKind string }{
			{"ConfigMap/monitoring/shared", "ConfigMap"},
			{"Service/monitoring/shared", "Service"},
		} {
			seed, bound := seedFor(t, cty.StringVal(render), tc.key)
			if !bound {
				t.Fatalf("%s: each.value was not bound from the data-derived for_each", tc.key)
			}
			name, ok := manifestNameInSeed(t, seed)
			if !ok {
				t.Fatalf("%s: no manifest in the seed; got keys %v", tc.key, seedKeys(seed))
			}
			if name != "shared" {
				t.Errorf("%s: seeded manifest names %q, want \"shared\"", tc.key, name)
			}
			if kind := seed["manifest"].GetAttr("kind").AsString(); kind != tc.wantKind {
				t.Errorf("%s: seeded manifest is kind %q, want %q - the seed came from another element", tc.key, kind, tc.wantKind)
			}
		}
	})

	// The two that must keep declining: a render this run could not know,
	// and one carrying a sensitivity mark. Neither binds each.value, and
	// neither seeds a manifest.
	t.Run("unknown", func(t *testing.T) {
		seed, bound := seedFor(t, cty.UnknownVal(cty.String), "ConfigMap/monitoring/shared")
		if bound {
			t.Error("each.value was bound from an unknown render")
		}
		if _, ok := seed["manifest"]; ok {
			t.Error("a manifest was seeded from an unknown render")
		}
	})
	t.Run("marked", func(t *testing.T) {
		seed, bound := seedFor(t, cty.StringVal(render).Mark("sensitive"), "ConfigMap/monitoring/shared")
		if bound {
			t.Error("each.value was bound from a sensitive render")
		}
		if _, ok := seed["manifest"]; ok {
			t.Error("a manifest was seeded from a sensitive render")
		}
	})
}
