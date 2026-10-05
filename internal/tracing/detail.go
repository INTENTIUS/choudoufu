// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tracing

import (
	"context"
	"log"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898. Detail spans and the span budget.
//
// A detail span is one of the high-volume spans: one per resource instance in
// a graph walk, one per provider resource call. An estate with thousands of
// resources would make thousands of them, so they go through [StartDetail],
// which consults the detail mode and a per-walk [DetailRecorder]:
//
//   - full: every detail span is emitted.
//   - aggregate: no detail span is emitted. The recorder counts them and, at
//     the end of the walk, [DetailRecorder.Emit] writes one summary span per
//     group (resource type and phase, or provider and RPC method) holding the
//     count, the total and the longest duration and the slowest member's label.
//   - auto (the default): detail spans are emitted until the walk has made
//     [DetailBudgetEnvVar] of them (default [DefaultDetailBudget]); the rest
//     are counted, and the summary spans are emitted only when something was
//     counted without its own span.
//
// The stated budget, per graph walk: at most the budget's detail spans in auto
// mode, none in aggregate mode, and at most [MaxAggregateSpans] summary spans
// in either. Full mode has no bound.
//
// Outside a walk (no recorder in the context) a detail span is always emitted:
// those call sites are not the high-volume ones.

// DetailModeEnvVar selects the detail mode: "full", "aggregate" or "auto".
const DetailModeEnvVar = "CHOUDOUFU_TRACE_DETAIL"

// DetailBudgetEnvVar is the per-walk detail span budget for auto mode.
const DetailBudgetEnvVar = "CHOUDOUFU_TRACE_SPAN_BUDGET"

// DefaultDetailBudget is the auto mode budget when DetailBudgetEnvVar is unset.
const DefaultDetailBudget = 2000

// MaxAggregateSpans bounds how many summary spans one walk's Emit writes. The
// smallest groups beyond it are folded into one more summary span named with
// the "other" group.
const MaxAggregateSpans = 256

// AggregateSpanPrefix starts the name of every summary span; the rest is the
// name the members' own spans would have had.
const AggregateSpanPrefix = "Aggregate: "

// DetailMode is one of the three detail modes.
type DetailMode int

const (
	DetailAuto DetailMode = iota
	DetailFull
	DetailAggregate
)

func (m DetailMode) String() string {
	switch m {
	case DetailFull:
		return "full"
	case DetailAggregate:
		return "aggregate"
	default:
		return "auto"
	}
}

// ParseDetailMode reads a mode name. An empty string is auto.
func ParseDetailMode(s string) (DetailMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return DetailAuto, true
	case "full", "all":
		return DetailFull, true
	case "aggregate", "summary":
		return DetailAggregate, true
	}
	return DetailAuto, false
}

var (
	detailMu     sync.RWMutex
	detailMode   = DetailAuto
	detailBudget = DefaultDetailBudget
)

// configureDetailFromEnv is called by [OpenTelemetryInit].
func configureDetailFromEnv() {
	mode, ok := ParseDetailMode(os.Getenv(DetailModeEnvVar))
	if !ok {
		log.Printf("[WARN] OpenTelemetry: %s=%q is not one of full, aggregate, auto; using auto", DetailModeEnvVar, os.Getenv(DetailModeEnvVar))
	}
	budget := DefaultDetailBudget
	if raw := os.Getenv(DetailBudgetEnvVar); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			budget = n
		} else {
			log.Printf("[WARN] OpenTelemetry: %s=%q is not a non-negative integer; using %d", DetailBudgetEnvVar, raw, DefaultDetailBudget)
		}
	}
	setDetail(mode, budget)
}

func setDetail(mode DetailMode, budget int) {
	detailMu.Lock()
	detailMode, detailBudget = mode, budget
	detailMu.Unlock()
}

// CurrentDetail returns the configured detail mode and auto mode budget.
func CurrentDetail() (DetailMode, int) {
	detailMu.RLock()
	defer detailMu.RUnlock()
	return detailMode, detailBudget
}

// DetailKind names what a detail span is about. It is the
// choudoufu.aggregate.kind attribute of a summary span.
type DetailKind string

const (
	DetailResourceInstance DetailKind = "resource_instance"
	DetailProviderCall     DetailKind = "provider_call"
)

// DetailSpec describes one detail span.
type DetailSpec struct {
	Kind DetailKind
	// Name is the span name. Members of one group share it.
	Name string
	// Group is the aggregation key within Kind and Name, such as a resource
	// type, or a provider address and RPC method joined by a space.
	Group string
	// GroupAttrs are copied onto the group's summary span. Types, addresses
	// and method names only, never values.
	GroupAttrs []attribute.KeyValue
	// Label identifies this member in a summary's choudoufu.aggregate.slowest
	// attribute, such as a resource instance address.
	Label string
}

// DetailRecorder holds one graph walk's detail span count and its groups.
type DetailRecorder struct {
	mode   DetailMode
	budget int

	mu       sync.Mutex
	detailed int
	groups   map[string]*detailGroup
}

type detailGroup struct {
	kind     DetailKind
	name     string
	group    string
	attrs    []attribute.KeyValue
	count    int
	detailed int
	total    time.Duration
	max      time.Duration
	slowest  string
	first    time.Time
	last     time.Time
}

type detailRecorderKey struct{}

// WithDetailRecorder returns a context carrying a fresh recorder for one
// walk, using the configured mode and budget. Call [DetailRecorder.Emit] with
// the walk's own context when the walk ends.
func WithDetailRecorder(ctx context.Context) (context.Context, *DetailRecorder) {
	mode, budget := CurrentDetail()
	rec := &DetailRecorder{mode: mode, budget: budget, groups: map[string]*detailGroup{}}
	return context.WithValue(ctx, detailRecorderKey{}, rec), rec
}

// DetailRecorderFromContext returns the context's recorder, or nil.
func DetailRecorderFromContext(ctx context.Context) *DetailRecorder {
	rec, _ := ctx.Value(detailRecorderKey{}).(*DetailRecorder)
	return rec
}

// tracerForCaller is [Tracer] for a caller skip frames further up.
func tracerForCaller(skip int) trace.Tracer {
	pc, _, _, ok := runtime.Caller(skip + 1)
	if !ok || runtime.FuncForPC(pc) == nil {
		return otel.Tracer("")
	}
	return otel.GetTracerProvider().Tracer(extractImportPath(runtime.FuncForPC(pc).Name()))
}

var noopSpan = noop.Span{}

// StartDetail starts a detail span, or counts one, as the context's recorder
// and the detail mode decide. When it only counts, the returned context is
// the one passed in, so anything started under it nests in the enclosing
// span, and the returned span records nothing but still must be ended.
func StartDetail(ctx context.Context, spec DetailSpec, opts ...trace.SpanStartOption) (context.Context, Span) {
	if !isTracingEnabled {
		return ctx, noopSpan
	}
	rec := DetailRecorderFromContext(ctx)
	if rec == nil {
		return tracerForCaller(1).Start(ctx, spec.Name, opts...)
	}
	g, emit := rec.admit(spec)
	if !emit {
		return ctx, &detailSpan{Span: noopSpan, rec: rec, group: g, label: spec.Label, start: time.Now()}
	}
	ctx, span := tracerForCaller(1).Start(ctx, spec.Name, opts...)
	return ctx, &detailSpan{Span: span, rec: rec, group: g, label: spec.Label, start: time.Now(), detailed: true}
}

func (r *DetailRecorder) admit(spec DetailSpec) (*detailGroup, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := string(spec.Kind) + "\x00" + spec.Name + "\x00" + spec.Group
	g, ok := r.groups[key]
	if !ok {
		g = &detailGroup{kind: spec.Kind, name: spec.Name, group: spec.Group, attrs: spec.GroupAttrs}
		r.groups[key] = g
	}
	var emit bool
	switch r.mode {
	case DetailFull:
		emit = true
	case DetailAggregate:
		emit = false
	default:
		emit = r.detailed < r.budget
	}
	if emit {
		r.detailed++
		g.detailed++
	}
	return g, emit
}

func (r *DetailRecorder) record(g *detailGroup, label string, start, end time.Time) {
	d := end.Sub(start)
	r.mu.Lock()
	defer r.mu.Unlock()
	g.count++
	g.total += d
	if d >= g.max {
		g.max = d
		g.slowest = label
	}
	if g.first.IsZero() || start.Before(g.first) {
		g.first = start
	}
	if end.After(g.last) {
		g.last = end
	}
}

// DetailedCount is how many detail spans the walk has emitted.
func (r *DetailRecorder) DetailedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.detailed
}

// Emit writes the walk's summary spans under the span in ctx, if the mode
// calls for them: always in aggregate mode, in auto mode only when some
// detail span was counted without being emitted, never in full mode. It
// returns how many summary spans it wrote.
func (r *DetailRecorder) Emit(ctx context.Context) int {
	if r == nil || !isTracingEnabled || r.mode == DetailFull {
		return 0
	}
	r.mu.Lock()
	groups := make([]*detailGroup, 0, len(r.groups))
	dropped := false
	for _, g := range r.groups {
		if g.count == 0 {
			continue
		}
		groups = append(groups, g)
		if g.count > g.detailed {
			dropped = true
		}
	}
	r.mu.Unlock()
	if len(groups) == 0 || (r.mode == DetailAuto && !dropped) {
		return 0
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].total != groups[j].total {
			return groups[i].total > groups[j].total
		}
		return groups[i].kind+DetailKind(groups[i].name+groups[i].group) < groups[j].kind+DetailKind(groups[j].name+groups[j].group)
	})
	if len(groups) > MaxAggregateSpans {
		other := &detailGroup{kind: "other", name: "other", group: "other"}
		for _, g := range groups[MaxAggregateSpans-1:] {
			other.count += g.count
			other.detailed += g.detailed
			other.total += g.total
			if g.max >= other.max {
				other.max, other.slowest = g.max, g.slowest
			}
			if other.first.IsZero() || g.first.Before(other.first) {
				other.first = g.first
			}
			if g.last.After(other.last) {
				other.last = g.last
			}
		}
		other.attrs = []attribute.KeyValue{traceattrs.Int64(traceattrs.AttrAggregateGroups, int64(len(groups)-(MaxAggregateSpans-1)))}
		groups = append(groups[:MaxAggregateSpans-1:MaxAggregateSpans-1], other)
	}

	tracer := tracerForCaller(1)
	for _, g := range groups {
		attrs := append([]attribute.KeyValue{
			traceattrs.String(traceattrs.AttrAggregateKind, string(g.kind)),
			traceattrs.Int64(traceattrs.AttrAggregateCount, int64(g.count)),
			traceattrs.Int64(traceattrs.AttrAggregateDetailedCount, int64(g.detailed)),
			traceattrs.Float64(traceattrs.AttrAggregateDurationTotal, float64(g.total)/float64(time.Millisecond)),
			traceattrs.Float64(traceattrs.AttrAggregateDurationMax, float64(g.max)/float64(time.Millisecond)),
			traceattrs.String(traceattrs.AttrAggregateSlowest, g.slowest),
		}, g.attrs...)
		_, span := tracer.Start(ctx, AggregateSpanPrefix+g.name,
			trace.WithTimestamp(g.first),
			trace.WithAttributes(attrs...),
		)
		span.End(trace.WithTimestamp(g.last))
	}
	return len(groups)
}

// detailSpan wraps a detail span (or a no-op one standing in for a counted
// one) so that End feeds the recorder.
type detailSpan struct {
	trace.Span
	rec      *DetailRecorder
	group    *detailGroup
	label    string
	start    time.Time
	detailed bool
	endOnce  sync.Once
}

func (s *detailSpan) End(opts ...trace.SpanEndOption) {
	s.endOnce.Do(func() {
		s.rec.record(s.group, s.label, s.start, time.Now())
		s.Span.End(opts...)
	})
}
