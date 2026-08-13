// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The ecs-eks cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableEcsEks = []string{
	// Registry-ratified ECS/EKS batch (#40, #44, issue #65).
	"aws_ecs_daemon",
	"aws_eks_access_entry",
	"aws_eks_addon",
	"aws_eks_capability",
	"aws_eks_cluster",
	"aws_eks_fargate_profile",
	"aws_eks_node_group",
}

var untaggableEcsEks = []string{
	"aws_ecs_cluster_capacity_providers",
	"aws_eks_access_policy_association",
}

func init() {
	registerCohortStamp(taggableEcsEks, untaggableEcsEks, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			"aws_ecs_cluster_capacity_providers": untaggedSchema("id", "cluster_name", "capacity_providers"),
			"aws_ecs_daemon":                     taggedSchema("id", "arn", "name", "cluster_arn"),
			"aws_eks_access_entry":               taggedSchema("cluster_name", "principal_arn"),
			"aws_eks_access_policy_association":  untaggedSchema("cluster_name", "principal_arn", "policy_arn"),
			"aws_eks_addon":                      taggedSchema("id", "arn", "cluster_name", "addon_name"),
			"aws_eks_capability":                 taggedSchema("arn", "cluster_name", "capability_name"),
			"aws_eks_cluster":                    taggedSchema("id", "arn", "name", "role_arn"),
			"aws_eks_fargate_profile":            taggedSchema("id", "arn", "cluster_name", "fargate_profile_name"),
			"aws_eks_node_group":                 taggedSchema("id", "arn", "cluster_name", "node_group_name"),
		})
	})
}
