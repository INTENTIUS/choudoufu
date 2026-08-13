// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesDatabases is the databases cohort's slice of [admittedTypesV0]:
// the types the databases ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesDatabases = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): sixth batch, databases beyond
	// ---- RDS/DynamoDB/ElastiCache (issue #65's own recipe: Redshift,
	// ---- OpenSearch/OpenSearchServerless, Neptune, DocDB, Timestream, QLDB,
	// ---- MemoryDB, Cassandra/Keyspaces). Same tools/row-gen pipeline as the
	// ---- batches above, cross-checked against live/import-grammar.json's
	// ---- scraped Import sections (the pinned v6.58.0 provider docs
	// ---- fetched directly) rather than accepted on the CFN registry's
	// ---- classification alone — several of these rows correct a row-gen
	// ---- "evidence-only" demotion the same way earlier batches corrected
	// ---- aws_sns_topic_policy and aws_qldb_ledger and aws_memorydb_subnet_group
	// ---- do here. Per-service scope is deliberately narrow, matching issue
	// ---- #65's own sub-lists rather than every row-gen proposal in each
	// ---- service: see internal/live/identity/table.go for the per-type
	// ---- evidence, the rejection, and the out-of-scope proposals this batch
	// ---- left for later. Cohort estate: live/e2e/estates/databases.
	"aws_redshift_cluster":                      {},
	"aws_redshift_parameter_group":              {},
	"aws_redshift_subnet_group":                 {},
	"aws_redshift_snapshot_schedule":            {},
	"aws_redshiftserverless_namespace":          {},
	"aws_redshiftserverless_workgroup":          {},
	"aws_opensearch_domain":                     {},
	"aws_elasticsearch_domain":                  {},
	"aws_opensearchserverless_collection":       {},
	"aws_opensearchserverless_collection_group": {},
	"aws_opensearchserverless_access_policy":    {},
	"aws_opensearchserverless_lifecycle_policy": {},
	"aws_opensearchserverless_security_policy":  {},
	"aws_neptune_cluster_parameter_group":       {},
	"aws_neptune_parameter_group":               {},
	"aws_neptune_subnet_group":                  {},
	"aws_docdb_event_subscription":              {},
	"aws_docdbelastic_cluster":                  {},
	"aws_timestreamwrite_database":              {},
	"aws_timestreamwrite_table":                 {},
	"aws_timestreaminfluxdb_db_cluster":         {},
	"aws_timestreaminfluxdb_db_instance":        {},
	"aws_timestreamquery_scheduled_query":       {},
	"aws_qldb_ledger":                           {},
	"aws_memorydb_acl":                          {},
	"aws_memorydb_cluster":                      {},
	"aws_memorydb_multi_region_cluster":         {},
	"aws_memorydb_parameter_group":              {},
	"aws_memorydb_user":                         {},
	"aws_memorydb_subnet_group":                 {},
	"aws_keyspaces_keyspace":                    {},
	"aws_keyspaces_table":                       {},
}

func init() { registerCohortAdmitted(admittedTypesDatabases) }
