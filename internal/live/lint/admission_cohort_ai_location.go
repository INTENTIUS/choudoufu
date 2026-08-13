// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesAiLocation is the ai-location cohort's slice of [admittedTypesV0]:
// the types the ai-location ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesAiLocation = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): sixth batch, AI services and
	// ---- Location (Bedrock, Bedrock Agents, BedrockAgentCore, Location
	// ---- Service, Lex V2 Models, plus the thin-coverage Comprehend,
	// ---- Kendra, Rekognition and Q Business slices). Same tools/row-gen
	// ---- pipeline and verification standard as the batches above, cross-
	// ---- checked against live/survey-full.json's per-type taggable/
	// ---- listable/identity-schema signals (the same v6.59.0 survey the
	// ---- BedrockAgentCore types were added to) and the AWS provider's
	// ---- documented Argument/Attribute/Import sections at the pinned
	// ---- v6.59.0 tag. Twelve of row-gen's proposals or evidence-only
	// ---- rows in this batch's scope are rejected on independent
	// ---- verification and five more are deferred as out of this batch's
	// ---- mechanism (matchTable content-match adoption, not this table) —
	// ---- see internal/live/identity/table.go for the per-type evidence
	// ---- and live/e2e/estates/ai-location/README.md for the full
	// ---- account. Textract and Polly never entered scope at all: neither
	// ---- has any AWS::Textract::* or AWS::Polly::* entry anywhere in
	// ---- live/registry.json, the same registry-absent shape as the
	// ---- streaming batch's SWF. Lex V1 (aws_lex_bot, aws_lex_bot_alias,
	// ---- aws_lex_intent, aws_lex_slot_type) is untouched per #58's
	// ---- override precedent — live/mapping.json already carries all four
	// ---- as cfn-unmodeled, so row-gen never proposes them. Cohort
	// ---- estate: live/e2e/estates/ai-location.
	"aws_bedrock_guardrail":                            {},
	"aws_bedrock_inference_profile":                    {},
	"aws_bedrockagent_agent":                           {},
	"aws_bedrockagent_agent_alias":                     {},
	"aws_bedrockagent_flow":                            {},
	"aws_bedrockagent_knowledge_base":                  {},
	"aws_bedrockagent_prompt":                          {},
	"aws_bedrockagentcore_agent_runtime":               {},
	"aws_bedrockagentcore_agent_runtime_endpoint":      {},
	"aws_bedrockagentcore_api_key_credential_provider": {},
	"aws_bedrockagentcore_browser":                     {},
	"aws_bedrockagentcore_browser_profile":             {},
	"aws_bedrockagentcore_code_interpreter":            {},
	"aws_bedrockagentcore_evaluator":                   {},
	"aws_bedrockagentcore_gateway":                     {},
	"aws_bedrockagentcore_harness":                     {},
	"aws_bedrockagentcore_memory":                      {},
	"aws_bedrockagentcore_oauth2_credential_provider":  {},
	"aws_bedrockagentcore_online_evaluation_config":    {},
	"aws_bedrockagentcore_policy_engine":               {},
	"aws_bedrockagentcore_resource_policy":             {},
	"aws_location_geofence_collection":                 {},
	"aws_location_map":                                 {},
	"aws_location_place_index":                         {},
	"aws_location_route_calculator":                    {},
	"aws_location_tracker":                             {},
	"aws_location_tracker_association":                 {},
	"aws_lexv2models_bot":                              {},
	"aws_lexv2models_bot_locale":                       {},
	"aws_comprehend_document_classifier":               {},
	"aws_kendra_index":                                 {},
	"aws_qbusiness_application":                        {},
	"aws_rekognition_collection":                       {},
	"aws_rekognition_project":                          {},
	"aws_rekognition_stream_processor":                 {},
}

func init() { registerCohortAdmitted(admittedTypesAiLocation) }
