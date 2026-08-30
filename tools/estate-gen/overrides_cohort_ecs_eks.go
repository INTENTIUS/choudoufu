// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesEcsEks is the ecs-eks cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesEcsEks = map[string]typeOverride{
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
	"aws_ecs_cluster_capacity_providers": {
		Reasons: []string{
			`cluster_name must name an actual ECS cluster - PutClusterCapacityProviders 400s with ClusterNotFoundException against one that does not exist - and no resource in this cohort's own requested types creates one, since aws_ecs_cluster is already covered by live/e2e/estate/ rather than by this cohort. A supporting aws_ecs_cluster is generated instead (NeedsSupporting), the same "supporting, not coverage" shape as the shared aws_iam_role; this entry folds the hand-written block live/e2e/estates/ecs-eks carried before #108 criterion 4, which every regeneration reverted`,
		},
		NeedsSupporting: []string{"aws_ecs_cluster"},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if cluster, ok := g.byType["aws_ecs_cluster"]; ok {
				body.SetAttributeRaw("cluster_name", exprTokens(cluster.Type+"."+cluster.Label+".name"))
			}
		},
	},
	"aws_ecs_capacity_provider": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (#136's cohort/type-fix rule: this type's identity is the six-Component assembled ARN template, so identityArgName never claimed "name" as its own single-component identity - but a bare "name" argument is now never treated as a same-named sibling's parent regardless); kept set to its own literal for consistency with aws_ecs_daemon's own "name" override below. The provider requires exactly one of auto_scaling_group_provider or managed_instances_provider (the doc's own NOTE), a "one of" the wire schema encodes as two Optional blocks rather than either being Required, so the generic required-block pass emits neither. auto_scaling_group_provider is the shallower shape - only auto_scaling_group_arn is Required inside it, where managed_instances_provider nests three further Required fields (instance_launch_template.ec2_instance_profile_arn, instance_launch_template.network_configuration.subnets, instance_requirements.memory_mib) - so that block is added here with a well-formed placeholder ARN: no aws_autoscaling_group resource exists in this cohort's own requested types to reference (that type belongs to the ec2-core cohort), the same "no admitted type exists in this cohort to reference" shape aws_eks_access_policy_association's access_scope.type placeholder above already uses`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-capacity-provider"`, g.cohort)))
			blk := body.AppendNewBlock("auto_scaling_group_provider", nil)
			blk.Body().SetAttributeRaw("auto_scaling_group_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:autoscaling:us-east-1:000000000000:autoScalingGroup:00000000-0000-0000-0000-000000000000:autoScalingGroupName/tofu-%s-cohort-asg"`, g.cohort)))
		},
	},
	"aws_ecs_daemon": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (#136's cohort/type-fix rule: a bare "name" argument is never treated as a same-named sibling's parent); kept set to its own literal. capacity_provider_arns and daemon_task_definition_arn are left unset here: the generic pass's siblingRef now recognizes both the singular "<base>_arn" shape and its plural "<base>_arns" counterpart over a list/set(string) argument, so both wire on their own to the cohort's own aws_ecs_capacity_provider.app.arn and aws_ecs_daemon_task_definition.app.arn respectively - no hand wiring left to do`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-ecs-daemon"`, g.cohort)))
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
	// Issue #554, found while regenerating this cohort to fix aws_ecs_service
	// below (unrelated to that fix, but surfaced by the same regeneration -
	// the #539 shape, caught here rather than shipped instead of joining
	// it). live/e2e/estates/ecs-eks/supporting.tf carried this resource's
	// full doc-example body - billing_mode, hash_key, range_key,
	// read_capacity, write_capacity, three "attribute" blocks (UserId,
	// GameTitle, TopScore), a ttl block and a global_secondary_index block -
	// entirely from seedFromExample, with no override, until this
	// regeneration silently dropped GameTitle and TopScore's own attribute
	// blocks: seedFromExample only ever lands a repeated block's FIRST
	// element (seed.go's own doc comment: "the generic pass renders exactly
	// one instance, so element zero is the only answer to which element
	// does this belong to"), so the doc example's second and third
	// "attribute" elements were never reachable, even though the table's
	// own range_key ("GameTitle") and global_secondary_index (hash_key
	// "GameTitle", range_key "TopScore") both need a matching
	// AttributeDefinition or CreateTable 400s ("... does not have a
	// corresponding entry in AttributeDefinitions"). This is a real,
	// pre-existing gap in seedFromExample's repeated-block handling, not
	// something #554 introduced - confirmed by regenerating this cohort
	// against unmodified main, before any of this issue's fixes, which
	// drops the identical two blocks - and it is general to any type whose
	// doc example repeats a block more than once, not specific to this
	// type; fixing seedFromExample itself to create more than one instance
	// of a repeated block is out of this issue's scope (a rule-level
	// change touching every cohort, not a fixture fix) and belongs in its
	// own issue.
	//
	// An override is the only lever available today to add the missing
	// content without that deeper fix, but adding ANY override for a type
	// suppresses seedFromExample for that type entirely (seed.go: "an
	// override suppresses the seed for its whole type" - the
	// aws_lambda_layer_version incident #136 fixed by making seeding an
	// all-or-nothing choice per type). So this override does not merely
	// append the two missing attribute blocks; it reconstructs the type's
	// entire body, byte-for-byte what seedFromExample used to produce, so
	// that adding it trades no other field away. "name" is left to the
	// generic pass, unaffected either way: aws_dynamodb_table self-
	// identifies by "name" in the identity table, so valueExpr's own
	// identity-argument tier sets it before Apply ever runs, the same as
	// every other override in this file that never sets "name" itself.
	"aws_dynamodb_table": {
		Reasons: []string{
			`the doc's own example sets billing_mode, hash_key, range_key, read_capacity, write_capacity, three "attribute" blocks (UserId, GameTitle, TopScore), a ttl block and a global_secondary_index block - previously reached entirely through seedFromExample with no override at all, but seedFromExample only ever lands a repeated block's first element (seed.go: "the generic pass renders exactly one instance, so element zero is the only answer"), so GameTitle and TopScore's own "attribute" blocks - both referenced by this table's own range_key and global_secondary_index - were never reachable that way; CreateTable 400s without a matching AttributeDefinition for each ("... does not have a corresponding entry in AttributeDefinitions"). A pre-existing, general gap in seedFromExample's repeated-block handling (confirmed against unmodified main), not specific to this type; reconstructed here in full rather than as a two-block patch, because adding any override for a type suppresses seedFromExample for that whole type (seed.go's own doc comment) - a partial override would have traded the rest of this body away`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("billing_mode", exprTokens(`"PROVISIONED"`))
			body.SetAttributeRaw("hash_key", exprTokens(`"UserId"`))
			body.SetAttributeRaw("range_key", exprTokens(`"GameTitle"`))
			body.SetAttributeRaw("read_capacity", exprTokens(`20`))
			body.SetAttributeRaw("write_capacity", exprTokens(`20`))
			for _, a := range []struct{ name, typ string }{
				{"UserId", "S"},
				{"GameTitle", "S"},
				{"TopScore", "N"},
			} {
				blk := body.AppendNewBlock("attribute", nil)
				blk.Body().SetAttributeRaw("name", exprTokens(fmt.Sprintf("%q", a.name)))
				blk.Body().SetAttributeRaw("type", exprTokens(fmt.Sprintf("%q", a.typ)))
			}
			ttl := body.AppendNewBlock("ttl", nil)
			ttl.Body().SetAttributeRaw("attribute_name", exprTokens(`"TimeToExist"`))
			ttl.Body().SetAttributeRaw("enabled", exprTokens(`true`))
			gsi := body.AppendNewBlock("global_secondary_index", nil)
			gsi.Body().SetAttributeRaw("hash_key", exprTokens(`"GameTitle"`))
			gsi.Body().SetAttributeRaw("name", exprTokens(`"GameTitleIndex"`))
			gsi.Body().SetAttributeRaw("non_key_attributes", exprTokens(`["UserId"]`))
			gsi.Body().SetAttributeRaw("projection_type", exprTokens(`"INCLUDE"`))
			gsi.Body().SetAttributeRaw("range_key", exprTokens(`"TopScore"`))
			gsi.Body().SetAttributeRaw("read_capacity", exprTokens(`10`))
			gsi.Body().SetAttributeRaw("write_capacity", exprTokens(`10`))
		},
	},
	// Issue #554: "container_definitions" is Required per the wire schema,
	// but it is a plain string, not a validated JSON blob - the generic
	// required-only pass fills it with the type-driven placeholder
	// (genericExprText's "placeholder"), which is not valid JSON, the same
	// "schema says a string, provider validates it is JSON" shape as
	// aws_iam_role's own assume_role_policy override above.
	"aws_ecs_task_definition": {
		Reasons: []string{
			`"container_definitions" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "container_definitions ... contains an invalid JSON"); the generic placeholder string is not, same shape as aws_iam_role's own assume_role_policy override above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("container_definitions", exprTokens(`jsonencode([
    {
      name      = "app"
      image     = "nginx:latest"
      cpu       = 256
      memory    = 512
      essential = true
    }
  ])`))
		},
	},
	// Issue #554: "task_definition" is Optional in the wire schema (the
	// doc's own prose: "Required unless using the EXTERNAL deployment
	// controller"), so the generic required-only pass never visits it, and
	// CreateService 400s with "Unable to describe task definition" against
	// a service that declares none - not caught by "terraform validate"
	// itself, only surfaced at apply. This cohort's own requested types
	// include no aws_ecs_task_definition to reference (only the unrelated
	// aws_ecs_daemon_task_definition, a distinct newer resource
	// aws_ecs_daemon already wires on its own) - a supporting
	// aws_ecs_task_definition is generated instead (NeedsSupporting), the
	// same "supporting, not coverage" shape as aws_ecs_cluster_capacity_providers'
	// own aws_ecs_cluster above.
	"aws_ecs_service": {
		Reasons: []string{
			`"task_definition" is Optional in the schema ("Required unless using the EXTERNAL deployment controller" per the doc, not expressed as a schema constraint), so the generic required-only pass never visits it and CreateService 400s with "Unable to describe task definition" against a service that declares none; this cohort's own requested types include no aws_ecs_task_definition to reference (only the unrelated aws_ecs_daemon_task_definition) - a supporting aws_ecs_task_definition is generated instead (NeedsSupporting), the same "supporting, not coverage" shape as aws_ecs_cluster_capacity_providers' own aws_ecs_cluster above. "force_new_deployment" is set here too, not left to seedFromExample: an override suppresses the seed pass for its whole type (seed.go's own doc comment), so adding this override for task_definition would otherwise silently drop the doc-seeded force_new_deployment = true this type carried before.`,
		},
		NeedsSupporting: []string{"aws_ecs_task_definition"},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if td, ok := g.byType["aws_ecs_task_definition"]; ok {
				body.SetAttributeRaw("task_definition", exprTokens(fmt.Sprintf("%s.arn", td)))
			}
			body.SetAttributeRaw("force_new_deployment", exprTokens(`true`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesEcsEks) }
