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

// schemaRefusal is the clause the "outside the subset" error adds when the
// run did have the provider's schemas and the fallback still refused. It says
// which of the fallback's two bars was not cleared, because "not in the
// table" is a misleading whole answer once a table entry is no longer the
// only way in.
func (r *resolver) schemaRefusal(typeName string) string {
	if len(r.schemas) == 0 {
		return ""
	}
	schema, served := r.schemas[typeName]
	switch {
	case !served:
		return fmt.Sprintf(" The provider serves no %s at all.", typeName)
	case schema.IdentitySchema == nil:
		return fmt.Sprintf(" The provider serves no resource identity schema for %s, so nothing but a table entry can say what identifies one.", typeName)
	}

	admitted := DerivableWith(map[string]providers.Schema{typeName: schema}, r.signal)
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

func pluralThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "all of them"
}
