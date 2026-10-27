// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/pkg/kubesweep"
)

// GitHub issue #1996: an object a values change took out of a for_each
// over a helm_template render carries the address annotation of the
// instance that made it, kubernetes_manifest.rest["ConfigMap/ns/gone"].
// Stock destroys it there. The sweep filed it as
// kubernetes_config_map_v1.orphan_ns_gone - another address, and another
// provider type doing the delete - although it had the address in hand.
func TestKubernetesOrphanIsPlannedAtItsAnnotatedAddress(t *testing.T) {
	keyed := func(typeName, name, key string) addrs.AbsResourceInstance {
		return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.StringKey(key)).Absolute(addrs.RootModuleInstance)
	}
	kept := keyed("kubernetes_manifest", "rest", "ConfigMap/ns/kept")
	gone := keyed("kubernetes_manifest", "rest", "ConfigMap/ns/gone")
	shard := keyed("kubernetes_config_map_v1", "shard", "shard-1")
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")

	for _, tc := range []struct {
		name    string
		objects []kubesweep.Object
		// want is each orphan's planned address and import id, by name.
		want map[string][2]string
	}{
		{
			name: "a manifest instance's ConfigMap whose key left the render",
			objects: []kubesweep.Object{
				labelled("ConfigMap", "ns", "kept", markers.EscapeAddress(kept.String())),
				labelled("ConfigMap", "ns", "gone", markers.EscapeAddress(gone.String())),
			},
			want: map[string][2]string{"gone": {gone.String(), kubesweep.ManifestImportID("v1", "ConfigMap", "ns", "gone")}},
		},
		{
			name:    "a typed instance whose for_each key is gone",
			objects: []kubesweep.Object{labelled("ConfigMap", "ns", "shard-1", markers.EscapeAddress(shard.String()))},
			want:    map[string][2]string{"shard-1": {shard.String(), "ns/shard-1"}},
		},
		// Every shape below keeps the synthetic orphan_ address it had
		// before #1996: the annotation cannot be the instance's own.
		{
			name:    "no annotation",
			objects: []kubesweep.Object{labelled("ConfigMap", "ns", "bare", "")},
			want:    map[string][2]string{"bare": {"kubernetes_config_map_v1.orphan_ns_bare", "ns/bare"}},
		},
		{
			name:    "the annotation names a type that does not manage the kind",
			objects: []kubesweep.Object{labelled("ConfigMap", "ns", "x", "kubernetes_secret_v1.s")},
			want:    map[string][2]string{"x": {"kubernetes_config_map_v1.orphan_ns_x", "ns/x"}},
		},
		{
			// The declared instance already has its object (cfg-b), so the
			// annotated address is occupied: a destroy there would be
			// planned at a live block's own instance.
			name: "the annotation names a declared instance that has its object",
			objects: []kubesweep.Object{
				labelled("ConfigMap", "ns", "cfg-b", cfg.String()),
				labelled("ConfigMap", "ns", "cfg-a", cfg.String()),
			},
			want: map[string][2]string{"cfg-a": {"kubernetes_config_map_v1.orphan_ns_cfg-a", "ns/cfg-a"}},
		},
		{
			// Two objects claim one undeclared address: which one the
			// address held is not the annotation's to say, so neither takes
			// it and both stay the orphans they were.
			name: "two objects carry one undeclared address",
			objects: []kubesweep.Object{
				labelled("ConfigMap", "ns", "a", markers.EscapeAddress(gone.String())),
				labelled("ConfigMap", "ns", "b", markers.EscapeAddress(gone.String())),
			},
			want: map[string][2]string{
				"a": {"kubernetes_config_map_v1.orphan_ns_a", "ns/a"},
				"b": {"kubernetes_config_map_v1.orphan_ns_b", "ns/b"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sweeper := &stubSweeper{kinds: []kubesweep.Kind{configMapKind()}, objects: map[string][]kubesweep.Object{"ConfigMap": tc.objects}}
			req := Request{
				Estate:   "m1116",
				Sweepers: []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
				Resolutions: []identity.Resolution{
					{Addr: kept, Class: identity.ClassConcrete, ImportID: kubesweep.ManifestImportID("v1", "ConfigMap", "ns", "kept")},
					{Addr: cfg, Class: identity.ClassConcrete, ImportID: "ns/cfg-b"},
				},
			}
			res := &Result{}
			res.Resolutions = append(res.Resolutions, req.Resolutions...)
			if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
				t.Fatalf("unexpected errors: %s", diags.Err())
			}
			got := map[string][2]string{}
			for _, o := range res.Orphans {
				addr, ok := UnescapeAddress(o.Normalized)
				if !ok {
					t.Fatalf("orphan %s: Normalized %q does not unescape", o.ImportID, o.Normalized)
				}
				if addr.Resource.Resource.Type != o.TypeName {
					t.Errorf("orphan %s: TypeName %s, address %s; classifyOrphans withholds a type mismatch", o.ImportID, o.TypeName, addr)
				}
				_, _, _, name, isManifest := kubesweep.ParseManifestImportID(o.ImportID)
				if !isManifest {
					_, name, _ = cutLast(o.ImportID)
				}
				got[name] = [2]string{addr.String(), o.ImportID}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("orphans = %v, want %v", got, tc.want)
			}
			for name, want := range tc.want {
				if got[name] != want {
					t.Errorf("orphan %s planned at %v, want %v", name, got[name], want)
				}
			}
		})
	}
}

// cutLast splits NAMESPACE/NAME at its last slash.
func cutLast(s string) (string, string, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[:i], s[i+1:], true
		}
	}
	return "", s, false
}
