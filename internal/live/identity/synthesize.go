// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"strings"

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
//   - The type has to be admitted by [DerivableWith] over its own schema,
//     either because every attribute the provider requires for import is a
//     required argument ([AdmitSchema]) or because this configuration sets
//     every one of them on every instance ([AdmitConfigSignal]). That is the
//     same verdict the report and the survey use; nothing new is decided
//     here.
//   - The identity has to be a *single* attribute. A composite identity is
//     two or more values joined by a separator character that no schema
//     carries - "_" for aws_route, "/" for an IAM attachment, "," for a
//     target group attachment - and guessing one would produce an import ID
//     that addresses nothing. Composites stay in the hand table, which is
//     where that inference is written down and checked (see
//     [VerifyTable]).
//   - The required attribute has to be the *whole* identity. If the schema
//     also marks something other than the context pair (account_id,
//     region) optional for import, the required attribute names only part
//     of what identifies an instance. aws_route is exactly this shape:
//     route_table_id is the only required identity attribute, and the
//     schema also lists the three destination_* arguments as
//     optional-for-import alternatives - so a synthesized entry keyed on
//     route_table_id alone would name a route table, not a route. See
//     [checkIdentity] in schema_verify.go for the same asymmetry, read
//     from the table-checking side.
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
	if len(schemas) == 0 {
		return TypeIdentity{}, false
	}
	schema, served := schemas[typeName]
	if !served || schema.Block == nil || schema.IdentitySchema == nil {
		return TypeIdentity{}, false
	}

	admitted := DerivableWith(map[string]providers.Schema{typeName: schema}, signal)
	if len(admitted) == 0 {
		return TypeIdentity{}, false
	}
	d := admitted[0]
	if len(d.IdentityAttrs) != 1 {
		// A composite identity needs a separator this package will not
		// invent. See the doc comment.
		return TypeIdentity{}, false
	}
	if !onlyContext(d.Context) {
		// The one required attribute is not the whole identity: the schema
		// also marks something other than the context pair optional for
		// import. aws_route is exactly this shape - route_table_id is the
		// only required identity attribute, but the schema also lists the
		// three destination_* arguments as optional-for-import
		// alternatives, and a route table's ID is not a route's identity.
		// [checkIdentity] in schema_verify.go documents the same
		// asymmetry from the table-checking side; this is the synthesis
		// side of it.
		return TypeIdentity{}, false
	}

	name := d.IdentityAttrs[0]
	if arg, ok := schema.Block.Attributes[name]; !ok || arg == nil {
		// DerivableWith already required this, both in its strict path and
		// in its cohort path; checked again because a synthesized entry that
		// reads an argument the type does not have would fail at resolution
		// with a message about the configuration rather than about the
		// schema.
		return TypeIdentity{}, false
	}

	return TypeIdentity{
		Type: typeName,
		Components: []Component{{
			Attrs:        []string{name},
			IdentityAttr: name,
		}},
		ImportSyntax:  strings.ToUpper(name),
		IdentityAttrs: []string{name},
		Synthesized:   true,
		Admits:        d.Admits,
	}, true
}

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
	schema, served := schemas[typeName]
	switch {
	case !served:
		return fmt.Sprintf(" The provider serves no %s at all.", typeName)
	case schema.IdentitySchema == nil:
		return fmt.Sprintf(" The provider serves no resource identity schema for %s, so nothing but a table entry can say what identifies one.", typeName)
	}

	admitted := DerivableWith(map[string]providers.Schema{typeName: schema}, signal)
	if len(admitted) == 0 {
		required, _ := identityAttrs(schema.IdentitySchema)
		return fmt.Sprintf(
			" The provider's identity schema for %s requires %s, and neither the schema nor this configuration says the configuration supplies %s: an object this run cannot name is one only marker discovery finds.",
			typeName, orList(required), pluralThem(len(required)))
	}
	return fmt.Sprintf(
		" The provider's identity schema for %s is a composite of %s, and the character that joins them into an import ID is in no schema, so that inference has to be written down in the table.",
		typeName, orList(admitted[0].IdentityAttrs))
}

// schemaRefusal is [SchemaRefusal] read off the resolver's own schemas and
// signal.
func (r *resolver) schemaRefusal(typeName string) string {
	return SchemaRefusal(typeName, r.schemas, r.signal)
}

// contextAttrs are the identity attributes the provider fills in itself
// rather than reading from configuration - the account and the region - so
// their presence among a schema's optional-for-import attributes says
// nothing about whether a required attribute is the whole identity. Any
// other name in that optional set means it is not: an alternative the
// configuration might supply (aws_route's destination_*) or a value the
// provider fills in under some other name, either way something
// [SynthesizeTypeIdentity] cannot infer.
var contextAttrs = map[string]bool{"account_id": true, "region": true}

// onlyContext reports whether every name in an identity schema's
// optional-for-import set is the context pair. See [contextAttrs].
func onlyContext(names []string) bool {
	for _, name := range names {
		if !contextAttrs[name] {
			return false
		}
	}
	return true
}

func pluralThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "all of them"
}
