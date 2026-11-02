// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// The three mutations of bucketcontract.go that the audit on GitHub issue
// #1383 found alive: ignoring a prefix that arrives inside an And filter or
// as the deprecated top-level Prefix, dropping RestrictPublicBuckets from
// the four flags, and removing the status-only reading of a denial. Each
// test below was red against its mutation before it was kept.

// A rule's prefix arrives in one of three places. A reader that misses one
// reads the rule as unfiltered, and that is wrong in both directions: a rule
// for another prefix is credited as this estate's recovery window, and a
// rule that expires somebody else's logs is refused as one that deletes
// records.
func TestLifecyclePrefixIsReadWhereverItArrives(t *testing.T) {
	elsewhere := map[string]func(prefix string) s3types.LifecycleRule{
		"inside an And filter": func(prefix string) s3types.LifecycleRule {
			return s3types.LifecycleRule{
				ID:     aws.String("and-filter"),
				Status: s3types.ExpirationStatusEnabled,
				Filter: &s3types.LifecycleRuleFilter{And: &s3types.LifecycleRuleAndOperator{Prefix: aws.String(prefix)}},
			}
		},
		"as the deprecated top-level Prefix": func(prefix string) s3types.LifecycleRule {
			return s3types.LifecycleRule{
				ID:     aws.String("old-prefix"),
				Status: s3types.ExpirationStatusEnabled,
				Prefix: aws.String(prefix), //nolint:staticcheck // the deprecated field is what this case is about
			}
		},
	}
	for where, rule := range elsewhere {
		t.Run(where, func(t *testing.T) {
			// A noncurrent expiry scoped to keys this estate never writes is
			// not this estate's recovery window.
			other := rule("logs/")
			other.NoncurrentVersionExpiration = &s3types.NoncurrentVersionExpiration{NoncurrentDays: aws.Int32(30)}
			if f := lifecycleFinding(t, []s3types.LifecycleRule{other}); f.OK() {
				t.Errorf("a noncurrent expiry under logs/ was credited to an estate under tofu-*: %q", f.Found)
			}

			// An expiry of current objects scoped the same way deletes no
			// record, and must not be refused as if it did.
			logs := rule("logs/")
			logs.Expiration = &s3types.LifecycleExpiration{Days: aws.Int32(7)}
			f := lifecycleFinding(t, []s3types.LifecycleRule{expiresNoncurrent("keep", 30), logs})
			if f.Unwaivable || !f.OK() {
				t.Errorf("a rule that expires logs/ was read as reaching the records: OK=%v DeletesRecords=%v %q", f.OK(), f.Unwaivable, f.Found)
			}

			// The control for both: the same shapes over the estate's own
			// keys do reach them, so the two results above come from the
			// prefix and not from the shape being ignored altogether.
			mine := rule("tofu-records/prod/")
			mine.Expiration = &s3types.LifecycleExpiration{Days: aws.Int32(7)}
			f = lifecycleFinding(t, []s3types.LifecycleRule{expiresNoncurrent("keep", 30), mine})
			if !f.Unwaivable {
				t.Errorf("a rule that expires tofu-records/prod/ was not read as deleting records: %q", f.Found)
			}
		})
	}
}

// RestrictPublicBuckets is the fourth flag and the one a loop over three
// would miss. It is also the one that matters once a public policy already
// exists, since the other three only stop new ones.
func TestPublicAccessBlockNeedsEachOfTheFourFlags(t *testing.T) {
	for _, flag := range []string{"BlockPublicAcls", "IgnorePublicAcls", "BlockPublicPolicy", "RestrictPublicBuckets"} {
		t.Run(flag, func(t *testing.T) {
			b := correctBucket()
			cfg := allFourOn()
			switch flag {
			case "BlockPublicAcls":
				cfg.BlockPublicAcls = aws.Bool(false)
			case "IgnorePublicAcls":
				cfg.IgnorePublicAcls = aws.Bool(false)
			case "BlockPublicPolicy":
				cfg.BlockPublicPolicy = aws.Bool(false)
			case "RestrictPublicBuckets":
				cfg.RestrictPublicBuckets = aws.Bool(false)
			}
			b.pab = cfg
			f := settingFinding(t, b, BucketPublicAccessBlock)
			if f.OK() {
				t.Fatalf("the bucket passed with %s off: %q", flag, f.Found)
			}
			if !strings.Contains(f.Found, flag) {
				t.Errorf("the finding does not name %s: %q", flag, f.Found)
			}
		})
	}
}

// statusOnly is an S3 failure that carries an HTTP status and no error code
// this package knows, which is what an S3-compatible store or a proxy in
// front of one sends.
func statusOnly(status int) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      errors.New("a body this client could not parse"),
	}
}

// A 403 with no recognisable code is still a denial: the setting is
// unreadable, which is a finding the operator can act on. Read as an outage
// it would fail the whole check with "could not be checked", and send them
// to the network.
func TestBucketContractReadsABare403AsUnreadable(t *testing.T) {
	b := correctBucket()
	b.pabErr = statusOnly(http.StatusForbidden)
	f := settingFinding(t, b, BucketPublicAccessBlock)
	if f.OK() || f.Outcome != Unreadable {
		t.Errorf("a 403 with no error code: OK=%v Unreadable=%v %q", f.OK(), f.Outcome == Unreadable, f.Found)
	}

	// The control. A 500 of the same shape is a read that never happened,
	// and stays an error.
	b = correctBucket()
	b.pabErr = statusOnly(http.StatusInternalServerError)
	if findings, err := CheckBucketContract(context.Background(), b, "the-bucket", "", estateNamespaces); err == nil {
		t.Errorf("a 500 produced findings instead of an error: %+v", findings)
	}
}

func settingFinding(t *testing.T, b *fakeBucket, setting Setting) Finding {
	t.Helper()
	findings, err := CheckBucketContract(context.Background(), b, "the-bucket", "", estateNamespaces)
	if err != nil {
		t.Fatalf("CheckBucketContract: %v", err)
	}
	for _, f := range findings {
		if f.Setting == setting {
			return f
		}
	}
	t.Fatalf("no %s finding", setting)
	return Finding{}
}
