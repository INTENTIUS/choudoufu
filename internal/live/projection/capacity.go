// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// SummaryRecordStoreTooSmall is the diagnostic summary for "this estate has
// more records than the configured record store can physically hold".
// GitHub issue #1146.
//
// Declared here rather than at the raise site for the same reason
// [SummaryRecordStoreWriteFailed] is: every record-store diagnostic summary
// is declared in this one package, so the set is enumerable.
const SummaryRecordStoreTooSmall = "Record store too small for this estate"

// SummaryRecordStoreNearlyFull is the warning half. See
// [CheckRecordCapacity] for why the band it covers is a warning and the
// band above it is a refusal.
const SummaryRecordStoreNearlyFull = "Record store is close to its ceiling"

// recordStoreOverheadKeys is how many keys a store holds that are not one
// estate resource's record: today just the provisioning sentinel
// ([provisionStoreSentinel]).
//
// A run also writes a guided-discovery hint and one key per root output,
// and those are deliberately NOT counted. Under-counting the overhead makes
// [CheckRecordCapacity] refuse one or two records LATE, which costs a run
// that was going to fail anyway; over-counting would make it refuse an
// estate that fits, which is a working configuration turned away. Only the
// first of those two errors is acceptable, so the number stays the one key
// every store provably has.
const recordStoreOverheadKeys = 1

// recordStoreWarnFraction is where [CheckRecordCapacity] starts warning:
// nine tenths of the backend's ceiling. See that function for the argument.
const recordStoreWarnFraction = 0.9

// RecordStoreCapacity is how many keys the backend rs names can hold, and
// true; or zero and false for a backend with no fixed ceiling.
//
// "local" and "s3" return false. A local store is bounded by the
// filesystem and an S3 bucket has no object-count limit at all, so neither
// has a number to state - which is itself the answer to "where does an
// estate this size go", and the refusal below says so.
func RecordStoreCapacity(rs *configs.LiveRecordStore) (int, bool) {
	if rs == nil || rs.Type != "ssm" {
		return 0, false
	}
	return staterecord.SSMTierCapacity(staterecord.SSMTier(rs.Tier)), true
}

// CheckRecordCapacity is GitHub issue #1146's plan-time ceiling check:
// given the number of records this estate will need, decide whether the
// configured record store can physically hold them, BEFORE the run writes
// the first one.
//
// # Why this exists at all
//
// SSM Parameter Store caps standard parameters at 10,000 per account per
// region (Service Quotas L-C3B871CB, Adjustable: False), and one record is
// one parameter. An estate of 10,069 resources therefore needs 10,070
// parameters and cannot use the standard tier, ever - not as a quota
// request, not as a retry, not as a throttling problem to wait out. Before
// this check that arithmetic arrived as a failure partway through an apply
// that had already written ten thousand records and created live resources
// nothing owned a record for. The count is known at plan time, so the
// refusal belongs at plan time.
//
// # Why one band refuses and the next one only warns
//
// planned exceeding the ceiling is ARITHMETIC. It cannot succeed in an
// empty account, at any hour, with any retry budget, so refusing it turns
// away nothing that would have worked. That band is an error.
//
// The band below it is not arithmetic, because the ceiling is an ACCOUNT
// and REGION total rather than this estate's budget: whether 9,900 records
// fit depends on how many parameters the account already holds, which this
// check deliberately does not read. Finding out means a paginated
// DescribeParameters sweep of up to ten thousand items on every plan, to
// answer a question that is stale the moment it is answered - and a
// refusal built on it would refuse estates that fit. So the band from
// [recordStoreWarnFraction] of the ceiling up to the ceiling is a warning:
// it is the only honest verdict available there, and it says the two
// numbers rather than gesturing at a limit.
//
// That split is the whole decision. Refuse where the outcome is decided by
// arithmetic this function can do; warn where it is decided by a number
// this function refuses to spend a plan's budget guessing at; stay silent
// nowhere.
//
// planned is the number of records the estate needs, ordinarily one per
// managed resource instance (since GitHub issue #364 every instance's
// identity is recorded, not only the record-backed types). A caller that
// can only supply a lower bound should supply it: an undercount refuses
// late, and late is the same failure this already improves on, whereas an
// overcount refuses an estate that fits.
func CheckRecordCapacity(rs *configs.LiveRecordStore, planned int) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	capacity, bounded := RecordStoreCapacity(rs)
	if !bounded || planned <= 0 {
		return diags
	}
	needed := planned + recordStoreOverheadKeys

	switch {
	case needed > capacity:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryRecordStoreTooSmall,
			recordStoreCapacityDetail(rs, planned, needed, capacity),
		))
	case float64(needed) >= float64(capacity)*recordStoreWarnFraction:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryRecordStoreNearlyFull,
			recordStoreNearlyFullDetail(rs, planned, needed, capacity),
		))
	}
	return diags
}

// recordStoreTierPhrase names the tier a message is talking about, and says
// so honestly for the unset case, where the account's own default-tier
// configuration decides and this fork has assumed the AWS default.
func recordStoreTierPhrase(rs *configs.LiveRecordStore) string {
	if rs != nil && rs.TierSet {
		return fmt.Sprintf("the %q tier", rs.Tier)
	}
	return "the standard tier (this record_store block sets no \"tier\", so the account's own default-tier configuration applies; standard is the AWS default)"
}

// recordStoreRemedies is the list of ways past the ceiling, generated from
// the tier table rather than written out, so a tier added to
// [staterecord.SSMTierNames] appears here without an edit.
func recordStoreRemedies(rs *configs.LiveRecordStore, needed int) string {
	var b strings.Builder
	for _, name := range staterecord.SSMTierNames() {
		tier := staterecord.SSMTier(name)
		if rs != nil && rs.Tier == name {
			continue
		}
		limit := staterecord.SSMTierCapacity(tier)
		if limit < needed {
			continue
		}
		switch tier {
		case staterecord.SSMTierAdvanced:
			b.WriteString(fmt.Sprintf("\n  - tier = %q in the record_store block raises the ceiling to %d, and bills every parameter per month. An advanced parameter cannot be reverted to a standard one.", name, limit))
		case staterecord.SSMTierIntelligent:
			b.WriteString(fmt.Sprintf("\n  - tier = %q raises the ceiling to %d as well, and AWS charges only for the parameters past %d, creating standard ones below that.", name, limit, staterecord.SSMStandardParameterLimit))
		default:
			b.WriteString(fmt.Sprintf("\n  - tier = %q holds %d.", name, limit))
		}
	}
	b.WriteString("\n  - record_store \"s3\" has no object-count ceiling, and record_store \"local\" is bounded only by the filesystem.")
	return b.String()
}

func recordStoreCapacityDetail(rs *configs.LiveRecordStore, planned, needed, capacity int) string {
	return fmt.Sprintf(
		"This estate needs %d records (%d resource instances plus %d store key of its own), and record_store %q holds at most %d at %s. That is a hard AWS quota (L-C3B871CB, Adjustable: False) rather than a limit a support request can raise, so this run is refused before it writes the first record rather than failing partway through an apply with records written for some instances and not others.\n\nWays past it:%s",
		needed, planned, recordStoreOverheadKeys, rs.Type, capacity, recordStoreTierPhrase(rs),
		recordStoreRemedies(rs, needed),
	)
}

func recordStoreNearlyFullDetail(rs *configs.LiveRecordStore, planned, needed, capacity int) string {
	return fmt.Sprintf(
		"This estate needs %d records (%d resource instances plus %d store key of its own), against a ceiling of %d for record_store %q at %s. That ceiling counts every parameter in the account and region, not just this estate's, so whether these fit depends on what else is already there - a number this plan does not read, because finding it out costs a full DescribeParameters sweep on every run and is stale as soon as it is answered. If the account holds more than %d other parameters, this apply will fail partway through.\n\nWays past it:%s",
		needed, planned, recordStoreOverheadKeys, capacity, rs.Type, recordStoreTierPhrase(rs),
		capacity-needed,
		recordStoreRemedies(rs, needed),
	)
}
