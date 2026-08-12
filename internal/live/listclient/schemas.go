// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package listclient

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TypeSchema is everything a caller needs to list one resource type and to
// make sense of the results.
type TypeSchema struct {
	// TypeName is the resource type name, e.g. "aws_vpc". A list resource
	// type name is the same string as the managed resource type it
	// enumerates.
	TypeName string

	// Config is the schema of the configuration block a list call for this
	// type accepts. It is never nil for a TypeSchema obtained from
	// [ListSchemas]. Its contents are provider-defined; build values for it
	// with [TypeSchema.BuildConfig].
	Config *configschema.Block

	// Identity is the identity schema of the managed resource type, which
	// types the Identity field of every result. It is nil when the provider
	// serves no identity schema for this type, in which case results carry
	// no usable identity.
	Identity *configschema.Object

	// IdentityVersion is the version of Identity, for callers that persist
	// identities and need to detect a schema change.
	IdentityVersion int64

	// Resource is the managed resource type's schema, which types the
	// Resource field of a result when full objects were requested. It is
	// nil when the provider serves a list schema for a type it has no
	// managed resource schema for, which would be a provider bug.
	Resource *configschema.Block
}

// HasIdentity reports whether results for this type will carry a decodable
// identity.
func (ts TypeSchema) HasIdentity() bool {
	return ts.Identity != nil
}

// Schemas is a provider's list-protocol surface: every type it can list,
// with the schemas for listing it.
//
// The zero value is a valid empty Schemas, which is what a provider that
// does not implement listing yields.
type Schemas struct {
	byType map[string]TypeSchema
}

// Len is the number of types the provider can list.
func (s Schemas) Len() int { return len(s.byType) }

// Types returns every listable type name, sorted.
func (s Schemas) Types() []string {
	out := make([]string, 0, len(s.byType))
	for name := range s.byType {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Supports reports whether the provider can list the named type.
func (s Schemas) Supports(typeName string) bool {
	_, ok := s.byType[typeName]
	return ok
}

// Get returns the schemas for one listable type.
func (s Schemas) Get(typeName string) (TypeSchema, bool) {
	ts, ok := s.byType[typeName]
	return ts, ok
}

// ListSchemas reports which resource types the given provider handle can
// list, and the schemas involved in listing each of them.
//
// A provider that implements no listing at all yields an empty Schemas and
// no diagnostics: not supporting the list protocol is a fact about the
// provider, not a failure of this call. A provider handle that cannot speak
// the protocol at all — anything that is not a gRPC provider handle — is a
// programming error and does produce an error diagnostic.
func ListSchemas(ctx context.Context, provider any) (Schemas, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	lister, ok := asLister(provider)
	if !ok {
		return Schemas{}, diags.Append(notAListerDiag(provider))
	}

	resp := lister.GetProviderSchema(ctx)
	diags = diags.Append(resp.Diagnostics)
	if resp.Diagnostics.HasErrors() {
		return Schemas{}, diags
	}

	schemas := Schemas{byType: make(map[string]TypeSchema, len(resp.ListResourceTypes))}
	for name, listSchema := range resp.ListResourceTypes {
		if listSchema.Block == nil {
			// A list schema with no configuration block is unusable: there
			// is nothing to encode a request against. Report it and skip
			// the type rather than handing back a TypeSchema that will
			// fail on every use.
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Invalid list resource schema",
				fmt.Sprintf("The provider offers a list resource schema for %q with no configuration block, so that type cannot be listed. This is a bug in the provider.", name),
			))
			continue
		}
		ts := TypeSchema{
			TypeName: name,
			Config:   listSchema.Block,
		}
		if resSchema, ok := resp.ResourceTypes[name]; ok {
			ts.Resource = resSchema.Block
			ts.Identity = resSchema.IdentitySchema
			ts.IdentityVersion = resSchema.IdentitySchemaVersion
		}
		schemas.byType[name] = ts
	}
	return schemas, diags
}

// BuildConfig builds a list configuration value for this type from the given
// arguments, filling in everything the caller did not set.
//
// Keys of vals name either attributes or nested block types of the list
// configuration schema. Every attribute the caller does not name is set to
// null, and every block type it does not name is set to that nesting mode's
// empty value, so the result always conforms to the schema's implied type —
// which is what the wire encoding requires. Values are converted to the
// schema's type where a conversion exists.
//
// An unknown key, or a value that cannot be converted, is an error
// diagnostic naming the key and the type the provider wanted: a caller
// building a filter against a schema it guessed at should hear about it
// here rather than as an opaque provider error.
func (ts TypeSchema) BuildConfig(vals map[string]cty.Value) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if ts.Config == nil {
		return cty.NilVal, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No list configuration schema",
			fmt.Sprintf("Cannot build a list configuration for %q because no configuration schema is known for it.", ts.TypeName),
		))
	}

	ty := ts.Config.ImpliedType()
	attrTypes := ty.AttributeTypes()

	for key := range vals {
		if _, ok := attrTypes[key]; !ok {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Unsupported list configuration argument",
				fmt.Sprintf("The list configuration for %q has no argument named %q. It accepts: %s.", ts.TypeName, key, joinSorted(attrTypes)),
			))
		}
	}
	if diags.HasErrors() {
		return cty.NilVal, diags
	}

	out := make(map[string]cty.Value, len(attrTypes))
	for name, wantTy := range attrTypes {
		given, ok := vals[name]
		if !ok {
			out[name] = unsetValue(ts.Config, name, wantTy)
			continue
		}
		converted, err := convert.Convert(given, wantTy)
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Invalid list configuration argument",
				fmt.Sprintf("Cannot use the given value for %q in the list configuration for %q: %s.", name, ts.TypeName, err),
			))
			continue
		}
		out[name] = converted
	}
	if diags.HasErrors() {
		return cty.NilVal, diags
	}
	return cty.ObjectVal(out), diags
}

// EmptyConfig is the list configuration that sets nothing: every argument
// null and every block empty. It is what [List] sends when the caller passes
// cty.NilVal.
func (ts TypeSchema) EmptyConfig() cty.Value {
	if ts.Config == nil {
		return cty.NilVal
	}
	val, _ := ts.BuildConfig(nil)
	return val
}

// unsetValue is the value for an argument the caller did not set. Attributes
// go null; nested blocks go to the empty value of their nesting mode,
// because a null collection is not what a provider decoding a block expects.
func unsetValue(b *configschema.Block, name string, ty cty.Type) cty.Value {
	nested, ok := b.BlockTypes[name]
	if !ok {
		return cty.NullVal(ty)
	}
	switch nested.Nesting {
	case configschema.NestingList:
		return cty.ListValEmpty(ty.ElementType())
	case configschema.NestingSet:
		return cty.SetValEmpty(ty.ElementType())
	case configschema.NestingMap:
		return cty.MapValEmpty(ty.ElementType())
	default:
		// NestingSingle and NestingGroup are represented as a single
		// object, for which absence is null.
		return cty.NullVal(ty)
	}
}

func joinSorted(attrTypes map[string]cty.Type) string {
	names := make([]string, 0, len(attrTypes))
	for name := range attrTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(nothing)"
	}
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
