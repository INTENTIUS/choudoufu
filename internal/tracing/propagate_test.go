// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tracing

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

func TestChildProcessEnv_replacesInheritedParent(t *testing.T) {
	NewTestTracer(t)
	ctx, span := Tracer().Start(context.Background(), "Start provider")
	defer span.End()

	base := []string{"PATH=/bin", "TRACEPARENT=00-11111111111111111111111111111111-2222222222222222-01", "tracestate=x=y"}
	env := ChildProcessEnv(ctx, base)

	var parents []string
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "TRACEPARENT=") {
			parents = append(parents, kv)
		}
		if strings.HasPrefix(strings.ToUpper(kv), "TRACESTATE=") {
			t.Errorf("inherited trace state kept: %q", kv)
		}
	}
	if len(parents) != 1 {
		t.Fatalf("got %d TRACEPARENT entries, want 1: %v", len(parents), env)
	}
	sc := span.SpanContext()
	want := "TRACEPARENT=00-" + sc.TraceID().String() + "-" + sc.SpanID().String() + "-01"
	if parents[0] != want {
		t.Errorf("TRACEPARENT = %q, want %q", parents[0], want)
	}
	if env[0] != "PATH=/bin" {
		t.Errorf("other variables lost: %v", env)
	}
}

func TestChildProcessEnv_noSpanLeavesBase(t *testing.T) {
	base := []string{"TRACEPARENT=00-11111111111111111111111111111111-2222222222222222-01"}
	got := ChildProcessEnv(context.Background(), base)
	if len(got) != 1 || got[0] != base[0] {
		t.Errorf("ChildProcessEnv without a span = %v, want base", got)
	}
}

func TestOutgoingGRPCContext(t *testing.T) {
	NewTestTracer(t)
	ctx, span := Tracer().Start(context.Background(), "tfplugin5.Provider/ReadResource")
	defer span.End()
	ctx = metadata.AppendToOutgoingContext(ctx, "other", "kept")

	md, _ := metadata.FromOutgoingContext(OutgoingGRPCContext(ctx))
	tp := md.Get("traceparent")
	if len(tp) != 1 || !strings.Contains(tp[0], span.SpanContext().SpanID().String()) {
		t.Errorf("traceparent = %v, want the span's ID", tp)
	}
	if md.Get("other")[0] != "kept" {
		t.Error("existing metadata lost")
	}
	if got := OutgoingGRPCContext(context.Background()); got != context.Background() {
		t.Error("a context without a span should come back unchanged")
	}
}

func TestMarkRefused(t *testing.T) {
	recorder := NewTestTracer(t)
	_, span := Tracer().Start(context.Background(), "live-wave-apply digest")
	MarkRefused(span, "digest", "set digest does not match")
	span.End()

	s := SpansNamed(recorder.Ended(), "live-wave-apply digest")[0]
	if v, _ := SpanAttr(s, traceattrs.AttrRefusedStep); v.AsString() != "digest" {
		t.Errorf("refused.step = %q", v.AsString())
	}
	if s.Status().Code.String() != "Error" {
		t.Errorf("status = %v, want Error", s.Status().Code)
	}
}
