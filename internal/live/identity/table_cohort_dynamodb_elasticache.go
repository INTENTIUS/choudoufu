// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableDynamodbElasticache is the dynamodb-elasticache cohort's slice of [DefaultTable]:
// the identity rows the dynamodb-elasticache ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableDynamodbElasticache = buildTable(
	// ---- Registry-ratified (#40, #44): fourth batch, DynamoDB periphery
	// ---- and ElastiCache (issue #65) ------------------------------------
	//
	// Same pipeline as the three batches above: every row started as a
	// tools/row-gen proposal or evidence-only finding from live/registry.json
	// and live/mapping.json, cross-checked against the AWS provider's
	// documented import behaviour (its "Import" section, fetched from the
	// provider's own website/docs/r/ source at the pinned v6.58.0 tag) and,
	// for two corrections below, against live/import-grammar.json's
	// scraped import grammar directly. Cohort estate:
	// live/e2e/estates/dynamodb-elasticache.
	//
	// DynamoDB's row-gen section (6 types) is almost entirely the
	// already-admitted aws_dynamodb_table: the other five are either
	// evidence-only or property-children folded onto AWS::DynamoDB::Table
	// with no CFN type of their own, so row-gen's primaryIdentifier
	// analysis never runs on four of them at all. There is no separate
	// DynamoDB "backup" resource type in the provider to propose or
	// reject — point-in-time recovery is an argument on aws_dynamodb_table
	// itself, not a standalone managed resource, so there was nothing here
	// for a backup row to be.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_elasticache_global_replication_group: row-gen proposed
	//     server-assigned via the registry's GlobalReplicationGroupId, and
	//     unlike the two corrections below, the provider agrees with the
	//     shape, not just the argument. Its own Argument Reference has no
	//     global_replication_group_id argument at all — the two Required
	//     arguments are global_replication_group_id_suffix and
	//     primary_replication_group_id — and its Attribute Reference
	//     exports global_replication_group_id as a separate, computed
	//     field: AWS prepends its own region-derived code to the
	//     configured suffix (the documented import example,
	//     okuqm-global-replication-group-1, is not a string any
	//     configuration sets). live/survey-full.json's own automated pass
	//     reaches "moves to Ops" for the same reason (untaggable, no
	//     native list resource, no identity schema in v6.58.0) — and
	//     unlike aws_ecr_registry_policy and its two account-singleton
	//     siblings in the IAM/ECR batch above, this type is not a
	//     one-per-account singleton either: many global replication
	//     groups can exist per account, so there is no deterministic
	//     fallback identity to read without a list. No admission path
	//     recovers it.
	//
	// Deferred as composite import IDs this batch does not hand-write, the
	// same restraint the Lambda and messaging batches above already
	// state (both times over a row-gen-proposed server-assigned guess;
	// these four are over row-gen's evidence-only and needs-hand-separator
	// output instead, now checkable because live/import-grammar.json's
	// scrape gives every one of them a confirmed separator character and
	// argument order — issue #65 notes this is why needs-hand-separator is
	// "now largely resolvable" going forward). Confirmed, not merely
	// guessed, and left out anyway: adding new composite-separator rows to
	// a registry-ratified section is a bigger methodological step than
	// this batch takes, so a future batch can lift these four rows
	// directly rather than re-deriving them:
	//
	//   - aws_dynamodb_global_secondary_index: table_name and index_name,
	//     both Required arguments and both named in the provider's own
	//     identity schema (required_for_import=[index_name, table_name]),
	//     joined by a comma (terraform import
	//     aws_dynamodb_global_secondary_index.example
	//     'example-table,example-index'). Parent-derived off the
	//     already-admitted aws_dynamodb_table.
	//   - aws_dynamodb_kinesis_streaming_destination: table_name and
	//     stream_arn, both Required arguments, joined by a comma
	//     (terraform import
	//     aws_dynamodb_kinesis_streaming_destination.example
	//     example,arn:aws:kinesis:us-east-1:111122223333:exampleStreamName).
	//     Docs-tier evidence only — v6.58.0 ships no identity schema for
	//     this type — but the Argument Reference and the import command
	//     agree on both required arguments and the separator.
	//   - aws_elasticache_user_group_association: user_group_id and
	//     user_id, both Required arguments, joined by a comma (terraform
	//     import aws_elasticache_user_group_association.example
	//     userGoupId1,userId). Parent-derived off
	//     aws_elasticache_user_group and aws_elasticache_user, both
	//     ratified below.
	//   - aws_dynamodb_contributor_insights: table_name (Required) and
	//     index_name (Optional), joined into
	//     name:TABLE_NAME/index:INDEX_NAME plus the account number
	//     (terraform import aws_dynamodb_contributor_insights.test
	//     name:ExampleTableName/index:ExampleIndexName/123456789012). Left
	//     out for a second reason beyond the separator: index_name is
	//     optional, and expressing "this literal segment only when an
	//     argument is set, omitted otherwise" is a component this table's
	//     vocabulary does not have — the same gap that kept the messaging
	//     batch's aws_cloudwatch_event_rule out for its optional
	//     event_bus_name.

	TypeIdentity{
		// row-gen classified this client-named (registry primaryIdentifier
		// TableName, in createOnlyProperties and not readOnlyProperties)
		// but could not paste a row: the argument name was only GUESSED
		// (snake_cased from "TableName") because v6.58.0 ships no identity
		// schema for this type and live/import-grammar.json's own scrape
		// shows no parsed argument either — its Import section text says
		// only "using the global table name" with no import-block
		// argument list to lift a name from. Confirmed directly against
		// the provider's own Argument Reference (fetched from the pinned
		// v6.58.0 docs source): this AWS DynamoDB Global Table (V1,
		// deprecated in the provider's own docs in favor of
		// aws_dynamodb_table's replica block, but still real and
		// importable in v6.58.0) declares exactly one required argument,
		// name, and its documented import command sets id to that same
		// name verbatim (terraform import aws_dynamodb_global_table.MyTable
		// MyTable). No tags argument exists in the Argument Reference at
		// all — survey-full.json's own taggable:false signal agrees — so
		// this type is untaggable, the same "moves to Ops" default the
		// automated classifier falls back to whenever it has neither an
		// identity schema nor a tags argument to reason from
		// (aws_ecs_cluster's docs-tier exception is the same shape, minus
		// the tags argument that tips it to marker instead). Client-named
		// admission needs neither: the name is already in configuration,
		// and no list or tag step is any part of path 3.
		Type:          "aws_dynamodb_global_table",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// Never a row-gen proposal at all: live/mapping.json folds this
		// type onto AWS::DynamoDB::Table (via==fold) rather than mapping
		// it to its own CFN type, so row-gen's primaryIdentifier analysis
		// never runs on it and it prints only as an evidence-only
		// property-child. Verified independently instead, against the
		// provider's real identity schema (live/survey-full.json:
		// required_for_import=[resource_arn], no optional-for-import
		// attribute beyond the account/region context pair) and its
		// documented import command (terraform import
		// aws_dynamodb_resource_policy.example
		// arn:aws:dynamodb:us-east-1:1234567890:table/my-table) — a single
		// argument, resource_arn, the parent table's own ARN. The same
		// named-singleton-child shape as aws_sns_topic_policy and
		// aws_sqs_queue_policy from the messaging batch above, keyed on
		// the parent's "arn"-shaped argument rather than a bucket's
		// "bucket". live/survey-full.json's own automated pass reaches
		// the same verdict (path: parent-derived, admission: schema)
		// independently of this batch's hand check.
		Type:          "aws_dynamodb_resource_policy",
		Components:    []Component{attr("resource_arn")},
		ImportSyntax:  "RESOURCE_ARN",
		IdentityAttrs: []string{"resource_arn"},
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[ClusterName], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; the argument came from
		// live/import-grammar.json rather than the provider's identity
		// schema (v6.58.0 ships none for this type). Confirmed against
		// the provider's own Argument Reference (cluster_id, Required)
		// and its documented import command (terraform import
		// aws_elasticache_cluster.my_cluster my_cluster).
		Type:          "aws_elasticache_cluster",
		Components:    []Component{attr("cluster_id")},
		ImportSyntax:  "CLUSTER_ID",
		IdentityAttrs: []string{"cluster_id"},
	},
	TypeIdentity{
		// row-gen's registry rule read CacheParameterGroupName as
		// primaryIdentifier ⊆ readOnlyProperties and would have proposed
		// server-assigned, but issue #55's import-grammar demotion caught
		// it first: live/import-grammar.json shows the documented import
		// ID is argument-composed ("redis-params"), so row-gen printed
		// evidence-only rather than a wrong pastable row — the same
		// automated catch that already protected the Lambda batch's
		// aws_lambda_alias-shaped mistakes from ever being proposed
		// clean. Confirmed against the provider's real Argument
		// Reference: name and family are both Required, id equals name
		// verbatim per the Attribute Reference, and tags is a real
		// optional argument (survey-full.json's own taggable:true signal
		// agrees, classing this "marker" on raw signals alone — the
		// identity is nonetheless the name argument, confirmed by the
		// documented import command: terraform import
		// aws_elasticache_parameter_group.default redis-params).
		Type:          "aws_elasticache_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ReplicationGroupId], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (replication_group_id, Required) and its
		// documented import command (terraform import
		// aws_elasticache_replication_group.my_replication_group
		// replication-group-1).
		Type:          "aws_elasticache_replication_group",
		Components:    []Component{attr("replication_group_id")},
		ImportSyntax:  "REPLICATION_GROUP_ID",
		IdentityAttrs: []string{"replication_group_id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ServerlessCacheName], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (name, Required) and its documented import
		// command (terraform import
		// aws_elasticache_serverless_cache.my_cluster my_cluster).
		Type:          "aws_elasticache_serverless_cache",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[CacheSubnetGroupName], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (name, Required) and its documented import
		// command (terraform import aws_elasticache_subnet_group.bar
		// tf-test-cache-subnet).
		Type:          "aws_elasticache_subnet_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[UserId], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (user_id, Required, alongside access_string,
		// engine and user_name) and its documented import command
		// (terraform import aws_elasticache_user.my_user userId1).
		Type:          "aws_elasticache_user",
		Components:    []Component{attr("user_id")},
		ImportSyntax:  "USER_ID",
		IdentityAttrs: []string{"user_id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[UserGroupId], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (user_group_id, Required, alongside engine)
		// and its documented import command (terraform import
		// aws_elasticache_user_group.my_user_group userGoupId1).
		Type:          "aws_elasticache_user_group",
		Components:    []Component{attr("user_group_id")},
		ImportSyntax:  "USER_GROUP_ID",
		IdentityAttrs: []string{"user_group_id"},
	},
)

func init() { registerCohortTable(identityTableDynamodbElasticache) }
