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
}
