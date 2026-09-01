// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTimelineTellsSerialFromConcurrent is the instrument's own control, and
// it is the whole reason a "peak 10" reading from this proxy can be believed:
// the SAME rig, against the same backend, is shown reporting peak 1 for a
// caller that is known to be serial and peak 8 for a caller that is known to
// be eight wide. A timeline that cannot report serialisation is not evidence
// of concurrency, and one that reports it for everything is not evidence
// either.
//
// No docker, no floci: the backend is a handler this test controls.
func TestTimelineTellsSerialFromConcurrent(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<ok/>"))
	}))
	defer backend.Close()

	p := NewCountingProxy(t, backend.URL)
	// Latency is what makes any of this visible: over loopback with none,
	// every request is a few hundred microseconds and a serial caller and a
	// concurrent one produce timelines nobody can tell apart.
	p.SetLatency(20 * time.Millisecond)

	const n = 24
	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 0, MaxIdleConnsPerHost: n}}
	post := func() {
		req, err := http.NewRequest(http.MethodPost, p.Endpoint()+"/", strings.NewReader("Action=ListRoles&Version=2010-05-08"))
		if err != nil {
			t.Errorf("building a request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("posting through the proxy: %v", err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// ── serial: one at a time, start to finish ──────────────────────────
	mark := p.TimelineLen()
	for i := 0; i < n; i++ {
		post()
	}
	serial := Timeline(p.SpansFrom(mark))
	if serial.Requests != n {
		t.Fatalf("serial: recorded %d spans, want %d", serial.Requests, n)
	}
	if serial.Peak != 1 {
		t.Errorf("serial: peak concurrency %d, want 1 - the instrument cannot see serialisation, so no reading it gives about concurrency means anything", serial.Peak)
	}
	if serial.NonOverlapping != serial.AdjacentPairs {
		t.Errorf("serial: %d of %d adjacent pairs non-overlapping, want all %d", serial.NonOverlapping, serial.AdjacentPairs, serial.AdjacentPairs)
	}
	t.Logf("serial:     %d requests, peak %d in flight, %d of %d adjacent pairs non-overlapping, mean concurrency %.2f, wall %s",
		serial.Requests, serial.Peak, serial.NonOverlapping, serial.AdjacentPairs, serial.MeanConcurrency(), serial.Wall.Round(time.Millisecond))

	// ── concurrent: eight in flight, a bounded pool ─────────────────────
	const width = 8
	mark = p.TimelineLen()
	var wg sync.WaitGroup
	slots := make(chan struct{}, width)
	for i := 0; i < n; i++ {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			post()
		}()
	}
	wg.Wait()
	concurrent := Timeline(p.SpansFrom(mark))
	if concurrent.Requests != n {
		t.Fatalf("concurrent: recorded %d spans, want %d", concurrent.Requests, n)
	}
	if concurrent.Peak < 2 {
		t.Errorf("concurrent: peak concurrency %d, want at least 2 - the instrument reports serial for a caller that is not", concurrent.Peak)
	}
	if concurrent.NonOverlapping >= serial.NonOverlapping {
		t.Errorf("concurrent: %d of %d adjacent pairs non-overlapping, which is not fewer than the serial run's %d - the statistic does not separate the two cases",
			concurrent.NonOverlapping, concurrent.AdjacentPairs, serial.NonOverlapping)
	}
	if concurrent.Wall >= serial.Wall {
		t.Errorf("concurrent wall %s is not shorter than serial wall %s; the injected latency is not being overlapped, so this backend cannot serve as a control",
			concurrent.Wall, serial.Wall)
	}
	t.Logf("concurrent: %d requests, peak %d in flight, %d of %d adjacent pairs non-overlapping, mean concurrency %.2f, wall %s",
		concurrent.Requests, concurrent.Peak, concurrent.NonOverlapping, concurrent.AdjacentPairs, concurrent.MeanConcurrency(), concurrent.Wall.Round(time.Millisecond))

	// ── the latency knob itself is real ─────────────────────────────────
	// A serial pass of n requests through a d-latency proxy cannot finish
	// faster than n*d. If it does, SetLatency is not being applied and every
	// wall clock above is measuring the loopback, not the shape.
	if floor := time.Duration(n) * p.Latency(); serial.Wall < floor {
		t.Errorf("serial wall %s is below the %s floor that %d requests at %s of injected latency imposes; the latency is not being injected",
			serial.Wall, floor, n, p.Latency())
	}
}

// TestTimelineArithmetic pins the two statistics against hand-built spans,
// so a change to the sweep or the pair count fails here rather than being
// discovered as a surprising number in a measurement run.
func TestTimelineArithmetic(t *testing.T) {
	base := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }

	t.Run("perfectly serial", func(t *testing.T) {
		spans := []Span{
			{Action: "A", Start: at(0), End: at(100)},
			{Action: "A", Start: at(100), End: at(200)},
			{Action: "A", Start: at(200), End: at(300)},
		}
		got := Timeline(spans)
		if got.Peak != 1 || got.NonOverlapping != 2 || got.AdjacentPairs != 2 {
			t.Fatalf("peak %d, %d/%d non-overlapping; want peak 1, 2/2", got.Peak, got.NonOverlapping, got.AdjacentPairs)
		}
		if got.Wall != 300*time.Millisecond || got.Busy != 300*time.Millisecond {
			t.Fatalf("wall %s busy %s; want 300ms and 300ms", got.Wall, got.Busy)
		}
		if mc := got.MeanConcurrency(); mc < 0.99 || mc > 1.01 {
			t.Fatalf("mean concurrency %.3f, want ~1", mc)
		}
	})

	t.Run("three wide", func(t *testing.T) {
		spans := []Span{
			{Action: "A", Start: at(0), End: at(100)},
			{Action: "A", Start: at(10), End: at(110)},
			{Action: "A", Start: at(20), End: at(120)},
		}
		got := Timeline(spans)
		if got.Peak != 3 || got.NonOverlapping != 0 {
			t.Fatalf("peak %d, %d non-overlapping; want peak 3, 0", got.Peak, got.NonOverlapping)
		}
		if got.Wall != 120*time.Millisecond {
			t.Fatalf("wall %s, want 120ms", got.Wall)
		}
		if mc := got.MeanConcurrency(); mc < 2.4 || mc > 2.6 {
			t.Fatalf("mean concurrency %.3f, want ~2.5", mc)
		}
	})

	t.Run("a gap is not busy", func(t *testing.T) {
		spans := []Span{
			{Action: "A", Start: at(0), End: at(100)},
			{Action: "A", Start: at(400), End: at(500)},
		}
		got := Timeline(spans)
		if got.Wall != 500*time.Millisecond || got.Busy != 200*time.Millisecond {
			t.Fatalf("wall %s busy %s; want 500ms and 200ms", got.Wall, got.Busy)
		}
	})

	t.Run("completion order does not change the answer", func(t *testing.T) {
		// The proxy appends in completion order, so a long first request
		// lands after the short ones it overlapped. [Timeline] sorts by
		// start, so this is the same timeline as {0,100},{10,20},{30,40}:
		// peak 2, and one non-overlapping pair - the two short requests,
		// which really did not overlap each other. The pair count is a
		// statement about START-ORDER ADJACENT pairs, not about whether
		// anything at all was in flight in between, which is why it is
		// reported alongside the peak rather than instead of it.
		spans := []Span{
			{Action: "A", Start: at(10), End: at(20)},
			{Action: "A", Start: at(30), End: at(40)},
			{Action: "A", Start: at(0), End: at(100)},
		}
		got := Timeline(spans)
		if got.Peak != 2 || got.NonOverlapping != 1 {
			t.Fatalf("peak %d, %d non-overlapping; want peak 2, 1", got.Peak, got.NonOverlapping)
		}
		if got.Wall != 100*time.Millisecond || got.Busy != 100*time.Millisecond {
			t.Fatalf("wall %s busy %s; want 100ms and 100ms", got.Wall, got.Busy)
		}
	})
}
