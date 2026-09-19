// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func expiresCurrent(id string, days int32, prefix string) s3types.LifecycleRule {
	r := s3types.LifecycleRule{
		ID:         aws.String(id),
		Status:     s3types.ExpirationStatusEnabled,
		Expiration: &s3types.LifecycleExpiration{Days: aws.Int32(days)},
	}
	if prefix != "" {
		r.Filter = &s3types.LifecycleRuleFilter{Prefix: aws.String(prefix)}
	}
	return r
}

func lifecycleFinding(t *testing.T, rules []s3types.LifecycleRule) BucketFinding {
	t.Helper()
	b := correctBucket()
	b.rules = rules
	findings, err := CheckBucketContract(context.Background(), b, "the-bucket", estateNamespaces)
	if err != nil {
		t.Fatalf("CheckBucketContract: %v", err)
	}
	for _, f := range findings {
		if f.Setting == BucketLifecycle {
			return f
		}
	}
	t.Fatal("no lifecycle finding")
	return BucketFinding{}
}

// TestLifecycleThatExpiresCurrentObjectsIsRefused is GitHub issue #1377. The
// lifecycle assertion exists so that a record destroyed by mistake can come
// back. A rule that expires CURRENT objects destroys records on a timer: a
// converged estate does not rewrite its records, so N days after the last
// write they are gone and the next plan reads an empty estate. Before this
// test the check looked only for a noncurrent expiry and reported such a
// bucket correct.
func TestLifecycleThatExpiresCurrentObjectsIsRefused(t *testing.T) {
	good := expiresNoncurrent("expire-noncurrent", 30)

	both := expiresNoncurrent("does-both", 30)
	both.Expiration = &s3types.LifecycleExpiration{Days: aws.Int32(1)}

	byDate := expiresCurrent("by-date", 0, "")
	byDate.Expiration = &s3types.LifecycleExpiration{Date: aws.Time(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))}

	switchedOff := expiresCurrent("switched-off", 1, "")
	switchedOff.Status = s3types.ExpirationStatusDisabled

	markersOnly := s3types.LifecycleRule{
		ID:         aws.String("tidy-delete-markers"),
		Status:     s3types.ExpirationStatusEnabled,
		Expiration: &s3types.LifecycleExpiration{ExpiredObjectDeleteMarker: aws.Bool(true)},
	}

	byTag := expiresCurrent("by-tag", 1, "")
	byTag.Filter = &s3types.LifecycleRuleFilter{Tag: &s3types.Tag{Key: aws.String("tofu-estate"), Value: aws.String("prod")}}

	for _, tc := range []struct {
		name  string
		rules []s3types.LifecycleRule
		ok    bool
		found string
	}{
		{"the same rule also expires current objects", []s3types.LifecycleRule{both}, false, `rule "does-both" expires current objects after 1 day(s)`},
		{"a second, unfiltered rule expires current objects", []s3types.LifecycleRule{good, expiresCurrent("sweep", 90, "")}, false, `rule "sweep" expires current objects after 90 day(s)`},
		{"a rule expiring current objects under every tofu- root", []s3types.LifecycleRule{good, expiresCurrent("tofu-sweep", 7, "tofu-")}, false, `rule "tofu-sweep" expires current objects`},
		{"a rule expiring current objects INSIDE a namespace", []s3types.LifecycleRule{good, expiresCurrent("inside", 7, "tofu-records/prod/terraform_data/")}, false, `rule "inside" expires current objects`},
		{"expiry on a date", []s3types.LifecycleRule{good, byDate}, false, `rule "by-date" expires current objects on 2030-01-01`},
		// A tag or size filter may or may not match a record, and records DO
		// carry tags, so the check cannot show this rule misses them.
		{"a tag-filtered rule expiring current objects", []s3types.LifecycleRule{good, byTag}, false, `rule "by-tag" expires current objects`},

		{"a rule expiring current objects somewhere else", []s3types.LifecycleRule{good, expiresCurrent("logs", 7, "logs/")}, true, `rule "expire-noncurrent"`},
		{"a rule for another estate's namespace", []s3types.LifecycleRule{good, expiresCurrent("other", 7, "tofu-records/prod-eu/")}, true, `rule "expire-noncurrent"`},
		{"a disabled rule that would expire current objects", []s3types.LifecycleRule{good, switchedOff}, true, `rule "expire-noncurrent"`},
		{"a rule that only tidies expired delete markers", []s3types.LifecycleRule{good, markersOnly}, true, `rule "expire-noncurrent"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := lifecycleFinding(t, tc.rules)
			if f.OK != tc.ok {
				t.Errorf("OK = %v, want %v (found: %s)", f.OK, tc.ok, f.Found)
			}
			if !strings.Contains(f.Found, tc.found) {
				t.Errorf("Found = %q, want it to contain %q", f.Found, tc.found)
			}
			if !tc.ok {
				_, detail := BucketContractRefusal("the-bucket", f)
				for _, want := range []string{"deletes records", "Remove the Expiration", "NoncurrentVersionExpiration"} {
					if !strings.Contains(detail, want) {
						t.Errorf("the refusal does not say %q: %q", want, detail)
					}
				}
			}
		})
	}
}

// TestLifecycleCoveredByOneRulePerNamespace: a bucket may cover the three
// namespaces with a rule each. Each rule used to have to cover all three by
// itself, so a correct configuration was refused and the only way past was a
// waiver that removes the check. GitHub issue #1377.
func TestLifecycleCoveredByOneRulePerNamespace(t *testing.T) {
	perNamespace := func(id, prefix string) s3types.LifecycleRule {
		r := expiresNoncurrent(id, 30)
		r.Filter = &s3types.LifecycleRuleFilter{Prefix: aws.String(prefix)}
		return r
	}
	all := []s3types.LifecycleRule{
		perNamespace("records", "tofu-records/"),
		perNamespace("hints", "tofu-hints/"),
		perNamespace("outputs", "tofu-outputs/"),
	}
	if f := lifecycleFinding(t, all); !f.OK {
		t.Errorf("three rules, one per namespace, were refused: %s", f.Found)
	}
	f := lifecycleFinding(t, all[:2])
	if f.OK {
		t.Fatalf("two of three namespaces covered was accepted: %s", f.Found)
	}
	if !strings.Contains(f.Found, "tofu-outputs/prod/") {
		t.Errorf("the finding does not name the namespace left uncovered: %s", f.Found)
	}
}

// TestLifecycleFindingSaysWhenVersionsAreAlsoKeptByCount: "after 30 day(s)"
// understates retention when the rule also keeps the newest N noncurrent
// versions whatever their age.
func TestLifecycleFindingSaysWhenVersionsAreAlsoKeptByCount(t *testing.T) {
	r := expiresNoncurrent("keep-five", 30)
	r.NoncurrentVersionExpiration.NewerNoncurrentVersions = aws.Int32(5)
	f := lifecycleFinding(t, []s3types.LifecycleRule{r})
	if !f.OK {
		t.Fatalf("refused: %s", f.Found)
	}
	if !strings.Contains(f.Found, "5") || !strings.Contains(f.Found, "newest") {
		t.Errorf("Found does not mention the versions kept by count: %s", f.Found)
	}
}

// TestAWaiverDoesNotCoverALifecycleThatDeletesRecords: allow_insecure lets a
// run proceed without a setting being ASSERTED, and its stated cost is that
// nothing is known to expire noncurrent versions. A rule read from the bucket
// and found to delete records is not that, so naming "lifecycle" does not
// waive it. An ordinary lifecycle failure is still waived by the same name.
func TestAWaiverDoesNotCoverALifecycleThatDeletesRecords(t *testing.T) {
	deleting := lifecycleFinding(t, []s3types.LifecycleRule{expiresNoncurrent("ok", 30), expiresCurrent("sweep", 90, "")})
	if !deleting.DeletesRecords {
		t.Fatalf("the finding is not marked as deleting records: %+v", deleting)
	}
	refused, waived := SplitWaived([]BucketFinding{deleting}, []string{"lifecycle"})
	if len(refused) != 1 || len(waived) != 0 {
		t.Errorf("a lifecycle that deletes records was waived: refused=%d waived=%d", len(refused), len(waived))
	}

	absent := lifecycleFinding(t, nil)
	if absent.DeletesRecords {
		t.Fatalf("a bucket with no rules is marked as deleting records")
	}
	refused, waived = SplitWaived([]BucketFinding{absent}, []string{"lifecycle"})
	if len(refused) != 0 || len(waived) != 1 {
		t.Errorf("an ordinary lifecycle failure was not waived by name: refused=%d waived=%d", len(refused), len(waived))
	}
}
