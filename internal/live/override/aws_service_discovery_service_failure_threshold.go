// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import "github.com/zclconf/go-cty/cty"

// aws_service_discovery_service's health_check_custom_config.failure_threshold
// is ForceNew, but AWS no longer supports the argument and always stores 1.
// A configured 5 therefore plans failure_threshold = 1 -> 5 and replaces the
// service on every plan. The provider's own schema at 6.71.0 marks it
// Deprecated: "The argument is no longer supported by AWS and the value is
// always set to 1." Reproduced on real AWS with provider 6.71.0 on 2026-11-04
// (#2309, live/override-repro/servicediscovery-35559).
//
// A prior of exactly 1 is equal to any configured number, because AWS
// ignores the value and changing it changes nothing on AWS. A prior other
// than 1 (a service AWS stored a different value for) still plans the
// change, and so does every other attribute of the service.
var _ = register(Override{
	Name:     "aws-service-discovery-service-failure-threshold",
	Provider: awsProvider,
	Type:     "aws_service_discovery_service",
	Approach: RewriteDiff,
	SDK:      SDKv2,
	Upstream: "hashicorp/terraform-provider-aws#35559",
	Summary:  "A configured health_check_custom_config failure_threshold other than 1 replaces the service on every plan",
	ProvenOn: "6.71.0",
	Requires: map[string]cty.Type{
		"health_check_custom_config.failure_threshold": cty.Number,
	},
	Plan: KeepPriorWhenEquivalent(Equivalence{
		Path:  cty.GetAttrPath("health_check_custom_config").IndexInt(0).GetAttr("failure_threshold"),
		Equal: serviceDiscoveryThresholdIgnored,
	}),
})

// serviceDiscoveryThresholdIgnored is true when AWS stored 1, the only value
// it now keeps, whatever number is configured.
func serviceDiscoveryThresholdIgnored(prior, config cty.Value) bool {
	if prior.IsMarked() || config.IsMarked() || prior.Type() != cty.Number || config.Type() != cty.Number {
		return false
	}
	eq := prior.Equals(cty.NumberIntVal(1))
	if eq.IsMarked() || !eq.IsKnown() {
		return false
	}
	return eq.True()
}
