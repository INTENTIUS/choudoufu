// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statemgr

import (
	"context"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898: lock wait, state read and state write are spans under
// the caller's span, and the lock wait counts its attempts.
func TestStateSpans(t *testing.T) {
	recorder := tracing.NewTestTracer(t)
	mgr := NewFullFake(nil, states.NewState())

	ctx, root := tracing.Tracer().Start(context.Background(), "operation")

	// Hold the lock, and let it go after the first attempt fails, so the
	// wait takes two attempts.
	if _, err := mgr.Lock(ctx, NewLockInfo()); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = mgr.Unlock(context.Background(), "placeholder")
	}()
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info := NewLockInfo()
	info.Operation = "OperationTypeApply"
	id, err := LockWithContext(lockCtx, mgr, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := Refresh(ctx, mgr); err != nil {
		t.Fatal(err)
	}
	if err := WriteAndPersist(ctx, mgr, states.NewState(), nil); err != nil {
		t.Fatal(err)
	}
	if err := Unlock(ctx, mgr, id); err != nil {
		t.Fatal(err)
	}
	root.End()

	spans := recorder.Ended()
	for _, name := range []string{TraceNameStateLockWait, TraceNameStateRead, TraceNameStateWrite, TraceNameStateUnlock} {
		got := tracing.SpansNamed(spans, name)
		if len(got) != 1 {
			t.Errorf("got %d %q spans, want 1", len(got), name)
			continue
		}
		if p := tracing.ParentOf(spans, got[0]); p == nil || p.Name() != "operation" {
			t.Errorf("%q parent = %v, want the caller's span", name, p)
		}
		if v, _ := tracing.SpanAttr(got[0], traceattrs.AttrStateBackend); v.AsString() != "*statemgr.fakeFull" {
			t.Errorf("%q backend = %q", name, v.AsString())
		}
	}
	wait := tracing.SpansNamed(spans, TraceNameStateLockWait)[0]
	if v, _ := tracing.SpanAttr(wait, traceattrs.AttrStateLockAttempts); v.AsInt64() != 2 {
		t.Errorf("lock attempts = %d, want 2", v.AsInt64())
	}
	if v, _ := tracing.SpanAttr(wait, traceattrs.AttrStateLockOperation); v.AsString() != "OperationTypeApply" {
		t.Errorf("lock operation = %q", v.AsString())
	}
}
