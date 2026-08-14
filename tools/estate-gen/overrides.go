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

	// NeedsSupporting names additional resource types planCohort must add
	// as supporting rows (rendered by the generic pass, labelled with the
	// cohort name, exactly like the shared aws_iam_role) so that this
	// type's Apply has a real sibling to reference. It generalizes
	// NeedsIAMRole: aws_ecs_cluster_capacity_providers 400s with
	// ClusterNotFoundException against a cluster that does not exist, and
	// live/e2e/estates/ecs-eks carried that supporting cluster as a
	// hand-written block regeneration kept reverting (#108 criterion 4's
	// documented-hand-edit class; folded here instead).
	NeedsSupporting []string

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
}

// registerCohortOverrides folds one per-cohort fragment into
// [typeOverrides]. Every overrides_cohort_*.go file in this package calls it
// from its own init, so a cohort's overrides land in that cohort's file
// rather than at the shared tail of one 5,000-line literal.
//
// Package-level var initializers all run before any init func, so the core
// literal above is complete before the first fragment merges into it. A type
// overridden by two cohorts is refused rather than silently overwritten,
// matching tools/mapping-gen/overlay.d's add-only merge; see
// contributing/LIVE-TABLES.md.
func registerCohortOverrides(fragment map[string]typeOverride) {
	for k, v := range fragment {
		if _, dup := typeOverrides[k]; dup {
			panic("estate-gen: type " + k + " is overridden by more than one cohort fragment")
		}
		typeOverrides[k] = v
	}
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

// workspacesWebPortalArnRef is the sibling aws_workspacesweb_portal's own
// portal_arn attribute as HCL source when this run renders one, or a
// literal placeholder portal ARN otherwise - shared by all eight
// WorkSpacesWeb *_association fold-children's own portal_arn argument,
// same conditional-sibling shape as ssoadminApplicationArnRef above.
func workspacesWebPortalArnRef(g *generator) string {
	addr, ok := g.byType["aws_workspacesweb_portal"]
	if !ok {
		return `"arn:aws:workspaces-web:us-east-1:000000000000:portal/tofucohortid00"`
	}
	return fmt.Sprintf("%s.portal_arn", addr)
}

// workspacesWebArnRef is one WorkSpacesWeb *_association fold-child's own
// settings-type ARN argument as HCL source: the named sibling's own
// exported *_arn attribute when this run renders one, or a literal
// placeholder ARN of the given resource-path segment otherwise (each
// WorkSpacesWeb settings type's own ARN embeds a different path segment,
// e.g. "browserSettings", confirmed against each type's own documented
// import ID example).
func workspacesWebArnRef(g *generator, siblingType, attrName, arnPathSegment string) string {
	addr, ok := g.byType[siblingType]
	if !ok {
		return fmt.Sprintf(`"arn:aws:workspaces-web:us-east-1:000000000000:%s/tofucohortid00"`, arnPathSegment)
	}
	return fmt.Sprintf("%s.%s", addr, attrName)
}
