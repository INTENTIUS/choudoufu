// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// Pinning the bucket's owner. GitHub issue #1381, part of the bucket backend
// (#1332).
//
// A bucket name is global and a name that nobody holds can be taken by
// anyone. Nothing about the name says which account the bucket is in, so a
// bucket of the right name in someone else's account is a bucket an estate
// will write its records to, and a record holds secret material. That is not
// hypothetical for a published name: the runnable project's derived bucket
// name carries the account id in the name itself, so anyone who reads the
// example knows what to create if the real bucket ever stops existing.
//
// S3 has a request-level answer for this, ExpectedBucketOwner: a header on
// every request naming the account the caller believes owns the bucket, which
// S3 checks before it does anything else. [S3Config.ExpectedBucketOwner] puts
// it on every request this store makes, from the configuration's
// record_store bucket_owner. The policy half is the same guarantee from the
// other side, written as an aws:ResourceAccount condition on every Allow
// (examples/record-store-bucket/iam/render-policy.sh --account): either half
// alone leaves a gap, since the policy protects a role that carries it and
// the header protects a run whose credentials came from somewhere else.

// expectedOwnerPtr is the ExpectedBucketOwner field's value for an S3 input:
// the account, or nil when no owner is pinned. Every input this package
// builds gets it, and a missed one is a request that skips the check
// entirely, which is why s3_test.go asserts the header operation by
// operation rather than once.
func expectedOwnerPtr(owner string) *string {
	if owner == "" {
		return nil
	}
	return aws.String(owner)
}

// BucketOwnerMismatchError is an S3 request refused while this store was
// pinning the bucket's owner.
//
// S3 answers a request whose bucket belongs to an account other than
// ExpectedBucketOwner with 403 AccessDenied, which is byte for byte what an
// IAM denial looks like: the response cannot tell the two apart, and neither
// can this type. What it can do is say that the pin exists and name what it
// expects, so an operator who is about to go through the role's S3 statements
// for the third time is told there is a second thing to check and how to
// check it.
//
// It never claims the bucket IS owned by someone else. Proving that takes a
// call this run is by definition not allowed to make.
type BucketOwnerMismatchError struct {
	// Bucket is the bucket the request was for.
	Bucket string
	// ExpectedOwner is the account the configuration pinned, as twelve digits.
	ExpectedOwner string
	// Err is the S3 error as it arrived.
	Err error
}

func (e *BucketOwnerMismatchError) Unwrap() error { return e.Err }

func (e *BucketOwnerMismatchError) Error() string {
	return fmt.Sprintf("%s %s (%v)", e.Headline(), e.Remedy(), e.Err)
}

// Headline is the one sentence of what happened.
func (e *BucketOwnerMismatchError) Headline() string {
	return fmt.Sprintf("S3 refused this request, and bucket %q may be owned by an account other than %s.", e.Bucket, e.ExpectedOwner)
}

// Remedy says what to check, and in which order.
func (e *BucketOwnerMismatchError) Remedy() string {
	return fmt.Sprintf("Every request this estate makes carries that account as ExpectedBucketOwner, because record_store's bucket_owner names it, and S3 refuses a request for a bucket owned by anyone else with the same 403 AccessDenied an ordinary permission failure gets. So there are two things to check and the response says nothing about which it is. Who owns the bucket: aws s3api get-bucket-location --bucket %s --expected-bucket-owner %s, from credentials that can read it. Then the role's own S3 permissions. A bucket name is global, so a bucket of this name in another account is something anyone can create.", e.Bucket, e.ExpectedOwner)
}

// asBucketOwnerMismatch turns an S3 error into a *[BucketOwnerMismatchError]
// when the store is pinning an owner and the request was denied. It returns
// nil when no owner is pinned, so a store that names none cannot produce this
// error at all, and nil for any failure that is not a denial.
//
// A denial is whatever [accessDenied] says one is, the same classification
// the bucket contract's reads and the sentinel handshake use. The KMS
// classification in kmsdenied.go reads the same errors and runs first
// everywhere both apply, because a KMS refusal has a remedy this one does not.
func asBucketOwnerMismatch(bucket, expectedOwner string, err error) *BucketOwnerMismatchError {
	if expectedOwner == "" {
		return nil
	}
	if !accessDenied(err) {
		return nil
	}
	return &BucketOwnerMismatchError{Bucket: bucket, ExpectedOwner: expectedOwner, Err: err}
}
