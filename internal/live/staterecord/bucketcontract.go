// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// The bucket contract. GitHub issue #1339, part of the bucket backend (#1332).
//
// A record in this bucket can be the only copy of what it says. The
// markerless slice has no hand-recovery verb - import is refused under a
// live block, and adoption means stamping a marker an untaggable type cannot
// carry - so the bucket's own settings are the recovery path, and three of
// them are asserted rather than recommended:
//
//   - versioning, without which an overwrite or a delete is final;
//   - a lifecycle rule that EXPIRES NONCURRENT VERSIONS, without which a
//     versioned bucket that is written on every apply grows forever, and
//     whose window is the recovery window: how long a record destroyed by
//     mistake can still be brought back;
//   - public-access block, because the objects hold secret material.
//
// # Encryption at rest is NOT asserted, deliberately
//
// Do not add it. S3 has encrypted every new object by default since January
// 2023, so "is encryption at rest on" is true of every bucket that exists:
// a check that cannot fail, which this repository does not count as a check.
// Asserting a particular FLAVOUR instead would refuse buckets that are
// correct for their owner's reasons. What replaces the assertion is a
// compatibility requirement - every SSE flavour must work, because the ETag
// is opaque here - and that is measured by #1344, not asserted by this file.
//
// # This file reports the bucket, not the run
//
// [CheckBucketContract] knows nothing about waivers. It answers "is this
// bucket correct" and nothing else, because that is the question the
// runnable project's `verify` asks (#1341), and a verify that honoured a
// waiver would print green for a bucket with versioning off. Whether a run
// may PROCEED past a finding is the caller's decision (#1340).

// BucketSetting names one asserted setting. The values are the names an
// operator writes in configuration (#1340's allow_insecure), so they are
// part of the configuration language and do not change casually.
type BucketSetting string

const (
	BucketVersioning        BucketSetting = "versioning"
	BucketLifecycle         BucketSetting = "lifecycle"
	BucketPublicAccessBlock BucketSetting = "public_access_block"
)

// BucketSettings is every asserted setting, in the order findings are
// reported.
var BucketSettings = []BucketSetting{BucketVersioning, BucketLifecycle, BucketPublicAccessBlock}

// BucketFinding is what one setting turned out to be.
type BucketFinding struct {
	Setting BucketSetting

	// OK is true when the bucket satisfies the assertion.
	OK bool

	// Unreadable is true when the setting could not be read at all - the
	// role lacks the Get* permission, typically. It is never true together
	// with OK. From the caller's side it is the same refusal a wrong setting
	// gets, because a bucket nobody could check is not a bucket that passed.
	Unreadable bool

	// DeletesRecords is true for the one lifecycle failure that is not an
	// absence: an enabled rule that expires CURRENT objects under the store's
	// keys. It is a different refusal with a different remedy, and it is the
	// one finding allow_insecure does not reach (see [SplitWaived]). GitHub
	// issue #1377.
	DeletesRecords bool

	// Found says what the bucket actually has, in one clause, for the
	// refusal to quote: "versioning is Suspended", "no lifecycle
	// configuration", "s3:GetBucketVersioning was denied".
	Found string
}

// BucketContractAPI is the three reads the contract needs, and the
// permissions they cost: s3:GetBucketVersioning,
// s3:GetLifecycleConfiguration and s3:GetBucketPublicAccessBlock. *s3.Client
// satisfies it.
type BucketContractAPI interface {
	GetBucketVersioning(ctx context.Context, in *s3.GetBucketVersioningInput, optFns ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
	GetBucketLifecycleConfiguration(ctx context.Context, in *s3.GetBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error)
	GetPublicAccessBlock(ctx context.Context, in *s3.GetPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.GetPublicAccessBlockOutput, error)
}

// BucketContractChecker is implemented by a store that lives in a bucket.
// The local store does not implement it: a directory has no such settings,
// and a store with nothing to assert is not a store that failed.
type BucketContractChecker interface {
	CheckBucketContract(ctx context.Context, namespaces []string) ([]BucketFinding, error)
}

// AsBucketContractChecker finds the bucket-backed store under s, looking
// through this package's own wrappers ([RunCache], [CountingStore]). False
// means there is nothing to assert - a local store - which is
// a different answer from a bucket that failed.
func AsBucketContractChecker(s Store) (BucketContractChecker, bool) {
	for s != nil {
		if c, ok := s.(BucketContractChecker); ok {
			return c, true
		}
		u, ok := s.(interface{ Unwrap() Store })
		if !ok {
			return nil, false
		}
		s = u.Unwrap()
	}
	return nil, false
}

// CheckBucketContract implements [BucketContractChecker]. namespaces are
// store-relative, like every key this store is handed; [S3Config.KeyPrefix]
// is joined ahead of each.
func (s *S3Store) CheckBucketContract(ctx context.Context, namespaces []string) ([]BucketFinding, error) {
	objectNamespaces := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		objectNamespaces = append(objectNamespaces, s.objectKey(ns))
	}
	return CheckBucketContract(ctx, s.client, s.bucket, s.expectedBucketOwner, objectNamespaces)
}

// CheckBucketContract reads the three settings of bucket and reports one
// finding per setting, always all three and always in [BucketSettings]
// order: a caller that refuses on the first bad one would make an operator
// fix them one run at a time.
//
// namespaces are the object-key prefixes the caller writes under - an
// estate's records, hint and outputs. The lifecycle assertion needs them: a
// rule scoped to some other prefix expires nothing of ours, and a rule that
// covers the records but not the outputs leaves the outputs growing. With
// none given, only a rule with no prefix filter counts.
//
// expectedOwner is the account that must own the bucket, as twelve digits, or
// "" for no check. Set, each of the three reads carries it as
// ExpectedBucketOwner, so a bucket of this name in some other account is
// refused rather than reported on. See bucketowner.go.
//
// The error return is for a failure that is not about the bucket's settings
// at all - a cancelled context, an unreachable endpoint. A denied read is NOT
// an error: it is a finding with Unreadable set.
func CheckBucketContract(ctx context.Context, api BucketContractAPI, bucket, expectedOwner string, namespaces []string) ([]BucketFinding, error) {
	findings := make([]BucketFinding, 0, len(BucketSettings))

	v, err := checkVersioning(ctx, api, bucket, expectedOwner)
	if err != nil {
		return nil, err
	}
	findings = append(findings, v)

	l, err := checkLifecycle(ctx, api, bucket, expectedOwner, namespaces)
	if err != nil {
		return nil, err
	}
	findings = append(findings, l)

	p, err := checkPublicAccessBlock(ctx, api, bucket, expectedOwner)
	if err != nil {
		return nil, err
	}
	findings = append(findings, p)

	return findings, nil
}

func checkVersioning(ctx context.Context, api BucketContractAPI, bucket, expectedOwner string) (BucketFinding, error) {
	f := BucketFinding{Setting: BucketVersioning}
	out, err := api.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket:              aws.String(bucket),
		ExpectedBucketOwner: expectedOwnerPtr(expectedOwner),
	})
	if err != nil {
		return settingReadFailure(f, "s3:GetBucketVersioning", expectedOwner, err)
	}
	switch out.Status {
	case s3types.BucketVersioningStatusEnabled:
		f.OK = true
		f.Found = "versioning is Enabled"
	case s3types.BucketVersioningStatusSuspended:
		f.Found = "versioning is Suspended"
	default:
		// A bucket that has never had versioning configured answers with no
		// Status at all, not with "Disabled".
		f.Found = "versioning has never been enabled"
	}
	return f, nil
}

func checkLifecycle(ctx context.Context, api BucketContractAPI, bucket, expectedOwner string, namespaces []string) (BucketFinding, error) {
	f := BucketFinding{Setting: BucketLifecycle}
	out, err := api.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{
		Bucket:              aws.String(bucket),
		ExpectedBucketOwner: expectedOwnerPtr(expectedOwner),
	})
	if err != nil {
		if apiErrorCode(err) == "NoSuchLifecycleConfiguration" {
			f.Found = "the bucket has no lifecycle configuration"
			return f, nil
		}
		return settingReadFailure(f, "s3:GetLifecycleConfiguration", expectedOwner, err)
	}

	// First, the thing that outranks everything else this assertion checks:
	// a rule that expires CURRENT objects deletes records on a timer. A
	// converged estate does not rewrite its records, so N days after the
	// last write they are gone, and the next plan reads an empty estate.
	// The noncurrent expiry below exists so a record can come BACK; it says
	// nothing about a bucket that throws live ones away, and until GitHub
	// issue #1377 this function reported such a bucket correct.
	var deleting []string
	for _, rule := range out.Rules {
		if rule.Status != s3types.ExpirationStatusEnabled {
			continue
		}
		when := expiresCurrentObjects(rule)
		if when == "" || !ruleMayReach(rule, namespaces) {
			continue
		}
		deleting = append(deleting, fmt.Sprintf("rule %q expires current objects %s, and a record is a current object", ruleID(rule), when))
	}
	if len(deleting) > 0 {
		f.DeletesRecords = true
		f.Found = strings.Join(deleting, "; ")
		return f, nil
	}

	// The assertion names what the lifecycle DOES, not that one exists. A
	// configuration whose rules only transition storage classes, or only
	// abort multipart uploads, or expire noncurrent versions of some other
	// prefix, satisfies "has a lifecycle policy" and fixes nothing.
	//
	// Coverage is per namespace: one rule may cover all three, or three
	// rules may cover one each. Each rule having to cover everything by
	// itself refused a correct configuration (#1377).
	var near []string
	var covering []s3types.LifecycleRule
	for _, rule := range out.Rules {
		if rule.NoncurrentVersionExpiration == nil || aws.ToInt32(rule.NoncurrentVersionExpiration.NoncurrentDays) <= 0 {
			continue
		}
		if rule.Status != s3types.ExpirationStatusEnabled {
			near = append(near, fmt.Sprintf("rule %q expires noncurrent versions but is %s", ruleID(rule), rule.Status))
			continue
		}
		if why := ruleFilteredBeyondPrefix(rule); why != "" {
			near = append(near, fmt.Sprintf("rule %q expires noncurrent versions but %s", ruleID(rule), why))
			continue
		}
		covering = append(covering, rule)
	}
	var used []string
	var uncovered []string
	targets := namespaces
	if len(targets) == 0 {
		// Not told which keys the store writes: only a rule with no prefix
		// can be shown to reach them.
		targets = []string{""}
	}
	for _, ns := range targets {
		found := false
		for _, rule := range covering {
			prefix := rulePrefix(rule)
			if prefix == "" || (ns != "" && strings.HasPrefix(NamespacePrefix(ns), prefix)) {
				if d := describeNoncurrentExpiry(rule); !slices.Contains(used, d) {
					used = append(used, d)
				}
				found = true
				break
			}
		}
		if !found {
			uncovered = append(uncovered, ns)
		}
	}
	if len(uncovered) == 0 && len(covering) > 0 {
		f.OK = true
		f.Found = strings.Join(used, "; ")
		return f, nil
	}
	for _, rule := range covering {
		if len(namespaces) == 0 {
			near = append(near, fmt.Sprintf("rule %q expires noncurrent versions but only under the prefix %q, and this check was not told which keys the store writes", ruleID(rule), rulePrefix(rule)))
			continue
		}
		for _, ns := range uncovered {
			near = append(near, fmt.Sprintf("rule %q expires noncurrent versions but only under the prefix %q, which does not contain %q", ruleID(rule), rulePrefix(rule), NamespacePrefix(ns)))
		}
	}
	switch {
	case len(near) > 0:
		f.Found = strings.Join(near, "; ")
	case len(out.Rules) == 0:
		f.Found = "the lifecycle configuration has no rules"
	default:
		f.Found = fmt.Sprintf("none of the lifecycle configuration's %d rule(s) expires noncurrent versions", len(out.Rules))
	}
	return f, nil
}

func ruleID(rule s3types.LifecycleRule) string {
	if id := aws.ToString(rule.ID); id != "" {
		return id
	}
	return "(unnamed rule)"
}

// describeNoncurrentExpiry is the sentence a passing finding reports. It
// says so when the rule ALSO keeps the newest N noncurrent versions whatever
// their age, because "after 30 day(s)" alone would understate what is kept.
func describeNoncurrentExpiry(rule s3types.LifecycleRule) string {
	nve := rule.NoncurrentVersionExpiration
	d := fmt.Sprintf("rule %q expires noncurrent versions after %d day(s)", ruleID(rule), aws.ToInt32(nve.NoncurrentDays))
	if n := aws.ToInt32(nve.NewerNoncurrentVersions); n > 0 {
		d += fmt.Sprintf(", keeping the newest %d whatever their age", n)
	}
	return d
}

// expiresCurrentObjects says when rule deletes current objects ("after N
// day(s)", "on <date>"), or "" when it does not. ExpiredObjectDeleteMarker
// alone is not an expiry of anything current: it tidies a delete marker that
// has no versions left under it.
func expiresCurrentObjects(rule s3types.LifecycleRule) string {
	e := rule.Expiration
	if e == nil {
		return ""
	}
	if d := aws.ToInt32(e.Days); d > 0 {
		return fmt.Sprintf("after %d day(s)", d)
	}
	if e.Date != nil && !e.Date.IsZero() {
		return "on " + e.Date.UTC().Format("2006-01-02")
	}
	return ""
}

// rulePrefix is the key prefix rule is limited to, "" for none.
func rulePrefix(rule s3types.LifecycleRule) string {
	prefix := aws.ToString(rule.Prefix) //nolint:staticcheck // the deprecated top-level Prefix is still what older configurations carry
	if fl := rule.Filter; fl != nil {
		if fl.And != nil {
			if p := aws.ToString(fl.And.Prefix); p != "" {
				prefix = p
			}
		}
		if p := aws.ToString(fl.Prefix); p != "" {
			prefix = p
		}
	}
	return prefix
}

// ruleFilteredBeyondPrefix says why rule cannot be RELIED ON to reach every
// object under a prefix, or "" when its only filter is a prefix (or none).
//
// Conservative on purpose. A rule filtered by tag or by object size may well
// cover every record today and stop covering them tomorrow with nothing here
// to notice.
func ruleFilteredBeyondPrefix(rule s3types.LifecycleRule) string {
	const why = "is filtered by tag or object size, which this check does not rely on"
	fl := rule.Filter
	if fl == nil {
		return ""
	}
	if fl.Tag != nil || fl.ObjectSizeGreaterThan != nil || fl.ObjectSizeLessThan != nil {
		return why
	}
	if fl.And != nil && (len(fl.And.Tags) > 0 || fl.And.ObjectSizeGreaterThan != nil || fl.And.ObjectSizeLessThan != nil) {
		return why
	}
	return ""
}

// ruleMayReach is the opposite question from coverage, asked of a rule that
// DELETES: could it touch a key under any of namespaces? Conservative the
// other way. A tag or size filter is no reassurance, since records carry
// tags and have sizes, so only a prefix that provably lies outside every
// namespace lets a deleting rule through. With no namespaces given, any
// enabled deleting rule may reach.
func ruleMayReach(rule s3types.LifecycleRule, namespaces []string) bool {
	prefix := rulePrefix(rule)
	if prefix == "" || len(namespaces) == 0 {
		return true
	}
	for _, ns := range namespaces {
		n := NamespacePrefix(ns)
		if strings.HasPrefix(n, prefix) || strings.HasPrefix(prefix, n) {
			return true
		}
	}
	return false
}

func checkPublicAccessBlock(ctx context.Context, api BucketContractAPI, bucket, expectedOwner string) (BucketFinding, error) {
	f := BucketFinding{Setting: BucketPublicAccessBlock}
	out, err := api.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{
		Bucket:              aws.String(bucket),
		ExpectedBucketOwner: expectedOwnerPtr(expectedOwner),
	})
	if err != nil {
		if apiErrorCode(err) == "NoSuchPublicAccessBlockConfiguration" {
			f.Found = "the bucket has no public-access block configuration"
			return f, nil
		}
		return settingReadFailure(f, "s3:GetBucketPublicAccessBlock", expectedOwner, err)
	}
	cfg := out.PublicAccessBlockConfiguration
	if cfg == nil {
		f.Found = "the bucket has no public-access block configuration"
		return f, nil
	}
	var off []string
	for _, flag := range []struct {
		name string
		on   *bool
	}{
		{"BlockPublicAcls", cfg.BlockPublicAcls},
		{"IgnorePublicAcls", cfg.IgnorePublicAcls},
		{"BlockPublicPolicy", cfg.BlockPublicPolicy},
		{"RestrictPublicBuckets", cfg.RestrictPublicBuckets},
	} {
		if !aws.ToBool(flag.on) {
			off = append(off, flag.name)
		}
	}
	if len(off) > 0 {
		f.Found = "public-access block has " + strings.Join(off, ", ") + " off"
		return f, nil
	}
	f.OK = true
	f.Found = "all four public-access block settings are on"
	return f, nil
}

// settingReadFailure sorts a failed read into a finding (the read was
// denied, so the setting is unreadable) or an error (the read never
// happened, so nothing is known about the bucket at all).
func settingReadFailure(f BucketFinding, permission, expectedOwner string, err error) (BucketFinding, error) {
	if accessDenied(err) {
		f.Unreadable = true
		f.Found = permission + " was denied"
		if expectedOwner != "" {
			// This read carried ExpectedBucketOwner, so a bucket owned by
			// another account is refused with the very same 403 the missing
			// permission gets, and an operator granted the permission twice
			// before looking anywhere else. GitHub issue #1381.
			f.Found += fmt.Sprintf(", which is also what S3 answers when the bucket is owned by an account other than %s", expectedOwner)
		}
		return f, nil
	}
	return f, fmt.Errorf("staterecord: s3: reading the bucket's %s setting: %w", f.Setting, err)
}

func apiErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// BucketContractRefusal is the headline and the paragraph for one failed
// finding, in internal/command's statelessCommandRefusals shape: what was
// refused, then what it protects against and what to do instead. Empty for a
// finding that passed.
func BucketContractRefusal(bucket string, f BucketFinding) (summary, detail string) {
	if f.OK {
		return "", ""
	}
	why, fix := "", ""
	switch f.Setting {
	case BucketVersioning:
		why = "A record in this bucket can be the only copy of what it says: a record-backed resource carries no marker and cannot be imported under a live block, so an overwrite or a delete in an unversioned bucket is final."
		fix = fmt.Sprintf("Enable it: aws s3api put-bucket-versioning --bucket %s --versioning-configuration Status=Enabled", bucket)
	case BucketLifecycle:
		if f.DeletesRecords {
			summary = "The record store bucket's lifecycle deletes records"
			detail = fmt.Sprintf("Bucket %q: %s.\n\nThat rule deletes records. An estate that has converged does not rewrite its records, so they age, and once one is expired the next plan reads an estate with that resource missing and proposes creating what already exists. For a record-backed resource the record was the only copy. Versioning keeps an expired record as a noncurrent version for a while, which is a recovery window and not a reason to let it happen.\n\nRemove the Expiration action from that rule, or limit the rule to a prefix outside this store's keys. Only NoncurrentVersionExpiration belongs on a rule that reaches them. The allow_insecure waiver does not cover this: it lets a run proceed without a setting being asserted, and this is a setting that was read and is destructive.", bucket, f.Found)
			return summary, detail
		}
		why = "Versioning is on and every apply writes records, so without a rule that expires noncurrent versions the bucket keeps every version of every record forever. The number of days in that rule is also the recovery window: a deleted or overwritten record survives as a noncurrent version for exactly that long, and that is the only way a record destroyed by mistake comes back. Choose it deliberately."
		fix = "Add an enabled lifecycle rule with no filter (or a prefix this store's keys sit under) and a NoncurrentVersionExpiration of the number of days you want to be able to recover within. A rule that only transitions storage classes, only aborts multipart uploads, or is filtered by tag does not satisfy this."
	case BucketPublicAccessBlock:
		why = "Records hold secret material, protected only by the bucket's encryption at rest and by IAM. A public bucket policy or ACL would publish them."
		fix = fmt.Sprintf("Turn all four settings on: aws s3api put-public-access-block --bucket %s --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true", bucket)
	}
	if f.Unreadable {
		summary = fmt.Sprintf("The record store bucket's %s setting could not be read", f.Setting)
		detail = fmt.Sprintf("Bucket %q: %s, so whether the bucket satisfies this setting is unknown, and a bucket nobody could check is refused the same as one that failed.\n\n%s\n\nGrant the role that read permission, or have someone who holds it confirm the setting.", bucket, f.Found, why)
		return summary, detail
	}
	summary = fmt.Sprintf("The record store bucket fails its %s assertion", f.Setting)
	detail = fmt.Sprintf("Bucket %q: %s.\n\n%s\n\n%s", bucket, f.Found, why, fix)
	return summary, detail
}

// BucketWaiverCost says what an estate gives up by waiving setting, as a
// clause that completes "is waived, so ...". GitHub issue #1340: the warning
// names the setting and its cost in the same sentence, never a generic
// "running with reduced checks", because a cost the reader has to look up is
// a cost they have already decided not to read.
func BucketWaiverCost(setting BucketSetting) string {
	switch setting {
	case BucketVersioning:
		return "an overwritten or deleted record cannot be brought back, and a record can be the only copy of what it says"
	case BucketLifecycle:
		return "nothing is known to expire noncurrent versions: the bucket may keep every version of every record forever, and nobody has chosen how long a record destroyed by mistake stays recoverable"
	case BucketPublicAccessBlock:
		return "nothing is known to stop a bucket policy or an ACL from publishing the records, which hold secret material"
	}
	return "that assertion is not made"
}

// SplitWaived sorts the findings that did not pass into the ones the run
// must refuse on and the ones waived names, leaving passing findings out of
// both. A waiver reaches exactly the settings it names: waiving one leaves a
// failure of either of the others in refused.
//
// An unreadable setting is waived by the same name as a wrong one. From the
// caller's side they are one refusal - the run cannot rely on the setting -
// and an operator whose role cannot read the bucket's configuration has no
// other way to proceed.
func SplitWaived(findings []BucketFinding, waived []string) (refused, waivedFailing []BucketFinding) {
	for _, f := range findings {
		if f.OK {
			continue
		}
		isWaived := false
		for _, name := range waived {
			// A rule that deletes records is never waived. The waiver's
			// stated cost (see [BucketWaiverCost]) is that nothing is KNOWN
			// to expire noncurrent versions; here something is known, and it
			// is destructive.
			if f.DeletesRecords {
				break
			}
			if BucketSetting(name) == f.Setting {
				isWaived = true
				break
			}
		}
		if isWaived {
			waivedFailing = append(waivedFailing, f)
		} else {
			refused = append(refused, f)
		}
	}
	return refused, waivedFailing
}
