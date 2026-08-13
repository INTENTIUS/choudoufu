// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverride is the residual, hand-written surface issue #56 asks this
// generator to keep "visible and rare": a provider-side requirement that
// never reaches configschema.Attribute.Required at all, because the AWS
// provider enforces it through plan-time validation (ExactlyOneOf,
// RequiredWith, a ValidateFunc that checks a string's shape) rather than
// through the wire schema fillBlock reads. Nothing in
// GetProviderSchemaResponse names these constraints - they were found only
// by running the generic pass's output through `terraform validate` and
// reading what it refused - so every entry below cites the validate error
// it exists to fix, not a schema field.
type typeOverride struct {
	// Reasons is recorded verbatim in the generated README's provenance
	// table ("overrides: ...") for this type.
	Reasons []string

	// NeedsIAMRole tells planCohort this type needs the shared
	// aws_iam_role support resource even though isRoleArg does not fire
	// on any of its schema-Required argument names (aws_lambda_function's
	// own "role" argument already triggers it on its own; this exists for
	// a type whose role dependency Apply adds by hand, inside an optional
	// block the generic required-only pass never visits).
	NeedsIAMRole bool

	// Apply runs after the generic required-only pass (and after
	// iamRoleRefExpr and identityArgName have already filled in every
	// argument the schema or the identity table account for): it adds or
	// replaces whatever the provider needs in practice that the schema
	// alone does not say. SetAttributeRaw on an argument the generic pass
	// already set replaces its value in place, at the position the
	// generic pass gave it; every other call appends at the end of the
	// block, which is why README provenance calls these out by name
	// rather than leaving them to blend in.
	Apply func(g *generator, body *hclwrite.Body, addr resourceAddr)
}

// typeOverrides is keyed by provider-local type name. Empty for every type
// the generic pass alone renders validate-clean; the two cohorts this
// generator has actually been run against (lambda, s3) are exactly what
// populated this table - see the estate-gen package doc comment and the
// issue #56 final report for the empirical trail (each entry's Reasons
// string is the `terraform validate` error the override exists to silence).
var typeOverrides = map[string]typeOverride{
	"aws_iam_role": {
		Reasons: []string{
			`schema requires "assume_role_policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"assume_role_policy\" contains an invalid JSON"); the generic string placeholder is not JSON`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("assume_role_policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "%s.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })`, g.cohort)))
		},
	},
	"aws_lambda_code_signing_config": {
		Reasons: []string{
			`allowed_publishers.signing_profile_version_arns is a required set of strings, but the provider validates each element is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "allowed_publishers" {
					blk.Body().SetAttributeRaw("signing_profile_version_arns", exprTokens(fmt.Sprintf(
						`["arn:aws:signer:us-east-1:000000000000:/signing-profiles/tofu_%s_cohort/1a2b3c4d5e"]`, g.cohort)))
				}
			}
		},
	},
	"aws_lambda_function": {
		Reasons: []string{
			`schema requires only function_name and role; the provider also requires exactly one of filename/image_uri/s3_bucket (validate: "one of ... must be specified"), and image_uri requires package_type = "Image"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("package_type", exprTokens(`"Image"`))
			body.SetAttributeRaw("image_uri", exprTokens(fmt.Sprintf(
				`"000000000000.dkr.ecr.us-east-1.amazonaws.com/tofu-%s-cohort-app:latest"`, g.cohort)))
		},
	},
	"aws_lambda_event_source_mapping": {
		Reasons: []string{
			`schema requires only function_name; the provider also requires exactly one of event_source_arn/self_managed_event_source (validate: "one of ... must be specified"), and event_source_arn requires starting_position`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("event_source_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:dynamodb:us-east-1:000000000000:table/tofu-%s-cohort-events/stream/2026-01-01T00:00:00.000"`, g.cohort)))
			body.SetAttributeRaw("starting_position", exprTokens(`"LATEST"`))
		},
	},
	"aws_lambda_layer_version": {
		Reasons: []string{
			`schema requires only layer_name; the provider also requires one of filename/s3_bucket (validate: "one of ... must be specified"), and s3_bucket requires s3_key; compatible_runtimes is optional in the schema but left empty renders a layer no function could ever reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("s3_bucket", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-artifacts"`, g.cohort)))
			body.SetAttributeRaw("s3_key", exprTokens(`"layers/app.zip"`))
			body.SetAttributeRaw("compatible_runtimes", exprTokens(`["python3.13"]`))
		},
	},
	"aws_lambda_capacity_provider": {
		Reasons: []string{
			`schema requires only name; vpc_config and permissions_config are both optional blocks, but the provider requires both in practice (validate: "Missing required argument") - permissions_config needs the shared aws_iam_role's ARN, which is why this type also sets NeedsIAMRole`,
		},
		NeedsIAMRole: true,
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			vpc := body.AppendNewBlock("vpc_config", nil)
			vpc.Body().SetAttributeRaw("subnet_ids", exprTokens(`["subnet-0123456789abcdef0"]`))
			vpc.Body().SetAttributeRaw("security_group_ids", exprTokens(`["sg-0123456789abcdef0"]`))

			perm := body.AppendNewBlock("permissions_config", nil)
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"placeholder"`
			}
			perm.Body().SetAttributeRaw("capacity_provider_operator_role_arn", exprTokens(ref))
		},
	},
	"aws_s3_bucket_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"); the generic string placeholder is not JSON`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			// The bucket's own "arn" attribute, not the identity attrName
			// parentRef returns (which names the bucket-argument value, the
			// bucket's name - the policy's Resource element needs the ARN).
			// resourceExpr is the Resource element's HCL source: an
			// interpolation of the sibling bucket's arn attribute when this
			// run is also rendering aws_s3_bucket, or a literal placeholder
			// ARN otherwise (a cohort that admits aws_s3_bucket_policy
			// without aws_s3_bucket, which none of this generator's own
			// cohorts do).
			resourceExpr := `"arn:aws:s3:::tofu-` + g.cohort + `-cohort-bucket/*"`
			if parent, _, ok := g.parentRef(addr.Type, "bucket"); ok {
				resourceExpr = fmt.Sprintf(`"${%s.arn}/*"`, parent)
			}
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "s3:GetObject"
      Resource  = %s
    }]
  })`, resourceExpr)))
		},
	},
	"aws_s3_bucket_versioning": {
		Reasons: []string{
			`versioning_configuration is a required block, but its "status" argument has no default the generic pass can infer - set to the provider's documented enum member "Enabled"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "versioning_configuration" {
					blk.Body().SetAttributeRaw("status", exprTokens(`"Enabled"`))
				}
			}
		},
	},
	"aws_s3_bucket_server_side_encryption_configuration": {
		Reasons: []string{
			`rule is a required block, but its nested apply_server_side_encryption_by_default block is itself optional in the schema while the provider requires it in practice (validate: "Missing required argument") along with its required sse_algorithm enum member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "rule" {
					def := blk.Body().AppendNewBlock("apply_server_side_encryption_by_default", nil)
					def.Body().SetAttributeRaw("sse_algorithm", exprTokens(`"AES256"`))
				}
			}
		},
	},
	"aws_s3_bucket_lifecycle_configuration": {
		Reasons: []string{
			`rule is optional in the schema (MinItems 0) but the provider requires at least one (validate: "Missing required argument"), and each rule requires an id and a status enum member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			rule := body.AppendNewBlock("rule", nil)
			rule.Body().SetAttributeRaw("id", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-expire"`, g.cohort)))
			rule.Body().SetAttributeRaw("status", exprTokens(`"Enabled"`))
		},
	},

	// EC2 core batch (issue #65). Every argument below is Optional in the
	// wire schema (so the generic required-only pass leaves it unset or
	// leaves a bare "placeholder" that fails an enum/format check the
	// schema itself does not carry), or is a nested block the schema marks
	// optional while the provider requires its contents in practice - the
	// same two failure shapes issue #56 already named for the Lambda and S3
	// cohorts above.
	"aws_ebs_snapshot_block_public_access": {
		Reasons: []string{
			`state is Required but Optional-shaped in nothing else - the provider validates it against a closed enum (validate: "expected state to be one of [...]"), and the generic pass's "placeholder" string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("state", exprTokens(`"block-new-sharing"`))
		},
	},
	"aws_ec2_capacity_reservation": {
		Reasons: []string{
			`instance_platform is Required and the provider validates it against a closed enum (validate: "expected instance_platform to be one of [...]"); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("instance_platform", exprTokens(`"Linux/UNIX"`))
		},
	},
	"aws_ec2_fleet": {
		Reasons: []string{
			`launch_template_config is a required block, but its own launch_template_specification child is optional in the schema while the provider requires it in practice (validate: "Invalid combination of arguments" on an empty launch_template_config); target_capacity_specification.default_target_capacity_type is Required and validated against a closed enum, and the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "launch_template_config":
					spec := blk.Body().AppendNewBlock("launch_template_specification", nil)
					spec.Body().SetAttributeRaw("launch_template_id", exprTokens(`"lt-0123456789abcdef0"`))
					spec.Body().SetAttributeRaw("version", exprTokens(`"$Latest"`))
				case "target_capacity_specification":
					blk.Body().SetAttributeRaw("default_target_capacity_type", exprTokens(`"on-demand"`))
				}
			}
		},
	},
	"aws_ec2_host": {
		Reasons: []string{
			`instance_family and instance_type are both Optional in the schema, but the provider requires exactly one (validate: "Invalid combination of arguments": "one of instance_family,instance_type must be specified"), and the generic required-only pass sets neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("instance_type", exprTokens(`"c5.xlarge"`))
		},
	},
	"aws_eip_association": {
		Reasons: []string{
			`every argument is Optional in the schema, so the generic pass renders an empty body, but the provider requires exactly one of instance_id/network_interface_id (validate: "Invalid combination of arguments"); allocation_id is documented as required in practice too (legacy EC2-Classic exception in the Argument Reference), so both are set here rather than just enough to silence validate. instance_id references this same cohort's aws_instance.app - the cross-resource reference issue #56 asks for - since identityArgName only wires that automatically for client-named types, and aws_instance is server-assigned.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if instance, ok := g.byType["aws_instance"]; ok {
				body.SetAttributeRaw("instance_id", exprTokens(fmt.Sprintf("%s.id", instance)))
			} else {
				body.SetAttributeRaw("instance_id", exprTokens(`"i-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("allocation_id", exprTokens(`"eipalloc-0123456789abcdef0"`))
		},
	},
	"aws_instance": {
		Reasons: []string{
			`ami and instance_type are both Optional in the schema (a launch_template can supply either instead), but the provider requires ami and instance_type when no launch_template is set (validate: "Missing required argument" x3), and the generic required-only pass sets neither since the schema alone does not say so`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("ami", exprTokens(`"ami-0123456789abcdef0"`))
			body.SetAttributeRaw("instance_type", exprTokens(`"t3.micro"`))
		},
	},
	"aws_network_interface_attachment": {
		Reasons: []string{
			`instance_id and network_interface_id are both Required but generic-string placeholders, not references - overridden to point at this cohort's own aws_instance.app and aws_network_interface.app for the same cross-resource-reference reason as aws_eip_association above (validate does not require this; it is a fixture-quality improvement, not a constraint fix)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if instance, ok := g.byType["aws_instance"]; ok {
				body.SetAttributeRaw("instance_id", exprTokens(fmt.Sprintf("%s.id", instance)))
			}
			if eni, ok := g.byType["aws_network_interface"]; ok {
				body.SetAttributeRaw("network_interface_id", exprTokens(fmt.Sprintf("%s.id", eni)))
			}
		},
	},
	"aws_network_interface_permission": {
		Reasons: []string{
			`aws_account_id is Required and the provider validates it is a well-formed 12-digit account ID (validate: "must be a valid AWS account ID"); permission is Required and validated against a closed enum (INSTANCE-ATTACH, EIP-ASSOCIATE); network_interface_id is overridden to reference this cohort's own aws_network_interface.app for the same reason as aws_network_interface_attachment above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("aws_account_id", exprTokens(`"123456789012"`))
			body.SetAttributeRaw("permission", exprTokens(`"INSTANCE-ATTACH"`))
			if eni, ok := g.byType["aws_network_interface"]; ok {
				body.SetAttributeRaw("network_interface_id", exprTokens(fmt.Sprintf("%s.id", eni)))
			}
		},
	},
	"aws_placement_group": {
		Reasons: []string{
			`strategy is Required and the provider validates it against a closed enum (validate: "expected strategy to be one of [...]"); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("strategy", exprTokens(`"cluster"`))
		},
	},
	"aws_spot_fleet_request": {
		Reasons: []string{
			`launch_specification and launch_template_config are both Optional in the schema, but the provider requires exactly one (validate: "Invalid combination of arguments"), and the generic pass sets neither; iam_fleet_role is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), and the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("iam_fleet_role", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::123456789012:role/tofu-%s-cohort-spot-fleet"`, g.cohort)))
			spec := body.AppendNewBlock("launch_specification", nil)
			spec.Body().SetAttributeRaw("ami", exprTokens(`"ami-0123456789abcdef0"`))
			spec.Body().SetAttributeRaw("instance_type", exprTokens(`"t3.micro"`))
		},
	},
	"aws_volume_attachment": {
		Reasons: []string{
			`instance_id is Required but a generic-string placeholder, not a reference - overridden to point at this cohort's own aws_instance.app for the same cross-resource-reference reason as aws_eip_association above. volume_id stays a literal placeholder: aws_ebs_volume is already admitted and covered by live/e2e/estate, not part of this cohort's own coverage, so there is no sibling aws_ebs_volume resource in this run to point at (validate does not require this either; it is a fixture-quality note, not a constraint fix).`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if instance, ok := g.byType["aws_instance"]; ok {
				body.SetAttributeRaw("instance_id", exprTokens(fmt.Sprintf("%s.id", instance)))
			}
		},
	},
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
	"aws_api_gateway_base_path_mapping": {
		Reasons: []string{
			`api_id has no identity-table candidate to auto-wire from (aws_api_gateway_rest_api is server-assigned, so parentRef never proposes it); wired to the REST API this cohort renders`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
		},
	},
	"aws_api_gateway_documentation_version": {
		Reasons: []string{
			`rest_api_id was mis-wired to aws_api_gateway_rest_api_policy.app (parentRef's only candidate whose identity self-names "rest_api_id" - the real parent, aws_api_gateway_rest_api, is server-assigned and never a parentRef candidate); corrected to the REST API this cohort renders`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
			body.SetAttributeRaw("version", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-docs-v1"`, g.cohort)))
		},
	},
	"aws_api_gateway_gateway_response": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; response_type is a fixed enum the provider validates server-side (terraform validate does not catch "placeholder", but it is not one of the documented values), set to the provider docs' own example value`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
			body.SetAttributeRaw("response_type", exprTokens(`"UNAUTHORIZED"`))
		},
	},
	"aws_api_gateway_method": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; resource_id has no identity-table candidate at all because aws_api_gateway_resource is not admitted this batch (rejected), and that type cannot be added as supporting infrastructure either - every fixture resource, coverage or supporting, has to be an admitted type (TestAdmissionTableCoversEstate, TestTableCoversFixtureTypes) - so this method attaches to the REST API's own root_resource_id instead of a child resource, which needs no unadmitted type at all`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
				body.SetAttributeRaw("resource_id", exprTokens(fmt.Sprintf("%s.root_resource_id", restAPI)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("authorization", exprTokens(`"NONE"`))
		},
	},
	// The three fold-children below (issue #68) all key on the same
	// (rest_api_id, resource_id, http_method) triple aws_api_gateway_method
	// above already does, since each duplicates the method's own composite
	// identity verbatim (internal/live/identity/table.go's "Fold-children"
	// section comment) - parentRef mis-wires rest_api_id the same way as
	// aws_api_gateway_documentation_version above (its only same-named
	// candidate is aws_api_gateway_rest_api_policy.app, whose own identity
	// happens to self-name "rest_api_id" too), and has no candidate at all
	// for resource_id, http_method or status_code, the same gap
	// aws_api_gateway_method's own override closes.
	"aws_api_gateway_integration": {
		Reasons: []string{
			`rest_api_id/resource_id/http_method mis-wired or left as the generic placeholder the same way aws_api_gateway_method's were, for the same reason - corrected to the same REST API root resource and GET method aws_api_gateway_method.app already targets, so this integration is the method's own; type is a fixed enum (validate: "expected type to be one of [...]"), set to MOCK, the shape floci's PutIntegration/GetIntegration round-trip cleanly (verified by hand)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
				body.SetAttributeRaw("resource_id", exprTokens(fmt.Sprintf("%s.root_resource_id", restAPI)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("type", exprTokens(`"MOCK"`))
		},
	},
	"aws_api_gateway_integration_response": {
		Reasons: []string{
			`rest_api_id/resource_id/http_method mis-wired or left as the generic placeholder the same way aws_api_gateway_integration's were above, and for the same reason - status_code is left schema-Required-but-unvalidated by terraform validate, but the provider expects a real HTTP status string, set to the aws_api_gateway_method_response.app row below's own value so the two agree`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
				body.SetAttributeRaw("resource_id", exprTokens(fmt.Sprintf("%s.root_resource_id", restAPI)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("status_code", exprTokens(`"200"`))
		},
	},
	"aws_api_gateway_method_response": {
		Reasons: []string{
			`rest_api_id/resource_id/http_method/status_code, the same corrections as aws_api_gateway_integration_response above and for the same reason`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
				body.SetAttributeRaw("resource_id", exprTokens(fmt.Sprintf("%s.root_resource_id", restAPI)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("status_code", exprTokens(`"200"`))
		},
	},
	"aws_api_gateway_method_settings": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; stage_name has no identity-table candidate to auto-wire from (aws_api_gateway_stage's own identity is the two-component rest_api_id/stage_name pair, not a single self-named argument, so identityArgName never fires on it, the same gap aws_api_gateway_method_settings's own fold parent has), wired to the stage this cohort renders instead of the generic placeholder; method_path left as the generic placeholder is not a real method path, set to the */* wildcard the provider docs use for "every method of every resource in the stage"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
			if stage, ok := g.byType["aws_api_gateway_stage"]; ok {
				body.SetAttributeRaw("stage_name", exprTokens(fmt.Sprintf("%s.stage_name", stage)))
			}
			body.SetAttributeRaw("method_path", exprTokens(`"*/*"`))
		},
	},
	"aws_api_gateway_model": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; content_type and schema left as the generic placeholder would not be valid JSON, set to a minimal real value`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
			body.SetAttributeRaw("content_type", exprTokens(`"application/json"`))
			body.SetAttributeRaw("schema", exprTokens(`jsonencode({})`))
		},
	},
	"aws_api_gateway_stage": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; deployment_id has no identity-table candidate because aws_api_gateway_deployment is not admitted this batch (rejected), so it is left as the generic placeholder string - a stage is its own coverage row and the deployment it names existing is not this type's identity concern`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
		},
	},
	"aws_api_gateway_usage_plan_key": {
		Reasons: []string{
			`usage_plan_id and key_id have no identity-table candidates because aws_api_gateway_usage_plan and aws_api_gateway_api_key are both server-assigned (parentRef never proposes a server-assigned sibling, the same gap as the REST API children above); wired to the two sibling coverage rows this cohort renders, and key_type set to its one documented value`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if plan, ok := g.byType["aws_api_gateway_usage_plan"]; ok {
				body.SetAttributeRaw("usage_plan_id", exprTokens(fmt.Sprintf("%s.id", plan)))
			}
			if key, ok := g.byType["aws_api_gateway_api_key"]; ok {
				body.SetAttributeRaw("key_id", exprTokens(fmt.Sprintf("%s.id", key)))
			}
			body.SetAttributeRaw("key_type", exprTokens(`"API_KEY"`))
		},
	},
	"aws_api_gateway_rest_api_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"), the same shape as aws_s3_bucket_policy above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			resourceExpr := fmt.Sprintf(`"arn:aws:execute-api:us-east-1:000000000000:tofu-%s-cohort-placeholder/*"`, g.cohort)
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				resourceExpr = fmt.Sprintf(`"${%s.execution_arn}/*"`, restAPI)
			}
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "execute-api:Invoke"
      Resource  = %s
    }]
  })`, resourceExpr)))
		},
	},
	"aws_api_gateway_domain_name_access_association": {
		Reasons: []string{
			`access_association_source_type is a fixed enum (validate: "Invalid String Enum Value", valid values: VPCE); domain_name_arn is validated as a well-formed ARN (validate: "Invalid ARN Value") - both need real-shaped values, not the generic placeholder`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("access_association_source_type", exprTokens(`"VPCE"`))
			body.SetAttributeRaw("access_association_source", exprTokens(`"vpce-0123456789abcdef0"`))
			body.SetAttributeRaw("domain_name_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:apigateway:us-east-1::/domainnames/tofu-%s-cohort-api-gateway-domain-name"`, g.cohort)))
		},
	},
	"aws_apigatewayv2_api": {
		Reasons: []string{
			`protocol_type is a fixed enum (validate: "expected protocol_type to be one of [WEBSOCKET HTTP]"), not the generic placeholder`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("protocol_type", exprTokens(`"HTTP"`))
		},
	},
	"aws_apigatewayv2_domain_name": {
		Reasons: []string{
			`domain_name_configuration's three required arguments are each validated: certificate_arn as a well-formed ARN (validate: "invalid ARN: arn: invalid prefix"), endpoint_type and security_policy as fixed enums (validate: "expected ... to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "domain_name_configuration" {
					blk.Body().SetAttributeRaw("certificate_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:acm:us-east-1:000000000000:certificate/tofu-%s-cohort-placeholder"`, g.cohort)))
					blk.Body().SetAttributeRaw("endpoint_type", exprTokens(`"REGIONAL"`))
					blk.Body().SetAttributeRaw("security_policy", exprTokens(`"TLS_1_2"`))
				}
			}
		},
	},
	// The three APS fold-children below (issue #68) all need their parent
	// reference wired by hand, the same reason aws_api_gateway_base_path_mapping
	// above does: aws_prometheus_workspace and aws_prometheus_scraper are
	// both server-assigned, so identityArgName never fires on them and
	// parentRef has no candidate to propose - even though each child's own
	// identity happens to self-name the same argument
	// (workspace_id/scraper_id) its parent's id lives under, valueExpr's
	// own-identity tier (3) fires first and fills in a placeholder name
	// instead of a reference, the same shape as aws_s3_bucket_policy or
	// aws_sns_topic_policy would hit had their own parents been
	// server-assigned instead of client-named/account-derived.
	"aws_prometheus_alert_manager_definition": {
		Reasons: []string{
			`workspace_id left as a generic placeholder name instead of a reference (aws_prometheus_workspace is server-assigned, so parentRef never proposes it); wired to the workspace this cohort renders. definition is a required string the provider expects as YAML Alertmanager configuration; the generic placeholder is not valid YAML`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ws, ok := g.byType["aws_prometheus_workspace"]; ok {
				body.SetAttributeRaw("workspace_id", exprTokens(fmt.Sprintf("%s.id", ws)))
			}
			body.SetAttributeRaw("definition", exprTokens(`<<-EOT
    route:
      receiver: default
    receivers:
      - name: default
  EOT
  `))
		},
	},
	"aws_prometheus_query_logging_configuration": {
		Reasons: []string{
			`workspace_id left as a generic placeholder name instead of a reference, the same reason and correction as aws_prometheus_alert_manager_definition above. destination is a required block the schema marks optional-in-shape but the provider requires present, and its own nested filters block is required in turn (validate: "Block destination[0].filters must have a configuration value")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ws, ok := g.byType["aws_prometheus_workspace"]; ok {
				body.SetAttributeRaw("workspace_id", exprTokens(fmt.Sprintf("%s.id", ws)))
			}
			dest := body.AppendNewBlock("destination", nil)
			cwl := dest.Body().AppendNewBlock("cloudwatch_logs", nil)
			cwl.Body().SetAttributeRaw("log_group_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:logs:us-east-1:000000000000:log-group:/aws/prometheus/tofu-%s-cohort:*"`, g.cohort)))
			filters := dest.Body().AppendNewBlock("filters", nil)
			filters.Body().SetAttributeRaw("qsp_threshold", exprTokens(`0`))
		},
	},
	"aws_prometheus_scraper_logging_configuration": {
		Reasons: []string{
			`scraper_id left as a generic placeholder name instead of a reference (aws_prometheus_scraper is server-assigned, so parentRef never proposes it); wired to the scraper this cohort renders. logging_destination is a required block the schema marks optional-in-shape but the provider requires present`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if sc, ok := g.byType["aws_prometheus_scraper"]; ok {
				body.SetAttributeRaw("scraper_id", exprTokens(fmt.Sprintf("%s.id", sc)))
			}
			dest := body.AppendNewBlock("logging_destination", nil)
			cwl := dest.Body().AppendNewBlock("cloudwatch_logs", nil)
			cwl.Body().SetAttributeRaw("log_group_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:logs:us-east-1:000000000000:log-group:/aws/prometheus/scraper/tofu-%s-cohort:*"`, g.cohort)))
		},
	},
	"aws_prometheus_scraper": {
		Reasons: []string{
			`schema requires only scrape_configuration; the provider also requires the source and destination blocks (validate: "Missing required argument"), each with their own nested required arguments (an EKS-shaped source, an AMP workspace ARN destination) the generic pass has no schema signal for`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scrape_configuration", exprTokens(`<<-EOT
    global:
      scrape_interval: 30s
    scrape_configs:
      - job_name: placeholder
  EOT
  `))
			src := body.AppendNewBlock("source", nil)
			eks := src.Body().AppendNewBlock("eks", nil)
			eks.Body().SetAttributeRaw("cluster_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:eks:us-east-1:000000000000:cluster/tofu-%s-cohort"`, g.cohort)))
			eks.Body().SetAttributeRaw("subnet_ids", exprTokens(`["subnet-0123456789abcdef0"]`))

			dest := body.AppendNewBlock("destination", nil)
			amp := dest.Body().AppendNewBlock("amp", nil)
			if ws, ok := g.byType["aws_prometheus_workspace"]; ok {
				amp.Body().SetAttributeRaw("workspace_arn", exprTokens(fmt.Sprintf("%s.arn", ws)))
			}
		},
	},
	"aws_apigatewayv2_routing_rule": {
		Reasons: []string{
			`domain_name was mis-wired to aws_api_gateway_domain_name.app (the v1 type) - both v1 and v2 domain name types self-identify by the same argument name, and parentRef's alphabetic tiebreak prefers "aws_api_gateway_domain_name" over "aws_apigatewayv2_domain_name" with no way to tell they are different API generations; corrected to the v2 domain name this type actually needs. action and condition are both required blocks the schema marks optional-in-shape but the provider requires present (validate: "Block action/condition must have a configuration value"), and priority must be 1-1000000 (validate: "must be between 1 and 1000000, got: 0")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if domain, ok := g.byType["aws_apigatewayv2_domain_name"]; ok {
				body.SetAttributeRaw("domain_name", exprTokens(fmt.Sprintf("%s.domain_name", domain)))
			}
			body.SetAttributeRaw("priority", exprTokens(`1`))

			action := body.AppendNewBlock("action", nil)
			invoke := action.Body().AppendNewBlock("invoke_api", nil)
			if v2api, ok := g.byType["aws_apigatewayv2_api"]; ok {
				invoke.Body().SetAttributeRaw("api_id", exprTokens(fmt.Sprintf("%s.id", v2api)))
			}
			if v2stage, ok := g.byType["aws_apigatewayv2_stage"]; ok {
				invoke.Body().SetAttributeRaw("stage", exprTokens(fmt.Sprintf("%s.name", v2stage)))
			}

			condition := body.AppendNewBlock("condition", nil)
			mbp := condition.Body().AppendNewBlock("match_base_paths", nil)
			mbp.Body().SetAttributeRaw("any_of", exprTokens(`["/"]`))
		},
	},
	"aws_apigatewayv2_stage": {
		Reasons: []string{
			`api_id has no identity-table candidate to auto-wire from (aws_apigatewayv2_api is server-assigned, the same gap as the REST API children above); wired to the v2 API this cohort renders`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if v2api, ok := g.byType["aws_apigatewayv2_api"]; ok {
				body.SetAttributeRaw("api_id", exprTokens(fmt.Sprintf("%s.id", v2api)))
			}
		},
	},
	"aws_eks_access_entry": {
		Reasons: []string{
			`the generic pass's same-name parent search matches cluster_name against aws_ecs_cluster_capacity_providers (an unrelated ECS type that also self-identifies by "cluster_name"), not aws_eks_cluster; overridden to the correct parent. principal_arn is a required argument the provider validates as a well-formed IAM ARN (validate: "is an invalid ARN"); set to the shared aws_iam_role's own ARN, standing in for "the IAM principal this entry grants access to"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cluster_name", exprTokens(eksClusterNameRef(g)))
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("principal_arn", exprTokens(ref))
		},
	},
	"aws_eks_access_policy_association": {
		Reasons: []string{
			`same cluster_name and principal_arn shapes as aws_eks_access_entry; overridden the same way. policy_arn is also set to a real AWS-managed EKS access policy ARN in place of the generic placeholder string, which is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cluster_name", exprTokens(eksClusterNameRef(g)))
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("principal_arn", exprTokens(ref))
			body.SetAttributeRaw("policy_arn", exprTokens(`"arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"`))
		},
	},
	"aws_eks_addon": {
		Reasons: []string{
			`same cluster_name mismatch as aws_eks_access_entry; overridden to the correct parent`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cluster_name", exprTokens(eksClusterNameRef(g)))
		},
	},
	"aws_eks_capability": {
		Reasons: []string{
			`same cluster_name mismatch as aws_eks_access_entry; overridden to the correct parent. type and delete_propagation_policy are also set to real provider enum members in place of the generic placeholder string, which is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cluster_name", exprTokens(eksClusterNameRef(g)))
			body.SetAttributeRaw("type", exprTokens(`"ARGOCD"`))
			body.SetAttributeRaw("delete_propagation_policy", exprTokens(`"RETAIN"`))
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
		},
	},
	"aws_eks_fargate_profile": {
		Reasons: []string{
			`same cluster_name mismatch as aws_eks_access_entry; overridden to the correct parent`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cluster_name", exprTokens(eksClusterNameRef(g)))
		},
	},
	"aws_eks_node_group": {
		Reasons: []string{
			`same cluster_name mismatch as aws_eks_access_entry; overridden to the correct parent. scaling_config is a required block whose max_size the provider validates as at least 1 (validate: "expected scaling_config.0.max_size to be at least (1)"); the generic pass's zero value fails that, so it is set to 1 alongside desired_size and min_size`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cluster_name", exprTokens(eksClusterNameRef(g)))
			for _, blk := range body.Blocks() {
				if blk.Type() == "scaling_config" {
					blk.Body().SetAttributeRaw("desired_size", exprTokens(`1`))
					blk.Body().SetAttributeRaw("max_size", exprTokens(`1`))
					blk.Body().SetAttributeRaw("min_size", exprTokens(`1`))
				}
			}
		},
	},
	"aws_ecs_daemon": {
		Reasons: []string{
			`the generic pass's same-name parent search matches this type's own client-chosen "name" argument against aws_eks_cluster (an unrelated EKS type whose single-component identity also happens to be named "name"), producing a cross-service reference where a plain placeholder string belongs; overridden back to a placeholder. capacity_provider_arns and daemon_task_definition_arn are required arguments the provider validates as well-formed ARNs (validate: "cannot be parsed as an ARN"); this batch rejected both aws_ecs_capacity_provider and aws_ecs_daemon_task_definition (see internal/live/identity/table.go), so no real sibling resource supplies either, and both are set to well-formed placeholder ARNs instead`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-ecs-daemon"`, g.cohort)))
			body.SetAttributeRaw("capacity_provider_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ecs:us-east-1:000000000000:capacity-provider/tofu-%s-cohort-capacity-provider"]`, g.cohort)))
			body.SetAttributeRaw("daemon_task_definition_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:ecs:us-east-1:000000000000:daemon-task-definition/tofu-%s-cohort-daemon-task:1"`, g.cohort)))
		},
	},
	"aws_eks_cluster": {
		Reasons: []string{
			`role_arn is a required argument the provider validates as a well-formed ARN (validate: "is an invalid ARN"); the generic pass's placeholder string is not one, and no same-name parent search finds the shared aws_iam_role for it (the argument is "role_arn", not "role"), so it is set here the same way isRoleArg's "role"/"*_role_arn" alias would if the generic pass's own suffix rule reached this argument name`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
		},
	},
	"aws_backup_framework": {
		Reasons: []string{
			`"name" is validated against a letters/numbers/underscores pattern with no hyphens (validate: "must be must be between 1 and 256 characters, starting with a letter, and consisting of letters, numbers, and underscores"), but the generic pass's placeholder name is hyphenated`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_backup_framework"`, g.cohort)))
		},
	},
	"aws_backup_logically_air_gapped_vault": {
		Reasons: []string{
			`"name" is validated against '^[a-zA-Z0-9\-\_]{2,50}$' and the generic pass's placeholder name is 54 characters, past the limit; min_retention_days and max_retention_days are both optional in the schema but the provider requires min_retention_days >= 7 in practice (validate: "value must be at least 7")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-lag-vault"`, g.cohort)))
			body.SetAttributeRaw("min_retention_days", exprTokens(`7`))
			body.SetAttributeRaw("max_retention_days", exprTokens(`30`))
		},
	},
	"aws_backup_report_plan": {
		Reasons: []string{
			`"name" is validated against the same letters/numbers/underscores pattern as aws_backup_framework above, no hyphens; report_setting.report_template is a required argument the schema does not constrain to an enum, but the provider does (validate: "expected report_setting.0.report_template to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_backup_report_plan"`, g.cohort)))
			for _, blk := range body.Blocks() {
				if blk.Type() == "report_setting" {
					blk.Body().SetAttributeRaw("report_template", exprTokens(`"RESOURCE_COMPLIANCE_REPORT"`))
				}
			}
		},
	},
	"aws_backup_restore_testing_plan": {
		Reasons: []string{
			`"name" is validated against a letters/numbers/underscores pattern with no hyphens, the same shape as aws_backup_framework above; recovery_point_selection is a required block the schema does not mark required (MinItems 0), and its own algorithm, include_vaults and recovery_point_types arguments are all required with no schema-visible default (validate: "Block recovery_point_selection must have a configuration value")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_restore_testing_plan"`, g.cohort)))
			sel := body.AppendNewBlock("recovery_point_selection", nil)
			sel.Body().SetAttributeRaw("algorithm", exprTokens(`"LATEST_WITHIN_WINDOW"`))
			sel.Body().SetAttributeRaw("include_vaults", exprTokens(`["*"]`))
			sel.Body().SetAttributeRaw("recovery_point_types", exprTokens(`["CONTINUOUS"]`))
		},
	},
	"aws_fsx_data_repository_association": {
		Reasons: []string{
			`data_repository_path, file_system_id and file_system_path are all plain strings in the schema, but the provider validates their shape at plan time (validate: "must begin with s3://", "must begin with fs-", "path must begin with /"); the generic "placeholder" string satisfies none of the three`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("data_repository_path", exprTokens(fmt.Sprintf(`"s3://tofu-%s-cohort-bucket/data"`, g.cohort)))
			body.SetAttributeRaw("file_system_id", exprTokens(`"fs-0123456789abcdef0"`))
			body.SetAttributeRaw("file_system_path", exprTokens(`"/data"`))
		},
	},
	"aws_fsx_ontap_file_system": {
		Reasons: []string{
			`deployment_type is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected deployment_type to be one of [...]"); storage_capacity is optional/computed but the provider requires it in the range 1024-1048576 when set (validate: "expected storage_capacity to be in the range"); throughput_capacity and throughput_capacity_per_ha_pair are each optional alone, but the provider requires exactly one (validate: "one of ... must be specified")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("deployment_type", exprTokens(`"SINGLE_AZ_1"`))
			body.SetAttributeRaw("storage_capacity", exprTokens(`1024`))
			body.SetAttributeRaw("throughput_capacity", exprTokens(`128`))
		},
	},
	"aws_fsx_openzfs_file_system": {
		Reasons: []string{
			`deployment_type is a plain string in the schema but the provider validates it against a fixed enum, a different set than aws_fsx_ontap_file_system's own (validate: "expected deployment_type to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("deployment_type", exprTokens(`"SINGLE_AZ_1"`))
		},
	},
	"aws_fsx_windows_file_system": {
		Reasons: []string{
			`throughput_capacity is optional/computed in the schema, rendered as the generic pass's numeric zero placeholder, but the provider validates it against a fixed set of MB/s values (validate: "expected throughput_capacity to be one of [8 16 32 ...]"), none of which is zero`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("throughput_capacity", exprTokens(`8`))
		},
	},
	"aws_fsx_ontap_volume": {
		Reasons: []string{
			`size_in_bytes and size_in_megabytes are each optional alone, but the provider requires exactly one (validate: "one of ... must be specified"); storage_virtual_machine_id is a plain string in the schema but the provider validates its length against the real 21-character svm-… shape (validate: "expected length of storage_virtual_machine_id to be in the range (21 - 21)"), which the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("size_in_megabytes", exprTokens(`1024`))
			body.SetAttributeRaw("storage_virtual_machine_id", exprTokens(`"svm-0123456789abcdef0"`))
		},
	},
	"aws_fsx_openzfs_snapshot": {
		Reasons: []string{
			`volume_id is a plain string in the schema but the provider validates its length against the real 23-character fsvol-… shape (validate: "expected length of volume_id to be in the range (23 - 23)")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("volume_id", exprTokens(`"fsvol-0123456789abcdef0"`))
		},
	},
	"aws_fsx_openzfs_volume": {
		Reasons: []string{
			`parent_volume_id is a plain string in the schema, but the provider's ValidateFunc rejects any literal placeholder outright for a root-level volume (validate: "must specify a filesystem id i.e. fs-12345678", regardless of length or fs- shape) - a real cross-reference to aws_fsx_openzfs_file_system.app's own root_volume_id is an unknown value at validate time, which the ValidateFunc never runs against, so it is both more honest and the only value that passes`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("parent_volume_id", exprTokens(`aws_fsx_openzfs_file_system.app.root_volume_id`))
		},
	},
	"aws_fsx_s3_access_point_attachment": {
		Reasons: []string{
			`type is a plain string in the schema but the provider validates it against the enum ["OPENZFS" "ONTAP"] (validate: "Invalid String Enum Value"); openzfs_configuration is a required block for type = "OPENZFS" that the schema does not mark required, its own volume_id is required with no schema-visible default, and its nested file_system_identity block is itself required with a required "type" of its own (validate: "Block ... must have a configuration value" at each level)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"OPENZFS"`))
			cfg := body.AppendNewBlock("openzfs_configuration", nil)
			// aws_fsx_openzfs_volume.app is this same cohort's own coverage
			// row for AWS::FSx::Volume, so this is a real in-run reference
			// rather than a second placeholder ARN.
			cfg.Body().SetAttributeRaw("volume_id", exprTokens(`aws_fsx_openzfs_volume.app.id`))
			identity := cfg.Body().AppendNewBlock("file_system_identity", nil)
			identity.Body().SetAttributeRaw("type", exprTokens(`"POSIX"`))
		},
	},
	"aws_athena_data_catalog": {
		Reasons: []string{
			`type is a required enum (validate: "expected type to be one of [...]"), and the generic string placeholder is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"GLUE"`))
			body.SetAttributeRaw("parameters", exprTokens(`{}`))
		},
	},
	"aws_glue_catalog_database": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment - so the generic pass's parentRef wiring points it at that unrelated resource instead of giving it its own placeholder`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_glue_catalog_database"`, g.cohort)))
		},
	},
	"aws_glue_catalog_table": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment; database_name is a plain required string argument the schema does not mark as any other type's identity, so parentRef never connects it to the sibling aws_glue_catalog_database (whose own identity is the account-derived catalog_id:name composite, not a single "database_name"-named argument) and the generic pass gives it an unrelated placeholder instead`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_glue_catalog_table"`, g.cohort)))
			if database, ok := g.byType["aws_glue_catalog_database"]; ok {
				body.SetAttributeRaw("database_name", exprTokens(fmt.Sprintf("%s.name", database)))
			}
		},
	},
	"aws_glue_connection": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-glue-connection"`, g.cohort)))
		},
	},
	"aws_glue_crawler": {
		Reasons: []string{
			`schema requires only database_name, name and role; the provider also requires exactly one of catalog_target/delta_target/dynamodb_target/hudi_target/iceberg_target/jdbc_target/mongodb_target/s3_target (validate: "one of ... must be specified"), and s3_target requires a path`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			s3 := body.AppendNewBlock("s3_target", nil)
			s3.Body().SetAttributeRaw("path", exprTokens(fmt.Sprintf(`"s3://tofu-%s-cohort-glue-crawler/"`, g.cohort)))
		},
	},
	"aws_glue_data_catalog_encryption_settings": {
		Reasons: []string{
			`data_catalog_encryption_settings.encryption_at_rest.catalog_encryption_mode is a required enum (validate: "expected catalog_encryption_mode to be one of [...]"), and the generic string placeholder is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() != "data_catalog_encryption_settings" {
					continue
				}
				for _, inner := range blk.Body().Blocks() {
					if inner.Type() == "encryption_at_rest" {
						inner.Body().SetAttributeRaw("catalog_encryption_mode", exprTokens(`"DISABLED"`))
					}
				}
			}
		},
	},
	"aws_glue_job": {
		Reasons: []string{
			`role_arn does not match isRoleArg's "role"/"*_role_arn" alias (it is exactly "role_arn", one underscore short), so the generic pass leaves it as an unparseable placeholder string (validate: "is an invalid ARN"); command.script_location has no schema default and an empty string is not a valid S3 or local path`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ref, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(ref))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "command" {
					blk.Body().SetAttributeRaw("script_location", exprTokens(fmt.Sprintf(
						`"s3://tofu-%s-cohort-glue-job/script.py"`, g.cohort)))
				}
			}
		},
	},
	"aws_glue_ml_transform": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment; role_arn does not match isRoleArg's alias, the same gap as aws_glue_job above; parameters.transform_type is a required enum (validate: "expected transform_type to be one of [...]") the generic placeholder does not satisfy`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-glue-ml-transform"`, g.cohort)))
			if ref, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(ref))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "parameters" {
					blk.Body().SetAttributeRaw("transform_type", exprTokens(`"FIND_MATCHES"`))
				}
			}
			if database, ok := g.byType["aws_glue_catalog_database"]; ok {
				if table, ok := g.byType["aws_glue_catalog_table"]; ok {
					for _, blk := range body.Blocks() {
						if blk.Type() == "input_record_tables" {
							blk.Body().SetAttributeRaw("database_name", exprTokens(fmt.Sprintf("%s.name", database)))
							blk.Body().SetAttributeRaw("table_name", exprTokens(fmt.Sprintf("%s.name", table)))
						}
					}
				}
			}
		},
	},
	"aws_glue_trigger": {
		Reasons: []string{
			`type is a required enum (validate: "expected type to be one of [...]"), and the generic string placeholder is not a member; actions is a required block but its own contents (job_name or crawler_name) are all optional in the schema while the provider requires one of them in practice`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"ON_DEMAND"`))
			if job, ok := g.byType["aws_glue_job"]; ok {
				for _, blk := range body.Blocks() {
					if blk.Type() == "actions" {
						blk.Body().SetAttributeRaw("job_name", exprTokens(fmt.Sprintf("%s.name", job)))
					}
				}
			}
		},
	},
	"aws_kinesis_firehose_delivery_stream": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment; destination is a required enum naming which optional *_configuration block the provider actually reads (validate: "expected destination to be one of [...]"), and the matching extended_s3_configuration block is itself optional in the schema while the provider requires it in practice once destination = "extended_s3"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-firehose"`, g.cohort)))
			body.SetAttributeRaw("destination", exprTokens(`"extended_s3"`))
			s3 := body.AppendNewBlock("extended_s3_configuration", nil)
			s3.Body().SetAttributeRaw("bucket_arn", exprTokens(fmt.Sprintf(`"arn:aws:s3:::tofu-%s-cohort-firehose"`, g.cohort)))
			if ref, ok := g.iamRoleRefExpr(); ok {
				s3.Body().SetAttributeRaw("role_arn", exprTokens(ref))
			}
		},
	},
	"aws_kinesis_stream": {
		Reasons: []string{
			`shard_count has no schema default and is left unset by the generic pass, but the provider's own CustomizeDiff defaults stream_mode_details.stream_mode to "PROVISIONED" and then requires shard_count to be at least 1 (found only by running the generic pass's output through terraform apply, not validate - the check is plan-time, not schema-level)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("shard_count", exprTokens(`1`))
		},
	},
	"aws_kinesis_stream_consumer": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment; stream_arn is a required argument the schema alone cannot wire to the sibling aws_kinesis_stream (its identity is "name", not "stream_arn", so parentRef never connects the two)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-kinesis-stream-consumer"`, g.cohort)))
			if stream, ok := g.byType["aws_kinesis_stream"]; ok {
				body.SetAttributeRaw("stream_arn", exprTokens(fmt.Sprintf("%s.arn", stream)))
			}
		},
	},
	// ---- Registry-ratified (#40, #44, #65): fourth batch, Route53
	// ---- remainder and CloudFront -----------------------------------------
	"aws_cloudfront_distribution": {
		Reasons: []string{
			`default_cache_behavior.viewer_protocol_policy and restrictions.geo_restriction.restriction_type are both required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "default_cache_behavior":
					blk.Body().SetAttributeRaw("viewer_protocol_policy", exprTokens(`"allow-all"`))
				case "restrictions":
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "geo_restriction" {
							inner.Body().SetAttributeRaw("restriction_type", exprTokens(`"none"`))
						}
					}
				}
			}
		},
	},
	"aws_cloudfront_function": {
		Reasons: []string{
			`"runtime" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected runtime to be one of [...]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("runtime", exprTokens(`"cloudfront-js-2.0"`))
		},
	},
	"aws_cloudfront_monitoring_subscription": {
		Reasons: []string{
			`monitoring_subscription.realtime_metrics_subscription_config.realtime_metrics_subscription_status is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected ... to be one of [...]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() != "monitoring_subscription" {
					continue
				}
				for _, inner := range blk.Body().Blocks() {
					if inner.Type() == "realtime_metrics_subscription_config" {
						inner.Body().SetAttributeRaw("realtime_metrics_subscription_status", exprTokens(`"Enabled"`))
					}
				}
			}
		},
	},
	"aws_cloudfront_anycast_ip_list": {
		Reasons: []string{
			`"ip_count" is a required number the schema does not constrain, but the provider validates it against a fixed set (validate: "Attribute ip_count value must be one of: [3 21]"); the generic placeholder 0 matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("ip_count", exprTokens(`3`))
		},
	},
	"aws_cloudfront_connection_function": {
		Reasons: []string{
			`connection_function_config is Optional+Computed in the wire schema, but the provider requires it present in practice (validate: "Block connection_function_config must have a configuration value as the provider has marked it as required"), and its comment/runtime members are both required with no default the generic pass can infer`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			cfg := body.AppendNewBlock("connection_function_config", nil)
			cfg.Body().SetAttributeRaw("comment", exprTokens(fmt.Sprintf(`"tofu %s cohort connection function"`, g.cohort)))
			cfg.Body().SetAttributeRaw("runtime", exprTokens(`"cloudfront-js-2.0"`))
		},
	},
	"aws_cloudfront_origin_access_control": {
		Reasons: []string{
			`origin_access_control_origin_type, signing_behavior and signing_protocol are all required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]"); the generic placeholder string matches none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("origin_access_control_origin_type", exprTokens(`"s3"`))
			body.SetAttributeRaw("signing_behavior", exprTokens(`"always"`))
			body.SetAttributeRaw("signing_protocol", exprTokens(`"sigv4"`))
		},
	},
	"aws_cloudfront_multitenant_distribution": {
		Reasons: []string{
			`viewer_certificate, tenant_config and default_cache_behavior are all Optional+Computed in the wire schema, but the provider requires all three present in practice (validate: "Block ... must have a configuration value as the provider has marked it as required") — a newer plugin-framework resource whose Required bit the generic required-only pass does not see the same way it sees aws_cloudfront_distribution's SDKv2 schema above; default_cache_behavior's own nested allowed_methods block is the same shape one level down (validate: "Block default_cache_behavior[0].allowed_methods must have a configuration value as the provider has marked it as required"), with items and cached_methods both required sets no default the generic pass can infer`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.AppendNewBlock("viewer_certificate", nil)
			body.AppendNewBlock("tenant_config", nil)
			dcb := body.AppendNewBlock("default_cache_behavior", nil)
			dcb.Body().SetAttributeRaw("target_origin_id", exprTokens(`"placeholder"`))
			dcb.Body().SetAttributeRaw("viewer_protocol_policy", exprTokens(`"allow-all"`))
			am := dcb.Body().AppendNewBlock("allowed_methods", nil)
			am.Body().SetAttributeRaw("items", exprTokens(`["GET", "HEAD"]`))
			am.Body().SetAttributeRaw("cached_methods", exprTokens(`["GET", "HEAD"]`))
		},
	},
	"aws_cloudfront_realtime_log_config": {
		Reasons: []string{
			`"sampling_rate" is a required number the schema constrains only to a type, but the provider validates it is in range 1-100 (validate: "expected sampling_rate to be in the range (1 - 100), got 0"); endpoint.stream_type is a required string the provider validates against a fixed one-member set (validate: "expected stream_type to be one of [Kinesis]"); and kinesis_stream_config's role_arn/stream_arn are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN") the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("sampling_rate", exprTokens(`50`))
			for _, blk := range body.Blocks() {
				if blk.Type() != "endpoint" {
					continue
				}
				blk.Body().SetAttributeRaw("stream_type", exprTokens(`"Kinesis"`))
				for _, inner := range blk.Body().Blocks() {
					if inner.Type() != "kinesis_stream_config" {
						continue
					}
					inner.Body().SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:iam::000000000000:role/tofu-%s-cohort-realtime-log-role"`, g.cohort)))
					inner.Body().SetAttributeRaw("stream_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:kinesis:us-east-1:000000000000:stream/tofu-%s-cohort-realtime-log-stream"`, g.cohort)))
				}
			}
		},
	},
	"aws_cloudfront_trust_store": {
		Reasons: []string{
			`ca_certificates_bundle_source is Optional+Computed in the wire schema, but the provider requires it present in practice (validate: "Block ca_certificates_bundle_source must have a configuration value as the provider has marked it as required"), and its nested ca_certificates_bundle_s3_location block's bucket/key/region are all required with no default the generic pass can infer`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			src := body.AppendNewBlock("ca_certificates_bundle_source", nil)
			loc := src.Body().AppendNewBlock("ca_certificates_bundle_s3_location", nil)
			loc.Body().SetAttributeRaw("bucket", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-trust-store-bundle"`, g.cohort)))
			loc.Body().SetAttributeRaw("key", exprTokens(`"ca-bundle.pem"`))
			loc.Body().SetAttributeRaw("region", exprTokens(`"us-east-1"`))
		},
	},
	"aws_cloudfront_vpc_origin": {
		Reasons: []string{
			`vpc_origin_endpoint_config is Optional+Computed in the wire schema, but the provider requires it present in practice (validate: "Block vpc_origin_endpoint_config must have a configuration value as the provider has marked it as required"), and its arn/http_port/https_port/name/origin_protocol_policy members are all required with no default the generic pass can infer; its own nested origin_ssl_protocols block is the same shape one level down (validate: "Block vpc_origin_endpoint_config[0].origin_ssl_protocols must have a configuration value as the provider has marked it as required"), with items and quantity both required with no default either`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			cfg := body.AppendNewBlock("vpc_origin_endpoint_config", nil)
			cfg.Body().SetAttributeRaw("arn", exprTokens(fmt.Sprintf(
				`"arn:aws:ec2:us-east-1:000000000000:vpc-endpoint-service/vpce-svc-tofu-%s-cohort"`, g.cohort)))
			cfg.Body().SetAttributeRaw("http_port", exprTokens(`80`))
			cfg.Body().SetAttributeRaw("https_port", exprTokens(`443`))
			cfg.Body().SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-vpc-origin"`, g.cohort)))
			cfg.Body().SetAttributeRaw("origin_protocol_policy", exprTokens(`"https-only"`))
			ssl := cfg.Body().AppendNewBlock("origin_ssl_protocols", nil)
			ssl.Body().SetAttributeRaw("items", exprTokens(`["TLSv1.2"]`))
			ssl.Body().SetAttributeRaw("quantity", exprTokens(`1`))
		},
	},
	"aws_route53_health_check": {
		Reasons: []string{
			`"type" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [...]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"HTTP"`))
		},
	},
	"aws_route53_resolver_endpoint": {
		Reasons: []string{
			`"direction" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected direction to be one of [...]"); and ip_address is a set-nesting block with MinItems 2 - the generic pass emits two blocks, but both carry the identical placeholder subnet_id, and a set collapses identical elements to one, so the provider sees only 1 (validate: "Attribute ip_address requires 2 item minimum, but config has only 1 declared")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("direction", exprTokens(`"INBOUND"`))
			i := 0
			for _, blk := range body.Blocks() {
				if blk.Type() != "ip_address" {
					continue
				}
				i++
				blk.Body().SetAttributeRaw("subnet_id", exprTokens(fmt.Sprintf(`"subnet-tofu%02dcohortplaceholder"`, i)))
			}
		},
	},
	"aws_route53_resolver_rule": {
		Reasons: []string{
			`"rule_type" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected rule_type to be one of [...]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule_type", exprTokens(`"FORWARD"`))
		},
	},
	"aws_route53_key_signing_key": {
		Reasons: []string{
			`"key_management_service_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("key_management_service_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:kms:us-east-1:000000000000:key/tofu-%s-cohort-ksk"`, g.cohort)))
		},
	},
	"aws_route53_resolver_firewall_rule": {
		Reasons: []string{
			`"action" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected action to be one of [ALLOW BLOCK ALERT]"); the generic placeholder string matches none of them, and only ALLOW/ALERT need no further conditionally-required block_response. firewall_domain_list_id is Optional in the schema (it names the standard-rule shape; dns_threat_protection names the advanced-rule shape instead), but the provider requires exactly one of the two set - not caught by "terraform validate" itself, only surfaced at apply (validate: "one of firewall_domain_list_id or dns_threat_protection must be specified") - and the fixture picks the standard-rule shape, the one internal/live/identity/table.go's own parent-derived aws_route53_resolver_firewall_rule entry composes an identity for.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("action", exprTokens(`"ALLOW"`))
			body.SetAttributeRaw("firewall_domain_list_id", exprTokens(`"placeholder"`))
		},
	},
	"aws_route53_resolver_query_log_config": {
		Reasons: []string{
			`"destination_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("destination_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:logs:us-east-1:000000000000:log-group:/tofu-%s-cohort-query-logs"`, g.cohort)))
		},
	},
	"aws_route53recoverycontrolconfig_safety_rule": {
		Reasons: []string{
			`asserted_controls and gating_controls are both Optional in the schema, but the provider requires exactly one of them set (validate: "one of asserted_controls,gating_controls must be specified"); a gating rule also needs target_controls, the list of controls it gates, which the schema likewise leaves Optional; rule_config.type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [ATLEAST AND OR]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			gating := fmt.Sprintf(`["arn:aws:route53-recovery-control::000000000000:controlpanel/tofu-%s-cohort-panel/routingcontrol/tofu-%s-cohort-gating"]`, g.cohort, g.cohort)
			target := fmt.Sprintf(`["arn:aws:route53-recovery-control::000000000000:controlpanel/tofu-%s-cohort-panel/routingcontrol/tofu-%s-cohort-target"]`, g.cohort, g.cohort)
			body.SetAttributeRaw("gating_controls", exprTokens(gating))
			body.SetAttributeRaw("target_controls", exprTokens(target))
			for _, blk := range body.Blocks() {
				if blk.Type() == "rule_config" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"ATLEAST"`))
				}
			}
		},
	},

	// EC2 networking beyond the core batch (issue #65). Same two failure
	// shapes as every batch above: a Required argument the wire schema
	// types as a plain string or number but the provider validates against
	// a closed enum, a CIDR/IP/ARN shape, or a range - or a nested block
	// left Optional in the schema while the provider requires its contents,
	// or ExactlyOneOf/AtLeastOneOf combinations the wire schema does not
	// express at all.
	"aws_customer_gateway": {
		Reasons: []string{
			`type is Required and the provider validates it against a closed enum (validate: "expected type to be one of [\"ipsec.1\"]"); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"ipsec.1"`))
		},
	},
	"aws_ec2_client_vpn_endpoint": {
		Reasons: []string{
			`authentication_options is a required block whose "type" argument the provider validates against a closed enum (validate: "expected type to be one of [...]"); the certificate-authentication member also needs root_certificate_chain_arn in practice, which the schema leaves Optional; server_certificate_arn is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("server_certificate_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:acm:us-east-1:000000000000:certificate/tofu-%s-cohort-server-cert"`, g.cohort)))
			for _, blk := range body.Blocks() {
				if blk.Type() == "authentication_options" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"certificate-authentication"`))
					blk.Body().SetAttributeRaw("root_certificate_chain_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:acm:us-east-1:000000000000:certificate/tofu-%s-cohort-root-cert"`, g.cohort)))
				}
			}
		},
	},
	"aws_ec2_client_vpn_route": {
		Reasons: []string{
			`destination_cidr_block is Required and the provider validates it is a well-formed CIDR (validate: "is not a valid CIDR block"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("destination_cidr_block", exprTokens(`"10.1.0.0/24"`))
		},
	},
	"aws_ec2_managed_prefix_list": {
		Reasons: []string{
			`address_family is Required and the provider validates it against a closed enum (validate: "expected address_family to be one of [\"IPv4\" \"IPv6\"]"); max_entries is Required and the provider validates it is at least 1 (validate: "expected max_entries to be at least (1), got 0"), the generic pass's zero-value number`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("address_family", exprTokens(`"IPv4"`))
			body.SetAttributeRaw("max_entries", exprTokens(`5`))
		},
	},
	"aws_ec2_managed_prefix_list_entry": {
		Reasons: []string{
			`cidr is Required and the provider validates it is a well-formed CIDR (validate: "to be a valid CIDR Value"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cidr", exprTokens(`"10.0.3.0/24"`))
		},
	},
	"aws_ec2_transit_gateway_connect_peer": {
		Reasons: []string{
			`peer_address is Required and the provider validates it is a well-formed IP (validate: "expected peer_address to contain a valid IP"); inside_cidr_blocks is Required and the provider validates each element is a well-formed CIDR inside a fixed range, 169.254.0.0/16 for an IPv4 element (validate: "is not a valid CIDR block", then "IPv4 range must be from range 169.254.0.0/16"); the generic pass's single "placeholder" element satisfies neither check`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("peer_address", exprTokens(`"10.0.0.1"`))
			body.SetAttributeRaw("inside_cidr_blocks", exprTokens(`["169.254.6.0/29"]`))
		},
	},
	"aws_ec2_transit_gateway_metering_policy_entry": {
		Reasons: []string{
			`metered_account is Required and the provider validates it against a closed enum (validate: "Invalid String Enum Value", valid values: source-attachment-owner, destination-attachment-owner, transit-gateway-owner); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("metered_account", exprTokens(`"source-attachment-owner"`))
		},
	},
	"aws_ec2_transit_gateway_route": {
		Reasons: []string{
			`destination_cidr_block is Required and the provider validates it is a well-formed CIDR (validate: "is not a valid CIDR block"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("destination_cidr_block", exprTokens(`"0.0.0.0/0"`))
		},
	},
	"aws_flow_log": {
		Reasons: []string{
			`every argument naming what the flow log watches (vpc_id, subnet_id, eni_id, transit_gateway_id, transit_gateway_attachment_id, regional_nat_gateway_id) is Optional in the schema, so the generic pass renders an empty body, but the provider requires exactly one (validate: "Invalid combination of arguments": "one of ... must be specified")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("vpc_id", exprTokens(`"vpc-0123456789abcdef0"`))
		},
	},
	"aws_network_acl_rule": {
		Reasons: []string{
			`cidr_block and ipv6_cidr_block are both Optional in the schema, but the provider requires exactly one (validate: "Invalid combination of arguments": "one of cidr_block,ipv6_cidr_block must be specified"); protocol is Required and the provider validates it against its own protocol-number/name table (validate: "unsupported NACL protocol"); rule_action is Required and validated against a closed enum (validate: "expected rule_action to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cidr_block", exprTokens(`"10.0.0.0/16"`))
			body.SetAttributeRaw("protocol", exprTokens(`"-1"`))
			body.SetAttributeRaw("rule_action", exprTokens(`"allow"`))
		},
	},
	"aws_vpc_dhcp_options": {
		Reasons: []string{
			`domain_name, domain_name_servers, ipv6_address_preferred_lease_time, netbios_name_servers, netbios_node_type and ntp_servers are all Optional in the schema, so the generic pass renders an empty body, but the provider requires at least one (validate: "Missing required argument": "one of ... must be specified")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("domain_name", exprTokens(`"example.com"`))
		},
	},
	"aws_vpc_ipam": {
		Reasons: []string{
			`operating_regions is a required block, but its own region_name argument is validated by the provider as a well-formed AWS region (validate: "doesn't look like AWS Region"), and the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "operating_regions" {
					blk.Body().SetAttributeRaw("region_name", exprTokens(`"us-east-1"`))
				}
			}
		},
	},

	// ---- compute-platforms cohort (fifth ratification batch) -----------
	"aws_apprunner_service": {
		Reasons: []string{
			`source_configuration is a required block with no required attributes the schema itself names inside it, but the provider requires exactly one of source_configuration.code_repository or source_configuration.image_repository set (validate: "one of ... must be specified"); the generic pass emits the outer block empty`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() != "source_configuration" {
					continue
				}
				img := blk.Body().AppendNewBlock("image_repository", nil)
				img.Body().SetAttributeRaw("image_identifier", exprTokens(`"public.ecr.aws/aws-containers/hello-app-runner:latest"`))
				img.Body().SetAttributeRaw("image_repository_type", exprTokens(`"ECR_PUBLIC"`))
			}
		},
	},
	"aws_apprunner_vpc_connector": {
		Reasons: []string{
			`vpc_connector_name is a required string the schema does not length-constrain, but the provider validates it is at most 40 characters (validate: "expected length of vpc_connector_name to be in the range (4 - 40)"); the generic "tofu-<cohort>-cohort-<type>" name is 44`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("vpc_connector_name", exprTokens(`"tofu-cp-apprunner-vpc-conn"`))
		},
	},
	"aws_apprunner_vpc_ingress_connection": {
		Reasons: []string{
			`service_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("service_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:apprunner:us-east-1:000000000000:service/tofu-%s-cohort-apprunner-service/00000000000000000000000000000000"`, g.cohort)))
		},
	},
	"aws_batch_compute_environment": {
		Reasons: []string{
			`type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [MANAGED UNMANAGED]"); UNMANAGED needs no further compute_resources block, the smaller of the two shapes`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"UNMANAGED"`))
		},
	},
	"aws_batch_job_definition": {
		Reasons: []string{
			`type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [container multinode]"); a container job definition also needs container_properties, a JSON string the schema does not otherwise require, with at least image and a resource sizing (validate: "invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"container"`))
			body.SetAttributeRaw("container_properties", exprTokens(`jsonencode({
    image  = "busybox"
    vcpus  = 1
    memory = 512
  })`))
		},
	},
	"aws_batch_job_queue": {
		Reasons: []string{
			`state is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "Attribute state value must be one of: [\"ENABLED\" \"DISABLED\"]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("state", exprTokens(`"ENABLED"`))
		},
	},
	"aws_emr_security_configuration": {
		Reasons: []string{
			`configuration is a required string the schema does not otherwise constrain, but the provider validates it is well-formed JSON (validate: "contains an invalid JSON"); the generic string placeholder is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("configuration", exprTokens(`jsonencode({
    EncryptionConfiguration = {
      EnableInTransitEncryption = false
      EnableAtRestEncryption    = false
    }
  })`))
		},
	},
	"aws_emr_studio": {
		Reasons: []string{
			`auth_mode is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected auth_mode to be one of [SSO IAM]"); service_role is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("auth_mode", exprTokens(`"SSO"`))
			body.SetAttributeRaw("service_role", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-emr-studio"`, g.cohort)))
		},
	},
	"aws_emrcontainers_virtual_cluster": {
		Reasons: []string{
			`container_provider.type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [EKS]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() != "container_provider" {
					continue
				}
				blk.Body().SetAttributeRaw("type", exprTokens(`"EKS"`))
			}
		},
	},
	"aws_lightsail_container_service": {
		Reasons: []string{
			`power is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected power to be one of [...]"); scale is a required number the schema does not range-constrain, but the provider validates it is between 1 and 20 (validate: "expected scale to be in the range (1 - 20), got 0")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("power", exprTokens(`"nano"`))
			body.SetAttributeRaw("scale", exprTokens(`1`))
		},
	},
	"aws_lightsail_database": {
		Reasons: []string{
			`master_database_name is a required string the schema does not otherwise constrain, but the provider validates it against a stricter character set than the generic "tofu-<cohort>-cohort-<type>" placeholder's hyphens allow (validate: "Subsequent characters can be letters, underscores, or digits")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("master_database_name", exprTokens(`"tofu_compute_platforms_database"`))
		},
	},
	"aws_lightsail_instance": {
		Reasons: []string{
			`availability_zone is a required string the schema does not constrain, but the provider validates it names an AZ within the configured provider region (validate/plan: "availability_zone must be within the same region as provider region: us-east-1"); the generic placeholder string does not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("availability_zone", exprTokens(`"us-east-1a"`))
		},
	},
	"aws_lightsail_distribution": {
		Reasons: []string{
			`default_cache_behavior.behavior is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected default_cache_behavior.0.behavior to be one of [dont-cache cache]"); origin.region_name is a required string the schema does not constrain, but the provider validates it looks like an AWS region (validate: "doesn't look like AWS Region")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "default_cache_behavior":
					blk.Body().SetAttributeRaw("behavior", exprTokens(`"cache"`))
				case "origin":
					blk.Body().SetAttributeRaw("region_name", exprTokens(`"us-east-1"`))
				}
			}
		},
	},

	// ---- Sixth batch, security and secrets (issue #65). Two shapes of
	// ---- fix: enum/format validators the generic pass's placeholders
	// ---- cannot satisfy, and cross-references the generic pass's
	// ---- parentRef wiring gets wrong within this cohort - several of this
	// ---- batch's own parent-derived rows (aws_guardduty_organization_configuration,
	// ---- aws_guardduty_filter/ipset/threatintelset/publishing_destination/member)
	// ---- have a plain "detector_id" argument that coincidentally matches
	// ---- aws_guardduty_organization_configuration's own identity argument
	// ---- name too (Components: []Component{attr("detector_id")}), so
	// ---- parentRef wires every one of them to that resource's own
	// ---- detector_id echo instead of the real aws_guardduty_detector
	// ---- marker - the same collision class as aws_glue_catalog_database's
	// ---- entry above, fixed the same way.
	"aws_acmpca_certificate_authority": {
		Reasons: []string{
			`certificate_authority_configuration.key_algorithm and .signing_algorithm are plain strings in the schema but the provider validates both against fixed enums (validate: "expected key_algorithm/signing_algorithm to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "certificate_authority_configuration" {
					blk.Body().SetAttributeRaw("key_algorithm", exprTokens(`"RSA_2048"`))
					blk.Body().SetAttributeRaw("signing_algorithm", exprTokens(`"SHA256WITHRSA"`))
				}
			}
		},
	},
	"aws_acmpca_certificate_authority_certificate": {
		Reasons: []string{
			`certificate_authority_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), which the generic pass's placeholder name is not - a real cross-reference to aws_acmpca_certificate_authority.app's own arn is both the fix and the point of this coverage row (the parent-derived composite this batch ratified)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ca, ok := g.byType["aws_acmpca_certificate_authority"]; ok {
				body.SetAttributeRaw("certificate_authority_arn", exprTokens(fmt.Sprintf("%s.arn", ca)))
			}
		},
	},
	"aws_acmpca_policy": {
		Reasons: []string{
			`resource_arn is not wired to any resource by the generic pass (aws_acmpca_certificate_authority is server-assigned, so parentRef's identity-argument match never fires - see this file's batch header comment); policy is a plain string in the schema but the provider validates it is well-formed JSON (validate: "contains an invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ca, ok := g.byType["aws_acmpca_certificate_authority"]; ok {
				body.SetAttributeRaw("resource_arn", exprTokens(fmt.Sprintf("%s.arn", ca)))
			}
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = "acm-pca:IssueCertificate"
    }]
  })`)))
		},
	},
	"aws_guardduty_organization_configuration": {
		Reasons: []string{
			`auto_enable_organization_members is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected auto_enable_organization_members to be one of [NEW ALL NONE]"); detector_id collides with this same type's own identity argument name (see this file's batch header comment), so the generic pass gives it a placeholder string instead of the real aws_guardduty_detector.app.id it should carry`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("auto_enable_organization_members", exprTokens(`"NEW"`))
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
		},
	},
	"aws_guardduty_filter": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; action is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected action to be one of [NOOP ARCHIVE]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("action", exprTokens(`"NOOP"`))
		},
	},
	"aws_guardduty_ipset": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; format is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected format to be one of [TXT STIX OTX_CSV ALIEN_VAULT PROOF_POINT FIRE_EYE]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("format", exprTokens(`"TXT"`))
		},
	},
	"aws_guardduty_threatintelset": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; format is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected format to be one of [TXT STIX OTX_CSV ALIEN_VAULT PROOF_POINT FIRE_EYE]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("format", exprTokens(`"TXT"`))
		},
	},
	"aws_guardduty_malware_protection_plan": {
		Reasons: []string{
			`protected_resource is a required block the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block protected_resource must have a configuration value"), and its own nested s3_bucket.bucket_name is required with no schema-visible default`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			pr := body.AppendNewBlock("protected_resource", nil)
			s3 := pr.Body().AppendNewBlock("s3_bucket", nil)
			s3.Body().SetAttributeRaw("bucket_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-malware-bucket"`, g.cohort)))
		},
	},
	"aws_guardduty_member": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_guardduty_organization_admin_account": {
		Reasons: []string{
			`admin_account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("admin_account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_guardduty_publishing_destination": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; destination_arn and kms_key_arn are plain strings in the schema but the provider validates both are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("destination_arn", exprTokens(`"arn:aws:s3:::tofu-security-cohort-guardduty-findings-bucket"`))
			body.SetAttributeRaw("kms_key_arn", exprTokens(`"arn:aws:kms:us-east-1:000000000000:key/1234abcd-12ab-34cd-56ef-1234567890ab"`))
		},
	},
	"aws_inspector2_filter": {
		Reasons: []string{
			`filter_criteria is a required block the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block filter_criteria must have a configuration value"); action is a plain string in the schema but the provider validates it against a fixed enum (validate: "Invalid String Enum Value", valid values [NONE SUPPRESS])`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("action", exprTokens(`"NONE"`))
			body.AppendNewBlock("filter_criteria", nil)
		},
	},
	"aws_inspector2_member_association": {
		Reasons: []string{
			`account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_kms_replica_key": {
		Reasons: []string{
			`primary_key_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); no aws_kms_key/aws_kms_external_key coverage row exists in this cohort to reference (KMS's own marker types are covered by the pre-registry v0 table and the KMS-remainder rows above), so this is a realistic literal rather than a cross-reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("primary_key_arn", exprTokens(`"arn:aws:kms:us-west-2:000000000000:key/1234abcd-12ab-34cd-56ef-1234567890ab"`))
		},
	},
	"aws_macie2_classification_job": {
		Reasons: []string{
			`job_type is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected job_type to be one of [ONE_TIME SCHEDULED]"); s3_job_definition.bucket_definitions.account_id/.buckets are required with no schema-visible default once the parent block is present`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("job_type", exprTokens(`"ONE_TIME"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "s3_job_definition" {
					bd := blk.Body().AppendNewBlock("bucket_definitions", nil)
					bd.Body().SetAttributeRaw("account_id", exprTokens(`"000000000000"`))
					bd.Body().SetAttributeRaw("buckets", exprTokens(fmt.Sprintf(`["tofu-%s-cohort-macie-bucket"]`, g.cohort)))
				}
			}
		},
	},
	"aws_macie2_findings_filter": {
		Reasons: []string{
			`action is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected action to be one of [ARCHIVE NOOP]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("action", exprTokens(`"ARCHIVE"`))
		},
	},
	"aws_secretsmanager_secret_policy": {
		Reasons: []string{
			`secret_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), which the generic pass's placeholder name is not - a real cross-reference to aws_secretsmanager_secret.app's own arn is both the fix and the point of this coverage row (the parent-derived composite this batch ratified); policy is a plain string in the schema but the provider validates it is well-formed JSON (validate: "contains an invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if secret, ok := g.byType["aws_secretsmanager_secret"]; ok {
				body.SetAttributeRaw("secret_arn", exprTokens(fmt.Sprintf("%s.arn", secret)))
			}
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = "secretsmanager:GetSecretValue"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_secretsmanager_secret_rotation": {
		Reasons: []string{
			`secret_id is not wired to any resource by the generic pass (aws_secretsmanager_secret is a marker type with no Components, so parentRef's identity-argument match never fires); rotation_rules is present but empty, and the provider requires one of automatically_after_days/schedule_expression set (validate: "one of ... must be specified")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if secret, ok := g.byType["aws_secretsmanager_secret"]; ok {
				body.SetAttributeRaw("secret_id", exprTokens(fmt.Sprintf("%s.id", secret)))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "rotation_rules" {
					blk.Body().SetAttributeRaw("automatically_after_days", exprTokens(`30`))
				}
			}
		},
	},
	"aws_securityhub_automation_rule": {
		Reasons: []string{
			`actions and criteria are both required blocks the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block actions/criteria must have a configuration value"); every field inside both is itself optional, so empty blocks are enough`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.AppendNewBlock("actions", nil)
			body.AppendNewBlock("criteria", nil)
		},
	},
	"aws_securityhub_automation_rule_v2": {
		Reasons: []string{
			`action and criteria are both required blocks the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block criteria/action must have a configuration value"); action.type and criteria.ocsf_finding_criteria_json are each required once their parent block is present; rule_order is a plain number in the schema but the provider validates it against a 1-1000 range (validate: "value must be between 1.000000 and 1000.000000", the generic pass's zero placeholder is out of range)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule_order", exprTokens(`1`))
			action := body.AppendNewBlock("action", nil)
			action.Body().SetAttributeRaw("type", exprTokens(`"FINDING_FIELDS_UPDATE"`))
			criteria := body.AppendNewBlock("criteria", nil)
			criteria.Body().SetAttributeRaw("ocsf_finding_criteria_json", exprTokens(`"{}"`))
		},
	},
	"aws_securityhub_configuration_policy_association": {
		Reasons: []string{
			`target_id is a plain string in the schema but the provider validates it is a root, OU or account id (validate: "Target ID must be a valid root, organizational unit or account id"); policy_id is a plain string in the schema but the provider validates it is either a UUID or the literal "SELF_MANAGED_SECURITY_HUB" (validate: "expected \"policy_id\" to be a valid UUID" / "expected policy_id to be one of [SELF_MANAGED_SECURITY_HUB]"), and this batch does not ratify aws_securityhub_configuration_policy (see the cohort README), so there is no real policy id to reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("target_id", exprTokens(`"000000000000"`))
			body.SetAttributeRaw("policy_id", exprTokens(`"SELF_MANAGED_SECURITY_HUB"`))
		},
	},
	"aws_securityhub_connector_v2": {
		Reasons: []string{
			`connector_provider is a required block the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block connector_provider must have a configuration value"); its own service_now.instance_name/.secret_arn are required once the block is present - secret_arn references aws_secretsmanager_secret.app's own arn, a real in-cohort value rather than a second placeholder ARN`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			cp := body.AppendNewBlock("connector_provider", nil)
			sn := cp.Body().AppendNewBlock("service_now", nil)
			sn.Body().SetAttributeRaw("instance_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort"`, g.cohort)))
			if secret, ok := g.byType["aws_secretsmanager_secret"]; ok {
				sn.Body().SetAttributeRaw("secret_arn", exprTokens(fmt.Sprintf("%s.arn", secret)))
			}
		},
	},
	"aws_securityhub_member": {
		Reasons: []string{
			`account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_securityhub_organization_admin_account": {
		Reasons: []string{
			`admin_account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("admin_account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_securityhub_standards_control": {
		Reasons: []string{
			`standards_control_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); control_status is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected control_status to be one of [ENABLED DISABLED]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("standards_control_arn", exprTokens(`"arn:aws:securityhub:us-east-1:000000000000:control/cis-aws-foundations-benchmark/v/1.2.0/1.10"`))
			body.SetAttributeRaw("control_status", exprTokens(`"ENABLED"`))
		},
	},
	"aws_securityhub_standards_control_association": {
		Reasons: []string{
			`association_status is a plain string in the schema but the provider validates it against a fixed enum (validate: "Invalid String Enum Value", valid values [ENABLED DISABLED]); standards_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "Invalid ARN Value")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("association_status", exprTokens(`"ENABLED"`))
			body.SetAttributeRaw("standards_arn", exprTokens(`"arn:aws:securityhub:us-east-1:000000000000:control/cis-aws-foundations-benchmark/v/1.2.0"`))
		},
	},
	"aws_ssm_patch_group": {
		Reasons: []string{
			`baseline_id is not wired to any resource by the generic pass (aws_ssm_patch_baseline is a marker type with no Components, so parentRef's identity-argument match never fires) - a real cross-reference to aws_ssm_patch_baseline.app's own id is both the fix and the point of this coverage row (the parent-derived composite this batch ratified)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if baseline, ok := g.byType["aws_ssm_patch_baseline"]; ok {
				body.SetAttributeRaw("baseline_id", exprTokens(fmt.Sprintf("%s.id", baseline)))
			}
		},
	},
	"aws_ssm_resource_data_sync": {
		Reasons: []string{
			`s3_destination.region is a plain string in the schema but the provider validates it looks like an AWS region (validate: "doesn't look like AWS Region")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "s3_destination" {
					blk.Body().SetAttributeRaw("region", exprTokens(`"us-east-1"`))
				}
			}
		},
	},
	"aws_ssm_service_setting": {
		Reasons: []string{
			`setting_id is a plain string in the schema but the provider validates it against two rules at once: it must begin with "/ssm/" and (per a separate check) parse as an ARN once the AWS provider prefixes it - a bare "/ssm/..." path is what the resource's own documented example uses`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("setting_id", exprTokens(`"/ssm/parameter-store/high-throughput-enabled"`))
		},
	},
	"aws_wafv2_ip_set": {
		Reasons: []string{
			`ip_address_version and scope are plain strings in the schema but the provider validates both against fixed enums (validate: "expected ip_address_version to be one of [IPV4 IPV6]" / "expected scope to be one of [CLOUDFRONT REGIONAL]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("ip_address_version", exprTokens(`"IPV4"`))
			body.SetAttributeRaw("scope", exprTokens(`"REGIONAL"`))
		},
	},
	"aws_wafv2_regex_pattern_set": {
		Reasons: []string{
			`scope is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected scope to be one of [CLOUDFRONT REGIONAL]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scope", exprTokens(`"REGIONAL"`))
		},
	},
	"aws_wafv2_rule_group": {
		Reasons: []string{
			`scope is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected scope to be one of [CLOUDFRONT REGIONAL]"); capacity is optional/computed in the schema, rendered as the generic pass's numeric zero placeholder, but the provider validates it is at least 1 (validate: "expected capacity to be at least (1), got 0")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scope", exprTokens(`"REGIONAL"`))
			body.SetAttributeRaw("capacity", exprTokens(`100`))
		},
	},
	"aws_wafv2_web_acl": {
		Reasons: []string{
			`scope is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected scope to be one of [CLOUDFRONT REGIONAL]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scope", exprTokens(`"REGIONAL"`))
		},
	},
	"aws_wafv2_web_acl_rule": {
		Reasons: []string{
			`web_acl_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), which the generic pass's placeholder name is not - a real cross-reference to aws_wafv2_web_acl.app's own arn is both the fix and the point of this coverage row (the parent-derived composite this batch ratified)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if acl, ok := g.byType["aws_wafv2_web_acl"]; ok {
				body.SetAttributeRaw("web_acl_arn", exprTokens(fmt.Sprintf("%s.arn", acl)))
			}
		},
	},
	"aws_vpc_ipam_pool": {
		Reasons: []string{
			`address_family is Required and the provider validates it against a closed enum (validate: "expected address_family to be one of [\"ipv4\" \"ipv6\"]"); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("address_family", exprTokens(`"ipv4"`))
		},
	},
	"aws_vpc_ipam_resource_discovery": {
		Reasons: []string{
			`operating_regions is a required block, the same shape as aws_vpc_ipam above; region_name is validated as a well-formed AWS region and the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "operating_regions" {
					blk.Body().SetAttributeRaw("region_name", exprTokens(`"us-east-1"`))
				}
			}
		},
	},

	// Developer tools batch (issue #65). Every argument below is
	// Required-but-Optional-shaped in the wire schema, validated against a
	// closed enum or a real-value format check the schema alone does not
	// carry - the same failure shape issue #56 already named for the
	// batches above.
	"aws_codeartifact_repository_permissions_policy": {
		Reasons: []string{
			`"policy_document" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"policy_document\" contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_document", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "codeartifact:ReadFromRepository"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_codebuild_fleet": {
		Reasons: []string{
			`base_capacity is Required and the provider validates it is at least 1 (validate: "expected base_capacity to be at least (1), got 0"), but the schema types it only as a number, so the generic pass's zero placeholder fails; compute_type and environment_type are both Required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("base_capacity", exprTokens(`1`))
			body.SetAttributeRaw("compute_type", exprTokens(`"BUILD_GENERAL1_SMALL"`))
			body.SetAttributeRaw("environment_type", exprTokens(`"LINUX_CONTAINER"`))
		},
	},
	"aws_codebuild_project": {
		Reasons: []string{
			`"service_role" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), the same isRoleArg gap aws_codedeploy_deployment_group's own service_role_arn does not have (this argument name lacks the "_role_arn" suffix the alias matches); artifacts.type, environment.compute_type, environment.type and source.type are all required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]"); source.buildspec is Optional in the schema, but the provider requires it when source.type is NO_SOURCE (apply-time only: "buildspec must be set when source's type is NO_SOURCE", not caught by "terraform validate")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if roleRef, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("service_role", exprTokens(roleRef))
			}
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "artifacts":
					blk.Body().SetAttributeRaw("type", exprTokens(`"NO_ARTIFACTS"`))
				case "environment":
					blk.Body().SetAttributeRaw("compute_type", exprTokens(`"BUILD_GENERAL1_SMALL"`))
					blk.Body().SetAttributeRaw("type", exprTokens(`"LINUX_CONTAINER"`))
				case "source":
					blk.Body().SetAttributeRaw("type", exprTokens(`"NO_SOURCE"`))
					blk.Body().SetAttributeRaw("buildspec", exprTokens(`"version: 0.2"`))
				}
			}
		},
	},

	"aws_vpn_connection": {
		Reasons: []string{
			`type is Required and the provider validates it against a closed enum (validate: "expected type to be one of [\"ipsec.1\" \"ipsec.1-aes256\"]"); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"ipsec.1"`))
		},
	},

	"aws_codepipeline": {
		Reasons: []string{
			`artifact_store.type is a required string the provider validates against a one-member enum (validate: "expected type to be one of [\"S3\"]"); "role_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), the same isRoleArg gap as aws_codebuild_project's service_role above ("role_arn" alone does not end "_role_arn"); each stage's action.category, action.owner and action.version are required strings the schema does not constrain to an enum or a length range, but the provider validates each (validate: "expected category to be one of [...]", "expected owner to be one of [...]", "expected length of ... version to be in the range (1 - 9)")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if roleRef, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(roleRef))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "artifact_store" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"S3"`))
				}
			}
			stageActions := []struct{ category, owner, provider string }{
				{"Source", "AWS", "S3"},
				{"Approval", "AWS", "Manual"},
			}
			i := 0
			for _, blk := range body.Blocks() {
				if blk.Type() != "stage" {
					continue
				}
				for _, action := range blk.Body().Blocks() {
					if action.Type() != "action" || i >= len(stageActions) {
						continue
					}
					sa := stageActions[i]
					action.Body().SetAttributeRaw("category", exprTokens(fmt.Sprintf("%q", sa.category)))
					action.Body().SetAttributeRaw("owner", exprTokens(fmt.Sprintf("%q", sa.owner)))
					action.Body().SetAttributeRaw("provider", exprTokens(fmt.Sprintf("%q", sa.provider)))
					action.Body().SetAttributeRaw("version", exprTokens(`"1"`))
					i++
				}
			}
		},
	},
	"aws_codeconnections_connection": {
		Reasons: []string{
			`"name" is a required string; the generic pass's cross-resource-reference heuristic points it at this cohort's own aws_codebuild_fleet.app.name (both arguments happen to be named "name"), but the provider validates connection names to at most 32 characters (apply: "Attribute name string length must be between 1 and 32") and the fleet's own identity-argument placeholder is longer than that - not caught by "terraform validate" itself (the referenced value is already known at plan time, but the provider's ValidateFunc only runs at apply), only surfaced at apply`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-connection"`, g.cohort)))
		},
	},
	"aws_codestarconnections_connection": {
		Reasons: []string{
			`Same cross-reference-length gap as aws_codeconnections_connection above, its CodeStarConnections predecessor - the provider validates this type's name to the same 32-character limit.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-starconn"`, g.cohort)))
		},
	},
	"aws_codebuild_report_group": {
		Reasons: []string{
			`type is a required string the provider validates against a fixed enum (validate: "expected type to be one of [\"TEST\" \"CODE_COVERAGE\"]"); export_config.type is likewise a required, enum-validated string (validate: "expected type to be one of [\"S3\" \"NO_EXPORT\"]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"TEST"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "export_config" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"NO_EXPORT"`))
				}
			}
		},
	},
	"aws_codepipeline_custom_action_type": {
		Reasons: []string{
			`category is a required string the provider validates against a fixed enum (validate: "expected category to be one of [...]"); provider_name is this type's own identity argument (internal/live/identity/table.go), but the generic pass's tofu-<cohort>-<type> placeholder exceeds the provider's documented 35-character limit (validate: "expected length of provider_name to be in the range (1 - 35)"); version is a required string the provider validates is 1-9 characters (validate: "expected length of version to be in the range (1 - 9)")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("category", exprTokens(`"Build"`))
			body.SetAttributeRaw("provider_name", exprTokens(fmt.Sprintf(`"tofu-%s-action"`, g.cohort)))
			body.SetAttributeRaw("version", exprTokens(`"1"`))
		},
	},
	"aws_codedeploy_deployment_group": {
		Reasons: []string{
			`app_name is a required string, but this type's identity is a two-attribute composite (internal/live/identity/table.go's Components: attr("app_name"), sep(":"), attr("deployment_group_name")), so identityArgName's own-identity convention does not fire (it only names a single-component identity's argument) and the generic pass left app_name as its own tofu-<cohort>-<type> placeholder instead of the sibling aws_codedeploy_app.app's real name - not a validate error (both are plain strings), but a plan-then-apply-time one (apply: "ApplicationDoesNotExistException: Application does not exist: ..."), so this override wires the cross-resource reference issue #56 asks for by hand.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if app, ok := g.byType["aws_codedeploy_app"]; ok {
				body.SetAttributeRaw("app_name", exprTokens(fmt.Sprintf("%s.name", app)))
			}
		},
	},
	"aws_codepipeline_webhook": {
		Reasons: []string{
			`authentication is a required string the provider validates against a fixed enum (validate: "expected authentication to be one of [...]"); UNAUTHENTICATED needs no matching auth_configuration block, unlike GITHUB_HMAC (a secret_token) or IP (an allowed_ip_range), keeping this override to the one attribute. "name" is also overridden away from the generic pass's cross-reference to aws_codebuild_fleet.app.name: CodeBuild::Fleet has no working create handler at all against the pinned floci image (see this cohort's README, "Verifying by hand"), so that reference made every plan against this fixture depend on a resource guaranteed to fail create, hiding this type's own result behind an unrelated one.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authentication", exprTokens(`"UNAUTHENTICATED"`))
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-webhook"`, g.cohort)))
		},
	},
	"aws_codestarnotifications_notification_rule": {
		Reasons: []string{
			`detail_type is a required string the provider validates against a fixed enum (validate: "expected detail_type to be one of [\"BASIC\" \"FULL\"]"); "resource" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN") - overridden to this cohort's own aws_codebuild_project.app.arn, the cross-resource reference issue #56 asks for, since this argument names an arbitrary already-admitted resource to watch rather than matching identityArgName's own-identity convention`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("detail_type", exprTokens(`"BASIC"`))
			if project, ok := g.byType["aws_codebuild_project"]; ok {
				body.SetAttributeRaw("resource", exprTokens(fmt.Sprintf("%s.arn", project)))
			} else {
				body.SetAttributeRaw("resource", exprTokens(fmt.Sprintf(
					`"arn:aws:codebuild:us-east-1:000000000000:project/tofu-%s-cohort"`, g.cohort)))
			}
		},
	},
	"aws_ecrpublic_repository_policy": {
		Reasons: []string{
			`"policy" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "ecr-public:DescribeRepositories"
    }]
  })`))
		},
	},
	// IoT core batch (issue #65).
	"aws_iot_authorizer": {
		Reasons: []string{
			`"authorizer_function_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); this argument names a Lambda function, not a role, so isRoleArg's cross-reference does not apply (this cohort requests no aws_lambda_function) - a literal, well-formed placeholder ARN is enough to satisfy the format check alone. "signing_disabled" is Optional and defaults to false (signing enabled), and the provider then requires "token_key_name" and "token_signing_public_keys" (apply: "\"token_key_name\" is required when signing is enabled", not caught by "terraform validate" - a real API-facing requirement, not a floci gap) - overridden to true so this fixture needs neither.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authorizer_function_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:lambda:us-east-1:000000000000:function:tofu-%s-authorizer"`, g.cohort)))
			body.SetAttributeRaw("signing_disabled", exprTokens(`true`))
		},
	},
	"aws_iot_policy": {
		Reasons: []string{
			`"policy" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = ["iot:Publish"]
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_iot_provisioning_template": {
		Reasons: []string{
			`"name" is this type's own identity argument (internal/live/identity/table.go), but the generic pass's tofu-<cohort>-<type> placeholder ("tofu-iot-cohort-provisioning-template", 37 characters) exceeds the provider's documented 36-character limit (validate: "expected length of name to be in the range (1 - 36)"); "template_body" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"template_body\" contains an invalid JSON") - the generic placeholder string is neither short enough nor JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-provtmpl"`, g.cohort)))
			body.SetAttributeRaw("template_body", exprTokens(`jsonencode({
    Parameters = {
      "AWS::IoT::Certificate::Id" = { Type = "String" }
    }
    Resources = {
      certificate = {
        Type = "AWS::IoT::Certificate"
        Properties = {
          CertificateId = { Ref = "AWS::IoT::Certificate::Id" }
          Status        = "ACTIVE"
        }
      }
      thing = {
        Type = "AWS::IoT::Thing"
        Properties = {
          ThingName = { Ref = "AWS::IoT::Certificate::Id" }
        }
      }
    }
  })`))
		},
	},
	"aws_iot_role_alias": {
		Reasons: []string{
			`"role_arn" is a required string the schema does not constrain and (unlike aws_codepipeline's own "role_arn" above) the provider ships no ARN-format validator on this particular attribute, so the generic placeholder passes "terraform validate" unchanged - but it is still the same bare isRoleArg gap named on aws_codepipeline and aws_codebuild_project above ("role_arn" alone does not end "_role_arn"), and a non-ARN placeholder would fail at apply against a real IoT role_alias create, so this override wires the real cross-resource reference anyway rather than leaving a validate-clean but apply-broken row.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if roleRef, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(roleRef))
			}
		},
	},
	"aws_iot_topic_rule": {
		Reasons: []string{
			`"name" is this type's own identity argument (internal/live/identity/table.go), but the generic pass's tofu-<cohort>-<type> placeholder uses hyphens, and the provider validates topic rule names against ^[0-9A-Za-z_]+$ (validate: "Name must match the pattern ^[0-9A-Za-z_]+$") - hyphens are not in that set, unlike every other client-named IoT type in this cohort.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_topic_rule"`, strings.ReplaceAll(g.cohort, "-", "_"))))
		},
	},
	"aws_iot_topic_rule_destination": {
		Reasons: []string{
			`"vpc_configuration.role_arn" is a required string nested inside the vpc_configuration block; the schema does not constrain it, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN") - isRoleArg's generic cross-reference only scans each type's top-level required arguments (planCohort's requiredArgNames pass), not nested block arguments, so this nested role_arn is never auto-wired the way aws_iot_provisioning_template's top-level provisioning_role_arn is.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			roleRef, ok := g.iamRoleRefExpr()
			if !ok {
				return
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "vpc_configuration" {
					blk.Body().SetAttributeRaw("role_arn", exprTokens(roleRef))
				}
			}
		},
	},
	// Identity batch (issue #65). Every argument below is Required in the
	// wire schema (so the generic required-only pass already sets it), but
	// the provider's own plan-time validation rejects the generic
	// placeholder value on a format or enum ground the schema itself does
	// not carry - the same shape as the batches above.
	"aws_cognito_resource_server": {
		Reasons: []string{
			`"user_pool_id" is a required string the schema does not constrain, but the provider validates it against the documented region_id shape (validate: "must be the region name followed by an underscore and then alphanumeric pattern"); the generic placeholder string is not one - resolved by hand to the sibling aws_cognito_user_pool's own real id rather than a synthesized literal, so this type actually exercises against a live pool during a floci apply instead of failing "User pool not found". "name" is a required string the schema does not constrain (distinct from the identity-bearing "identifier" argument this cohort's identity table already reads), but the generic pass pointed it at aws_iam_server_certificate's own placeholder name purely because both types happen to take a "name" argument - an accidental cross-type reference this override breaks with an independent literal`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-resource-server"`, g.cohort)))
		},
	},
	"aws_cognito_user": {
		Reasons: []string{
			`"user_pool_id" is a required string the schema does not constrain, but the provider validates it against the documented region_id shape; the generic placeholder string is not one - resolved to the sibling aws_cognito_user_pool's own real id, same fix as aws_cognito_resource_server above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
		},
	},
	"aws_cognito_user_group": {
		Reasons: []string{
			`"user_pool_id" is a required string the schema does not constrain, but the provider validates it against the documented region_id shape (validate: "must be the region name followed by an underscore and then alphanumeric pattern"); the generic placeholder string is not one - resolved to the sibling aws_cognito_user_pool's own real id, same fix as aws_cognito_resource_server above. "name" (the group's own name, distinct from user_pool_id) is a required string the generic pass pointed at aws_iam_server_certificate's own placeholder name for the same accidental cross-type reason as aws_cognito_resource_server's "name" above - broken the same way`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-user-group"`, g.cohort)))
		},
	},
	"aws_cognito_user_in_group": {
		Reasons: []string{
			`"user_pool_id" is a required string the schema does not constrain, but the provider validates it against the documented region_id shape; the generic placeholder string is not one - resolved to the sibling aws_cognito_user_pool's own real id, same fix as aws_cognito_user_group above. "group_name" and "username" are both required strings the generic pass rendered as independent literals unrelated to the sibling aws_cognito_user_group and aws_cognito_user resources this same run also creates (neither is a single-component identity argument gen.go's parentRef links automatically: aws_cognito_user_group's own name is real but not the type identityArgName treats as its identity-bearing argument in isolation, and aws_cognito_user's identity is the two-component user_pool_id+username composite, not a single one) - resolved by hand to both siblings' own real attributes so this attaches a real user to a real group during a floci apply rather than naming two groups/users that were never created`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
			body.SetAttributeRaw("group_name", exprTokens(cognitoUserGroupNameRef(g)))
			body.SetAttributeRaw("username", exprTokens(cognitoUsernameRef(g)))
		},
	},
	"aws_cognito_user_pool": {
		Reasons: []string{
			`"name" is a required string the schema does not constrain, but the generic pass pointed it at the unrelated aws_iam_server_certificate's own placeholder name purely because both types happen to take a "name" argument - the same accidental cross-type reference aws_cognito_resource_server's own "name" override above breaks, given its own independent literal here instead so a floci apply exercises this type on its own rather than skipping it whenever the certificate resource fails`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-user-pool"`, g.cohort)))
		},
	},
	"aws_cognito_identity_provider": {
		Reasons: []string{
			`"provider_name" is a required string the schema does not constrain, but the provider validates it is at most 32 UTF-8 characters (validate: "cannot be longer than 32 UTF-8 characters"); the generic placeholder-suffixed name is longer. "provider_type" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected provider_type to be one of [SAML Facebook Google LoginWithAmazon SignInWithApple OIDC]"). "user_pool_id" needs the same real-pool-reference fix as aws_cognito_resource_server above (its own generic placeholder does not match the documented region_id shape at all)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
			body.SetAttributeRaw("provider_name", exprTokens(fmt.Sprintf(`"tofu-%s-idp"`, g.cohort)))
			body.SetAttributeRaw("provider_type", exprTokens(`"OIDC"`))
			body.SetAttributeRaw("provider_details", exprTokens(`{
    client_id                  = "placeholder"
    authorize_scopes           = "openid"
    attributes_request_method  = "GET"
    oidc_issuer                = "https://accounts.example.com"
  }`))
		},
	},
	"aws_iam_group_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON policy"); the generic string placeholder is not JSON - the group-policy sibling of aws_s3_bucket_policy's own override above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:ListAllMyBuckets"
      Resource = "*"
    }]
  })`))
		},
	},
	"aws_iam_user_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON; the generic string placeholder is not JSON - the user-policy sibling of aws_iam_group_policy's own override above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:ListAllMyBuckets"
      Resource = "*"
    }]
  })`))
		},
	},
	"aws_iam_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON; the generic string placeholder is not JSON - same fix as aws_iam_group_policy above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:ListAllMyBuckets"
      Resource = "*"
    }]
  })`))
		},
	},
	"aws_iam_group_policy_attachment": {
		Reasons: []string{
			`"policy_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not one - resolved by hand to the sibling aws_iam_policy's own real arn attribute (aws_iam_policy is server-assigned, so identityArgName gives gen.go's parentRef nothing to link automatically) rather than a synthesized literal ARN no CreateOpenIDConnectProvider-style call ever minted, so an attach actually has a real policy on the other end during a floci apply`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_arn", exprTokens(iamPolicyArnRef(g)))
		},
	},
	"aws_iam_user_policy_attachment": {
		Reasons: []string{
			`"policy_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN; the generic placeholder string is not one - resolved to the sibling aws_iam_policy's own real arn attribute, same fix as aws_iam_group_policy_attachment above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_arn", exprTokens(iamPolicyArnRef(g)))
		},
	},
	"aws_iam_openid_connect_provider": {
		Reasons: []string{
			`"url" is a required string the schema does not constrain, but the provider validates it parses as a URL with a host (validate: "expected \"url\" to have a host"); the generic placeholder string has none`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("url", exprTokens(`"https://accounts.example.com"`))
		},
	},
	"aws_ssoadmin_instance_access_control_attributes": {
		Reasons: []string{
			`"instance_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder-suffixed name is not one. This is the type identityArgName treats as the single-component owner of "instance_arn" (see internal/live/identity/table.go's own entry), so every other type in this cohort that also takes an instance_arn argument (aws_ssoadmin_application, aws_ssoadmin_permission_set, aws_ssoadmin_account_assignment) already references this resource's own attribute through gen.go's parentRef rather than rendering a second, independent placeholder - fixing the ARN shape here is what fixes all four.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("instance_arn", exprTokens(`"arn:aws:sso:::instance/ssoins00000000001"`))
		},
	},
	"aws_cognito_identity_pool_roles_attachment": {
		Reasons: []string{
			`"identity_pool_id" is a required string the schema does not constrain, but the provider validates its length (1-55) and shape (a real identity pool id, "REGION:UUID") at apply time - not caught by "terraform validate" itself, only surfaced at apply (validate: "expected length of identity_pool_id to be in the range (1 - 55)"); the generic placeholder-suffixed name is both too long and the wrong shape. This is the type identityArgName treats as the single-component owner of "identity_pool_id" (its Components read the same-named argument), so aws_cognito_identity_pool_provider_principal_tag's own identity_pool_id already references this resource's attribute through gen.go's parentRef rather than rendering an independent placeholder - fixing the shape here is what fixes both. "roles" is a required map the schema leaves unconstrained (MinItems 0), but the provider requires at least the "authenticated" or "unauthenticated" key set (apply-time validate: "Either \"authenticated\" or \"unauthenticated\" must be defined") - not caught by "terraform validate" either, only surfaced at apply, the same "schema says Optional/unconstrained, provider requires it in practice" shape aws_s3_bucket_lifecycle_configuration's own override above already has.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("identity_pool_id", exprTokens(`"us-east-1:00000000-0000-0000-0000-000000000000"`))
			body.SetAttributeRaw("roles", exprTokens(fmt.Sprintf(
				`{ authenticated = "arn:aws:iam::000000000000:role/tofu-%s-cohort-authenticated" }`, g.cohort)))
		},
	},
	"aws_ssoadmin_permission_set": {
		Reasons: []string{
			`"name" is Required and the schema pins its own length range, but the value the generic pass supplied (a reference to the sibling aws_iam_server_certificate's own long placeholder name, matched purely because both types happen to take a "name" argument) exceeds the provider's own 1-32 character limit (validate: "expected length of name to be in the range (1 - 32)") - the same accidental cross-type name collision aws_cognito_user_pool's own "name" argument also inherits from the same certificate resource, but that type's schema tolerates the longer string; this one does not.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-permset"`, g.cohort)))
		},
	},
	"aws_ssoadmin_application_assignment": {
		Reasons: []string{
			`"application_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN; the generic placeholder string is not one, and this type has no single-component identity entry for gen.go's parentRef to link automatically, unlike instance_arn above - resolved by hand to the sibling aws_ssoadmin_application's own arn attribute when this run renders one. "principal_type" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected principal_type to be one of [USER GROUP]"). "principal_id" is a required string the schema does not constrain, but the provider validates it looks like an Identity Store principal id, a GUID optionally prefixed by a 10-hex-digit domain segment; the generic placeholder string matches neither shape`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("application_arn", exprTokens(ssoadminApplicationArnRef(g)))
			body.SetAttributeRaw("principal_type", exprTokens(`"USER"`))
			body.SetAttributeRaw("principal_id", exprTokens(`"12345678-1234-1234-1234-123456789012"`))
		},
	},
	"aws_ssoadmin_account_assignment": {
		Reasons: []string{
			`"permission_set_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN; the generic placeholder string is not one, and (like application_arn above) this type has no single-component identity entry for parentRef to link automatically - resolved by hand to the sibling aws_ssoadmin_permission_set's own arn attribute. "principal_type" and "target_type" are both required strings the schema does not constrain to an enum, but the provider validates each against its own fixed set (validate: "expected principal_type to be one of [USER GROUP]", "expected target_type to be one of [AWS_ACCOUNT]"). "principal_id" needs the same Identity Store principal-id shape as aws_ssoadmin_application_assignment's own override above. "target_id" is a required string the schema does not constrain, but the provider validates it looks like a 12-digit AWS account id (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("permission_set_arn", exprTokens(ssoadminPermissionSetArnRef(g)))
			body.SetAttributeRaw("principal_type", exprTokens(`"GROUP"`))
			body.SetAttributeRaw("target_type", exprTokens(`"AWS_ACCOUNT"`))
			body.SetAttributeRaw("principal_id", exprTokens(`"12345678-1234-1234-1234-123456789012"`))
			body.SetAttributeRaw("target_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_ssoadmin_application": {
		Reasons: []string{
			`"application_provider_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "Invalid ARN Value"); the generic placeholder string is not one - set to AWS's own built-in custom SAML application provider, a real, documented value (not account-specific) rather than a synthesized placeholder ARN. "name" is a required string the generic pass pointed at the unrelated aws_iam_server_certificate's own placeholder name purely because both types happen to take a "name" argument - the same accidental cross-type reference aws_cognito_user_pool's own "name" override above breaks, given its own independent literal here`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("application_provider_arn", exprTokens(`"arn:aws:sso::aws:applicationProvider/custom-saml"`))
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-app"`, g.cohort)))
		},
	},
	// Observability and eventing remainder batch (issue #65). Every
	// argument below is Optional in the wire schema (so the generic
	// required-only pass leaves it unset, or leaves a bare "placeholder"
	// that fails an enum/ARN-format/length check the schema itself does not
	// carry), or is a nested block the schema marks optional while the
	// provider requires its contents in practice - the same two failure
	// shapes issue #56 already named for the earlier cohorts above.
	"aws_cloudwatch_alarm_mute_rule": {
		Reasons: []string{
			`rule is a required argument typed as a nested block with MinItems 0 in the wire schema, so the generic required-only pass never renders one at all - not caught by "terraform validate" (which only checks the arguments a block actually has), only surfaced applying against floci (apply: "missing required field, PutAlarmMuteRuleInput.Rule"). Its own nested schedule block is likewise required in practice.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			rule := body.AppendNewBlock("rule", nil)
			schedule := rule.Body().AppendNewBlock("schedule", nil)
			schedule.Body().SetAttributeRaw("duration", exprTokens(`"PT4H"`))
			schedule.Body().SetAttributeRaw("expression", exprTokens(`"cron(0 2 * * ? *)"`))
		},
	},
	"aws_cloudwatch_contributor_insight_rule": {
		Reasons: []string{
			`rule_definition is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "A string value was provided that is not valid JSON string format"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule_definition", exprTokens(fmt.Sprintf(`jsonencode({
    Schema = {
      Name    = "CloudWatchLogRule"
      Version = 1
    }
    LogGroupNames = ["tofu-%s-cohort-insight-source"]
    LogFormat     = "JSON"
    Contribution = {
      Keys = ["$.ip"]
    }
    AggregateOn = "Count"
  })`, g.cohort)))
		},
	},
	"aws_cloudwatch_event_api_destination": {
		Reasons: []string{
			`connection_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); http_method is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected http_method to be one of [...]"). connection_arn references this same cohort's aws_cloudwatch_event_connection.app.arn - an unknown value at validate time, which the ARN-format check never runs against - rather than a literal placeholder.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if conn, ok := g.byType["aws_cloudwatch_event_connection"]; ok {
				body.SetAttributeRaw("connection_arn", exprTokens(fmt.Sprintf("%s.arn", conn)))
			} else {
				body.SetAttributeRaw("connection_arn", exprTokens(fmt.Sprintf(
					`"arn:aws:events:us-east-1:000000000000:connection/tofu-%s-cohort/00000000-0000-0000-0000-000000000000"`, g.cohort)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"POST"`))
		},
	},
	"aws_cloudwatch_event_archive": {
		Reasons: []string{
			`name is length-limited to 48 characters (validate: "expected length of name to be in the range (1 - 48)"), and this cohort's own name ("observability") makes the generic tofu-<cohort>-cohort-<type> placeholder 51 characters - shortened here to a value that still names the cohort and the type. event_source_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); it references this same cohort's aws_cloudwatch_event_bus.app.arn - an unknown value at validate time, which the ARN-format check never runs against - rather than a literal placeholder.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-obs-event-archive"`))
			if bus, ok := g.byType["aws_cloudwatch_event_bus"]; ok {
				body.SetAttributeRaw("event_source_arn", exprTokens(fmt.Sprintf("%s.arn", bus)))
			} else {
				body.SetAttributeRaw("event_source_arn", exprTokens(fmt.Sprintf(
					`"arn:aws:events:us-east-1:000000000000:event-bus/tofu-%s-cohort-bus"`, g.cohort)))
			}
		},
	},
	"aws_cloudwatch_event_connection": {
		Reasons: []string{
			`authorization_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected authorization_type to be one of [...]"); auth_parameters is a required block, but the provider requires exactly one of its api_key/basic/oauth children set in practice (validate: "Invalid combination of arguments" x3 on an empty auth_parameters), and the chosen child's own key/value pair is itself required.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authorization_type", exprTokens(`"API_KEY"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "auth_parameters" {
					apiKey := blk.Body().AppendNewBlock("api_key", nil)
					apiKey.Body().SetAttributeRaw("key", exprTokens(`"x-api-key"`))
					apiKey.Body().SetAttributeRaw("value", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-api-key-value"`, g.cohort)))
				}
			}
		},
	},
	"aws_cloudwatch_event_endpoint": {
		Reasons: []string{
			`event_bus is a required block appearing exactly twice in the schema (a global endpoint always names a primary and a secondary event bus), and each child's event_bus_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN" x2); the generic pass's placeholder string is neither. The first bus references this same cohort's aws_cloudwatch_event_bus.app.arn; the second is a literal placeholder in a different region, since a global endpoint's two buses are documented as living in different regions. routing_config.failover_config's primary.health_check and secondary.route are both required in practice - not caught by "terraform validate" (which only checks the arguments a block actually has, and the generic pass rendered both primary and secondary as empty blocks), only surfaced applying against floci (apply: "missing required field, CreateEndpointInput.RoutingConfig.FailoverConfig.Primary" and "...Secondary").`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			primary := fmt.Sprintf(`"arn:aws:events:us-west-2:000000000000:event-bus/tofu-%s-cohort-secondary"`, g.cohort)
			firstExpr := primary
			if bus, ok := g.byType["aws_cloudwatch_event_bus"]; ok {
				firstExpr = fmt.Sprintf("%s.arn", bus)
			}
			i := 0
			for _, blk := range body.Blocks() {
				if blk.Type() != "event_bus" {
					continue
				}
				if i == 0 {
					blk.Body().SetAttributeRaw("event_bus_arn", exprTokens(firstExpr))
				} else {
					blk.Body().SetAttributeRaw("event_bus_arn", exprTokens(primary))
				}
				i++
			}
			for _, blk := range body.Blocks() {
				if blk.Type() != "routing_config" {
					continue
				}
				for _, fc := range blk.Body().Blocks() {
					if fc.Type() != "failover_config" {
						continue
					}
					for _, leg := range fc.Body().Blocks() {
						switch leg.Type() {
						case "primary":
							leg.Body().SetAttributeRaw("health_check", exprTokens(
								`"arn:aws:route53:::healthcheck/00000000-0000-0000-0000-000000000000"`))
						case "secondary":
							leg.Body().SetAttributeRaw("route", exprTokens(`"us-west-2"`))
						}
					}
				}
			}
		},
	},
	"aws_cloudwatch_event_permission": {
		Reasons: []string{
			`principal is a required string the schema does not constrain, but the provider validates it is "*" or a 12-digit AWS account ID (validate: "\"principal\" must be * or a 12 digit AWS account ID"); the generic placeholder string is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("principal", exprTokens(`"*"`))
		},
	},
	"aws_cloudwatch_log_account_policy": {
		Reasons: []string{
			`policy_document is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "contains an invalid JSON"); policy_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected policy_type to be one of [...]"); the generic placeholder string satisfies neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_document", exprTokens(fmt.Sprintf(`jsonencode({
    DestinationArn = "arn:aws:lambda:us-east-1:000000000000:function:tofu-%s-cohort-log-account-policy-target"
    FilterPattern  = ""
    Distribution   = "Random"
  })`, g.cohort)))
			body.SetAttributeRaw("policy_type", exprTokens(`"SUBSCRIPTION_FILTER_POLICY"`))
		},
	},
	"aws_cloudwatch_log_delivery": {
		Reasons: []string{
			`delivery_destination_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); it references this same cohort's aws_cloudwatch_log_delivery_destination.app.arn - an unknown value at validate time, which the ARN-format check never runs against - rather than a literal placeholder. delivery_source_name is likewise pointed at this same cohort's aws_cloudwatch_log_delivery_source.app.name instead of the generic pass's disconnected literal string, so the delivery names a source that actually exists in this estate.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if dest, ok := g.byType["aws_cloudwatch_log_delivery_destination"]; ok {
				body.SetAttributeRaw("delivery_destination_arn", exprTokens(fmt.Sprintf("%s.arn", dest)))
			} else {
				body.SetAttributeRaw("delivery_destination_arn", exprTokens(fmt.Sprintf(
					`"arn:aws:logs:us-east-1:000000000000:delivery-destination:tofu-%s-cohort-log-delivery-destination"`, g.cohort)))
			}
			if src, ok := g.byType["aws_cloudwatch_log_delivery_source"]; ok {
				body.SetAttributeRaw("delivery_source_name", exprTokens(fmt.Sprintf("%s.name", src)))
			}
		},
	},
	"aws_cloudwatch_log_delivery_destination": {
		Reasons: []string{
			`name is length-limited to 60 characters (validate: "Attribute name string length must be between 1 and 60"), and this cohort's own name ("observability") makes the generic tofu-<cohort>-cohort-<type> placeholder 61 characters - shortened here by one character's worth of margin. delivery_destination_configuration is Optional in the schema, but the provider requires it in practice unless delivery_destination_type is XRAY (validate: "delivery_destination_configuration is required when delivery_destination_type is not XRAY") - set to XRAY here rather than inventing a destination_resource_arn this cohort has no real destination for.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-obs-log-delivery-destination"`))
			body.SetAttributeRaw("delivery_destination_type", exprTokens(`"XRAY"`))
		},
	},
	"aws_cloudwatch_log_delivery_source": {
		Reasons: []string{
			`resource_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); the generic placeholder string is not. log_type is paired with resource_arn here as the provider's own documented CloudFront example (ACCESS_LOGS / a CloudFront distribution ARN) rather than left at the generic placeholder, since the two arguments name the same source in practice even though only the ARN shape is checked locally.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("log_type", exprTokens(`"ACCESS_LOGS"`))
			body.SetAttributeRaw("resource_arn", exprTokens(`"arn:aws:cloudfront::000000000000:distribution/EDFDVBD6EXAMPLE"`))
		},
	},
	"aws_cloudwatch_log_destination": {
		Reasons: []string{
			`role_arn and target_arn are both required strings the schema does not constrain, but the provider validates each is a well-formed ARN (validate: "is an invalid ARN" x2); the generic placeholder string is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-log-destination"`, g.cohort)))
			body.SetAttributeRaw("target_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:kinesis:us-east-1:000000000000:stream/tofu-%s-cohort-log-destination-target"`, g.cohort)))
		},
	},
	"aws_cloudwatch_log_resource_policy": {
		Reasons: []string{
			`policy_document is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_document", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "logs.amazonaws.com" }
      Action    = "logs:PutLogEvents"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_cloudwatch_log_subscription_filter": {
		Reasons: []string{
			`destination_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("destination_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:lambda:us-east-1:000000000000:function:tofu-%s-cohort-log-subscription-target"`, g.cohort)))
		},
	},
	"aws_cloudwatch_log_transformer": {
		Reasons: []string{
			`transformer_config is a required argument typed as a list of nested blocks with MinItems 0 in the wire schema, so the generic required-only pass never renders one at all (validate: "Block transformer_config must have a configuration value as the provider has marked it as required"), and the provider requires its first processor to be a parser - parse_json is the simplest one. log_group_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); this cohort admits no aws_cloudwatch_log_group of its own (that type ratifies elsewhere, in the client-named section above), so the ARN is a literal placeholder rather than a cross-reference.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("log_group_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:logs:us-east-1:000000000000:log-group:/tofu-%s-cohort-log-transformer-source"`, g.cohort)))
			tc := body.AppendNewBlock("transformer_config", nil)
			tc.Body().AppendNewBlock("parse_json", nil)
		},
	},
	"aws_grafana_workspace": {
		Reasons: []string{
			`account_access_type, authentication_providers and permission_type are each required strings (or a set of them) the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]" x3); the generic placeholder string matches none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("account_access_type", exprTokens(`"CURRENT_ACCOUNT"`))
			body.SetAttributeRaw("authentication_providers", exprTokens(`["AWS_SSO"]`))
			body.SetAttributeRaw("permission_type", exprTokens(`"SERVICE_MANAGED"`))
		},
	},
	"aws_rum_app_monitor": {
		Reasons: []string{
			`domain and domain_list are both Optional in the schema, so the generic pass sets neither, but the provider requires exactly one of them (validate: "one of domain,domain_list must be specified" x2)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("domain", exprTokens(fmt.Sprintf(`"tofu-%s-cohort.example.com"`, g.cohort)))
		},
	},
	"aws_xray_resource_policy": {
		Reasons: []string{
			`policy_document is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "A string value was provided that is not valid JSON string format"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_document", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "xray.amazonaws.com" }
      Action    = "xray:GetSamplingStatisticSummaries"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_xray_sampling_rule": {
		Reasons: []string{
			`rule_name is length-limited to 32 characters (validate: "expected length of rule_name to be in the range (1 - 32)"), and this cohort's own name ("observability") makes the generic tofu-<cohort>-cohort-<type> placeholder 46 characters - shortened here to a value that still names the cohort and the type. http_method is length-limited to 10 characters and the generic "placeholder" string is 11; priority must be in (1 - 9999) and version must be at least 1, but both are numeric arguments the generic required-only pass zero-values rather than infers a real member for; resource_arn has no local format check but "*" (match any resource) is the provider's own documented value for a rule with no specific resource.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule_name", exprTokens(`"tofu-obs-xray-rule"`))
			body.SetAttributeRaw("service_name", exprTokens(`"tofu-obs-xray-rule"`))
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("resource_arn", exprTokens(`"*"`))
			body.SetAttributeRaw("priority", exprTokens(`1000`))
			body.SetAttributeRaw("version", exprTokens(`1`))
		},
	},
	// Streaming and app integration batch (issue #65). Five of this
	// cohort's types are server-assigned in internal/live/identity/table.go
	// (no Components at all), so the generic pass's identityArgName never
	// fires for their own "name" argument, which is itself a plain Required
	// string in the real provider schema even though the type's *identity*
	// is server-assigned. Left to the generic pass's same-name parent
	// search, all five instead point their "name" at whichever other
	// resource in this run happens to render first with its own "name"
	// Required too (aws_appflow_connector_profile, alphabetically first) -
	// the identical failure shape aws_ecs_daemon's own override above
	// already names for the same root cause. Every one of the five gets its
	// own literal name back here.
	"aws_appsync_graphql_api": {
		Reasons: []string{
			`"name" mis-wired to aws_appflow_connector_profile.app.name by the generic pass's same-name parent search (this type is server-assigned, so identityArgName never supplies its own name); corrected to a literal. authentication_type is Required and the provider validates it against a fixed enum (validate: "expected authentication_type to be one of [...]"), and the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-appsync-graphql-api"`, g.cohort)))
			body.SetAttributeRaw("authentication_type", exprTokens(`"API_KEY"`))
		},
	},
	"aws_msk_configuration": {
		Reasons: []string{
			`"name" mis-wired to aws_appflow_connector_profile.app.name, the same cause and fix as aws_appsync_graphql_api above. server_properties is Required but the generic placeholder string is not a real Kafka broker properties file, which the provider does not validate at plan time but does need at apply time (confirmed by hand against floci during this batch's verification)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-msk-configuration"`, g.cohort)))
			body.SetAttributeRaw("server_properties", exprTokens(`"auto.create.topics.enable=true"`))
		},
	},
	"aws_mskconnect_connector": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_appsync_graphql_api above. capacity is a required block whose two nested block_types (autoscaling, provisioned_capacity) are both themselves Optional in the schema, but the provider requires exactly one (validate: "Missing required argument" once floci is asked to create it); kafka_cluster.apache_kafka_cluster.bootstrap_servers is Required but the generic placeholder string is not the bootstrap-broker-list format the provider expects, so it is wired to this cohort's own aws_msk_cluster's bootstrap_brokers output; kafka_cluster_client_authentication.authentication_type and kafka_cluster_encryption_in_transit.encryption_type are both Optional in the schema but their empty blocks leave the provider to guess, set here to their documented defaults for clarity; plugin.custom_plugin.arn/revision are Required but the generic placeholder string and zero are neither a real plugin ARN nor a real revision, wired to this cohort's own aws_mskconnect_custom_plugin instead; kafkaconnect_version is Required and the provider validates it against the versions MSK Connect actually supports, not an arbitrary string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-mskconnect-connector"`, g.cohort)))
			body.SetAttributeRaw("kafkaconnect_version", exprTokens(`"2.7.1"`))
			body.SetAttributeRaw("connector_configuration", exprTokens(`{
    "connector.class" = "org.apache.kafka.connect.mirror.MirrorSourceConnector"
    "tasks.max"        = "1"
    "topics"           = "example"
  }`))
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "capacity":
					pc := blk.Body().AppendNewBlock("provisioned_capacity", nil)
					pc.Body().SetAttributeRaw("worker_count", exprTokens(`1`))
				case "kafka_cluster":
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "apache_kafka_cluster" {
							bootstrapExpr := `"placeholder"`
							if cluster, ok := g.byType["aws_msk_cluster"]; ok {
								bootstrapExpr = fmt.Sprintf("%s.bootstrap_brokers", cluster)
							}
							inner.Body().SetAttributeRaw("bootstrap_servers", exprTokens(bootstrapExpr))
						}
					}
				case "kafka_cluster_client_authentication":
					blk.Body().SetAttributeRaw("authentication_type", exprTokens(`"NONE"`))
				case "kafka_cluster_encryption_in_transit":
					blk.Body().SetAttributeRaw("encryption_type", exprTokens(`"PLAINTEXT"`))
				case "plugin":
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "custom_plugin" {
							arnExpr := `"placeholder"`
							revExpr := `0`
							if plugin, ok := g.byType["aws_mskconnect_custom_plugin"]; ok {
								arnExpr = fmt.Sprintf("%s.arn", plugin)
								revExpr = fmt.Sprintf("%s.latest_revision", plugin)
							}
							inner.Body().SetAttributeRaw("arn", exprTokens(arnExpr))
							inner.Body().SetAttributeRaw("revision", exprTokens(revExpr))
						}
					}
				}
			}
		},
	},
	"aws_mskconnect_custom_plugin": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_appsync_graphql_api above. content_type is Required and the provider validates it against a fixed enum (validate: "expected content_type to be one of [JAR ZIP]"), and the generic placeholder string is not a member; location.s3.bucket_arn is Required and validated as a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-mskconnect-custom-plugin"`, g.cohort)))
			body.SetAttributeRaw("content_type", exprTokens(`"JAR"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "location" {
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "s3" {
							inner.Body().SetAttributeRaw("bucket_arn", exprTokens(fmt.Sprintf(
								`"arn:aws:s3:::tofu-%s-cohort-plugins"`, g.cohort)))
						}
					}
				}
			}
		},
	},
	"aws_mskconnect_worker_configuration": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_appsync_graphql_api above. properties_file_content is Required but the generic placeholder string is not a real Kafka Connect worker properties file, which the provider does not validate at plan time but does need at apply time (confirmed by hand against floci during this batch's verification)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-mskconnect-worker-configuration"`, g.cohort)))
			body.SetAttributeRaw("properties_file_content", exprTokens(`"key.converter=org.apache.kafka.connect.storage.StringConverter\nvalue.converter=org.apache.kafka.connect.storage.StringConverter\n"`))
		},
	},
	"aws_mq_broker": {
		Reasons: []string{
			`engine_type, engine_version and host_instance_type are all Required strings the provider validates against real ActiveMQ/RabbitMQ values, not an arbitrary placeholder (engine_type: validate "expected engine_type to be one of [ACTIVEMQ RABBITMQ]"); user is Optional-shaped in the schema (a set with no min_items) but the provider requires at least one broker user in practice (found only by exercising a create against floci during this batch's verification, not by validate). engine_type is RABBITMQ rather than the more common ActiveMQ: floci's own AmazonMQ emulation refuses ACTIVEMQ outright ("BadRequestException: Only RABBITMQ EngineType is supported"), confirmed by hand against floci during this batch's verification - both engine types are equally real and valid against the actual AWS API, so this is a floci-emulator accommodation, not a correctness compromise`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine_type", exprTokens(`"RABBITMQ"`))
			body.SetAttributeRaw("engine_version", exprTokens(`"3.13"`))
			body.SetAttributeRaw("host_instance_type", exprTokens(`"mq.t3.micro"`))
			user := body.AppendNewBlock("user", nil)
			user.Body().SetAttributeRaw("username", exprTokens(`"tofuadmin"`))
			user.Body().SetAttributeRaw("password", exprTokens(fmt.Sprintf(`"Tofu%sCohortPw1!"`, g.cohort)))
		},
	},
	"aws_mq_configuration": {
		Reasons: []string{
			`"name" mis-wired the same way as aws_appsync_graphql_api above (this type is client-named in the identity table, but via a "name" argument the generic pass's own parent search also treats as a same-name candidate before Components resolves it, so the override is needed regardless of admission path). engine_type and engine_version are Required strings validated against real ActiveMQ/RabbitMQ values, the same shape as aws_mq_broker above; data is Required and must be a well-formed broker configuration document (XML for ActiveMQ), not an arbitrary placeholder string - not caught by validate, found by exercising a create against floci during this batch's verification`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-mq-configuration"`, g.cohort)))
			body.SetAttributeRaw("engine_type", exprTokens(`"ACTIVEMQ"`))
			body.SetAttributeRaw("engine_version", exprTokens(`"5.18"`))
			body.SetAttributeRaw("data", exprTokens(`<<DATA
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<broker xmlns="http://activemq.apache.org/schema/core">
  <plugins>
  </plugins>
</broker>
DATA
`))
		},
	},
	"aws_msk_cluster": {
		Reasons: []string{
			`kafka_version and number_of_broker_nodes are both Required, and the generic pass's zero-value/placeholder defaults (0, "placeholder") are neither a real Kafka version nor a legal broker count (must be at least 1 and a multiple of the number of client_subnets); broker_node_group_info.instance_type is Required and the provider validates it is a real MSK-supported instance type, not an arbitrary string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("kafka_version", exprTokens(`"3.6.0"`))
			body.SetAttributeRaw("number_of_broker_nodes", exprTokens(`1`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "broker_node_group_info" {
					blk.Body().SetAttributeRaw("instance_type", exprTokens(`"kafka.m5.large"`))
				}
			}
		},
	},
	"aws_msk_serverless_cluster": {
		Reasons: []string{
			`client_authentication.sasl.iam.enabled is Required and schema-valid as either bool, but MSK Serverless accepts only IAM-authenticated SASL in practice (the generic pass's zero-value "false" passes validate but the provider refuses it at create - confirmed by hand against floci during this batch's verification); vpc_config.subnet_ids is Required and schema-valid with the generic pass's single-element placeholder list, but MSK Serverless requires subnets in at least two distinct Availability Zones in practice, the same not-caught-by-validate shape`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "client_authentication":
					for _, sasl := range blk.Body().Blocks() {
						if sasl.Type() == "sasl" {
							for _, iam := range sasl.Body().Blocks() {
								if iam.Type() == "iam" {
									iam.Body().SetAttributeRaw("enabled", exprTokens(`true`))
								}
							}
						}
					}
				case "vpc_config":
					blk.Body().SetAttributeRaw("subnet_ids", exprTokens(`["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]`))
				}
			}
		},
	},
	"aws_appflow_connector_profile": {
		Reasons: []string{
			`connection_mode and connector_type are both Required strings validated against fixed enums (validate: "expected connection_mode to be one of [Public Private]", "expected connector_type to be one of [...]"); connector_profile_config.connector_profile_credentials and .connector_profile_properties are both required blocks (min_items 1) but every field inside their per-connector-type oneof sub-blocks is itself optional in the schema, so the generic pass renders both empty - the provider needs one real connector-type sub-block filled in, chosen here as CustomConnector/APIKEY, the connector type needing the fewest required fields of the 24 the enum offers`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("connection_mode", exprTokens(`"Public"`))
			body.SetAttributeRaw("connector_type", exprTokens(`"CustomConnector"`))
			for _, blk := range body.Blocks() {
				if blk.Type() != "connector_profile_config" {
					continue
				}
				for _, inner := range blk.Body().Blocks() {
					switch inner.Type() {
					case "connector_profile_credentials":
						cc := inner.Body().AppendNewBlock("custom_connector", nil)
						cc.Body().SetAttributeRaw("authentication_type", exprTokens(`"APIKEY"`))
						ak := cc.Body().AppendNewBlock("api_key", nil)
						ak.Body().SetAttributeRaw("api_key", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-appflow-api-key"`, g.cohort)))
					case "connector_profile_properties":
						inner.Body().AppendNewBlock("custom_connector", nil)
					}
				}
			}
		},
	},
	"aws_appflow_flow": {
		Reasons: []string{
			`destination_flow_config.connector_type and source_flow_config.connector_type are both Required strings validated against the same fixed enum as aws_appflow_connector_profile above; task.task_type is Required and validated against its own fixed enum (validate: "expected task_type to be one of [...]"); trigger_config.trigger_type is Required and validated against a fixed enum too. Chosen connector type is S3 rather than aws_appflow_connector_profile's CustomConnector: S3 flows need no connector_profile_name at all (Optional in the schema, and AppFlow's own S3 connector is IAM-based, not credential-based), the simplest of the connector types this cohort's sibling resource does not have to match`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "destination_flow_config":
					blk.Body().SetAttributeRaw("connector_type", exprTokens(`"S3"`))
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "destination_connector_properties" {
							s3 := inner.Body().AppendNewBlock("s3", nil)
							s3.Body().SetAttributeRaw("bucket_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-appflow-dest"`, g.cohort)))
						}
					}
				case "source_flow_config":
					blk.Body().SetAttributeRaw("connector_type", exprTokens(`"S3"`))
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "source_connector_properties" {
							s3 := inner.Body().AppendNewBlock("s3", nil)
							s3.Body().SetAttributeRaw("bucket_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-appflow-source"`, g.cohort)))
							s3.Body().SetAttributeRaw("bucket_prefix", exprTokens(`"data"`))
						}
					}
				case "task":
					blk.Body().SetAttributeRaw("task_type", exprTokens(`"Passthrough"`))
				case "trigger_config":
					blk.Body().SetAttributeRaw("trigger_type", exprTokens(`"OnDemand"`))
				}
			}
		},
	},
	"aws_pipes_pipe": {
		Reasons: []string{
			`role_arn, source and target are all Required and validated as well-formed ARNs (validate: "is an invalid ARN"), and source is additionally validated against a pattern requiring either an "smk://" bootstrap string or a real ARN (validate: "expected value of source to match regular expression ..."); the generic placeholder string satisfies none of the three. source is wired to this cohort's own aws_msk_cluster (a Managed Streaming Kafka source pipe reads from), with the source_parameters.managed_streaming_kafka_parameters.topic_name the MSK source type requires in practice (Optional in the schema, not caught by validate); target stays a literal placeholder ARN naming an SQS queue - no admitted type in this cohort is a valid Pipes target, the same "no real sibling to reference" shape aws_rds_integration's own override above accepts for its target_arn`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
			sourceExpr := fmt.Sprintf(`"arn:aws:kafka:us-east-1:000000000000:cluster/tofu-%s-cohort-msk-cluster/placeholder"`, g.cohort)
			if cluster, ok := g.byType["aws_msk_cluster"]; ok {
				sourceExpr = cluster.String() + ".arn"
			}
			body.SetAttributeRaw("source", exprTokens(sourceExpr))
			body.SetAttributeRaw("target", exprTokens(fmt.Sprintf(
				`"arn:aws:sqs:us-east-1:000000000000:tofu-%s-cohort-pipes-target"`, g.cohort)))
			sp := body.AppendNewBlock("source_parameters", nil)
			mskp := sp.Body().AppendNewBlock("managed_streaming_kafka_parameters", nil)
			mskp.Body().SetAttributeRaw("topic_name", exprTokens(`"example"`))
		},
	},
	"aws_ivschat_logging_configuration": {
		Reasons: []string{
			`destination_configuration is Required in the provider's own docs (exactly one of cloudwatch_logs/firehose/s3, each with its own Required leaf) but Optional in the wire schema - enforced only by the provider's plan-time validation, so the generic required-only pass never visits it at all; wired to a literal S3 bucket name, the same destination_configuration.s3 shape aws_ivs_recording_configuration already renders (the generic pass fills that one because it is genuinely block-Required there)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			dc := body.AppendNewBlock("destination_configuration", nil)
			s3 := dc.Body().AppendNewBlock("s3", nil)
			s3.Body().SetAttributeRaw("bucket_name", exprTokens(`"placeholder"`))
		},
	},
	"aws_medialive_multiplex": {
		Reasons: []string{
			`name is a plain client-chosen string (not this type's identity - aws_medialive_multiplex is server-assigned), but gen.go's parentRef mistakes it for a parent reference: this cohort's other client-named type, aws_media_packagev2_channel_group, also owns a single-component identity argument called "name", and parentRef's own same-name tiebreaker only guards two types that *both* claim "name" as their identity - not a server-assigned type's ordinary same-named argument. Left alone, the generic pass would point the multiplex at an unrelated MediaPackage channel group's name`,
			`availability_zones is a required list of strings, but the provider validates a 2-item minimum (validate: "attribute availability_zones requires 2 item minimum, but config has only 1 declared"); the generic pass emits one placeholder element`,
			`multiplex_settings is Required in the provider's own docs (transport_stream_bitrate and transport_stream_id both Required within it) but Optional in the wire schema - enforced only by the provider's plan-time validation, so the generic required-only pass never visits it at all`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-medialive-multiplex"`, g.cohort)))
			body.SetAttributeRaw("availability_zones", exprTokens(`["us-east-1a", "us-east-1b"]`))
			ms := body.AppendNewBlock("multiplex_settings", nil)
			ms.Body().SetAttributeRaw("transport_stream_bitrate", exprTokens(`1000000`))
			ms.Body().SetAttributeRaw("transport_stream_id", exprTokens(`1`))
		},
	},
	"aws_medialive_multiplex_program": {
		Reasons: []string{
			`multiplex_id names the parent aws_medialive_multiplex by its server-assigned id, but this type's identity is a two-component composite (program_name, multiplex_id joined by "/"), and gen.go's identityArgName only links a single-component identity - so parentRef has nothing to match multiplex_id against and the generic pass leaves it a disconnected placeholder, the same "no automatic link to a server-assigned parent" gap cognitoUserPoolIDRef and iamPolicyArnRef below already work around for their own types`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("multiplex_id", exprTokens(medialiveMultiplexIDRef(g)))
		},
	},
	"aws_config_config_rule": {
		Reasons: []string{
			`source.owner is Required and the provider validates it against a closed enum (validate: "expected owner to be one of [\"CUSTOM_LAMBDA\" \"AWS\" \"CUSTOM_POLICY\"]"); the generic placeholder string is not a member. Set to AWS, the managed-rule case, which also requires source_identifier (Optional in the schema, Required by the provider in practice when owner=AWS) - a real AWS managed rule identifier.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "source" {
					blk.Body().SetAttributeRaw("owner", exprTokens(`"AWS"`))
					blk.Body().SetAttributeRaw("source_identifier", exprTokens(`"S3_BUCKET_VERSIONING_ENABLED"`))
				}
			}
		},
	},
	"aws_config_conformance_pack": {
		Reasons: []string{
			`schema requires neither template_body nor template_s3_uri, but the provider requires exactly one of them in practice (validate: "one of template_body,template_s3_uri must be specified"); the generic pass sets neither. template_body is set to a minimal, syntactically valid Config conformance pack template wrapping one managed rule.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("template_body", exprTokens(`<<-TEMPLATE
  Resources:
    ConformancePackVersioning:
      Type: AWS::Config::ConfigRule
      Properties:
        Source:
          Owner: AWS
          SourceIdentifier: S3_BUCKET_VERSIONING_ENABLED
  TEMPLATE
`))
		},
	},
	"aws_config_remediation_configuration": {
		Reasons: []string{
			`target_type is Required and the provider validates it against a closed enum with exactly one member (validate: "expected target_type to be one of [\"SSM_DOCUMENT\"]"); the generic placeholder string is not it.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("target_type", exprTokens(`"SSM_DOCUMENT"`))
			body.SetAttributeRaw("target_id", exprTokens(`"AWS-PublishSNSNotification"`))
		},
	},
	"aws_controltower_control": {
		Reasons: []string{
			`control_identifier and target_identifier are both Required and validated as well-formed ARNs (validate: "is an invalid ARN: arn: invalid prefix"); the generic placeholder string is neither. Set to the shapes the provider's own documented Import example uses: target_identifier an organizational unit ARN, control_identifier an AWS-defined control ARN - neither references a real sibling resource, the same "no real sibling to reference" shape several other overrides in this file accept.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("target_identifier", exprTokens(
				`"arn:aws:organizations::123456789012:ou/o-exampleorgid/ou-exampleroot-exampleouid1"`))
			body.SetAttributeRaw("control_identifier", exprTokens(
				`"arn:aws:controltower:us-east-1::control/AWS-GR_S3_BUCKET_VERSIONING_ENABLED"`))
		},
	},
	"aws_controltower_landing_zone": {
		Reasons: []string{
			`schema requires manifest_json as a plain string, but the provider validates it is well-formed JSON (validate: "\"manifest_json\" contains an invalid JSON"); the generic string placeholder is not JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("manifest_json", exprTokens(`jsonencode({
    governedRegions = ["us-east-1"]
    organizationStructure = {
      security = { name = "Security" }
      sandbox  = { name = "Sandbox" }
    }
    centralizedLogging = {
      accountId = "123456789012"
      configurations = {
        loggingBucket   = { retentionDays = 365 }
        accessLoggingBucket = { retentionDays = 3650 }
      }
    }
  })`))
		},
	},
	"aws_organizations_resource_policy": {
		Reasons: []string{
			`schema requires content as a plain string, but the provider validates it is well-formed JSON (validate: "\"content\" contains an invalid JSON"); the generic string placeholder is not JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("content", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "organizations:DescribeOrganization"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_resourceexplorer2_index": {
		Reasons: []string{
			`type is Required and the provider validates it against a closed enum (validate: "Invalid String Enum Value", valid values LOCAL/AGGREGATOR); the generic placeholder string is neither.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"LOCAL"`))
		},
	},
	"aws_servicecatalog_portfolio_share": {
		Reasons: []string{
			`type and principal_id are both Required; type is validated against a closed enum (validate: "expected type to be one of [\"ACCOUNT\" \"ORGANIZATION\" \"ORGANIZATIONAL_UNIT\" \"ORGANIZATION_MEMBER_ACCOUNT\"]") and, for the ACCOUNT case this override picks, principal_id is validated as a 12-digit account ID (validate: "must be a valid account ID, organization ARN/ID, or organizational unit ARN/ID") - the generic placeholder string satisfies neither.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"ACCOUNT"`))
			body.SetAttributeRaw("principal_id", exprTokens(`"123456789012"`))
		},
	},
	"aws_auditmanager_assessment": {
		Reasons: []string{
			`roles is Optional-shaped in nothing else - the provider requires at least one roles block in practice (validate: "Block roles must have a configuration value as the provider has marked it as required"), with role_arn and role_type both Required inside it (schema doesn't surface either as top-level Required).`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			roles := body.AppendNewBlock("roles", nil)
			roles.Body().SetAttributeRaw("role_arn", exprTokens(ref))
			roles.Body().SetAttributeRaw("role_type", exprTokens(`"PROCESS_OWNER"`))
		},
	},
	"aws_organizations_account": {
		Reasons: []string{
			`email is Required and the provider validates it is a well-formed email address (validate: "invalid value for email (must be a valid email address)"); the generic placeholder string is not one.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("email", exprTokens(fmt.Sprintf(`"tofu-%s-cohort@example.com"`, g.cohort)))
		},
	},
	"aws_organizations_organizational_unit": {
		Reasons: []string{
			`parent_id is Required and the provider validates it is a well-formed root or OU identifier (validate: "invalid value for parent_id"); the generic placeholder string is neither. Set to a syntactically valid organization root ID - no real root or parent OU is part of this cohort to reference.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("parent_id", exprTokens(`"r-a1b2"`))
		},
	},
	"aws_organizations_policy": {
		Reasons: []string{
			`schema requires content as a plain string, but the provider validates it is well-formed JSON (validate: "\"content\" contains an invalid JSON"); the generic string placeholder is not JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("content", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "*"
      Resource = "*"
    }]
  })`))
		},
	},
	"aws_servicecatalog_product": {
		Reasons: []string{
			`type is Required and the provider validates it against a closed enum (validate: "expected type to be one of [\"CLOUD_FORMATION_TEMPLATE\" \"MARKETPLACE\" \"TERRAFORM_OPEN_SOURCE\" \"TERRAFORM_CLOUD\" \"EXTERNAL\"]"); the generic placeholder string is not a member. provisioning_artifact_parameters is a required block whose own template_physical_id and template_url are both Optional in the schema, but the provider requires exactly one of them in practice (validate: "one of ... must be specified"), and the generic pass sets neither.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"CLOUD_FORMATION_TEMPLATE"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "provisioning_artifact_parameters" {
					blk.Body().SetAttributeRaw("template_url", exprTokens(fmt.Sprintf(
						`"https://s3.amazonaws.com/tofu-%s-cohort/servicecatalog-product-template.json"`, g.cohort)))
				}
			}
		},
	},
	"aws_servicecatalog_provisioned_product": {
		Reasons: []string{
			`schema requires none of product_id/product_name or provisioning_artifact_id/provisioning_artifact_name; the provider requires exactly one of each pair in practice (validate: "one of ... must be specified" ×4), and the generic pass sets none of the four. product_id is wired to this cohort's own aws_servicecatalog_product; provisioning_artifact_name stays a literal, since no admitted type in this cohort represents a specific provisioning artifact.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			productIDExpr := fmt.Sprintf(`"prod-tofu-%s-cohort-placeholder"`, g.cohort)
			if product, ok := g.byType["aws_servicecatalog_product"]; ok {
				productIDExpr = product.String() + ".id"
			}
			body.SetAttributeRaw("product_id", exprTokens(productIDExpr))
			body.SetAttributeRaw("provisioning_artifact_name", exprTokens(`"v1"`))
		},
	},
	"aws_servicecatalogappregistry_attribute_group": {
		Reasons: []string{
			`schema requires attributes as a plain string, but the provider validates it is well-formed JSON (validate: "Invalid JSON String Value"); the generic string placeholder is not JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("attributes", exprTokens(`jsonencode({
    environment = "governance"
  })`))
		},
	},
	"aws_servicecatalogappregistry_attribute_group_association": {
		Reasons: []string{
			`application_id and attribute_group_id both validate fine as generic placeholder strings (no ARN/enum/JSON constraint), so this override exists only to wire them to this cohort's own aws_servicecatalogappregistry_application and aws_servicecatalogappregistry_attribute_group instead - the two markers internal/live/identity/table.go's own entry for this type documents the composite identity as running through.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			appExpr := `"placeholder"`
			if app, ok := g.byType["aws_servicecatalogappregistry_application"]; ok {
				appExpr = app.String() + ".id"
			}
			agExpr := `"placeholder"`
			if ag, ok := g.byType["aws_servicecatalogappregistry_attribute_group"]; ok {
				agExpr = ag.String() + ".id"
			}
			body.SetAttributeRaw("application_id", exprTokens(appExpr))
			body.SetAttributeRaw("attribute_group_id", exprTokens(agExpr))
		},
	},
	// ---- data-movement batch (issue #65) ---------------------------------

	"aws_appintegrations_data_integration": {
		Reasons: []string{
			`source_uri is a required string the schema does not constrain, but the provider validates it against a fixed pattern (validate: "invalid value for source_uri (should be a valid source uri)"), documented as a connector-profile scheme like "Salesforce://AppFlow/example"; the generic placeholder string does not match it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("source_uri", exprTokens(fmt.Sprintf(`"Salesforce://AppFlow/tofu-%s-cohort"`, g.cohort)))
		},
	},
	"aws_appintegrations_event_integration": {
		Reasons: []string{
			`event_filter.source is a required string the schema does not constrain, but the provider validates it against a fixed prefix regex (validate: "should be not be more than 255 alphanumeric, forward slashes, dots, underscores, or hyphen characters" - the message text does not match the actual pattern, which the provider's own source requires to start "aws.partner/"); the generic placeholder string does not start with it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "event_filter" {
					blk.Body().SetAttributeRaw("source", exprTokens(fmt.Sprintf(
						`"aws.partner/tofu-%s-cohort"`, g.cohort)))
				}
			}
		},
	},
	"aws_datasync_agent": {
		Reasons: []string{
			`activation_key and ip_address are both Optional in the schema, but the provider requires one of them at apply time ("one of activation_key or ip_address is required") - an apply-time-only gap ` + "`terraform validate`" + ` does not catch, found by hand-verifying this cohort against the pinned floci image. ip_address makes Terraform itself perform an HTTP GET against that address to retrieve the real activation key before the DataSync API call happens at all, which hangs indefinitely against any address that is not an actual reachable agent appliance; activation_key needs no such round-trip, so it is the one this override sets`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("activation_key", exprTokens(`"placeholder-activation-key"`))
		},
	},
	"aws_datasync_location_azure_blob": {
		Reasons: []string{
			`authentication_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected authentication_type to be one of [\"SAS\" \"NONE\"]"); agent_arns is a required set of strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authentication_type", exprTokens(`"SAS"`))
			body.SetAttributeRaw("agent_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:datasync:us-east-1:000000000000:agent/agent-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_location_efs": {
		Reasons: []string{
			`efs_file_system_arn and ec2_config's security_group_arns/subnet_arn are all required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("efs_file_system_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:elasticfilesystem:us-east-1:000000000000:file-system/fs-tofu%scohort"`, g.cohort)))
			for _, blk := range body.Blocks() {
				if blk.Type() == "ec2_config" {
					blk.Body().SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
						`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
					blk.Body().SetAttributeRaw("subnet_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:ec2:us-east-1:000000000000:subnet/subnet-tofu%scohort"`, g.cohort)))
				}
			}
		},
	},
	"aws_datasync_location_fsx_lustre_file_system": {
		Reasons: []string{
			`fsx_filesystem_arn and security_group_arns are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("fsx_filesystem_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:fsx:us-east-1:000000000000:file-system/fs-tofu%scohort"`, g.cohort)))
			body.SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_location_fsx_ontap_file_system": {
		Reasons: []string{
			`security_group_arns and storage_virtual_machine_arn are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN"); protocol's nfs and smb sub-blocks are both Optional in the schema, but the provider requires exactly one set (validate: "one of protocol.0.nfs,protocol.0.smb must be specified"), and the generic pass renders neither, unlike its sibling aws_datasync_location_fsx_openzfs_file_system, whose protocol block has only one sub-block to choose`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
			body.SetAttributeRaw("storage_virtual_machine_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:fsx:us-east-1:000000000000:storage-virtual-machine/svm-tofu%scohort"`, g.cohort)))
			for _, blk := range body.Blocks() {
				if blk.Type() == "protocol" {
					nfs := blk.Body().AppendNewBlock("nfs", nil)
					nfs.Body().AppendNewBlock("mount_options", nil)
				}
			}
		},
	},
	"aws_datasync_location_fsx_openzfs_file_system": {
		Reasons: []string{
			`fsx_filesystem_arn and security_group_arns are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("fsx_filesystem_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:fsx:us-east-1:000000000000:file-system/fs-tofu%scohort"`, g.cohort)))
			body.SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_location_fsx_windows_file_system": {
		Reasons: []string{
			`fsx_filesystem_arn and security_group_arns are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("fsx_filesystem_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:fsx:us-east-1:000000000000:file-system/fs-tofu%scohort"`, g.cohort)))
			body.SetAttributeRaw("security_group_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:ec2:us-east-1:000000000000:security-group/sg-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_location_hdfs": {
		Reasons: []string{
			`agent_arns is a required set of strings the provider validates are well-formed ARNs (validate: "is an invalid ARN"); name_node.port is a required number the schema does not range-constrain, but the provider validates it is a valid port (validate: "expected \"name_node.0.port\" to be a valid port number, got: 0"), and the generic pass's numeric zero placeholder is not one; authentication_type is Optional in the schema, but the provider's own AWS SDK request validation requires it client-side before any HTTP call is made ("missing required field, CreateLocationHdfsInput.AuthenticationType") - ` + "`terraform validate`" + ` does not catch this one either, only an apply against the pinned floci image did; SIMPLE also requires simple_user, which the schema likewise leaves Optional`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("agent_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:datasync:us-east-1:000000000000:agent/agent-tofu%scohort"`+"]", g.cohort)))
			body.SetAttributeRaw("authentication_type", exprTokens(`"SIMPLE"`))
			body.SetAttributeRaw("simple_user", exprTokens(`"placeholder"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "name_node" {
					blk.Body().SetAttributeRaw("port", exprTokens(`8020`))
				}
			}
		},
	},
	"aws_datasync_location_nfs": {
		Reasons: []string{
			`on_prem_config.agent_arns is a required set of strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "on_prem_config" {
					blk.Body().SetAttributeRaw("agent_arns", exprTokens(fmt.Sprintf(
						`["arn:aws:datasync:us-east-1:000000000000:agent/agent-tofu%scohort"`+"]", g.cohort)))
				}
			}
		},
	},
	"aws_datasync_location_s3": {
		Reasons: []string{
			`s3_bucket_arn is a required string the provider validates is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("s3_bucket_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:s3:::tofu-%s-cohort-datasync-s3"`, g.cohort)))
		},
	},
	"aws_datasync_location_smb": {
		Reasons: []string{
			`agent_arns is a required set of strings the provider validates are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("agent_arns", exprTokens(fmt.Sprintf(
				`["arn:aws:datasync:us-east-1:000000000000:agent/agent-tofu%scohort"`+"]", g.cohort)))
		},
	},
	"aws_datasync_task": {
		Reasons: []string{
			`destination_location_arn and source_location_arn are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN") - overridden to this cohort's own aws_datasync_location_nfs.app.arn and aws_datasync_location_s3.app.arn, the cross-resource reference issue #56 asks for and the provider's own documented example uses verbatim`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if src, ok := g.byType["aws_datasync_location_nfs"]; ok {
				body.SetAttributeRaw("source_location_arn", exprTokens(fmt.Sprintf("%s.arn", src)))
			}
			if dst, ok := g.byType["aws_datasync_location_s3"]; ok {
				body.SetAttributeRaw("destination_location_arn", exprTokens(fmt.Sprintf("%s.arn", dst)))
			}
		},
	},
	"aws_dms_certificate": {
		Reasons: []string{
			`certificate_pem and certificate_wallet are both Optional in the schema, but the provider requires exactly one set (validate: "one of certificate_pem,certificate_wallet must be specified"), and the generic pass sets neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("certificate_pem", exprTokens(`"placeholder-pem"`))
		},
	},
	"aws_dms_endpoint": {
		Reasons: []string{
			`endpoint_type and engine_name are both required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]"); "s3" is not among engine_name's valid values - that shape is what the separate aws_dms_s3_endpoint type below covers`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("endpoint_type", exprTokens(`"source"`))
			body.SetAttributeRaw("engine_name", exprTokens(`"mysql"`))
		},
	},
	"aws_dms_event_subscription": {
		Reasons: []string{
			`sns_topic_arn is a required string the provider validates is a well-formed ARN (validate: "is an invalid ARN"), and no aws_sns_topic is part of this cohort to reference, the same gap aws_db_event_subscription's own override fills; source_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected source_type to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("sns_topic_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:sns:us-east-1:000000000000:tofu-%s-cohort-events"`, g.cohort)))
			body.SetAttributeRaw("source_type", exprTokens(`"replication-task"`))
		},
	},
	"aws_dms_replication_config": {
		Reasons: []string{
			`replication_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected replication_type to be one of [...]"); source_endpoint_arn and target_endpoint_arn are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN") - overridden to this cohort's own aws_dms_endpoint.app.endpoint_arn and aws_dms_s3_endpoint.app.endpoint_arn, the cross-resource reference issue #56 asks for and the provider's own documented example uses the same way; table_mappings is a required string the provider validates is well-formed JSON (validate: "contains an invalid JSON"); compute_config.replication_subnet_group_id is Optional in the schema, but the generic pass already renders a placeholder for it - overridden to this cohort's own aws_dms_replication_subnet_group.app.replication_subnet_group_id instead`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("replication_type", exprTokens(`"full-load"`))
			if ep, ok := g.byType["aws_dms_endpoint"]; ok {
				body.SetAttributeRaw("source_endpoint_arn", exprTokens(fmt.Sprintf("%s.endpoint_arn", ep)))
			}
			if ep, ok := g.byType["aws_dms_s3_endpoint"]; ok {
				body.SetAttributeRaw("target_endpoint_arn", exprTokens(fmt.Sprintf("%s.endpoint_arn", ep)))
			}
			body.SetAttributeRaw("table_mappings", exprTokens(`jsonencode({
    rules = [{
      rule-type   = "selection"
      rule-id     = "1"
      rule-name   = "1"
      object-locator = {
        schema-name = "%"
        table-name  = "%"
      }
      rule-action = "include"
    }]
  })`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "compute_config" {
					if sng, ok := g.byType["aws_dms_replication_subnet_group"]; ok {
						blk.Body().SetAttributeRaw("replication_subnet_group_id", exprTokens(
							fmt.Sprintf("%s.replication_subnet_group_id", sng)))
					}
				}
			}
		},
	},
	"aws_dms_replication_subnet_group": {
		Reasons: []string{
			`subnet_ids is a required list with a provider-enforced 2-item minimum (validate: "Attribute subnet_ids requires 2 item minimum, but config has only 1 declared"), the same MinItems gap aws_route53_resolver_firewall_rule's ip_address override fixes in the observability batch, and the schema itself does not say so`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("subnet_ids", exprTokens(
				`["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]`))
		},
	},
	"aws_dms_replication_task": {
		Reasons: []string{
			`migration_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected migration_type to be one of [...]"); replication_instance_arn, source_endpoint_arn and target_endpoint_arn are all required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN") - overridden to this cohort's own aws_dms_replication_instance.app.replication_instance_arn, aws_dms_endpoint.app.endpoint_arn and aws_dms_s3_endpoint.app.endpoint_arn, the cross-resource reference issue #56 asks for; table_mappings is a required string the provider validates is well-formed JSON (validate: "contains an invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("migration_type", exprTokens(`"full-load"`))
			if ri, ok := g.byType["aws_dms_replication_instance"]; ok {
				body.SetAttributeRaw("replication_instance_arn", exprTokens(
					fmt.Sprintf("%s.replication_instance_arn", ri)))
			}
			if ep, ok := g.byType["aws_dms_endpoint"]; ok {
				body.SetAttributeRaw("source_endpoint_arn", exprTokens(fmt.Sprintf("%s.endpoint_arn", ep)))
			}
			if ep, ok := g.byType["aws_dms_s3_endpoint"]; ok {
				body.SetAttributeRaw("target_endpoint_arn", exprTokens(fmt.Sprintf("%s.endpoint_arn", ep)))
			}
			body.SetAttributeRaw("table_mappings", exprTokens(`jsonencode({
    rules = [{
      rule-type   = "selection"
      rule-id     = "1"
      rule-name   = "1"
      object-locator = {
        schema-name = "%"
        table-name  = "%"
      }
      rule-action = "include"
    }]
  })`))
		},
	},
	"aws_dms_s3_endpoint": {
		Reasons: []string{
			`endpoint_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected endpoint_type to be one of [...]") - set to "target", pairing with aws_dms_endpoint.app's own "source" so aws_dms_replication_config and aws_dms_replication_task above have one endpoint of each type to reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("endpoint_type", exprTokens(`"target"`))
		},
	},
	"aws_transfer_user": {
		Reasons: []string{
			`server_id is a required string the provider validates is a well-formed Transfer server id, lowercase alphanumeric only (validate: "isn't a valid transfer server id") - the generic placeholder string is not one; overridden to this cohort's own aws_transfer_server.app.id, the cross-resource reference issue #56 asks for and exactly the composite this type's own internal/live/identity/table.go entry (server_id/user_name) is ratified on`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if srv, ok := g.byType["aws_transfer_server"]; ok {
				body.SetAttributeRaw("server_id", exprTokens(fmt.Sprintf("%s.id", srv)))
			}
		},
	},
	"aws_transfer_workflow": {
		Reasons: []string{
			`steps.type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [...]"); DELETE needs no further delete_step_details block, the smaller of the five shapes`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "steps" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"DELETE"`))
				}
			}
		},
	},
	// Databases batch (issue #65). Several entries below fix the same
	// parentRef mis-wiring shape aws_ecs_daemon and aws_eks_access_entry
	// document above: a type whose own "name" argument is not its
	// identity.LookupType-visible identity (either because it is
	// server-assigned, per identityArgName's rule at gen.go:114-124, or
	// because its identity is a multi-component composite, e.g. the three
	// OpenSearchServerless policy types' name+type pair) has no competing
	// claim on "name", so parentRef's same-name search silently wires it to
	// the alphabetically-first sibling that does own "name" as a
	// single-component identity - aws_docdb_event_subscription in this
	// cohort. Every one of those types is corrected back to its own literal
	// name below; none of them has any real relationship to a DocDB event
	// subscription. The rest of the entries below fix real
	// `terraform validate` failures: a placeholder string that is not a
	// well-formed ARN, exceeds a length limit, or is not a member of a
	// closed enum the schema itself does not carry; one
	// (aws_redshift_cluster) fixes a provider-side requirement that
	// validate does not catch at all, only a real apply against floci.
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
			`"name" was mis-wired to aws_docdb_event_subscription.app.name (this type is server-assigned per internal/live/identity/table.go, so identityArgName never claims "name" as its own, and parentRef's same-name search picks the alphabetically-first sibling that does); corrected to a literal name. "auth_type" is a required argument the provider validates against a closed enum (PLAIN_TEXT, SECRET_ARN per the provider's own Argument Reference), and the generic placeholder string is neither. "shard_capacity" and "shard_count" are both required integers the generic pass leaves at their zero value, which the provider's own documented Argument Reference says is below the minimum in practice (not caught by validate, found by reading the provider's example usage) - set to the documented example's own values`,
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
			`"name" was mis-wired to aws_docdb_event_subscription.app.name (this type's identity is the composite name+type pair, more than one Component, so identityArgName never claims "name" as its own single-component identity - the same shape gen.go:116's "len(entry.Components) != 1" comment describes); corrected to a literal name. "type" is required and the provider validates it against a one-member closed enum (must be "data", per the provider's own Argument Reference); the generic placeholder is not. "policy" is a required string the provider validates as well-formed JSON matching its own access-policy shape (Rules/Principal), confirmed against the provider's documented example`,
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
	"aws_redshift_snapshot_schedule": {
		Reasons: []string{
			`"definitions" is a list of schedule expressions the schema types as unconstrained strings; the generic placeholder is neither a documented cron nor rate expression (not caught by validate, found by reading the provider's own documented example) - set to that same example's "rate(12 hours)"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("definitions", exprTokens(`["rate(12 hours)"]`))
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

// medialiveMultiplexIDRef is the sibling aws_medialive_multiplex's own id
// attribute as HCL source when this run renders one, or a literal
// placeholder id-shaped string otherwise. multiplex_id is not a
// single-component identity argument gen.go's parentRef links
// automatically (aws_medialive_multiplex is server-assigned, so
// identityArgName returns ok=false for it, the same gap
// cognitoUserPoolIDRef below documents for its own type), so
// aws_medialive_multiplex_program's own override resolves it here by hand.
func medialiveMultiplexIDRef(g *generator) string {
	addr, ok := g.byType["aws_medialive_multiplex"]
	if !ok {
		return `"12345678"`
	}
	return fmt.Sprintf("%s.id", addr)
}

// eksClusterNameRef is the sibling aws_eks_cluster's name attribute as HCL
// source when this run renders one, or a literal placeholder (a cohort
// that requests one of the six EKS composite children without
// aws_eks_cluster itself, which none of this generator's own cohorts do).
func eksClusterNameRef(g *generator) string {
	addr, ok := g.byType["aws_eks_cluster"]
	if !ok {
		return `"placeholder"`
	}
	return fmt.Sprintf("%s.name", addr)
}

// cognitoUserPoolIDRef is the sibling aws_cognito_user_pool's id attribute
// as HCL source when this run renders one, or a literal placeholder
// user-pool-id-shaped string otherwise. user_pool_id is not a
// single-component identity argument gen.go's parentRef links
// automatically (aws_cognito_user_pool is server-assigned, so
// identityArgName returns ok=false for it), so every Cognito child type
// that takes a user_pool_id argument resolves it here by hand instead of
// rendering its own independent, disconnected placeholder - the same
// conditional-sibling shape as ssoadminApplicationArnRef below, but for a
// real pool a floci apply can actually find rather than a synthesized id
// no CreateUserPool call ever minted.
func cognitoUserPoolIDRef(g *generator) string {
	addr, ok := g.byType["aws_cognito_user_pool"]
	if !ok {
		return `"us-east-1_tofuidpool"`
	}
	return fmt.Sprintf("%s.id", addr)
}

// cognitoUserGroupNameRef is the sibling aws_cognito_user_group's name
// attribute as HCL source when this run renders one, or a literal
// placeholder otherwise - for aws_cognito_user_in_group's own group_name,
// the same conditional-sibling shape as cognitoUserPoolIDRef above.
func cognitoUserGroupNameRef(g *generator) string {
	addr, ok := g.byType["aws_cognito_user_group"]
	if !ok {
		return `"placeholder"`
	}
	return fmt.Sprintf("%s.name", addr)
}

// cognitoUsernameRef is the sibling aws_cognito_user's username attribute
// as HCL source when this run renders one, or a literal placeholder
// otherwise - for aws_cognito_user_in_group's own username, same shape as
// cognitoUserGroupNameRef above.
func cognitoUsernameRef(g *generator) string {
	addr, ok := g.byType["aws_cognito_user"]
	if !ok {
		return `"placeholder"`
	}
	return fmt.Sprintf("%s.username", addr)
}

// iamPolicyArnRef is the sibling aws_iam_policy's own arn attribute as HCL
// source when this run renders one, or a literal placeholder ARN
// otherwise - for the two IAM policy-attachment types' own policy_arn,
// same conditional-sibling shape as ssoadminApplicationArnRef below
// (aws_iam_policy is server-assigned, so identityArgName gives parentRef
// nothing to link automatically).
func iamPolicyArnRef(g *generator) string {
	addr, ok := g.byType["aws_iam_policy"]
	if !ok {
		return `"arn:aws:iam::000000000000:policy/tofu-identity-cohort-policy"`
	}
	return fmt.Sprintf("%s.arn", addr)
}

// ssoadminApplicationArnRef is the sibling aws_ssoadmin_application's arn
// attribute as HCL source when this run renders one, or a literal
// placeholder ARN otherwise - the same conditional-sibling shape as
// eksClusterNameRef above, needed because application_arn is not a
// single-component identity argument gen.go's parentRef links
// automatically (see aws_ssoadmin_application_assignment's own override).
func ssoadminApplicationArnRef(g *generator) string {
	addr, ok := g.byType["aws_ssoadmin_application"]
	if !ok {
		return `"arn:aws:sso::000000000000:application/id-tofucohort"`
	}
	return fmt.Sprintf("%s.arn", addr)
}

// ssoadminPermissionSetArnRef is the sibling aws_ssoadmin_permission_set's
// arn attribute as HCL source when this run renders one, or a literal
// placeholder ARN otherwise - same shape as ssoadminApplicationArnRef
// above, for aws_ssoadmin_account_assignment's own permission_set_arn.
func ssoadminPermissionSetArnRef(g *generator) string {
	addr, ok := g.byType["aws_ssoadmin_permission_set"]
	if !ok {
		return `"arn:aws:sso:::permissionSet/ssoins-tofucohortid00/ps-tofucohortid00"`
	}
	return fmt.Sprintf("%s.arn", addr)
}
