// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// SynthesizeTypeIdentity builds a [TypeIdentity] for a resource type
// [DefaultTable] does not cover, out of the provider's own resource identity
// schema plus - where the schema leaves the question open - the config-side
// naming signal.
//
// This is the fallback that stops a type needing a hand-written row before it
// can resolve at all. Until it existed, [Resolve] refused any type missing
// from the table, so every type the provider's schemas already describe
// completely still had to be typed out in table.go before a projection could
// name one of its instances. The schemas describe the common case exactly:
// one identity attribute, named after the configuration argument that
// supplies it.
//
// The rule is the strict one, and it is narrower than [DerivableWith]'s on
// purpose:
//
//   - The type must not be vetoed by [NotImportable]. That one is not a
//     reading of the schemas at all; it is the answer to the question the
//     schemas cannot be asked, which is whether ImportResourceState works.
//     It sits here, in the fallback itself, so that every route reaching for
//     the fallback gets it - see [NotImportable] for which routes those are
//     and what went wrong while the check lived in only one of them.
//   - The type has to be admitted by [derivableOne] over its own schema,
//     either because every attribute that identifies an instance is a
//     required argument ([AdmitSchema]) or because this configuration sets
//     every one of them on every instance ([AdmitConfigSignal]). That is the
//     same verdict the report and the survey use; nothing new is decided
//     here. Which attributes those are is [identityCandidates]' question,
//     and it has one extra answer for a type whose identity schema requires
//     nothing for import.
//   - A composite identity - two or more attributes, joined into a legacy
//     import ID by a separator character no schema carries - is admitted
//     and marked [TypeIdentity.IdentityObjectOnly] (#105). It imports by
//     identity object, and the separator it cannot know stays uninvented
//     rather than being guessed. This bullet used to say the opposite and
//     the refusal message below outlived the rule by longer still; see
//     [synthesizeTypeIdentity].
//   - The identifying attributes have to be the *whole* identity. If the
//     schema also marks something that is not context optional for import,
//     they name only part of what identifies an instance. aws_route is
//     exactly this shape: route_table_id is the only required identity
//     attribute, and the schema also lists the three destination_*
//     arguments as optional-for-import alternatives - so a synthesized
//     entry keyed on route_table_id alone would name a route table, not a
//     route. See [checkIdentity] in schema_verify.go for the same
//     asymmetry, read from the table-checking side.
//
// The synthesized entry names exactly one attribute on each side: the
// argument the identity is read from, and the attribute another resource may
// read it back out of. It deliberately does not add "id" to
// [TypeIdentity.IdentityAttrs] the way many hand-written rows do, because
// whether a type's id attribute equals its import identity is precisely the
// inference a schema does not carry: aws_route's id is a synthesized
// r-rtb-… value and aws_ecs_cluster's is an ARN, and handing either out
// would resolve some other resource's identity to the wrong string.
//
// A nil or empty schema map, or a type the provider does not serve, returns
// false and leaves the caller with today's behaviour exactly.
func SynthesizeTypeIdentity(typeName string, schemas map[string]providers.Schema, signal *ConfigSignal) (TypeIdentity, bool) {
	ti, refusal := synthesizeTypeIdentity(typeName, schemas, signal)
	return ti, refusal == ""
}

// synthesizeTypeIdentity is [SynthesizeTypeIdentity] and [SchemaRefusal] in
// one function, returning either the entry or the clause saying why there
// is none - never both, never neither.
//
// Splitting them was the defect. [SchemaRefusal] used to re-derive the
// answer from its own reading of the schemas and then name a cause from a
// list of causes it kept separately, and the list had gone stale: its final
// clause said "the identity schema is a composite of X, and the character
// that joins them into an import ID is in no schema", which stopped being a
// refusal at all once [TypeIdentity.IdentityObjectOnly] gave a composite
// identity somewhere to go. Every reader of that clause was sent after a
// missing separator, and the gate that had actually fired was
// [onlyContext] - a completely different problem with a completely
// different fix. Measured at hashicorp/aws 6.59.0 and hashicorp/google
// 7.44.0, the clause was reached 6 and 214 times respectively and was wrong
// about the cause every single time, aws_route among them - the very type
// this file's own doc comment uses to explain the [onlyContext] gate.
//
// One function cannot drift from itself, so the refusal a caller reads is
// now produced at the point the refusal happens, by the code that refuses.
func synthesizeTypeIdentity(typeName string, schemas map[string]providers.Schema, signal *ConfigSignal) (TypeIdentity, string) {
	// Ahead of every schema reading below, and deliberately ahead of the
	// "no schemas at all" answer too, because this one needs none: it is
	// [NotImportable], the issue #331 veto, and it is here rather than at
	// the call sites because this function IS the schema fallback every
	// admission route reaches for. internal/live/lint's admitted() asks it,
	// [resolver.lookupType] asks it, and internal/live/liveimport's
	// admittedByProviderSchema asks it - the last two with no veto of their
	// own until this line existed, which is how a type with no Importer
	// reached a live-import and a resolution that lint had already refused.
	//
	// Refusing here also gives the refusal its own sentence for free: one
	// function produces the entry or the reason, never both, so a reader of
	// [SchemaRefusal] is told about the missing Importer instead of being
	// sent after a schema that is in fact perfectly good.
	if NotImportable(typeName) {
		return TypeIdentity{}, fmt.Sprintf(" %s is refused for a reason no schema carries: %s.", typeName, NotImportableReason)
	}
	if len(schemas) == 0 {
		return TypeIdentity{}, noSchemasRefusal
	}
	schema, served := schemas[typeName]
	switch {
	case !served:
		return TypeIdentity{}, fmt.Sprintf(" The provider serves no %s at all.", typeName)
	case schema.IdentitySchema == nil:
		return TypeIdentity{}, fmt.Sprintf(" The provider serves no resource identity schema for %s, so nothing but a table entry can say what identifies one.", typeName)
	case schema.Block == nil:
		return TypeIdentity{}, fmt.Sprintf(" The provider serves no configuration schema for %s, so nothing says which of its arguments an identity attribute is read from.", typeName)
	}

	d, admitted := derivableOne(schemas, typeName, schema, signal)
	if !admitted || len(d.IdentityAttrs) == 0 {
		candidates, _ := identityCandidates(schemas, typeName, schema)
		required, _ := identityAttrs(schema.IdentitySchema)
		switch {
		case len(candidates) == 0:
			return TypeIdentity{}, fmt.Sprintf(
				" The provider's identity schema for %s requires nothing for import and marks nothing but context optional for import, so it names no value that would tell one instance from another: an object this run cannot name is one only marker discovery finds.",
				typeName)
		case len(required) == 0:
			return TypeIdentity{}, fmt.Sprintf(
				" The provider's identity schema for %s requires nothing for import, so the only value it offers that would tell one instance from another is %s, and neither the schema nor this configuration says the configuration supplies %s on every instance: an object this run cannot name is one only marker discovery finds.",
				typeName, orList(candidates), pluralThem(len(candidates)))
		default:
			return TypeIdentity{}, fmt.Sprintf(
				" The provider's identity schema for %s requires %s, and neither the schema nor this configuration says the configuration supplies %s: an object this run cannot name is one only marker discovery finds.",
				typeName, orList(candidates), pluralThem(len(candidates)))
		}
	}
	if extra := notContext(schemas, typeName, schema, d.Context); len(extra) > 0 {
		// The attributes that identify an instance are not the whole
		// identity: the schema also marks something that is not context
		// optional for import. aws_route is exactly this shape -
		// route_table_id is the only required identity attribute, but the
		// schema also lists the three destination_* arguments as
		// optional-for-import alternatives, and a route table's ID is not
		// a route's identity. [checkIdentity] in schema_verify.go
		// documents the same asymmetry from the table-checking side; this
		// is the synthesis side of it.
		return TypeIdentity{}, fmt.Sprintf(
			" The provider's identity schema for %s also marks %s optional for import, and %s not something the provider fills in from its own configuration the way an account or a region is, so %s %s only part of what tells one instance from another: which part the rest supplies is an inference no schema carries, and it has to be written down in the table.",
			typeName, orList(extra), pluralIs(len(extra)), orList(d.IdentityAttrs), namesVerb(len(d.IdentityAttrs)))
	}

	names := append([]string(nil), d.IdentityAttrs...)
	sort.Strings(names)
	components := make([]Component, 0, len(names))
	for _, name := range names {
		if arg, ok := schema.Block.Attributes[name]; !ok || arg == nil {
			// DerivableWith already required this, both in its strict path
			// and in its cohort path; checked again because a synthesized
			// entry that reads an argument the type does not have would fail
			// at resolution with a message about the configuration rather
			// than about the schema.
			return TypeIdentity{}, fmt.Sprintf(
				" The provider's identity schema for %s names %q, and the type's own configuration schema has no argument by that name, so nothing in this configuration can supply it.",
				typeName, name)
		}
		components = append(components, Component{
			Attrs:        []string{name},
			IdentityAttr: name,
		})
	}

	// One attribute is the whole identity, so the import ID is that
	// attribute's value and the ordinary string path works.
	//
	// More than one is GitHub issue #105. The config signal has already
	// proved every attribute is client-named, so the identity is entirely
	// computable - what is missing is only the separator that joins the
	// values into the provider's legacy import-ID grammar, and no schema
	// carries one. Terraform 1.12 resource identity is what makes that
	// stop mattering: the projection imports by identity object when the
	// schema allows, and this type has an identity schema by the check at
	// the top of this function.
	//
	// So the entry carries the attributes and refuses to invent the string.
	// [TypeIdentity.IdentityObjectOnly] is what makes the refusal stick:
	// without it, resolve.go's classify would concatenate the values with
	// nothing between them and hand out "prodsvc" for an aws_ecs_service in
	// cluster "prod" named "svc" - a string that is not empty, so every
	// guard downstream passes it through.
	if len(names) == 1 {
		return TypeIdentity{
			Type:          typeName,
			Components:    components,
			ImportSyntax:  strings.ToUpper(names[0]),
			IdentityAttrs: names,
			Synthesized:   true,
			Admits:        d.Admits,
		}, ""
	}

	return TypeIdentity{
		Type:       typeName,
		Components: components,
		// Deliberately not a joined grammar: there is none. The syntax
		// string is documentation, and what it has to document here is that
		// this type is imported by identity object.
		ImportSyntax:       "identity{" + strings.Join(names, ", ") + "}",
		IdentityAttrs:      names,
		IdentityObjectOnly: true,
		Synthesized:        true,
		Admits:             d.Admits,
	}, ""
}

// noSchemasRefusal is the reason [synthesizeTypeIdentity] gives when it was
// handed no schemas at all. [SchemaRefusal] never returns it - a caller
// that offered no schemas gets the same silence [Resolve] gives - but the
// synthesis side still has to distinguish "refused" from "admitted", so
// the reason exists rather than being an empty string that would read as
// success.
const noSchemasRefusal = " No provider schemas were read, so nothing but a table entry can say what identifies one."

// SchemaRefusal is the clause the "outside the subset" error adds when the
// caller did have the provider's schemas and the fallback still refused the
// type. It says which of the fallback's two bars was not cleared, because
// "not in the table" is a misleading whole answer once a table entry is no
// longer the only way in.
//
// Exported so that a caller outside this package can word its own refusal
// the same way [Resolve] words its - lint's admission check, principally,
// which runs before a resolver exists at all and has to explain a refusal in
// the same voice so that the two points do not read as two different rules.
// An empty schemas map returns "", the same silence a caller that never
// offered any gets from [Resolve] itself.
func SchemaRefusal(typeName string, schemas map[string]providers.Schema, signal *ConfigSignal) string {
	if len(schemas) == 0 {
		return ""
	}
	_, refusal := synthesizeTypeIdentity(typeName, schemas, signal)
	return refusal
}

// schemaRefusal is [SchemaRefusal] read off the resolver's own schemas and
// signal.
func (r *resolver) schemaRefusal(typeName string) string {
	return SchemaRefusal(typeName, r.schemas, r.signal)
}

// onlyContext reports whether every name in an identity schema's
// optional-for-import set is context: something the provider fills in
// itself rather than something this type's own configuration supplies to
// narrow which instance is meant. See [isContextAttr] for the rule and for
// why it is derived from the schema rather than a fixed pair of names.
//
// typeName is schema's own key in schemas, passed through so
// [isContextAttr] can leave the type under test out of its own
// corroboration count. See that function's doc comment for why the count
// has to exclude it rather than merely include it once for free.
func onlyContext(schemas map[string]providers.Schema, typeName string, schema providers.Schema, names []string) bool {
	return len(notContext(schemas, typeName, schema, names)) == 0
}

// notContext is [onlyContext]'s answer with the evidence kept: the names
// that are not context, in the order given. A refusal that has to say why
// needs these, and deriving them twice - once to decide and once to
// explain - is how the two got to disagree in the first place.
func notContext(schemas map[string]providers.Schema, typeName string, schema providers.Schema, names []string) []string {
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
// Context, see [SynthesizeTypeIdentity]), and schema already cleared
// [locallyDefaultable] in the guard above, which is the same test the loop
// applies to every other candidate. That free match would let a provider
// with as few as two resource types clear a ">= 2 other types" bar with
// zero real corroboration, on any name they happen to share - coincidence
// standing in for the provider-wide convention the rule exists to detect.
// Excluding typeName's own key keeps the threshold counting what its name
// says it counts: independently-authored types, not the type asking the
// question.
func isContextAttr(schemas map[string]providers.Schema, typeName string, schema providers.Schema, name string) bool {
	if !locallyDefaultable(schema, name) {
		return false
	}
	corroborated := 0
	for otherName, other := range schemas {
		if otherName == typeName {
			continue
		}
		if other.IdentitySchema == nil || other.Block == nil {
			continue
		}
		_, optional := identityAttrs(other.IdentitySchema)
		for _, o := range optional {
			if o == name && locallyDefaultable(other, name) {
				corroborated++
				break
			}
		}
		if corroborated >= 1 {
			break
		}
	}
	return corroborated >= 1
}

// locallyDefaultable is [isContextAttr]'s per-type half: whether name, in
// this one type's own resource block, is either absent or something the
// provider can fill in without the configuration's help - and is not a
// flattened path into a nested block or nested-type attribute the
// configuration actually sets. "id" and "name" are excluded unconditionally
// regardless of what the block says about them; see [isContextAttr] for
// why.
func locallyDefaultable(schema providers.Schema, name string) bool {
	if name == "id" || name == "name" {
		return false
	}
	if schema.Block == nil {
		return true
	}
	if nestedContextPath(schema.Block, name) {
		return false
	}
	attr, ok := schema.Block.Attributes[name]
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
func nestedContextPath(b *configschema.Block, name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] != '_' {
			continue
		}
		prefix, leaf := name[:i], name[i+1:]
		if prefix == "" || leaf == "" {
			continue
		}
		if nb, ok := b.BlockTypes[prefix]; ok {
			if _, ok := nb.Block.Attributes[leaf]; ok {
				return true
			}
		}
		if attr, ok := b.Attributes[prefix]; ok && attr.NestedType != nil {
			if _, ok := attr.NestedType.Attributes[leaf]; ok {
				return true
			}
		}
	}
	return false
}

func pluralThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "all of them"
}

func pluralIs(n int) string {
	if n == 1 {
		return "it is"
	}
	return "they are"
}

func namesVerb(n int) string {
	if n == 1 {
		return "names"
	}
	return "name"
}
