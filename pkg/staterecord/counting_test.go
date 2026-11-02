// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
)

// TestCountingStoreCountsEveryMethod proves the counter sees each of the five
// store methods, because a counter blind to one of them reports a smaller
// number than the truth and every conclusion drawn from it is wrong in the
// safe-looking direction.
func TestCountingStoreCountsEveryMethod(t *testing.T) {
	ctx := context.Background()
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	c := NewCountingStore(inner, nil)

	if _, _, _, err := c.Get(ctx, "a"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	v, err := c.PutIfAbsent(ctx, "a", []byte("1"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	v, err = c.PutIfVersion(ctx, "a", []byte("2"), v)
	if err != nil {
		t.Fatalf("PutIfVersion: %v", err)
	}
	if _, err := c.List(ctx, ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := c.Delete(ctx, "a", v); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got := c.Counts()
	if got.Total != 5 {
		t.Errorf("Total = %d, want 5 (trips: %v)", got.Total, c.Trips())
	}
	for _, m := range []string{"Get", "PutIfAbsent", "PutIfVersion", "List", "Delete"} {
		if got.ByMethod[m] != 1 {
			t.Errorf("ByMethod[%q] = %d, want 1", m, got.ByMethod[m])
		}
	}
}

// TestCountingStoreAttributesTheCallSite is the part of the instrument that
// makes it useful rather than merely correct: a per-method total names no
// line to fix. The site must be the caller, not the counter and not the
// store.
func TestCountingStoreAttributesTheCallSite(t *testing.T) {
	ctx := context.Background()
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	c := NewCountingStore(inner, nil)
	if _, _, _, err := c.Get(ctx, "k"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	trips := c.Trips()
	if len(trips) != 1 {
		t.Fatalf("got %d trips, want 1", len(trips))
	}
	site := trips[0].Site
	if !strings.Contains(site, "TestCountingStoreAttributesTheCallSite") {
		t.Errorf("Site = %q, want it to name this test function", site)
	}
	if !strings.Contains(site, "counting_test.go:") {
		t.Errorf("Site = %q, want a file:line in this file", site)
	}
	if strings.Contains(site, "staterecord.(*CountingStore)") {
		t.Errorf("Site = %q attributes the trip to the counter itself", site)
	}
	if trips[0].Key != "k" {
		t.Errorf("Key = %q, want %q", trips[0].Key, "k")
	}
}

// TestCountingStoreCountsRepeatsSeparately pins the number a cache can win:
// trips that re-read a key an earlier trip already read. Reported apart from
// the total because they are the only ones a cache removes.
func TestCountingStoreCountsRepeatsSeparately(t *testing.T) {
	ctx := context.Background()
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	c := NewCountingStore(inner, nil)
	for i := 0; i < 3; i++ {
		if _, _, _, err := c.Get(ctx, "same"); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if _, _, _, err := c.Get(ctx, "other"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	got := c.Counts()
	if got.Total != 4 {
		t.Fatalf("Total = %d, want 4", got.Total)
	}
	if got.DistinctKeys != 2 {
		t.Errorf("DistinctKeys = %d, want 2", got.DistinctKeys)
	}
	if got.RepeatTrips != 2 {
		t.Errorf("RepeatTrips = %d, want 2", got.RepeatTrips)
	}
}

// TestCountingStoreLogRoundTrips proves the out-of-process half: what the log
// writes is exactly what [ParseTripLog] reads back. The subprocess
// measurement has no other channel, so a lossy format here would silently
// under-report the real binary's cost.
func TestCountingStoreLogRoundTrips(t *testing.T) {
	ctx := context.Background()
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	var buf bytes.Buffer
	c := NewCountingStore(inner, &buf)
	if _, _, _, err := c.Get(ctx, "one"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.List(ctx, ""); err != nil {
		t.Fatalf("List: %v", err)
	}

	parsed, err := ParseTripLog(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseTripLog: %v", err)
	}
	want := c.Trips()
	if len(parsed) != len(want) {
		t.Fatalf("parsed %d trips, recorded %d", len(parsed), len(want))
	}
	for i := range want {
		if parsed[i] != want[i] {
			t.Errorf("trip %d: parsed %+v, recorded %+v", i, parsed[i], want[i])
		}
	}
}

// TestParseTripLogRefusesAMalformedLine keeps the parser from turning an
// unreadable line into a missing trip: under-reporting is the one failure
// mode this whole instrument exists to end.
func TestParseTripLogRefusesAMalformedLine(t *testing.T) {
	if _, err := ParseTripLog([]byte("Get\tvia\tsite\tkey\nnot-a-trip\n")); err == nil {
		t.Fatal("ParseTripLog accepted a line with the wrong field count")
	}
}

// TestCountingStoreIsConcurrencySafe runs under -race; the sweep and the
// projection both read records from goroutines.
func TestCountingStoreIsConcurrencySafe(t *testing.T) {
	ctx := context.Background()
	inner, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	var buf bytes.Buffer
	c := NewCountingStore(inner, &lockedTestWriter{w: &buf})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _, _, _ = c.Get(ctx, "k")
			}
		}()
	}
	wg.Wait()
	if got := c.Total(); got != 80 {
		t.Errorf("Total = %d, want 80", got)
	}
}

type lockedTestWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedTestWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
