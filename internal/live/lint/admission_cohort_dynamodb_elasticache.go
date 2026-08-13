// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesDynamodbElasticache is the dynamodb-elasticache cohort's slice of [admittedTypesV0]:
// the types the dynamodb-elasticache ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesDynamodbElasticache = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): fourth batch, DynamoDB periphery
	// ---- and ElastiCache (issue #65). Same tools/row-gen pipeline as the
	// ---- three batches above, cross-checked against the AWS provider's
	// ---- documented import behaviour (its "Import" section, fetched from
	// ---- the provider's own website/docs/r/ source at the pinned v6.58.0
	// ---- tag) and, where row-gen's own registry evidence was too weak to
	// ---- paste, against live/import-grammar.json's scraped import
	// ---- grammar rows — see internal/live/identity/table.go for the
	// ---- per-type evidence and for the rows this batch rejected or
	// ---- deferred. Cohort estate: live/e2e/estates/dynamodb-elasticache.
	//
	// DynamoDB's row-gen section is nearly empty beyond the already-admitted
	// aws_dynamodb_table (6 types total; 4 are property-children folded
	// onto AWS::DynamoDB::Table with no pastable row of their own). Of
	// those, only aws_dynamodb_resource_policy's real shape is simple
	// enough to hand-verify within this batch's discipline; the other
	// three are composite import IDs this batch defers rather than
	// hand-guesses — see internal/live/identity/table.go. DynamoDB has no
	// separate "backup" resource type in the provider at all:
	// aws_dynamodb_table's own point_in_time_recovery block covers that
	// ground, not a standalone resource, so there was nothing here for a
	// backup row to be.
	"aws_dynamodb_global_table":    {},
	"aws_dynamodb_resource_policy": {},

	// ElastiCache: seven of row-gen's nine proposed/correctable types land
	// here — the six client-named singular resources (cluster, replication
	// group, serverless cache, subnet group, user, user group) plus the
	// parameter group, corrected from row-gen's evidence-only demotion.
	// aws_elasticache_global_replication_group is rejected outright (not a
	// row-gen misclassification — the identity genuinely cannot be
	// recovered, see table.go), and
	// aws_elasticache_user_group_association is deferred as a composite
	// this batch does not hand-write.
	"aws_elasticache_cluster":           {},
	"aws_elasticache_parameter_group":   {},
	"aws_elasticache_replication_group": {},
	"aws_elasticache_serverless_cache":  {},
	"aws_elasticache_subnet_group":      {},
	"aws_elasticache_user":              {},
	"aws_elasticache_user_group":        {},
}

func init() { registerCohortAdmitted(admittedTypesDynamodbElasticache) }
