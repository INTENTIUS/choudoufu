// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package kubesweep is the Kubernetes estate sweep (GitHub issue #1065,
// under #1016's ruling): which live objects carry this estate's label, so
// that one whose block the configuration no longer declares can be
// proposed for removal, exactly as the AWS tagging sweep does for a tagged
// resource.
//
// # What a sweep is here
//
// Kubernetes has no cross-kind label-filtered list, so the AWS shape - one
// GetResources call over the whole account - does not survive. What does
// survive is nearly as good: every kind supports a cluster-wide,
// label-selected list (GET /api/v1/configmaps?labelSelector=..., across
// every namespace at once), which returns only this estate's objects and
// never grows with the cluster. A sweep is therefore one list per kind,
// not one per kind per namespace as #1016 first estimated, plus the two
// discovery calls (/api, /apis) that say which kinds the cluster serves.
//
// # Which kinds
//
// Every kind the cluster serves with list and delete verbs. A kind the
// provider has a resource type for is joined by kind name:
// hashicorp/kubernetes names its types kubernetes_<snake_kind> with an
// optional API-version suffix (kubernetes_config_map and
// kubernetes_config_map_v1 both manage a ConfigMap; kubernetes_ingress
// managed the retired extensions/v1beta1 Ingress and kubernetes_ingress_v1
// the current one), so [KindOfType] recovers the kind from the type name
// and [TypeFor] picks, for a listed object, the type the configuration
// declares for that kind when it declares one, else the versioned name
// when there is one (it targets the API the cluster serves today), else
// the plain one. Every other served kind - every CRD, and the built-in
// kinds the provider never gave a type - is listed under the manifest
// type (GitHub issue #1079's third unit): the provider's one type that
// manages any kind the cluster serves, admitted by schema shape
// (markers.ManifestSurface) and named to [Sweeper.Kinds] by the caller
// that read the schema, so that no type name is spelled out here.
// kubernetes_manifest imports by apiVersion, kind, namespace and name
// ([ManifestImportID]) and destroys through the live object it read, so
// an orphan of such a kind can be acted on exactly as one of a built-in
// kind can. Before #1079 those kinds were not listed at all, on the
// reasoning that what cannot be destroyed through the provider should
// not be proposed; once kubernetes_manifest is in the type universe that
// reasoning points the other way.
//
// # What is excluded, and why it is the whole safety of this
//
// A controller copies template labels: a Deployment's pod-template labels
// reach its ReplicaSets and Pods, a StatefulSet's volumeClaimTemplate
// labels reach its PVCs. A tofu-estate label an author put in a template
// therefore lands on objects nobody declared, and every one of them would
// be an orphan and a delete candidate - a wrong marker nobody wrote, which
// HANDOFF's safety rule names as worse than a refusal. The test that keeps
// them out is the object's own metadata.ownerReferences: a non-empty one
// means a controller owns it, and [Client.List] never returns such an
// object. The server-created singletons (the default ServiceAccount,
// kube-root-ca.crt) carry no estate label and are never selected.
package kubesweep

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// TypePrefix is what every hashicorp/kubernetes resource type name begins
// with.
const TypePrefix = "kubernetes_"

// versionSuffix matches the API-version suffix some type names carry:
// _v1, _v1beta1, _v2, _v2beta2.
var versionSuffix = regexp.MustCompile(`_v[0-9]+(?:(?:alpha|beta)[0-9]+)?$`)

// KindOfType recovers the Kubernetes kind a provider resource type manages
// from its name: kubernetes_config_map_v1 is ConfigMap, versioned true.
// A name outside the provider's prefix reports ok false.
func KindOfType(typeName string) (kind string, versioned bool, ok bool) {
	if !strings.HasPrefix(typeName, TypePrefix) {
		return "", false, false
	}
	rest := strings.TrimPrefix(typeName, TypePrefix)
	if loc := versionSuffix.FindStringIndex(rest); loc != nil {
		versioned = true
		rest = rest[:loc[0]]
	}
	if rest == "" {
		return "", false, false
	}
	var b strings.Builder
	for _, part := range strings.Split(rest, "_") {
		if part == "" {
			continue
		}
		r := []rune(part)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String(), versioned, true
}

// KindTypes groups provider resource type names by the kind they manage:
// ConfigMap -> [kubernetes_config_map, kubernetes_config_map_v1]. Names
// outside the provider's prefix are ignored.
func KindTypes(typeNames []string) map[string][]string {
	out := map[string][]string{}
	for _, t := range typeNames {
		kind, _, ok := KindOfType(t)
		if !ok {
			continue
		}
		out[kind] = append(out[kind], t)
	}
	for kind := range out {
		sort.Strings(out[kind])
	}
	return out
}

// TypeFor picks the provider type a listed object of kind is filed under:
// the type the configuration declares for that kind when it declares
// exactly one, else the versioned name when the kind has one, else the
// plain name. ok is false for a kind the provider has no type for.
func TypeFor(kindTypes map[string][]string, declared map[string]bool, kind string) (string, bool) {
	candidates := kindTypes[kind]
	if len(candidates) == 0 {
		return "", false
	}
	var declaredHere []string
	for _, t := range candidates {
		if declared[t] {
			declaredHere = append(declaredHere, t)
		}
	}
	if len(declaredHere) == 1 {
		return declaredHere[0], true
	}
	for _, t := range candidates {
		if _, versioned, _ := KindOfType(t); versioned {
			return t, true
		}
	}
	return candidates[0], true
}

// OrphanResourceName is the resource name an undeclared object is planned
// under, since an estate-only label carries no configuration address:
// orphan_<namespace>_<name>, or orphan_<name> for a cluster-scoped kind,
// with every character outside a resource name's grammar folded to "_".
// Namespaces and names are DNS labels, so only "." can need folding.
func OrphanResourceName(namespace, name string) string {
	s := "orphan_" + name
	if namespace != "" {
		s = "orphan_" + namespace + "_" + name
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// ManifestOrphanResourceName is [OrphanResourceName] for an object listed
// under the manifest type: orphan_<kind>_<namespace>_<name>, the kind
// folded to lower case, because that type files every kind under one name
// and two kinds can each have an object of the same namespace and name.
func ManifestOrphanResourceName(kind, namespace, name string) string {
	rest := strings.TrimPrefix(OrphanResourceName(namespace, name), "orphan_")
	return OrphanResourceName("", strings.ToLower(kind)) + "_" + rest
}

// ManifestImportID renders the provider's documented import id for a
// kubernetes_manifest object, the namespace segment present for a
// namespaced kind only: the same string internal/live/identity renders
// for a declared block, so a listed object and a resolution meet on it.
func ManifestImportID(apiVersion, kind, namespace, name string) string {
	if namespace == "" {
		return "apiVersion=" + apiVersion + ",kind=" + kind + ",name=" + name
	}
	return "apiVersion=" + apiVersion + ",kind=" + kind + ",namespace=" + namespace + ",name=" + name
}

// ParseManifestImportID reads a [ManifestImportID] back. ok is false for
// anything that is not one, including a value with the four keys in
// another order (the provider's own parser takes them in any order, but
// nothing here writes them in any other).
func ParseManifestImportID(id string) (apiVersion, kind, namespace, name string, ok bool) {
	fields := map[string]string{}
	for _, part := range strings.Split(id, ",") {
		k, v, found := strings.Cut(part, "=")
		if !found || k == "" || v == "" {
			return "", "", "", "", false
		}
		if _, dup := fields[k]; dup {
			return "", "", "", "", false
		}
		switch k {
		case "apiVersion", "kind", "namespace", "name":
		default:
			return "", "", "", "", false
		}
		fields[k] = v
	}
	apiVersion, kind, namespace, name = fields["apiVersion"], fields["kind"], fields["namespace"], fields["name"]
	if apiVersion == "" || kind == "" || name == "" {
		return "", "", "", "", false
	}
	return apiVersion, kind, namespace, name, true
}
