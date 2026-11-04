// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import "github.com/zclconf/go-cty/cty"

// awsProvider is the AWS provider's namespace and type.
const awsProvider = "hashicorp/aws"

// aws_security_group_rule holds cidr_blocks and ipv6_cidr_blocks as lists,
// and both force replacement, so reordering the same CIDRs deletes and
// recreates the rule. AWS treats them as a set. Reproduced on real AWS and
// floci with provider 6.71.0 on 2026-11-04 (#2309): swapping two CIDRs
// planned "-/+ ... # forces replacement".
var _ = register(Override{
	Name:     "aws-security-group-rule-cidr-order",
	Provider: awsProvider,
	Type:     "aws_security_group_rule",
	Approach: RewriteDiff,
	SDK:      SDKv2,
	Upstream: "hashicorp/terraform-provider-aws#19082",
	Summary:  "Reordering the same CIDRs in cidr_blocks or ipv6_cidr_blocks replaces the rule",
	ProvenOn: "6.71.0",
	Requires: map[string]cty.Type{
		"cidr_blocks":      cty.List(cty.String),
		"ipv6_cidr_blocks": cty.List(cty.String),
	},
	Plan: KeepPriorWhenEquivalent(
		Equivalence{Path: cty.GetAttrPath("cidr_blocks"), Equal: SameStrings},
		Equivalence{Path: cty.GetAttrPath("ipv6_cidr_blocks"), Equal: SameStrings},
	),
})
