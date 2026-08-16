// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesIamEcr is the iam-ecr cohort's slice of [typeOverrides],
// folding the fully hand-written live/e2e/estates/iam-ecr (#108 criterion
// 4's last iam.tf/ecr.tf cohort with no recorded regeneration command at
// all). Every entry's provider-validation knowledge comes from the hand
// files' own blocks; their ratification-evidence comments moved verbatim
// into that cohort's README.md ("Ratification evidence, relocated from
// iam.tf/ecr.tf") when the fold replaced the files. Registered by init
// below; see contributing/LIVE-TABLES.md.
var typeOverridesIamEcr = map[string]typeOverride{
	"aws_ecr_registry_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"); set to the hand-written cohort's own registry policy - an allow-pull statement for the account root principal, kept verbatim (the Sid charset permits no cohort-derived hyphens)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "TofuIamEcrCohortAllowPull"
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = ["ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage"]
    }]
  })`))
		},
	},
	"aws_ecr_registry_scanning_configuration": {
		Reasons: []string{
			`scan_type is a required argument the provider validates as an enum (validate: "expected scan_type to be one of [\"BASIC\" \"ENHANCED\"], got placeholder"); set to "BASIC", the member the hand-written cohort used`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scan_type", exprTokens(`"BASIC"`))
		},
	},
	"aws_ecr_replication_configuration": {
		Reasons: []string{
			`replication_configuration is optional in the wire schema (MinItems 0), so the generic pass renders an empty resource - but a replication configuration with no rule configures nothing, and the destination's region and registry_id carry provider-side shape validation (a region name and a 12-digit account ID) no generic placeholder satisfies. The hand-written block's literal us-west-2/000000000000 destination is kept: a placeholder region and registry ID rather than a second real account, the same "keep the block out of the emulator's boundary" choice live/e2e/estates/lambda's placeholder ARNs make`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			rc := body.AppendNewBlock("replication_configuration", nil)
			rule := rc.Body().AppendNewBlock("rule", nil)
			dest := rule.Body().AppendNewBlock("destination", nil)
			dest.Body().SetAttributeRaw("region", exprTokens(`"us-west-2"`))
			dest.Body().SetAttributeRaw("registry_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_iam_service_linked_role": {
		Reasons: []string{
			`aws_service_name is a required argument the provider validates by shape (validate: "must be a full service hostname e.g. elasticbeanstalk.amazonaws.com"); set to elasticbeanstalk.amazonaws.com, the provider's own documented example service, as the hand-written cohort chose`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("aws_service_name", exprTokens(`"elasticbeanstalk.amazonaws.com"`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesIamEcr) }
