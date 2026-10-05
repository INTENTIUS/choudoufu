// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package setplan

import (
	"context"
	"time"

	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898. Spans for a set plan: one per root, one per stage
// under it (each stage's child process gets the stage span as its
// TRACEPARENT, see [Exec]), and one for the set digest.
const (
	TraceNameRoot   = "Set plan root"
	TraceNameStage  = "Set plan stage"
	TraceNameDigest = "Set digest"
)

// runRootTraced is runRoot inside a "Set plan root" span.
func runRootTraced(ctx context.Context, runner Runner, base string, p rootPaths, now func() time.Time) Root {
	ctx, span := tracing.Tracer().Start(ctx, TraceNameRoot,
		tracing.SpanAttributes(traceattrs.String(traceattrs.AttrRoot, p.rel)),
	)
	defer span.End()
	r := runRoot(ctx, runner, base, p, now)
	span.SetAttributes(
		traceattrs.String(traceattrs.AttrStatus, string(r.Status)),
		traceattrs.Bool("choudoufu.changes", r.Changes),
	)
	if r.Estate != "" {
		span.SetAttributes(traceattrs.String(traceattrs.AttrEstate, r.Estate))
	}
	if r.Status != StatusPlanned {
		span.SetAttributes(traceattrs.String(traceattrs.AttrStage, string(r.Stage)))
		tracing.SetSpanError(span, "root failed at stage "+string(r.Stage))
	}
	return r
}

// traceStage runs one stage inside a "Set plan stage" span.
func traceStage(ctx context.Context, stage Stage, run func(ctx context.Context) error) error {
	ctx, span := tracing.Tracer().Start(ctx, TraceNameStage,
		tracing.SpanAttributes(traceattrs.String(traceattrs.AttrStage, string(stage))),
	)
	defer span.End()
	err := run(ctx)
	if err != nil {
		// A stage error's text is the child's stderr: diagnostics that can
		// quote configuration. Only the stage name goes on the span.
		tracing.SetSpanError(span, "stage "+string(stage)+" failed")
	}
	return err
}

// digestDocumentTraced is digestDocument inside a "Set digest" span.
func digestDocumentTraced(ctx context.Context, doc *Document) error {
	_, span := tracing.Tracer().Start(ctx, TraceNameDigest,
		tracing.SpanAttributes(traceattrs.Int64(traceattrs.AttrRoots, int64(len(doc.Roots)))),
	)
	defer span.End()
	err := digestDocument(doc)
	if err != nil {
		tracing.SetSpanError(span, err)
		return err
	}
	if doc.Digest != "" {
		span.SetAttributes(traceattrs.String(traceattrs.AttrSetDigest, doc.Digest))
	}
	return nil
}
