// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"context"
	"math/rand/v2"
	"time"
)

// Retry policy defaults: one initial attempt plus up to four retries. See
// doc.go's "Retries" section for the policy this file implements and why
// only a ThrottlingException triggers it.
const (
	// defaultMaxAttempts counts the first try, so this budgets four
	// retries: the shape a plan against a large estate needs to ride out a
	// brief throttling window without hanging indefinitely on one stuck
	// call.
	defaultMaxAttempts = 5

	// defaultRetryBaseDelay is the backoff curve's starting point: the
	// first retry's sleep is uniformly random between 0 and this value.
	defaultRetryBaseDelay = 200 * time.Millisecond

	// defaultRetryMaxDelay caps any single retry's sleep, however many
	// attempts have already doubled the curve past it.
	defaultRetryMaxDelay = 5 * time.Second
)

// retrySleep waits for d, or returns ctx's error if ctx is canceled or
// times out first. This is the "context-respecting" half of the policy: a
// canceled or deadline-exceeded plan stops retrying immediately instead of
// finishing out its backoff curve regardless of who is still waiting on
// it. Config.RetrySleep overrides this for a test that wants a
// deterministic, instant backoff curve instead of a real sleep.
func retrySleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// backoffDelay is the sleep before retry attempt N+1 (attempt is the
// attempt number that just failed, 1-indexed): a uniformly random duration
// between 0 and min(maxDelay, base*2^(attempt-1)) — full jitter, AWS's own
// documented recommendation for avoiding synchronized retry storms when
// many concurrent callers back off from the same throttled API at once,
// which is exactly the shape a plan against a large estate creates:
// discovery issues its GetResource calls concurrently, so a naive fixed
// exponential curve would have every one of them retry in lockstep.
func backoffDelay(base, maxDelay time.Duration, attempt int) time.Duration {
	if base <= 0 || maxDelay <= 0 {
		return 0
	}
	ceiling := maxDelay
	// attempt-1 is capped well under 64 by defaultMaxAttempts and any sane
	// override, but a shift past 62 would overflow silently rather than
	// saturate, so this stays on the maxDelay side of that line rather than
	// trust the shift never to be asked for one.
	if shift := attempt - 1; shift < 62 {
		if scaled := base << uint(shift); scaled > 0 && scaled < ceiling {
			ceiling = scaled
		}
	}
	return time.Duration(rand.Int64N(int64(ceiling) + 1))
}
