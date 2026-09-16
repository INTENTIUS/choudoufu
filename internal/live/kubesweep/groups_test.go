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
