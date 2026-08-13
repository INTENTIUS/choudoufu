// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

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

// ssoadminApplicationArnRef is the sibling aws_ssoadmin_application's arn
// attribute as HCL source when this run renders one, or a literal
// placeholder ARN otherwise - the same conditional-sibling shape as
// eksClusterNameRef above, needed because application_arn is not a
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
