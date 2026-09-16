// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// ManifestKeyOf is GitHub issue #1109's read: the natural key out of a
// migrated object, so live-import can name the live object it is about to
// label. Proving it red: make it read only the `manifest` attribute and
// the object-fallback case reports not found, which is the stock state a
// kubernetes_manifest imported rather than applied leaves behind.

func manifestValue(apiVersion, kind, namespace, name string) cty.Value {
	meta := map[string]cty.Value{"name": cty.StringVal(name)}
	if namespace != "" {
		meta["namespace"] = cty.StringVal(namespace)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal(apiVersion),
		"kind":       cty.StringVal(kind),
		"metadata":   cty.ObjectVal(meta),
	})
}

func TestManifestKeyOf(t *testing.T) {
	crontab := manifestValue("stable.example.com/v1", "CronTab", "smoke-crd", "my-crontab")
	clusterScoped := manifestValue("stable.example.com/v1", "ClusterCronTab", "", "global")

	cases := map[string]struct {
		obj  cty.Value
		want ManifestKey
		ok   bool
	}{
		"from the manifest argument": {
			obj: cty.ObjectVal(map[string]cty.Value{
				"manifest": crontab,
				"object":   cty.NullVal(cty.DynamicPseudoType),
			}),
			want: ManifestKey{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"},
			ok:   true,
		},
		"from the computed object when the state stores manifest null": {
			obj: cty.ObjectVal(map[string]cty.Value{
				"manifest": cty.NullVal(cty.DynamicPseudoType),
				"object":   crontab,
			}),
			want: ManifestKey{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"},
			ok:   true,
		},
		"a cluster-scoped kind has no namespace and is still complete": {
			obj: cty.ObjectVal(map[string]cty.Value{
				"manifest": clusterScoped,
				"object":   cty.NullVal(cty.DynamicPseudoType),
			}),
			want: ManifestKey{APIVersion: "stable.example.com/v1", Kind: "ClusterCronTab", Name: "global"},
			ok:   true,
		},
		"neither attribute carries a key": {
			obj: cty.ObjectVal(map[string]cty.Value{
				"manifest": cty.NullVal(cty.DynamicPseudoType),
				"object":   cty.NullVal(cty.DynamicPseudoType),
			}),
		},
		"a manifest with no metadata.name is not a key": {
			obj: cty.ObjectVal(map[string]cty.Value{
				"manifest": cty.ObjectVal(map[string]cty.Value{
					"apiVersion": cty.StringVal("v1"),
					"kind":       cty.StringVal("ConfigMap"),
				}),
				"object": cty.NullVal(cty.DynamicPseudoType),
			}),
		},
		"a null object is not a key": {obj: cty.NullVal(cty.DynamicPseudoType)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := ManifestKeyOf(tc.obj)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (key %+v)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("key = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestManifestKeyOf_PrefersTheDeclaration pins the order the two sources
// are read in: the operator's own manifest wins over the object the
// provider read back, so a key is the declaration whenever there is one.
func TestManifestKeyOf_PrefersTheDeclaration(t *testing.T) {
	got, ok := ManifestKeyOf(cty.ObjectVal(map[string]cty.Value{
		"manifest": manifestValue("stable.example.com/v1", "CronTab", "declared", "my-crontab"),
		"object":   manifestValue("stable.example.com/v1", "CronTab", "read-back", "my-crontab"),
	}))
	if !ok || got.Namespace != "declared" {
		t.Fatalf("key = %+v (ok %v), want the manifest argument's own namespace", got, ok)
	}
}
