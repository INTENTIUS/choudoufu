// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Record is one stored record's content and version, as [BulkReader.GetAll]
// returns it — the same pair [Store.Get] returns for one key, for a caller
// that asked for many.
type Record struct {
	Payload []byte
	Version string
}

// BulkReader is the optional half of [Store] that loads a whole namespace in
// one call. It exists because a plan needs the entire estate's records and
// stock OpenTofu gets its whole equivalent — the state file — in one read.
// Without it, a converged plan's cheapest possible shape is still one call
// per instance for information no per-instance decision needed separately.
//
// GetAll returns every record whose key begins with keyPrefix, keyed by the
// same key [Store.Get] and [Store.List] use. An empty keyPrefix means every
// key. The returned map is the CALLER's; implementations must not retain or
// reuse it.
//
// The result is complete for that prefix: a key absent from the map holds no
// record. That is what makes it usable as a snapshot rather than a warm
// cache — a reader can answer "there is nothing recorded for this address"
// from it without going back to the store.
//
// It is optional rather than part of [Store] because it is an optimization
// and not a semantic: a backend with no way to enumerate values simply does
// not implement it, and every caller keeps working through [Store.Get].
type BulkReader interface {
	GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error)
}

var (
	_ BulkReader = (*LocalStore)(nil)
	_ BulkReader = (*S3Store)(nil)
)

// GetAll reads every record under keyPrefix from the store directory in one
// walk. Costs no network at all, so the whole namespace is one traversal
// plus one file read each — the local backend's equivalent of stock reading
// its state file.
//
// The exclusions are [LocalStore.List]'s exactly: a lockfile is not a
// record, and a temp file is a write in progress that no reader may observe.
//
// A file the walk saw and the read no longer finds fails the whole bulk read,
// for [S3Store.GetAll]'s reason (GitHub issue #1355): the walk and the read
// disagreeing about what is in the directory means what came back is not a
// snapshot of it, and a snapshot short by one key is how an instance drops
// out of prior state with nothing said.
func (s *LocalStore) GetAll(_ context.Context, keyPrefix string) (map[string]Record, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	out := map[string]Record{}
	var vanished []string
	err := filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), lockSuffix) || strings.Contains(d.Name(), tmpInfix) {
			return nil
		}
		rel, err := filepath.Rel(s.dir, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, keyPrefix) {
			return nil
		}
		payload, version, exists, err := readRecord(p)
		if err != nil {
			return err
		}
		if !exists {
			// Removed between the walk and the read. Collected rather than
			// skipped: see this method's doc comment.
			vanished = append(vanished, key)
			return nil
		}
		out[key] = Record{Payload: payload, Version: version}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("staterecord: local: reading everything under %q: %w", keyPrefix, err)
	}
	if len(vanished) > 0 {
		return nil, fmt.Errorf("staterecord: local: reading everything under %q: %s, so the walk and the reads that follow it disagree about what this namespace holds; refused rather than returned short (GitHub issue #1355)", keyPrefix, namesVanished(vanished))
	}
	return out, nil
}

// DefaultS3GetAllParallelism is how many GetObject calls [S3Store.GetAll] has
// in flight at once unless [S3Config.GetAllParallelism] says otherwise.
//
// Eight is chosen to be unremarkable, not tuned, and it has not been measured
// at scale. The namespace read here holds one record per managed instance -
// internal/live/projection's write-back records every instance, an identity
// envelope for an ordinary taggable resource as well as the whole value of a
// record-backed one - so the read is N GetObject calls for an estate of N
// instances and this bound sets how long that takes. An earlier version of
// this comment called the namespace "the record-backed slice only, a small
// fraction of an estate" and the bound "not load-bearing". That was the
// design text's claim and the code never matched it. The bound is
// configurable because the estate that needs otherwise will know why and
// should not have to patch the binary to find out. GitHub issue #1336.
const DefaultS3GetAllParallelism = 8

// GetAll reads every record under keyPrefix: one ListObjectsV2 pagination,
// then one GetObject per key, at most [S3Config.GetAllParallelism] of them in
// flight at once.
//
// S3 is the backend that genuinely cannot bulk-fetch. There is no batch-read
// operation in the S3 API - ListObjectsV2 returns each object's key and ETag
// but never its body - so N objects cost N GetObject calls whatever this
// function does. Overlapping them is the only saving there is.
//
// # A bulk read is complete or it fails
//
// That is the constraint, and it matters more than the speedup. [BulkReader]
// promises the result is complete for its prefix: a key absent from the map
// holds no record. A plan reads a record key with no configuration behind it
// as an instruction to destroy, and reads a declared instance with no record
// as something to create. So a map that silently lacks a key is the worst
// thing this backend can produce, and parallelism is exactly where it would
// come from: the sequential loop this replaced got completeness for free by
// returning on its first error, and a fan-out has to be written to keep it.
//
// So: results are written by index into a slice, never into a shared map.
// The first failure wins, cancels the rest, and is the error returned, naming
// its key. The map is built only after every worker has stopped, and only if
// nothing failed AND every key was handed to a worker - a cancelled context
// that stopped the feed with no GET in flight would otherwise leave no error
// at all and a short map behind it.
//
// # A key the LIST named and the GET did not find
//
// This used to be the one omission that was kept: the key was read as
// deleted between the LIST and its GET, and left out of the map as the
// correct way to say so. GitHub issue #1355 is why it is not kept any more.
//
// The omission is correct about the store and wrong about the run. What
// consumes this map is [RunCache], which holds it for the whole read phase
// and answers every later question about a key inside the namespace from it
// WITHOUT going back to the store. So one 404 in one GET does not cost one
// stale answer; it makes "there is no record for this instance" the run's
// settled position, and for an instance whose record IS its whole state that
// is prior state with an instance missing from it. On an ordinary plan that
// proposes a create for something that exists. On `apply -destroy` it
// proposes nothing at all, and the run prints a success with one fewer
// instance destroyed than the estate has - #1355's shape exactly, with no
// error and no warning anywhere in the output.
//
// So the key is fetched a SECOND time before its absence is believed, and if
// the second GET does not find it either, the whole bulk read fails and names
// it. Two reads of one store that disagree is not a snapshot, whichever of
// them is right. [RunCache.ensureLoaded] treats that failure the way it
// treats any other - it loads no snapshot and every read goes to the store
// per key - so a record that really was deleted still reads as absent, but by
// a read taken now rather than by an omission from a torn snapshot.
func (s *S3Store) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	keys, err := s.List(ctx, keyPrefix)
	if err != nil {
		return nil, err
	}

	workers := s.getAllParallelism
	if workers < 1 {
		workers = DefaultS3GetAllParallelism
	}
	if workers > len(keys) {
		workers = len(keys)
	}

	// found[i] is keys[i]'s record, nil when both GETs 404ed. Indexed, so no
	// two workers ever write the same memory and nothing here needs a lock.
	found := make([]*Record, len(keys))
	err = boundedFanOut(ctx, len(keys), workers, func(ctx context.Context, i int) error {
		rec, exists, getErr := s.getForBulk(ctx, keys[i])
		if getErr != nil {
			return getErr
		}
		if !exists {
			// Asked once more before the absence is believed. See this
			// function's own doc comment: a spurious or transient 404 for a
			// key nobody deleted is otherwise the run's settled answer for
			// that instance.
			rec, exists, getErr = s.getForBulk(ctx, keys[i])
			if getErr != nil {
				return getErr
			}
		}
		if exists {
			found[i] = &rec
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("staterecord: s3: reading everything under %q: %w", keyPrefix, err)
	}

	var vanished []string
	for i, rec := range found {
		if rec == nil {
			vanished = append(vanished, keys[i])
		}
	}
	if len(vanished) > 0 {
		return nil, fmt.Errorf("staterecord: s3: reading everything under %q: %s, twice over: the listing and the reads that follow it disagree about what this namespace holds, so what came back is not a snapshot of it and is refused rather than returned short (GitHub issue #1355)", keyPrefix, namesVanished(vanished))
	}

	out := make(map[string]Record, len(keys))
	for i, rec := range found {
		out[keys[i]] = *rec
	}
	return out, nil
}

// namesVanished is the clause naming the keys a listing returned and the
// reads could not find. Every key is named up to a handful of them, because
// the operator's next move is to look one up; past that the count is what
// carries, and a hundred names in a diagnostic hide the sentence around them.
func namesVanished(keys []string) string {
	const show = 5
	if len(keys) == 1 {
		return fmt.Sprintf("the listing named %q and no record was there", keys[0])
	}
	if len(keys) <= show {
		return fmt.Sprintf("the listing named %d keys that no record was there for (%s)", len(keys), strings.Join(keys, ", "))
	}
	return fmt.Sprintf("the listing named %d keys that no record was there for (%s, and %d more)", len(keys), strings.Join(keys[:show], ", "), len(keys)-show)
}

// boundedFanOut runs do(ctx, i) for every i in [0, n), at most workers at a
// time, and returns nil only if EVERY one of them ran and none failed.
//
// It is apart from [S3Store.GetAll] so the one property that is awkward to
// reach through a real client can be tested directly: a context that ends
// with no call in flight. Every in-flight call would report the cancellation
// itself, so the only evidence of that window is that the feed stopped short,
// and a caller that built its result from "no error" would build it from the
// keys the feed got to.
//
//   - The first failure wins and cancels the rest. Later failures are the
//     cancellation's own echoes and would hide the call that actually broke.
//   - An early stop with no failure is still an error.
//
// workers below 1 is clamped to 1 here rather than trusted to the caller.
// [S3Store.GetAll] does clamp, but a zero reaching this function starts no
// worker at all, so the feed blocks on its first send and the call never
// returns - a hang, not a failure, and the next caller to forget is the one
// who finds out. GitHub issue #1383.
func boundedFanOut(ctx context.Context, n, workers int, do func(ctx context.Context, i int) error) error {
	if workers < 1 {
		workers = 1
	}
	fanCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		failOnce sync.Once
		failure  error
		wg       sync.WaitGroup
	)
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if err := do(fanCtx, i); err != nil {
					failOnce.Do(func() {
						failure = err
						cancel()
					})
				}
			}
		}()
	}

	handed := 0
feed:
	for i := 0; i < n; i++ {
		select {
		case jobs <- i:
			handed++
		case <-fanCtx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	if failure != nil {
		return failure
	}
	if handed != n {
		return fmt.Errorf("stopped after %d of %d: %w", handed, n, context.Cause(fanCtx))
	}
	return nil
}

// getForBulk is one key's GetObject for [S3Store.GetAll]. exists is false,
// with no error, only for a 404 that named the key: it was deleted between
// the LIST and this GET. A 404 that named the bucket is a failure like any
// other, because dropping the key would turn a bucket that went away into a
// short map, which is the one thing a bulk read may not produce. Every other
// failure is an error that names the key.
func (s *S3Store) getForBulk(ctx context.Context, key string) (rec Record, exists bool, err error) {
	res, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:              aws.String(s.bucket),
		Key:                 aws.String(s.objectKey(key)),
		ExpectedBucketOwner: s.expectedOwner(),
	})
	if err != nil {
		if missingKey(err) {
			return Record{}, false, nil
		}
		if status, ok := httpStatus(err); ok && status == http.StatusNotFound {
			return Record{}, false, fmt.Errorf("getting %q: %s: %w", key, s.notTheKey(err), err)
		}
		if denied := asKMSDenied(err); denied != nil {
			return Record{}, false, fmt.Errorf("getting %q: %w", key, denied)
		}
		if unusable := asKMSKeyUnusable(err); unusable != nil {
			return Record{}, false, fmt.Errorf("getting %q: %w", key, unusable)
		}
		// After the two KMS cases, the same order s3OpError uses: a KMS
		// refusal has a remedy naming the bucket's owner does not.
		if foreign := asBucketOwnerMismatch(s.bucket, s.expectedBucketOwner, err); foreign != nil {
			return Record{}, false, fmt.Errorf("getting %q: %w", key, foreign)
		}
		return Record{}, false, fmt.Errorf("getting %q: %w", key, err)
	}
	payload, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if readErr != nil {
		return Record{}, false, fmt.Errorf("reading %q: %w", key, readErr)
	}
	return Record{Payload: payload, Version: aws.ToString(res.ETag)}, true, nil
}
