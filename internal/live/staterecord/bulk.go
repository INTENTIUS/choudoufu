// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
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
	_ BulkReader = (*SSMStore)(nil)
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

// GetAll reads every record under keyPrefix with one GetParametersByPath
// pagination — the identical call [SSMStore.List] already makes, keeping the
// values it already returns instead of throwing them away and fetching each
// one again with GetParameter. This is the backend the bulk read is worth
// most to: an estate of N instances costs ceil(N/10) API calls here rather
// than N.
func (s *SSMStore) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	folder := s.keyPrefix
	if i := strings.LastIndex(keyPrefix, "/"); i >= 0 {
		folder = s.parameterName(keyPrefix[:i])
	}

	out := map[string]Record{}
	var token *string
	for {
		res, err := s.client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:      aws.String(folder),
			Recursive: aws.Bool(true),
			NextToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("staterecord: ssm: reading everything under %q: %w", keyPrefix, err)
		}
		for _, p := range res.Parameters {
			key := s.keyFromParameterName(aws.ToString(p.Name))
			if !strings.HasPrefix(key, keyPrefix) {
				continue
			}
			payload, err := base64.StdEncoding.DecodeString(aws.ToString(p.Value))
			if err != nil {
				return nil, fmt.Errorf("staterecord: ssm: decoding the value stored for %q: %w", key, err)
			}
			out[key] = Record{Payload: payload, Version: strconv.FormatInt(p.Version, 10)}
		}
		if res.NextToken == nil || aws.ToString(res.NextToken) == "" {
			break
		}
		token = res.NextToken
	}
	return out, nil
}

// GetAll reads every record under keyPrefix: one ListObjectsV2 pagination,
// then one GetObject per key.
//
// S3 is the backend that genuinely cannot bulk-fetch. There is no batch-read
// operation in the S3 API — ListObjectsV2 returns each object's key and ETag
// but never its body — so this saves the LIST-plus-per-key-version round
// trips and nothing else, and N objects still cost N GetObject calls. That
// is S3's floor, not this function's. It is still worth having: it is one
// call at the [Store] seam, so a caller reads the namespace once instead of
// once per accessor, and the day S3 grows a batch read only this function
// changes.
//
// The gets are sequential on purpose. Overlapping N round trips is a
// different fix from not making them, with its own failure modes, and it
// belongs in whatever decides this backend's concurrency policy rather than
// arriving as a side effect of a bulk read.
func (s *S3Store) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	keys, err := s.List(ctx, keyPrefix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Record, len(keys))
	for _, key := range keys {
		res, err := s.client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(s.objectKey(key)),
		})
		if err != nil {
			if code, ok := httpStatus(err); ok && code == http.StatusNotFound {
				// Deleted between the list and the get: absent is the right
				// answer, said by leaving it out of the map.
				continue
			}
			return nil, fmt.Errorf("staterecord: s3: reading everything under %q: getting %q: %w", keyPrefix, key, err)
		}
		payload, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("staterecord: s3: reading %q: %w", key, readErr)
		}
		out[key] = Record{Payload: payload, Version: aws.ToString(res.ETag)}
	}
	return out, nil
}
