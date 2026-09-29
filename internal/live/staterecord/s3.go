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
// document a caveat about:
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

	// expectedBucketOwner rides every request as ExpectedBucketOwner when it
	// is set. See bucketowner.go.
	expectedBucketOwner string

	getAllParallelism int

	baseTags map[string]string
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

	// ExpectedBucketOwner is the AWS account that must own Bucket, as twelve
	// digits. Set, every request this store makes carries it as S3's
	// ExpectedBucketOwner and S3 refuses the request if the bucket belongs to
	// any other account. Empty means no check, which is the behaviour of
	// every build before GitHub issue #1381.
	//
	// It comes from record_store's bucket_owner. See bucketowner.go for why a
	// bucket's NAME is not an answer to whose bucket it is, and for what the
	// refusal looks like.
	ExpectedBucketOwner string

	// GetAllParallelism bounds how many GetObject calls [S3Store.GetAll] has
	// in flight at once. Zero or negative takes
	// [DefaultS3GetAllParallelism]; 1 is a sequential read.
	GetAllParallelism int

	// BaseTags go on every object this store writes. The record store sets
	// tofu-estate here, because every object in an estate's namespaces -
	// its records, its sentinel, its hint, its outputs - is the estate's,
	// whether or not it records a resource. See [WithObjectTags] for the
	// per-write half. GitHub issue #1337.
	BaseTags map[string]string
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

		expectedBucketOwner: cfg.ExpectedBucketOwner,

		getAllParallelism: cfg.GetAllParallelism,
		baseTags:          cfg.BaseTags,
	}, nil
}

// expectedOwner is the ExpectedBucketOwner every input this store builds
// carries: the pinned account, or nil when none is pinned.
func (s *S3Store) expectedOwner() *string {
	return expectedOwnerPtr(s.expectedBucketOwner)
}

// opError is [s3OpError] with this store's own bucket and pinned owner, so a
// denial while an owner is pinned says so. It is the only wrapper the store's
// record operations use.
func (s *S3Store) opError(doing, key string, err error) error {
	return s3OpError(s.bucket, s.expectedBucketOwner, doing, key, err)
}

// objectKey joins s.keyPrefix and key into the object key sent to S3.
func (s *S3Store) objectKey(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return strings.TrimSuffix(s.keyPrefix, "/") + "/" + key
}

// ObjectKey is the S3 object key this store will read and write key at:
// [S3Config.KeyPrefix] joined ahead of it. Exported so a caller can say
// where a record actually is, in words an operator can paste into the AWS
// CLI - issue #916.
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

// missingKey reports whether err is S3 saying the OBJECT is not there: a 404
// whose error code names the key rather than the bucket.
//
// S3 answers a missing bucket with 404 too, so the status on its own cannot
// tell the two apart, and this store used to not try: every 404 was read as
// absence. A bucket deleted or misrouted mid-run therefore read as an empty
// estate from Get, and as a version conflict from an update or a delete -
// and a conflict is exactly what internal/live/projection's unwritten-record
// ledger skips on purpose (issue #1287), so the run lost the only record it
// had that a write never landed. GitHub issue #1383.
//
// Two codes count as the key's absence. "NoSuchKey" is what real S3 sends.
// "NotFound" is what aws-sdk-go-v2 labels a 404 carrying no parseable <Code>
// at all (measured against the SDK, both with an empty body and with a body
// that is not S3's error XML), which is what some S3-compatible stores answer
// for a missing object. Reading an uncoded 404 as absence keeps those stores
// working, and the case it gets wrong - a compatible store that answers a
// bare 404 for a missing BUCKET - is one real S3 never produces, because real
// S3 always names NoSuchBucket.
//
// Every other coded 404, NoSuchBucket above all, is an error on every
// operation.
func missingKey(err error) bool {
	if status, ok := httpStatus(err); !ok || status != http.StatusNotFound {
		return false
	}
	switch apiErrorCode(err) {
	case "NoSuchKey", "NotFound", "":
		return true
	}
	return false
}

// notTheKey is the sentence a 404 gets when its code named something other
// than the key. It names the bucket, which no other error from this store
// needs to, because this is the one failure whose subject is the bucket
// rather than the record.
func (s *S3Store) notTheKey(err error) string {
	return fmt.Sprintf("bucket %q answered %s, so the 404 is the bucket's own and not a missing key", s.bucket, apiErrorCode(err))
}

// Get implements [Store].
func (s *S3Store) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, "", false, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:              aws.String(s.bucket),
		Key:                 aws.String(s.objectKey(key)),
		ExpectedBucketOwner: s.expectedOwner(),
	})
	if err != nil {
		if missingKey(err) {
			return nil, "", false, nil
		}
		if status, ok := httpStatus(err); ok && status == http.StatusNotFound {
			return nil, "", false, fmt.Errorf("staterecord: s3: getting %q: %s: %w", key, s.notTheKey(err), err)
		}
		return nil, "", false, s.opError("getting", key, err)
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
// makes it. cause is the refusal that brought us here, kept so it survives
// a re-read that fails.
func (s *S3Store) conflictError(ctx context.Context, key, expectedVersion string, cause error) error {
	_, actual, exists, err := s.Get(ctx, key)
	if err != nil {
		// Both halves matter and this used to return only the second. The
		// re-read exists purely to name the current version; when it fails,
		// what the conditional write itself was told is still the fact the
		// operator needs, and a re-read that fails for its own reason (a
		// bucket that just went away, a denial) says something else again.
		if cause == nil {
			// The absent-version delete path, where the condition was checked
			// by this store's own read and no request was refused.
			return fmt.Errorf("staterecord: s3: %q failed its version condition, and the re-read that would name the version the store now holds failed too: %w", key, err)
		}
		return fmt.Errorf("staterecord: s3: %q failed its version condition, and the re-read that would name the version the store now holds failed too: %w (the condition failure: %w)", key, err, cause)
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
		Bucket:              aws.String(s.bucket),
		Key:                 aws.String(s.objectKey(key)),
		Body:                bytes.NewReader(payload),
		ExpectedBucketOwner: s.expectedOwner(),
	}
	if expectedVersion == "" {
		input.IfNoneMatch = aws.String("*")
	} else {
		input.IfMatch = aws.String(expectedVersion)
	}
	// Tags ride the PutObject itself, so tagging costs no request and there
	// is no window in which the object exists untagged: a tag-conditioned
	// IAM policy reading a just-written object never sees it bare. A
	// PutObject REPLACES the object's tag set, so every write carries the
	// full set, and an object an older build wrote without tags is tagged by
	// the next write to it.
	//
	// [S3Config.BaseTags] is encoded LAST, so it wins on any key it defines.
	// The estate tag is the one that matters: store.go's contract is that an
	// object's tofu-estate can never name a different estate from the one the
	// store was opened for, and a context tag that could overwrite it would
	// make that a hope rather than a fact. A caller's per-write tags still
	// carry every key BaseTags does not define, which is the address half.
	if tagging := encodeObjectTagging(ObjectTags(ctx), s.baseTags); tagging != "" {
		input.Tagging = aws.String(tagging)
	}
	out, err := s.client.PutObject(ctx, input)
	if err != nil {
		status, ok := httpStatus(err)
		if ok && status == http.StatusPreconditionFailed {
			return "", s.conflictError(ctx, key, expectedVersion, err)
		}
		// An update whose record is GONE. Real S3 answers a PutObject that
		// carries If-Match for a key that does not exist with 404 NoSuchKey,
		// not with 412 - measured on GitHub issue #1344, where the
		// conformance suite first ran against real S3 and this case failed
		// under every flavour. It is the same conflict from the caller's
		// side: the version it read is not the version the store holds,
		// because the store holds none. Reported as one, with the actual
		// version re-read rather than assumed empty, since another writer may
		// have created the key again in between.
		//
		// Only for an update, and only for a 404 that named the KEY. A create
		// (If-None-Match) that meets a 404 has nothing to conflict with, and
		// a 404 that named the bucket is not about this record at all.
		if missingKey(err) && expectedVersion != "" {
			return "", s.conflictError(ctx, key, expectedVersion, err)
		}
		if ok && status == http.StatusNotFound && !missingKey(err) {
			return "", fmt.Errorf("staterecord: s3: writing %q: %s: %w", key, s.notTheKey(err), err)
		}
		return "", s.opError("writing", key, err)
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
		return s.conflictError(ctx, key, expectedVersion, nil)
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:              aws.String(s.bucket),
		Key:                 aws.String(s.objectKey(key)),
		IfMatch:             aws.String(expectedVersion),
		ExpectedBucketOwner: s.expectedOwner(),
	})
	if err != nil {
		status, ok := httpStatus(err)
		// A 412 is the version mismatch. A 404 that named the KEY is the same
		// conflict from the other side: the record the caller read is gone, so
		// the version it holds is not the store's. A 404 that named the BUCKET
		// is neither, and reporting it as a conflict would have this run's
		// unwritten-record ledger skip it (issue #1287).
		if ok && status == http.StatusPreconditionFailed {
			return s.conflictError(ctx, key, expectedVersion, err)
		}
		if missingKey(err) {
			return s.conflictError(ctx, key, expectedVersion, err)
		}
		if ok && status == http.StatusNotFound {
			return fmt.Errorf("staterecord: s3: deleting %q: %s: %w", key, s.notTheKey(err), err)
		}
		return s.opError("deleting", key, err)
	}
	return nil
}

// List implements [Store] by paginating ListObjectsV2 with Prefix set to
// this store's own key prefix plus keyPrefix — S3's list primitive is
// already an ordinary string prefix, the same contract [Store.List]
// promises, so no client-side filtering beyond stripping s.keyPrefix back
// off is needed.
func (s *S3Store) List(ctx context.Context, keyPrefix string) ([]string, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	prefix := s.objectKey(keyPrefix)

	var keys []string
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:              aws.String(s.bucket),
			Prefix:              aws.String(prefix),
			ContinuationToken:   token,
			ExpectedBucketOwner: s.expectedOwner(),
		})
		if err != nil {
			// A LIST has no "the key is absent" answer to be confused with,
			// so a 404 here was always an error. It still names the bucket,
			// so the operator reading it learns the same thing the other
			// operations now say.
			if status, ok := httpStatus(err); ok && status == http.StatusNotFound {
				return nil, fmt.Errorf("staterecord: s3: listing %q: %s: %w", keyPrefix, s.notTheKey(err), err)
			}
			return nil, s.opError("listing", keyPrefix, err)
		}
		for _, obj := range out.Contents {
			keys = append(keys, s.keyFromObjectKey(aws.ToString(obj.Key)))
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		// A page that says it is truncated and hands back no token to ask
		// for the rest with. This used to break out of the loop next to the
		// IsTruncated test and return what it had, with no error: a listing
		// short by an unknown number of keys, indistinguishable from a
		// complete one. Every consumer of this listing reads a key's absence
		// as the record's absence - [S3Store.GetAll] builds the plan-phase
		// snapshot from exactly these keys, and internal/live/projection's
		// orphan discovery treats the set as the estate's record-backed
		// resources - so a short listing is an estate with instances missing
		// from it and nothing said. GitHub issue #1355. Real S3 always sends
		// the token with the truncation; an S3-compatible store that does
		// not is refused here rather than silently believed.
		if aws.ToString(out.NextContinuationToken) == "" {
			return nil, fmt.Errorf("staterecord: s3: listing %q: bucket %q answered with a truncated page and no continuation token, so the rest of the listing cannot be asked for and what came back is short by an unknown number of keys; refusing rather than reading it as the whole namespace (GitHub issue #1355)", keyPrefix, s.bucket)
		}
		token = out.NextContinuationToken
	}
	sort.Strings(keys)
	return keys, nil
}
