// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import "github.com/zclconf/go-cty/cty"

// aws_elasticache_user takes authentication_mode.type "no-password-required"
// (the CreateUser/ModifyUser input enum) but AWS reads it back as
// "no-password" (the output enum), so every plan after apply shows
// type = "no-password" -> "no-password-required". Reproduced on real AWS with
// provider 6.71.0 on 2026-11-04 (#2309, live/override-repro/elasticache-user-42712).
//
// Only that one pair is treated as equal. "no-password" is not a value the
// provider's validation accepts in configuration, so a prior of
// "no-password" can only have come from "no-password-required"; any other
// configured type ("password", "iam") still plans the change.
var _ = register(Override{
	Name:             "aws-elasticache-user-no-password",
	Provider:         awsProvider,
	Type:             "aws_elasticache_user",
	Approach:         RewriteDiff,
	SDK:              SDKv2,
	Upstream:         "hashicorp/terraform-provider-aws#42712",
	StockAlternative: "None: the provider rejects no-password, the value AWS returns, in configuration, so stock users can only ignore_changes the whole authentication_mode block.",
	Summary:          "authentication_mode type \"no-password-required\" plans an update to \"no-password\" on every plan",
	ProvenOn:         "6.71.0",
	Requires: map[string]cty.Type{
		"authentication_mode.type": cty.String,
	},
	Plan: KeepPriorWhenEquivalent(Equivalence{
		Path:  cty.GetAttrPath("authentication_mode").IndexInt(0).GetAttr("type"),
		Equal: elasticacheNoPasswordSame,
	}),
})

// elasticacheNoPasswordSame is true only for prior "no-password" against
// configured "no-password-required".
func elasticacheNoPasswordSame(prior, config cty.Value) bool {
	if prior.IsMarked() || config.IsMarked() || prior.Type() != cty.String || config.Type() != cty.String {
		return false
	}
	return prior.AsString() == "no-password" && config.AsString() == "no-password-required"
}
