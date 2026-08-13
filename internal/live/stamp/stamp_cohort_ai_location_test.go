// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The ai-location cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableAiLocation = []string{
	// Registry-ratified AI services and Location batch (#40, #44,
	// issue #65). aws_bedrockagentcore_resource_policy,
	// aws_lexv2models_bot_locale and aws_location_tracker_association
	// are this batch's three untaggable types, below. See
	// live/e2e/estates/ai-location/README.md.
	"aws_bedrock_guardrail",
	"aws_bedrock_inference_profile",
	"aws_bedrockagent_agent",
	"aws_bedrockagent_agent_alias",
	"aws_bedrockagent_flow",
	"aws_bedrockagent_knowledge_base",
	"aws_bedrockagent_prompt",
	"aws_bedrockagentcore_agent_runtime",
	"aws_bedrockagentcore_agent_runtime_endpoint",
	"aws_bedrockagentcore_api_key_credential_provider",
	"aws_bedrockagentcore_browser",
	"aws_bedrockagentcore_browser_profile",
	"aws_bedrockagentcore_code_interpreter",
	"aws_bedrockagentcore_evaluator",
	"aws_bedrockagentcore_gateway",
	"aws_bedrockagentcore_harness",
	"aws_bedrockagentcore_memory",
	"aws_bedrockagentcore_oauth2_credential_provider",
	"aws_bedrockagentcore_online_evaluation_config",
	"aws_bedrockagentcore_policy_engine",
	"aws_comprehend_document_classifier",
	"aws_kendra_index",
	"aws_lexv2models_bot",
	"aws_location_geofence_collection",
	"aws_location_map",
	"aws_location_place_index",
	"aws_location_route_calculator",
	"aws_location_tracker",
	"aws_qbusiness_application",
	"aws_rekognition_collection",
	"aws_rekognition_project",
	"aws_rekognition_stream_processor",
}

var untaggableAiLocation = []string{
	// Registry-ratified AI services and Location batch (#40, #44,
	// issue #65): three types with no tags argument at all —
	// aws_bedrockagentcore_resource_policy's Attribute Reference
	// exports none, aws_lexv2models_bot_locale's Argument Reference
	// names no tags block, and aws_location_tracker_association's
	// Argument Reference names only consumer_arn/tracker_name/region.
	// See live/e2e/estates/ai-location/README.md.
	"aws_bedrockagentcore_resource_policy",
	"aws_lexv2models_bot_locale",
	"aws_location_tracker_association",
}

func init() {
	registerCohortStamp(taggableAiLocation, untaggableAiLocation, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified AI services and Location batch (#40, #44,
			// issue #65). Taggable per the real provider's documented Argument
			// Reference, except the three types below whose Argument/Attribute
			// Reference names no tags block at all. See
			// live/e2e/estates/ai-location/README.md.
			"aws_bedrock_guardrail":                            taggedSchema("id", "guardrail_id", "name"),
			"aws_bedrock_inference_profile":                    taggedSchema("id", "name"),
			"aws_bedrockagent_agent":                           taggedSchema("id", "agent_id", "agent_name"),
			"aws_bedrockagent_agent_alias":                     taggedSchema("id", "agent_alias_id", "agent_id"),
			"aws_bedrockagent_flow":                            taggedSchema("id", "name"),
			"aws_bedrockagent_knowledge_base":                  taggedSchema("id", "name", "role_arn"),
			"aws_bedrockagent_prompt":                          taggedSchema("id", "name"),
			"aws_bedrockagentcore_agent_runtime":               taggedSchema("id", "agent_runtime_id", "agent_runtime_name"),
			"aws_bedrockagentcore_agent_runtime_endpoint":      taggedSchema("id", "name", "agent_runtime_id"),
			"aws_bedrockagentcore_api_key_credential_provider": taggedSchema("id", "name"),
			"aws_bedrockagentcore_browser":                     taggedSchema("id", "name"),
			"aws_bedrockagentcore_browser_profile":             taggedSchema("id", "profile_id", "name"),
			"aws_bedrockagentcore_code_interpreter":            taggedSchema("id", "name"),
			"aws_bedrockagentcore_evaluator":                   taggedSchema("id", "evaluator_id", "evaluator_name"),
			"aws_bedrockagentcore_gateway":                     taggedSchema("id", "name"),
			"aws_bedrockagentcore_harness":                     taggedSchema("id", "harness_id", "harness_name"),
			"aws_bedrockagentcore_memory":                      taggedSchema("id", "name"),
			"aws_bedrockagentcore_oauth2_credential_provider":  taggedSchema("id", "name"),
			"aws_bedrockagentcore_online_evaluation_config":    taggedSchema("id", "online_evaluation_config_name"),
			"aws_bedrockagentcore_policy_engine":               taggedSchema("id", "name"),
			"aws_bedrockagentcore_resource_policy":             untaggedSchema("id", "resource_arn", "policy"),
			"aws_comprehend_document_classifier":               taggedSchema("id", "arn", "name"),
			"aws_kendra_index":                                 taggedSchema("id", "name", "role_arn"),
			"aws_lexv2models_bot":                              taggedSchema("id", "name", "role_arn"),
			"aws_lexv2models_bot_locale":                       untaggedSchema("id", "bot_id", "bot_version", "locale_id"),
			"aws_location_geofence_collection":                 taggedSchema("id", "collection_name"),
			"aws_location_map":                                 taggedSchema("id", "map_name"),
			"aws_location_place_index":                         taggedSchema("id", "index_name"),
			"aws_location_route_calculator":                    taggedSchema("id", "calculator_name"),
			"aws_location_tracker":                             taggedSchema("id", "tracker_name"),
			"aws_location_tracker_association":                 untaggedSchema("id", "tracker_name", "consumer_arn"),
			"aws_qbusiness_application":                        taggedSchema("id", "display_name"),
			"aws_rekognition_collection":                       taggedSchema("id", "collection_id"),
			"aws_rekognition_project":                          taggedSchema("id", "name"),
			"aws_rekognition_stream_processor":                 taggedSchema("id", "name", "role_arn"),
		})
	})
}
