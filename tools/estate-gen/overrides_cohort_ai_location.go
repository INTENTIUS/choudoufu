// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesAiLocation is the ai-location cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesAiLocation = map[string]typeOverride{
	// ---- ai-location cohort (issue #65's sixth registry-backed batch) ----
	//
	// Every entry below shares one root cause the generic pass has no way
	// to see: these newer Plugin Framework resources name their IAM role
	// argument the bare "role_arn" (not "role" or a "*_role_arn" suffix),
	// which isRoleArg's curated alias does not match, so the generic pass
	// falls back to the type-driven string placeholder instead of wiring
	// the cohort's shared aws_iam_role. Confirmed by `terraform validate`
	// against the pinned v6.58.0 provider, not guessed.

	"aws_bedrockagent_knowledge_base": {
		Reasons: []string{
			`role_arn is named exactly "role_arn", which isRoleArg's curated alias does not match (only "role" or a "*_role_arn" suffix); the generic placeholder string is not a well-formed ARN (validate: "cannot be parsed as an ARN"). knowledge_base_configuration is Required-in-shape but the generic pass never visits it (validate: "Block ... must have a configuration value"); type = "MANAGED" needs no storage_configuration (the provider's own docs: "storage_configuration is not required when knowledge_base_configuration.type is MANAGED"), but the provider still requires the matching managed_knowledge_base_configuration sub-block present even though every field inside it is Optional-with-defaults (validate: "must be configured when ... type equals MANAGED")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
			kb := body.AppendNewBlock("knowledge_base_configuration", nil)
			kb.Body().SetAttributeRaw("type", exprTokens(`"MANAGED"`))
			kb.Body().AppendNewBlock("managed_knowledge_base_configuration", nil)
		},
	},
	"aws_bedrockagentcore_agent_runtime": {
		Reasons: []string{
			`role_arn is the same bare-"role_arn" gap as aws_bedrockagent_knowledge_base above. agent_runtime_artifact and network_configuration are both Required-in-shape blocks the generic pass never visits (validate: "Block ... must have a configuration value"); container_configuration.container_uri and network_mode = "PUBLIC" are the minimal valid choice within each. agent_runtime_name must match ^[a-zA-Z][a-zA-Z0-9_]{0,47}$ (validate: "value must match regular expression"), which the generic tofu-<cohort>-cohort-<type> placeholder's hyphens violate`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("agent_runtime_name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_agent_runtime"`, strings.ReplaceAll(g.cohort, "-", "_"))))
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
			artifact := body.AppendNewBlock("agent_runtime_artifact", nil)
			container := artifact.Body().AppendNewBlock("container_configuration", nil)
			container.Body().SetAttributeRaw("container_uri", exprTokens(fmt.Sprintf(
				`"000000000000.dkr.ecr.us-east-1.amazonaws.com/tofu-%s-cohort-agent-runtime:latest"`, g.cohort)))
			netCfg := body.AppendNewBlock("network_configuration", nil)
			netCfg.Body().SetAttributeRaw("network_mode", exprTokens(`"PUBLIC"`))
		},
	},
	"aws_bedrockagentcore_agent_runtime_endpoint": {
		Reasons: []string{
			`name was mis-wired to aws_iam_role.<cohort>.name by the generic pass's same-name parentRef search, the same shape aws_bedrockagentcore_code_interpreter's own override above explains - this type's own name pattern (^[a-zA-Z][a-zA-Z0-9_]{0,47}$) surfaced only at apply time (terraform validate does not evaluate cross-resource references), against the pinned floci image during this batch's verification`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_runtime_endpoint"`, strings.ReplaceAll(g.cohort, "-", "_"))))
		},
	},
	"aws_bedrockagentcore_api_key_credential_provider": {
		Reasons: []string{
			`schema requires exactly one of api_key/api_key_wo (validate: "No attribute specified when one (and only one) of [api_key,api_key_wo] is required"); the generic pass renders neither, since api_key is Sensitive and this generator's required-only pass only fills non-sensitive Required arguments`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("api_key", exprTokens(`"tofu-cohort-placeholder-api-key"`))
		},
	},
	"aws_bedrockagentcore_browser": {
		Reasons: []string{
			`network_configuration is Required-in-shape but the generic pass never visits it (validate: "Block network_configuration must have a configuration value"); network_mode = "PUBLIC" is the minimal valid choice (vpc_config is Optional, only needed for network_mode = "VPC"). name is also mis-wired to aws_iam_role.<cohort>.name by the generic pass's same-name parentRef search (this type is server-assigned, so identityArgName never supplies its own name); left as-is rather than overridden, since aws_bedrockagentcore_browser's own "name" argument carries no format pattern in the provider's docs and the IAM role's name string is a harmless, if confusing, valid value here — unlike its siblings below, which do enforce ^[a-zA-Z][a-zA-Z0-9_]{0,47}$`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			netCfg := body.AppendNewBlock("network_configuration", nil)
			netCfg.Body().SetAttributeRaw("network_mode", exprTokens(`"PUBLIC"`))
		},
	},
	"aws_bedrockagentcore_browser_profile": {
		Reasons: []string{
			`name was mis-wired to aws_iam_role.<cohort>.name by the generic pass's same-name parentRef search, the same shape aws_bedrockagentcore_code_interpreter's own override below explains - this type's own name pattern (^[a-zA-Z][a-zA-Z0-9_]{0,47}$) surfaced only at apply time (terraform validate does not evaluate cross-resource references), against the pinned floci image during this batch's verification`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_browser_profile"`, strings.ReplaceAll(g.cohort, "-", "_"))))
		},
	},
	"aws_bedrockagentcore_code_interpreter": {
		Reasons: []string{
			`same network_configuration gap as aws_bedrockagentcore_browser above. name was also mis-wired to aws_iam_role.<cohort>.name by the generic pass's same-name parentRef search (this type is server-assigned, so identityArgName never supplies its own name, and parentRef's fallback then matches any sibling that owns a "name" argument) - the same "mis-wired to a same-named sibling" shape the streaming batch's own aws_appsync_graphql_api needed corrected, except here the sibling it lands on happens to be an IAM role whose own name contains hyphens the ^[a-zA-Z][a-zA-Z0-9_]{0,47}$ pattern (validate: "must start with a letter and contain only letters, numbers, and underscores") rejects`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_code_interpreter"`, strings.ReplaceAll(g.cohort, "-", "_"))))
			netCfg := body.AppendNewBlock("network_configuration", nil)
			netCfg.Body().SetAttributeRaw("network_mode", exprTokens(`"PUBLIC"`))
		},
	},
	"aws_bedrockagentcore_evaluator": {
		Reasons: []string{
			`evaluator_config is Required-in-shape but the generic pass never visits it (validate: "Block evaluator_config must have a configuration value"); llm_as_a_judge with a numerical rating_scale is the minimal valid variant (code_based would need a real Lambda ARN this cohort has no sibling for). evaluator_name must match ^[a-zA-Z][a-zA-Z0-9_]{0,47}$ (validate: "must begin with a letter and contain only alphanumeric characters and underscores"), which the generic placeholder's hyphens violate. level is a fixed enum (TOOL_CALL/TRACE/SESSION), not the generic placeholder string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("evaluator_name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_evaluator"`, strings.ReplaceAll(g.cohort, "-", "_"))))
			body.SetAttributeRaw("level", exprTokens(`"SESSION"`))
			cfg := body.AppendNewBlock("evaluator_config", nil)
			judge := cfg.Body().AppendNewBlock("llm_as_a_judge", nil)
			judge.Body().SetAttributeRaw("instructions", exprTokens(`"Score the agent's helpfulness on the scale below."`))
			modelCfg := judge.Body().AppendNewBlock("model_config", nil)
			bedrockCfg := modelCfg.Body().AppendNewBlock("bedrock_evaluator_model_config", nil)
			bedrockCfg.Body().SetAttributeRaw("model_id", exprTokens(`"anthropic.claude-3-haiku-20240307-v1:0"`))
			scale := judge.Body().AppendNewBlock("rating_scale", nil)
			numerical := scale.Body().AppendNewBlock("numerical", nil)
			numerical.Body().SetAttributeRaw("definition", exprTokens(`"How helpful the agent's response was."`))
			numerical.Body().SetAttributeRaw("label", exprTokens(`"Helpfulness"`))
			numerical.Body().SetAttributeRaw("value", exprTokens(`1`))
		},
	},
	"aws_bedrockagentcore_gateway": {
		Reasons: []string{
			`role_arn is the same bare-"role_arn" gap as aws_bedrockagent_knowledge_base above. authorizer_type = "AWS_IAM" avoids the Optional-in-shape-but-Required-in-practice authorizer_configuration block CUSTOM_JWT would need (validate confirms authorizer_configuration is required only "when authorizer_type is set to CUSTOM_JWT")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authorizer_type", exprTokens(`"AWS_IAM"`))
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
		},
	},
	"aws_bedrockagentcore_harness": {
		Reasons: []string{
			`model is Required-in-shape but the generic pass never visits it (validate: "Block model must have a configuration value"); bedrock_model_config is the minimal valid variant. harness_name must be 1-40 characters (validate: "string length must be between 1 and 40"), shorter than the generic tofu-<cohort>-cohort-<type> placeholder's 48`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("harness_name", exprTokens(fmt.Sprintf(`"tofu_%s_harness"`, strings.ReplaceAll(g.cohort, "-", "_"))))
			model := body.AppendNewBlock("model", nil)
			bedrockCfg := model.Body().AppendNewBlock("bedrock_model_config", nil)
			bedrockCfg.Body().SetAttributeRaw("model_id", exprTokens(`"anthropic.claude-3-haiku-20240307-v1:0"`))
		},
	},
	"aws_bedrockagentcore_memory": {
		Reasons: []string{
			`event_expiry_duration must be 7-365 (validate: "value must be between 7 and 365"); the generic numeric placeholder is 0. name was also mis-wired to aws_iam_role.<cohort>.name by the generic pass's same-name parentRef search, the same shape aws_bedrockagentcore_code_interpreter's own override above explains - this type's own name pattern surfaced only at apply time (terraform validate does not evaluate cross-resource references), against the pinned floci image during this batch's verification`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_memory"`, strings.ReplaceAll(g.cohort, "-", "_"))))
			body.SetAttributeRaw("event_expiry_duration", exprTokens(`30`))
		},
	},
	"aws_bedrockagentcore_oauth2_credential_provider": {
		Reasons: []string{
			`credential_provider_vendor is a fixed enum (validate: "does not match any valid values", the pinned provider's real set differs from its own docs) - CustomOauth2 is the one variant whose oauth2_provider_config sub-block (custom_oauth2_provider_config) has no Required fields of its own. oauth2_provider_config itself is Required-in-shape but the generic pass never visits it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("credential_provider_vendor", exprTokens(`"CustomOauth2"`))
			cfg := body.AppendNewBlock("oauth2_provider_config", nil)
			cfg.Body().AppendNewBlock("custom_oauth2_provider_config", nil)
		},
	},
	"aws_bedrockagentcore_online_evaluation_config": {
		Reasons: []string{
			`data_source_config, evaluator and rule are all Required-in-shape blocks the generic pass never visits (validate: "Block ... must have a configuration value"). online_evaluation_config_name must start with a letter and contain only alphanumerics/underscores, up to 48 characters (validate: "must start with a letter and contain only alphanumeric characters and underscores"), which the generic placeholder's hyphens and length both violate`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("online_evaluation_config_name", exprTokens(fmt.Sprintf(`"tofu_%s_online_eval"`, strings.ReplaceAll(g.cohort, "-", "_"))))
			ds := body.AppendNewBlock("data_source_config", nil)
			cwLogs := ds.Body().AppendNewBlock("cloudwatch_logs", nil)
			cwLogs.Body().SetAttributeRaw("log_group_names", exprTokens(fmt.Sprintf(`["/tofu-%s-cohort/agent-traces"]`, g.cohort)))
			cwLogs.Body().SetAttributeRaw("service_names", exprTokens(`["bedrock-agentcore"]`))
			ev := body.AppendNewBlock("evaluator", nil)
			ev.Body().SetAttributeRaw("evaluator_id", exprTokens(`"Builtin.Helpfulness"`))
			rule := body.AppendNewBlock("rule", nil)
			sampling := rule.Body().AppendNewBlock("sampling_config", nil)
			sampling.Body().SetAttributeRaw("sampling_percentage", exprTokens(`10`))
		},
	},
	"aws_bedrockagentcore_policy_engine": {
		Reasons: []string{
			`name was mis-wired to aws_iam_role.<cohort>.name by the generic pass's same-name parentRef search, the same shape aws_bedrockagentcore_code_interpreter's own override above explains - this type's own name pattern (^[a-zA-Z][a-zA-Z0-9_]{0,47}$) surfaced only at apply time (terraform validate does not evaluate cross-resource references), against the pinned floci image during this batch's verification`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_policy_engine"`, strings.ReplaceAll(g.cohort, "-", "_"))))
		},
	},
	"aws_bedrockagentcore_resource_policy": {
		Reasons: []string{
			`resource_arn is validated as a well-formed ARN (validate: "cannot be parsed as an ARN"); the generic identity-argument placeholder ("tofu-<cohort>-cohort-...") is not one. policy is validated as well-formed JSON (validate: "is not valid JSON string format"), the same shape as aws_dynamodb_resource_policy's own override above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			resourceARN := fmt.Sprintf(`"arn:aws:bedrock-agentcore:us-east-1:000000000000:runtime/tofu-%s-cohort"`, g.cohort)
			body.SetAttributeRaw("resource_arn", exprTokens(resourceARN))
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "bedrock-agentcore:InvokeAgentRuntime"
      Resource  = %s
    }]
  })`, resourceARN)))
		},
	},
	"aws_comprehend_document_classifier": {
		Reasons: []string{
			`language_code is a fixed enum (validate: "expected language_code to be one of [en es fr de it pt]"), not the generic placeholder string. input_data_config's data_format defaults to COMPREHEND_CSV, which then requires s3_uri (validate: "one of input_data_config.0.augmented_manifests,input_data_config.0.s3_uri must be specified"); the generic pass renders the block but leaves it empty because none of its own arguments are schema-Required`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("language_code", exprTokens(`"en"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "input_data_config" {
					blk.Body().SetAttributeRaw("s3_uri", exprTokens(fmt.Sprintf(
						`"s3://tofu-%s-cohort-comprehend/train/"`, g.cohort)))
				}
			}
		},
	},
	"aws_kendra_index": {
		Reasons: []string{
			`role_arn is the same bare-"role_arn" gap as aws_bedrockagent_knowledge_base above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
		},
	},
	"aws_lexv2models_bot": {
		Reasons: []string{
			`role_arn is the same bare-"role_arn" gap as aws_bedrockagent_knowledge_base above. data_privacy is Required-in-shape but the generic pass never visits it (validate: "Block data_privacy must have a configuration value"). idle_session_ttl_in_seconds must be 60-86400 (validate: "cannot be parsed as an ARN" aside, the schema documents the range directly); the generic numeric placeholder is 0`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
			body.SetAttributeRaw("idle_session_ttl_in_seconds", exprTokens(`60`))
			dp := body.AppendNewBlock("data_privacy", nil)
			dp.Body().SetAttributeRaw("child_directed", exprTokens(`false`))
		},
	},
	"aws_location_tracker_association": {
		Reasons: []string{
			`consumer_arn is validated as a well-formed ARN (validate: "invalid ARN: arn: invalid prefix"); wired to this cohort's own aws_location_geofence_collection, the same consumer shape the provider's own example config uses (aws_location_tracker_association.example.consumer_arn = aws_location_geofence_collection.example.collection_arn)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			consumerExpr := fmt.Sprintf(`"arn:aws:geo:us-east-1:000000000000:geofence-collection/tofu-%s-cohort-placeholder"`, g.cohort)
			if collection, ok := g.byType["aws_location_geofence_collection"]; ok {
				consumerExpr = collection.String() + ".collection_arn"
			}
			body.SetAttributeRaw("consumer_arn", exprTokens(consumerExpr))
		},
	},
	"aws_qbusiness_application": {
		Reasons: []string{
			`attachments_configuration is Required-in-shape but the generic pass never visits it (validate: "Block attachments_configuration must have a configuration value"); DISABLED is the minimal valid choice. identity_center_instance_arn is validated as a well-formed ARN (validate: "cannot be parsed as an ARN"); this cohort has no IAM Identity Center instance sibling to reference, so it stays a literal placeholder ARN, the same "no real sibling to reference" shape aws_rds_integration's own override above accepts for its target_arn`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("identity_center_instance_arn", exprTokens(`"arn:aws:sso:::instance/ssoins00000000001"`))
			ac := body.AppendNewBlock("attachments_configuration", nil)
			ac.Body().SetAttributeRaw("attachments_control_mode", exprTokens(`"DISABLED"`))
		},
	},
	"aws_rekognition_stream_processor": {
		Reasons: []string{
			`role_arn is the same bare-"role_arn" gap as aws_bedrockagent_knowledge_base above. input, output and settings are all Required-in-shape blocks the generic pass never visits (validate: "Block ... must have a configuration value"); each also requires at least one of its own Optional-in-shape sub-blocks in practice (validate: "At least one attribute out of [...] must be specified") - kinesis_video_stream, s3_destination and connected_home are the minimal valid choice within each`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
			input := body.AppendNewBlock("input", nil)
			kvs := input.Body().AppendNewBlock("kinesis_video_stream", nil)
			kvs.Body().SetAttributeRaw("arn", exprTokens(fmt.Sprintf(
				`"arn:aws:kinesisvideo:us-east-1:000000000000:stream/tofu-%s-cohort-stream/1234567890000"`, g.cohort)))
			output := body.AppendNewBlock("output", nil)
			s3dest := output.Body().AppendNewBlock("s3_destination", nil)
			s3dest.Body().SetAttributeRaw("bucket", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-rekognition"`, g.cohort)))
			settings := body.AppendNewBlock("settings", nil)
			connectedHome := settings.Body().AppendNewBlock("connected_home", nil)
			connectedHome.Body().SetAttributeRaw("labels", exprTokens(`["PERSON"]`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesAiLocation) }
