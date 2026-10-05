// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898: a span per walk; under it a span per resource
// instance for plan and apply, named by address and carrying the action.
func TestContext2_tracingPerResourceInstance(t *testing.T) {
	recorder := tracing.NewTestTracer(t)
	tracing.SetDetailForTest(t, tracing.DetailFull, 0)

	m := testModule(t, "plan-good")
	p := testProvider("aws")
	tfCtx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		}, nil),
	})

	ctx, root := tracing.Tracer().Start(context.Background(), "terragucci root")
	plan, diags := tfCtx.Plan(ctx, m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("plan: %s", diags.Err())
	}
	_, diags = tfCtx.Apply(ctx, plan, m, nil)
	if diags.HasErrors() {
		t.Fatalf("apply: %s", diags.Err())
	}
	root.End()

	spans := recorder.Ended()
	walkOf := func(name string, want string) {
		t.Helper()
		got := tracing.SpansNamed(spans, name)
		if len(got) != 2 {
			t.Fatalf("got %d %q spans, want 2 (aws_instance.foo and .bar)", len(got), name)
		}
		addrsSeen := map[string]bool{}
		for _, s := range got {
			w := tracing.ParentOf(spans, s)
			if w == nil || w.Name() != traceNameGraphWalk {
				t.Errorf("%q parent = %v, want %q", name, w, traceNameGraphWalk)
				continue
			}
			if op, _ := tracing.SpanAttr(w, traceAttrWalkOperation); op.AsString() != want {
				t.Errorf("%q is under the %s walk, want %s", name, op.AsString(), want)
			}
			if s.SpanContext().TraceID() != root.SpanContext().TraceID() {
				t.Errorf("%q is in another trace than the caller's span", name)
			}
			a, _ := tracing.SpanAttr(s, traceattrs.AttrResourceInstanceAddress)
			addrsSeen[a.AsString()] = true
			if act, ok := tracing.SpanAttr(s, traceattrs.AttrResourceInstanceAction); !ok || act.AsString() != "Create" {
				t.Errorf("%q for %s: action = %q, want Create", name, a.AsString(), act.AsString())
			}
		}
		if !addrsSeen["aws_instance.foo"] || !addrsSeen["aws_instance.bar"] {
			t.Errorf("%q addresses = %v", name, addrsSeen)
		}
	}
	walkOf(traceNamePlanResourceInstance, walkPlan.String())
	walkOf(traceNameApplyResourceInstance, walkApply.String())

	// Every graph walk span is a descendant of the caller's span.
	for _, w := range tracing.SpansNamed(spans, traceNameGraphWalk) {
		if w.SpanContext().TraceID() != root.SpanContext().TraceID() {
			t.Error("a graph walk started its own trace")
		}
	}
	if n := len(tracing.SpansNamed(spans, tracing.AggregateSpanPrefix+traceNamePlanResourceInstance)); n != 0 {
		t.Errorf("full mode wrote %d summary spans", n)
	}
}

// In aggregate mode the walk has no per-instance span, and one summary span
// per resource type and phase.
func TestContext2_tracingAggregate(t *testing.T) {
	recorder := tracing.NewTestTracer(t)
	tracing.SetDetailForTest(t, tracing.DetailAggregate, 0)

	m := testModule(t, "plan-good")
	p := testProvider("aws")
	tfCtx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		}, nil),
	})
	if _, diags := tfCtx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts); diags.HasErrors() {
		t.Fatalf("plan: %s", diags.Err())
	}

	spans := recorder.Ended()
	if n := len(tracing.SpansNamed(spans, traceNamePlanResourceInstance)); n != 0 {
		t.Errorf("aggregate mode wrote %d per-instance spans", n)
	}
	sums := tracing.SpansNamed(spans, tracing.AggregateSpanPrefix+traceNamePlanResourceInstance)
	if len(sums) != 1 {
		t.Fatalf("got %d plan summary spans, want 1 (one resource type)", len(sums))
	}
	s := sums[0]
	if w := tracing.ParentOf(spans, s); w == nil || w.Name() != traceNameGraphWalk {
		t.Errorf("summary parent = %v, want %q", w, traceNameGraphWalk)
	}
	if v, _ := tracing.SpanAttr(s, traceattrs.AttrAggregateCount); v.AsInt64() != 2 {
		t.Errorf("count = %d, want 2", v.AsInt64())
	}
	if v, _ := tracing.SpanAttr(s, traceattrs.AttrResourceType); v.AsString() != "aws_instance" {
		t.Errorf("resource type = %q", v.AsString())
	}
}
