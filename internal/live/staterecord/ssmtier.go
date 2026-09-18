// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// SSMTier is which Parameter Store tier an [SSMStore] creates its
// parameters in. It is the one setting that moves this backend's record
// ceiling, and the reason GitHub issue #1146 exists: before it, the
// advanced tier's 100,000-parameter ceiling could not be reached even
// deliberately, so an estate of 10,000 resources had no SSM answer at all.
//
// The spellings here are the fork's own configuration vocabulary
// (lower-case, underscored), not the SDK's; [SSMTier.parameterTier] maps
// them onto types.ParameterTier. They exist as their own type so that the
// record_store block's decoder, this store and every message about the
// ceiling name the same three values, and so [SSMTierNames] can render them
// rather than a hand-maintained list drifting out of step.
type SSMTier string

const (
	// SSMTierUnset is the zero value and the default: PutParameter is sent
	// with no Tier at all, so the ACCOUNT's own default-tier configuration
	// decides. That default is "standard" for an account that has never
	// changed it, which is why [SSMTierCapacity] prices this the same as
	// [SSMTierStandard] — an assumption a capacity message must state
	// rather than hide, since an account set to Advanced or
	// Intelligent-Tiering has more room than this admits.
	//
	// It is the only possible default. Sending an explicit "Standard"
	// instead would silently override a default-tier choice made outside
	// this tool, and sending "Advanced" would silently start a
	// per-parameter monthly charge.
	SSMTierUnset SSMTier = ""

	// SSMTierStandard writes every parameter in the standard tier
	// explicitly: 10,000 per account per region, 4KB values, no charge.
	// Distinct from [SSMTierUnset] in exactly one way — it pins the tier
	// against an account default of Advanced or Intelligent-Tiering.
	SSMTierStandard SSMTier = "standard"

	// SSMTierAdvanced writes every parameter in the advanced tier: 100,000
	// per account per region and 8KB values, BILLED per parameter per
	// month. It is also one-way per parameter — the SDK's own
	// documentation is explicit that "you can't revert an advanced
	// parameter to a standard parameter", because the revert would truncate
	// an 8KB value to 4KB.
	SSMTierAdvanced SSMTier = "advanced"

	// SSMTierIntelligent is Parameter Store's Intelligent-Tiering: AWS
	// decides per write, creating a standard parameter unless the request
	// needs an advanced one — and "more than 10,000 parameters already
	// exist in your account in the current Region" is one of the listed
	// conditions that needs one. So it reaches the same 100,000 ceiling as
	// [SSMTierAdvanced] while charging only for the parameters past 10,000.
	//
	// That makes it the cheaper of the two ways over the wall, which is why
	// it is offered alongside advanced rather than left out as a detail.
	SSMTierIntelligent SSMTier = "intelligent_tiering"
)

// Parameter Store's hard limits, quoted from
// aws-sdk-go-v2/service/ssm@v1.62.0's PutParameterInput documentation. See
// [SSMStore]'s "Capacity" section for the whole statement and for which of
// these this package enforces (none: the count check belongs at plan time,
// in internal/live/projection).
const (
	// SSMStandardParameterLimit is Service Quotas' L-C3B871CB, listed
	// Adjustable: False. Per account, per region, across every estate and
	// every parameter this fork did not write.
	SSMStandardParameterLimit = 10000

	// SSMAdvancedParameterLimit is the same quota for advanced parameters,
	// also per account per region.
	SSMAdvancedParameterLimit = 100000

	// SSMParameterNameLimit is the longest parameter name SSM accepts,
	// INCLUDING the roughly 45-character ARN prefix AWS prepends. Measured
	// against this SDK version by GitHub issue #1283.
	SSMParameterNameLimit = 1011

	// SSMParameterHierarchyLimit is how many "/"-delimited levels a
	// parameter name may have. Also #1283.
	SSMParameterHierarchyLimit = 15

	// SSMStandardValueLimit and SSMAdvancedValueLimit are the per-parameter
	// value sizes. [SSMStore] base64-encodes payload, costing a further
	// 4/3, so the usable payload is about three quarters of each.
	SSMStandardValueLimit = 4 * 1024
	SSMAdvancedValueLimit = 8 * 1024
)

// ssmTiers is the one table every other function here reads, in the order
// a message should list them.
var ssmTiers = []struct {
	tier     SSMTier
	capacity int
	value    int
	sdk      types.ParameterTier
}{
	{SSMTierUnset, SSMStandardParameterLimit, SSMStandardValueLimit, ""},
	{SSMTierStandard, SSMStandardParameterLimit, SSMStandardValueLimit, types.ParameterTierStandard},
	{SSMTierAdvanced, SSMAdvancedParameterLimit, SSMAdvancedValueLimit, types.ParameterTierAdvanced},
	{SSMTierIntelligent, SSMAdvancedParameterLimit, SSMAdvancedValueLimit, types.ParameterTierIntelligentTiering},
}

// Known reports whether t is one of the tiers this fork knows, the empty
// default included.
func (t SSMTier) Known() bool {
	for _, e := range ssmTiers {
		if e.tier == t {
			return true
		}
	}
	return false
}

// parameterTier is the SDK enum to send for t. The empty return for
// [SSMTierUnset] is what makes the default defer to the account's own
// default-tier configuration: aws-sdk-go-v2 omits a zero-valued enum from
// the request entirely.
func (t SSMTier) parameterTier() types.ParameterTier {
	for _, e := range ssmTiers {
		if e.tier == t {
			return e.sdk
		}
	}
	return ""
}

// SSMTierCapacity is how many parameters an account may hold in one region
// at tier t: [SSMStandardParameterLimit] or [SSMAdvancedParameterLimit].
//
// [SSMTierUnset] is priced as standard, because that is the account default
// for an account that has not changed it. An account that HAS set a
// different default has more room than this reports, so the number is a
// conservative lower bound in exactly one direction: it can under-report
// what such an account allows, never over-report it. An unknown tier is
// priced as standard for the same reason — the smallest of the ceilings is
// the only safe guess.
func SSMTierCapacity(t SSMTier) int {
	for _, e := range ssmTiers {
		if e.tier == t {
			return e.capacity
		}
	}
	return SSMStandardParameterLimit
}

// SSMTierValueLimit is the largest parameter value tier t accepts, before
// [SSMStore]'s base64 encoding costs a further 4/3.
func SSMTierValueLimit(t SSMTier) int {
	for _, e := range ssmTiers {
		if e.tier == t {
			return e.value
		}
	}
	return SSMStandardValueLimit
}

// SSMTierNames is every tier an author may write, in listing order and
// excluding [SSMTierUnset], which is spelled by omitting the argument.
// Derived from the same table the behavior is, so a message listing the
// valid values cannot drift from the set actually accepted.
func SSMTierNames() []string {
	names := make([]string, 0, len(ssmTiers))
	for _, e := range ssmTiers {
		if e.tier == SSMTierUnset {
			continue
		}
		names = append(names, string(e.tier))
	}
	return names
}

// ParseSSMTier reads an author's spelling of a tier. The empty string is
// [SSMTierUnset] and valid: it is how "leave it to the account default" is
// written. Anything else unrecognised returns false.
func ParseSSMTier(s string) (SSMTier, bool) {
	t := SSMTier(s)
	if !t.Known() {
		return SSMTierUnset, false
	}
	return t, true
}
