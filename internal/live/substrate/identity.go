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
// #1079). Neither is keyed on the type name. See
// internal/live/identity's metadata.go and manifest.go for the rulings.
func (kubernetes) SynthesizeIdentity(_ string, schema providers.Schema) (SynthesizedIdentity, bool) {
	if namespaced, ok := ObjectMetaShape(schema.Block); ok {
		return objectMetaIdentity(namespaced), true
	}
	if markers.ManifestSurface(schema.Block) {
		return manifestIdentity(), true
	}
	return SynthesizedIdentity{}, false
}

// ObjectMetaShape reports whether block carries Kubernetes object metadata,
// and whether the kind it describes is namespaced. Read from the schema,
// never from a type-name list, for the same reason markers.Taggable is.
//
// It is the shape internal/live/identity's metadata.go documents: a
// "metadata" nested block of list nesting with at most one item, holding a
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
	if nested.Nesting != configschema.NestingList || nested.MaxItems != 1 {
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
