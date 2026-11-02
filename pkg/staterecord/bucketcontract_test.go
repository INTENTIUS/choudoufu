// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// fakeBucket answers the three reads [CheckBucketContract] makes. A nil
// error with a nil output field means "the API's own not-configured answer",
// spelled the way S3 spells it for that setting.
type fakeBucket struct {
	versioning    s3types.BucketVersioningStatus
	versioningErr error

	rules        []s3types.LifecycleRule
	noLifecycle  bool
	lifecycleErr error

	pab    *s3types.PublicAccessBlockConfiguration
	pabErr error
}

func apiError(code string) error {
	return &smithy.GenericAPIError{Code: code, Message: code}
}

func (b *fakeBucket) GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	if b.versioningErr != nil {
		return nil, b.versioningErr
	}
	return &s3.GetBucketVersioningOutput{Status: b.versioning}, nil
}

func (b *fakeBucket) GetBucketLifecycleConfiguration(context.Context, *s3.GetBucketLifecycleConfigurationInput, ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	if b.lifecycleErr != nil {
		return nil, b.lifecycleErr
	}
	if b.noLifecycle {
		return nil, apiError("NoSuchLifecycleConfiguration")
	}
	return &s3.GetBucketLifecycleConfigurationOutput{Rules: b.rules}, nil
}

func (b *fakeBucket) GetPublicAccessBlock(context.Context, *s3.GetPublicAccessBlockInput, ...func(*s3.Options)) (*s3.GetPublicAccessBlockOutput, error) {
	if b.pabErr != nil {
		return nil, b.pabErr
	}
	if b.pab == nil {
		return nil, apiError("NoSuchPublicAccessBlockConfiguration")
	}
	return &s3.GetPublicAccessBlockOutput{PublicAccessBlockConfiguration: b.pab}, nil
}

func expiresNoncurrent(id string, days int32) s3types.LifecycleRule {
	return s3types.LifecycleRule{
		ID:                          aws.String(id),
		Status:                      s3types.ExpirationStatusEnabled,
		NoncurrentVersionExpiration: &s3types.NoncurrentVersionExpiration{NoncurrentDays: aws.Int32(days)},
	}
}

func allFourOn() *s3types.PublicAccessBlockConfiguration {
	return &s3types.PublicAccessBlockConfiguration{
		BlockPublicAcls: aws.Bool(true), IgnorePublicAcls: aws.Bool(true),
		BlockPublicPolicy: aws.Bool(true), RestrictPublicBuckets: aws.Bool(true),
	}
}

// correctBucket is a bucket that satisfies all three assertions. Every case
// below starts from it and breaks exactly one thing, so a case that fails
// for a second reason shows up as a second finding.
func correctBucket() *fakeBucket {
	return &fakeBucket{
		versioning: s3types.BucketVersioningStatusEnabled,
		rules:      []s3types.LifecycleRule{expiresNoncurrent("expire-noncurrent", 30)},
		pab:        allFourOn(),
	}
}

var estateNamespaces = []string{"tofu-records/prod/", "tofu-hints/prod/", "tofu-outputs/prod/"}

func TestBucketContract(t *testing.T) {
	transitionOnly := s3types.LifecycleRule{
		ID:     aws.String("to-glacier"),
		Status: s3types.ExpirationStatusEnabled,
		NoncurrentVersionTransitions: []s3types.NoncurrentVersionTransition{
			{NoncurrentDays: aws.Int32(30), StorageClass: s3types.TransitionStorageClassGlacier},
		},
	}
	abortOnly := s3types.LifecycleRule{
		ID:                             aws.String("abort-multipart"),
		Status:                         s3types.ExpirationStatusEnabled,
		AbortIncompleteMultipartUpload: &s3types.AbortIncompleteMultipartUpload{DaysAfterInitiation: aws.Int32(7)},
	}
	disabled := expiresNoncurrent("switched-off", 30)
	disabled.Status = s3types.ExpirationStatusDisabled
	elsewhere := expiresNoncurrent("logs-only", 30)
	elsewhere.Filter = &s3types.LifecycleRuleFilter{Prefix: aws.String("logs/")}
	recordsOnly := expiresNoncurrent("records-only", 30)
	recordsOnly.Filter = &s3types.LifecycleRuleFilter{Prefix: aws.String("tofu-records/")}
	everyRoot := expiresNoncurrent("every-tofu-root", 30)
	everyRoot.Filter = &s3types.LifecycleRuleFilter{Prefix: aws.String("tofu-")}
	byTag := expiresNoncurrent("by-tag", 30)
	byTag.Filter = &s3types.LifecycleRuleFilter{Tag: &s3types.Tag{Key: aws.String("tofu-estate"), Value: aws.String("prod")}}
	zeroDays := expiresNoncurrent("zero-days", 0)

	for _, tc := range []struct {
		name   string
		break_ func(*fakeBucket)
		// failing is the one setting expected to fail, "" when all pass.
		failing    Setting
		unreadable bool
		found      string
	}{
		{name: "a correct bucket passes all three"},
		{name: "a prefix filter every namespace sits under still covers", break_: func(b *fakeBucket) { b.rules = []s3types.LifecycleRule{everyRoot} }},

		{name: "versioning never enabled", break_: func(b *fakeBucket) { b.versioning = "" }, failing: BucketVersioning, found: "never been enabled"},
		{name: "versioning suspended", break_: func(b *fakeBucket) { b.versioning = s3types.BucketVersioningStatusSuspended }, failing: BucketVersioning, found: "Suspended"},

		{name: "no lifecycle configuration", break_: func(b *fakeBucket) { b.noLifecycle = true }, failing: BucketLifecycle, found: "no lifecycle configuration"},
		// The whole point of the assertion (#1339): a policy that exists and
		// expires nothing satisfies "has a lifecycle policy" and fixes nothing.
		{name: "a lifecycle that only transitions", break_: func(b *fakeBucket) { b.rules = []s3types.LifecycleRule{transitionOnly} }, failing: BucketLifecycle, found: "none of the lifecycle configuration's 1 rule(s) expires noncurrent versions"},
		{name: "a lifecycle that only aborts multipart uploads", break_: func(b *fakeBucket) { b.rules = []s3types.LifecycleRule{abortOnly, transitionOnly} }, failing: BucketLifecycle, found: "none of the lifecycle configuration's 2 rule(s)"},
		{name: "a lifecycle with no rules", break_: func(b *fakeBucket) { b.rules = nil }, failing: BucketLifecycle, found: "has no rules"},
		{name: "the expiring rule is disabled", break_: func(b *fakeBucket) { b.rules = []s3types.LifecycleRule{disabled} }, failing: BucketLifecycle, found: `"switched-off" expires noncurrent versions but is Disabled`},
		{name: "the expiring rule covers some other prefix", break_: func(b *fakeBucket) { b.rules = []s3types.LifecycleRule{elsewhere} }, failing: BucketLifecycle, found: `only under the prefix "logs/"`},
		{name: "the expiring rule covers the records but not the outputs", break_: func(b *fakeBucket) { b.rules = []s3types.LifecycleRule{recordsOnly} }, failing: BucketLifecycle, found: `does not contain "tofu-hints/prod/"`},
		{name: "the expiring rule is filtered by tag", break_: func(b *fakeBucket) { b.rules = []s3types.LifecycleRule{byTag} }, failing: BucketLifecycle, found: "filtered by tag"},
		{name: "zero days is not an expiry", break_: func(b *fakeBucket) { b.rules = []s3types.LifecycleRule{zeroDays} }, failing: BucketLifecycle, found: "none of the lifecycle"},

		{name: "no public-access block", break_: func(b *fakeBucket) { b.pab = nil }, failing: BucketPublicAccessBlock, found: "no public-access block configuration"},
		{name: "one public-access flag off", break_: func(b *fakeBucket) { b.pab.BlockPublicPolicy = aws.Bool(false) }, failing: BucketPublicAccessBlock, found: "BlockPublicPolicy off"},

		{name: "versioning unreadable", break_: func(b *fakeBucket) { b.versioningErr = apiError("AccessDenied") }, failing: BucketVersioning, unreadable: true, found: "s3:GetBucketVersioning was denied"},
		{name: "lifecycle unreadable", break_: func(b *fakeBucket) { b.lifecycleErr = apiError("AccessDenied") }, failing: BucketLifecycle, unreadable: true, found: "s3:GetLifecycleConfiguration was denied"},
		{name: "public-access block unreadable", break_: func(b *fakeBucket) { b.pabErr = apiError("AccessDenied") }, failing: BucketPublicAccessBlock, unreadable: true, found: "s3:GetBucketPublicAccessBlock was denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := correctBucket()
			if tc.break_ != nil {
				tc.break_(b)
			}
			findings, err := CheckBucketContract(context.Background(), b, "the-bucket", "", estateNamespaces)
			if err != nil {
				t.Fatalf("CheckBucketContract: %v", err)
			}
			if len(findings) != len(BucketSettings) {
				t.Fatalf("got %d findings, want one per setting (%d): an operator fixing a bucket needs all of them at once", len(findings), len(BucketSettings))
			}
			for i, f := range findings {
				if f.Setting != BucketSettings[i] {
					t.Errorf("finding %d is %q, want %q", i, f.Setting, BucketSettings[i])
				}
				wantOK := f.Setting != tc.failing
				if f.OK() != wantOK {
					t.Errorf("%s: OK = %v, want %v (found: %s)", f.Setting, f.OK(), wantOK, f.Found)
				}
				if f.OK() && f.Outcome == Unreadable {
					t.Errorf("%s is both OK and Unreadable", f.Setting)
				}
				summary, detail := BucketContractRefusal("the-bucket", f)
				if f.OK() {
					if summary != "" || detail != "" {
						t.Errorf("%s passed and still produced a refusal: %q", f.Setting, summary)
					}
					continue
				}
				if (f.Outcome == Unreadable) != tc.unreadable {
					t.Errorf("%s: Unreadable = %v, want %v", f.Setting, f.Outcome == Unreadable, tc.unreadable)
				}
				if !strings.Contains(f.Found, tc.found) {
					t.Errorf("%s: Found = %q, want it to contain %q", f.Setting, f.Found, tc.found)
				}
				// Refuse BY NAME: the setting in the headline, what the bucket
				// has and the bucket's own name in the paragraph.
				if !strings.Contains(summary, string(f.Setting)) {
					t.Errorf("the refusal's headline %q does not name the setting %q", summary, f.Setting)
				}
				if !strings.Contains(detail, "the-bucket") || !strings.Contains(detail, f.Found) {
					t.Errorf("the refusal's detail names neither the bucket nor what was found: %q", detail)
				}
			}
		})
	}
}

// TestBucketContractDoesNotMistakeAnOutageForAFinding: a read that never
// happened says nothing about the bucket, so it is an error and not three
// findings an operator would go and "fix".
func TestBucketContractDoesNotMistakeAnOutageForAFinding(t *testing.T) {
	b := correctBucket()
	b.lifecycleErr = errors.New("dial tcp: connection refused")
	findings, err := CheckBucketContract(context.Background(), b, "the-bucket", "", estateNamespaces)
	if err == nil {
		t.Fatalf("an unreachable endpoint produced findings instead of an error: %+v", findings)
	}
	if !strings.Contains(err.Error(), string(BucketLifecycle)) {
		t.Errorf("the error does not say which setting was being read: %v", err)
	}
}

// TestBucketContractWithNoNamespacesTrustsOnlyAnUnfilteredRule: told nothing
// about which keys the store writes, a prefix-filtered rule cannot be shown
// to cover them.
func TestBucketContractWithNoNamespacesTrustsOnlyAnUnfilteredRule(t *testing.T) {
	filtered := expiresNoncurrent("tofu-only", 30)
	filtered.Filter = &s3types.LifecycleRuleFilter{Prefix: aws.String("tofu-")}
	b := correctBucket()
	b.rules = []s3types.LifecycleRule{filtered}
	findings, err := CheckBucketContract(context.Background(), b, "the-bucket", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if findings[1].OK() {
		t.Errorf("a prefix-filtered rule passed with no namespaces to check it against: %s", findings[1].Found)
	}
	b.rules = []s3types.LifecycleRule{expiresNoncurrent("whole-bucket", 30)}
	findings, err = CheckBucketContract(context.Background(), b, "the-bucket", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !findings[1].OK() {
		t.Errorf("an unfiltered rule was refused: %s", findings[1].Found)
	}
}
