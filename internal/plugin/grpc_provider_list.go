// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/msgpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/opentofu/opentofu/internal/plugin/convert"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/tfdiags"
	proto "github.com/opentofu/opentofu/internal/tfplugin5"
)

// This file implements the core-side client for the provider list protocol
// (the ListResource server-streaming RPC added in protocol 5.10 / 6.10).
//
// These methods are deliberately not part of providers.Interface: only the
// concrete GRPC provider handles speak the list protocol, and every other
// implementation of that interface in the tree (mocks, test doubles, the
// unmanaged-provider shims) has no business growing a list method it cannot
// serve. Callers that want to list should type-assert for the methods they
// need; internal/stateless/listclient does exactly that.

// ListResource enumerates the live instances of one managed resource type,
// buffering the whole stream. Use ListResourceStream instead when the result
// set may be large or when the caller wants to stop early.
func (p *GRPCProvider) ListResource(ctx context.Context, r providers.ListResourceRequest) (resp providers.ListResourceResponse) {
	resp.Diagnostics = p.ListResourceStream(ctx, r, func(ev providers.ListResourceEvent) bool {
		resp.Events = append(resp.Events, ev)
		return true
	})
	return resp
}

// ListResourceStream enumerates the live instances of one managed resource
// type, calling emit once per result as the provider sends it. Returning
// false from emit ends the stream early and is not an error.
//
// A provider that does not implement the list protocol at all, or that does
// not list this particular type, produces an error diagnostic saying so; it
// never panics and never reports a transport failure for that case.
func (p *GRPCProvider) ListResourceStream(ctx context.Context, r providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	logger.Trace("GRPCProvider: ListResourceStream")
	var diags tfdiags.Diagnostics

	schema := p.GetProviderSchema(ctx)
	if schema.Diagnostics.HasErrors() {
		return schema.Diagnostics
	}

	listSchema, ok := schema.ListResourceTypes[r.TypeName]
	if !ok {
		return diags.Append(listUnsupportedDiag(r.TypeName, len(schema.ListResourceTypes)))
	}
	if listSchema.Block == nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid list resource schema",
			fmt.Sprintf("The provider offered a list resource schema for %q with no configuration block. This is a bug in the provider.", r.TypeName),
		))
	}

	// A ListResource result is typed by the *managed* resource type of the
	// same name: its identity schema types the identity, and its resource
	// schema types the optional full object.
	resSchema, hasResSchema := schema.ResourceTypes[r.TypeName]

	configTy := listSchema.Block.ImpliedType()
	config := r.Config
	if config == cty.NilVal {
		config = cty.NullVal(configTy)
	}
	configMP, err := msgpack.Marshal(config, configTy)
	if err != nil {
		return diags.Append(fmt.Errorf("failed to encode list configuration for %q: %w", r.TypeName, err))
	}

	protoReq := &proto.ListResource_Request{
		TypeName:              r.TypeName,
		Config:                &proto.DynamicValue{Msgpack: configMP},
		IncludeResourceObject: r.IncludeResourceObject,
		Limit:                 r.Limit,
	}

	// Listing a large account can produce individually large messages when
	// full resource objects are included, so raise the 4MB default the same
	// way the schema call does.
	const maxRecvSize = 64 << 20
	stream, err := p.client.ListResource(ctx, protoReq, grpc.MaxRecvMsgSizeCallOption{MaxRecvMsgSize: maxRecvSize})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return diags.Append(listUnimplementedDiag(r.TypeName))
		}
		return diags.Append(grpcErr(err))
	}

	for {
		protoEv, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return diags
		}
		if err != nil {
			// A server that does not register the ListResource handler
			// reports Unimplemented on the first Recv rather than when the
			// stream is opened, so the check has to happen in both places.
			if status.Code(err) == codes.Unimplemented {
				return diags.Append(listUnimplementedDiag(r.TypeName))
			}
			return diags.Append(grpcErr(err))
		}

		ev, evDiags := decodeListEvent(r.TypeName, protoEv, resSchema, hasResSchema)
		diags = diags.Append(evDiags)
		if !emit(ev) {
			return diags
		}
	}
}

// decodeListEvent turns one wire event into a providers.ListResourceEvent.
// The returned diagnostics are call-level problems (a decode failure); the
// provider's own per-event diagnostics ride along on the event itself.
func decodeListEvent(typeName string, protoEv *proto.ListResource_Event, resSchema providers.Schema, hasResSchema bool) (providers.ListResourceEvent, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	ev := providers.ListResourceEvent{
		DisplayName:    protoEv.DisplayName,
		Identity:       cty.NilVal,
		ResourceObject: cty.NilVal,
	}
	ev.Diagnostics = ev.Diagnostics.Append(convert.ProtoToDiagnostics(protoEv.Diagnostic))

	if protoEv.Identity != nil && protoEv.Identity.IdentityData != nil {
		switch {
		case !hasResSchema || resSchema.IdentitySchema == nil:
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"List result identity could not be decoded",
				fmt.Sprintf("The provider returned identity data for a %q result but serves no resource identity schema for that type, so the identity was dropped.", typeName),
			))
		default:
			identity, err := decodeDynamicValue(protoEv.Identity.IdentityData, resSchema.IdentitySchema.ImpliedType())
			if err != nil {
				diags = diags.Append(fmt.Errorf("failed to decode identity of a %q list result: %w", typeName, err))
			} else {
				ev.Identity = identity
			}
		}
	}

	if protoEv.ResourceObject != nil {
		if !hasResSchema || resSchema.Block == nil {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"List result object could not be decoded",
				fmt.Sprintf("The provider returned a full resource object for a %q result but serves no managed resource schema for that type, so the object was dropped.", typeName),
			))
		} else {
			obj, err := decodeDynamicValue(protoEv.ResourceObject, resSchema.Block.ImpliedType())
			if err != nil {
				diags = diags.Append(fmt.Errorf("failed to decode the resource object of a %q list result: %w", typeName, err))
			} else {
				ev.ResourceObject = obj
			}
		}
	}

	return ev, diags
}

// listUnsupportedDiag reports that this provider cannot list the requested
// type, distinguishing "lists nothing at all" from "lists other types".
func listUnsupportedDiag(typeName string, listable int) tfdiags.Diagnostic {
	if listable == 0 {
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

// listUnimplementedDiag reports that the provider's server has no
// ListResource handler at all, which is what an older provider answers.
func listUnimplementedDiag(typeName string) tfdiags.Diagnostic {
	return tfdiags.Sourceless(
		tfdiags.Error,
		"Provider does not implement the list protocol",
		fmt.Sprintf("The provider answered the ListResource request for %q with gRPC status Unimplemented, meaning it was built against a plugin protocol revision that predates resource listing.", typeName),
	)
}
