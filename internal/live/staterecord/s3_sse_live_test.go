// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"crypto/md5" //nolint:gosec // comparing against S3's own MD5 ETag, not using it for security
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// sseBucketsEnvVar names the real buckets [TestS3StoreCASUnderEverySSEFlavour]
// runs against: a comma-separated list of flavour=bucket, the flavours being
// the keys of sseFlavours. The smoke scenario cas-holds-under-every-sse-flavour
// creates the buckets, sets this, and runs the test.
const sseBucketsEnvVar = "CHOUDOUFU_SSE_BUCKETS"

type sseFlavour struct {
	// algorithm is what HeadObject reports for an object in this bucket.
	algorithm s3types.ServerSideEncryption
	// etagIsMD5 is whether S3 documents the ETag of a single-part object
	// under this flavour as the MD5 of its content. It is for SSE-S3 and is
	// not for any KMS flavour, and that difference is the whole subject.
	etagIsMD5 bool
}

var sseFlavours = map[string]sseFlavour{
	"sse-s3":      {algorithm: s3types.ServerSideEncryptionAes256, etagIsMD5: true},
	"sse-kms-aws": {algorithm: s3types.ServerSideEncryptionAwsKms},
	"sse-kms-cmk": {algorithm: s3types.ServerSideEncryptionAwsKms},
	"dsse-kms":    {algorithm: s3types.ServerSideEncryptionAwsKmsDsse},
}

// TestS3StoreCASUnderEverySSEFlavour is GitHub issue #1344: the bucket
// backend (#1332) asserts nothing about a bucket's encryption flavour and
// requires that every one of them works. The argument for why it should is
// that the ETag is opaque here - SSE-KMS changes what an ETag's VALUE is, it
// stops being the content MD5, but not that it is a valid entity tag for
// If-Match, and [S3Store] hands back aws.ToString(out.ETag) untouched. This
// is the measurement, because an argument is not one.
//
// # Real AWS, and it skips without it
//
// An emulator's object store does not reproduce the ETag semantics that are
// the subject, so this cannot run in CI and is maintainer-run; the claims
// index says so. It therefore SKIPS when its buckets are not named, and a
// skip is not a pass: nothing here is evidence unless the run's output shows
// each flavour's subtests passing.
//
// # Why this cannot pass for the wrong reason
//
// Two checks per flavour come before the suite. The object written really is
// encrypted the way the flavour says (HeadObject), so a mislabelled bucket
// fails instead of quietly running SSE-S3 four times. And the ETag really
// does or does not equal the payload's MD5, as S3 documents for that
// flavour, so the KMS runs are known to have exercised an ETag that is NOT a
// content hash - the case the opaque-ETag argument is about. A store that
// computed or checked an MD5 would pass the first flavour and fail the rest.
func TestS3StoreCASUnderEverySSEFlavour(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv(sseBucketsEnvVar))
	if raw == "" {
		t.Skipf("%s is not set. This test needs real S3 buckets, one per SSE flavour, and is maintainer-run: `just smoke cas-holds-under-every-sse-flavour` creates them and runs it. A skip here is not a pass.", sseBucketsEnvVar)
	}
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatalf("loading AWS configuration: %v", err)
	}
	client := s3.NewFromConfig(awsCfg)

	seen := map[string]bool{}
	for _, pair := range strings.Split(raw, ",") {
		name, bucket, ok := strings.Cut(strings.TrimSpace(pair), "=")
		flavour, known := sseFlavours[name]
		if !ok || !known || bucket == "" {
			t.Fatalf("%s entry %q is not flavour=bucket with a flavour out of %v", sseBucketsEnvVar, pair, flavourNames())
		}
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			prefix := "cas-sse-test/" + randomKeySegment(t)
			t.Cleanup(func() { deleteEverythingUnder(t, client, bucket, prefix+"/") })

			probe, err := NewS3Store(S3Config{Client: client, Bucket: bucket, KeyPrefix: prefix + "/probe"})
			if err != nil {
				t.Fatal(err)
			}
			payload := []byte("a record payload, small, the way every record payload is")
			etag, err := probe.PutIfAbsent(ctx, "k", payload)
			if err != nil {
				t.Fatalf("writing the probe object: %v", err)
			}
			head, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(probe.ObjectKey("k"))})
			if err != nil {
				t.Fatalf("HeadObject on the probe: %v", err)
			}
			if head.ServerSideEncryption != flavour.algorithm {
				t.Fatalf("bucket %q encrypted the probe with %q, but it was given as %s (%q): the buckets are mislabelled, and running on would measure the wrong flavour", bucket, head.ServerSideEncryption, name, flavour.algorithm)
			}
			if name == "sse-kms-cmk" && strings.Contains(aws.ToString(head.SSEKMSKeyId), "alias/aws/s3") {
				t.Fatalf("the sse-kms-cmk bucket is using the AWS-managed key")
			}
			sum := md5.Sum(payload) //nolint:gosec // see the import
			md5ETag := `"` + hex.EncodeToString(sum[:]) + `"`
			if got := etag == md5ETag; got != flavour.etagIsMD5 {
				t.Fatalf("under %s the ETag %s %s the payload's MD5 %s, and S3 documents the opposite: either the bucket is not the flavour it was given as, or the premise of this test has moved", name, etag, map[bool]string{true: "equals", false: "does not equal"}[got], md5ETag)
			}
			t.Logf("%s: ServerSideEncryption=%s, ETag %s, which %s the content MD5", name, head.ServerSideEncryption, etag, map[bool]string{true: "IS", false: "is NOT"}[flavour.etagIsMD5])

			// The whole conditional-write contract, unchanged from what every
			// other backend is held to: If-None-Match creates, If-Match
			// updates and deletes, a 412 becomes a VersionConflictError
			// naming both versions.
			n := 0
			runConformance(t, func(t *testing.T) Store {
				t.Helper()
				n++
				store, err := NewS3Store(S3Config{Client: client, Bucket: bucket, KeyPrefix: fmt.Sprintf("%s/case-%03d", prefix, n)})
				if err != nil {
					t.Fatalf("NewS3Store: %v", err)
				}
				return store
			})
		})
	}
	for _, must := range []string{"sse-s3", "sse-kms-cmk"} {
		if !seen[must] {
			t.Errorf("%s did not name a %s bucket. #1344 requires at least SSE-S3 and SSE-KMS with a customer managed key; without the second, no ETag that differs from an MD5 was ever exercised.", sseBucketsEnvVar, must)
		}
	}
}

func flavourNames() []string {
	names := make([]string, 0, len(sseFlavours))
	for name := range sseFlavours {
		names = append(names, name)
	}
	return names
}

func deleteEverythingUnder(t *testing.T, client *s3.Client, bucket, prefix string) {
	t.Helper()
	ctx := context.Background()
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), ContinuationToken: token})
		if err != nil {
			t.Logf("cleanup: listing %s/%s: %v", bucket, prefix, err)
			return
		}
		for _, obj := range out.Contents {
			if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: obj.Key}); err != nil {
				t.Logf("cleanup: deleting %s: %v", aws.ToString(obj.Key), err)
			}
		}
		if out.NextContinuationToken == nil {
			return
		}
		token = out.NextContinuationToken
	}
}
