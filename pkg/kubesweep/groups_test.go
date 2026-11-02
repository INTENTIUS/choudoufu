// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import "testing"

// TestBuiltinGroupsMatchTheGroupsIssue1111Named is the derivation guard
// for [builtinKindGroups] (GitHub issue #1111): one representative kind
// per group the issue names, pinned to that group. builtinKindGroups is
// computed from k8s.io/client-go/kubernetes/scheme rather than hand-typed
// here, so this test is what would catch a client-go upgrade that quietly
// stopped registering one of these kinds under its documented group -
// the drift this package cannot otherwise see coming.
func TestBuiltinGroupsMatchTheGroupsIssue1111Named(t *testing.T) {
	for _, tc := range []struct {
		kind, group string
	}{
		{"ConfigMap", ""}, // core
		{"Deployment", "apps"},
		{"Job", "batch"},
		{"NetworkPolicy", "networking.k8s.io"},
		{"ClusterRoleBinding", "rbac.authorization.k8s.io"},
		{"StorageClass", "storage.k8s.io"},
		{"HorizontalPodAutoscaler", "autoscaling"},
		{"PodDisruptionBudget", "policy"},
		{"PriorityClass", "scheduling.k8s.io"},
		{"CertificateSigningRequest", "certificates.k8s.io"},
		{"ValidatingWebhookConfiguration", "admissionregistration.k8s.io"},
	} {
		if !servesBuiltinKind(tc.kind, tc.group) {
			t.Errorf("servesBuiltinKind(%q, %q) = false, want true: builtinKindGroups() = %v", tc.kind, tc.group, builtinKindGroups()[tc.kind])
		}
	}

	// The collision GitHub issue #1111 reports: a CRD's own group never
	// serves a built-in's kind.
	if servesBuiltinKind("Deployment", "example.com") {
		t.Error("servesBuiltinKind(Deployment, example.com) = true, want false: this is the exact collision #1111 reports")
	}

	// apiextensions.k8s.io is deliberately absent (see groups.go's
	// package doc): no hashicorp/kubernetes resource type manages a
	// CustomResourceDefinition object today, so nothing joins on it.
	if servesBuiltinKind("CustomResourceDefinition", "apiextensions.k8s.io") {
		t.Error("CustomResourceDefinition/apiextensions.k8s.io is now derived; groups.go's package doc claiming otherwise needs updating, and builtinGroupOverrides may no longer need it explained away")
	}
}

// TestAPIServiceJoinsItsServedResource is GitHub issue #1880's guard: both
// provider types for an APIService name the kind the server lists
// (APIService, not the join's "ApiService"), and that kind is accepted
// under apiregistration.k8s.io, a group k8s.io/client-go's scheme does not
// register. Without either half the sweep files kubernetes_api_service(_v1)
// as unserved and a declared APIService as an undeclared object.
func TestAPIServiceJoinsItsServedResource(t *testing.T) {
	for _, typeName := range []string{"kubernetes_api_service", "kubernetes_api_service_v1"} {
		kind, _, ok := KindOfType(typeName)
		if !ok || kind != "APIService" {
			t.Errorf("KindOfType(%q) = %q, %v; want APIService, true", typeName, kind, ok)
		}
	}
	if !servesBuiltinKind("APIService", "apiregistration.k8s.io") {
		t.Error("servesBuiltinKind(APIService, apiregistration.k8s.io) = false, want true")
	}
	if servesBuiltinKind("APIService", "example.com") {
		t.Error("servesBuiltinKind(APIService, example.com) = true, want false: a CRD spelled APIService in its own group is not the built-in")
	}
	kt := KindTypes([]string{"kubernetes_api_service", "kubernetes_api_service_v1"})
	if got, ok := TypeFor(kt, map[string]bool{"kubernetes_api_service": true}, "APIService"); !ok || got != "kubernetes_api_service" {
		t.Errorf("TypeFor(APIService) with the deprecated type declared = %q, %v; want kubernetes_api_service", got, ok)
	}
}
