// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableData is the data cohort's slice of [DefaultTable]:
// the identity rows the data ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableData = buildTable(
	// ---- Registry-ratified (#40, #44): fourth batch, data plane (Kinesis,
	// ---- KinesisFirehose, Glue, Athena; issue #65's recipe) ---------------
	//
	// Same pipeline as the earlier batches: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour (its "Import" section
	// at the pinned v6.58.0 tag). Several rows also needed the pinned
	// provider's own wire schema, read directly with `terraform providers
	// schema -json` against the real hashicorp/aws 6.58.0 binary, to settle
	// whether a value row-gen or the website docs named is actually a
	// settable configuration argument — the same category of check that
	// caught the earlier batches' aws_sqs_queue and aws_sns_topic_policy
	// corrections, applied here to more rows than any prior batch needed.
	// Cohort estate: live/e2e/estates/data.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_athena_named_query: row-gen proposed server-assigned via the
	//     registry's opaque "NamedQueryId", and the provider agrees — the
	//     documented import command uses the query ID, which no argument
	//     reconstructs. Unlike this table's singleton-per-account
	//     serverAssigned rows (aws_ecr_registry_policy and this batch's own
	//     aws_glue_data_catalog_encryption_settings below), a named query is
	//     not a singleton: many exist per workgroup, distinguished only by
	//     that opaque ID. The pinned provider's wire schema confirms the
	//     type carries no tags argument and the provider ships no native
	//     list resource for it either, so none of the four admission paths
	//     recovers a pre-existing instance — live/survey-full.json's own
	//     classifier reaches the same "moves to Ops" verdict for exactly
	//     this reason.
	//   - aws_glue_schema: row-gen proposed server-assigned via the
	//     registry's "Arn", and the provider's documented import command
	//     confirms an ARN
	//     (arn:aws:glue:REGION:ACCOUNT:schema/REGISTRY_NAME/SCHEMA_NAME) —
	//     but unlike aws_glue_registry below, REGISTRY_NAME is not
	//     reconstructable: the resource's only registry reference argument
	//     is registry_arn (the parent's full ARN string), and the pinned
	//     provider's wire schema shows registry_name is computed-only,
	//     never settable. Building this identity would mean parsing a bare
	//     name out of a parent ARN string, a component this table's
	//     vocabulary does not have (sep, attr and cloud concatenate; none
	//     of them extract a substring of another component). Left out
	//     rather than guessed.
	//   - aws_glue_partition: row-gen classed this evidence-only (the
	//     registry's own primaryIdentifier rule does not fire on it at
	//     all). The provider's documented import command
	//     (CATALOG_ID:DATABASE_NAME:TABLE_NAME:PARTITION_VALUE1#PARTITION_VALUE2#...)
	//     is otherwise fully reconstructable — the same account-derived
	//     catalog_id as aws_glue_catalog_table above, plus its required
	//     database_name and table_name — except for partition_values
	//     itself, a required list(string) argument the pinned provider's
	//     wire schema confirms is joined into the import string with "#".
	//     This table's Components vocabulary has no list-join primitive
	//     (every component reads one scalar argument), so this one is left
	//     out rather than guessed, the same standard of care as the schema
	//     rejection above.
	//
	// Out of this batch's named scope, not rejected on the merits:
	//
	//   - aws_kinesis_resource_policy: row-gen proposed this cleanly
	//     (client-named via the provider's own identity schema,
	//     required_for_import=[resource_arn]) but issue #65's recipe scopes
	//     this batch's Kinesis slice to streams and consumers.
	//   - aws_kinesis_analytics_application, aws_kinesisanalyticsv2_application,
	//     aws_kinesis_video_stream: different CFN services (KinesisAnalytics,
	//     KinesisAnalyticsV2, KinesisVideo), not Kinesis or KinesisFirehose.
	//   - aws_glue_catalog: a distinct, newer top-level "Catalog" resource
	//     (federated catalog registration) easily confused with
	//     aws_glue_catalog_database/aws_glue_catalog_table by name alone;
	//     issue #65's recipe names only "catalog database/table". Its own
	//     identity schema (required=[name]) would make it a clean, direct
	//     client-named row for a future batch.
	//   - aws_glue_catalog_table_optimizer, aws_glue_dev_endpoint,
	//     aws_glue_security_configuration, aws_glue_user_defined_function,
	//     aws_glue_workflow, aws_glue_data_quality_ruleset: all evidence-only
	//     per row-gen (no pastable row), outside this batch's named scope.
	//     aws_glue_catalog_table_optimizer in particular looks fully
	//     reconstructable on inspection (catalog_id,database_name,table_name,type,
	//     comma-separated, no list-join needed) — a plausible pickup for a
	//     future batch.
	//   - aws_athena_capacity_reservation, aws_athena_prepared_statement:
	//     needs-hand-separator and evidence-only respectively per row-gen;
	//     issue #65's recipe names only workgroups, data catalogs and named
	//     queries.

	TypeIdentity{
		// registry.json classed this evidence-only (argument "name" GUESSED
		// from the CFN property name, not backed by an identity schema or
		// the carve seed — v6.58.0 ships no identity schema for this type).
		// Independently confirmed against the provider's documented import
		// command (terraform import aws_kinesis_stream.example
		// example-stream) and Attribute Reference ("arn - ... (same as
		// id)"): the pinned provider's wire schema confirms name is the
		// sole required argument and id/arn are optional+computed.
		Type:          "aws_kinesis_stream",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	serverAssigned("aws_kinesis_stream_consumer",
		"Kinesis mints the stream consumer's ARN at create time, embedding a creation timestamp it assigns itself; the name argument names the consumer but not one registration of it against a stream.",
		"STREAMARN/consumer/CONSUMERNAME:TIMESTAMP", "arn", "id"),

	TypeIdentity{
		// row-gen proposed aws_kinesis_firehose_delivery_stream client-named
		// via "arn" (registry primaryIdentifier=[DeliveryStreamName], but
		// the argument line row-gen actually emitted came from the
		// provider's own identity schema, live/survey-full.json:
		// required_for_import=[arn]) — right about which value the provider
		// needs, wrong about it being a plain configuration argument: the
		// pinned provider's wire schema shows arn is computed-only, never
		// settable, so attr("arn") would find nothing in a real resource
		// block. The provider's documented import command (terraform import
		// aws_kinesis_firehose_delivery_stream.example
		// arn:aws:firehose:us-east-1:123456789012:deliverystream/example-delivery-stream)
		// confirms the ARN is this predictable account-derived shape, the
		// same correction the messaging batch made to aws_sqs_queue above:
		// reconstructed from the required "name" argument plus the run's
		// own region and account, not read as a literal "arn" argument.
		Type: "aws_kinesis_firehose_delivery_stream",
		Components: []Component{
			inAttr("arn", sep("arn:aws:firehose:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":deliverystream/")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:firehose:REGION:ACCOUNT:deliverystream/NAME",
		IdentityAttrs: []string{"arn", "id"},
	},

	TypeIdentity{
		// row-gen classed this evidence-only (argument "database_name"
		// GUESSED from the CFN property name — v6.58.0 ships no identity
		// schema for this type). Independently confirmed against the
		// provider's documented import command (terraform import
		// aws_glue_catalog_database.database 123456789012:my_database) and
		// its Argument/Attribute Reference: the real argument is "name",
		// not "database_name", and "catalog_id" is optional, defaulting to
		// the run's own AWS account ID when omitted — an account-derived
		// shape, not a plain client-named row. The pinned provider's wire
		// schema confirms catalog_id is optional+computed and id is
		// "Catalog ID and name of the database".
		Type: "aws_glue_catalog_database",
		Components: []Component{
			inAttr("id", cloud(CloudAccountID)),
			inAttr("id", sep(":")),
			inAttr("id", attr("name")),
		},
		ImportSyntax:  "CATALOG_ID:NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque,
		// undocumented "Id" (AWS::Glue::Table's primaryIdentifier) —
		// rejected as a proposal, the same registry/provider mismatch shape
		// the earlier batches' rejections found. The provider's documented
		// import command (terraform import aws_glue_catalog_table.MyTable
		// 123456789012:MyDatabase:MyTable) and its Attribute Reference
		// ("id - Catalog ID, database name, and table name, separated by
		// colons") show the identity is fully reconstructed from
		// configuration: the same account-derived catalog_id as
		// aws_glue_catalog_database just above, plus the required, already
		// admitted parent's database_name and this resource's own required
		// name. Untaggable — the pinned provider's wire schema carries no
		// tags argument for this type at all.
		Type: "aws_glue_catalog_table",
		Components: []Component{
			inAttr("id", cloud(CloudAccountID)),
			inAttr("id", sep(":")),
			inAttr("id", attr("database_name")),
			inAttr("id", sep(":")),
			inAttr("id", attr("name")),
		},
		ImportSyntax:  "CATALOG_ID:DATABASE_NAME:TABLE_NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed aws_glue_registry server-assigned via the
		// registry's opaque "Arn" — right that the provider's documented
		// import command (terraform import aws_glue_registry.example
		// arn:aws:glue:us-west-2:123456789012:registry/example) uses the
		// ARN, but the flat serverAssigned() template undersells it, the
		// same correction as aws_kinesis_firehose_delivery_stream above:
		// the ARN is the predictable
		// arn:aws:glue:REGION:ACCOUNT:registry/NAME shape, built from the
		// required "registry_name" argument plus the run's region and
		// account, not an opaque server mint.
		Type: "aws_glue_registry",
		Components: []Component{
			inAttr("arn", sep("arn:aws:glue:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":registry/")),
			inAttr("arn", attr("registry_name")),
		},
		ImportSyntax:  "arn:aws:glue:REGION:ACCOUNT:registry/REGISTRY_NAME",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named. Confirmed against
		// the provider's own identity schema (live/survey-full.json:
		// required_for_import=[name]) and against the documented import
		// command (terraform import aws_glue_job.example example), which
		// sets id to the job name.
		Type:          "aws_glue_job",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, argument sourced
		// from live/import-grammar.json. Confirmed against the provider's
		// documented import command (terraform import
		// aws_glue_crawler.MyJob MyJob) and Attribute Reference ("id -
		// Crawler name").
		Type:          "aws_glue_crawler",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen classed this evidence-only (no identity schema in
		// v6.58.0). Independently confirmed against the provider's
		// documented import command (terraform import
		// aws_glue_connection.MyConnection 123456789012:MyConnection) and
		// Attribute Reference ("id - Catalog ID and name of the
		// connection"): the same account-derived catalog_id:name shape as
		// aws_glue_catalog_database above, not a plain client-named row.
		Type: "aws_glue_connection",
		Components: []Component{
			inAttr("id", cloud(CloudAccountID)),
			inAttr("id", sep(":")),
			inAttr("id", attr("name")),
		},
		ImportSyntax:  "CATALOG_ID:NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed aws_glue_classifier server-assigned via the
		// registry's opaque "Id" (AWS::Glue::Classifier's primaryIdentifier,
		// with an empty createOnlyProperties list — the polymorphic
		// Grok/XML/JSON/CSV classifier shapes defeat the registry's own
		// top-level modeling) — rejected as a proposal, the same
		// registry/provider mismatch shape as aws_lambda_alias and
		// aws_cloudwatch_alarm_mute_rule. The provider's documented import
		// command (terraform import aws_glue_classifier.MyClassifier
		// MyClassifier) and Attribute Reference ("id - Name of the
		// classifier") show the identity is the required "name" argument,
		// already in configuration. Untaggable — the pinned provider's wire
		// schema carries no tags argument for this type at all.
		Type:          "aws_glue_classifier",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen's argument line named the literal doc token "CATALOG-ID",
		// not a real configuration argument — evidence-only, never a
		// pastable row. The provider's real argument is "catalog_id",
		// optional, defaulting to the run's own AWS account ID when
		// omitted (the same default aws_glue_catalog_database and
		// aws_glue_connection above share), and its Attribute Reference
		// sets id to that same catalog ID — a singleton-per-account shape
		// like the IAM/ECR batch's aws_ecr_registry_policy, not a
		// discovered one. Untaggable — the pinned provider's wire schema
		// carries no tags argument for this type at all.
		Type:          "aws_glue_data_catalog_encryption_settings",
		Components:    []Component{inAttr("id", cloud(CloudAccountID))},
		ImportSyntax:  "CATALOG_ID",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, argument sourced
		// from live/import-grammar.json. Confirmed against the provider's
		// documented import command (terraform import
		// aws_glue_trigger.MyTrigger MyTrigger) and Attribute Reference
		// ("id - Trigger name").
		Type:          "aws_glue_trigger",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	serverAssigned("aws_glue_ml_transform",
		"Glue assigns the ML transform's ID (tfm-…) at create time; the name argument names the transform but the API accepts only the ID as an identity.",
		"tfm-ID", "id"),

	TypeIdentity{
		// row-gen classed this evidence-only (argument "name" GUESSED from
		// the CFN property name — v6.58.0 ships no identity schema for this
		// type). Independently confirmed against the provider's documented
		// import command (terraform import aws_athena_workgroup.example
		// example) and Attribute Reference ("id - Workgroup name").
		Type:          "aws_athena_workgroup",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen classed this evidence-only (argument "name" GUESSED from
		// the CFN property name — v6.58.0 ships no identity schema for this
		// type). Independently confirmed against the provider's documented
		// import command (terraform import aws_athena_data_catalog.example
		// example-data-catalog) and Attribute Reference ("id - Name of the
		// data catalog").
		Type:          "aws_athena_data_catalog",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
)

func init() { registerCohortTable(identityTableData) }
