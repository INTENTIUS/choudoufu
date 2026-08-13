// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The databases cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableDatabases = []string{
	// Registry-ratified databases batch (#40, #44, issue #65): every
	// ratified type in this batch except the three OpenSearchServerless
	// policy types below (untaggableAdmittedTypes) carries a top-level
	// tags argument in the pinned provider's own wire schema, confirmed
	// against the generated live/e2e/estates/databases fixture. See
	// live/e2e/estates/databases/README.md.
	"aws_redshift_cluster",
	"aws_redshift_parameter_group",
	"aws_redshift_subnet_group",
	"aws_redshift_snapshot_schedule",
	"aws_redshiftserverless_namespace",
	"aws_redshiftserverless_workgroup",
	"aws_opensearch_domain",
	"aws_elasticsearch_domain",
	"aws_opensearchserverless_collection",
	"aws_opensearchserverless_collection_group",
	"aws_neptune_cluster_parameter_group",
	"aws_neptune_parameter_group",
	"aws_neptune_subnet_group",
	"aws_docdb_event_subscription",
	"aws_docdbelastic_cluster",
	"aws_timestreamwrite_database",
	"aws_timestreamwrite_table",
	"aws_timestreaminfluxdb_db_cluster",
	"aws_timestreaminfluxdb_db_instance",
	"aws_timestreamquery_scheduled_query",
	"aws_qldb_ledger",
	"aws_memorydb_acl",
	"aws_memorydb_cluster",
	"aws_memorydb_multi_region_cluster",
	"aws_memorydb_parameter_group",
	"aws_memorydb_user",
	"aws_memorydb_subnet_group",
	"aws_keyspaces_keyspace",
	"aws_keyspaces_table",
}

var untaggableDatabases = []string{
	// Registry-ratified databases batch (#40, #44, issue #65): the
	// three OpenSearchServerless policy types (access, lifecycle,
	// security) carry only a name/type/policy document, the same
	// untaggable shape as aws_sns_topic_policy and
	// aws_sqs_queue_policy above, confirmed against the generated
	// live/e2e/estates/databases fixture. See
	// live/e2e/estates/databases/README.md, "Untaggable types".
	"aws_opensearchserverless_access_policy",
	"aws_opensearchserverless_lifecycle_policy",
	"aws_opensearchserverless_security_policy",
}

func init() {
	registerCohortStamp(taggableDatabases, untaggableDatabases, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified databases batch (#40, #44, issue #65).
			// Taggable/untaggable per the real provider's documented Argument
			// Reference for each type, confirmed against the generated
			// live/e2e/estates/databases fixture: the three OpenSearchServerless
			// policy types (access, lifecycle, security) carry only a
			// name/type/policy document, the same untaggable shape as
			// aws_sns_topic_policy above; every other type in this batch is
			// taggable.
			"aws_redshift_cluster":                      taggedSchema("id", "arn", "cluster_identifier"),
			"aws_redshift_parameter_group":              taggedSchema("id", "arn", "name"),
			"aws_redshift_subnet_group":                 taggedSchema("id", "arn", "name"),
			"aws_redshift_snapshot_schedule":            taggedSchema("id", "arn", "identifier"),
			"aws_redshiftserverless_namespace":          taggedSchema("id", "arn", "namespace_name"),
			"aws_redshiftserverless_workgroup":          taggedSchema("id", "arn", "workgroup_name"),
			"aws_opensearch_domain":                     taggedSchema("id", "arn", "domain_name"),
			"aws_elasticsearch_domain":                  taggedSchema("id", "arn", "domain_name"),
			"aws_opensearchserverless_collection":       taggedSchema("id", "arn", "name"),
			"aws_opensearchserverless_collection_group": taggedSchema("id", "arn", "name"),
			"aws_opensearchserverless_access_policy":    untaggedSchema("id", "name", "type", "policy"),
			"aws_opensearchserverless_lifecycle_policy": untaggedSchema("id", "name", "type", "policy"),
			"aws_opensearchserverless_security_policy":  untaggedSchema("id", "name", "type", "policy"),
			"aws_neptune_cluster_parameter_group":       taggedSchema("id", "arn", "name"),
			"aws_neptune_parameter_group":               taggedSchema("id", "arn", "name"),
			"aws_neptune_subnet_group":                  taggedSchema("id", "arn", "name"),
			"aws_docdb_event_subscription":              taggedSchema("id", "arn", "name"),
			"aws_docdbelastic_cluster":                  taggedSchema("id", "arn", "name"),
			"aws_timestreamwrite_database":              taggedSchema("id", "arn", "database_name"),
			"aws_timestreamwrite_table":                 taggedSchema("id", "arn", "database_name", "table_name"),
			"aws_timestreaminfluxdb_db_cluster":         taggedSchema("id", "arn", "name"),
			"aws_timestreaminfluxdb_db_instance":        taggedSchema("id", "arn", "name"),
			"aws_timestreamquery_scheduled_query":       taggedSchema("id", "arn", "name"),
			"aws_qldb_ledger":                           taggedSchema("id", "arn", "name"),
			"aws_memorydb_acl":                          taggedSchema("id", "arn", "name"),
			"aws_memorydb_cluster":                      taggedSchema("id", "arn", "name"),
			"aws_memorydb_multi_region_cluster":         taggedSchema("id", "arn", "multi_region_cluster_name"),
			"aws_memorydb_parameter_group":              taggedSchema("id", "arn", "name"),
			"aws_memorydb_user":                         taggedSchema("id", "arn", "user_name"),
			"aws_memorydb_subnet_group":                 taggedSchema("id", "arn", "name"),
			"aws_keyspaces_keyspace":                    taggedSchema("id", "arn", "name"),
			"aws_keyspaces_table":                       taggedSchema("id", "arn", "keyspace_name", "table_name"),
		})
	})
}
