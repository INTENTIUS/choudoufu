// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"sync"

	"k8s.io/client-go/kubernetes/scheme"
)

// GitHub issue #1111: joining a served resource to a provider type by
// r.Kind alone lets a CRD whose spec.names.kind is spelled like a
// built-in's - Deployment, Service, Ingress, whatever - collide with that
// built-in's provider type. hashicorp/kubernetes' own identity schema
// hardcodes each type's apiVersion and kind as Go constants that name
// nowhere in the type's configuration schema (TestKubernetesAPIVersionAndKindHaveNoConfigSource
// in internal/live/identity proves that for kubernetes_config_map, and it
// is the same shape for every kubernetes_* type: 41 of hashicorp/kubernetes
// 3.2.1's 82 resource types carry an identity schema at all, and every one
// of them requires api_version/kind as constants with no schema-carried
// hint of the value), so there is no provider schema to read the group
// from here.
//
// What does carry it, and is not hand-maintained by this package, is
// k8s.io/client-go's own type registry: the same registry every generated
// typed clientset (and this repo's own transitive dependency on
// client-go) is built against. Every kind a real Kubernetes API server
// serves under core, apps, batch, networking.k8s.io,
// rbac.authorization.k8s.io, storage.k8s.io, autoscaling, policy,
// scheduling.k8s.io, certificates.k8s.io or admissionregistration.k8s.io -
// the groups GitHub issue #1111 names - is registered there under the
// same Kind and the same Group, because that registry is generated from
// the same Kubernetes API definitions the apiserver itself serves. A CRD
// that spells its kind like one of these is never registered there under
// its own group, so the join below correctly refuses it.
//
// apiextensions.k8s.io (the CustomResourceDefinition type itself) is not
// in this registry - k8s.io/client-go/kubernetes/scheme only wires up
// clientset-served groups, and apiextensions-apiserver's own generated
// clientset is not a dependency of this module - but no
// hashicorp/kubernetes resource type manages a CustomResourceDefinition
// object today (CRDs are declared through kubernetes_manifest, which is
// the manifest type, not a byKind entry), so no join ever needs that
// group. If a future provider version adds such a type, its kind's entry
// belongs in [builtinGroupOverrides] below, with the reason this comment
// gives.
//
// TestBuiltinGroupsMatchTheGroupsIssue1111Named is the guard: it pins the
// derived group for one representative kind per group GitHub issue #1111
// named, so a client-go upgrade that stopped registering one of them
// under its documented group would fail loudly here rather than
// silently re-opening the collision.

// builtinGroupOverrides is the guarded exception table for a kind whose
// group k8s.io/client-go/kubernetes/scheme does not carry: empty today,
// on purpose (see the package doc above). A kind added here without a
// scheme entry needs its own comment naming which built-in provider type
// it is for and why the scheme cannot answer.
var builtinGroupOverrides = map[string][]string{}

var (
	builtinKindGroupsOnce sync.Once
	builtinKindGroupsVal  map[string]map[string]bool
)

// builtinKindGroups is Kind -> the set of API groups the Kubernetes API
// itself registers that kind under, computed once from
// k8s.io/client-go/kubernetes/scheme's own type registry plus
// [builtinGroupOverrides].
func builtinKindGroups() map[string]map[string]bool {
	builtinKindGroupsOnce.Do(func() {
		out := map[string]map[string]bool{}
		for gvk := range scheme.Scheme.AllKnownTypes() {
			groups, ok := out[gvk.Kind]
			if !ok {
				groups = map[string]bool{}
				out[gvk.Kind] = groups
			}
			groups[gvk.Group] = true
		}
		for kind, groups := range builtinGroupOverrides {
			set, ok := out[kind]
			if !ok {
				set = map[string]bool{}
				out[kind] = set
			}
			for _, g := range groups {
				set[g] = true
			}
		}
		builtinKindGroupsVal = out
	})
	return builtinKindGroupsVal
}

// servesBuiltinKind reports whether the Kubernetes API itself registers
// kind under group - never whether the provider implements a resource
// type for it, which [KindTypes] already answers. A served resource whose
// kind matches a provider type's kind by name but whose own group fails
// this check is a different API's definition of the same spelling
// (GitHub issue #1111), and is never that provider type's object.
func servesBuiltinKind(kind, group string) bool {
	return builtinKindGroups()[kind][group]
}
