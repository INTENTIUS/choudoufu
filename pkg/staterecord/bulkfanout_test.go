// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBoundedFanOutClampsWorkers: a workers count below 1 starts no worker,
// so the feed blocks on its first send and the call never returns. A hang is
// worse than a failure - there is no error to read and no stack to look at
// without attaching to the process - so the clamp is inside the function
// rather than trusted to every caller. GitHub issue #1383.
//
// The timeout is the point of the test: without it a regression hangs the
// whole package's run instead of failing this one case.
func TestBoundedFanOutClampsWorkers(t *testing.T) {
	for _, workers := range []int{0, -1, -1000} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			var ran atomic.Int64
			done := make(chan error, 1)
			go func() {
				done <- boundedFanOut(context.Background(), 5, workers, func(context.Context, int) error {
					ran.Add(1)
					return nil
				})
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("boundedFanOut(n=5, workers=%d): %v", workers, err)
				}
				if got := ran.Load(); got != 5 {
					t.Errorf("%d of 5 jobs ran", got)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("boundedFanOut(n=5, workers=%d) never returned: workers below 1 start no worker and the feed blocks forever", workers)
			}
		})
	}
}

// TestBoundedFanOutCancelsTheRestOnTheFirstFailure. The doc comment says the
// first failure "cancels the rest", and that is not an optimization: a bulk
// read of a large estate whose credentials just expired would otherwise send
// every remaining GET and wait for all of them before reporting the first.
//
// It is measured by counting the GETs that START after the failure, with the
// rest of the fan-out held in a slow response so the count is decided by
// whether the feed stopped and not by timing.
//
// The mutation this catches: removing cancel() from the failOnce block. Every
// other assertion in this package still passes with it gone, because the
// error returned and the map built are the same either way.
func TestBoundedFanOutCancelsTheRestOnTheFirstFailure(t *testing.T) {
	const (
		keys       = 20
		workers    = 4
		slowEnough = 200 * time.Millisecond
	)
	store, fake, seeded := s3StoreForGetAll(t, workers, keys)

	var started atomic.Int64
	broken := seeded[0] // handed out first, so the failure happens at once
	fake.beforeGet = func(path string) int {
		started.Add(1)
		if strings.HasSuffix(path, broken) {
			return http.StatusInternalServerError
		}
		// Every other GET is slow, so the workers are all busy when the
		// failure lands and the only way more GETs begin is the feed
		// handing out more work.
		time.Sleep(slowEnough)
		return 0
	}

	_, err := store.GetAll(context.Background(), getAllNamespace)
	if err == nil {
		t.Fatal("GetAll succeeded with a GET that failed")
	}
	if !strings.Contains(err.Error(), broken) {
		t.Fatalf("the error does not name the failed key %q: %v", broken, err)
	}
	// With the cancel in place the feed stops once the workers are busy, so
	// only the first handful of GETs ever begin. Without it every key is
	// handed out and all 20 run.
	if got := started.Load(); got > keys/2 {
		t.Errorf("%d of %d GETs started after the first failure: the rest were not cancelled", got, keys)
	}
}

// TestGetAllClampsItsOwnParallelism keeps GetAll's own clamp honest, since
// boundedFanOut's clamp would now hide its removal.
func TestGetAllClampsItsOwnParallelism(t *testing.T) {
	for _, parallelism := range []int{0, -3} {
		t.Run(fmt.Sprintf("parallelism=%d", parallelism), func(t *testing.T) {
			store, _, keys := s3StoreForGetAll(t, parallelism, 6)
			got, err := store.GetAll(context.Background(), getAllNamespace)
			if err != nil {
				t.Fatalf("GetAll: %v", err)
			}
			if len(got) != len(keys) {
				t.Errorf("read %d of %d records", len(got), len(keys))
			}
		})
	}
}

// TestBoundedFanOutStillReportsAnEarlyStop: the clamp must not have changed
// what an early stop means. A context that ends with no call in flight leaves
// the feed short and no failure behind it, and that is still an error.
func TestBoundedFanOutStillReportsAnEarlyStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := boundedFanOut(ctx, 3, 0, func(context.Context, int) error { return nil })
	if err == nil {
		t.Fatal("a fan-out that stopped short returned no error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the error does not carry the cancellation: %v", err)
	}
}
