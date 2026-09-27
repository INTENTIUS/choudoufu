// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"fmt"
	"sort"
	"strings"
)

// Controller-held resources (GitHub issue #1606, ruled on #1604
// 2026-09-26): a cloud resource an in-cluster controller made from an owned
// object is controller-held by default. It is never swept as an orphan and
// never offered for adoption, whatever else it carries, and it is reported
// with the object that made it when its tags name that object. There is no
// opt-in stamp: #1105's objection (a third party can strip it) applies to a
// controller's tags as much as to anything this estate could write.
//
// ControllerTagKeys is the one list of the tag keys that say so. Each entry
// names the controller and the source the key was read from, measured
// 2026-09-26, rather than a key anyone remembered. Both controllers write
// these through their own tag reconciliation, so a key here on a live AWS
// resource is the controller's claim, not a label an operator chose.
//
// What was measured and left out, so nobody adds it back on a guess:
//
//   - crossplane.io/external-name is a Kubernetes ANNOTATION on the managed
//     resource (crossplane-runtime pkg/meta/meta.go,
//     AnnotationKeyExternalName). It names the cloud resource from the
//     cluster side and is never written to the cloud, so a sweep reading
//     AWS tags cannot see it.
//   - ACK's %K8S_RESOURCE_NAME% and %K8S_RESOURCE_KIND% expansions
//     (runtime pkg/tags/tag_format.go) exist, but no default tag uses them;
//     an operator who wants them picks the key, so there is no fixed key to
//     match. ACK's default chart also writes app.kubernetes.io/managed-by
//     and kro.run/kro-version, but only when the CR carries the label they
//     copy (runtime pkg/runtime/tags.go skips an empty expansion), and
//     managed-by is not ACK's own claim.
var ControllerTagKeys = []ControllerTagKey{
	// ACK: aws-controllers-k8s/runtime pkg/config/config.go,
	// defaultResourceTags, and every service controller's
	// helm/values.yaml resourceTags (checked on s3-controller). The
	// value is "<service alias>-<controller version>", e.g. s3-v1.0.14.
	// The runtime's own FilterSystemTags treats every
	// "services.k8s.aws/" key as controller-injected
	// (pkg/types/aws_resource_manager.go).
	{Key: "services.k8s.aws/controller-version", Controller: ControllerACK, Names: "controller"},
	// ACK: same source; the value is the CR's Kubernetes namespace.
	{Key: "services.k8s.aws/namespace", Controller: ControllerACK, Names: "namespace"},

	// Crossplane: crossplane-runtime pkg/resource/resource.go,
	// ExternalResourceTagKey* and GetExternalTags; upjet pkg/config/
	// resource.go's Tagger copies kind, name and providerconfig into
	// spec.forProvider.tags for every upjet-generated AWS provider
	// (provider-upjet-aws). Kind is the managed resource's lower-cased
	// group-kind, e.g. bucket.s3.aws.upbound.io.
	{Key: "crossplane-kind", Controller: ControllerCrossplane, Names: "kind"},
	{Key: "crossplane-name", Controller: ControllerCrossplane, Names: "name"},
	{Key: "crossplane-namespace", Controller: ControllerCrossplane, Names: "namespace"},
	{Key: "crossplane-providerconfig", Controller: ControllerCrossplane, Names: "providerconfig"},
	{Key: "crossplane-providerconfig-kind", Controller: ControllerCrossplane, Names: "providerconfig-kind"},
}

// Controller names an in-cluster controller that makes cloud resources.
type Controller string

const (
	ControllerACK        Controller = "ACK"
	ControllerCrossplane Controller = "Crossplane"
)

// ControllerTagKey is one entry of [ControllerTagKeys]: a tag key, the
// controller that writes it, and which part of the owning object its value
// names.
type ControllerTagKey struct {
	Key        string
	Controller Controller
	Names      string
}

// ControllerHold is what a resource's tags say about the controller that
// made it.
type ControllerHold struct {
	// Controller is the controller the tags name.
	Controller Controller

	// Tags are the controller tags the resource carries, by key, and
	// nothing else.
	Tags map[string]string
}

// ControllerHeld reports whether tags carry any of [ControllerTagKeys]. A
// key present with an empty value still counts: the controller wrote the
// key, and a value is what it names, not whether it holds the resource.
//
// When tags carry keys of more than one controller, the first controller
// in [ControllerTagKeys] order is the one reported and every carried key
// is kept, so the description still shows the other's.
func ControllerHeld(tags map[string]string) (ControllerHold, bool) {
	var hold ControllerHold
	for _, k := range ControllerTagKeys {
		v, ok := tags[k.Key]
		if !ok {
			continue
		}
		if hold.Tags == nil {
			hold.Controller = k.Controller
			hold.Tags = map[string]string{}
		}
		hold.Tags[k.Key] = v
	}
	return hold, hold.Tags != nil
}

// Describe names the owning object as far as the tags identify it, in one
// line an operator can act on:
//
//	ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a
//	Crossplane managed resource bucket.s3.aws.upbound.io "assets" (providerconfig default)
func (h ControllerHold) Describe() string {
	switch h.Controller {
	case ControllerACK:
		version := h.Tags["services.k8s.aws/controller-version"]
		s := "ACK controller"
		if svc, _, ok := strings.Cut(version, "-"); ok && svc != "" {
			s = fmt.Sprintf("ACK %s controller (%s)", svc, version)
		} else if version != "" {
			s = fmt.Sprintf("ACK controller (%s)", version)
		}
		if ns := h.Tags["services.k8s.aws/namespace"]; ns != "" {
			return s + ", custom resource in namespace " + ns
		}
		return s + ", custom resource not named by its tags"
	case ControllerCrossplane:
		s := "Crossplane managed resource"
		if kind := h.Tags["crossplane-kind"]; kind != "" {
			s += " " + kind
		}
		if name := h.Tags["crossplane-name"]; name != "" {
			if ns := h.Tags["crossplane-namespace"]; ns != "" {
				name = ns + "/" + name
			}
			s += fmt.Sprintf(" %q", name)
		}
		if pc := h.Tags["crossplane-providerconfig"]; pc != "" {
			s += " (providerconfig " + pc + ")"
		}
		return s
	}
	keys := make([]string, 0, len(h.Tags))
	for k := range h.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return string(h.Controller) + " (" + strings.Join(keys, ", ") + ")"
}
