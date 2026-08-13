// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesDynamodbElasticache is the dynamodb-elasticache cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesDynamodbElasticache = map[string]typeOverride{
	"aws_dynamodb_resource_policy": {
		Reasons: []string{
			`schema requires "resource_arn" as a plain string, but the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); the generic identity-argument placeholder ("tofu-<cohort>-cohort-...") is not one. schema also requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "is not valid JSON string format"); the generic string placeholder is not JSON`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			tableARN := fmt.Sprintf(`"arn:aws:dynamodb:us-east-1:000000000000:table/tofu-%s-cohort-app"`, g.cohort)
			body.SetAttributeRaw("resource_arn", exprTokens(tableARN))
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "dynamodb:DescribeTable"
      Resource  = %s
    }]
  })`, tableARN)))
		},
	},
	"aws_elasticache_cluster": {
		Reasons: []string{
			`schema requires only cluster_id; the provider also requires exactly one of engine/replication_group_id (validate: "one of engine,replication_group_id must be specified") and, once engine is chosen, node_type, num_cache_nodes and parameter_group_name become required in practice too (validate: "Missing required argument"). engine is set to "memcached" rather than the more familiar "redis": AWS's CreateCacheCluster API (which this resource calls directly, unlike aws_elasticache_replication_group) only accepts the redis/valkey engines when joining an existing replication group, confirmed by floci's own emulation of that same real-AWS rule (apply: "InvalidParameterValue: Engine must be 'memcached'. For Redis/Valkey use CreateReplicationGroup.") — not a floci gap, the standalone-cluster shape genuinely requires memcached. cluster_id is also length-limited to 50 characters (validate: "expected length ... to be in the range (1 - 50)"), and this cohort's own name ("dynamodb-elasticache") makes the generic tofu-<cohort>-cohort-<type> placeholder 54 characters - shortened here to a value that still names the cohort and the type`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cluster_id", exprTokens(`"tofu-ddb-ec-cluster"`))
			body.SetAttributeRaw("engine", exprTokens(`"memcached"`))
			body.SetAttributeRaw("node_type", exprTokens(`"cache.t3.micro"`))
			body.SetAttributeRaw("num_cache_nodes", exprTokens(`1`))
			body.SetAttributeRaw("parameter_group_name", exprTokens(`"default.memcached1.6"`))
		},
	},
	"aws_elasticache_replication_group": {
		Reasons: []string{
			`schema requires only replication_group_id; the provider also requires node_type in practice once no global_replication_group_id is set (validate: "\"node_type\" is required unless \"global_replication_group_id\" is set"), and engine defaults to redis but is set explicitly here for clarity. replication_group_id is also length-limited to 40 characters (validate: "expected length ... to be in the range (1 - 40)"), and this cohort's own name ("dynamodb-elasticache") makes the generic tofu-<cohort>-cohort-<type> placeholder 58 characters - shortened here to a value that still names the cohort and the type`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("replication_group_id", exprTokens(`"tofu-ddb-ec-replgrp"`))
			body.SetAttributeRaw("engine", exprTokens(`"redis"`))
			body.SetAttributeRaw("node_type", exprTokens(`"cache.t3.micro"`))
		},
	},
	"aws_elasticache_user": {
		Reasons: []string{
			`engine is a required argument the schema types as an unconstrained string, but the provider validates it against a closed enum (validate: "expected engine to be one of [\"redis\" \"valkey\"]"); the generic placeholder string is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine", exprTokens(`"redis"`))
		},
	},
	"aws_elasticache_user_group": {
		Reasons: []string{
			`engine is a required argument the schema types as an unconstrained string, but the provider validates it against a closed enum (validate: "expected engine to be one of [\"redis\" \"valkey\"]"); the generic placeholder string is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine", exprTokens(`"redis"`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesDynamodbElasticache) }
