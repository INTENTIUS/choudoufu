// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// This file is the Kubernetes admission rule (GitHub issue #1064, under
// #1016's ruling): a type whose schema carries Kubernetes object metadata
// is identified by that metadata's namespace and name, and needs no row of
// its own to say so.
//
// # Why a shape, not a row per type
//
// The provider's own identity schema names api_version and kind, which are
// constants the provider hardcodes per type and which appear nowhere in a
// configuration; [synthesizeTypeIdentity]'s identity-schema route therefore
// refuses every kubernetes_* type, correctly, and
// TestKubernetesAPIVersionAndKindHaveNoConfigSource pins that. What the
// provider documents as the import id is not that schema but
// NAMESPACE/NAME (or NAME for a cluster-scoped kind), read from the
// metadata block, which is the shape #326 ratified four rows for by hand.
// Seventy-three of hashicorp/kubernetes 3.2.1's eighty-two resource types
// carry the same block, so the four rows are a sample of a convention, and
// this file states the convention once. The four rows stay in the table as
// a check on it: metadata_test.go requires the rule to reproduce each of
// them exactly.
//
// # What the shape is
//
// Kubernetes ObjectMeta, as the provider renders it: a "metadata" nested
// block of list nesting with at most one item (declared by max_items, or
// undeclared the way a plugin-framework schema leaves it, see
// markers.UndeclaredSingleObjectMetadata),
// holding a settable "name",
// a computed "uid", a settable "labels" map, and - for a namespaced kind -
// a settable "namespace". The uid and labels are required by the
// predicate so that an unrelated provider's "metadata" block cannot match;
// nothing else in the tree has that combination.
//
// # What it deliberately refuses
//
//   - generateName: the server mints the name, so the join key is
//     unknowable before the create. That is the one shape that would put a
//     configuration address back on the object, and #1016 refuses it
//     rather than papering over it; internal/live/lint's RuleGenerateName
//     is the refusal.
//   - A missing namespace on a namespaced kind is refused rather than
//     defaulted to "default", exactly as the ratified kubernetes_config_map
//     row already does: a resolver that guessed would fabricate an identity
//     the configuration never stated.
//   - kubernetes_manifest, which takes a whole manifest as one dynamic
//     attribute and imports by a different mechanism, is not the shape and
//     stays refused as unadmitted-type.

// ObjectMetaShape reports whether block carries Kubernetes object metadata,
// and whether the kind it describes is namespaced. Read from the schema,
// never from a type-name list, for the same reason markers.Taggable is.
//
// The predicate and the entry it admits are the Kubernetes family's answer
// to [substrate.Substrate.SynthesizeIdentity] (GitHub issue #1586), which
// [synthesizeTypeIdentity] asks; this is the same predicate, kept here for
// the callers that ask the identity package.
//
// The entry is NAMESPACE/NAME for a namespaced kind, NAME otherwise, each
// component read from the metadata block the way [Component.Block] reads
// a singular nested block, under its own name as the identity attribute.
// It is the same entry the four ratified rows carry, so a reference from
// another resource to metadata[0].name resolves through
// [Resolution.attrParts] exactly as it does for those rows.
//
// The entry also claims "id" as an identity attribute (GitHub issue
// #1067's second estate): hashicorp/kubernetes sets every object-metadata
// resource's id to its own import id - the name for a cluster-scoped
// kind, NAMESPACE/NAME for a namespaced one - so a sibling reading
// kubernetes_namespace_v1.x.id (the shape grafana/quickpizza's root
// writes on every one of its twenty namespaced objects) is reading the
// parent's whole identity, which [resolver.parentPart] answers from the
// parent's own resolution rather than refusing as "Not an identity
// attribute". The four ratified rows carry no IdentityAttrs of their own
// and [schemaReproducesRow] disregards "id", so the rule still reproduces
// them.
func ObjectMetaShape(block *configschema.Block) (namespaced bool, ok bool) {
	return substrate.ObjectMetaShape(block)
}
