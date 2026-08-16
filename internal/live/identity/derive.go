// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"

	"github.com/intentius/choudoufu/internal/providers"
)

// DerivableType is one resource type whose identity the provider's own
// schemas describe completely in terms of that type's required
// configuration arguments. It is a self-admission candidate: no hand
// inference is needed to know what names an instance of it, because every
// attribute the provider requires for import is an argument the
// configuration must set, under the same name.
//
// Candidate, not conclusion. What the schemas establish is that the
// identity is supplied by configuration arguments; they say nothing about
// whether the value in those arguments is statically knowable. A route's
// route_table_id is a required argument and is usually a reference to a
// live rtb- ID, so aws_route is derivable in this sense and still resolves
// parent-derived per instance. That split is the right one: admission is a
// property of the type, and concrete-versus-parent-derived is a property
// of the instance, decided by [Resolve] from the argument's expression.
//
// Nor does derivability say a type belongs in the stateless subset. An
// aws_acm_certificate_validation is derivable by this rule and is excluded
// from the subset anyway, because it is a waiter rather than a resource
// (see live/SURVEY.md). Identity recoverability and subset membership
// are different questions.
type DerivableType struct {
	// Type is the resource type name.
	Type string

	// IdentityAttrs are the identity attributes that name one instance,
	// sorted: the ones the provider marks required for import, or - when it
	// requires nothing for import - the optional-for-import ones that are
	// not context. See [identityCandidates] for that split and why it is
	// the same question either way.
	IdentityAttrs []string

	// Context are the identity attributes the provider can fill in itself
	// rather than ones the configuration has to supply. In the AWS provider
	// these are account_id and region, which is why they are excluded from
	// the rule rather than counted against it; [isContextAttr] derives the
	// answer instead of naming them.
	Context []string

	// InTable is true when [DefaultTable] already covers this type, so a
	// caller can tell "the schemas back a row we already wrote by hand"
	// from "the schemas offer a row nobody has written".
	InTable bool

	// Admits says what backs the admission: [AdmitSchema] when the strict
	// rule settled it from the schemas alone, [AdmitConfigSignal] when the
	// schemas left it open and a configuration answered. The two are not
	// interchangeable - one is a property of the type and the other is a
	// reading of one configuration - so the evidence travels with the
	// verdict rather than being reconstructed by whoever reports it.
	Admits Admission

	// Naming is the per-instance evidence behind an [AdmitConfigSignal]
	// verdict, sorted by address, and empty for [AdmitSchema]. It is what
	// lets a report name the block that decided rather than asserting the
	// type.
	Naming []InstanceNaming
}

// Derivable reports every resource type in a provider's schemas whose
// identity is fully client-assigned by the two schemas alone, sorted by
// type name. See [DerivableType] for what the answer means and does not
// mean.
//
// The rule is deliberately the strict one: a type qualifies only when its
// identity schema names at least one attribute that identifies an instance
// (see [identityCandidates]) and every one of those attributes is the name
// of a *required* argument of the type.
// An optional argument does not qualify it, and the reason is specific
// rather than cautious. The AWS provider's legacy-SDK schemas mark
// aws_s3_bucket's bucket argument Optional+Computed (because bucket_prefix
// is the alternative) and mark aws_vpc's id attribute Optional+Computed
// too (because that is what the SDK does with id). A rule that accepted
// Optional+Computed arguments would therefore admit aws_vpc as
// client-named, which is exactly wrong: EC2 assigns that ID and no
// configuration argument determines it. The two cases are indistinguishable
// in the schema, so the strict rule stops at the ones that are provable.
// That is the boundary between what the schemas can settle on their own and
// what needs something else.
//
// The something else is a configuration, and [DerivableWith] is this
// function with it folded in: no aws_vpc block sets id and every
// aws_s3_bucket block that names a bucket sets bucket, which separates the
// two cases the schemas cannot. Use this function when there is no
// configuration to read - a survey of a provider's whole type set - and
// [DerivableWith] when there is one.
func Derivable(resourceTypes map[string]providers.Schema) []DerivableType {
	out := make([]DerivableType, 0, len(resourceTypes))
	for typeName, schema := range resourceTypes {
		if d, ok := derivable(resourceTypes, typeName, schema); ok {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// DerivableNew is [Derivable] restricted to the types [DefaultTable] does
// not already cover: the set a wiring batch would grow the table with, each
// row backed by the provider's own schema rather than by a reading of its
// documentation.
func DerivableNew(resourceTypes map[string]providers.Schema) []DerivableType {
	all := Derivable(resourceTypes)
	out := make([]DerivableType, 0, len(all))
	for _, d := range all {
		if !d.InTable {
			out = append(out, d)
		}
	}
	return out
}

func derivable(schemas map[string]providers.Schema, typeName string, schema providers.Schema) (DerivableType, bool) {
	if schema.IdentitySchema == nil || schema.Block == nil {
		return DerivableType{}, false
	}

	required, context := identityCandidates(schemas, typeName, schema)
	if len(required) == 0 {
		return DerivableType{}, false
	}

	for _, name := range required {
		arg, ok := schema.Block.Attributes[name]
		if !ok || !arg.Required {
			return DerivableType{}, false
		}
	}

	_, inTable := DefaultTable[typeName]
	return DerivableType{
		Type:          typeName,
		IdentityAttrs: required,
		Context:       context,
		InTable:       inTable,
		Admits:        AdmitSchema,
	}, true
}

// identityCandidates splits a type's identity schema into the attributes
// that name one instance of it and the attributes that are context - the
// account, the project, the region, whatever the provider fills in from its
// own ambient configuration. It is the one place either derivation rule
// decides what "the identity attributes" means, so the strict rule and the
// config-signal rule cannot disagree about it.
//
// An identity schema that requires something for import has already
// answered: the required set names the instance and the optional set is
// everything else, which [SynthesizeTypeIdentity] then puts to
// [onlyContext] to make sure the "everything else" really is ambient. That
// is the whole of today's behaviour and this returns it unchanged.
//
// An identity schema that requires *nothing* is the case this exists for.
// Reading it as "the provider will not say what names one" and stopping
// there is what the code did, and it is wrong twice over. It is wrong about
// singletons, which do look like this - an account-wide setting whose
// identity is nothing but account_id and region - but for those the answer
// falls out anyway: every optional attribute is context, the candidate set
// is empty, and both callers refuse exactly as before. And it is wrong
// about the far commoner shape, an identity that is a client-supplied name
// scoped by ambient context, which the provider marks entirely optional for
// import because the ambient half can be defaulted and the ID grammar puts
// them all in one string. google_workflows_workflow requires nothing and
// offers name, project and region; project and region are context by the
// same test aws_route53_record's set_identifier and aws_route's
// destination_cidr_block fail, and what is left is the name.
//
// So the candidates are the optional-for-import attributes that are not
// context, and the context is the rest. Nothing here admits anything: a
// candidate still has to be an argument this type's own block carries, and
// still has to be either required of every configuration ([derivable]) or
// observed on every instance in this one ([cohortAttrs] plus
// [ConfigSignal.NamingOfType]). Both bars are the ones a required-for-
// import attribute already has to clear, and they are what keeps an
// optional attribute some configuration omits from becoming a component
// that would send a lookup at the wrong live object.
//
// schemas is the whole provider schema set, needed because [isContextAttr]
// decides by corroboration across independently-authored types and cannot
// answer from one schema alone. A caller that has only the one type's
// schema therefore gets no context at all, every optional attribute counts
// as a candidate, and the type is refused rather than admitted on a
// half-read - which is the safe direction.
func identityCandidates(schemas map[string]providers.Schema, typeName string, schema providers.Schema) (candidates, context []string) {
	required, optional := identityAttrs(schema.IdentitySchema)
	if len(required) > 0 {
		return required, optional
	}
	for _, name := range optional {
		if isContextAttr(schemas, typeName, schema, name) {
			context = append(context, name)
			continue
		}
		candidates = append(candidates, name)
	}
	return candidates, context
}
