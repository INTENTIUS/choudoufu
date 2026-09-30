// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
)

// GitHub issue #1757: a root with two kubernetes provider configurations
// on one cluster runs one discovery pass per configuration, and both list
// the same objects. The pass whose configuration declares the block an
// object's address annotation names binds it (#1640); before this fix the
// other pass, which may not bind outside its own configuration, filed the
// same object as an orphan with Removal set, and Merge kept both: an
// import of ns/x at the block's address and a destroy of ns/x in one plan.

func k8sTwoConfig(t *testing.T) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	src := `
provider "kubernetes" {}
provider "kubernetes" {
  alias = "other"
}
resource "kubernetes_config_map_v1" "cfg" {
  metadata {
    name      = "cfg-${var.v}"
    namespace = "ns"
  }
}
variable "v" { default = "b" }
resource "kubernetes_config_map_v1" "elsewhere" {
  provider = kubernetes.other
  metadata {
    name      = "elsewhere"
    namespace = "ns"
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return loadConfig(t, dir)
}

func k8sProvider(alias string) addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes"), Alias: alias}
}

// twoConfigMerge runs a full Discover once per provider configuration
// over one cluster holding objects, then Merge, the way the command layer
// drives a multi-provider plan. It returns every pass's orphans (by pass
// label) as well as the merged result.
func twoConfigMerge(t *testing.T, objects []kubesweep.Object, resolutions []identity.Resolution, refused map[string]bool) (*Result, map[string][]OwnedResource) {
	t.Helper()
	cfg := k8sTwoConfig(t)
	perPass := map[string][]OwnedResource{}
	var passes []Pass
	for _, scope := range []addrs.AbsProviderConfig{k8sProvider(""), k8sProvider("other")} {
		sweeper := &stubSweeper{kinds: []kubesweep.Kind{configMapKind()}, objects: map[string][]kubesweep.Object{"ConfigMap": objects}}
		res, diags := Discover(context.Background(), Request{
			Estate:        "m1116",
			Config:        cfg,
			Provider:      newFakeCloud(),
			Resolutions:   resolutions,
			NodeRefused:   refused,
			ScopeProvider: scope,
			Sweepers:      []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
		})
		if diags.HasErrors() {
			t.Fatalf("pass %s: unexpected errors: %s", scope, diags.Err())
		}
		perPass[scope.String()] = append([]OwnedResource(nil), res.Orphans...)
		passes = append(passes, Pass{Provider: scope, Result: res})
	}
	merged, _, diags := Merge("m1116", passes, false)
	if diags.HasErrors() {
		t.Fatalf("merge: unexpected errors: %s", diags.Err())
	}
	return merged, perPass
}

// resolutionsFor is every merged resolution importing importID, rendered
// as "address import=ID" with "(undeclared)" on a removal.
func resolutionsFor(res *Result, importID string) []string {
	var out []string
	for _, r := range res.Resolutions {
		if r.ImportID != importID {
			continue
		}
		s := r.Addr.String() + " import=" + r.ImportID
		if r.Undeclared {
			s += " (undeclared)"
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func removalsFor(res *Result, importID string) []string {
	var out []string
	for _, o := range res.Orphans {
		if o.ImportID == importID && o.Removal {
			out = append(out, o.Addr.String()+" via "+o.Provider.String())
		}
	}
	sort.Strings(out)
	return out
}

func TestKubernetesTwoConfigsBindOnceAndRemoveNothing(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")
	merged, perPass := twoConfigMerge(t,
		[]kubesweep.Object{labelled("ConfigMap", "ns", "x", cfg.String())},
		nil, map[string]bool{cfg.String(): true})

	// What each pass decided on its own, before Merge: the answer to
	// whether the orphan the non-owning pass files carries Removal.
	for pass, orphans := range perPass {
		for _, o := range orphans {
			t.Logf("pass %s: orphan %s import=%s Removal=%v Withheld=%q", pass, o.Addr, o.ImportID, o.Removal, o.Withheld)
		}
	}

	if got := resolutionsFor(merged, "ns/x"); len(got) != 1 || got[0] != cfg.String()+" import=ns/x" {
		t.Errorf("merged resolutions importing ns/x = %v, want exactly [%s import=ns/x]", got, cfg)
	}
	if got := removalsFor(merged, "ns/x"); len(got) != 0 {
		t.Errorf("merged removals of ns/x = %v, want none: %s's pass bound it", got, cfg)
	}
	var bound []string
	for _, b := range merged.Bindings {
		bound = append(bound, b.Addr.String()+" import="+b.ImportID)
	}
	if len(bound) != 1 || bound[0] != cfg.String()+" import=ns/x" {
		t.Errorf("merged bindings = %v, want exactly [%s import=ns/x]", bound, cfg)
	}
}

// The controls: what no configuration's block claims by its annotation is
// the orphan it always was, removed once after the merge's dedup.
func TestKubernetesTwoConfigsControls(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")
	gone := k8sInstance(t, "kubernetes_config_map_v1", "gone")
	for _, tc := range []struct {
		name        string
		objects     []kubesweep.Object
		resolutions []identity.Resolution
		refused     map[string]bool
		importID    string
		// wantRemovals is how many merged orphans of importID carry
		// Removal, and how many undeclared resolutions import it.
		wantRemovals int
	}{
		{
			name:         "an annotation no configuration declares",
			objects:      []kubesweep.Object{labelled("ConfigMap", "ns", "left", gone.String())},
			importID:     "ns/left",
			wantRemovals: 1,
		},
		{
			name:         "an unannotated object",
			objects:      []kubesweep.Object{labelled("ConfigMap", "ns", "plain", "")},
			importID:     "ns/plain",
			wantRemovals: 1,
		},
		{
			// The owning pass does not bind cfg-a (cfg's own object,
			// cfg-b, is listed) and orphans it; the other pass must not
			// decide otherwise.
			name: "the owner orphans an object carrying its address",
			objects: []kubesweep.Object{
				labelled("ConfigMap", "ns", "cfg-b", cfg.String()),
				labelled("ConfigMap", "ns", "cfg-a", cfg.String()),
			},
			resolutions:  []identity.Resolution{{Addr: cfg, Class: identity.ClassConcrete, ImportID: "ns/cfg-b"}},
			importID:     "ns/cfg-a",
			wantRemovals: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged, _ := twoConfigMerge(t, tc.objects, tc.resolutions, tc.refused)
			if got := removalsFor(merged, tc.importID); len(got) != tc.wantRemovals {
				t.Errorf("merged removals of %s = %v, want %d", tc.importID, got, tc.wantRemovals)
			}
			var undeclared []string
			for _, r := range resolutionsFor(merged, tc.importID) {
				if strings.HasSuffix(r, "(undeclared)") {
					undeclared = append(undeclared, r)
				}
			}
			if len(undeclared) != tc.wantRemovals {
				t.Errorf("merged removal resolutions of %s = %v, want %d", tc.importID, undeclared, tc.wantRemovals)
			}
		})
	}
}
