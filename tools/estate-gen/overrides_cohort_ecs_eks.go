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
}

func init() { registerCohortOverrides(typeOverridesEcsEks) }
