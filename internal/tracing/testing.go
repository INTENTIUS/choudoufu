// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// GitHub issue #1898. Test helpers for checking span names and parents from
// any package's tests. They swap process-wide state, so a test that uses them
// must not call t.Parallel.

// NewTestTracer turns tracing on for the rest of the test with an in-memory
// recorder as the global tracer provider, and restores the previous state at
// cleanup. Read the finished spans with recorder.Ended().
func NewTestTracer(t testing.TB) *tracetest.SpanRecorder {
	t.Helper()
	prevProvider := otel.GetTracerProvider()
	prevEnabled := isTracingEnabled
	prevMode, prevBudget := CurrentDetail()

	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	isTracingEnabled = true

	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevProvider)
		isTracingEnabled = prevEnabled
		setDetail(prevMode, prevBudget)
	})
	return rec
}

// SetDetailForTest sets the detail mode and budget until the test ends.
// Pair it with [NewTestTracer], whose cleanup restores the previous values.
func SetDetailForTest(t testing.TB, mode DetailMode, budget int) {
	t.Helper()
	setDetail(mode, budget)
}

// SpansNamed returns the ended spans with the given name, in end order.
func SpansNamed(spans []sdktrace.ReadOnlySpan, name string) []sdktrace.ReadOnlySpan {
	var ret []sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == name {
			ret = append(ret, s)
		}
	}
	return ret
}

// ParentOf returns the ended span that is s's parent, or nil.
func ParentOf(spans []sdktrace.ReadOnlySpan, s sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	want := s.Parent().SpanID()
	if !want.IsValid() {
		return nil
	}
	for _, c := range spans {
		if c.SpanContext().SpanID() == want {
			return c
		}
	}
	return nil
}

// SpanAttr returns the value of the named attribute on s, and whether it was set.
func SpanAttr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// TraceIDOf is the trace ID of the span in ctx, for tests that compare traces.
func TraceIDOf(ctx context.Context) trace.TraceID {
	return trace.SpanContextFromContext(ctx).TraceID()
}
