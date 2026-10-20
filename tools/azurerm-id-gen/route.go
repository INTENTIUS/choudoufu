// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// assignRoutes records on every row the route that admits the type
// (#1120 unit 17), asking the two pieces of code that decide it rather
// than restating their rules here:
//
//   - internal/live/substrate's guards, run over this very artifact
//     (substrate.AzurermWithheldIn), say which templates are withheld;
//   - internal/live/identity's identity-schema route
//     (identity.IdentitySchemaRouteAdmits), given the pinned schema and
//     told the provider block answers subscription_id as substrate tells
//     it, says which identity-schema types it admits on the schemas alone.
//
// Then: an identity-schema type the route admits is identity-schema; one it
// refuses is template-fallback when its template passes the guards,
// withheld when the template is refused, and unmapped when it has none. A
// type with no identity schema is its class, or withheld. The route does
// not depend on a configuration at azurerm 5.10.0: no identity-schema type
// is in the cohort a configuration's naming could admit, which
// internal/live/identity's TestAzurermRoutesMatchTheRecord pins.
func assignRoutes(a *artifact, schemas map[string]typeSchema) error {
	raw, err := encode(a)
	if err != nil {
		return err
	}
	withheld, err := substrate.AzurermWithheldIn(raw)
	if err != nil {
		return err
	}
	ps := providerSchemas(schemas)
	cloud := substrate.AzurermCloudIdentityAttrs()
	for i := range a.Types {
		e := &a.Types[i]
		templateClass := e.Class
		if e.Class == classIdentitySchema {
			templateClass = e.TemplateClass
		}
		bearing := templateClass == classTemplate || templateClass == classParentID
		_, isWithheld := withheld[e.Type]
		switch {
		case e.Class == classIdentitySchema && identity.IdentitySchemaRouteAdmits(ps, e.Type, cloud):
			e.Route = substrate.AzurermRouteIdentitySchema
		case bearing && isWithheld:
			e.Route = substrate.AzurermRouteWithheld
		case bearing && e.Class == classIdentitySchema:
			e.Route = substrate.AzurermRouteTemplateFallback
		case bearing:
			e.Route = templateClass
		default:
			e.Route = substrate.AzurermRouteUnmapped
		}
	}
	return nil
}

// providerSchemas is the identity-schema types' schemas in the provider
// package's terms, reduced to what the route reads: which arguments a type
// has and how each may be set, and its identity attributes.
func providerSchemas(schemas map[string]typeSchema) map[string]providers.Schema {
	out := map[string]providers.Schema{}
	for name, ts := range schemas {
		if ts.Identity == nil {
			continue
		}
		block := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
		for arg, info := range ts.Attrs {
			block.Attributes[arg] = &configschema.Attribute{Type: cty.String, Required: info.Required, Optional: info.Optional, Computed: info.Computed}
		}
		ident := &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{}}
		for _, attr := range ts.Identity {
			if ts.IdentityOptional[attr] {
				ident.Attributes[attr] = &configschema.Attribute{Type: cty.String, Optional: true}
				continue
			}
			ident.Attributes[attr] = &configschema.Attribute{Type: cty.String, Required: true}
		}
		out[name] = providers.Schema{Block: block, IdentitySchema: ident}
	}
	return out
}
