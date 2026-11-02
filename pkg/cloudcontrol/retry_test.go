// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// noSleep is a Config.RetrySleep that returns instantly (still respecting
// ctx), so a test that exercises many retries does not pay for the real
// backoff curve.
func noSleep(ctx context.Context, _ time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// throttlingBody is one AWS JSON 1.0 ThrottlingException response.
func writeThrottled(w http.ResponseWriter) {
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"__type":  "com.amazonaws.cloudformation#ThrottlingException",
		"message": "Rate exceeded",
	})
}

// TestRetrySucceedsAfterThrottling counts attempts with an httptest server
// that throttles the first two calls and succeeds on the third, which must
// land inside the default attempt budget.
func TestRetrySucceedsAfterThrottling(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			writeThrottled(w)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ResourceDescription": map[string]string{"Identifier": "i-0123", "Properties": `{"ok":true}`},
		})
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL, RetrySleep: noSleep})
	desc, err := c.GetResource(context.Background(), "AWS::EC2::Instance", "i-0123")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if desc.Identifier != "i-0123" {
		t.Errorf("Identifier = %q, want i-0123", desc.Identifier)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("server saw %d attempts, want exactly 3 (two throttled, one success)", got)
	}
}

// TestRetryGivesUpAtMaxAttempts pins the bound: a server that always
// throttles is called exactly MaxAttempts times, never more, and the
// caller sees the ThrottlingException rather than a retry-exhaustion error
// of its own invention.
func TestRetryGivesUpAtMaxAttempts(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		writeThrottled(w)
	}))
	defer server.Close()

	const maxAttempts = 4
	c := New(Config{Endpoint: server.URL, MaxAttempts: maxAttempts, RetrySleep: noSleep})
	_, err := c.GetResource(context.Background(), "AWS::EC2::Instance", "i-0123")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !HasCode(err, CodeThrottlingError) {
		t.Errorf("err = %v, want it to carry CodeThrottlingError", err)
	}
	if got := atomic.LoadInt32(&attempts); got != maxAttempts {
		t.Errorf("server saw %d attempts, want exactly MaxAttempts=%d", got, maxAttempts)
	}
}

// TestRetryNeverFiresForNonThrottlingErrors is the other half of the
// policy: a ValidationException (or any *APIError code besides
// ThrottlingException) returns on the very first attempt, no matter how
// many attempts the policy would otherwise allow.
func TestRetryNeverFiresForNonThrottlingErrors(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"__type":  "com.amazonaws.cloudformation#ValidationException",
			"message": "malformed request",
		})
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL, MaxAttempts: 5, RetrySleep: noSleep})
	_, err := c.GetResource(context.Background(), "AWS::EC2::Instance", "i-0123")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !HasCode(err, CodeValidationError) {
		t.Errorf("err = %v, want it to carry CodeValidationError", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("server saw %d attempts, want exactly 1 (no retry for a non-throttling error)", got)
	}
}

// TestRetryStopsOnContextCancellation is the context-respecting half of the
// policy: a context canceled before the retry loop's sleep returns stops
// the loop immediately rather than exhausting MaxAttempts. The real
// retrySleep is used here (not the test's noSleep override) so the
// cancellation is actually observed mid-sleep.
func TestRetryStopsOnContextCancellation(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		writeThrottled(w)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// A base delay long enough that the cancellation below lands the loop
	// inside its sleep rather than racing it.
	c := New(Config{Endpoint: server.URL, MaxAttempts: 10, RetryBaseDelay: time.Hour, RetryMaxDelay: time.Hour})

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := c.GetResource(ctx, "AWS::EC2::Instance", "i-0123")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("server saw %d attempts, want exactly 1 (throttled once, then canceled mid-sleep)", got)
	}
}

// TestBackoffDelayStaysWithinBounds is a property test over backoffDelay in
// isolation: every sample it draws, across a spread of attempt numbers,
// lands in [0, min(maxDelay, base*2^(attempt-1))], and the ceiling itself
// never exceeds maxDelay.
func TestBackoffDelayStaysWithinBounds(t *testing.T) {
	const (
		base     = 100 * time.Millisecond
		maxDelay = 2 * time.Second
	)
	for attempt := 1; attempt <= 20; attempt++ {
		want := base << uint(attempt-1) //nolint:gosec // attempt is a small loop bound, never near a shift overflow
		if want <= 0 || want > maxDelay {
			want = maxDelay
		}
		for i := 0; i < 50; i++ {
			d := backoffDelay(base, maxDelay, attempt)
			if d < 0 || d > want {
				t.Fatalf("attempt %d: backoffDelay = %s, want within [0, %s]", attempt, d, want)
			}
			if d > maxDelay {
				t.Fatalf("attempt %d: backoffDelay = %s exceeded maxDelay %s", attempt, d, maxDelay)
			}
		}
	}
}

// TestBackoffDelayZeroInputsAreSafe pins that a zero base or max delay
// (a caller-supplied policy of "never wait") returns 0 rather than
// panicking on a degenerate shift or an empty random range.
func TestBackoffDelayZeroInputsAreSafe(t *testing.T) {
	if d := backoffDelay(0, time.Second, 1); d != 0 {
		t.Errorf("base=0: backoffDelay = %s, want 0", d)
	}
	if d := backoffDelay(time.Second, 0, 1); d != 0 {
		t.Errorf("maxDelay=0: backoffDelay = %s, want 0", d)
	}
}
