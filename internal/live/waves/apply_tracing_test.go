// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"context"
	"encoding/json"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898: a refused set shows in the trace at the step that
// refused it, in line with chant's wave gates.

func tracedApply(t *testing.T, a *applyRun, f *fakeRunner, wave int) ([]sdktrace.ReadOnlySpan, *ApplyResult) {
	t.Helper()
	recorder := tracing.NewTestTracer(t)
	ctx, root := tracing.Tracer().Start(context.Background(), "live-wave-apply")
	res, err := Apply(ctx, ApplyOptions{
		Set: a.set, Approved: a.approved, Waves: a.doc, Wave: wave,
		Resume: a.resume, Runner: f,
		Save: func(*Resume) error { return nil },
	})
	root.End()
	if err != nil {
		t.Fatal(err)
	}
	return recorder.Ended(), res
}

func refusedStep(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) string {
	t.Helper()
	got := tracing.SpansNamed(spans, name)
	if len(got) != 1 {
		t.Fatalf("got %d %q spans, want 1", len(got), name)
	}
	if p := tracing.ParentOf(spans, got[0]); p == nil || p.Name() != "live-wave-apply" {
		t.Errorf("%q parent = %v, want the command span", name, p)
	}
	if v, ok := tracing.SpanAttr(got[0], traceattrs.AttrRefused); !ok || !v.AsBool() {
		return ""
	}
	v, _ := tracing.SpanAttr(got[0], traceattrs.AttrRefusedStep)
	return v.AsString()
}

func TestApplyTracing_digestRefusal(t *testing.T) {
	a := newApplyRun(t)
	a.set.Roots[0].Plan = json.RawMessage(`{"resource_changes":[]}`)
	spans, res := tracedApply(t, a, a.runner(), 1)
	if res.ExitCode != ExitSetMoved {
		t.Fatalf("exit %d", res.ExitCode)
	}
	if got := refusedStep(t, spans, TraceNameDigest); got != RefusedStepDigest {
		t.Errorf("refused step = %q, want %q", got, RefusedStepDigest)
	}
	if n := len(tracing.SpansNamed(spans, TraceNameFreshPlan)); n != 0 {
		t.Errorf("a set refused at its digest still planned (%d spans)", n)
	}
}

func TestApplyTracing_resumeRefusal(t *testing.T) {
	a := newApplyRun(t)
	a.resume.SetDigest = "sha256:another"
	spans, res := tracedApply(t, a, a.runner(), 1)
	if res.ExitCode != ExitError {
		t.Fatalf("exit %d", res.ExitCode)
	}
	if got := refusedStep(t, spans, TraceNameDigest); got != "" {
		t.Errorf("the digest gate refused (%q); it should have passed", got)
	}
	if got := refusedStep(t, spans, TraceNameResume); got != RefusedStepResume {
		t.Errorf("refused step = %q, want %q", got, RefusedStepResume)
	}
}

func TestApplyTracing_freshPlanRefusal(t *testing.T) {
	a := newApplyRun(t)
	f := a.runner()
	f.moved["estates/e04"] = true
	spans, res := tracedApply(t, a, f, 1)
	if res.ExitCode != ExitSetMoved {
		t.Fatalf("exit %d", res.ExitCode)
	}
	if got := refusedStep(t, spans, TraceNameFreshPlan); got != RefusedStepFreshPlan {
		t.Errorf("refused step = %q, want %q", got, RefusedStepFreshPlan)
	}
	if n := len(tracing.SpansNamed(spans, TraceNameApplyRoot)); n != 0 {
		t.Errorf("a refused wave has %d apply spans", n)
	}
}

func TestApplyTracing_applySpansPerRoot(t *testing.T) {
	a := newApplyRun(t)
	spans, res := tracedApply(t, a, a.runner(), 1)
	if res.ExitCode != ExitApplied {
		t.Fatalf("exit %d: %s", res.ExitCode, res.Error)
	}
	applies := tracing.SpansNamed(spans, TraceNameApplyRoot)
	if len(applies) != 3 {
		t.Fatalf("got %d apply spans, want 3 (wave 1's roots)", len(applies))
	}
	for _, s := range applies {
		if p := tracing.ParentOf(spans, s); p == nil || p.Name() != "live-wave-apply" {
			t.Errorf("apply span parent = %v", p)
		}
		if v, _ := tracing.SpanAttr(s, traceattrs.AttrOutcome); v.AsString() != OutcomeLanded {
			t.Errorf("outcome = %q", v.AsString())
		}
	}
}
