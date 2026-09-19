// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// getAllParallelisms is every bound the decisive tests run at. GitHub issue
// #1336 asks for 1 and the default by name: a concurrency defect that only
// appears above 1 has bitten this repository before, and a fix that only
// holds at 1 is the sequential loop. 32 is past the key count, which is the
// "more workers than work" edge.
var getAllParallelisms = []int{1, DefaultS3GetAllParallelism, 32}

const getAllNamespace = "tofu-records/prod/"

// s3StoreForGetAll is a store over the fake server with parallelism workers
// and nKeys records seeded under the namespace. Retries are off: an injected
// 500 must reach GetAll as a failure, not be absorbed by the SDK.
func s3StoreForGetAll(t *testing.T, parallelism, nKeys int) (*S3Store, *fakeS3Server, []string) {
	t.Helper()
	server, fake := newFakeS3Server(t)
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		Retryer:     func() aws.Retryer { return retry.AddWithMaxAttempts(retry.NewStandard(), 1) },
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket", GetAllParallelism: parallelism})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	var keys []string
	for i := 0; i < nKeys; i++ {
		key := fmt.Sprintf("%saws_thing/key%02d", getAllNamespace, i)
		if _, err := store.PutIfAbsent(context.Background(), key, []byte(key)); err != nil {
			t.Fatalf("seeding %q: %v", key, err)
		}
		keys = append(keys, key)
	}
	return store, fake, keys
}

// TestS3GetAllIsCompleteOrItFails is the constraint that matters more than
// the speedup. One GET fails mid-fanout; the call must fail and name the key,
// and must never hand back a map that is merely missing it - a plan reads an
// absent record as "create this" and a map that reads as authoritative is how
// that plan gets built.
func TestS3GetAllIsCompleteOrItFails(t *testing.T) {
	for _, parallelism := range getAllParallelisms {
		t.Run(fmt.Sprintf("parallelism=%d", parallelism), func(t *testing.T) {
			store, fake, keys := s3StoreForGetAll(t, parallelism, 20)
			broken := keys[11]
			fake.beforeGet = func(path string) int {
				if strings.HasSuffix(path, broken) {
					return http.StatusInternalServerError
				}
				return 0
			}
			got, err := store.GetAll(context.Background(), getAllNamespace)
			if err == nil {
				t.Fatalf("GetAll returned %d of %d records and no error: a short map that reads as complete", len(got), len(keys))
			}
			if got != nil {
				t.Errorf("GetAll returned an error AND a map of %d records; a caller that ignores the error has a partial namespace", len(got))
			}
			if !strings.Contains(err.Error(), broken) {
				t.Errorf("the error does not name the key that failed (%q): %v", broken, err)
			}
			if strings.Contains(err.Error(), "context canceled") {
				t.Errorf("the error reported is a sibling GET's cancellation, not the failure that caused it: %v", err)
			}
		})
	}
}

// TestS3GetAllOmitsAKeyDeletedBetweenListAndGet is the one legitimate
// omission, and the test that tells it apart from the case above: a 404 is
// "deleted in between", answered by leaving the key out, with no error.
func TestS3GetAllOmitsAKeyDeletedBetweenListAndGet(t *testing.T) {
	for _, parallelism := range getAllParallelisms {
		t.Run(fmt.Sprintf("parallelism=%d", parallelism), func(t *testing.T) {
			store, fake, keys := s3StoreForGetAll(t, parallelism, 20)
			gone := keys[7]
			fake.beforeGet = func(path string) int {
				if strings.HasSuffix(path, gone) {
					return http.StatusNotFound
				}
				return 0
			}
			got, err := store.GetAll(context.Background(), getAllNamespace)
			if err != nil {
				t.Fatalf("a key deleted between the LIST and its GET failed the whole read: %v", err)
			}
			if _, present := got[gone]; present {
				t.Errorf("the deleted key %q is in the map", gone)
			}
			if len(got) != len(keys)-1 {
				t.Errorf("got %d records, want %d: every key but the deleted one", len(got), len(keys)-1)
			}
			for _, key := range keys {
				if key == gone {
					continue
				}
				if rec, ok := got[key]; !ok || string(rec.Payload) != key || rec.Version == "" {
					t.Errorf("record %q is missing, or carries the wrong payload or no version: %+v", key, rec)
				}
			}
		})
	}
}

// TestS3GetAllStopsOnCancellationWithNoShortMap: a cancelled context returns
// an error. The GETs block until the test cancels, so the cancellation lands
// mid-fanout at every bound.
func TestS3GetAllStopsOnCancellationWithNoShortMap(t *testing.T) {
	for _, parallelism := range getAllParallelisms {
		t.Run(fmt.Sprintf("parallelism=%d", parallelism), func(t *testing.T) {
			store, fake, keys := s3StoreForGetAll(t, parallelism, 20)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var served atomic.Int32
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			fake.beforeGet = func(string) int {
				if served.Add(1) == 3 {
					cancel()
				}
				if served.Load() >= 3 {
					select {
					case <-release:
					case <-time.After(2 * time.Second):
					}
				}
				return 0
			}
			got, err := store.GetAll(ctx, getAllNamespace)
			if err == nil {
				t.Fatalf("a cancelled GetAll returned %d of %d records and no error", len(got), len(keys))
			}
			if got != nil {
				t.Errorf("a cancelled GetAll returned a map of %d records beside its error", len(got))
			}
		})
	}
}

// TestS3GetAllIsBoundedAndActuallyConcurrent measures the fan-out instead of
// trusting it: never more GETs in flight than the bound, and - above 1 -
// really more than one, or the "parallel" read is the sequential one with
// extra goroutines.
func TestS3GetAllIsBoundedAndActuallyConcurrent(t *testing.T) {
	for _, parallelism := range getAllParallelisms {
		t.Run(fmt.Sprintf("parallelism=%d", parallelism), func(t *testing.T) {
			const nKeys = 24
			store, fake, keys := s3StoreForGetAll(t, parallelism, nKeys)
			var (
				mu       sync.Mutex
				inFlight int
				peak     int
			)
			fake.beforeGet = func(string) int {
				mu.Lock()
				inFlight++
				if inFlight > peak {
					peak = inFlight
				}
				mu.Unlock()
				time.Sleep(15 * time.Millisecond)
				mu.Lock()
				inFlight--
				mu.Unlock()
				return 0
			}
			got, err := store.GetAll(context.Background(), getAllNamespace)
			if err != nil {
				t.Fatalf("GetAll: %v", err)
			}
			if len(got) != len(keys) {
				t.Fatalf("got %d records, want %d", len(got), len(keys))
			}
			want := parallelism
			if want > nKeys {
				want = nKeys
			}
			if peak > want {
				t.Errorf("%d GETs were in flight at once, past the bound of %d", peak, want)
			}
			if parallelism > 1 && peak < 2 {
				t.Errorf("at most %d GET was ever in flight with a bound of %d: the read is not concurrent", peak, parallelism)
			}
			if parallelism == 1 && peak != 1 {
				t.Errorf("a bound of 1 had %d GETs in flight; 1 must be the sequential read", peak)
			}
		})
	}
}

// TestS3GetAllDefaultsToEight: zero means the default, and the default is the
// number the epic rules.
func TestS3GetAllDefaultsToEight(t *testing.T) {
	if DefaultS3GetAllParallelism != 8 {
		t.Errorf("DefaultS3GetAllParallelism = %d, want 8 (#1332)", DefaultS3GetAllParallelism)
	}
	store, fake, _ := s3StoreForGetAll(t, 0, 24)
	var mu sync.Mutex
	inFlight, peak := 0, 0
	fake.beforeGet = func(string) int {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		time.Sleep(15 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return 0
	}
	if _, err := store.GetAll(context.Background(), getAllNamespace); err != nil {
		t.Fatal(err)
	}
	if peak > DefaultS3GetAllParallelism || peak < 2 {
		t.Errorf("an unset bound peaked at %d GETs in flight, want between 2 and %d", peak, DefaultS3GetAllParallelism)
	}
}

// TestBoundedFanOutNeverReportsSuccessForWorkItDidNotDo isolates the window a
// real client cannot reach on purpose: the context ends while no call is in
// flight, so nothing reports a failure. Every call here SUCCEEDS, including
// the one that cancels, so the only evidence that the fan-out stopped short
// is that it did. The rule is "nil means all n ran": either an error comes
// back, or every index was visited.
//
// n is large because the feed's select picks at random between a ready
// worker and a done context, so after the cancel it may hand out a few more
// before it notices. It will not hand out twenty thousand.
func TestBoundedFanOutNeverReportsSuccessForWorkItDidNotDo(t *testing.T) {
	const n = 20000
	for _, workers := range getAllParallelisms {
		t.Run(fmt.Sprintf("parallelism=%d", workers), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var ran atomic.Int64
			err := boundedFanOut(ctx, n, workers, func(_ context.Context, i int) error {
				ran.Add(1)
				if i == 2 {
					cancel()
				}
				return nil
			})
			if err == nil && ran.Load() != n {
				t.Fatalf("boundedFanOut returned nil having run %d of %d: a caller building its result from this has a short result that reads as complete", ran.Load(), n)
			}
			if err == nil {
				t.Fatalf("all %d ran despite the cancel, so this run never reached the window it is here for; raise n", n)
			}
		})
	}
}

// TestBoundedFanOutRunsEverythingOnce is the control for the test above.
func TestBoundedFanOutRunsEverythingOnce(t *testing.T) {
	for _, workers := range append([]int{0}, getAllParallelisms...) {
		const n = 500
		seen := make([]atomic.Int32, n)
		nn := n
		if workers == 0 {
			nn = 0
		}
		if err := boundedFanOut(context.Background(), nn, workers, func(_ context.Context, i int) error {
			seen[i].Add(1)
			return nil
		}); err != nil {
			t.Fatalf("workers=%d: %v", workers, err)
		}
		for i := 0; i < nn; i++ {
			if got := seen[i].Load(); got != 1 {
				t.Fatalf("workers=%d: index %d ran %d times, want exactly once", workers, i, got)
			}
		}
	}
}
