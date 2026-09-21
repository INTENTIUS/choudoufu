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

// TestHeldKubernetesDeletesDiagWording pins the variants the command-level
// tests do not reach: nothing has finalizers, so there is nothing for a
// command to show and the closing says the server is still finishing; and a
// cluster-scoped object's command carries no -n.
func TestHeldKubernetesDeletesDiagWording(t *testing.T) {
	pod := HeldKubernetesDelete{Addr: k8sInstance(t, "kubernetes_pod", "web"), Kind: "Pod", Namespace: "a", Name: "web", DeletionTimestamp: "x"}
	job := HeldKubernetesDelete{Addr: k8sInstance(t, "kubernetes_job", "run"), Kind: "Job", Group: "batch", Namespace: "a", Name: "run", DeletionTimestamp: "x"}
	role := HeldKubernetesDelete{Addr: k8sInstance(t, "kubernetes_cluster_role", "r"), Kind: "ClusterRole", Group: "rbac.authorization.k8s.io", Name: "r", DeletionTimestamp: "x", Finalizers: []string{"x/y"}}
	for name, tc := range map[string]struct {
		held []HeldKubernetesDelete
		want string
	}{
		"one object, no finalizers": {[]HeldKubernetesDelete{pod},
			"The API server accepted the delete of 1 object and it is still in the cluster, terminating:\n\n" +
				"  - Pod a/web (kubernetes_pod.web), no finalizers: the server has not finished the delete yet\n\n" +
				"It stays until the server finishes the delete, and the next plan will propose destroying it again."},
		"two objects, no finalizers": {[]HeldKubernetesDelete{job, pod},
			"The API server accepted the delete of 2 objects and they are still in the cluster, terminating:\n\n" +
				"  - Job a/run (kubernetes_job.run), no finalizers: the server has not finished the delete yet\n" +
				"  - Pod a/web (kubernetes_pod.web), no finalizers: the server has not finished the delete yet\n\n" +
				"They stay until the server finishes the delete, and the next plan will propose destroying them again."},
		"cluster-scoped, grouped": {[]HeldKubernetesDelete{role},
			"The API server accepted the delete of 1 object and it is still in the cluster, terminating:\n\n" +
				"  - ClusterRole r (kubernetes_cluster_role.r), finalizers: x/y\n\n" +
				"It stays until those finalizers are removed, and the next plan will propose destroying it again. To see what holds it:\n" +
				"  kubectl get clusterrole.rbac.authorization.k8s.io r -o jsonpath='{.metadata.finalizers}'"},
	} {
		diags := HeldKubernetesDeletesDiag(tc.held)
		if len(diags) != 1 {
			t.Fatalf("%s: diagnostics = %v, want one", name, diags)
		}
		if got := diags[0].Description().Detail; got != tc.want {
			t.Errorf("%s: detail:\n%s\nwant:\n%s", name, got, tc.want)
		}
	}
}
