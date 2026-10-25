// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import "sort"

// This file is the provider identity-schema reading: what a provider's own
// schemas say about which of a resource type's identity attributes name one
// instance and which are context the provider fills in itself. It reads the
// schemas through [TypeSchema] and [SchemaSet] rather than through
// OpenTofu's configschema types, so that a caller with no access to this
// module's internal packages (plunge, reading a bridged Terraform
// provider's schemas) can ask the same questions and get the same answers.
// choudoufu's own resolver adapts its provider schemas to these interfaces
// in internal/live/identity.

// TypeSchema is one managed resource type's two provider schemas - the
// resource schema and the resource identity schema - as far as this package
// reads them.
type TypeSchema interface {
	// HasIdentity reports whether the provider serves an identity schema
	// for the type.
	HasIdentity() bool

	// EachIdentityAttribute calls fn once for every attribute of the
	// identity schema, with whether the provider requires it for import.
	// The order is unspecified. It calls nothing when HasIdentity is false.
	EachIdentityAttribute(fn func(name string, requiredForImport bool))

	// HasResource reports whether the provider serves a resource schema for
	// the type.
	HasResource() bool

	// Argument returns the resource schema's top-level attribute called
	// name, and false when there is none (or no resource schema at all).
	Argument(name string) (Argument, bool)

	// NestedAttribute reports whether the resource schema has a nested
	// block, or an attribute of a nested type, called container, and that
	// nested shape has an attribute called leaf.
	NestedAttribute(container, leaf string) bool
}

// Argument is what this package reads of one resource schema attribute.
type Argument struct {
	Required bool
	Optional bool
	Computed bool
}

// SchemaSet is every managed resource type one provider serves schemas
// for. Some rules here decide by corroboration across independently
// authored types (see [IsContextAttr]), which is why they take the whole
// set and not one type's schemas.
type SchemaSet interface {
	// Each calls fn for every type in the set, in unspecified order, until
	// fn returns false.
	Each(fn func(typeName string, schema TypeSchema) bool)
}

// Schema is a plain [TypeSchema] for a caller that builds the schemas
// itself rather than adapting a provider's.
type Schema struct {
	// Identity maps each identity attribute to whether the provider
	// requires it for import. Nil means the provider serves no identity
	// schema for the type; an empty, non-nil map is an identity schema
	// with no attributes.
	Identity map[string]bool

	// Arguments are the resource schema's top-level attributes. Nil means
	// the provider serves no resource schema.
	Arguments map[string]Argument

	// Nested maps each nested block, and each attribute of a nested type,
	// to the names of the attributes it holds.
	Nested map[string][]string
}

var _ TypeSchema = Schema{}

// HasIdentity implements [TypeSchema].
func (s Schema) HasIdentity() bool { return s.Identity != nil }

// EachIdentityAttribute implements [TypeSchema].
func (s Schema) EachIdentityAttribute(fn func(name string, requiredForImport bool)) {
	for name, required := range s.Identity {
		fn(name, required)
	}
}

// HasResource implements [TypeSchema].
func (s Schema) HasResource() bool { return s.Arguments != nil }

// Argument implements [TypeSchema].
func (s Schema) Argument(name string) (Argument, bool) {
	a, ok := s.Arguments[name]
	return a, ok
}

// NestedAttribute implements [TypeSchema].
func (s Schema) NestedAttribute(container, leaf string) bool {
	for _, name := range s.Nested[container] {
		if name == leaf {
			return true
		}
	}
	return false
}

// Schemas is a plain [SchemaSet], keyed by resource type name.
type Schemas map[string]Schema

var _ SchemaSet = Schemas(nil)

// Each implements [SchemaSet].
func (s Schemas) Each(fn func(typeName string, schema TypeSchema) bool) {
	for name, schema := range s {
		if !fn(name, schema) {
			return
		}
	}
}

// IdentityAttrs splits a type's identity schema into the attributes the
// provider requires for import and the ones it accepts optionally, each
// sorted. Both are nil when the type has no identity schema.
func IdentityAttrs(schema TypeSchema) (required, optional []string) {
	return identityAttrs(schema)
}

// IdentityCandidates splits a type's identity schema into the attributes
// that name one instance of it and the attributes that are context: the
// account, the project, the region, whatever the provider fills in from its
// own ambient configuration. schemas is the provider's whole set, typeName
// is schema's own key in it.
func IdentityCandidates(schemas SchemaSet, typeName string, schema TypeSchema) (candidates, context []string) {
	return identityCandidates(schemas, typeName, schema)
}

// IsContextAttr reports whether one optional-for-import identity attribute
// of typeName is context the provider fills in itself, rather than
// something a configuration supplies to tell one instance from a sibling.
// The rule, and why it is derived from the schemas rather than from a list
// of names, is on isContextAttr in schema.go.
func IsContextAttr(schemas SchemaSet, typeName string, schema TypeSchema, name string) bool {
	return isContextAttr(schemas, typeName, schema, name)
}

// NotContext is the names, in the order given, that [IsContextAttr] says are
// not context.
func NotContext(schemas SchemaSet, typeName string, schema TypeSchema, names []string) []string {
	return notContext(schemas, typeName, schema, names)
}

// identityAttrs splits an identity schema's attributes into the ones the
// provider requires for import and the ones it accepts optionally, each
// sorted. The optional ones are context the provider can supply itself
// (account_id, region) or alternatives among which it needs no particular
// one, so only the required ones are what configuration has to name.
func identityAttrs(schema TypeSchema) (required, optional []string) {
	if !schema.HasIdentity() {
		return nil, nil
	}
	schema.EachIdentityAttribute(func(name string, requiredForImport bool) {
		switch {
		case requiredForImport:
			required = append(required, name)
		default:
			optional = append(optional, name)
		}
	})
	sort.Strings(required)
	sort.Strings(optional)
	return required, optional
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
// everything else, which SynthesizeTypeIdentity (internal/live/identity) then puts to
// its onlyContext to make sure the "everything else" really is ambient. That
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
// still has to be either required of every configuration (derivable) or
// observed on every instance in this one (cohortAttrs plus
// ConfigSignal.NamingOfType). Both bars are the ones a required-for-
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
func identityCandidates(schemas SchemaSet, typeName string, schema TypeSchema) (candidates, context []string) {
	required, optional := identityAttrs(schema)
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

// notContext is the names that are not context, in the order given. A
// refusal that has to say why needs these, and deriving them twice - once
// to decide and once to explain - is how the two got to disagree in the
// first place.
func notContext(schemas SchemaSet, typeName string, schema TypeSchema, names []string) []string {
	var out []string
	for _, name := range names {
		if !isContextAttr(schemas, typeName, schema, name) {
			out = append(out, name)
		}
	}
	return out
}

// isContextAttr reports whether one optional-for-import identity attribute
// is context - the account, the region, the project, the zone, the kind of
// value a provider fills in from its own ambient configuration rather than
// reading from this resource - as opposed to something the configuration
// might supply to distinguish this instance from a sibling that shares the
// same required identity attributes.
//
// The rule used to be two literal names, "account_id" and "region", because
// those are the pair the AWS provider's schemas mark optional-for-import on
// almost every type. That is an AWS fact wearing a general-sounding name:
// the GCP provider's equivalent pair is "project" and "region" (also
// "zone" and "location"), and a fixed AWS pair admits none of them (#218).
// Widening the list to include those names would only move the same bug to
// the next provider that used a different word for the same idea, so this
// derives the answer from what the schemas already say instead:
//
//   - A name that decomposes as <nested block>_<its own attribute>, matching
//     something the type's own resource block actually nests, is not
//     context: it is a flattened path to a value the configuration sets
//     through a nested block, and dropping it can conflate two configured
//     instances that share every other identity value. This is
//     google_project_iam_member's condition_title: the identity schema
//     marks it optional standing next to three required attributes, but the
//     resource block carries it as the required "title" argument of a
//     nested "condition" block, so a member/role/project triple bound with
//     two different conditions is two different bindings the flattened name
//     alone cannot tell apart.
//   - A name present in the type's own resource block as anything other
//     than Computed - required, or optional without a computed default -
//     is not context: the schema itself says there is no value the
//     provider can fill in on its own, so a caller who sets it is choosing
//     something the provider cannot recover by omission. This is
//     aws_route's destination_cidr_block, destination_ipv6_cidr_block and
//     destination_prefix_list_id: all three are real, hand-settable
//     arguments of aws_route with no computed fallback, so a route_table_id
//     alone is a route table, not a route. It is also, in the GCP corpus
//     this rule was written against, google_sql_user's "host": MySQL grants
//     the same account different privileges at different hosts, the
//     provider never fills the value in on its own (Computed is false), and
//     the argument's own description says as much - "Changing this forces
//     a new resource to be created."
//   - The literal names "id" and "name" are never context, independent of
//     whether the block marks them Computed. [TypeIdentity.IdentityAttrs]
//     already refuses to hand out "id" as an identity source for the exact
//     same reason stated where that refusal is built: whether a type's id
//     equals its import identity is an inference no schema carries. "name"
//     needs the same refusal for the same reason, seen from this rule's
//     side: an SDK convention where a resource's own name is
//     Optional+Computed, because the provider will invent one when the
//     caller leaves it out, describes a great many real resource-defining
//     labels - google_colab_runtime_template's "name" and
//     google_cloud_quotas_quota_preference's "name" both fit the shape
//     that would otherwise pass every other test in this rule, and both
//     are literally the value that distinguishes one instance from
//     another. A schema cannot settle which case a given "name" is, so this
//     refuses both rather than guess.
//   - Anything else - present in the block as Computed, or absent from the
//     block and matching no nested path - is context only if some *other*
//     resource type in the same provider treats the identical name the
//     same way: absent, or Computed, in that type's own block, with no
//     nested match there either. A name one type alone marks
//     optional-for-import and Computed is exactly as informative as a name
//     never seen before; nothing yet says whether it is shared
//     provider-wide plumbing (account_id, region, project, zone, location)
//     or a one-off value this particular type happens to default sensibly
//     (aws_dynamodb_table_item's range_key_value, which is Computed in that
//     type's own block and appears nowhere else in the AWS provider's
//     identity schemas, and is exactly as load-bearing to a table item's
//     identity as its hash key). Recurrence across independently-authored
//     types is the corroboration a single type's schema cannot offer on
//     its own; requiring at least one other corroborating type is what
//     keeps a provider-wide scoping value in and a type-specific default
//     out without naming either kind.
//
// "At least one other" means the type under test does not count toward its
// own corroboration (#228). schemas is the caller's whole provider schema
// set, and typeName's own entry is in it - schema is schemas[typeName] -
// so without the exclusion below, the loop would always find schema itself
// a match: name is optional-for-import in schema's own IdentitySchema by
// construction (every name this is ever called with comes from a type's own
// Context, see SynthesizeTypeIdentity (internal/live/identity)), and schema already cleared
// [locallyDefaultable] in the guard above, which is the same test the loop
// applies to every other candidate. That free match would let a provider
// with as few as two resource types clear a ">= 2 other types" bar with
// zero real corroboration, on any name they happen to share - coincidence
// standing in for the provider-wide convention the rule exists to detect.
// Excluding typeName's own key keeps the threshold counting what its name
// says it counts: independently-authored types, not the type asking the
// question.
func isContextAttr(schemas SchemaSet, typeName string, schema TypeSchema, name string) bool {
	if !locallyDefaultable(schema, name) {
		return false
	}
	corroborated := 0
	schemas.Each(func(otherName string, other TypeSchema) bool {
		if otherName == typeName {
			return true
		}
		if !other.HasIdentity() || !other.HasResource() {
			return true
		}
		_, optional := identityAttrs(other)
		for _, o := range optional {
			if o == name && locallyDefaultable(other, name) {
				corroborated++
				break
			}
		}
		return corroborated < 1
	})
	return corroborated >= 1
}

// locallyDefaultable is [isContextAttr]'s per-type half: whether name, in
// this one type's own resource block, is either absent or something the
// provider can fill in without the configuration's help - and is not a
// flattened path into a nested block or nested-type attribute the
// configuration actually sets. "id" and "name" are excluded unconditionally
// regardless of what the block says about them; see [isContextAttr] for
// why.
func locallyDefaultable(schema TypeSchema, name string) bool {
	if name == "id" || name == "name" {
		return false
	}
	if !schema.HasResource() {
		return true
	}
	if nestedContextPath(schema, name) {
		return false
	}
	attr, ok := schema.Argument(name)
	if !ok {
		return true
	}
	return attr.Computed
}

// nestedContextPath reports whether name splits at some underscore into a
// nested block or nested-type attribute's own name and one of its leaf
// attribute's name - <block>_<attr> - both present in b. A match means name
// is not a flat top-level attribute at all but a path the schema's own
// generator flattened for the identity attribute list, and the leaf it
// names is a value the configuration sets through that nested shape.
// google_project_iam_member's "condition_title" is exactly this: no
// top-level "condition_title" attribute exists, but a required "title"
// attribute exists inside the "condition" nested block.
//
// Every underscore position is tried because the split point is not
// knowable from the flattened name alone.
func nestedContextPath(schema TypeSchema, name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] != '_' {
			continue
		}
		prefix, leaf := name[:i], name[i+1:]
		if prefix == "" || leaf == "" {
			continue
		}
		if schema.NestedAttribute(prefix, leaf) {
			return true
		}
	}
	return false
}
