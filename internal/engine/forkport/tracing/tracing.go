// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package tracing is a choudoufu fork port (#2381, unit W4 of #2256): the
// fork's per-walk and per-resource-instance OpenTelemetry spans (GitHub
// issue #1898) on OpenTofu's new runtime (internal/engine).
//
// The default runtime opens a "Graph walk" span for each plan and apply,
// and under it a detail span per resource instance, counted against the
// span budget and folded into aggregate summaries over it
// (internal/tracing's DetailRecorder). The new runtime has no graph walk,
// so none of these exist there. Its planning and applying engines do call
// back at the start and end of each managed resource instance (their
// Tracer types); internal/tofu's shim already builds those callbacks to
// drive its hooks, and calls [Walk], [StartInstance] and [EndInstance]
// from them to open the same spans.
//
// It is the whole port. Each call site in the shim is commented
// "choudoufu fork port (#2381): remove when the row closes". Removing the
// port is deleting this package, those call sites and the "tracing-spans"
// row of internal/live/newruntime/gaps.json. No upstream issue tracks the
// gap: upstream's own tracing of internal/engine closes it.
//
// It is registered in internal/engine/forkport as [Name]. Switched off
// (forkport.Enabled), every function here returns its context unchanged
// and opens no span, as before the port.
package tracing

import (
	"context"

	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// Name is the port's name in the internal/engine/forkport registry.
const Name = "tracing"

// Walk opens the span for one plan or apply, named name and carrying the
// attribute opKey = op, and its detail recorder, as the default runtime's
// graph walk does. end closes both: call it with the run's diagnostics
// once the engine has returned.
func Walk(ctx context.Context, name, opKey, op string) (_ context.Context, end func(tfdiags.Diagnostics)) {
	if !forkport.Enabled(Name) {
		return ctx, func(tfdiags.Diagnostics) {}
	}
	ctx, span := tracing.Tracer().Start(ctx, name, tracing.SpanAttributes(traceattrs.String(opKey, op)))
	ctx, detail := tracing.WithDetailRecorder(ctx)
	return ctx, func(diags tfdiags.Diagnostics) {
		detail.Emit(ctx)
		tracing.SetSpanError(span, diags)
		span.End()
	}
}

// instanceSpanKey carries the span [StartInstance] opened to [EndInstance].
// A counted detail span leaves the context's current span alone, so the
// span cannot be found from the context's span.
type instanceSpanKey struct{}

// StartInstance opens the detail span for one resource instance, labelled
// with its address and resource type, and returns the context the engine
// passes back to [EndInstance]. action, when not empty, is the change the
// instance carries, such as "Create"; the apply knows it at the start, the
// plan only at the end.
func StartInstance(ctx context.Context, spec tracing.DetailSpec, addr, resourceType, action string) context.Context {
	if !forkport.Enabled(Name) {
		return ctx
	}
	ctx, span := tracing.StartDetail(ctx, spec, tracing.SpanAttributes(
		traceattrs.OpenTofuResourceInstanceAddress(addr),
		traceattrs.OpenTofuResourceType(resourceType),
	))
	setAction(span, action)
	return context.WithValue(ctx, instanceSpanKey{}, span)
}

// EndInstance records the instance's action when not empty and closes the
// span [StartInstance] opened in ctx. Without one it does nothing.
func EndInstance(ctx context.Context, action string, diags tfdiags.Diagnostics) {
	span, ok := ctx.Value(instanceSpanKey{}).(tracing.Span)
	if !ok {
		return
	}
	setAction(span, action)
	tracing.SetSpanError(span, diags)
	span.End()
}

func setAction(span tracing.Span, action string) {
	if action != "" && span.IsRecording() {
		span.SetAttributes(traceattrs.OpenTofuResourceInstanceAction(action))
	}
}
