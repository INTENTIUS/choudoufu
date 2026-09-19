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
func (s *LocalStore) GetAll(_ context.Context, keyPrefix string) (map[string]Record, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	out := map[string]Record{}
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
			// Removed between the walk and the read: absent is the right
			// answer, and leaving it out of the map is how absence is said.
			return nil
		}
		out[key] = Record{Payload: payload, Version: version}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("staterecord: local: reading everything under %q: %w", keyPrefix, err)
	}
	return out, nil
}

// DefaultS3GetAllParallelism is how many GetObject calls [S3Store.GetAll] has
// in flight at once unless [S3Config.GetAllParallelism] says otherwise.
//
// Eight is chosen to be unremarkable, not tuned. The namespace read here is
// the record-backed slice only - a small fraction of an estate - so the bound
// is not load-bearing, and it is configurable because the estate that needs
// otherwise will know why and should not have to patch the binary to find
// out. GitHub issue #1336.
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
// One omission is legitimate and is kept: a key that 404s between the LIST
// and its GET was deleted in between, and leaving it out of the map is the
// correct way to say so. That is a different thing from a GET that failed.
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

	// found[i] is keys[i]'s record, nil when it 404ed. Indexed, so no two
	// workers ever write the same memory and nothing here needs a lock.
	found := make([]*Record, len(keys))
	err = boundedFanOut(ctx, len(keys), workers, func(ctx context.Context, i int) error {
		rec, exists, getErr := s.getForBulk(ctx, keys[i])
		if getErr != nil {
			return getErr
		}
		if exists {
			found[i] = &rec
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("staterecord: s3: reading everything under %q: %w", keyPrefix, err)
	}

	out := make(map[string]Record, len(keys))
	for i, rec := range found {
		if rec != nil {
			out[keys[i]] = *rec
		}
	}
	return out, nil
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
func boundedFanOut(ctx context.Context, n, workers int, do func(ctx context.Context, i int) error) error {
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
// with no error, only for a 404: the key was deleted between the LIST and
// this GET. Every other failure is an error that names the key.
func (s *S3Store) getForBulk(ctx context.Context, key string) (rec Record, exists bool, err error) {
	res, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if code, ok := httpStatus(err); ok && code == http.StatusNotFound {
			return Record{}, false, nil
		}
		if denied := asKMSDenied(err); denied != nil {
			return Record{}, false, fmt.Errorf("getting %q: %w", key, denied)
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
