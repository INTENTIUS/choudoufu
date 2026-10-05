// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tracing

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898. Provider call spans.

// ProviderCall describes one gRPC call into a provider plugin.
type ProviderCall struct {
	// Service is the gRPC service, "tfplugin5.Provider" or "tfplugin6.Provider".
	Service string
	// Method is the RPC method, such as "ReadResource".
	Method string
	// Provider is the provider source address, when the caller knows it.
	Provider string
	// TypeName is the resource or data source type the call is about, if any.
	TypeName string
	// Detail puts the span under the detail budget: true for the per-resource
	// calls (ReadResource, PlanResourceChange, ApplyResourceChange,
	// ReadDataSource, ImportResourceState), false for the once-per-provider
	// calls (GetProviderSchema, ConfigureProvider), which are always emitted.
	Detail bool
}

// ProviderCallSpanName is the span name for a provider call:
// "<service>/<method>", as the OpenTelemetry RPC conventions name a gRPC
// client span.
func ProviderCallSpanName(service, method string) string {
	return service + "/" + method
}

// StartProviderCall starts the span for a provider call and returns a context
// whose outgoing gRPC metadata carries the current span as "traceparent", so
// a provider instrumented with otelgrpc joins the trace. When the call is
// only counted (over the budget, or in aggregate mode), the metadata carries
// the enclosing span instead.
func StartProviderCall(ctx context.Context, c ProviderCall) (context.Context, Span) {
	if !isTracingEnabled {
		return ctx, noopSpan
	}
	name := ProviderCallSpanName(c.Service, c.Method)
	attrs := []attribute.KeyValue{
		traceattrs.RPCSystem("grpc"),
		traceattrs.RPCService(c.Service),
		traceattrs.RPCMethod(c.Method),
	}
	groupAttrs := append([]attribute.KeyValue(nil), attrs...)
	if c.Provider != "" {
		attrs = append(attrs, traceattrs.OpenTofuProviderAddress(c.Provider))
		groupAttrs = append(groupAttrs, traceattrs.OpenTofuProviderAddress(c.Provider))
	}
	if c.TypeName != "" {
		attrs = append(attrs, traceattrs.OpenTofuResourceType(c.TypeName))
	}
	var span Span
	if c.Detail {
		ctx, span = StartDetail(ctx, DetailSpec{
			Kind:       DetailProviderCall,
			Name:       name,
			Group:      c.Provider,
			GroupAttrs: groupAttrs,
			Label:      c.TypeName,
		}, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	} else {
		ctx, span = tracerForCaller(1).Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	}
	return OutgoingGRPCContext(ctx), span
}
