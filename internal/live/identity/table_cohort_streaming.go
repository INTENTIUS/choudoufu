// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableStreaming is the streaming cohort's slice of [DefaultTable]:
// the identity rows the streaming ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableStreaming = buildTable(
	// ---- Registry-ratified (#40, #44, #65): fifth batch, streaming and
	// ---- app integration ------------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own website/docs/r/
	// source at the pinned v6.59.0 tag) and, for the types the pinned
	// release ships one, against its own ResourceIdentitySchema
	// (live/survey-full.json's required_for_import field) — not accepted on
	// the registry's classification alone. Cohort estate:
	// live/e2e/estates/streaming.
	//
	// MSK's scope this batch is deliberately narrow — clusters,
	// configurations and the serverless cluster, plus MSK Connect (which
	// live/mapping.json carries under AWS::KafkaConnect::*, a
	// via:"service-alias" row, not AWS::MSK::* itself). row-gen's MSK
	// section proposes six more types (aws_msk_cluster_policy,
	// aws_msk_replicator, aws_msk_single_scram_secret_association,
	// aws_msk_vpc_connection all pastable; aws_msk_scram_secret_association
	// and aws_msk_topic evidence-only) that this batch does not evaluate at
	// all — deferred to a future batch's scope, not rejected on the merits.
	// SWF never entered scope: live/mapping.json carries aws_swf_domain as
	// via:"cfn-unmodeled" (tools/mapping-gen/overlay.d/sweep-servicequotas-
	// xray.json's own note: "all three services have zero matching CFN
	// Registry types at all"), so row-gen prints nothing for it.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_appsync_api: row-gen proposed server-assigned via the
	//     registry's ApiArn (AWS::AppSync::Api's primaryIdentifier). The
	//     provider disagrees: its documented import command (terraform
	//     import aws_appsync_api.example example-api-id) and Attribute
	//     Reference both name the identifier api_id, not the arn — the
	//     same registry-says-one-field-but-the-provider-imports-by-another
	//     shape the earlier batches' rejections established (e.g. the
	//     messaging batch's aws_cloudwatch_event_rule). api_id is itself a
	//     registry readOnlyProperty, so a corrected row is possible in
	//     principle, but this batch does not write one — see
	//     live/e2e/estates/streaming/README.md for why.
	//   - aws_appsync_api_cache, aws_appsync_api_key: row-gen proposed both
	//     server-assigned off registry readOnlyProperties (Id, ApiKeyId).
	//     Independent verification of live/registry.json itself finds both
	//     CFN types' handlers block is create/read/update/delete/list all
	//     false — the "registry-laggard" shape live/LIMITATIONS.md's
	//     Registry-laggard live services table already names both of these
	//     exact types for (they are two of that table's existing rows). A
	//     registry entry with no working handler at all supplies no real
	//     evidence, whatever its primaryIdentifier field claims; row-gen's
	//     classifier does not check handler liveness, so this is exactly
	//     the kind of proposal the ratification step exists to catch
	//     instead.
	//   - aws_appsync_domain_name_api_association: row-gen proposed
	//     server-assigned via the registry's ApiAssociationIdentifier
	//     (AWS::AppSync::DomainNameApiAssociation's primaryIdentifier). The
	//     provider disagrees outright, not just on which field: its
	//     documented import command (terraform import
	//     aws_appsync_domain_name_api_association.example example.com)
	//     imports by the client-supplied domain_name argument, already in
	//     configuration — this type is client-named, not server-assigned,
	//     and the registry's own claim is simply wrong for what this
	//     provider does.
	//   - aws_appsync_function: row-gen proposed server-assigned via the
	//     registry's FunctionArn (AWS::AppSync::FunctionConfiguration's
	//     primaryIdentifier, a single field). The provider disagrees: its
	//     documented import command (terraform import
	//     aws_appsync_function.example xxxxx-yyyyy) is a hyphen-joined
	//     composite of api_id (a configured argument) and function_id (a
	//     server-assigned output first available only after creation) —
	//     the registry's single-field claim understates the real shape the
	//     same way aws_cloudwatch_event_rule's did in the messaging batch,
	//     and the correction needs a component this table's vocabulary
	//     does not have (composing a configured argument with the type's
	//     own not-yet-created output), not just a separator guess.
	//   - aws_scheduler_schedule: row-gen itself never proposed this row —
	//     its "name" argument was GUESSED, not backed by an identity
	//     schema, live/import-grammar.json or the carve seed, so it landed
	//     evidence-only. Independent verification confirms row-gen's own
	//     caution was warranted: the provider's documented import ID is
	//     the composite group_name/name (terraform import
	//     aws_scheduler_schedule.example my-schedule-group/my-schedule),
	//     and its own v6.59.0 identity schema lists both group_name and
	//     name as required_for_import but neither as a required argument
	//     (live/survey-full.json: "identity attrs (group_name, name) are
	//     settable but not required arguments, so client-naming is
	//     unprovable from the schema"). This batch's mandate is to ratify
	//     what row-gen proposes; a row it never proposed stays out.

	serverAssigned("aws_mq_broker",
		"Amazon MQ assigns the broker's own id (a UUID) at create time; broker_name is client-chosen but names the broker, not its identity. Confirmed against the provider's documented import command (terraform import aws_mq_broker.example a1b2c3d4-d5f6-7777-8888-9999aaaabbbbcccc) and its Attribute Reference, which states id is \"the unique ID that Amazon MQ generates for the broker.\"",
		"ID", "id"),
	serverAssigned("aws_mq_configuration",
		"Amazon MQ assigns the configuration's own id (c-…) at create time; name is client-chosen but names the configuration, not its identity. Confirmed against the provider's documented import command (terraform import aws_mq_configuration.example c-0187d1eb-88c8-475a-9b79-16ef5a10c94f) and its Attribute Reference, which states id is \"the unique ID that Amazon MQ generates for the configuration.\"",
		"ID", "id"),

	serverAssigned("aws_msk_cluster",
		"MSK mints the cluster's own ARN at create time, embedding a UUID it assigns itself; cluster_name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's own v6.59.0 identity schema (required_for_import: arn) and its documented import command (terraform import aws_msk_cluster.example arn:aws:kafka:us-west-2:123456789012:cluster/example/279c0212-d057-4dba-9aa9-1c4e5a25bfc7-3).",
		"ARN", "arn"),
	serverAssigned("aws_msk_configuration",
		"MSK mints the configuration's own ARN at create time; name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's documented import command (terraform import aws_msk_configuration.example arn:aws:kafka:us-west-2:123456789012:configuration/example/279c0212-d057-4dba-9aa9-1c4e5a25bfc7-3). Untaggable — its Argument Reference names no tags block at all, and live/registry.json's own tagging.taggable is false — so it reaches this table only through the registry-backed path, not the marker path; see live/e2e/estates/streaming/README.md, \"Untaggable types.\"",
		"ARN", "arn"),
	serverAssigned("aws_msk_serverless_cluster",
		"MSK mints the serverless cluster's own ARN at create time; cluster_name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's own v6.59.0 identity schema (required_for_import: arn) and its documented import command (terraform import aws_msk_serverless_cluster.example arn:aws:kafka:us-west-2:123456789012:cluster/example/279c0212-d057-4dba-9aa9-1c4e5a25bfc7-3).",
		"ARN", "arn"),

	// MSK Connect: live/mapping.json's AWS::KafkaConnect::* rows reach
	// these three aws_mskconnect_* types by via:"service-alias", not a
	// direct AWS::MSK::* mapping — the CFN service is named KafkaConnect,
	// the TF resources keep the mskconnect_ prefix.
	serverAssigned("aws_mskconnect_connector",
		"KafkaConnect (MSK Connect) mints the connector's own ARN at create time; connector_name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's documented import command (terraform import aws_mskconnect_connector.example 'arn:aws:kafkaconnect:eu-central-1:123456789012:connector/example/264edee4-17a3-412e-bd76-6681cfc93805-3').",
		"ARN", "arn"),
	serverAssigned("aws_mskconnect_custom_plugin",
		"KafkaConnect mints the custom plugin's own ARN at create time; name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's documented import command (terraform import aws_mskconnect_custom_plugin.example 'arn:aws:kafkaconnect:eu-central-1:123456789012:custom-plugin/debezium-example/abcdefgh-1234-5678-9abc-defghijklmno-4').",
		"ARN", "arn"),
	serverAssigned("aws_mskconnect_worker_configuration",
		"KafkaConnect mints the worker configuration's own ARN at create time; name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's documented import command (terraform import aws_mskconnect_worker_configuration.example 'arn:aws:kafkaconnect:eu-central-1:123456789012:worker-configuration/example/8848493b-7fcc-478c-a646-4a52634e3378-4').",
		"ARN", "arn"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[ConnectorProfileName], in
		// createOnlyProperties and not in readOnlyProperties — client-named,
		// row-gen proposed it correctly the first time. Confirmed against
		// the provider's own v6.59.0 identity schema (required_for_import:
		// name) and its documented import command (terraform import
		// aws_appflow_connector_profile.example example-profile). Untaggable
		// — its Attribute Reference exports only arn and credentials_arn,
		// no tags, and live/registry.json's own tagging.taggable is false
		// — so it reaches this table only through the registry-backed
		// path; see live/e2e/estates/streaming/README.md, "Untaggable
		// types."
		Type:          "aws_appflow_connector_profile",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[FlowName], client-named,
		// proposed correctly. Confirmed against the provider's own v6.59.0
		// identity schema (required_for_import: name) and its documented
		// import command (terraform import aws_appflow_flow.example
		// example-flow).
		Type:          "aws_appflow_flow",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	serverAssigned("aws_appsync_graphql_api",
		"AppSync mints the GraphQL API's own id at create time; the registry's primaryIdentifier for this type, ApiId, is exactly the value the provider's own documented import command uses (terraform import aws_appsync_graphql_api.example 0123456789) and exports as both id (\"API ID\") and arn (\"ARN\") — the one AppSync proposal in this batch's scope where the registry and the provider agree; see the rejected-proposals note above for the four AppSync siblings where they do not.",
		"ID", "id", "arn"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], client-named, proposed
		// correctly (row-gen's argument line came from
		// live/import-grammar.json). Confirmed against the provider's
		// documented import command (terraform import aws_pipes_pipe.example
		// my-pipe) and its Attribute Reference, which states id is "Same as
		// name."
		Type:          "aws_pipes_pipe",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], client-named, proposed
		// correctly. Confirmed against the provider's documented import
		// command (terraform import aws_scheduler_schedule_group.example
		// my-schedule-group) and its Attribute Reference, which states id
		// is "Name of the schedule group." aws_scheduler_schedule, the
		// sibling type one section below in the provider's own docs, is
		// not ratified here — see the rejected-proposals note above.
		Type:          "aws_scheduler_schedule_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
)

func init() { registerCohortTable(identityTableStreaming) }
