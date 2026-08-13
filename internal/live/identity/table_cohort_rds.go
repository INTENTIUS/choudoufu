// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableRds is the rds cohort's slice of [DefaultTable]:
// the identity rows the rds ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableRds = buildTable(
	// ---- Registry-ratified (#40, #44): fourth batch, RDS (issue #65's
	// ---- ratification campaign) -----------------------------------------
	//
	// Same pipeline as the earlier batches: every row started as a
	// tools/row-gen proposal from live/registry.json's RDS section (18
	// proposals), cross-checked against the AWS provider's documented
	// import behaviour at the pinned v6.58.0 tag
	// (raw.githubusercontent.com/hashicorp/terraform-provider-aws/v6.58.0/website/docs/r/*.html.markdown)
	// rather than accepted on the registry's classification alone. Cohort
	// estate: live/e2e/estates/rds.
	//
	// Five of the seventeen ratified rows are corrections, the same shape
	// as the messaging batch's aws_sns_topic_policy: row-gen filed them
	// "evidence-only" or "needs hand separator" because the registry's own
	// primaryIdentifier/readOnlyProperties evidence did not resolve them,
	// but the provider's own Import section names a concrete, documented
	// grammar built entirely from arguments already in configuration —
	// aws_db_proxy_default_target_group, aws_db_proxy_endpoint,
	// aws_db_instance_role_association, aws_rds_cluster_role_association and
	// aws_rds_global_cluster below. One proposal is rejected outright
	// (aws_db_proxy_target): its documented import string embeds a literal
	// segment ("RDS_INSTANCE" vs. "TRACKED_CLUSTER") chosen by *which* of two
	// optional arguments a config sets, a conditional-literal component this
	// table's vocabulary does not have, the same "needs a component this
	// table's vocabulary does not have yet" shape as the messaging batch's
	// aws_cloudwatch_event_rule rejection.
	//
	// aws_db_instance keeps live/SURVEY.md's own recorded wrinkle (the
	// "third wrinkle" in that file's "Classification wrinkles" section): the
	// original survey filed it under marker on taggability alone, because
	// v6.58.0 ships it no identity schema and no list resource, but its
	// documented import ID is the client-chosen "identifier" argument, so it
	// wires client-named here, exactly as that file predicted a batch that
	// reached RDS would do. Its own "id" attribute is the RDS DBI resource
	// ID, a distinct provider-minted value the provider's own Attribute
	// Reference lists separately from "identifier" — unlike
	// aws_rds_cluster_instance below, whose "id" and "identifier" attributes
	// are documented as the same string — so "id" is deliberately not
	// claimed as an identity source here, the same standard of care as
	// aws_ecs_cluster's synthesized id.
	//
	// aws_db_instance is also this batch's emulator caveat, the same
	// deliberate stance as the messaging batch's aws_sqs_queue: floci needs
	// the Docker socket mounted into its container to serve RDS at all
	// (lex00/floci#28), and neither the gated Go test harness
	// (internal/live/flocitest.flocitest.go), the shell e2e harness
	// (live/e2e/run.sh) nor any cohort README's "Verifying by hand" `docker
	// run` command mounts it as of this batch — confirmed by inspection, not
	// merely carried over from live/SURVEY.md's note. The type ratifies on
	// paper: its identity is sound and independently verified against the
	// provider's docs regardless of what any one emulator can run. See
	// live/e2e/estates/rds/README.md's "Verifying by hand" section for the
	// caveat recorded the way aws_sqs_queue's is.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_db_proxy_target: row-gen filed this evidence-only (a fold
	//     child of aws_db_proxy_default_target_group with no registry
	//     primaryIdentifier of its own). The provider's documented import ID
	//     is "db_proxy_name/target_group_name/type/resource_identifier",
	//     where db_proxy_name and target_group_name are both configured
	//     arguments and resource_identifier is whichever of
	//     db_instance_identifier or db_cluster_identifier a config sets
	//     (idlessAttr's alternation-list shape handles that part fine) — but
	//     "type" is the literal string "RDS_INSTANCE" or "TRACKED_CLUSTER"
	//     chosen by *which* of those two optional arguments is set, not a
	//     value any argument carries and not a fixed separator either. No
	//     [Component] in this table's vocabulary expresses "a literal
	//     conditioned on which alternative matched", so this stays a
	//     needs-hand-separator case rather than a guess this batch writes
	//     blind, the same stance as the messaging batch's two rejections.
	//
	// Not this batch's to decide: aws_db_proxy_target's own true fold
	// children (aws_db_snapshot, aws_db_cluster_snapshot,
	// aws_rds_cluster_endpoint, and the rest of the RDS resource family
	// row-gen classifies "marker" by taggability alone rather than proposing
	// a pastable row) carry no registry evidence at all and are simply
	// outside this batch's scope, the same as the messaging batch's Logs and
	// Events family.

	TypeIdentity{
		// registry.json: primaryIdentifier=[SubscriptionName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_db_event_subscription.default
		// rds-event-sub) and its Attribute Reference, which states id is
		// "The name of the RDS event notification subscription" — the same
		// name argument verbatim.
		Type:          "aws_db_event_subscription",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBInstanceIdentifier], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// The provider ships no identity schema for this type in v6.58.0 (see
		// live/SURVEY.md's "third wrinkle"), but its documented import
		// command (terraform import aws_db_instance.default
		// mydb-rds-instance) and Argument Reference ("identifier - (Optional)
		// The name of the RDS instance, if omitted, Terraform will assign a
		// random, unique identifier") confirm the client-named shape
		// row-gen proposed. Its own "id" attribute is "RDS DBI resource ID"
		// per the Attribute Reference — a distinct provider-minted value,
		// not the identifier — so "id" is deliberately not claimed as an
		// identity source here. See this section's banner comment above for
		// the emulator caveat (lex00/floci#28) this type ratifies despite.
		Type:          "aws_db_instance",
		Components:    []Component{attr("identifier")},
		ImportSyntax:  "IDENTIFIER",
		IdentityAttrs: []string{"identifier"}, // "id" intentionally omitted: id is the DBI resource ID, not the identifier
	},
	TypeIdentity{
		// row-gen filed this evidence-only: registry.json's primaryIdentifier
		// (TargetGroupArn) is entirely a readOnlyProperties field, so its own
		// classify rule refuses a pastable row, noting only "import docs show
		// argument-composed ID". Reading that import section directly: the
		// documented command (terraform import
		// aws_db_proxy_default_target_group.example example) imports "using
		// the db_proxy_name" — a named-singleton child of aws_db_proxy, the
		// same shape as aws_sns_topic_policy in the messaging batch, keyed on
		// the parent's own name argument rather than an opaque ARN the
		// registry's primaryIdentifier names. The provider's own Attribute
		// Reference confirms it: "id - Name of the RDS DB Proxy" — the
		// exported "name" attribute is the target group's own fixed name
		// ("default"), a different thing, and is not claimed as an identity
		// source here.
		Type:          "aws_db_proxy_default_target_group",
		Components:    []Component{attr("db_proxy_name")},
		ImportSyntax:  "DB_PROXY_NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen filed this evidence-only: registry.json's guessed argument
		// name (db_proxy_endpoint_name) is "not backed by a provider identity
		// schema or the carve seed", so its own rules refuse a pastable row.
		// Reading the import section directly resolves it cleanly: the
		// documented import ID is "DB-PROXY-NAME/DB-PROXY-ENDPOINT-NAME", a
		// concrete composite of two arguments the Argument Reference marks
		// Required (db_proxy_name, db_proxy_endpoint_name) — the same
		// concrete-composite shape as aws_iam_role_policy_attachment. The
		// Attribute Reference confirms "id" is exactly that composite ("The
		// name of the proxy and proxy endpoint separated by /"), so id is
		// claimed as an identity source, the same standard of care as
		// aws_iam_role_policy's colon-joined id.
		Type: "aws_db_proxy_endpoint",
		Components: []Component{
			attr("db_proxy_name"),
			sep("/"),
			attr("db_proxy_endpoint_name"),
		},
		ImportSyntax:  "DB-PROXY-NAME/DB-PROXY-ENDPOINT-NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[OptionGroupName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_db_option_group.example
		// mysql-option-group) and its Attribute Reference ("id - DB option
		// group name").
		Type:          "aws_db_option_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBParameterGroupName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_db_parameter_group.rds_pg rds-pg) and its
		// Attribute Reference ("id - The db parameter group name").
		Type:          "aws_db_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBProxyName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_db_proxy.example example) and its Argument
		// Reference ("name" is Required, not merely settable). Unlike the
		// types above, its own "id" attribute is documented as "The Amazon
		// Resource Name (ARN) for the proxy" — a different value from name —
		// so "id" is deliberately not claimed as an identity source here,
		// the same standard of care as aws_ecs_cluster's synthesized id.
		Type:          "aws_db_proxy",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted: id is the proxy's ARN, not name
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBSubnetGroupName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// The provider ships an identity schema for this type
		// (required_for_import: name), which live/survey-full.json's own
		// mechanical pass reads as "needs-config-signal" because name is
		// settable but not a schema-Required argument (Optional, Terraform
		// assigns a random name when omitted) — the same shape
		// aws_s3_bucket's own "bucket" argument already has among the types
		// this table admits unconditionally. Confirmed against the
		// provider's documented import command (terraform import
		// aws_db_subnet_group.default production-subnet-group) and its
		// Attribute Reference ("id - The db subnet group name").
		Type:          "aws_db_subnet_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBClusterIdentifier], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Same "needs-config-signal" mechanical classification as
		// aws_db_subnet_group above, for the same reason
		// (cluster_identifier is Optional; Terraform assigns a random one
		// when omitted), overridden here the same way. Confirmed against the
		// provider's documented import command (terraform import
		// aws_rds_cluster.aurora_cluster aurora-prod-cluster) and its
		// Attribute Reference ("id - RDS Cluster Identifier").
		Type:          "aws_rds_cluster",
		Components:    []Component{attr("cluster_identifier")},
		ImportSyntax:  "CLUSTER_IDENTIFIER",
		IdentityAttrs: []string{"id", "cluster_identifier"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBInstanceIdentifier] (this type
		// maps to the same AWS::RDS::DBInstance CFN type as aws_db_instance
		// above), in createOnlyProperties and not in readOnlyProperties —
		// client-named. Confirmed against the provider's documented import
		// command (terraform import
		// aws_rds_cluster_instance.prod_instance_1
		// aurora-cluster-instance-1) and its Attribute Reference, which lists
		// both "identifier" and "id" as "Instance identifier" — the same
		// string — unlike aws_db_instance above, where id is a distinct
		// DBI resource ID. "id" is claimed as an identity source here for
		// exactly that reason.
		Type:          "aws_rds_cluster_instance",
		Components:    []Component{attr("identifier")},
		ImportSyntax:  "IDENTIFIER",
		IdentityAttrs: []string{"id", "identifier"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBClusterParameterGroupName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_rds_cluster_parameter_group.cluster_pg
		// production-pg-1) and its Attribute Reference ("id - The db cluster
		// parameter group name").
		Type:          "aws_rds_cluster_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen filed this evidence-only (a fold child of aws_rds_cluster
		// with no registry primaryIdentifier of its own). Reading the
		// provider's Import section directly: the documented import ID is
		// "DB Cluster Identifier and IAM Role ARN separated by a comma", a
		// concrete composite of two arguments the Argument Reference marks
		// Required (db_cluster_identifier, role_arn) — the same
		// concrete-composite shape as aws_iam_role_policy. The Attribute
		// Reference confirms "id" is exactly that composite, so id is
		// claimed as an identity source.
		Type: "aws_rds_cluster_role_association",
		Components: []Component{
			attr("db_cluster_identifier"),
			sep(","),
			attr("role_arn"),
		},
		ImportSyntax:  "DBCLUSTERIDENTIFIER,ROLEARN",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen refused a pastable row outright ("the composite separator
		// is not registry evidence; a human chooses it") because
		// primaryIdentifier=[Engine, EngineVersion] is a composite with no
		// separator in any schema. Reading the provider's Import section
		// resolves it: the documented import ID is "engine and engine_version
		// separated by a colon", and both halves are Required arguments
		// already in configuration — the same concrete-composite shape as
		// aws_iam_role_policy's ROLENAME:POLICYNAME. The provider's own
		// Attribute Reference exports no "id" at all for this type, so this
		// imports by string only, like aws_route_table_association; nothing
		// is claimed as an identity source.
		Type: "aws_rds_custom_db_engine_version",
		Components: []Component{
			attr("engine"),
			sep(":"),
			attr("engine_version"),
		},
		ImportSyntax:  "ENGINE:ENGINE_VERSION",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this evidence-only: the registry's own primaryIdentifier
		// (GlobalClusterIdentifier) is in createOnlyProperties, which would
		// ordinarily propose client-named, but row-gen's own rule flags the
		// argument name as "GUESSED: snake_cased CFN property name, not
		// backed by a provider identity schema or the carve seed" and
		// refuses a pastable row on that basis alone. The provider's own
		// Argument Reference resolves the guess directly: "global_cluster_identifier
		// - (Required, Forces new resources)" is exactly that argument, no
		// snake-casing inference needed, and its Attribute Reference confirms
		// "id - RDS Global Cluster identifier" is the same string. Confirmed
		// against the documented import command (terraform import
		// aws_rds_global_cluster.example example).
		Type:          "aws_rds_global_cluster",
		Components:    []Component{attr("global_cluster_identifier")},
		ImportSyntax:  "GLOBAL_CLUSTER_IDENTIFIER",
		IdentityAttrs: []string{"id", "global_cluster_identifier"},
	},
	serverAssigned("aws_rds_integration",
		"the RDS service assigns the integration's own ARN at create time (Amazon Resource Name (ARN) of the Integration); integration_name, source_arn and target_arn together name what it connects, not the integration resource itself.",
		"ARN", "arn", "id"),
	// aws_rds_integration: registry.json's primaryIdentifier (IntegrationArn)
	// is entirely a readOnlyProperties field, matching row-gen's
	// server-assigned proposal. Confirmed against the provider's documented
	// import command (terraform import aws_rds_integration.example
	// arn:aws:rds:us-west-2:123456789012:integration:abcdefgh-...) and its
	// Attribute Reference, which lists both "arn" and a deprecated "id"
	// alias of the same ARN.

	TypeIdentity{
		// row-gen filed this evidence-only (a fold child of aws_db_instance
		// with no registry primaryIdentifier of its own). Reading the
		// provider's Import section directly: the documented import ID is
		// "DB Instance Identifier and IAM Role ARN separated by a comma", a
		// concrete composite of two arguments the Argument Reference marks
		// Required (db_instance_identifier, role_arn) — the same shape as
		// aws_rds_cluster_role_association above. The Attribute Reference
		// confirms "id" is exactly that composite, so id is claimed as an
		// identity source.
		Type: "aws_db_instance_role_association",
		Components: []Component{
			attr("db_instance_identifier"),
			sep(","),
			attr("role_arn"),
		},
		ImportSyntax:  "DBINSTANCEIDENTIFIER,ROLEARN",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBShardGroupIdentifier], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_rds_shard_group.example
		// example-shard-group) and its Argument Reference
		// ("db_shard_group_identifier" is Required, with no
		// Terraform-assigned fallback, unlike every *_group name above). Its
		// Attribute Reference exports no "id" attribute at all (only arn,
		// db_shard_group_resource_id, endpoint), so nothing is claimed as an
		// identity source beyond the argument itself.
		Type:          "aws_rds_shard_group",
		Components:    []Component{attr("db_shard_group_identifier")},
		ImportSyntax:  "DB_SHARD_GROUP_IDENTIFIER",
		IdentityAttrs: []string{"db_shard_group_identifier"},
	},
)

func init() { registerCohortTable(identityTableRds) }
