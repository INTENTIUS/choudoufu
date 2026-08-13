// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The dynamodb-elasticache cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableDynamodbElasticache = []string{
	// Registry-ratified DynamoDB periphery and ElastiCache batch (#40,
	// #44, issue #65). Taggable per the real provider's documented
	// Argument Reference for each type.
	"aws_elasticache_cluster",
	"aws_elasticache_parameter_group",
	"aws_elasticache_replication_group",
	"aws_elasticache_serverless_cache",
	"aws_elasticache_subnet_group",
	"aws_elasticache_user",
	"aws_elasticache_user_group",
}

var untaggableDynamodbElasticache = []string{
	// Registry-ratified DynamoDB periphery and ElastiCache batch (#40,
	// #44, issue #65): both types' Argument Reference names no tags
	// block at all. See live/e2e/estates/dynamodb-elasticache/README.md,
	// "Untaggable types".
	"aws_dynamodb_global_table",
	"aws_dynamodb_resource_policy",
}

func init() {
	registerCohortStamp(taggableDynamodbElasticache, untaggableDynamodbElasticache, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified DynamoDB periphery and ElastiCache batch (#40,
			// #44, issue #65). Taggable/untaggable per the real provider's
			// documented Argument Reference for each type: the two DynamoDB
			// types carry no tags argument at all, the seven ElastiCache types
			// all do.
			"aws_dynamodb_global_table":         untaggedSchema("id", "name"),
			"aws_dynamodb_resource_policy":      untaggedSchema("id", "resource_arn", "policy"),
			"aws_elasticache_cluster":           taggedSchema("id", "arn", "cluster_id", "engine"),
			"aws_elasticache_parameter_group":   taggedSchema("id", "arn", "name", "family"),
			"aws_elasticache_replication_group": taggedSchema("id", "arn", "replication_group_id"),
			"aws_elasticache_serverless_cache":  taggedSchema("id", "arn", "name", "engine"),
			"aws_elasticache_subnet_group":      taggedSchema("id", "arn", "name"),
			"aws_elasticache_user":              taggedSchema("id", "arn", "user_id", "user_name"),
			"aws_elasticache_user_group":        taggedSchema("id", "arn", "user_group_id"),
		})
	})
}
