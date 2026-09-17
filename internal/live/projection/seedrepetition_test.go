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
	"github.com/intentius/choudoufu/internal/configs"
)

// GitHub issue #1178. The defect on a cluster: a kubernetes_manifest under
// count or for_each never bound to the objects it had just created, because
// the configured seed - the only thing that can put a `manifest` in a
// projected prior, since the provider never reads that argument back - was
// built with an evaluator that refuses count.index and each.key, so the
// whole argument was dropped and the estate's marker inside it went with it.
//
// Proving these red: delete the seedRepetition call from
// [builder.prepareRead] and TestExpandedManifestSeedNeedsRepetitionData's
// "with" arm fails with the manifest missing from the seed, which is
// exactly the state that read as UNOWNED on the cluster. The "without" arm
// is the same test's own control: it pins that the gap is real, so the
// "with" arm cannot be passing for some other reason.

// expandedManifestConfig writes a root with a counted and a for_each
// manifest block, each naming its object from its own repetition value -
// #1178's own shape, and the one the reference-k8s-cert-manager estate's
// day2_count stage adds.
func expandedManifestConfig(t *testing.T) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "kubernetes_manifest" "shard" {
  count = 2
  manifest = {
    apiVersion = "cert-manager.io/v1"
    kind       = "Issuer"
    metadata   = { name = "shard-${count.index}", namespace = "cert-manager" }
    spec       = { selfSigned = {} }
  }
}

resource "kubernetes_manifest" "byname" {
  for_each = { alpha = "one", beta = "two" }
  manifest = {
    apiVersion = "cert-manager.io/v1"
    kind       = "Issuer"
    metadata   = { name = "fe-${each.key}", namespace = "cert-manager" }
    spec       = { selfSigned = {}, note = each.value }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return loadConfig(t, dir)
}

// manifestNameInSeed reads metadata.name out of a seeded manifest, or
// reports what went missing instead.
func manifestNameInSeed(t *testing.T, seed map[string]cty.Value) (string, bool) {
	t.Helper()
	manifest, ok := seed["manifest"]
	if !ok {
		return "", false
	}
	if !manifest.Type().IsObjectType() || !manifest.Type().HasAttribute("metadata") {
		t.Fatalf("the seeded manifest is not an object with metadata: %#v", manifest)
	}
	meta := manifest.GetAttr("metadata")
	if !meta.Type().IsObjectType() || !meta.Type().HasAttribute("name") {
		t.Fatalf("the seeded manifest's metadata has no name: %#v", meta)
	}
	return meta.GetAttr("name").AsString(), true
}

func TestExpandedManifestSeedNeedsRepetitionData(t *testing.T) {
	ctx := context.Background()
	cfg := expandedManifestConfig(t)
	mod := cfg.Module
	schema := manifestSeedSchema()

	for _, tc := range []struct {
		name     string
		resource string
		key      addrs.InstanceKey
		want     string
	}{
		{"count index 0", "kubernetes_manifest.shard", addrs.IntKey(0), "shard-0"},
		{"count index 1", "kubernetes_manifest.shard", addrs.IntKey(1), "shard-1"},
		{"for_each alpha", "kubernetes_manifest.byname", addrs.StringKey("alpha"), "fe-alpha"},
		{"for_each beta", "kubernetes_manifest.byname", addrs.StringKey("beta"), "fe-beta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := mod.ManagedResources[tc.resource]
			if rc == nil {
				t.Fatalf("fixture does not declare %s; it declares %v", tc.resource, keysOfResources(cfg))
			}

			// The control: the bare module-level evaluator, which is what
			// prepareRead used before #1178. The repetition reference cannot
			// resolve, so the WHOLE manifest argument is left out.
			bare, _ := configuredAttrsSeed(ctx, mod.StaticEvaluator, cfg.Path, rc, schema, nil)
			if name, ok := manifestNameInSeed(t, bare); ok {
				t.Fatalf("the bare module evaluator seeded a manifest naming %q; this control must fail, or the test below proves nothing", name)
			}

			rd, ok := (&builder{}).seedRepetition(ctx, cfg.Path, mod, rc, tc.key)
			if !ok {
				t.Fatalf("seedRepetition declined %s%s", tc.resource, tc.key)
			}
			seeded, _ := configuredAttrsSeed(ctx, mod.StaticEvaluator.WithRepetitionData(rd), cfg.Path, rc, schema, nil)
			name, ok := manifestNameInSeed(t, seeded)
			if !ok {
				t.Fatalf("no manifest in the seed for %s%s; got keys %v", tc.resource, tc.key, seedKeys(seeded))
			}
			if name != tc.want {
				t.Errorf("seeded manifest names %q, want %q", name, tc.want)
			}
		})
	}
}

// TestSeedRepetitionReadsTheInstanceKey pins what the repetition data is
// derived FROM: the key, never the block's position or a guess. The
// each.value arm matters on its own - a for_each whose values are statically
// evaluable must carry them, or a manifest naming each.value stays unseeded
// for the same reason #1178's counted one did.
func TestSeedRepetitionReadsTheInstanceKey(t *testing.T) {
	ctx := context.Background()
	cfg := expandedManifestConfig(t)
	mod := cfg.Module
	counted := mod.ManagedResources["kubernetes_manifest.shard"]
	keyed := mod.ManagedResources["kubernetes_manifest.byname"]

	rd, ok := (&builder{}).seedRepetition(ctx, cfg.Path, mod, counted, addrs.IntKey(3))
	if !ok || rd.CountIndex == cty.NilVal {
		t.Fatalf("an IntKey produced %#v, ok=%v; count.index must come from the key", rd, ok)
	}
	if got, _ := rd.CountIndex.AsBigFloat().Int64(); got != 3 {
		t.Errorf("count.index = %d, want 3", got)
	}
	if rd.EachKey != cty.NilVal || rd.EachValue != cty.NilVal {
		t.Errorf("a counted instance was given each.key/each.value: %#v", rd)
	}

	rd, ok = (&builder{}).seedRepetition(ctx, cfg.Path, mod, keyed, addrs.StringKey("beta"))
	if !ok {
		t.Fatal("a StringKey on a for_each block was declined")
	}
	if rd.EachKey == cty.NilVal || rd.EachKey.AsString() != "beta" {
		t.Errorf("each.key = %#v, want \"beta\"", rd.EachKey)
	}
	if rd.EachValue == cty.NilVal || rd.EachValue.AsString() != "two" {
		t.Errorf("each.value = %#v, want \"two\" from the block's own for_each", rd.EachValue)
	}
	if rd.CountIndex != cty.NilVal {
		t.Errorf("a for_each instance was given a count.index: %#v", rd)
	}

	// The shapes that must decline rather than invent a value: an unkeyed
	// instance, and a key whose kind the block's own expansion contradicts.
	if _, ok := (&builder{}).seedRepetition(ctx, cfg.Path, mod, counted, addrs.NoKey); ok {
		t.Error("an unkeyed instance was given repetition data")
	}
	if _, ok := (&builder{}).seedRepetition(ctx, cfg.Path, mod, counted, addrs.StringKey("alpha")); ok {
		t.Error("a StringKey on a block with no for_each was given each.key")
	}
	if _, ok := (&builder{}).seedRepetition(ctx, cfg.Path, mod, keyed, addrs.IntKey(0)); ok {
		t.Error("an IntKey on a block with no count was given a count.index")
	}
	if _, ok := (&builder{}).seedRepetition(ctx, addrs.RootModule, nil, nil, addrs.IntKey(0)); ok {
		t.Error("a nil resource block was given repetition data")
	}
}

// TestSeedRepetitionDeclinesAnUnevaluableForEachValue pins the safety half:
// each.value is a real value or it is absent, never a guess. A for_each over
// a resource's own computed attribute cannot be evaluated from configuration,
// so each.key is still answered from the key and each.value is left unset -
// which behaves exactly as no repetition data at all, and the argument that
// reads it stays unseeded.
func TestSeedRepetitionDeclinesAnUnevaluableForEachValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "kubernetes_namespace" "ns" {
  metadata { name = "x" }
}

resource "kubernetes_manifest" "byname" {
  for_each = { alpha = kubernetes_namespace.ns.metadata[0].name }
  manifest = {
    apiVersion = "cert-manager.io/v1"
    kind       = "Issuer"
    metadata   = { name = "fe-${each.key}", namespace = each.value }
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	rc := cfg.Module.ManagedResources["kubernetes_manifest.byname"]
	if rc == nil {
		t.Fatalf("fixture does not declare the block; it declares %v", keysOfResources(cfg))
	}
	rd, ok := (&builder{}).seedRepetition(context.Background(), cfg.Path, cfg.Module, rc, addrs.StringKey("alpha"))
	if !ok {
		t.Fatal("the instance was declined outright; each.key is knowable from the key alone")
	}
	if rd.EachKey == cty.NilVal || rd.EachKey.AsString() != "alpha" {
		t.Errorf("each.key = %#v, want \"alpha\"", rd.EachKey)
	}
	if rd.EachValue != cty.NilVal {
		t.Errorf("each.value = %#v for a for_each configuration cannot evaluate; it must stay unset", rd.EachValue)
	}
}

// TestForEachElementsMemoIsPerBlock pins that [builder.forEachElements]'s
// memo - which exists so a for_each of n elements is not evaluated n times
// over - keys on the block and not on anything coarser. Two blocks in one
// module must not share an answer, and two instances of one block must get
// the same one.
//
// Proved red by keying the memo on the module path alone: "byname" then
// answers with "other"'s elements and each.value comes back wrong, which is
// the seed carrying a value from a different block.
func TestForEachElementsMemoIsPerBlock(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "kubernetes_manifest" "byname" {
  for_each = { alpha = "one", beta = "two" }
  manifest = { apiVersion = "v1", kind = "ConfigMap", metadata = { name = each.key } }
}

resource "kubernetes_manifest" "other" {
  for_each = { alpha = "ONE", beta = "TWO" }
  manifest = { apiVersion = "v1", kind = "Secret", metadata = { name = each.key } }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	mod := cfg.Module
	b := &builder{}

	for _, tc := range []struct{ resource, key, want string }{
		{"kubernetes_manifest.byname", "alpha", "one"},
		{"kubernetes_manifest.other", "alpha", "ONE"},
		{"kubernetes_manifest.byname", "beta", "two"},
		{"kubernetes_manifest.other", "beta", "TWO"},
		// Repeats, now served from the memo rather than recomputed.
		{"kubernetes_manifest.byname", "alpha", "one"},
		{"kubernetes_manifest.other", "beta", "TWO"},
	} {
		rc := mod.ManagedResources[tc.resource]
		if rc == nil {
			t.Fatalf("fixture does not declare %s", tc.resource)
		}
		rd, ok := b.seedRepetition(ctx, cfg.Path, mod, rc, addrs.StringKey(tc.key))
		if !ok || rd.EachValue == cty.NilVal {
			t.Fatalf("%s[%q] produced %#v, ok=%v", tc.resource, tc.key, rd, ok)
		}
		if got := rd.EachValue.AsString(); got != tc.want {
			t.Errorf("%s[%q] each.value = %q, want %q", tc.resource, tc.key, got, tc.want)
		}
	}
	if len(b.seedEach) != 2 {
		t.Errorf("the memo holds %d entries for two blocks: %v", len(b.seedEach), b.seedEach)
	}
}
