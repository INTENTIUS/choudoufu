// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableStragglers is the stragglers cohort's slice of [DefaultTable]:
// the identity rows the stragglers ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableStragglers = buildTable(
	// Rejected on independent verification, all cross-checked against
	// live/survey-full.json's per-type taggable/listable signals in
	// addition to the provider's documented Argument/Attribute/Import
	// sections at the pinned v6.59.0 tag:
	//
	//   - aws_bedrock_guardrail_version: row-gen proposed this cleanly
	//     (registry primaryIdentifier=[GuardrailId,Version] ⊆
	//     readOnlyProperties), but the provider's own docs show it
	//     untaggable (no tags argument, no tags_all attribute) with no
	//     Cloud Control list fallback (registry: "not listable") and no
	//     native provider list resource — the same "moves to Ops"
	//     mechanism gap as aws_vpc_ipam_pool_cidr_allocation in the
	//     ec2-networking batch. The registry's server-assigned evidence
	//     is real; there is simply nothing to discover it by.
	//   - aws_bedrockagent_agent_action_group, aws_bedrockagent_agent_collaborator,
	//     aws_bedrockagent_agent_knowledge_base_association: all three
	//     mint their own server-assigned id (action_group_id,
	//     collaborator_id — the knowledge-base association has no
	//     standalone id at all, per its Bedrock Agent API) scoped under a
	//     parent agent, with no config argument supplying it, and all
	//     three are untaggable with no native list resource
	//     (live/survey-full.json: "moves to Ops"). Unlike
	//     aws_bedrockagent_agent_alias and aws_bedrockagentcore_agent_runtime_endpoint
	//     above, taggability does not rescue these — there is no
	//     discovery mechanism at all, not just an unreconstructable
	//     import id.
	//   - aws_bedrockagent_data_source: same shape — data_source_id is
	//     server-assigned, scoped under knowledge_base_id, and the type
	//     is untaggable with no native list resource. row-gen's own
	//     "needs hand separator" filing undersold the real gap: even with
	//     a human-chosen separator, data_source_id itself is not
	//     reconstructable from configuration.
	//   - aws_bedrockagentcore_gateway_target, aws_bedrockagentcore_memory_strategy,
	//     aws_bedrockagentcore_workload_identity: each mints its own
	//     server-assigned id (target_id, strategy_id, the workload
	//     identity's own name/ARN pair) with no config argument supplying
	//     it, and each is untaggable with no native list resource
	//     (live/survey-full.json: "moves to Ops" for all three).
	//     aws_bedrockagentcore_workload_identity in particular is the one
	//     row-gen filed with a GUESSED argument (name); the provider's own
	//     schema confirms name is create-only but not exported back as
	//     the resource's identity, so the guess would have been wrong
	//     regardless of the taggability gap.
	//   - aws_lexv2models_bot_version, aws_lexv2models_intent,
	//     aws_lexv2models_slot, aws_lexv2models_slot_type: siblings of
	//     aws_lexv2models_bot_locale (ratified above) that do NOT share
	//     its fully-reconstructable shape. Each mints its own
	//     server-assigned id (bot_version's own version number,
	//     intent_id, slot_id, slot_type_id respectively) that is exported
	//     but never a config argument on the resource itself, and all
	//     four are untaggable with no native list resource
	//     (live/survey-full.json: "moves to Ops" for all four). Confirmed
	//     against each type's own documented Attribute Reference at the
	//     pinned v6.59.0 tag, not assumed from the family resemblance to
	//     bot_locale.
	//
	// Deferred as out of this batch's mechanism, not rejected on the
	// merits — each needs live/foreign/classify.go's matchTable
	// (list-plus-content-match adoption), which row-gen's own non-goals
	// explicitly exclude (issue #44) and no ratification batch to date
	// has touched:
	//
	//   - aws_bedrockagentcore_gateway_rule, aws_bedrockagentcore_policy:
	//     both untaggable with a native list resource that requires a
	//     parent-scoped list argument (gateway_identifier,
	//     policy_engine_id respectively) — live/survey-full.json's own
	//     "list + content match" path, a mechanism this table's four
	//     Components-based admission paths do not reach.
	//   - aws_bedrockagentcore_registry: never proposed by row-gen at all
	//     (no AWS::BedrockAgentCore::Registry entry in live/registry.json
	//     — the provider's own v6.59.0 identity schema is the only
	//     evidence for it), but live/survey-full.json shows the same
	//     "list + content match" shape as the two above, noted here so a
	//     future matchTable batch does not have to re-derive it.
	//
	// Out of this batch's named scope, not rejected on the merits — issue
	// #65's recipe scopes Comprehend, Kendra, Rekognition and Polly to
	// "only clean proposals":
	//
	//   - aws_kendra_data_source, aws_kendra_faq: both "needs hand
	//     separator" per row-gen (composite Id+IndexId, parent-input
	//     IndexId, no join character in the registry) — Kendra's own
	//     thin CFN registry coverage (3 types total, only Index proposed
	//     cleanly) is exactly the "mostly thin provider coverage" this
	//     batch's recipe calls out, so the correction work this batch
	//     spent on Location and Lex V2 Models is not repeated here.
	//   - aws_textract_*, aws_polly_*: no AWS::Textract::* or
	//     AWS::Polly::* entry anywhere in live/registry.json — the same
	//     registry-absent shape as the streaming batch's SWF family, so
	//     row-gen proposes nothing for either service and neither ever
	//     entered this batch's scope.
	//   - aws_comprehend_entity_recognizer: the provider ships this
	//     resource, but it has no AWS::Comprehend::* registry entry
	//     beyond DocumentClassifier (row-gen's own "Comprehend (1 types)"
	//     count), so there is no CFN evidence for it to ratify against.

	// ---- Registry-ratified (#40, #44, #65): stragglers batch (issue #65's
	// ---- ratification campaign) ------------------------------------------
	//
	// Every row below is a type an earlier ratified batch's own cohort
	// README named as reachable but left outside that batch's stated named
	// scope. Same verification standard as every batch above: the pinned
	// v6.58.0 provider docs (fetched directly from
	// raw.githubusercontent.com/hashicorp/terraform-provider-aws at that
	// tag) or live/import-grammar.json's own scrape of the same source, not
	// the CFN registry's classification alone. Cohort estate:
	// live/e2e/estates/stragglers.
	//
	// Transfer Family: the data-movement batch named servers, users,
	// workflows and connectors only, leaving six more Transfer types
	// reachable. Five ratify here; the sixth, aws_transfer_ssh_key, is
	// rejected (see below).
	serverAssigned("aws_transfer_certificate",
		"the provider's Attribute Reference documents certificate_id as a resource-exported attribute, absent from the Argument Reference entirely (Required arguments are only certificate, usage; tags is Optional) - the documented import command uses certificate_id, which no configuration argument reconstructs.",
		"CERTIFICATE_ID", "id", "arn"),
	serverAssigned("aws_transfer_profile",
		"the provider's Attribute Reference documents profile_id as a resource-exported attribute, absent from the Argument Reference entirely (Required arguments are only as2_id, profile_type; tags is Optional) - the documented import command uses profile_id, which no configuration argument reconstructs.",
		"PROFILE_ID", "id", "arn"),
	serverAssigned("aws_transfer_web_app",
		"the provider's Attribute Reference documents web_app_id as a resource-exported attribute, absent from the Argument Reference entirely (the sole Required argument is the identity_provider_details block; tags is Optional) - the documented import command uses web_app_id, which no configuration argument reconstructs.",
		"WEB_APP_ID", "id", "arn"),
	TypeIdentity{
		// A web app customization is a named singleton child: exactly one
		// per web app, identified by the web app's own id. web_app_id is a
		// Required argument in the provider's own Argument Reference (not
		// merely an exported attribute), and the documented import command
		// uses that same value verbatim - the same named-singleton-child
		// shape as aws_s3_bucket_policy and
		// aws_networkmanager_core_network_policy_attachment below. The
		// provider ships no tags argument for this type at all.
		Type:          "aws_transfer_web_app_customization",
		Components:    []Component{attr("web_app_id")},
		ImportSyntax:  "WEB_APP_ID",
		IdentityAttrs: []string{"id", "web_app_id"},
	},
	serverAssigned("aws_transfer_agreement",
		"the documented import composite is server_id/agreement_id; server_id is a real Required argument, but agreement_id is documented only in the Attribute Reference (\"The unique identifier for the AS2 agreement\"), absent from the Argument Reference entirely - no configuration argument reconstructs the full identity. tags is Optional, so the type is taggable.",
		"SERVER_ID/AGREEMENT_ID", "id", "arn"),

	// NetworkManager: row-gen marks aws_networkmanager_core_network_policy_attachment
	// a property-child fold of AWS::NetworkManager::CoreNetwork with no
	// pastable row - but the provider's own Argument Reference shows its
	// sole identifying component, core_network_id, is a real Required
	// argument, and the documented import command uses that value alone,
	// with no separator. The same named-singleton-child shape as
	// aws_s3_bucket_policy: at most one policy attachment per core network,
	// concrete whenever the core network is. The provider ships no tags
	// argument for this type (policy_document is the only other argument).
	// Every other NetworkManager type row-gen proposes is already ratified
	// by the networking-advanced batch; this is the sole straggler in the
	// service.
	TypeIdentity{
		Type:          "aws_networkmanager_core_network_policy_attachment",
		Components:    []Component{attr("core_network_id")},
		ImportSyntax:  "CORE_NETWORK_ID",
		IdentityAttrs: []string{"id", "core_network_id"},
	},

	// Storage Gateway: registry-present for exactly one type, TapePool
	// (cfn-unmodeled beyond it - every other real Storage Gateway resource
	// has no CFN Registry entry at all, per live/mapping.json's own sweep).
	// The provider's documented import command uses the tape pool's ARN,
	// which the Attribute Reference confirms is a resource-exported
	// attribute (pool_name is the client-chosen argument, but the ARN's
	// own pool-NNNNNNNN suffix is server-minted, not pool_name). tags is
	// Optional, so the type is taggable.
	serverAssigned("aws_storagegateway_tape_pool",
		"the documented import id is the tape pool's ARN (arn:...:tapepool/pool-12345678); the Attribute Reference confirms arn is a resource-exported attribute, and the ARN's pool-NNNNNNNN suffix is server-minted, distinct from the client-chosen pool_name argument - no configuration argument reconstructs it.",
		"ARN", "id", "arn"),

	// ECR remainder: the IAM/ECR batch's own row-gen output classified all
	// five of these evidence-only (per #44's non-goals, no pastable row was
	// ever generated for any of them), not on any identity weakness -
	// independent verification against the pinned provider docs supplies a
	// clean, single-argument, Required-in-schema identity for each, the
	// same registry-undersold shape earlier batches corrected repeatedly
	// (aws_sns_topic_policy, aws_qldb_ledger, aws_memorydb_subnet_group).
	// None of the five carries a tags argument.
	TypeIdentity{
		// Named singleton child of aws_ecr_repository: at most one lifecycle
		// policy per repository, identified by the repository's own name
		// (a real Required argument, confirmed against the provider's own
		// Identity Schema: "repository - Name of the ECR repository").
		Type:          "aws_ecr_lifecycle_policy",
		Components:    []Component{attr("repository")},
		ImportSyntax:  "REPOSITORY",
		IdentityAttrs: []string{"id", "repository"},
	},
	TypeIdentity{
		// ecr_repository_prefix is a Required, ForceNew argument, and the
		// documented import command uses that value verbatim, with no
		// separator - a plain client-named type, not a fold of anything.
		Type:          "aws_ecr_pull_through_cache_rule",
		Components:    []Component{attr("ecr_repository_prefix")},
		ImportSyntax:  "ECR_REPOSITORY_PREFIX",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// principal_arn is a Required, ForceNew argument (the IAM principal
		// excluded from image-pull-time recording), and the documented
		// import command uses that value verbatim.
		Type:          "aws_ecr_pull_time_update_exclusion",
		Components:    []Component{attr("principal_arn")},
		ImportSyntax:  "PRINCIPAL_ARN",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// prefix is a Required, ForceNew argument, and the documented
		// import command uses that value verbatim; resource_tags (tags
		// applied to repositories this template creates) is a distinct
		// concept from a tags argument on the template resource itself,
		// which does not exist.
		Type:          "aws_ecr_repository_creation_template",
		Components:    []Component{attr("prefix")},
		ImportSyntax:  "PREFIX",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// Named singleton child of aws_ecr_repository, the same shape as
		// aws_ecr_lifecycle_policy above and the same type the IAM/ECR
		// batch's own devtools-batch follow-up (aws_ecrpublic_repository_policy)
		// already cites as this type's deferred sibling.
		Type:          "aws_ecr_repository_policy",
		Components:    []Component{attr("repository")},
		ImportSyntax:  "REPOSITORY",
		IdentityAttrs: []string{"id", "repository"},
	},
)

func init() { registerCohortTable(identityTableStragglers) }
