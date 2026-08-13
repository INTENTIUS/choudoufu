// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesMedia is the media cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesMedia = map[string]typeOverride{
	"aws_ivschat_logging_configuration": {
		Reasons: []string{
			`destination_configuration is Required in the provider's own docs (exactly one of cloudwatch_logs/firehose/s3, each with its own Required leaf) but Optional in the wire schema - enforced only by the provider's plan-time validation, so the generic required-only pass never visits it at all; wired to a literal S3 bucket name, the same destination_configuration.s3 shape aws_ivs_recording_configuration already renders (the generic pass fills that one because it is genuinely block-Required there)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			dc := body.AppendNewBlock("destination_configuration", nil)
			s3 := dc.Body().AppendNewBlock("s3", nil)
			s3.Body().SetAttributeRaw("bucket_name", exprTokens(`"placeholder"`))
		},
	},
	"aws_medialive_multiplex": {
		Reasons: []string{
			`name is a plain client-chosen string (not this type's identity - aws_medialive_multiplex is server-assigned), but gen.go's parentRef mistakes it for a parent reference: this cohort's other client-named type, aws_media_packagev2_channel_group, also owns a single-component identity argument called "name", and parentRef's own same-name tiebreaker only guards two types that *both* claim "name" as their identity - not a server-assigned type's ordinary same-named argument. Left alone, the generic pass would point the multiplex at an unrelated MediaPackage channel group's name`,
			`availability_zones is a required list of strings, but the provider validates a 2-item minimum (validate: "attribute availability_zones requires 2 item minimum, but config has only 1 declared"); the generic pass emits one placeholder element`,
			`multiplex_settings is Required in the provider's own docs (transport_stream_bitrate and transport_stream_id both Required within it) but Optional in the wire schema - enforced only by the provider's plan-time validation, so the generic required-only pass never visits it at all`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-medialive-multiplex"`, g.cohort)))
			body.SetAttributeRaw("availability_zones", exprTokens(`["us-east-1a", "us-east-1b"]`))
			ms := body.AppendNewBlock("multiplex_settings", nil)
			ms.Body().SetAttributeRaw("transport_stream_bitrate", exprTokens(`1000000`))
			ms.Body().SetAttributeRaw("transport_stream_id", exprTokens(`1`))
		},
	},
	"aws_medialive_multiplex_program": {
		Reasons: []string{
			`multiplex_id names the parent aws_medialive_multiplex by its server-assigned id, but this type's identity is a two-component composite (program_name, multiplex_id joined by "/"), and gen.go's identityArgName only links a single-component identity - so parentRef has nothing to match multiplex_id against and the generic pass leaves it a disconnected placeholder, the same "no automatic link to a server-assigned parent" gap cognitoUserPoolIDRef and iamPolicyArnRef below already work around for their own types`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("multiplex_id", exprTokens(medialiveMultiplexIDRef(g)))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesMedia) }
