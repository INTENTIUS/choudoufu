// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"cloud.google.com/go/storage"
)

// The GCS bucket contract: bucketcontract.go's three settings, in GCS's
// terms. GitHub issue #1119. The reasons are the bucket's and are not
// repeated here:
//
//   - Object Versioning, without which an overwrite or a delete is final;
//   - a lifecycle rule that deletes NONCURRENT versions, and no rule that
//     deletes live objects under the store's keys;
//   - public access prevention enforced, because the objects hold secret
//     material.
//
// All three come from one read, storage.buckets.get, so one permission
// covers the whole contract and a denial makes all three unreadable at once.
//
// The names are not the S3 ones. allow_insecure's names are unique across
// stores (contract.go), and a GCS lifecycle rule is not an S3 one: it has no
// Enabled status, and "noncurrent" is a condition on a Delete action rather
// than an action of its own.
//
// Not asserted, deliberately: encryption at rest, for the reason the S3
// contract gives (every GCS object is encrypted, so the check cannot fail),
// and soft delete, which is a second recovery window GCS keeps on its own
// and which this store does not depend on.
const (
	GCSObjectVersioning       Setting = "object_versioning"
	GCSNoncurrentLifecycle    Setting = "noncurrent_lifecycle"
	GCSPublicAccessPrevention Setting = "public_access_prevention"
)

// GCSSettings is every asserted setting, in the order findings are reported.
var GCSSettings = []Setting{GCSObjectVersioning, GCSNoncurrentLifecycle, GCSPublicAccessPrevention}

// gcsBucketReadPermission is the one permission the contract needs.
const gcsBucketReadPermission = "storage.buckets.get"

// CheckContract implements [ContractChecker]. opts.Namespaces are
// store-relative; [GCSConfig.KeyPrefix] is joined ahead of each.
func (s *GCSStore) CheckContract(ctx context.Context, opts ContractOptions) ([]Finding, error) {
	names := make([]string, 0, len(opts.Namespaces))
	for _, ns := range opts.Namespaces {
		names = append(names, s.objectName(ns))
	}
	return CheckGCSBucketContract(ctx, s.client, s.bucket, names)
}

// ContractSubject implements [ContractChecker].
func (s *GCSStore) ContractSubject() (label, value string) { return "Bucket", s.bucket }

// ContractRefusal implements [ContractChecker].
func (s *GCSStore) ContractRefusal(f Finding) (summary, detail string) {
	return GCSBucketContractRefusal(s.bucket, f)
}

// ContractCheckFailed implements [ContractChecker].
func (s *GCSStore) ContractCheckFailed(err error) (summary, detail string) {
	return BucketContractCheckFailed(s.bucket, err)
}

// ContractRefusalClosing implements [ContractChecker]. None is needed, for
// the S3 bucket's reason.
func (s *GCSStore) ContractRefusalClosing([]Setting) string { return "" }

// CheckGCSBucketContract reads bucket's attributes once and reports one
// finding per setting, always all three, in [GCSSettings] order.
//
// namespaces are the object-name prefixes the caller writes under, for the
// lifecycle assertion, exactly as [CheckBucketContract] takes them. With none
// given, only a rule with no prefix condition counts.
//
// The error return is for a failure that says nothing about the settings: a
// cancelled context, an unreachable endpoint, a bucket that does not exist.
// A denied read is three [Unreadable] findings.
func CheckGCSBucketContract(ctx context.Context, client *storage.Client, bucket string, namespaces []string) ([]Finding, error) {
	attrs, err := client.Bucket(bucket).Attrs(ctx)
	if err != nil {
		if IsAccessDenied(err) {
			findings := make([]Finding, 0, len(GCSSettings))
			for _, setting := range GCSSettings {
				findings = append(findings, Finding{Setting: setting, Outcome: Unreadable, Found: gcsBucketReadPermission + " was denied"})
			}
			return findings, nil
		}
		return nil, fmt.Errorf("staterecord: gcs: reading bucket %q's settings: %w", bucket, err)
	}
	return []Finding{
		checkGCSVersioning(attrs),
		checkGCSLifecycle(attrs.Lifecycle.Rules, namespaces),
		checkGCSPublicAccessPrevention(attrs),
	}, nil
}

func checkGCSVersioning(attrs *storage.BucketAttrs) Finding {
	f := Finding{Setting: GCSObjectVersioning}
	if attrs.VersioningEnabled {
		f.Outcome = Passed
		f.Found = "Object Versioning is enabled"
		return f
	}
	f.Found = "Object Versioning is not enabled"
	return f
}

func checkGCSPublicAccessPrevention(attrs *storage.BucketAttrs) Finding {
	f := Finding{Setting: GCSPublicAccessPrevention}
	switch attrs.PublicAccessPrevention {
	case storage.PublicAccessPreventionEnforced:
		f.Outcome = Passed
		f.Found = "public access prevention is enforced"
	case storage.PublicAccessPreventionInherited:
		// Inherited from an organization policy this read cannot see, and
		// "inherited" is also what a bucket with no setting at all answers.
		f.Found = "public access prevention is inherited, not enforced on the bucket"
	default:
		f.Found = fmt.Sprintf("public access prevention is %s, not enforced", attrs.PublicAccessPrevention)
	}
	return f
}

func checkGCSLifecycle(rules []storage.LifecycleRule, namespaces []string) Finding {
	f := Finding{Setting: GCSNoncurrentLifecycle}

	// First the rule that outranks everything else, for #1377's reason: a
	// Delete that can match a LIVE object removes records on a timer, and a
	// converged estate does not rewrite them.
	var deleting []string
	for i, rule := range rules {
		if rule.Action.Type != storage.DeleteAction || !gcsRuleMatchesLive(rule) || !gcsRuleMayReach(rule, namespaces) {
			continue
		}
		deleting = append(deleting, fmt.Sprintf("rule %d deletes live objects%s, and a record is a live object", i+1, gcsDescribeCondition(rule.Condition)))
	}
	if len(deleting) > 0 {
		f.Unwaivable = true
		f.Found = strings.Join(deleting, "; ")
		return f
	}

	var near []string
	var covering []int
	for i, rule := range rules {
		if rule.Action.Type != storage.DeleteAction {
			continue
		}
		if why := gcsNoncurrentExpiryGap(rule); why != "" {
			near = append(near, fmt.Sprintf("rule %d deletes noncurrent versions but %s", i+1, why))
			continue
		}
		covering = append(covering, i)
	}
	targets := namespaces
	if len(targets) == 0 {
		targets = []string{""}
	}
	var used, uncovered []string
	for _, ns := range targets {
		found := false
		for _, i := range covering {
			if gcsRuleCovers(rules[i], ns) {
				if d := fmt.Sprintf("rule %d deletes noncurrent versions%s", i+1, gcsDescribeCondition(rules[i].Condition)); !slices.Contains(used, d) {
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
		f.Outcome = Passed
		f.Found = strings.Join(used, "; ")
		return f
	}
	for _, i := range covering {
		prefixes := strings.Join(rules[i].Condition.MatchesPrefix, ", ")
		if len(namespaces) == 0 {
			near = append(near, fmt.Sprintf("rule %d deletes noncurrent versions but only under the prefixes %s, and this check was not told which objects the store writes", i+1, prefixes))
			continue
		}
		for _, ns := range uncovered {
			near = append(near, fmt.Sprintf("rule %d deletes noncurrent versions but only under the prefixes %s, none of which contains %q", i+1, prefixes, NamespacePrefix(ns)))
		}
	}
	switch {
	case len(near) > 0:
		f.Found = strings.Join(near, "; ")
	case len(rules) == 0:
		f.Found = "the bucket has no lifecycle rules"
	default:
		f.Found = fmt.Sprintf("none of the bucket's %d lifecycle rule(s) deletes noncurrent versions", len(rules))
	}
	return f
}

// gcsNoncurrentOnly reports whether a rule's condition can only match a
// noncurrent version. GCS defines three conditions that way: an object that
// has never been made noncurrent has no noncurrent time and no newer
// versions, so it does not satisfy them.
func gcsNoncurrentOnly(c storage.LifecycleCondition) bool {
	return c.Liveness == storage.Archived || c.DaysSinceNoncurrentTime > 0 || c.NumNewerVersions > 0 || !c.NoncurrentTimeBefore.IsZero()
}

// gcsRuleMatchesLive reports whether a rule may match a live object.
func gcsRuleMatchesLive(rule storage.LifecycleRule) bool {
	return !gcsNoncurrentOnly(rule.Condition)
}

// gcsNoncurrentExpiryGap says why a Delete rule cannot be relied on to
// expire noncurrent versions under its prefixes, or "" when it can.
func gcsNoncurrentExpiryGap(rule storage.LifecycleRule) string {
	c := rule.Condition
	if !gcsNoncurrentOnly(c) {
		return "also matches live objects"
	}
	if len(c.MatchesSuffix) > 0 || len(c.MatchesStorageClasses) > 0 || !c.CustomTimeBefore.IsZero() || c.DaysSinceCustomTime > 0 || !c.CreatedBefore.IsZero() {
		return "is filtered by suffix, storage class, custom time or creation date, which this check does not rely on"
	}
	if c.DaysSinceNoncurrentTime <= 0 && c.NumNewerVersions <= 0 && c.AgeInDays <= 0 && c.NoncurrentTimeBefore.IsZero() {
		// Archived with no other condition deletes a version the moment it
		// stops being live: there is no window to recover a record in.
		return "does so as soon as they become noncurrent, which leaves no recovery window"
	}
	return ""
}

// gcsRuleCovers reports whether rule's prefix condition reaches every object
// under ns. A rule with no prefix condition reaches everything.
func gcsRuleCovers(rule storage.LifecycleRule, ns string) bool {
	if len(rule.Condition.MatchesPrefix) == 0 {
		return true
	}
	if ns == "" {
		return false
	}
	for _, p := range rule.Condition.MatchesPrefix {
		if strings.HasPrefix(NamespacePrefix(ns), p) {
			return true
		}
	}
	return false
}

// gcsRuleMayReach is the question asked of a rule that deletes live objects:
// could it touch any object under namespaces? Only a prefix condition that
// provably lies outside every namespace lets it through.
func gcsRuleMayReach(rule storage.LifecycleRule, namespaces []string) bool {
	if len(rule.Condition.MatchesPrefix) == 0 || len(namespaces) == 0 {
		return true
	}
	for _, p := range rule.Condition.MatchesPrefix {
		for _, ns := range namespaces {
			n := NamespacePrefix(ns)
			if strings.HasPrefix(n, p) || strings.HasPrefix(p, n) {
				return true
			}
		}
	}
	return false
}

// gcsDescribeCondition is the clause a finding quotes for a rule's timing,
// starting with a space, or "" for a rule with no timing condition.
func gcsDescribeCondition(c storage.LifecycleCondition) string {
	var parts []string
	if c.AgeInDays > 0 {
		parts = append(parts, fmt.Sprintf("%d day(s) after creation", c.AgeInDays))
	}
	if c.DaysSinceNoncurrentTime > 0 {
		parts = append(parts, fmt.Sprintf("%d day(s) after becoming noncurrent", c.DaysSinceNoncurrentTime))
	}
	if c.NumNewerVersions > 0 {
		parts = append(parts, fmt.Sprintf("once %d newer version(s) exist", c.NumNewerVersions))
	}
	if !c.CreatedBefore.IsZero() {
		parts = append(parts, "if created before "+c.CreatedBefore.UTC().Format("2006-01-02"))
	}
	if !c.NoncurrentTimeBefore.IsZero() {
		parts = append(parts, "if noncurrent before "+c.NoncurrentTimeBefore.UTC().Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " and ")
}

// GCSBucketContractRefusal is the headline and the paragraph for one failed
// finding, in [BucketContractRefusal]'s shape. Empty for a finding that
// passed.
func GCSBucketContractRefusal(bucket string, f Finding) (summary, detail string) {
	if f.OK() {
		return "", ""
	}
	why, fix := "", ""
	switch f.Setting {
	case GCSObjectVersioning:
		why = "A record in this bucket can be the only copy of what it says: a record-backed resource carries no marker and cannot be imported under a live block, so an overwrite or a delete in a bucket without Object Versioning is final."
		fix = fmt.Sprintf("Enable it: gcloud storage buckets update gs://%s --versioning", bucket)
	case GCSNoncurrentLifecycle:
		if f.Unwaivable {
			summary = "The record store bucket's lifecycle deletes records"
			detail = fmt.Sprintf("Bucket %q: %s.\n\nThat rule deletes records. An estate that has converged does not rewrite its records, so they age, and once one is deleted the next plan reads an estate with that resource missing and proposes creating what already exists. For a record-backed resource the record was the only copy.\n\nGive the rule a noncurrent-only condition (isLive: false, daysSinceNoncurrentTime or numNewerVersions), or limit it with matchesPrefix to objects outside this store's prefixes. The allow_insecure waiver does not cover this.", bucket, f.Found)
			return summary, detail
		}
		why = "Object Versioning is on and every apply writes records, so without a rule that deletes noncurrent versions the bucket keeps every version of every record forever. The days in that rule are also the recovery window: a deleted or overwritten record survives as a noncurrent version for that long. Choose it deliberately."
		fix = "Add a lifecycle rule with action Delete and the condition daysSinceNoncurrentTime set to the number of days you want to be able to recover within, with no matchesPrefix or one that this store's prefixes sit under. A rule filtered by suffix, storage class or custom time does not satisfy this."
	case GCSPublicAccessPrevention:
		why = "Records hold secret material, protected only by encryption at rest and by IAM. A grant to allUsers or allAuthenticatedUsers would publish them."
		fix = fmt.Sprintf("Enforce it: gcloud storage buckets update gs://%s --public-access-prevention", bucket)
	}
	if f.Outcome == Unreadable {
		summary = fmt.Sprintf("The record store bucket's %s setting could not be read", f.Setting)
		detail = fmt.Sprintf("Bucket %q: %s, so whether the bucket satisfies this setting is unknown, and a bucket nobody could check is refused the same as one that failed.\n\n%s\n\nGrant this identity %s on the bucket, or have someone who holds it confirm the setting.", bucket, f.Found, why, gcsBucketReadPermission)
		return summary, detail
	}
	summary = fmt.Sprintf("The record store bucket fails its %s assertion", f.Setting)
	detail = fmt.Sprintf("Bucket %q: %s.\n\n%s\n\n%s", bucket, f.Found, why, fix)
	return summary, detail
}

// GCSWaiverCost says what an estate gives up by waiving setting, as a clause
// completing "is waived, so ...". See [BucketWaiverCost].
func GCSWaiverCost(setting Setting) string {
	switch setting {
	case GCSObjectVersioning:
		return "an overwritten or deleted record cannot be brought back, and a record can be the only copy of what it says"
	case GCSNoncurrentLifecycle:
		return "nothing is known to delete noncurrent versions: the bucket may keep every version of every record forever, and nobody has chosen how long a record destroyed by mistake stays recoverable"
	case GCSPublicAccessPrevention:
		return "nothing is known to stop an IAM grant to allUsers or allAuthenticatedUsers from publishing the records, which hold secret material"
	}
	return "that assertion is not made"
}

var _ ContractChecker = (*GCSStore)(nil)
