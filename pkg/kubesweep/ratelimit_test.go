// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	restclient "k8s.io/client-go/rest"
)

// sweepResource is one resource a fake cluster's discovery serves.
type sweepResource struct {
	gv, name, kind string
	namespaced     bool
	listable       bool // serves both list and delete
}

// kindClusterResources is roughly what a kind cluster at v1.37 serves:
// every built-in group-version's resources, the ones without list and
// delete (bindings, the reviews, componentstatuses) included so the sweep
// filters them the way it does on a real cluster.
var kindClusterResources = func() []sweepResource {
	var out []sweepResource
	add := func(gv string, namespaced bool, listable bool, pairs ...string) {
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, sweepResource{gv: gv, name: pairs[i], kind: pairs[i+1], namespaced: namespaced, listable: listable})
		}
	}
	add("v1", true, true, "configmaps", "ConfigMap", "endpoints", "Endpoints", "events", "Event",
		"limitranges", "LimitRange", "persistentvolumeclaims", "PersistentVolumeClaim", "pods", "Pod",
		"podtemplates", "PodTemplate", "replicationcontrollers", "ReplicationController",
		"resourcequotas", "ResourceQuota", "secrets", "Secret", "serviceaccounts", "ServiceAccount",
		"services", "Service")
	add("v1", false, true, "namespaces", "Namespace", "nodes", "Node", "persistentvolumes", "PersistentVolume")
	add("v1", true, false, "bindings", "Binding")
	add("v1", false, false, "componentstatuses", "ComponentStatus")
	add("admissionregistration.k8s.io/v1", false, true, "mutatingwebhookconfigurations", "MutatingWebhookConfiguration",
		"validatingwebhookconfigurations", "ValidatingWebhookConfiguration",
		"validatingadmissionpolicies", "ValidatingAdmissionPolicy",
		"validatingadmissionpolicybindings", "ValidatingAdmissionPolicyBinding",
		"mutatingadmissionpolicies", "MutatingAdmissionPolicy",
		"mutatingadmissionpolicybindings", "MutatingAdmissionPolicyBinding")
	add("apiextensions.k8s.io/v1", false, true, "customresourcedefinitions", "CustomResourceDefinition")
	add("apiregistration.k8s.io/v1", false, true, "apiservices", "APIService")
	add("apps/v1", true, true, "controllerrevisions", "ControllerRevision", "daemonsets", "DaemonSet",
		"deployments", "Deployment", "replicasets", "ReplicaSet", "statefulsets", "StatefulSet")
	add("events.k8s.io/v1", true, true, "events", "Event")
	add("authentication.k8s.io/v1", false, false, "selfsubjectreviews", "SelfSubjectReview", "tokenreviews", "TokenReview")
	add("authorization.k8s.io/v1", false, false, "selfsubjectaccessreviews", "SelfSubjectAccessReview",
		"selfsubjectrulesreviews", "SelfSubjectRulesReview", "subjectaccessreviews", "SubjectAccessReview")
	add("authorization.k8s.io/v1", true, false, "localsubjectaccessreviews", "LocalSubjectAccessReview")
	add("autoscaling/v2", true, true, "horizontalpodautoscalers", "HorizontalPodAutoscaler")
	add("batch/v1", true, true, "cronjobs", "CronJob", "jobs", "Job")
	add("certificates.k8s.io/v1", false, true, "certificatesigningrequests", "CertificateSigningRequest")
	add("networking.k8s.io/v1", true, true, "ingresses", "Ingress", "networkpolicies", "NetworkPolicy")
	add("networking.k8s.io/v1", false, true, "ingressclasses", "IngressClass", "ipaddresses", "IPAddress", "servicecidrs", "ServiceCIDR")
	add("policy/v1", true, true, "poddisruptionbudgets", "PodDisruptionBudget")
	add("rbac.authorization.k8s.io/v1", false, true, "clusterrolebindings", "ClusterRoleBinding", "clusterroles", "ClusterRole")
	add("rbac.authorization.k8s.io/v1", true, true, "rolebindings", "RoleBinding", "roles", "Role")
	add("storage.k8s.io/v1", false, true, "csidrivers", "CSIDriver", "csinodes", "CSINode",
		"storageclasses", "StorageClass", "volumeattachments", "VolumeAttachment",
		"volumeattributesclasses", "VolumeAttributesClass")
	add("storage.k8s.io/v1", true, true, "csistoragecapacities", "CSIStorageCapacity")
	add("scheduling.k8s.io/v1", false, true, "priorityclasses", "PriorityClass")
	add("coordination.k8s.io/v1", true, true, "leases", "Lease")
	add("node.k8s.io/v1", false, true, "runtimeclasses", "RuntimeClass")
	add("discovery.k8s.io/v1", true, true, "endpointslices", "EndpointSlice")
	add("flowcontrol.apiserver.k8s.io/v1", false, true, "flowschemas", "FlowSchema",
		"prioritylevelconfigurations", "PriorityLevelConfiguration")
	add("resource.k8s.io/v1", false, true, "deviceclasses", "DeviceClass", "resourceslices", "ResourceSlice")
	add("resource.k8s.io/v1", true, true, "resourceclaims", "ResourceClaim", "resourceclaimtemplates", "ResourceClaimTemplate")
	return out
}()

// crdResources is n custom resources across groups of five, the shape a
// chart like kyverno or argo-cd installs.
func crdResources(n int) []sweepResource {
	out := make([]sweepResource, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, sweepResource{
			gv:         fmt.Sprintf("group%d.example.io/v1", i/5),
			name:       fmt.Sprintf("widget%ds", i),
			kind:       fmt.Sprintf("Widget%d", i),
			namespaced: i%2 == 0,
			listable:   true,
		})
	}
	return out
}

// sweepAPIServer serves legacy discovery for resources and an empty list
// for every collection, counting requests by kind of path.
type sweepAPIServer struct {
	*httptest.Server
	discovery, lists atomic.Int64
}

func newSweepAPIServer(t *testing.T, resources []sweepResource) *sweepAPIServer {
	t.Helper()
	byGV := map[string][]sweepResource{}
	var gvs []string
	for _, r := range resources {
		if _, ok := byGV[r.gv]; !ok {
			gvs = append(gvs, r.gv)
		}
		byGV[r.gv] = append(byGV[r.gv], r)
	}
	resourceList := func(gv string) string {
		var rs []map[string]any
		for _, r := range byGV[gv] {
			verbs := []string{"create", "get"}
			if r.listable {
				verbs = append(verbs, "list", "delete")
			}
			rs = append(rs, map[string]any{"name": r.name, "kind": r.kind, "namespaced": r.namespaced, "verbs": verbs})
		}
		b, _ := json.Marshal(map[string]any{"kind": "APIResourceList", "groupVersion": gv, "resources": rs})
		return string(b)
	}
	var groups []map[string]any
	for _, gv := range gvs {
		if gv == "v1" {
			continue
		}
		name := strings.SplitN(gv, "/", 2)[0]
		v := map[string]string{"groupVersion": gv, "version": strings.SplitN(gv, "/", 2)[1]}
		groups = append(groups, map[string]any{"name": name, "versions": []any{v}, "preferredVersion": v})
	}
	groupsBody, _ := json.Marshal(map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": groups})

	s := &sweepAPIServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case path == "/api":
			s.discovery.Add(1)
			fmt.Fprint(w, `{"kind":"APIVersions","versions":["v1"]}`)
			return
		case path == "/apis":
			s.discovery.Add(1)
			w.Write(groupsBody)
			return
		case path == "/api/v1":
			s.discovery.Add(1)
			fmt.Fprint(w, resourceList("v1"))
			return
		}
		if gv := strings.TrimPrefix(path, "/apis/"); gv != path && strings.Count(gv, "/") == 1 {
			if _, ok := byGV[gv]; ok {
				s.discovery.Add(1)
				fmt.Fprint(w, resourceList(gv))
				return
			}
		}
		s.lists.Add(1)
		fmt.Fprint(w, `{"kind":"List","apiVersion":"v1","metadata":{},"items":[]}`)
	}))
	t.Cleanup(s.Close)
	return s
}

// sweepOnce is what discovery.KubernetesSweep asks of the client: Kinds,
// then one List per kind.
func sweepOnce(ctx context.Context, c *Client) (int, error) {
	kinds, _, err := c.Kinds(ctx, nil, "kubernetes_manifest")
	if err != nil {
		return 0, err
	}
	for _, k := range kinds {
		if _, _, err := c.List(ctx, k, "tofu-estate", "e"); err != nil {
			return len(kinds), fmt.Errorf("listing %s: %w", k.GVR, err)
		}
	}
	return len(kinds), nil
}

// TestSweepIsNotThrottledByClientGoDefaults: a sweep is one burst of
// reads, a List per kind the cluster serves, and on a kind cluster with a
// chart's CRDs that is a hundred requests. Built from a rest.Config with
// QPS and Burst unset, client-go gives each client its own 5 QPS, burst 10
// token bucket, and the sweep spends most of its time waiting on itself:
// at least 18 seconds for these 98 kinds, every sweep, every run.
//
// The deadline is one the default limiter cannot meet. client-go's limiter
// refuses at once a wait that would pass the context's deadline, so the
// red arm fails in milliseconds with "would exceed context deadline"
// rather than after the full wait.
func TestSweepIsNotThrottledByClientGoDefaults(t *testing.T) {
	resources := append(append([]sweepResource{}, kindClusterResources...), crdResources(40)...)
	srv := newSweepAPIServer(t, resources)

	// A bare config, not [RestConfig]'s, so this holds of [New] on its
	// own; TestRestConfigSetsRateLimits holds the record store's clients.
	c, err := New(&restclient.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	kinds, err := sweepOnce(ctx, c)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("sweep of %d kinds did not finish inside 5s (%d discovery requests, %d lists, %s): %s", kinds, srv.discovery.Load(), srv.lists.Load(), elapsed, err)
	}
	t.Logf("one sweep: %d kinds, %d discovery requests, %d lists, %s", kinds, srv.discovery.Load(), srv.lists.Load(), elapsed)
	if kinds < 90 {
		t.Fatalf("the fake cluster served %d listable kinds, want at least 90: the test no longer measures a sweep a 5 QPS limiter cannot finish in 5s", kinds)
	}
}

// TestRestConfigSetsRateLimits: the record store on Kubernetes builds its
// clientset straight from [RestConfig], not through [New], so the limits
// have to be on the config itself.
func TestRestConfigSetsRateLimits(t *testing.T) {
	for _, env := range []string{"KUBECONFIG", "KUBE_CONFIG_PATH", "KUBE_CONFIG_PATHS"} {
		t.Setenv(env, "")
	}
	cfg, err := RestConfig(Attrs{Host: "https://cluster.example:6443"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QPS != SweepQPS || cfg.Burst != SweepBurst {
		t.Fatalf("RestConfig gave QPS %v, Burst %d; want %v, %d", cfg.QPS, cfg.Burst, float32(SweepQPS), SweepBurst)
	}
	kept := withRateLimits(&restclient.Config{QPS: 7, Burst: 9})
	if kept.QPS != 7 || kept.Burst != 9 {
		t.Fatalf("withRateLimits replaced a caller's QPS 7, Burst 9 with %v, %d", kept.QPS, kept.Burst)
	}
}
