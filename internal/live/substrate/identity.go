// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is each family's answer to the admission question GitHub issue
// #1586 moved here: given a resource type the ratified table does not
// cover, how does its schema say one instance is identified?
//
// Before it, internal/live/identity's synthesizeTypeIdentity asked the two
// Kubernetes shapes by name ahead of the identity-schema route, and a third
// family (GCP and Azure admit by natural key, #1118's axis table) would
// have been a third hard-coded call. It now loops over [All] and asks
// [Substrate.SynthesizeIdentity]; the order of [All] is load-bearing,
// because the AWS answer is the catch-all (see [aws.SynthesizeIdentity]).
//
// The answer is a [SynthesizedIdentity], not an identity.TypeIdentity,
// because internal/live/identity imports this package and not the other
// way round. It carries exactly the fields a family's synthesis sets, and
// the identity package turns it into its own entry.

// SynthesizedIdentity is a family's reading of one type's identity from its
// schema.
type SynthesizedIdentity struct {
	// FromIdentitySchema: the provider's own resource identity schema
	// decides, through internal/live/identity's identity-schema route (which
	// reads the full schema map and the configuration signal, neither of
	// which a family sees). Every other field is empty when it is set.
	FromIdentitySchema bool

	// NonAWSProvider is identity.TypeIdentity's field of the same name.
	NonAWSProvider bool

	// Components, ImportSyntax and IdentityAttrs are the entry's, in
	// identity.TypeIdentity's terms.
	Components    []IdentityComponent
	ImportSyntax  string
	IdentityAttrs []string
}

// IdentityComponent is the subset of identity.Component a family's
// synthesis sets. Each field means what the identity package's field of the
// same name means, except SameNameIdentity, which stands for
// identity.SameNameIdentity in IdentityAttr.
type IdentityComponent struct {
	Literal          string
	Attrs            []string
	Block            string
	Path             []string
	OmitIfAbsent     bool
	SameNameIdentity bool
}

// SynthesizeIdentity asks every family in [All]'s order how typeName's
// schema identifies an instance, and the first to answer decides
// (internal/live/identity's synthesizeTypeIdentity). The answer is AWS's
// identity-schema route ([SynthesizedIdentity.FromIdentitySchema]) for any
// type no earlier family claims, which is why a family placed after AWS
// would never be asked (GitHub issue #1742).
func SynthesizeIdentity(typeName string, schema providers.Schema) (SynthesizedIdentity, bool) {
	for _, s := range All {
		if synth, ok := s.SynthesizeIdentity(typeName, schema); ok {
			return synth, true
		}
	}
	return SynthesizedIdentity{}, false
}

// SynthesizeIdentity is always the identity-schema route: AWS types are
// identified by the attributes the provider's resource identity schema
// names. It claims every type, which is why AWS is last in [All]: a family
// whose convention the identity schema cannot reach (Kubernetes, whose
// identity schema requires api_version and kind, constants no configuration
// carries) has to be asked first. It is also the route every family without
// a synthesis of its own falls through to, hashicorp/google among them,
// exactly as it did before #1586.
func (aws) SynthesizeIdentity(string, providers.Schema) (SynthesizedIdentity, bool) {
	return SynthesizedIdentity{FromIdentitySchema: true}, true
}

// SynthesizeIdentity is the Kubernetes natural key, read from one of the
// family's two carrier shapes: object metadata ([ObjectMetaShape], GitHub
// issue #1064) or a whole-object manifest ([markers.ManifestSurface],
// #1079); or, for a type that patches fields of an object it does not own,
// the patched object's key ([FieldGranularShape], #1191). None is keyed on
// the type name. See
// internal/live/identity's metadata.go and manifest.go for the rulings.
func (kubernetes) SynthesizeIdentity(_ string, schema providers.Schema) (SynthesizedIdentity, bool) {
	if namespaced, ok := ObjectMetaShape(schema.Block); ok {
		return objectMetaIdentity(namespaced), true
	}
	if markers.ManifestSurface(schema.Block) {
		return manifestIdentity(), true
	}
	// GitHub issue #1191: the field-granular shape, whose identity is the
	// object it patches. See [FieldGranularShape].
	if namespaced, ok := FieldGranularShape(schema.Block); ok {
		return fieldGranularIdentity(schema.Block, namespaced), true
	}
	return SynthesizedIdentity{}, false
}

// ObjectMetaShape reports whether block carries Kubernetes object metadata,
// and whether the kind it describes is namespaced. Read from the schema,
// never from a type-name list, for the same reason markers.Taggable is.
//
// It is the shape internal/live/identity's metadata.go documents: a
// "metadata" nested block of list nesting with at most one item (declared
// by max_items, or by [markers.UndeclaredSingleObjectMetadata] where a
// plugin-framework schema cannot declare it), holding a
// settable "name", a computed "uid", a settable "labels" map, and - for a
// namespaced kind - a settable "namespace".
func ObjectMetaShape(block *configschema.Block) (namespaced bool, ok bool) {
	if block == nil {
		return false, false
	}
	nested, has := block.BlockTypes["metadata"]
	if !has || nested == nil {
		return false, false
	}
	if nested.Nesting != configschema.NestingList || (nested.MaxItems != 1 && !markers.UndeclaredSingleObjectMetadata(nested)) {
		return false, false
	}
	attrs := nested.Block.Attributes
	name, hasName := attrs["name"]
	if !hasName || name == nil || name.Type != cty.String || (!name.Optional && !name.Required) {
		return false, false
	}
	uid, hasUID := attrs["uid"]
	if !hasUID || uid == nil || !uid.Computed || uid.Type != cty.String {
		return false, false
	}
	labels, hasLabels := attrs["labels"]
	if !hasLabels || labels == nil || !labels.Type.IsMapType() {
		return false, false
	}
	ns, hasNS := attrs["namespace"]
	namespaced = hasNS && ns != nil && ns.Type == cty.String && (ns.Optional || ns.Required)
	return namespaced, true
}

// ManifestImportSyntax is the provider's documented import id for a
// manifest object, the namespace segment present for a namespaced kind
// only.
const ManifestImportSyntax = "apiVersion=APIVERSION,kind=KIND,[namespace=NAMESPACE,]name=NAME"

// objectMetaIdentity is NAMESPACE/NAME for a namespaced kind, NAME
// otherwise, each read from the metadata block under its own name as the
// identity attribute, with "id" claimed too (GitHub issue #1067).
func objectMetaIdentity(namespaced bool) SynthesizedIdentity {
	name := IdentityComponent{Attrs: []string{"name"}, Block: "metadata", SameNameIdentity: true}
	if !namespaced {
		return SynthesizedIdentity{
			NonAWSProvider: true,
			Components:     []IdentityComponent{name},
			ImportSyntax:   "NAME",
			IdentityAttrs:  []string{"id"},
		}
	}
	return SynthesizedIdentity{
		NonAWSProvider: true,
		Components: []IdentityComponent{
			{Attrs: []string{"namespace"}, Block: "metadata", SameNameIdentity: true},
			{Literal: "/"},
			name,
		},
		ImportSyntax:  "NAMESPACE/NAME",
		IdentityAttrs: []string{"id"},
	}
}

// manifestIdentity is the four natural-key components read out of the
// manifest argument's own object constructor, joined into the provider's
// import id, the namespace segment omitted when the manifest has none.
func manifestIdentity() SynthesizedIdentity {
	key := func(path ...string) IdentityComponent {
		return IdentityComponent{Attrs: []string{"manifest"}, Path: path}
	}
	return SynthesizedIdentity{
		NonAWSProvider: true,
		Components: []IdentityComponent{
			{Literal: "apiVersion="},
			key("apiVersion"),
			{Literal: ",kind="},
			key("kind"),
			{Attrs: []string{"manifest"}, Path: []string{"metadata", "namespace"}, Literal: ",namespace=", OmitIfAbsent: true},
			{Literal: ",name="},
			key("metadata", "name"),
		},
		ImportSyntax: ManifestImportSyntax,
	}
}

// ---- GitHub issue #1191: the field-granular shape ----

// The schema attributes that make a type field-granular (GitHub issue
// #1191): a top-level server-side-apply field manager name and the force
// flag beside it. hashicorp/kubernetes 3.2.1 serves exactly six types with
// both - kubernetes_labels, kubernetes_annotations, kubernetes_env,
// kubernetes_config_map_v1_data, kubernetes_secret_v1_data and
// kubernetes_node_taint - and kubernetes_manifest, the seventh type that
// can name a field manager, names it in a nested block instead, so it is
// not this shape.
const (
	FieldManagerAttr = "field_manager"
	FieldForceAttr   = "force"
)

// FieldGranularShape reports whether block is the field-granular
// Kubernetes shape (GitHub issue #1191, ruled 2026-10-03): a resource that
// writes some fields of an object it does not own, under a server-side-apply
// field manager it names itself. Read from the schema, never from a
// type-name list, for the same reason [ObjectMetaShape] is:
//
//   - a top-level optional string [FieldManagerAttr] and a top-level
//     optional bool [FieldForceAttr];
//   - a "metadata" nested list block of at most one item with a settable
//     string "name", and WITHOUT the computed uid and the labels map
//     [ObjectMetaShape] requires - the block names the patched object, it
//     is not this resource's own object metadata.
//
// The predicate and [ObjectMetaShape] can therefore never both answer true
// for one schema.
//
// What the shape owns is fields, not an object, so its ownership marker is
// not a label: it is the field manager every write is made under,
// [markers.FieldManagerFor]'s "choudoufu:<estate>", and the patched
// object's own estate label (if it has one) is irrelevant to it.
func FieldGranularShape(block *configschema.Block) (namespaced bool, ok bool) {
	if block == nil {
		return false, false
	}
	fm, hasFM := block.Attributes[FieldManagerAttr]
	if !hasFM || fm == nil || fm.Type != cty.String || !fm.Optional {
		return false, false
	}
	force, hasForce := block.Attributes[FieldForceAttr]
	if !hasForce || force == nil || force.Type != cty.Bool || !force.Optional {
		return false, false
	}
	nested, has := block.BlockTypes["metadata"]
	if !has || nested == nil || nested.Nesting != configschema.NestingList || nested.MaxItems != 1 {
		return false, false
	}
	attrs := nested.Block.Attributes
	name, hasName := attrs["name"]
	if !hasName || name == nil || name.Type != cty.String || (!name.Optional && !name.Required) {
		return false, false
	}
	if _, hasUID := attrs["uid"]; hasUID {
		return false, false
	}
	if _, hasLabels := attrs["labels"]; hasLabels {
		return false, false
	}
	ns, hasNS := attrs["namespace"]
	namespaced = hasNS && ns != nil && ns.Type == cty.String && (ns.Optional || ns.Required)
	return namespaced, true
}

// FieldGranularNamesKind reports whether a field-granular schema names the
// patched object's apiVersion and kind in its own configuration (the
// top-level required api_version and kind of kubernetes_labels,
// kubernetes_annotations and kubernetes_env). The other three types patch
// one fixed kind, which the provider hardcodes.
func FieldGranularNamesKind(block *configschema.Block) bool {
	if block == nil {
		return false
	}
	for _, name := range []string{"api_version", "kind"} {
		a, ok := block.Attributes[name]
		if !ok || a == nil || a.Type != cty.String || !a.Required {
			return false
		}
	}
	return true
}

// FieldGranularImportSyntax is the identity string a field-granular type
// that names its kind renders: the shape hashicorp/kubernetes' own
// kubernetes_env Read parses its id with (keys in any order; measured
// against 3.2.1 on 2026-10-03, where an id without apiVersion, kind and
// name is refused with "ID must contain apiVersion, kind, and name").
const FieldGranularImportSyntax = "apiVersion=APIVERSION,kind=KIND,[namespace=NAMESPACE,]name=NAME"

// fieldGranularIdentity is the patched object's natural key, read from the
// block's own configuration. It identifies the OBJECT, not the fields:
// every field-granular block in one estate writes under the one field
// manager "choudoufu:<estate>", and server-side apply drops a manager's
// fields that its next apply leaves out, so two blocks of one estate on one
// object would erase each other's writes. One block per object per estate
// is therefore the identity, and a second is refused as the same identity.
//
//   - names its kind: apiVersion, kind, the namespace when declared (a
//     cluster-scoped object has none), and the name, in
//     [FieldGranularImportSyntax].
//   - a fixed namespaced kind (ConfigMap, Secret data): NAMESPACE/NAME,
//     the namespace required, as it is for the object-metadata shape.
//   - a fixed cluster-scoped kind (a node's taints): NAME.
//
// No identity attribute is claimed: these resources' only attributes are
// the fields they write.
func fieldGranularIdentity(block *configschema.Block, namespaced bool) SynthesizedIdentity {
	name := IdentityComponent{Attrs: []string{"name"}, Block: "metadata", SameNameIdentity: true}
	if FieldGranularNamesKind(block) {
		return SynthesizedIdentity{
			NonAWSProvider: true,
			Components: []IdentityComponent{
				{Literal: "apiVersion="},
				{Attrs: []string{"api_version"}, SameNameIdentity: true},
				{Literal: ",kind="},
				{Attrs: []string{"kind"}, SameNameIdentity: true},
				{Attrs: []string{"namespace"}, Block: "metadata", Literal: ",namespace=", OmitIfAbsent: true, SameNameIdentity: true},
				{Literal: ",name="},
				name,
			},
			ImportSyntax: FieldGranularImportSyntax,
		}
	}
	if !namespaced {
		return SynthesizedIdentity{
			NonAWSProvider: true,
			Components:     []IdentityComponent{name},
			ImportSyntax:   "NAME",
		}
	}
	return SynthesizedIdentity{
		NonAWSProvider: true,
		Components: []IdentityComponent{
			{Attrs: []string{"namespace"}, Block: "metadata", SameNameIdentity: true},
			{Literal: "/"},
			name,
		},
		ImportSyntax: "NAMESPACE/NAME",
	}
}
