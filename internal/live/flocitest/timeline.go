// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"sort"
	"sync/atomic"
	"time"
)

// This file is the half of the proxy that answers "were these requests
// overlapping?" rather than "how many were there?".
//
// Issue #654 needed exactly this and built it ad hoc: a reverse proxy that
// injects a fixed latency (so serialisation is visible at all - over
// loopback with no latency every request is a millisecond and ten-wide and
// one-wide look the same on the clock) and records each request's start and
// end, from which two statistics fall out:
//
//   - PEAK CONCURRENCY, the most requests in flight at any instant. Stock
//     OpenTofu walks its graph at -parallelism 10, so ten is the number a
//     read pass that matches stock reaches, and one is the signature of a
//     pass that reads its instances in a loop.
//   - NON-OVERLAPPING ADJACENT PAIRS: sort by start time, and count the
//     pairs where the earlier request had already finished when the later
//     one began. A perfectly serial pass scores n-1 out of n-1; a ten-wide
//     one scores a handful, the boundaries between batches.
//
// The second is the statistic that made #654's defect obvious, because a
// peak can be reached once by accident while the rest of the pass is
// serial, and this cannot.
//
// That instrument was never kept, so nothing has ever re-run it. This is
// it, in the repository, on [CountingProxy] so that call counts and the
// timeline come from the same seam and cannot describe different runs.

// Span is one request's interval through the proxy: when it arrived, when
// its response was fully forwarded back, and which AWS action it carried
// (the same classification [CountingProxy.Counts] groups by).
//
// Start is stamped BEFORE any injected latency, because the interval this
// wants is the one the caller sees - a caller waiting on a 100 ms network
// is a caller with a request in flight.
type Span struct {
	Action string
	Start  time.Time
	End    time.Time
}

// TimelineStats is what a slice of [Span] says about concurrency.
type TimelineStats struct {
	// Requests is how many spans the statistics were computed over.
	Requests int

	// Peak is the greatest number of requests in flight at one instant. A
	// request that ends at exactly the instant another begins does not
	// count as overlapping it.
	Peak int

	// AdjacentPairs is Requests-1, or 0 for fewer than two requests: the
	// denominator NonOverlapping is out of.
	AdjacentPairs int

	// NonOverlapping is how many of those pairs did not overlap - the
	// earlier request (by start time) had already ended when the later one
	// began.
	NonOverlapping int

	// Wall is from the first start to the last end.
	Wall time.Duration

	// Busy is the union of every span: how much of Wall had at least one
	// request in flight. Wall-Busy is the time the run spent doing
	// something other than waiting on this endpoint.
	Busy time.Duration

	// Sum is every span's duration added up, ignoring overlap. Sum/Wall is
	// the mean concurrency across the whole window, which is the number
	// that separates "reached ten once" from "ran ten wide".
	Sum time.Duration
}

// MeanConcurrency is Sum/Wall: the average number of requests in flight
// across the whole window, counting the gaps. Zero when Wall is zero.
func (s TimelineStats) MeanConcurrency() float64 {
	if s.Wall <= 0 {
		return 0
	}
	return float64(s.Sum) / float64(s.Wall)
}

// SetLatency makes the proxy sleep d before forwarding each request, so
// that a pass which serialises its calls takes visibly longer than one that
// overlaps them. Zero (the default) forwards immediately.
//
// It is settable at any time so that a harness can stand an estate up at
// full speed and pay the latency only on the runs it is measuring.
func (p *CountingProxy) SetLatency(d time.Duration) {
	p.latency.Store(int64(d))
}

// Latency is the delay [CountingProxy.SetLatency] installed.
func (p *CountingProxy) Latency() time.Duration {
	return time.Duration(p.latency.Load())
}

// appendSpan records one completed request's interval.
func (p *CountingProxy) appendSpan(s Span) {
	p.mu.Lock()
	p.spans = append(p.spans, s)
	p.mu.Unlock()
}

// TimelineLen is how many spans have been recorded. A caller takes this
// before a run and passes it to [CountingProxy.SpansFrom] afterwards to get
// exactly that run's timeline, the way [CountingProxy.Total] is used for
// that run's call count.
func (p *CountingProxy) TimelineLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.spans)
}

// SpansFrom is a copy of every span recorded after the first n.
func (p *CountingProxy) SpansFrom(n int) []Span {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 {
		n = 0
	}
	if n > len(p.spans) {
		return nil
	}
	out := make([]Span, len(p.spans)-n)
	copy(out, p.spans[n:])
	return out
}

// Timeline computes [TimelineStats] over spans. It sorts a copy, so the
// caller's slice is untouched and need not already be ordered - the proxy
// appends in COMPLETION order, not start order, which is precisely the
// difference concurrency makes.
func Timeline(spans []Span) TimelineStats {
	stats := TimelineStats{Requests: len(spans)}
	if len(spans) == 0 {
		return stats
	}

	byStart := make([]Span, len(spans))
	copy(byStart, spans)
	sort.Slice(byStart, func(i, j int) bool {
		if byStart[i].Start.Equal(byStart[j].Start) {
			return byStart[i].End.Before(byStart[j].End)
		}
		return byStart[i].Start.Before(byStart[j].Start)
	})

	if len(byStart) > 1 {
		stats.AdjacentPairs = len(byStart) - 1
		for i := 0; i+1 < len(byStart); i++ {
			// !After, not Before: a request that ends at exactly the instant
			// the next begins did not overlap it.
			if !byStart[i].End.After(byStart[i+1].Start) {
				stats.NonOverlapping++
			}
		}
	}

	// Peak, by a sweep over the endpoints. Ends are processed before starts
	// at an equal instant, for the same reason.
	type event struct {
		at    time.Time
		delta int
	}
	events := make([]event, 0, 2*len(byStart))
	for _, s := range byStart {
		events = append(events, event{s.Start, +1}, event{s.End, -1})
		stats.Sum += s.End.Sub(s.Start)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at.Equal(events[j].at) {
			return events[i].delta < events[j].delta
		}
		return events[i].at.Before(events[j].at)
	})
	inFlight := 0
	for _, e := range events {
		inFlight += e.delta
		if inFlight > stats.Peak {
			stats.Peak = inFlight
		}
	}

	// Wall and Busy, by merging the intervals in start order.
	first, last := byStart[0].Start, byStart[0].End
	busyStart, busyEnd := byStart[0].Start, byStart[0].End
	for _, s := range byStart[1:] {
		if s.End.After(last) {
			last = s.End
		}
		if s.Start.After(busyEnd) {
			stats.Busy += busyEnd.Sub(busyStart)
			busyStart, busyEnd = s.Start, s.End
			continue
		}
		if s.End.After(busyEnd) {
			busyEnd = s.End
		}
	}
	stats.Busy += busyEnd.Sub(busyStart)
	stats.Wall = last.Sub(first)
	return stats
}

// TimelineByAction is [Timeline] computed per action name, so a pass whose
// peak is one can be asked WHICH calls were the serial ones.
func TimelineByAction(spans []Span) map[string]TimelineStats {
	grouped := map[string][]Span{}
	for _, s := range spans {
		grouped[s.Action] = append(grouped[s.Action], s)
	}
	out := make(map[string]TimelineStats, len(grouped))
	for action, ss := range grouped {
		out[action] = Timeline(ss)
	}
	return out
}

// atomicDuration is the latency field's type. It lives here rather than in
// proxy.go so that everything this file added is in this file.
type atomicDuration = atomic.Int64
