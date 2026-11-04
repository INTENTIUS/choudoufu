// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import "github.com/zclconf/go-cty/cty"

// aws_backup_region_settings sends only the configured keys of
// resource_type_opt_in_preference and resource_type_management_preference to
// UpdateRegionSettings, and AWS leaves the other resource types as they
// were. Read then stores every resource type AWS returns, so a config that
// names one type plans every other type "true -> null" on every plan, and
// applying that changes nothing. Reproduced on real AWS with provider 6.71.0
// on 2026-11-04 (#2309): { "DynamoDB" = true } planned the other 18 opt-in
// types to null. The management preference map is the same map of bool, set
// and read by the same code, and AWS returns both of its types (DynamoDB,
// EFS) whether or not they are configured.
var _ = register(Override{
	Name:             "aws-backup-region-settings-subset",
	Provider:         awsProvider,
	Type:             "aws_backup_region_settings",
	Approach:         RewriteDiff,
	SDK:              SDKv2,
	Upstream:         "hashicorp/terraform-provider-aws#22744",
	StockAlternative: "Listing every resource type AWS returns stops the diff in stock OpenTofu until AWS adds a type, as it did with DSQL; the configuration then drifts again.",
	Summary:          "Configuring some resource types in resource_type_opt_in_preference or resource_type_management_preference plans every other type AWS returns as removed on every plan",
	ProvenOn:         "6.71.0",
	Requires: map[string]cty.Type{
		"resource_type_opt_in_preference":     cty.Map(cty.Bool),
		"resource_type_management_preference": cty.Map(cty.Bool),
	},
	Plan: KeepPriorWhenEquivalent(
		Equivalence{Path: cty.GetAttrPath("resource_type_opt_in_preference"), Equal: configuredKeysMatchPrior},
		Equivalence{Path: cty.GetAttrPath("resource_type_management_preference"), Equal: configuredKeysMatchPrior},
	),
})

// configuredKeysMatchPrior is an [Equivalence.Equal] for a map where the
// remote side adds keys of its own: equal when every configured key is in
// prior with the same value. Keys only in prior do not count. A configured
// key that is missing from prior, or has a different value there, is a real
// change.
func configuredKeysMatchPrior(prior, config cty.Value) bool {
	if prior.IsMarked() || config.IsMarked() || !prior.Type().IsMapType() || !config.Type().IsMapType() ||
		!prior.Type().ElementType().Equals(config.Type().ElementType()) {
		return false
	}
	p := prior.AsValueMap()
	for k, cv := range config.AsValueMap() {
		pv, ok := p[k]
		if !ok || cv.IsNull() || pv.IsNull() || !pv.RawEquals(cv) {
			return false
		}
	}
	return true
}
