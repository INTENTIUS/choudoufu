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
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

// GCSStore is a [Store] backed by Google Cloud Storage objects, using the
// object's generation as the version. GitHub issue #1119, the GCP platform
// epic.
//
// # What is genuinely atomic
//
// GCS gives every write of an object a new generation number, and every
// object request can carry a precondition on it. So, as with [S3Store],
// each conditional operation is one request carrying its condition, and
// nothing is locked:
//
//   - [GCSStore.PutIfAbsent] and a "" [GCSStore.PutIfVersion] call send
//     ifGenerationMatch=0, which GCS defines as "only if no live object
//     exists".
//   - A non-"" [GCSStore.PutIfVersion] call sends ifGenerationMatch with the
//     generation the writer read.
//   - [GCSStore.Delete] sends ifGenerationMatch on the delete.
//
// GCS answers a failed precondition with 412, and that is mapped to a
// [VersionConflictError] after one re-read that names the generation the
// store holds now, the way S3's If-Match failure is.
//
// A version is the generation in decimal. It is never "": GCS assigns
// generations from a clock and never assigns 0.
//
// # What this store does not manage
//
// The bucket, its versioning, lifecycle and public access prevention are the
// caller's; see gcscontract.go for what is asserted about them. The client is
// built by the caller too (internal/live/projection builds it the way stock's
// gcs backend does), so nothing credential-shaped is in this file.
type GCSStore struct {
	client    *storage.Client
	bucket    string
	keyPrefix string

	// encryptionKey is a customer-supplied key (stock's encryption_key),
	// sent with every object request, and kmsKeyName a Cloud KMS key
	// (kms_encryption_key) named on every write. At most one is set.
	encryptionKey []byte
	kmsKeyName    string

	getAllParallelism int

	baseTags map[string]string
}

// GCSConfig configures a [GCSStore].
type GCSConfig struct {
	// Client is the storage client every call goes through, built and
	// authenticated by the caller.
	Client *storage.Client

	// Bucket is the GCS bucket every key lives in.
	Bucket string

	// KeyPrefix is joined ahead of every key, as [S3Config.KeyPrefix] is.
	KeyPrefix string

	// EncryptionKey is a 32 byte customer-supplied encryption key, or nil.
	EncryptionKey []byte

	// KMSKeyName is the Cloud KMS key every write names, or "".
	KMSKeyName string

	// GetAllParallelism bounds how many object reads [GCSStore.GetAll] has in
	// flight at once. Zero or negative takes [DefaultS3GetAllParallelism],
	// which is a number about fanning reads out and not about S3.
	GetAllParallelism int

	// BaseTags go on every object this store writes, as custom metadata.
	// See [S3Config.BaseTags]; the estate's tag is set here.
	BaseTags map[string]string
}

// NewGCSStore builds a [GCSStore] from cfg.
func NewGCSStore(cfg GCSConfig) (*GCSStore, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("staterecord: gcs: Client must not be nil")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("staterecord: gcs: Bucket must not be empty")
	}
	if len(cfg.EncryptionKey) > 0 && cfg.KMSKeyName != "" {
		return nil, fmt.Errorf("staterecord: gcs: a customer-supplied encryption key and a KMS key are mutually exclusive")
	}
	return &GCSStore{
		client:            cfg.Client,
		bucket:            cfg.Bucket,
		keyPrefix:         cfg.KeyPrefix,
		encryptionKey:     cfg.EncryptionKey,
		kmsKeyName:        cfg.KMSKeyName,
		getAllParallelism: cfg.GetAllParallelism,
		baseTags:          cfg.BaseTags,
	}, nil
}

// objectName joins s.keyPrefix and key into the object name sent to GCS.
func (s *GCSStore) objectName(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return strings.TrimSuffix(s.keyPrefix, "/") + "/" + key
}

// ObjectName is the GCS object name this store reads and writes key at, so
// a caller can say where a record is in words gcloud takes.
func (s *GCSStore) ObjectName(key string) string {
	return s.objectName(key)
}

func (s *GCSStore) keyFromObjectName(name string) string {
	if s.keyPrefix == "" {
		return name
	}
	return strings.TrimPrefix(name, strings.TrimSuffix(s.keyPrefix, "/")+"/")
}

// object is the handle for key, carrying the customer-supplied key when
// there is one: GCS refuses every read of an object written with one unless
// the request carries the same key.
func (s *GCSStore) object(key string) *storage.ObjectHandle {
	o := s.client.Bucket(s.bucket).Object(s.objectName(key))
	if len(s.encryptionKey) > 0 {
		o = o.Key(s.encryptionKey)
	}
	return o
}

// gcsStatus is the HTTP status a GCS error carries, when it carries one.
func gcsStatus(err error) (int, bool) {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code, true
	}
	return 0, false
}

// gcsBucketMissing reports whether err is GCS saying the BUCKET is not
// there. The client reports every 404 on an object request as
// [storage.ErrObjectNotExist], so the status cannot tell a missing record
// from a missing bucket, and the second must never read as the first: an
// empty estate is what a missing record means to a plan. This is
// [missingKey]'s distinction (GitHub issue #1383) in GCS's words, which put
// it in the message rather than in a code.
func gcsBucketMissing(err error) bool {
	if errors.Is(err, storage.ErrBucketNotExist) {
		return true
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(gerr.Message+" "+gerr.Body), "bucket does not exist")
}

// gcsMissingObject reports whether err says the object, and not the bucket,
// is absent.
func gcsMissingObject(err error) bool {
	if gcsBucketMissing(err) {
		return false
	}
	if errors.Is(err, storage.ErrObjectNotExist) {
		return true
	}
	status, ok := gcsStatus(err)
	return ok && status == http.StatusNotFound
}

func (s *GCSStore) bucketGone(doing, key string, err error) error {
	return fmt.Errorf("staterecord: gcs: %s %q: bucket %q does not exist, so the 404 is the bucket's own and not a missing record: %w", doing, key, s.bucket, err)
}

func (s *GCSStore) opError(doing, key string, err error) error {
	return fmt.Errorf("staterecord: gcs: %s %q in bucket %q: %w", doing, key, s.bucket, err)
}

// read is one object read: the payload and its generation. exists is false,
// with no error, only when the object is absent and the bucket is not.
func (s *GCSStore) read(ctx context.Context, key string) (payload []byte, version string, exists bool, err error) {
	r, err := s.object(key).NewReader(ctx)
	if err != nil {
		if gcsMissingObject(err) {
			return nil, "", false, nil
		}
		if gcsBucketMissing(err) {
			return nil, "", false, s.bucketGone("getting", key, err)
		}
		return nil, "", false, s.opError("getting", key, err)
	}
	defer func() { _ = r.Close() }()
	payload, err = io.ReadAll(r)
	if err != nil {
		return nil, "", false, fmt.Errorf("staterecord: gcs: reading %q: %w", key, err)
	}
	gen := r.Attrs.Generation
	if gen <= 0 {
		// A version of "" means absent to every caller, so a read that came
		// back with no generation cannot be returned as a record.
		return nil, "", false, fmt.Errorf("staterecord: gcs: reading %q: the response carried no object generation, so there is no version a conditional write could name", key)
	}
	return payload, strconv.FormatInt(gen, 10), true, nil
}

// Get implements [Store].
func (s *GCSStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, "", false, err
	}
	return s.read(ctx, key)
}

// conflictError builds the *[VersionConflictError] for a failed
// precondition, with a re-read naming what the store holds now. See
// [S3Store.conflictError]; this is the same rule.
func (s *GCSStore) conflictError(ctx context.Context, key, expectedVersion string, cause error) error {
	_, actual, exists, err := s.read(ctx, key)
	if err != nil {
		if cause == nil {
			return fmt.Errorf("staterecord: gcs: %q failed its version condition, and the re-read that would name the version the store now holds failed too: %w", key, err)
		}
		return fmt.Errorf("staterecord: gcs: %q failed its version condition, and the re-read that would name the version the store now holds failed too: %w (the condition failure: %w)", key, err, cause)
	}
	if !exists {
		actual = ""
	}
	return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: actual}
}

// generation parses a version this store handed out. Anything else cannot
// be the version of an object here, so it is a conflict rather than a
// request GCS would answer for some other generation.
func generation(version string) (int64, bool) {
	gen, err := strconv.ParseInt(version, 10, 64)
	if err != nil || gen <= 0 {
		return 0, false
	}
	return gen, true
}

// PutIfAbsent implements [Store].
func (s *GCSStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	return s.PutIfVersion(ctx, key, payload, "")
}

// PutIfVersion implements [Store]: ifGenerationMatch=0 for a create, and
// the generation read for an update.
func (s *GCSStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	conds := storage.Conditions{DoesNotExist: true}
	if expectedVersion != "" {
		gen, ok := generation(expectedVersion)
		if !ok {
			return "", s.conflictError(ctx, key, expectedVersion, nil)
		}
		conds = storage.Conditions{GenerationMatch: gen}
	}

	w := s.object(key).If(conds).NewWriter(ctx)
	// One request: a resumable upload is a session of several, and a record
	// is a few kilobytes.
	w.ChunkSize = 0
	w.ContentType = "application/octet-stream"
	if s.kmsKeyName != "" {
		w.KMSKeyName = s.kmsKeyName
	}
	// The tags ride the write itself as custom metadata, so the object never
	// exists without them. BaseTags is merged LAST so the estate cannot be
	// overwritten by a per-write tag; see [S3Store.PutIfVersion].
	if md := mergeTags(ObjectTags(ctx), s.baseTags); len(md) > 0 {
		w.Metadata = md
	}
	if _, err := io.Copy(w, bytes.NewReader(payload)); err != nil {
		_ = w.Close()
		return "", s.writeError(ctx, key, expectedVersion, err)
	}
	if err := w.Close(); err != nil {
		return "", s.writeError(ctx, key, expectedVersion, err)
	}
	attrs := w.Attrs()
	if attrs == nil || attrs.Generation <= 0 {
		return "", fmt.Errorf("staterecord: gcs: writing %q: the write succeeded and its response carried no generation, so the record's new version is unknown", key)
	}
	return strconv.FormatInt(attrs.Generation, 10), nil
}

func (s *GCSStore) writeError(ctx context.Context, key, expectedVersion string, err error) error {
	if status, ok := gcsStatus(err); ok && status == http.StatusPreconditionFailed {
		return s.conflictError(ctx, key, expectedVersion, err)
	}
	if gcsBucketMissing(err) {
		return s.bucketGone("writing", key, err)
	}
	// An update whose object is gone. GCS answers that with 412, but a 404
	// that names the object is the same conflict from the caller's side, as
	// it is on S3 (GitHub issue #1344).
	if gcsMissingObject(err) && expectedVersion != "" {
		return s.conflictError(ctx, key, expectedVersion, err)
	}
	return s.opError("writing", key, err)
}

// mergeTags is the tag sets merged, later sets winning on a shared key.
func mergeTags(sets ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, set := range sets {
		for k, v := range set {
			merged[k] = v
		}
	}
	return merged
}

// Delete implements [Store]. expectedVersion == "" against an absent key is
// a no-op, checked with a read; any other value sends ifGenerationMatch on
// the delete itself.
func (s *GCSStore) Delete(ctx context.Context, key string, expectedVersion string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if expectedVersion == "" {
		_, _, exists, err := s.read(ctx, key)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		return s.conflictError(ctx, key, expectedVersion, nil)
	}
	gen, ok := generation(expectedVersion)
	if !ok {
		return s.conflictError(ctx, key, expectedVersion, nil)
	}
	// No customer-supplied key on a delete: GCS does not need one to delete,
	// and the handle without it is the same object.
	err := s.client.Bucket(s.bucket).Object(s.objectName(key)).If(storage.Conditions{GenerationMatch: gen}).Delete(ctx)
	if err == nil {
		return nil
	}
	if status, ok := gcsStatus(err); ok && status == http.StatusPreconditionFailed {
		return s.conflictError(ctx, key, expectedVersion, err)
	}
	if gcsBucketMissing(err) {
		return s.bucketGone("deleting", key, err)
	}
	if gcsMissingObject(err) {
		return s.conflictError(ctx, key, expectedVersion, err)
	}
	return s.opError("deleting", key, err)
}

// List implements [Store] with objects.list on this store's prefix plus
// keyPrefix. GCS's prefix is a plain string prefix, as [Store.List]
// promises, and without versions=true it returns live objects only, so a
// noncurrent version of a deleted record is not listed.
func (s *GCSStore) List(ctx context.Context, keyPrefix string) ([]string, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	q := &storage.Query{Prefix: s.objectName(keyPrefix)}
	if err := q.SetAttrSelection([]string{"Name"}); err != nil {
		return nil, fmt.Errorf("staterecord: gcs: listing %q: %w", keyPrefix, err)
	}
	it := s.client.Bucket(s.bucket).Objects(ctx, q)
	var keys []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			// A LIST has no "the key is absent" answer, so a 404 here is the
			// bucket's.
			if status, ok := gcsStatus(err); (ok && status == http.StatusNotFound) || errors.Is(err, storage.ErrBucketNotExist) {
				return nil, s.bucketGone("listing", keyPrefix, err)
			}
			return nil, s.opError("listing", keyPrefix, err)
		}
		keys = append(keys, s.keyFromObjectName(attrs.Name))
	}
	sort.Strings(keys)
	return keys, nil
}

// GetAll implements [BulkReader]: one listing, then one read per key, at
// most [GCSConfig.GetAllParallelism] in flight. The rules are
// [S3Store.GetAll]'s, and for the same reason (GitHub issue #1355): the
// result is complete or the call fails, and a key the listing named that
// two reads cannot find fails the whole read rather than being left out.
func (s *GCSStore) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
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

	found := make([]*Record, len(keys))
	err = boundedFanOut(ctx, len(keys), workers, func(ctx context.Context, i int) error {
		for attempt := 0; attempt < 2; attempt++ {
			payload, version, exists, getErr := s.read(ctx, keys[i])
			if getErr != nil {
				return getErr
			}
			if exists {
				found[i] = &Record{Payload: payload, Version: version}
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("staterecord: gcs: reading everything under %q: %w", keyPrefix, err)
	}

	var vanished []string
	for i, rec := range found {
		if rec == nil {
			vanished = append(vanished, keys[i])
		}
	}
	if len(vanished) > 0 {
		return nil, fmt.Errorf("staterecord: gcs: reading everything under %q: %s, twice over: the listing and the reads that follow it disagree about what this namespace holds, so what came back is not a snapshot of it and is refused rather than returned short (GitHub issue #1355)", keyPrefix, namesVanished(vanished))
	}

	out := make(map[string]Record, len(keys))
	for i, rec := range found {
		out[keys[i]] = *rec
	}
	return out, nil
}
