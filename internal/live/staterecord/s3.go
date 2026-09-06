// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3Store is a [Store] backed by S3 object versions via conditional
// writes: If-Match and If-None-Match, the ETag-based compare-and-swap
// primitive S3 added for general-purpose buckets. This is the store's
// strongest offering — a real, server-enforced CAS, not a
// read-compare-write approximation — and a version here is exactly an
// object's ETag, unmodified.
//
// # What is genuinely atomic
//
// Every conditional operation is a single S3 request carrying the
// condition; there is no read-compare-write window for this store to
// document a caveat about, unlike [SSMStore]:
//
//   - [S3Store.PutIfAbsent] and a "" [S3Store.PutIfVersion] call send
//     If-None-Match: * — S3 rejects the write with HTTP 412 if any object
//     already exists at the key.
//   - A non-"" [S3Store.PutIfVersion] call sends If-Match: <version> — S3
//     rejects the write with HTTP 412 if the object's current ETag does not
//     match.
//   - [S3Store.Delete] sends If-Match: <version> on DeleteObject, which S3
//     honors for general-purpose buckets, not only the directory-bucket
//     case the S3 API docs otherwise reserve conditional deletes for.
//
// On a 412, this store issues one extra read (Get) purely to populate
// [VersionConflictError.ActualVersion] with an accurate answer; that read
// is not part of the conditional guarantee itself; the conditional write
// already failed atomically before it.
//
// # What this store does not manage
//
// Bucket creation, lifecycle policy, and encryption configuration are the
// caller's concern — S3Store only issues GetObject/PutObject/DeleteObject/
// ListObjectsV2 against a bucket and (optional) key prefix it is given. It
// does not build or authenticate the [s3.Client] itself; the caller
// supplies one already configured for the target account, region and
// endpoint, which is what keeps this store's own surface free of anything
// AWS-credential-shaped.
type S3Store struct {
	client    *s3.Client
	bucket    string
	keyPrefix string
}

// S3Config configures an [S3Store].
type S3Config struct {
	// Client is the S3 client every call goes through. The caller builds
	// and authenticates it — region, credentials, any endpoint override
	// for a local emulator — this package has no opinion on any of that.
	Client *s3.Client

	// Bucket is the S3 bucket every key lives in.
	Bucket string

	// KeyPrefix is joined ahead of every key this store is asked for, so
	// one bucket can host more than one caller's keyspace without either
	// seeing the other's keys in [S3Store.List]. Empty means keys map
	// directly to object keys. This package does not interpret
	// KeyPrefix's structure at all — it is an opaque string, the same as
	// every key passed to the [Store] interface.
	KeyPrefix string
}

// NewS3Store builds an [S3Store] from cfg.
func NewS3Store(cfg S3Config) (*S3Store, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("staterecord: s3: Client must not be nil")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("staterecord: s3: Bucket must not be empty")
	}
	return &S3Store{
		client:    cfg.Client,
		bucket:    cfg.Bucket,
		keyPrefix: cfg.KeyPrefix,
	}, nil
}

// objectKey joins s.keyPrefix and key into the object key sent to S3.
func (s *S3Store) objectKey(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return strings.TrimSuffix(s.keyPrefix, "/") + "/" + key
}

// ObjectKey is the S3 object key this store will read and write key at:
// [S3Config.KeyPrefix] joined ahead of it. Exported for the same reason
// [SSMStore.ParameterName] is - see that method's comment and issue #916.
func (s *S3Store) ObjectKey(key string) string {
	return s.objectKey(key)
}

// keyFromObjectKey reverses objectKey, for turning ListObjectsV2 results
// back into the opaque keys [Store.List] promises.
func (s *S3Store) keyFromObjectKey(objectKey string) string {
	if s.keyPrefix == "" {
		return objectKey
	}
	return strings.TrimPrefix(objectKey, strings.TrimSuffix(s.keyPrefix, "/")+"/")
}

// httpStatus extracts the HTTP status code from an aws-sdk-go-v2 error,
// generically — via smithy's own response-error wrapper rather than any
// S3-specific error type, so this works the same for every status code
// this store cares about (404, 412) without a type switch per case.
func httpStatus(err error) (int, bool) {
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		return respErr.HTTPStatusCode(), true
	}
	return 0, false
}

// Get implements [Store].
func (s *S3Store) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, "", false, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if code, ok := httpStatus(err); ok && code == http.StatusNotFound {
			return nil, "", false, nil
		}
		return nil, "", false, fmt.Errorf("staterecord: s3: getting %q: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	payload, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", false, fmt.Errorf("staterecord: s3: reading %q: %w", key, err)
	}
	return payload, aws.ToString(out.ETag), true, nil
}

// conflictError builds the *[VersionConflictError] for a 412 response: a
// best-effort read to report the record's true current state, since the
// conditional write itself already failed atomically before this call
// makes it.
func (s *S3Store) conflictError(ctx context.Context, key, expectedVersion string) error {
	_, actual, exists, err := s.Get(ctx, key)
	if err != nil {
		return err
	}
	av := ""
	if exists {
		av = actual
	}
	return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: av}
}

// PutIfAbsent implements [Store].
func (s *S3Store) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	return s.PutIfVersion(ctx, key, payload, "")
}

// PutIfVersion implements [Store]. expectedVersion == "" sends
// If-None-Match: *; any other value sends If-Match: <expectedVersion> —
// see the type doc for why both are a single atomic S3 request rather
// than a read-compare-write.
func (s *S3Store) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
		Body:   bytes.NewReader(payload),
	}
	if expectedVersion == "" {
		input.IfNoneMatch = aws.String("*")
	} else {
		input.IfMatch = aws.String(expectedVersion)
	}
	out, err := s.client.PutObject(ctx, input)
	if err != nil {
		if code, ok := httpStatus(err); ok && code == http.StatusPreconditionFailed {
			return "", s.conflictError(ctx, key, expectedVersion)
		}
		return "", fmt.Errorf("staterecord: s3: writing %q: %w", key, err)
	}
	return aws.ToString(out.ETag), nil
}

// Delete implements [Store]. expectedVersion == "" against an absent key
// is a no-op (checked with a HeadObject first, since DeleteObject's
// If-Match has no "only if absent" form); any other value sends
// If-Match: <expectedVersion> on DeleteObject itself, S3's real
// conditional delete.
func (s *S3Store) Delete(ctx context.Context, key string, expectedVersion string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if expectedVersion == "" {
		_, _, exists, err := s.Get(ctx, key)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		return s.conflictError(ctx, key, expectedVersion)
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:  aws.String(s.bucket),
		Key:     aws.String(s.objectKey(key)),
		IfMatch: aws.String(expectedVersion),
	})
	if err != nil {
		if code, ok := httpStatus(err); ok && (code == http.StatusPreconditionFailed || code == http.StatusNotFound) {
			return s.conflictError(ctx, key, expectedVersion)
		}
		return fmt.Errorf("staterecord: s3: deleting %q: %w", key, err)
	}
	return nil
}

// List implements [Store] by paginating ListObjectsV2 with Prefix set to
// this store's own key prefix plus keyPrefix — S3's list primitive is
// already an ordinary string prefix, the same contract [Store.List]
// promises, so no client-side filtering beyond stripping s.keyPrefix back
// off is needed (unlike [SSMStore.List]).
func (s *S3Store) List(ctx context.Context, keyPrefix string) ([]string, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	prefix := s.objectKey(keyPrefix)

	var keys []string
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("staterecord: s3: listing %q: %w", keyPrefix, err)
		}
		for _, obj := range out.Contents {
			keys = append(keys, s.keyFromObjectKey(aws.ToString(obj.Key)))
		}
		if !aws.ToBool(out.IsTruncated) || aws.ToString(out.NextContinuationToken) == "" {
			break
		}
		token = out.NextContinuationToken
	}
	sort.Strings(keys)
	return keys, nil
}
