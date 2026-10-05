// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tracing

import (
	"context"
	"fmt"
	"testing"

	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

func startWalk(t *testing.T) (context.Context, *DetailRecorder, func()) {
	t.Helper()
	ctx, walk := Tracer().Start(context.Background(), "walk")
	ctx, rec := WithDetailRecorder(ctx)
	return ctx, rec, func() { walk.End() }
}

func resourceSpec(typ string, i int) DetailSpec {
	return DetailSpec{
		Kind:       DetailResourceInstance,
		Name:       "Plan resource instance changes",
		Group:      typ,
		GroupAttrs: nil,
		Label:      fmt.Sprintf("%s.r[%d]", typ, i),
	}
}

func TestStartDetail_fullNestsUnderWalk(t *testing.T) {
	recorder := NewTestTracer(t)
	SetDetailForTest(t, DetailFull, 1)

	ctx, rec, end := startWalk(t)
	for i := range 3 {
		rctx, span := StartDetail(ctx, resourceSpec("aws_s3_bucket", i))
		_, call := StartDetail(rctx, DetailSpec{Kind: DetailProviderCall, Name: "tfplugin5.Provider/PlanResourceChange", Group: "aws PlanResourceChange"})
		call.End()
		span.End()
	}
	if n := rec.Emit(ctx); n != 0 {
		t.Errorf("full mode emitted %d summary spans, want 0", n)
	}
	end()

	spans := recorder.Ended()
	walk := SpansNamed(spans, "walk")[0]
	res := SpansNamed(spans, "Plan resource instance changes")
	if len(res) != 3 {
		t.Fatalf("got %d resource spans, want 3", len(res))
	}
	for _, r := range res {
		if p := ParentOf(spans, r); p == nil || p.Name() != "walk" {
			t.Errorf("resource span parent = %v, want walk", p)
		}
	}
	for _, c := range SpansNamed(spans, "tfplugin5.Provider/PlanResourceChange") {
		if p := ParentOf(spans, c); p == nil || p.Name() != "Plan resource instance changes" {
			t.Errorf("provider call parent = %v, want the resource span", p)
		}
		if c.SpanContext().TraceID() != walk.SpanContext().TraceID() {
			t.Error("provider call is in another trace")
		}
	}
}

func TestStartDetail_aggregateEmitsOnlySummaries(t *testing.T) {
	recorder := NewTestTracer(t)
	SetDetailForTest(t, DetailAggregate, 1000)

	ctx, rec, end := startWalk(t)
	for i := range 50 {
		rctx, span := StartDetail(ctx, resourceSpec("aws_s3_bucket", i))
		if rctx != ctx {
			t.Fatal("aggregate mode must hand back the caller's context")
		}
		span.End()
	}
	for i := range 20 {
		_, span := StartDetail(ctx, resourceSpec("aws_iam_role", i))
		span.End()
	}
	if n := rec.Emit(ctx); n != 2 {
		t.Errorf("emitted %d summary spans, want 2", n)
	}
	end()

	spans := recorder.Ended()
	if got := len(SpansNamed(spans, "Plan resource instance changes")); got != 0 {
		t.Errorf("aggregate mode emitted %d detail spans", got)
	}
	sums := SpansNamed(spans, AggregateSpanPrefix+"Plan resource instance changes")
	if len(sums) != 2 {
		t.Fatalf("got %d summary spans, want 2", len(sums))
	}
	counts := map[int64]bool{}
	for _, s := range sums {
		if p := ParentOf(spans, s); p == nil || p.Name() != "walk" {
			t.Errorf("summary parent = %v, want walk", p)
		}
		v, ok := SpanAttr(s, traceattrs.AttrAggregateCount)
		if !ok {
			t.Fatal("summary has no count")
		}
		counts[v.AsInt64()] = true
		if v, _ := SpanAttr(s, traceattrs.AttrAggregateDetailedCount); v.AsInt64() != 0 {
			t.Errorf("detailed_count = %d, want 0", v.AsInt64())
		}
		if _, ok := SpanAttr(s, traceattrs.AttrAggregateSlowest); !ok {
			t.Error("summary has no slowest label")
		}
	}
	if !counts[50] || !counts[20] {
		t.Errorf("summary counts = %v, want 50 and 20", counts)
	}
}

func TestStartDetail_autoStopsAtBudget(t *testing.T) {
	recorder := NewTestTracer(t)
	SetDetailForTest(t, DetailAuto, 10)

	ctx, rec, end := startWalk(t)
	for i := range 25 {
		_, span := StartDetail(ctx, resourceSpec("aws_s3_bucket", i))
		span.End()
	}
	if got := rec.DetailedCount(); got != 10 {
		t.Errorf("DetailedCount = %d, want 10", got)
	}
	if n := rec.Emit(ctx); n != 1 {
		t.Errorf("emitted %d summary spans, want 1", n)
	}
	end()

	spans := recorder.Ended()
	if got := len(SpansNamed(spans, "Plan resource instance changes")); got != 10 {
		t.Errorf("got %d detail spans, want the budget of 10", got)
	}
	sum := SpansNamed(spans, AggregateSpanPrefix+"Plan resource instance changes")[0]
	if v, _ := SpanAttr(sum, traceattrs.AttrAggregateCount); v.AsInt64() != 25 {
		t.Errorf("count = %d, want 25", v.AsInt64())
	}
	if v, _ := SpanAttr(sum, traceattrs.AttrAggregateDetailedCount); v.AsInt64() != 10 {
		t.Errorf("detailed_count = %d, want 10", v.AsInt64())
	}
}

func TestStartDetail_autoUnderBudgetEmitsNoSummary(t *testing.T) {
	NewTestTracer(t)
	SetDetailForTest(t, DetailAuto, 10)

	ctx, rec, end := startWalk(t)
	defer end()
	for i := range 5 {
		_, span := StartDetail(ctx, resourceSpec("aws_s3_bucket", i))
		span.End()
	}
	if n := rec.Emit(ctx); n != 0 {
		t.Errorf("emitted %d summary spans under budget, want 0", n)
	}
}

// The span budget the issue asks for: a large set in aggregate mode stays
// under MaxAggregateSpans however many resources and types it has.
func TestStartDetail_aggregateBudgetForLargeSet(t *testing.T) {
	recorder := NewTestTracer(t)
	SetDetailForTest(t, DetailAggregate, DefaultDetailBudget)

	ctx, rec, end := startWalk(t)
	for typ := range 400 {
		for i := range 25 {
			_, span := StartDetail(ctx, resourceSpec(fmt.Sprintf("t%03d", typ), i))
			span.End()
		}
	}
	n := rec.Emit(ctx)
	end()
	if n != MaxAggregateSpans {
		t.Errorf("emitted %d summary spans, want the cap %d", n, MaxAggregateSpans)
	}
	// 10000 resources, 400 types: the whole trace is the walk plus the cap.
	if got := len(recorder.Ended()); got != MaxAggregateSpans+1 {
		t.Errorf("trace has %d spans, want %d", got, MaxAggregateSpans+1)
	}
	other := SpansNamed(recorder.Ended(), AggregateSpanPrefix+"other")
	if len(other) != 1 {
		t.Fatalf("got %d 'other' summary spans, want 1", len(other))
	}
	if v, _ := SpanAttr(other[0], traceattrs.AttrAggregateGroups); v.AsInt64() != 400-(MaxAggregateSpans-1) {
		t.Errorf("other folds %d groups, want %d", v.AsInt64(), 400-(MaxAggregateSpans-1))
	}
}

func TestStartDetail_noRecorderIsAlwaysDetailed(t *testing.T) {
	recorder := NewTestTracer(t)
	SetDetailForTest(t, DetailAggregate, 0)

	_, span := StartDetail(context.Background(), resourceSpec("aws_s3_bucket", 0))
	span.End()
	if got := len(SpansNamed(recorder.Ended(), "Plan resource instance changes")); got != 1 {
		t.Errorf("got %d spans outside a walk, want 1", got)
	}
}

func TestParseDetailMode(t *testing.T) {
	for in, want := range map[string]DetailMode{"": DetailAuto, "auto": DetailAuto, "FULL": DetailFull, "aggregate": DetailAggregate} {
		got, ok := ParseDetailMode(in)
		if !ok || got != want {
			t.Errorf("ParseDetailMode(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseDetailMode("loud"); ok {
		t.Error("ParseDetailMode accepted an unknown mode")
	}
}

func TestConfigureDetailFromEnv(t *testing.T) {
	NewTestTracer(t) // restores the mode at cleanup
	t.Setenv(DetailModeEnvVar, "aggregate")
	t.Setenv(DetailBudgetEnvVar, "17")
	configureDetailFromEnv()
	if m, b := CurrentDetail(); m != DetailAggregate || b != 17 {
		t.Errorf("CurrentDetail = %v, %d; want aggregate, 17", m, b)
	}
	t.Setenv(DetailBudgetEnvVar, "-3")
	configureDetailFromEnv()
	if _, b := CurrentDetail(); b != DefaultDetailBudget {
		t.Errorf("a negative budget gave %d, want the default", b)
	}
}
