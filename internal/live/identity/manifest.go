// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the third carrier shape for a Kubernetes object's identity
// (GitHub issue #1079, ruled 2026-09-12), beside the object-metadata block
// [ObjectMetaShape] admits: a resource whose whole object is one dynamic
// `manifest` argument - hashicorp/kubernetes's kubernetes_manifest, the
// type every custom resource is declared through. The natural key #1016
// ruled on is all there, authored in the configuration: apiVersion, kind,
// metadata.namespace and metadata.name are four keys of the manifest's
// object constructor, read through [Component.Path], and the provider's
// documented import id is built from exactly those four
// ("apiVersion=<string>,kind=<string>,[namespace=<string>,]name=<string>",
// the namespace segment omitted for a cluster-scoped kind).
//
// No identity attribute is claimed: the resource's own attributes are
// `manifest` (the desired object) and `object` (the live one, computed),
// neither of which is a flat value another resource could read as an
// identity, so a sibling reading kubernetes_manifest.x.object.metadata.name
// is refused today the way any non-identity reference is. The marker on a
// manifest object - the same tofu-estate label, into
// manifest.metadata.labels - and the sweep over every kind the cluster
// serves are the ruling's next two units; until the first lands a manifest
// object is bound by its natural key and carries no label, the way an
// untaggable AWS type is bound by its declaration.

// ManifestShape reports whether block is the kubernetes_manifest shape: a
// required dynamic `manifest` argument holding the whole object, a
// computed dynamic `object` the provider reads back, and no metadata block
// of its own (that would be [ObjectMetaShape]). Read from the schema, never
// from the type name, for the same reason the other two carriers are.
func ManifestShape(block *configschema.Block) bool {
	if block == nil {
		return false
	}
	if _, ok := block.BlockTypes["metadata"]; ok {
		return false
	}
	manifest, ok := block.Attributes["manifest"]
	if !ok || manifest == nil || !manifest.Required || manifest.Type != cty.DynamicPseudoType {
		return false
	}
	object, ok := block.Attributes["object"]
	if !ok || object == nil || !object.Computed || object.Type != cty.DynamicPseudoType {
		return false
	}
	return true
}

// ManifestImportSyntax is the provider's documented import id for a
// manifest object, the namespace segment present for a namespaced kind
// only.
const ManifestImportSyntax = "apiVersion=APIVERSION,kind=KIND,[namespace=NAMESPACE,]name=NAME"

// synthesizeManifestIdentity builds the entry for a type whose schema has
// [ManifestShape]: the four natural-key components read out of the
// manifest argument's own object constructor by [Component.Path], joined
// into the provider's import id. The namespace component is OmitIfAbsent,
// so a cluster-scoped manifest (no metadata.namespace key) renders the
// documented shorter form rather than failing.
func synthesizeManifestIdentity(typeName string, schema providers.Schema) (TypeIdentity, bool) {
	if !ManifestShape(schema.Block) {
		return TypeIdentity{}, false
	}
	key := func(path ...string) Component {
		return Component{Attrs: []string{"manifest"}, Path: path}
	}
	return TypeIdentity{
		Type:           typeName,
		NonAWSProvider: true,
		Components: []Component{
			{Literal: "apiVersion="},
			key("apiVersion"),
			{Literal: ",kind="},
			key("kind"),
			{Attrs: []string{"manifest"}, Path: []string{"metadata", "namespace"}, Literal: ",namespace=", OmitIfAbsent: true},
			{Literal: ",name="},
			key("metadata", "name"),
		},
		ImportSyntax: ManifestImportSyntax,
		Synthesized:  true,
		Admits:       AdmitSchema,
	}, true
}
