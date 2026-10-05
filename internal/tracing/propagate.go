// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tracing

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"

	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898. Handing the current span to something outside this
// process: a child process through TRACEPARENT and TRACESTATE (the same
// variables [OpenTelemetryInit] reads), or a gRPC server through the
// "traceparent" metadata key that otelgrpc's server handler reads.

// TraceEnv returns "TRACEPARENT=..." (and "TRACESTATE=..." when there is a
// trace state) for the span in ctx, or nil when ctx carries no valid span.
func TraceEnv(ctx context.Context) []string {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return nil
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	var ret []string
	if v := carrier.Get("traceparent"); v != "" {
		ret = append(ret, traceParentEnvVar+"="+v)
	}
	if v := carrier.Get("tracestate"); v != "" {
		ret = append(ret, traceStateEnvVar+"="+v)
	}
	return ret
}

// ChildProcessEnv returns base with the span in ctx as the child's trace
// parent. TRACEPARENT and TRACESTATE already in base (inherited from whoever
// started this process) are replaced, so the child nests under this
// process's span rather than beside it. When ctx carries no valid span, base
// comes back unchanged.
func ChildProcessEnv(ctx context.Context, base []string) []string {
	add := TraceEnv(ctx)
	if len(add) == 0 {
		return base
	}
	ret := make([]string, 0, len(base)+len(add))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, traceParentEnvVar) || strings.EqualFold(name, traceStateEnvVar) {
			continue
		}
		ret = append(ret, kv)
	}
	return append(ret, add...)
}

// OutgoingGRPCContext returns ctx with the span in ctx set as the
// "traceparent" (and "tracestate") of outgoing gRPC metadata. When ctx
// carries no valid span, ctx comes back unchanged.
func OutgoingGRPCContext(ctx context.Context) context.Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		md = md.Copy()
	} else {
		md = metadata.MD{}
	}
	propagation.TraceContext{}.Inject(ctx, metadataCarrier(md))
	return metadata.NewOutgoingContext(ctx, md)
}

type metadataCarrier metadata.MD

func (c metadataCarrier) Get(key string) string {
	if v := metadata.MD(c).Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

func (c metadataCarrier) Set(key, value string) { metadata.MD(c).Set(key, value) }

func (c metadataCarrier) Keys() []string {
	ret := make([]string, 0, len(c))
	for k := range c {
		ret = append(ret, k)
	}
	return ret
}

// MarkRefused records on span that the step it covers refused to go on: a
// wave gate, a set digest mismatch, a resume that does not match. step is a
// short fixed name for the gate ("digest", "resume", "approval", ...) and
// reason a fixed phrase, never a resource attribute value. The span's status
// becomes Error, so a trace viewer shows the refusal at the step itself.
func MarkRefused(span Span, step, reason string) {
	if span == nil {
		return
	}
	span.SetAttributes(
		traceattrs.Bool(traceattrs.AttrRefused, true),
		traceattrs.String(traceattrs.AttrRefusedStep, step),
		traceattrs.String(traceattrs.AttrRefusedReason, reason),
	)
	span.SetStatus(codes.Error, "refused at "+step)
}
