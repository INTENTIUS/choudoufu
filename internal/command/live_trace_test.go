// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/intentius/choudoufu/internal/tracing"
)

// TestLiveCheckSpans pins GitHub issue #1856's span shape for live-check:
// one "live-check" span joined to the caller's TRACEPARENT, with a child span
// for each of loading, reading provider schemas and analysing.
func TestLiveCheckSpans(t *testing.T) {
	rec := tracing.NewTestTracer(t)
	ctx, remote := liveTraceparentContext(t)

	c, done := newLiveCheckCommand(t)
	report := c.liveCheck(ctx, "../../live/e2e/estate")
	done(t)
	if !report.Readable() {
		t.Fatalf("live/e2e/estate did not load: %s", report.Load.Diags.Error())
	}

	root := requireLiveSpanTree(t, rec.Ended(), remote, "live-check",
		"live-check.load",
		"live-check.schemas",
		"live-check.analyze",
	)
	requireLiveSpanAttrs(t, root, "live.readable", "live.estate", "live.instances", "live.sites", "live.refusals")
}

// liveTraceparentContext is what tracing.OpenTelemetryInit builds from the
// TRACEPARENT environment variable: a context carrying a remote parent span.
func liveTraceparentContext(t *testing.T) (context.Context, trace.SpanContext) {
	t.Helper()
	const traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	carrier := propagation.MapCarrier{}
	carrier.Set("traceparent", traceparent)
	ctx := propagation.TraceContext{}.Extract(context.Background(), carrier)
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() || !sc.IsRemote() {
		t.Fatalf("traceparent %q did not extract to a remote span context", traceparent)
	}
	return ctx, sc
}

// requireLiveSpanTree fails unless root is one finished span that joins the
// remote parent, and each name in children is one finished span whose parent
// is root.
func requireLiveSpanTree(t *testing.T, spans []sdktrace.ReadOnlySpan, remote trace.SpanContext, root string, children ...string) sdktrace.ReadOnlySpan {
	t.Helper()
	roots := tracing.SpansNamed(spans, root)
	if len(roots) != 1 {
		t.Fatalf("got %d %q spans, want 1", len(roots), root)
	}
	r := roots[0]
	if r.Parent().SpanID() != remote.SpanID() || r.SpanContext().TraceID() != remote.TraceID() {
		t.Errorf("%q has parent %s in trace %s, want the TRACEPARENT span %s in trace %s",
			root, r.Parent().SpanID(), r.SpanContext().TraceID(), remote.SpanID(), remote.TraceID())
	}
	for _, name := range children {
		found := tracing.SpansNamed(spans, name)
		if len(found) != 1 {
			t.Errorf("got %d %q spans, want 1", len(found), name)
			continue
		}
		if p := tracing.ParentOf(spans, found[0]); p == nil || p.Name() != root {
			t.Errorf("%q is not a child of %q", name, root)
		}
	}
	return r
}

func requireLiveSpanAttrs(t *testing.T, s sdktrace.ReadOnlySpan, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := tracing.SpanAttr(s, k); !ok {
			t.Errorf("%q has no %q attribute", s.Name(), k)
		}
	}
}
