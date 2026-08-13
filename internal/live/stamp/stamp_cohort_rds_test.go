// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The rds cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableRds = []string{
	// Registry-ratified RDS batch (#40, #44, issue #65's ratification
	// campaign). aws_db_instance_role_association, aws_db_proxy_default_target_group
	// and aws_rds_cluster_role_association are this batch's untaggable
	// types, below.
	"aws_db_event_subscription",
	"aws_db_instance",
	"aws_db_option_group",
	"aws_db_parameter_group",
	"aws_db_proxy",
	"aws_db_proxy_endpoint",
	"aws_db_subnet_group",
	"aws_rds_cluster",
	"aws_rds_cluster_instance",
	"aws_rds_cluster_parameter_group",
	"aws_rds_custom_db_engine_version",
	"aws_rds_global_cluster",
	"aws_rds_integration",
	"aws_rds_shard_group",
}

var untaggableRds = []string{
	// Registry-ratified RDS batch (#40, #44, issue #65's ratification
	// campaign): three types with no tags argument at all. See
	// live/e2e/estates/rds/README.md, "Untaggable types".
	"aws_db_instance_role_association",
	"aws_db_proxy_default_target_group",
	"aws_rds_cluster_role_association",
}

func init() {
	registerCohortStamp(taggableRds, untaggableRds, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified RDS batch (#40, #44, issue #65's ratification
			// campaign). Taggable/untaggable per the real provider's documented
			// Argument Reference for each type: aws_db_instance_role_association,
			// aws_db_proxy_default_target_group and
			// aws_rds_cluster_role_association carry no tags argument at all.
			"aws_db_event_subscription":         taggedSchema("id", "arn", "name", "sns_topic"),
			"aws_db_instance":                   taggedSchema("id", "identifier", "instance_class"),
			"aws_db_instance_role_association":  untaggedSchema("id", "db_instance_identifier", "feature_name", "role_arn"),
			"aws_db_option_group":               taggedSchema("id", "arn", "name", "engine_name", "major_engine_version"),
			"aws_db_parameter_group":            taggedSchema("id", "arn", "name", "family"),
			"aws_db_proxy":                      taggedSchema("id", "arn", "name", "engine_family", "role_arn"),
			"aws_db_proxy_default_target_group": untaggedSchema("id", "arn", "name", "db_proxy_name"),
			"aws_db_proxy_endpoint":             taggedSchema("id", "arn", "db_proxy_name", "db_proxy_endpoint_name"),
			"aws_db_subnet_group":               taggedSchema("id", "arn", "name", "subnet_ids"),
			"aws_rds_cluster":                   taggedSchema("id", "arn", "cluster_identifier", "engine"),
			"aws_rds_cluster_instance":          taggedSchema("id", "arn", "identifier", "cluster_identifier", "engine", "instance_class"),
			"aws_rds_cluster_parameter_group":   taggedSchema("id", "arn", "name", "family"),
			"aws_rds_cluster_role_association":  untaggedSchema("id", "db_cluster_identifier", "feature_name", "role_arn"),
			"aws_rds_custom_db_engine_version":  taggedSchema("arn", "engine", "engine_version"),
			"aws_rds_global_cluster":            taggedSchema("id", "arn", "global_cluster_identifier"),
			"aws_rds_integration":               taggedSchema("id", "arn", "integration_name", "source_arn", "target_arn"),
			"aws_rds_shard_group":               taggedSchema("arn", "db_shard_group_identifier", "db_cluster_identifier", "max_acu"),
		})
	})
}
