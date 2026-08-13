// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableAiLocation is the ai-location cohort's slice of [DefaultTable]:
// the identity rows the ai-location ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableAiLocation = buildTable(
	// ---- Registry-ratified (#40, #44, #65): sixth batch, AI services and
	// ---- Location ---------------------------------------------------------
	//
	// Bedrock, Bedrock Agents (agents, knowledge bases, guardrails,
	// prompts), BedrockAgentCore (the v6.59.0 bump's new runtime/gateway/
	// memory/policy surface), Location Service, the Lex V2 Models family,
	// and the thin-coverage Comprehend/Kendra/Rekognition/Q Business
	// slices. Same tools/row-gen pipeline as the batches above, but this
	// batch leans harder on live/survey-full.json's per-type
	// taggable/listable/identity-schema signals than row-gen's own
	// registry-only classification: several of these newer Plugin
	// Framework resources are taggable and thus marker-path admittable
	// even where row-gen filed them "needs hand separator" or
	// "evidence-only" because their real import ID is a server-assigned
	// composite no config argument reconstructs (row-gen only tries the
	// client-named/parent-derived paths, never marker path, against a
	// composite primaryIdentifier). Every row below is additionally
	// confirmed against the AWS provider's documented Argument/Attribute/
	// Import sections at the pinned v6.59.0 tag, not accepted on the
	// registry's or the survey's classification alone. See the rejected-
	// and deferred-proposals note below this section for the disposition
	// of every row-gen row in scope that is not here, and
	// live/e2e/estates/ai-location/README.md for the full account. Cohort
	// estate: live/e2e/estates/ai-location.

	serverAssigned("aws_bedrock_guardrail",
		"Bedrock mints the guardrail's own guardrail_id at create time and a new version identifier each time it is published; the provider's real Import section documents a guardrail_id,version composite, not the single GuardrailArn the CFN registry's primaryIdentifier alone would suggest. No id attribute is separately documented, so IdentityAttrs stays empty.",
		"GUARDRAILID,VERSION"),
	serverAssigned("aws_bedrock_inference_profile",
		"Bedrock mints the inference profile's own identifier at create time; the provider's documented import example (inference_profile-id-12345678) is a server-assigned string distinct from the client-chosen name argument, correcting row-gen's own evidence-only flag (which read the registry's primaryIdentifier as argument-composed). Taggable per live/survey-full.json, so the marker path applies regardless.",
		"ID", "id"),
	serverAssigned("aws_bedrockagent_agent",
		"Bedrock mints the agent's own agent_id at create time; agent_name configures it but does not identify it. Its Attribute Reference documents id as \"Unique identifier of the agent\", the same value as agent_id.",
		"AGENTID", "id"),
	serverAssigned("aws_bedrockagent_agent_alias",
		"Bedrock mints the alias's own agent_alias_id at create time, scoped under its parent agent; the provider's documented import id is a comma-delimited agent_alias_id,agent_id composite with no single reconstructable value, which is why row-gen filed this \"needs hand separator\" rather than proposing a row. Taggable (the resource has its own tags/tags_all pair), so the marker path recovers it independently of that composite — the correction this batch makes to row-gen's classification, the same shape as aws_ec2_transit_gateway_connect_peer in the ec2-networking batch.",
		"ALIASID,AGENTID"),
	serverAssigned("aws_bedrockagent_flow",
		"Bedrock mints the flow's own identifier at create time; its Attribute Reference documents id as \"The unique identifier of the flow.\"",
		"ID", "id"),
	serverAssigned("aws_bedrockagent_knowledge_base",
		"Bedrock mints the knowledge base's own identifier at create time; its Attribute Reference documents id as \"Unique identifier of the knowledge base.\"",
		"ID", "id"),
	serverAssigned("aws_bedrockagent_prompt",
		"Bedrock mints the prompt's own identifier at create time; its Attribute Reference documents id as \"Unique identifier of the prompt.\"",
		"ID", "id"),

	serverAssigned("aws_bedrockagentcore_agent_runtime",
		"BedrockAgentCore mints the runtime's own agent_runtime_id at create time; agent_runtime_name configures it but does not identify it. No id attribute is documented (a Plugin Framework resource with its own Identity Schema instead), so IdentityAttrs stays empty.",
		"AGENTRUNTIMEID"),
	serverAssigned("aws_bedrockagentcore_agent_runtime_endpoint",
		"BedrockAgentCore mints the endpoint's own identifier at create time, scoped under its parent runtime; the provider's documented import id is an agent_runtime_id,endpoint-name composite, which is why row-gen filed this evidence-only rather than proposing a row (its argument-composed note). Taggable (tags/tags_all documented), so the marker path recovers it independently — the same correction this batch makes for aws_bedrockagent_agent_alias above.",
		"AGENTRUNTIMEID,NAME"),
	serverAssigned("aws_bedrockagentcore_api_key_credential_provider",
		"BedrockAgentCore mints the credential provider's own credential_provider_arn at create time; the name argument configures it but the provider's own import id is the ARN, not the name. No id attribute is documented.",
		"CREDENTIALPROVIDERARN"),
	serverAssigned("aws_bedrockagentcore_browser",
		"BedrockAgentCore mints the browser's own browser_id at create time; name configures it but does not identify it. No id attribute is documented.",
		"BROWSERID"),
	serverAssigned("aws_bedrockagentcore_browser_profile",
		"BedrockAgentCore mints the browser profile's own profile_id at create time; live/survey-full.json's v6.59.0 identity schema confirms profile_id as the sole required-for-import attribute. No id attribute is documented.",
		"PROFILEID"),
	serverAssigned("aws_bedrockagentcore_code_interpreter",
		"BedrockAgentCore mints the code interpreter's own code_interpreter_id at create time; name configures it but does not identify it. No id attribute is documented.",
		"CODEINTERPRETERID"),
	serverAssigned("aws_bedrockagentcore_evaluator",
		"BedrockAgentCore mints the evaluator's own evaluator_id at create time; live/survey-full.json's v6.59.0 identity schema confirms evaluator_id as the sole required-for-import attribute. No id attribute is documented.",
		"EVALUATORID"),
	serverAssigned("aws_bedrockagentcore_gateway",
		"BedrockAgentCore mints the gateway's own gateway_id at create time; no create-only argument in the registry evidence reconstructs it. No id attribute is documented.",
		"GATEWAYIDENTIFIER"),
	serverAssigned("aws_bedrockagentcore_harness",
		"BedrockAgentCore mints the harness's own harness_id at create time; live/survey-full.json's v6.59.0 identity schema confirms harness_id as the sole required-for-import attribute. No id attribute is documented.",
		"HARNESSID"),
	serverAssigned("aws_bedrockagentcore_memory",
		"BedrockAgentCore mints the memory's own identifier at create time; its Attribute Reference documents id as \"Unique identifier of the Memory.\"",
		"MEMORYID", "id"),
	serverAssigned("aws_bedrockagentcore_oauth2_credential_provider",
		"BedrockAgentCore mints the credential provider's own credential_provider_arn at create time; name configures it but the provider's own import id is the ARN. No id attribute is documented.",
		"CREDENTIALPROVIDERARN"),
	serverAssigned("aws_bedrockagentcore_online_evaluation_config",
		"BedrockAgentCore mints the config's own online_evaluation_config_id at create time; live/survey-full.json's v6.59.0 identity schema confirms it as the sole required-for-import attribute. No id attribute is documented.",
		"ONLINEEVALUATIONCONFIGID"),
	serverAssigned("aws_bedrockagentcore_policy_engine",
		"BedrockAgentCore mints the policy engine's own policy_engine_id at create time; live/survey-full.json's v6.59.0 identity schema confirms it as the sole required-for-import attribute. No id attribute is documented.",
		"POLICYENGINEID"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[ResourceArn], client-named,
		// proposed correctly (row-gen's argument line came from the
		// provider's own v6.59.0 identity schema). Confirmed against the
		// provider's documented Identity Schema (required: resource_arn)
		// and its terraform import example.
		Type:          "aws_bedrockagentcore_resource_policy",
		Components:    []Component{attr("resource_arn")},
		ImportSyntax:  "RESOURCE_ARN",
		IdentityAttrs: []string{"resource_arn"},
	},

	// Location Service. All five of the service's base types are taggable
	// (live/survey-full.json's "marker" signal), but each also has a
	// clean, single-argument client-named import grammar confirmed
	// against the provider's own Argument and Import sections — the more
	// precise path, so this batch prefers it over the coarser marker
	// path the taggable signal alone would justify, the same choice the
	// storage and ec2-networking batches made for their own taggable
	// client-named types. row-gen filed four of these five evidence-only
	// (GUESSED argument names, unconfirmed against an identity schema or
	// live/import-grammar.json); the argument names below are confirmed
	// directly against the pinned v6.59.0 provider docs' Argument
	// Reference sections, not guessed.
	TypeIdentity{
		Type:          "aws_location_geofence_collection",
		Components:    []Component{attr("collection_name")},
		ImportSyntax:  "COLLECTION_NAME",
		IdentityAttrs: []string{"collection_name"},
	},
	TypeIdentity{
		// row-gen: evidence-only (GUESSED argument name). Confirmed
		// against the provider's Argument Reference ("map_name - (Required)
		// The name for the map resource.") and Import section ("import
		// aws_location_map resources using the map name").
		Type:          "aws_location_map",
		Components:    []Component{attr("map_name")},
		ImportSyntax:  "MAP_NAME",
		IdentityAttrs: []string{"map_name"},
	},
	TypeIdentity{
		// row-gen: evidence-only (GUESSED argument name). Confirmed
		// against the provider's Argument Reference ("index_name -
		// (Required) Name of the place index resource.") and Import
		// section.
		Type:          "aws_location_place_index",
		Components:    []Component{attr("index_name")},
		ImportSyntax:  "INDEX_NAME",
		IdentityAttrs: []string{"index_name"},
	},
	TypeIdentity{
		// row-gen: evidence-only (GUESSED argument name). Confirmed
		// against the provider's Argument Reference ("calculator_name -
		// (Required) The name of the route calculator resource.") and
		// Import section.
		Type:          "aws_location_route_calculator",
		Components:    []Component{attr("calculator_name")},
		ImportSyntax:  "CALCULATOR_NAME",
		IdentityAttrs: []string{"calculator_name"},
	},
	TypeIdentity{
		// row-gen: evidence-only (GUESSED argument name). Confirmed
		// against the provider's Argument Reference ("tracker_name -
		// (Required) The name of the tracker resource.") and Import
		// section.
		Type:          "aws_location_tracker",
		Components:    []Component{attr("tracker_name")},
		ImportSyntax:  "TRACKER_NAME",
		IdentityAttrs: []string{"tracker_name"},
	},
	TypeIdentity{
		// row-gen: needs hand separator (composite TrackerName+ConsumerArn,
		// no join character in the registry). The provider's own Import
		// section supplies the separator directly: "tracker_name|consumer_arn",
		// confirmed against the pinned v6.59.0 tag rather than guessed —
		// the same correction shape as the ec2-networking batch's
		// network_acl_rule. Both halves are the resource's own required
		// config arguments (tracker_name, consumer_arn), so this is
		// parent-derived and untaggable-swept like the rest of this
		// table's composite associations, even though the type is not in
		// live/mapping.json's marker set.
		Type: "aws_location_tracker_association",
		Components: []Component{
			attr("tracker_name"),
			sep("|"),
			attr("consumer_arn"),
		},
		ImportSyntax:  "TRACKER_NAME|CONSUMER_ARN",
		IdentityAttrs: nil,
	},

	serverAssigned("aws_lexv2models_bot",
		"Lex mints the bot's own id at create time; bot_type configures it but does not identify it. Its Attribute Reference documents id as \"Unique identifier for a particular bot.\" The V1 aws_lex_bot type is a separate, cfn-unmodeled resource per #58 and is untouched by this row.",
		"ID", "id"),
	TypeIdentity{
		// row-gen: evidence-only (fold of AWS::Lex::Bot, proposed as
		// "parent-derived admission keyed on aws_lexv2models_bot once
		// ratified"). Unlike its sibling folds (bot_version, intent, slot,
		// slot_type — all rejected below because each mints its own
		// server-assigned id with no config argument supplying it),
		// aws_lexv2models_bot_locale's identity is the one genuinely
		// reconstructable from configuration alone: its own Argument
		// Reference requires locale_id, bot_id and bot_version, and its
		// documented Import id is exactly those three, comma-delimited,
		// in that order ("en_US,abcd-12345678,1"), confirmed against the
		// pinned v6.59.0 tag.
		Type: "aws_lexv2models_bot_locale",
		Components: []Component{
			attr("locale_id"),
			sep(","),
			attr("bot_id"),
			sep(","),
			attr("bot_version"),
		},
		ImportSyntax:  "LOCALE_ID,BOT_ID,BOT_VERSION",
		IdentityAttrs: nil,
	},

	serverAssigned("aws_comprehend_document_classifier",
		"Comprehend mints the classifier's own ARN at create time; the registry's primaryIdentifier (Arn) is confirmed by the provider's own Import section, which imports by ARN. No id attribute is separately documented.",
		"ARN"),
	serverAssigned("aws_kendra_index",
		"Kendra mints the index's own id at create time; edition and encryption configuration do not identify it. Its Attribute Reference documents id as \"The identifier of the Index.\"",
		"ID", "id"),
	serverAssigned("aws_qbusiness_application",
		"Q Business mints the application's own application_id at create time; identity_type and the OIDC/QuickSight configuration blocks do not identify it. No id attribute is documented.",
		"APPLICATIONID"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[CollectionId], client-named,
		// proposed correctly. Confirmed against the provider's own
		// v6.59.0 identity schema (required_for_import: collection_id)
		// and its documented import command.
		Type:          "aws_rekognition_collection",
		Components:    []Component{attr("collection_id")},
		ImportSyntax:  "COLLECTION_ID",
		IdentityAttrs: []string{"collection_id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ProjectName], client-named,
		// proposed correctly. Confirmed against the provider's own
		// v6.59.0 identity schema (required_for_import: name) and its
		// documented import command.
		Type:          "aws_rekognition_project",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], client-named, proposed
		// correctly. Confirmed against the provider's own v6.59.0 identity
		// schema (required_for_import: name) and its documented import
		// command.
		Type:          "aws_rekognition_stream_processor",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
)

func init() { registerCohortTable(identityTableAiLocation) }
