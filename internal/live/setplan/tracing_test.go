// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package setplan

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898: a set plan is a span per root with a span per stage
// under it, then a span for the set digest, all under the caller's span.
func TestRunTracing(t *testing.T) {
	recorder := tracing.NewTestTracer(t)
	base := setup(t, "a", "b")
	runner := &fakeRunner{breakAt: map[string]Stage{"b": StagePlan}}

	ctx, root := tracing.Tracer().Start(context.Background(), "live-plan-set")
	if _, err := Run(ctx, Options{Roots: []string{"a", "b"}, BaseDir: base, OutDir: "out", Parallel: 2, Runner: runner, Now: fixedClock()}); err != nil {
		t.Fatal(err)
	}
	root.End()

	spans := recorder.Ended()
	roots := tracing.SpansNamed(spans, TraceNameRoot)
	if len(roots) != 2 {
		t.Fatalf("got %d root spans, want 2", len(roots))
	}
	for _, r := range roots {
		if p := tracing.ParentOf(spans, r); p == nil || p.Name() != "live-plan-set" {
			t.Errorf("root span parent = %v, want the command span", p)
		}
		name, _ := tracing.SpanAttr(r, traceattrs.AttrRoot)
		status, _ := tracing.SpanAttr(r, traceattrs.AttrStatus)
		switch name.AsString() {
		case "a":
			if status.AsString() != string(StatusPlanned) {
				t.Errorf("a: status %q", status.AsString())
			}
		case "b":
			if stage, _ := tracing.SpanAttr(r, traceattrs.AttrStage); stage.AsString() != string(StagePlan) {
				t.Errorf("b: failed stage %q, want plan", stage.AsString())
			}
			if r.Status().Code.String() != "Error" {
				t.Errorf("b: status code %v, want Error", r.Status().Code)
			}
		default:
			t.Errorf("unexpected root %q", name.AsString())
		}
	}

	stages := tracing.SpansNamed(spans, TraceNameStage)
	// a: init, plan, show. b: init, plan (fails).
	if len(stages) != 5 {
		t.Errorf("got %d stage spans, want 5", len(stages))
	}
	for _, s := range stages {
		if p := tracing.ParentOf(spans, s); p == nil || p.Name() != TraceNameRoot {
			t.Errorf("stage span parent = %v, want a root span", p)
		}
	}
	digests := tracing.SpansNamed(spans, TraceNameDigest)
	if len(digests) != 1 {
		t.Fatalf("got %d digest spans, want 1", len(digests))
	}
	if v, _ := tracing.SpanAttr(digests[0], traceattrs.AttrSetDigest); v.AsString() == "" {
		t.Error("digest span has no set digest")
	}
}

// Exec hands each child process the stage span as TRACEPARENT.
func TestExecChildTraceparent(t *testing.T) {
	tracing.NewTestTracer(t)
	ctx, span := tracing.Tracer().Start(context.Background(), TraceNameStage)
	defer span.End()
	env := tracing.ChildProcessEnv(ctx, ChildEnv([]string{"PATH=/bin"}, "/cache"))
	want := "TRACEPARENT=00-" + span.SpanContext().TraceID().String() + "-" + span.SpanContext().SpanID().String() + "-01"
	found := false
	for _, kv := range env {
		if kv == want {
			found = true
		}
	}
	if !found {
		t.Errorf("child env %v has no %s", env, want)
	}
}
