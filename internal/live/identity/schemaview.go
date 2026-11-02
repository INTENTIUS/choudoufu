// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	pid "github.com/intentius/choudoufu/pkg/identity"
)

// The identity-schema reading rules live in pkg/identity (GitHub issue
// #2244) and read a provider's schemas through its TypeSchema and SchemaSet
// interfaces. This file is the adapter from OpenTofu's own provider schema
// types to those interfaces, and the unexported names this package's code
// and tests have always called the rules by. Nothing is copied: each value
// wraps the provider's schema in place.

// providerSchema is one providers.Schema as a [pid.TypeSchema].
type providerSchema struct {
	s providers.Schema
}

var _ pid.TypeSchema = providerSchema{}

func (p providerSchema) HasIdentity() bool { return p.s.IdentitySchema != nil }

func (p providerSchema) EachIdentityAttribute(fn func(name string, requiredForImport bool)) {
	if p.s.IdentitySchema == nil {
		return
	}
	for name, attr := range p.s.IdentitySchema.Attributes {
		fn(name, attr.Required)
	}
}

func (p providerSchema) HasResource() bool { return p.s.Block != nil }

func (p providerSchema) Argument(name string) (pid.Argument, bool) {
	if p.s.Block == nil {
		return pid.Argument{}, false
	}
	attr, ok := p.s.Block.Attributes[name]
	if !ok || attr == nil {
		return pid.Argument{}, false
	}
	return pid.Argument{Required: attr.Required, Optional: attr.Optional, Computed: attr.Computed}, true
}

func (p providerSchema) NestedAttribute(container, leaf string) bool {
	b := p.s.Block
	if b == nil {
		return false
	}
	if nb, ok := b.BlockTypes[container]; ok {
		if _, ok := nb.Block.Attributes[leaf]; ok {
			return true
		}
	}
	if attr, ok := b.Attributes[container]; ok && attr != nil && attr.NestedType != nil {
		if _, ok := attr.NestedType.Attributes[leaf]; ok {
			return true
		}
	}
	return false
}

// providerSchemas is a provider's resource schemas as a [pid.SchemaSet].
type providerSchemas map[string]providers.Schema

var _ pid.SchemaSet = providerSchemas(nil)

func (m providerSchemas) Each(fn func(typeName string, schema pid.TypeSchema) bool) {
	for name, s := range m {
		if !fn(name, providerSchema{s}) {
			return
		}
	}
}

// identityAttrs is [pid.IdentityAttrs] over one identity schema.
func identityAttrs(body *configschema.Object) (required, optional []string) {
	return pid.IdentityAttrs(providerSchema{providers.Schema{IdentitySchema: body}})
}

// identityCandidates is [pid.IdentityCandidates].
func identityCandidates(schemas map[string]providers.Schema, typeName string, schema providers.Schema) (candidates, context []string) {
	return pid.IdentityCandidates(providerSchemas(schemas), typeName, providerSchema{schema})
}

// isContextAttr is [pid.IsContextAttr].
func isContextAttr(schemas map[string]providers.Schema, typeName string, schema providers.Schema, name string) bool {
	return pid.IsContextAttr(providerSchemas(schemas), typeName, providerSchema{schema}, name)
}

// notContext is [pid.NotContext]: the names that are not context, in the
// order given.
func notContext(schemas map[string]providers.Schema, typeName string, schema providers.Schema, names []string) []string {
	return pid.NotContext(providerSchemas(schemas), typeName, providerSchema{schema}, names)
}
