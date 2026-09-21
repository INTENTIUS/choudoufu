// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
)

// TestHeldKubernetesDeletesJoinsByGroupAsWellAsKind (GitHub issue #1184,
// on #1111's lesson): a CRD spelled like a built-in kind, in its own group,
// is a different resource. A delete through the built-in type is looked for
// in the built-in's listing only, and a delete through the manifest type in
// the group its import id names - so a terminating object of the same
// kind, namespace and name in the other group is never reported as this
// run's. The end-to-end shape is pinned in internal/command.
func TestHeldKubernetesDeletesJoinsByGroupAsWellAsKind(t *testing.T) {
	builtIn := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map"}}
	lookalike := kubesweep.Kind{GVR: schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "example.com/v1", TypeNames: []string{"kubernetes_manifest"}, Manifest: true}
	terminating := []kubesweep.Object{{Kind: "ConfigMap", Namespace: "a", Name: "same", DeletionTimestamp: "2026-09-16T08:23:38Z", Finalizers: []string{"x/y"}}}

	for name, tc := range map[string]struct {
		deleted    DeletedKubernetesObject
		wantListed string
	}{
		"built-in type": {
			deleted:    DeletedKubernetesObject{Addr: k8sInstance(t, "kubernetes_config_map", "same"), TypeName: "kubernetes_config_map", ImportID: "a/same"},
			wantListed: "/v1, Resource=configmaps",
		},
		"manifest type, the CRD's group": {
			deleted:    DeletedKubernetesObject{Addr: k8sInstance(t, "kubernetes_manifest", "same"), TypeName: "kubernetes_manifest", ImportID: "apiVersion=example.com/v1,kind=ConfigMap,namespace=a,name=same"},
			wantListed: "example.com/v1, Resource=configmaps",
		},
		"manifest type, the core group": {
			deleted:    DeletedKubernetesObject{Addr: k8sInstance(t, "kubernetes_manifest", "same"), TypeName: "kubernetes_manifest", ImportID: "apiVersion=v1,kind=ConfigMap,namespace=a,name=same"},
			wantListed: "/v1, Resource=configmaps",
		},
	} {
		sweeper := &groupRecordingSweeper{stubSweeper: stubSweeper{kinds: []kubesweep.Kind{builtIn, lookalike}, objects: map[string][]kubesweep.Object{"ConfigMap": terminating}}}
		held, err := HeldKubernetesDeletes(context.Background(), sweeper, []string{"kubernetes_config_map", "kubernetes_manifest"}, "kubernetes_manifest", "smoke-k8s", []DeletedKubernetesObject{tc.deleted})
		if err != nil {
			t.Fatalf("%s: %s", name, err)
		}
		if len(sweeper.gvrs) != 1 || sweeper.gvrs[0] != tc.wantListed {
			t.Errorf("%s: listed %v, want only %q", name, sweeper.gvrs, tc.wantListed)
		}
		if len(held) != 1 || held[0].Addr.String() != tc.deleted.Addr.String() {
			t.Errorf("%s: held = %+v, want the one deleted object", name, held)
		}
	}
}

// groupRecordingSweeper records which resource each list was sent to, which
// [stubSweeper]'s kind-keyed record cannot tell apart here.
type groupRecordingSweeper struct {
	stubSweeper
	gvrs []string
}

func (s *groupRecordingSweeper) List(ctx context.Context, k kubesweep.Kind, key, value string) ([]kubesweep.Object, int, error) {
	s.gvrs = append(s.gvrs, k.GVR.String())
	return s.stubSweeper.List(ctx, k, key, value)
}

// TestHeldKubernetesDeletesDiagIsNothingForNothing: no held object, no
// diagnostic - not an empty warning.
func TestHeldKubernetesDeletesDiagIsNothingForNothing(t *testing.T) {
	if diags := HeldKubernetesDeletesDiag(nil); len(diags) != 0 {
		t.Errorf("diagnostics = %v, want none", diags)
	}
}
