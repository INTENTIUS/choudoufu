// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package listclient

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/tfdiags"
)

// Lister is the part of a provider handle this package uses. The concrete
// gRPC provider handles implement it: *plugin.GRPCProvider for plugin
// protocol 5 and *plugin6.GRPCProvider for protocol 6.
//
// It is deliberately not providers.Interface. Listing is not something every
// provider implementation in the tree can do — the mocks and test doubles
// that satisfy providers.Interface have no list protocol behind them — so
// the capability is expressed as its own small interface and discovered by
// assertion rather than by widening the provider interface.
type Lister interface {
	// GetProviderSchema is the source of the list resource schemas.
	GetProviderSchema(ctx context.Context) providers.GetProviderSchemaResponse

	// ListResourceStream runs the ListResource streaming RPC, calling emit
	// once per result. Returning false from emit ends the stream.
	ListResourceStream(ctx context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics
}

// Request is a single list call.
type Request struct {
	// TypeName is the resource type to enumerate, which must be one of the
	// types reported by [ListSchemas].
	TypeName string

	// Config is the list configuration, conforming to the type's list
	// configuration schema — build it with [TypeSchema.BuildConfig].
	// cty.NilVal means "set nothing", and is filled in from the schema.
	Config cty.Value

	// IncludeResource asks the provider to attach the full resource object
	// to every result. Providers may pay a read per result for this, so
	// leave it false when the identity is all that is wanted.
	IncludeResource bool

	// Limit caps the number of results; zero means no cap. This is a
	// request to the provider, which ends the stream once it is reached —
	// it is not a client-side truncation, so a provider that ignores it
	// will still send everything.
	Limit int64
}

// Result is one listed resource instance.
type Result struct {
	// TypeName is the resource type that was listed.
	TypeName string

	// Identity is the resource identity, an object typed by the resource
	// type's identity schema. It is cty.NilVal when the provider sent no
	// identity or serves no identity schema for the type; a caller that
	// needs an identity must check, because a provider is free to send a
	// result without one.
	Identity cty.Value

	// DisplayName is the provider's human-readable label for the result.
	// It is for display only: nothing guarantees it is unique or stable.
	DisplayName string

	// Resource is the full resource object, typed by the resource type's
	// schema. It is cty.NilVal unless the request set IncludeResource.
	Resource cty.Value

	// Diagnostics are the diagnostics the provider attached to this
	// individual result. A result can carry an error diagnostic and still
	// be delivered, because the rest of the stream stays valid; these are
	// not returned as call-level diagnostics for that reason.
	Diagnostics tfdiags.Diagnostics
}

// HasIdentity reports whether this result carries a usable identity object.
func (r Result) HasIdentity() bool {
	return r.Identity != cty.NilVal && !r.Identity.IsNull()
}

// IdentityAttr reads one string attribute out of the result's identity, for
// example "id" or "region". The second return is false when the result has
// no identity, when the identity has no such attribute, or when that
// attribute is null or not a string.
func (r Result) IdentityAttr(name string) (string, bool) {
	if !r.HasIdentity() {
		return "", false
	}
	ty := r.Identity.Type()
	if !ty.IsObjectType() || !ty.HasAttribute(name) {
		return "", false
	}
	v := r.Identity.GetAttr(name)
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}

// List enumerates the live instances of one resource type, buffering the
// whole stream.
//
// The provider argument is a provider handle; it is typed as any so that a
// caller holding a providers.Interface can pass it and get a diagnostic
// rather than a compile error when that provider turns out not to speak the
// list protocol. config must conform to the type's list configuration
// schema, and cty.NilVal means "set nothing".
//
// Diagnostics from the call itself are returned; diagnostics the provider
// attached to individual results ride on those results.
func List(ctx context.Context, provider any, typeName string, config cty.Value, includeResource bool) ([]Result, tfdiags.Diagnostics) {
	var results []Result
	diags := Stream(ctx, provider, Request{
		TypeName:        typeName,
		Config:          config,
		IncludeResource: includeResource,
	}, func(r Result) bool {
		results = append(results, r)
		return true
	})
	return results, diags
}

// Stream enumerates the live instances of one resource type, calling emit
// once per result as it arrives instead of buffering. Returning false from
// emit ends the stream early, which is not an error.
//
// Stream is the form to use when the result set may be large, or when the
// caller can stop as soon as it has found what it is looking for.
func Stream(ctx context.Context, provider any, req Request, emit func(Result) bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	lister, ok := asLister(provider)
	if !ok {
		return diags.Append(notAListerDiag(provider))
	}
	if req.TypeName == "" {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Missing resource type to list",
			"A list request must name the resource type to enumerate.",
		))
	}

	config := req.Config
	if config == cty.NilVal {
		// Resolve the schema so the provider receives a well-formed empty
		// configuration rather than an untyped null, which providers
		// decoding into a struct do not universally tolerate.
		schemas, schemaDiags := ListSchemas(ctx, lister)
		diags = diags.Append(schemaDiags)
		if schemaDiags.HasErrors() {
			return diags
		}
		ts, ok := schemas.Get(req.TypeName)
		if !ok {
			return diags.Append(unsupportedTypeDiag(req.TypeName, schemas))
		}
		config = ts.EmptyConfig()
	}

	streamDiags := lister.ListResourceStream(ctx, providers.ListResourceRequest{
		TypeName:              req.TypeName,
		Config:                config,
		IncludeResourceObject: req.IncludeResource,
		Limit:                 req.Limit,
	}, func(ev providers.ListResourceEvent) bool {
		return emit(Result{
			TypeName:    req.TypeName,
			Identity:    ev.Identity,
			DisplayName: ev.DisplayName,
			Resource:    ev.ResourceObject,
			Diagnostics: ev.Diagnostics,
		})
	})
	return diags.Append(streamDiags)
}

// asLister recovers the list capability from a provider handle.
func asLister(provider any) (Lister, bool) {
	if provider == nil {
		return nil, false
	}
	l, ok := provider.(Lister)
	return l, ok
}

func notAListerDiag(provider any) tfdiags.Diagnostic {
	what := "a nil provider"
	if provider != nil {
		what = fmt.Sprintf("a provider of type %T", provider)
	}
	return tfdiags.Sourceless(
		tfdiags.Error,
		"Provider without list protocol support",
		fmt.Sprintf("Listing resources requires a plugin-backed provider handle, but this operation was given %s, which cannot make ListResource calls. This is a bug in OpenTofu.", what),
	)
}

func unsupportedTypeDiag(typeName string, schemas Schemas) tfdiags.Diagnostic {
	if schemas.Len() == 0 {
		return tfdiags.Sourceless(
			tfdiags.Error,
			"Provider without list support",
			fmt.Sprintf("This provider offers no list resource types, so %q cannot be listed. Listing requires a provider built against plugin protocol 5.10 or 6.10 or later that implements the list protocol for this type.", typeName),
		)
	}
	return tfdiags.Sourceless(
		tfdiags.Error,
		"Unlistable resource type",
		fmt.Sprintf("The provider supports listing, but not for %q. Only the types present in the provider's list resource schemas can be listed.", typeName),
	)
}
