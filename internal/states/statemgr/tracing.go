// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statemgr

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898. Spans for the state backend: waiting for the lock,
// reading the persistent snapshot and writing it. Their attributes name the
// state manager's Go type, the lock operation and the attempt count; no
// state content goes on them.

const (
	// TraceNameStateLockWait covers [LockWithContext]: every attempt to take
	// the lock and every wait between attempts.
	TraceNameStateLockWait = "State lock wait"
	// TraceNameStateUnlock covers releasing the lock.
	TraceNameStateUnlock = "State unlock"
	// TraceNameStateRead covers RefreshState through [Refresh].
	TraceNameStateRead = "State read"
	// TraceNameStateWrite covers PersistState through [Persist].
	TraceNameStateWrite = "State write"
)

// stateManagerType names a state manager on a span by its Go type, such as
// "*remote.State" or "*statemgr.Filesystem".
func stateManagerType(mgr any) string {
	return fmt.Sprintf("%T", mgr)
}

// Refresh calls mgr.RefreshState inside a "State read" span.
func Refresh(ctx context.Context, mgr Refresher) error {
	ctx, span := tracing.Tracer().Start(ctx, TraceNameStateRead,
		tracing.SpanAttributes(traceattrs.String(traceattrs.AttrStateBackend, stateManagerType(mgr))),
	)
	defer span.End()
	err := mgr.RefreshState(ctx)
	if err != nil {
		tracing.SetSpanError(span, err)
	}
	return err
}

// Persist calls mgr.PersistState inside a "State write" span.
func Persist(ctx context.Context, mgr Persister, schemas *tofu.Schemas) error {
	ctx, span := tracing.Tracer().Start(ctx, TraceNameStateWrite,
		tracing.SpanAttributes(traceattrs.String(traceattrs.AttrStateBackend, stateManagerType(mgr))),
	)
	defer span.End()
	err := mgr.PersistState(ctx, schemas)
	if err != nil {
		tracing.SetSpanError(span, err)
	}
	return err
}

// Unlock calls s.Unlock inside a "State unlock" span.
func Unlock(ctx context.Context, s Locker, id string) error {
	ctx, span := tracing.Tracer().Start(ctx, TraceNameStateUnlock,
		tracing.SpanAttributes(
			traceattrs.String(traceattrs.AttrStateBackend, stateManagerType(s)),
			traceattrs.String(traceattrs.AttrStateLockID, id),
		),
	)
	defer span.End()
	err := s.Unlock(ctx, id)
	if err != nil {
		tracing.SetSpanError(span, err)
	}
	return err
}
