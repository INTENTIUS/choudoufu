// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// RecordVersion is one version of one record as its store keeps it. It
// carries no payload: a history says when a record changed, and reading what
// it held is a separate, deliberate act (s3:GetObjectVersion). GitHub issue
// #1954.
type RecordVersion struct {
	// VersionID is the backend's own name for the version, the one an
	// operator passes to `aws s3api get-object --version-id`.
	VersionID string

	// LastModified is when the version was written.
	LastModified time.Time

	// Current is true for the version a Get returns today. A record whose
	// newest version is a delete marker has no current version.
	Current bool

	// Deleted is true for a delete marker: the record was removed then.
	Deleted bool
}

// VersionLister is a [Store] that keeps the past versions of a record. Only
// [S3Store] is one: the bucket contract refuses a bucket without versioning,
// so every overwrite and delete leaves a noncurrent version behind. The
// local and Kubernetes stores replace a record in place and keep nothing.
type VersionLister interface {
	// ListVersions returns every version of key the store still holds,
	// newest first, delete markers included. It returns nil and no error for
	// a key that never existed or whose versions have all expired.
	ListVersions(ctx context.Context, key string) ([]RecordVersion, error)
}

// AsVersionLister finds the [VersionLister] under s's wrappers, the same walk
// [AsContractChecker] makes. false means the store keeps no past versions.
func AsVersionLister(s Store) (VersionLister, bool) {
	for s != nil {
		if v, ok := s.(VersionLister); ok {
			return v, true
		}
		u, ok := s.(interface{ Unwrap() Store })
		if !ok {
			return nil, false
		}
		s = u.Unwrap()
	}
	return nil, false
}

// ListVersions implements [VersionLister] with ListObjectVersions, which
// takes s3:ListBucketVersions. The estate's own role does not carry that
// grant (examples/record-store-bucket/iam), so a denial here is expected
// under it and is returned as S3 said it.
//
// ListObjectVersions matches a prefix, so a key that another key extends
// ("type/abc" and "type/abc/def", which RecordKey's chunking can produce)
// would share a listing; only versions of exactly this object key are kept.
// S3 returns versions and delete markers as two lists, each newest first, so
// they are merged on LastModified with the current version ahead of any tie.
func (s *S3Store) ListVersions(ctx context.Context, key string) ([]RecordVersion, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	objectKey := s.objectKey(key)
	in := &s3.ListObjectVersionsInput{
		Bucket:              aws.String(s.bucket),
		Prefix:              aws.String(objectKey),
		ExpectedBucketOwner: s.expectedOwner(),
	}
	var out []RecordVersion
	for {
		page, err := s.client.ListObjectVersions(ctx, in)
		if err != nil {
			return nil, s.opError("listing the versions of", key, err)
		}
		for _, v := range page.Versions {
			if aws.ToString(v.Key) != objectKey {
				continue
			}
			out = append(out, RecordVersion{
				VersionID:    aws.ToString(v.VersionId),
				LastModified: aws.ToTime(v.LastModified),
				Current:      aws.ToBool(v.IsLatest),
			})
		}
		for _, m := range page.DeleteMarkers {
			if aws.ToString(m.Key) != objectKey {
				continue
			}
			out = append(out, RecordVersion{
				VersionID:    aws.ToString(m.VersionId),
				LastModified: aws.ToTime(m.LastModified),
				Deleted:      true,
				// A delete marker that is latest means the record is gone;
				// it is not a current version anyone can Get.
			})
		}
		if !aws.ToBool(page.IsTruncated) {
			break
		}
		if page.NextKeyMarker == nil && page.NextVersionIdMarker == nil {
			return nil, fmt.Errorf("staterecord: s3: listing the versions of %q: the bucket said the listing was truncated and gave no marker to continue from", key)
		}
		in.KeyMarker = page.NextKeyMarker
		in.VersionIdMarker = page.NextVersionIdMarker
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastModified.Equal(out[j].LastModified) {
			return out[i].LastModified.After(out[j].LastModified)
		}
		return out[i].Current && !out[j].Current
	})
	return out, nil
}
