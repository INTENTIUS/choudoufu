// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesDatabases is the databases cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesDatabases = map[string]typeOverride{
	// Databases batch (issue #65). Before #136's cohort/type-fix rule,
	// several entries below fixed the same parentRef mis-wiring shape
	// aws_ecs_daemon and aws_eks_access_entry document above: a type whose
	// own "name" argument is not its identity.LookupType-visible identity
	// (either because it is server-assigned, per identityArgName's rule at
	// gen.go:114-124, or because its identity is a multi-component
	// composite, e.g. the three OpenSearchServerless policy types' name+type
	// pair) had no competing claim on "name", so parentRef's same-name
	// search silently wired it to the alphabetically-first sibling that did
	// own "name" as a single-component identity - aws_docdb_event_subscription
	// in this cohort, with no real relationship to any of them. That never
	// happens now (gen.go's parentRef never treats a bare "name" argument as
	// a same-named sibling's parent), and the entries below that used to fix
	// it keep their own literal names anyway, for consistency with their
	// siblings; each says so. The rest of the entries below fix real
	// `terraform validate` failures: a placeholder string that is not a
	// well-formed ARN, exceeds a length limit, or is not a member of a
	// closed enum the schema itself does not carry; one
	// (aws_redshift_cluster) fixes a provider-side requirement that
	// validate does not catch at all, only a real apply against floci.
	// #175 ratification batch, 2026-08-15.
	"aws_redshift_endpoint_access": {
		Reasons: []string{
			`endpoint_name is validated against a 30-character ceiling the wire schema does not express (validate: "expected length of endpoint_name to be in the range (1 - 30)"), and the generic tofu-<cohort>-cohort-<type> literal is 46 characters; shortened here, still cohort-prefixed and unique in the run`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("endpoint_name", exprTokens(`"tofu-databases-endpoint"`))
		},
	},
	"aws_redshift_cluster": {
		Reasons: []string{
			`neither "manage_master_password" nor "master_password" is Required in the wire schema (the provider accepts either), so the generic required-only pass sets neither, and validate does not catch the gap - but the provider's own plan-time logic refuses the combination outright (apply: "one of \"manage_master_password\" or \"master_password\" is required"), found only by exercising a real apply against floci`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("master_password", exprTokens(`"TofuDatabasesCohortPassw0rd"`))
		},
	},
	"aws_docdb_event_subscription": {
		Reasons: []string{
			`"sns_topic_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "\"sns_topic_arn\" (placeholder) is an invalid ARN: arn: invalid prefix"); no aws_sns_topic is part of this cohort to reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("sns_topic_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:sns:us-east-1:000000000000:tofu-%s-cohort-events"`, g.cohort)))
		},
	},
	"aws_docdbelastic_cluster": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (#136's cohort/type-fix rule: a bare "name" argument is never treated as a same-named sibling's parent); kept set to its own literal for consistency with its siblings. "auth_type" is a required argument the provider validates against a closed enum (PLAIN_TEXT, SECRET_ARN per the provider's own Argument Reference), and the generic placeholder string is neither. "shard_capacity" and "shard_count" are both required integers the generic pass leaves at their zero value, which the provider's own documented Argument Reference says is below the minimum in practice (not caught by validate, found by reading the provider's example usage) - set to the documented example's own values`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-docdbelastic-cluster"`, g.cohort)))
			body.SetAttributeRaw("auth_type", exprTokens(`"PLAIN_TEXT"`))
			body.SetAttributeRaw("shard_capacity", exprTokens(`2`))
			body.SetAttributeRaw("shard_count", exprTokens(`1`))
		},
	},
	"aws_elasticsearch_domain": {
		Reasons: []string{
			`"domain_name" is a required string the schema does not constrain, but the provider validates it against a closed shape (validate: "must start with a lowercase alphabet and be at least 3 and no more than 28 characters long. Valid characters are a-z (lowercase letters), 0-9, and - (hyphen)"); the generic tofu-<cohort>-cohort-<type> placeholder is 44 characters and carries no uppercase, but is otherwise disqualified purely on length`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("domain_name", exprTokens(`"tofu-db-es-domain"`))
		},
	},
	"aws_keyspaces_keyspace": {
		Reasons: []string{
			`"name" is a required string the schema does not constrain, but the provider validates it against a closed shape (validate: "The name can have up to 48 characters. It must begin with an alpha-numeric character and can only contain alpha-numeric characters and underscores."); the generic placeholder is hyphenated`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_keyspaces_keyspace"`, g.cohort)))
		},
	},
	"aws_keyspaces_table": {
		Reasons: []string{
			`"keyspace_name" and "table_name" are both required strings the schema does not constrain, but the provider validates both against the same closed shape as aws_keyspaces_keyspace's own "name" above (validate: "The keyspace/table name can have up to 48 characters..."), and the generic pass's placeholders are both hyphenated; keyspace_name is also wired to the sibling aws_keyspaces_keyspace this cohort renders rather than left as an unrelated literal, since a table genuinely belongs to a keyspace. schema_definition.column.type is required and the provider validates it against its own lower-case CQL type-name shape (validate: "The type must consist of lower case alphanumerics and an optional list of upto two lower case alphanumerics enclosed in angle brackets '<>'."); the generic placeholder is neither lower-case nor a real type, set to "text"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			keyspaceNameExpr := fmt.Sprintf(`"tofu_%s_cohort_keyspaces_keyspace"`, g.cohort)
			if keyspace, ok := g.byType["aws_keyspaces_keyspace"]; ok {
				keyspaceNameExpr = fmt.Sprintf("%s.name", keyspace)
			}
			body.SetAttributeRaw("keyspace_name", exprTokens(keyspaceNameExpr))
			body.SetAttributeRaw("table_name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_keyspaces_table"`, g.cohort)))
			for _, blk := range body.Blocks() {
				if blk.Type() != "schema_definition" {
					continue
				}
				for _, inner := range blk.Body().Blocks() {
					switch inner.Type() {
					case "column":
						inner.Body().SetAttributeRaw("name", exprTokens(`"id"`))
						inner.Body().SetAttributeRaw("type", exprTokens(`"text"`))
					case "partition_key":
						inner.Body().SetAttributeRaw("name", exprTokens(`"id"`))
					}
				}
			}
		},
	},
	"aws_memorydb_user": {
		Reasons: []string{
			`authentication_mode.type is a required argument the provider validates against a closed enum (validate: "expected type to be one of [\"password\" \"iam\"], got placeholder"); set to "password", which the provider's own documented example pairs with a "passwords" list the generic pass never sets (Optional in the schema, but the API rejects a password-mode user with none) - added here for the same apply-time reason aws_backup_restore_testing_plan's recovery_point_selection above is`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "authentication_mode" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"password"`))
					blk.Body().SetAttributeRaw("passwords", exprTokens(`["TofuDatabasesCohortPassw0rd2026"]`))
				}
			}
		},
	},
	"aws_opensearch_domain": {
		Reasons: []string{
			`Same "domain_name" shape constraint as aws_elasticsearch_domain above (validate: "must start with a lowercase alphabet and be at least 3 and no more than 28 characters long..."); the generic placeholder is 40 characters`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("domain_name", exprTokens(`"tofu-db-os-domain"`))
		},
	},
	"aws_opensearchserverless_access_policy": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (#136's cohort/type-fix rule: this type's identity is the composite name+type pair, more than one Component, so identityArgName never claimed "name" as its own single-component identity - the same shape gen.go:116's "len(entry.Components) != 1" comment describes - but a bare "name" argument is now never treated as a same-named sibling's parent regardless); kept set to its own literal for consistency with its siblings. "type" is required and the provider validates it against a one-member closed enum (must be "data", per the provider's own Argument Reference); the generic placeholder is not. "policy" is a required string the provider validates as well-formed JSON matching its own access-policy shape (Rules/Principal), confirmed against the provider's documented example`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-access-policy"`))
			body.SetAttributeRaw("type", exprTokens(`"data"`))
			resourceExpr := `"collection/tofu-db-collection"`
			if collection, ok := g.byType["aws_opensearchserverless_collection"]; ok {
				resourceExpr = fmt.Sprintf(`"collection/${%s.name}"`, collection)
			}
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode([
    {
      Rules = [
        {
          ResourceType = "collection"
          Resource     = [%s]
          Permission   = ["aoss:*"]
        }
      ]
      Principal = ["arn:aws:iam::000000000000:root"]
    }
  ])`, resourceExpr)))
		},
	},
	"aws_opensearchserverless_collection": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_opensearchserverless_access_policy above; corrected to a literal name. AWS itself refuses to create a collection with no matching encryption security policy (not caught by validate; found by reading the provider's own documented example, which sequences an aws_opensearchserverless_security_policy before its collection with an explicit depends_on) - wired to this cohort's own aws_opensearchserverless_security_policy the same way`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-collection"`))
			if secPolicy, ok := g.byType["aws_opensearchserverless_security_policy"]; ok {
				body.SetAttributeRaw("depends_on", exprTokens(fmt.Sprintf(`[%s]`, secPolicy)))
			}
		},
	},
	"aws_opensearchserverless_collection_group": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_opensearchserverless_access_policy above; corrected to a literal name. "standby_replicas" is required and the provider validates it against a closed enum (ENABLED, DISABLED per the provider's own Argument Reference); the generic placeholder is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-collection-group"`))
			body.SetAttributeRaw("standby_replicas", exprTokens(`"ENABLED"`))
		},
	},
	"aws_opensearchserverless_lifecycle_policy": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_opensearchserverless_access_policy above; corrected to a literal name. "type" is required and the provider validates it against a one-member closed enum (must be "retention", per the provider's own Argument Reference); the generic placeholder is not. "policy" is a required string the provider validates as well-formed JSON matching its own lifecycle-policy shape (Rules with ResourceType/Resource/MinIndexRetention), confirmed against the provider's documented example`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-lifecycle-policy"`))
			body.SetAttributeRaw("type", exprTokens(`"retention"`))
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Rules = [
      {
        ResourceType      = "index"
        Resource          = ["index/tofu-db-collection/*"]
        MinIndexRetention = "30d"
      }
    ]
  })`))
		},
	},
	"aws_opensearchserverless_security_policy": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_opensearchserverless_access_policy above; corrected to a literal name. "type" is required and the provider validates it against a closed enum (encryption, network per the provider's own Argument Reference); the generic placeholder is neither. "policy" is a required string the provider validates as well-formed JSON matching its own encryption-policy shape (Rules/AWSOwnedKey), confirmed against the provider's documented example - the policy's Resource pattern targets this cohort's own aws_opensearchserverless_collection by name, since it is the encryption policy that type's own override depends_on`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-security-policy"`))
			body.SetAttributeRaw("type", exprTokens(`"encryption"`))
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Rules = [
      {
        ResourceType = "collection"
        Resource     = ["collection/tofu-db-collection"]
      }
    ]
    AWSOwnedKey = true
  })`))
		},
	},
	"aws_qldb_ledger": {
		Reasons: []string{
			`"name" is a required string the schema does not constrain, but the provider validates its length (validate: "expected length of name to be in the range (1 - 32), got tofu-databases-cohort-qldb-ledger"), 34 characters against a 32-character limit. "permissions_mode" is required and the provider validates it against a closed enum (ALLOW_ALL, STANDARD per the provider's own Argument Reference); the generic placeholder is neither, and STANDARD is the value the provider's own docs recommend over the legacy ALLOW_ALL`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-qldb-ledger"`))
			body.SetAttributeRaw("permissions_mode", exprTokens(`"STANDARD"`))
		},
	},
	"aws_timestreaminfluxdb_db_cluster": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_docdbelastic_cluster above (this type is also server-assigned); corrected to a literal name. "db_instance_type" is required and the plugin-framework schema validates it against a closed enum (validate: "Invalid String Enum Value" - db.influx.medium, db.influx.large, ...); the generic placeholder is not a member. "vpc_security_group_ids" and "vpc_subnet_ids" are both required lists of strings the framework schema validates by regular expression (^sg-[a-z0-9]+$ and ^subnet-[a-z0-9]+$ respectively); the generic placeholder string matches neither. "allocated_storage", "bucket", "organization", "password" and "username" are all Optional in the wire schema, so the generic required-only pass never sets them, but the provider's own plan-time business logic requires all five for a V2 cluster (not caught by validate; found by exercising a real apply against floci: "Missing Required Configuration for InfluxDB V2": "allocated_storage/bucket/organization/password/username is required for InfluxDB V2 clusters") - added by hand the same way aws_timestreaminfluxdb_db_instance's own allocated_storage already is. "password" also has its own regular-expression shape (validate: "Attribute password value must match regular expression '^[a-zA-Z0-9]+$'"), found the same apply-time way; the generic cohort-derived literal that would otherwise land here is hyphenated, so this one is alphanumeric-only instead`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-influxdb-cluster"`))
			body.SetAttributeRaw("db_instance_type", exprTokens(`"db.influx.medium"`))
			body.SetAttributeRaw("vpc_security_group_ids", exprTokens(`["sg-0123456789abcdef0"]`))
			body.SetAttributeRaw("vpc_subnet_ids", exprTokens(`["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]`))
			body.SetAttributeRaw("allocated_storage", exprTokens(`20`))
			body.SetAttributeRaw("bucket", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-influxdb"`, g.cohort)))
			body.SetAttributeRaw("organization", exprTokens(fmt.Sprintf(`"tofu-%s-cohort"`, g.cohort)))
			body.SetAttributeRaw("password", exprTokens(`"TofuDatabasesCohortPassw0rd2026"`))
			body.SetAttributeRaw("username", exprTokens(`"admin"`))
		},
	},
	"aws_timestreaminfluxdb_db_instance": {
		Reasons: []string{
			`Same "name" mis-wiring, "db_instance_type" enum, and "vpc_security_group_ids"/"vpc_subnet_ids" regular-expression shapes as aws_timestreaminfluxdb_db_cluster above. "allocated_storage" is a required integer the framework schema validates as 20-16384 (validate: "Attribute allocated_storage value must be between 20 and 16384, got: 0"); the generic pass's zero value is below the minimum`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-influxdb-instance"`))
			body.SetAttributeRaw("db_instance_type", exprTokens(`"db.influx.medium"`))
			body.SetAttributeRaw("vpc_security_group_ids", exprTokens(`["sg-0123456789abcdef0"]`))
			body.SetAttributeRaw("vpc_subnet_ids", exprTokens(`["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]`))
			body.SetAttributeRaw("allocated_storage", exprTokens(`20`))
		},
	},
	"aws_timestreamquery_scheduled_query": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_docdbelastic_cluster above (this type is also server-assigned). schedule_configuration, error_report_configuration, notification_configuration and target_configuration are all required blocks the wire schema marks optional-in-shape while the plugin framework requires each present in practice (validate: "Block ... must have a configuration value as the provider has marked it as required"); the generic required-only pass never visits any of the four since none is Required at the top level, so all four - and every one of their own required nested fields - are added by hand here, following the provider's own documented example verbatim`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-db-scheduled-query"`))

			sched := body.AppendNewBlock("schedule_configuration", nil)
			sched.Body().SetAttributeRaw("schedule_expression", exprTokens(`"rate(1 hour)"`))

			errRpt := body.AppendNewBlock("error_report_configuration", nil)
			s3cfg := errRpt.Body().AppendNewBlock("s3_configuration", nil)
			s3cfg.Body().SetAttributeRaw("bucket_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-scheduled-query-errors"`, g.cohort)))

			notif := body.AppendNewBlock("notification_configuration", nil)
			sns := notif.Body().AppendNewBlock("sns_configuration", nil)
			sns.Body().SetAttributeRaw("topic_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:sns:us-east-1:000000000000:tofu-%s-cohort-scheduled-query"`, g.cohort)))

			target := body.AppendNewBlock("target_configuration", nil)
			tsCfg := target.Body().AppendNewBlock("timestream_configuration", nil)
			if db, ok := g.byType["aws_timestreamwrite_database"]; ok {
				tsCfg.Body().SetAttributeRaw("database_name", exprTokens(fmt.Sprintf("%s.database_name", db)))
			} else {
				tsCfg.Body().SetAttributeRaw("database_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-timestreamwrite-database"`, g.cohort)))
			}
			if tbl, ok := g.byType["aws_timestreamwrite_table"]; ok {
				tsCfg.Body().SetAttributeRaw("table_name", exprTokens(fmt.Sprintf("%s.table_name", tbl)))
			} else {
				tsCfg.Body().SetAttributeRaw("table_name", exprTokens(`"tofu-db-timestreamwrite-table"`))
			}
			tsCfg.Body().SetAttributeRaw("time_column", exprTokens(`"time"`))
			dim := tsCfg.Body().AppendNewBlock("dimension_mapping", nil)
			dim.Body().SetAttributeRaw("name", exprTokens(`"region"`))
			dim.Body().SetAttributeRaw("dimension_value_type", exprTokens(`"VARCHAR"`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesDatabases) }
