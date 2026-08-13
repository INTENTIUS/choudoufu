// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesRds is the rds cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesRds = map[string]typeOverride{
	"aws_db_instance": {
		Reasons: []string{
			`schema requires only identifier and instance_class; the provider's create-time logic also requires allocated_storage, engine, username and one of password/password_wo/manage_master_user_password (validate does not catch any of these - they are enforced only once Create actually runs, confirmed by hand against floci during this batch's verification), and instance_class needs a real instance type, not an arbitrary string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("instance_class", exprTokens(`"db.t3.micro"`))
			body.SetAttributeRaw("allocated_storage", exprTokens(`10`))
			body.SetAttributeRaw("engine", exprTokens(`"mysql"`))
			body.SetAttributeRaw("username", exprTokens(`"admin"`))
			body.SetAttributeRaw("password", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-pw"`, g.cohort)))
			body.SetAttributeRaw("skip_final_snapshot", exprTokens(`true`))
		},
	},
	"aws_rds_cluster_instance": {
		Reasons: []string{
			`schema requires identifier, cluster_identifier, engine and instance_class; the provider validates engine against the same fixed enum as aws_rds_cluster (validate: "expected engine to be one of [aurora-mysql aurora-postgresql mysql postgres]"), and the documented example sets it from the parent cluster's own engine argument rather than an independent literal - instance_class also needs a real instance type, not an arbitrary string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			engineExpr := `"aurora-mysql"`
			if parent, ok := g.byType["aws_rds_cluster"]; ok {
				engineExpr = parent.String() + ".engine"
			}
			body.SetAttributeRaw("engine", exprTokens(engineExpr))
			body.SetAttributeRaw("instance_class", exprTokens(`"db.r4.large"`))
		},
	},
	"aws_db_event_subscription": {
		Reasons: []string{
			`schema requires only name and sns_topic; the provider validates sns_topic is a well-formed ARN (validate: "is an invalid ARN"), and no aws_sns_topic is part of this cohort to reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("sns_topic", exprTokens(fmt.Sprintf(
				`"arn:aws:sns:us-east-1:000000000000:tofu-%s-cohort-events"`, g.cohort)))
		},
	},
	"aws_db_instance_role_association": {
		Reasons: []string{
			`schema requires db_instance_identifier, feature_name and role_arn; the provider validates role_arn is a well-formed ARN (validate: "is an invalid ARN"), and db_instance_identifier is a bare string the generic pass has no parentRef alias for (its own name differs from aws_db_instance's "identifier" identity argument), so it defaults to a placeholder that names no real instance in this cohort`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if parent, ok := g.byType["aws_db_instance"]; ok {
				body.SetAttributeRaw("db_instance_identifier", exprTokens(parent.String()+".identifier"))
			}
			body.SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-role"`, g.cohort)))
		},
	},
	"aws_db_proxy": {
		Reasons: []string{
			`schema requires name, engine_family, role_arn and vpc_subnet_ids; the provider validates engine_family is one of MYSQL/POSTGRESQL/SQLSERVER (validate: "expected engine_family to be one of ...") and role_arn is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine_family", exprTokens(`"MYSQL"`))
			body.SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-role"`, g.cohort)))
		},
	},
	"aws_db_proxy_default_target_group": {
		Reasons: []string{
			`schema requires only db_proxy_name; the generic pass's parentRef alias does not fire because this type's own identity argument (db_proxy_name, per internal/live/identity/table.go) has the same name as its own Required argument, so it fills its own identity placeholder instead of referencing the sibling aws_db_proxy this batch's cohort also admits - db_proxy_name is set to aws_db_proxy.app's own "name" argument by hand instead`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if parent, ok := g.byType["aws_db_proxy"]; ok {
				body.SetAttributeRaw("db_proxy_name", exprTokens(parent.String()+".name"))
			}
		},
	},
	"aws_rds_cluster": {
		Reasons: []string{
			`schema requires cluster_identifier and engine; the provider validates engine against a fixed enum (validate: "expected engine to be one of [aurora-mysql aurora-postgresql mysql postgres]"; a second, independent validator on the same attribute also rejects it: "invalid value for engine (must begin with custom-)"); skip_final_snapshot also defaults to false, and the provider refuses a destroy without it or a final_snapshot_identifier (found only by exercising a destroy against floci during this batch's verification, not by validate)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine", exprTokens(`"aurora-mysql"`))
			body.SetAttributeRaw("skip_final_snapshot", exprTokens(`true`))
		},
	},
	"aws_rds_cluster_role_association": {
		Reasons: []string{
			`schema requires db_cluster_identifier, feature_name and role_arn; the provider validates role_arn is a well-formed ARN (validate: "is an invalid ARN"), and db_cluster_identifier is a bare string the generic pass has no parentRef alias for (its own name differs from aws_rds_cluster's "cluster_identifier" identity argument), so it defaults to a placeholder that names no real cluster in this cohort`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if parent, ok := g.byType["aws_rds_cluster"]; ok {
				body.SetAttributeRaw("db_cluster_identifier", exprTokens(parent.String()+".cluster_identifier"))
			}
			body.SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-role"`, g.cohort)))
		},
	},
	"aws_rds_custom_db_engine_version": {
		Reasons: []string{
			`schema requires engine, database_installation_files_s3_bucket_name, database_installation_files_s3_prefix and engine_version; the provider validates engine must begin with "custom-" (validate: "invalid value for engine (must begin with custom-)"), the same shape as aws_rds_cluster's engine but a disjoint enum`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine", exprTokens(`"custom-oracle-ee-cdb"`))
		},
	},
	"aws_rds_shard_group": {
		Reasons: []string{
			`schema requires db_cluster_identifier, db_shard_group_identifier and max_acu; db_cluster_identifier is a bare string the generic pass has no parentRef alias for (its own name differs from aws_rds_cluster's "cluster_identifier" identity argument), so it defaults to a placeholder that names no real cluster in this cohort - no provider-side validation catches the mismatch, but the fix keeps this cohort's shard group pointed at the real cluster it admits alongside it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if parent, ok := g.byType["aws_rds_cluster"]; ok {
				body.SetAttributeRaw("db_cluster_identifier", exprTokens(parent.String()+".cluster_identifier"))
			}
		},
	},
	"aws_rds_integration": {
		Reasons: []string{
			`schema requires integration_name, source_arn and target_arn; the provider validates both are well-formed ARNs (validate: "Invalid ARN Value") - source_arn references the cohort's own aws_rds_cluster, target_arn names a Redshift Serverless namespace no type in this cohort covers`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			sourceExpr := fmt.Sprintf(`"arn:aws:rds:us-east-1:000000000000:cluster:tofu-%s-cohort-cluster"`, g.cohort)
			if parent, ok := g.byType["aws_rds_cluster"]; ok {
				sourceExpr = parent.String() + ".arn"
			}
			body.SetAttributeRaw("source_arn", exprTokens(sourceExpr))
			body.SetAttributeRaw("target_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:redshift-serverless:us-east-1:000000000000:namespace/tofu-%s-cohort-namespace"`, g.cohort)))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesRds) }
