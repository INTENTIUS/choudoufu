// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The iam-ecr cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableIamEcr = []string{
	// Registry-ratified IAM and ECR batch (#40, #44, issue #26).
	"aws_ecr_repository",
	"aws_iam_instance_profile",
	"aws_iam_service_linked_role",
	"aws_iam_user",
}

var untaggableIamEcr = []string{
	// Registry-ratified IAM and ECR batch (#40, #44, issue #26): three
	// singleton-per-account ECR types with no tags argument at all. See
	// live/e2e/estates/iam-ecr/README.md, "Untaggable types".
	// Registry-ratified ECS/EKS batch (#40, #44, issue #65): the
	// deferred aws_iam_group (its own old blocker, the doc gate, closed
	// by #54) plus this batch's two untaggable ECS/EKS rows. See
	// live/e2e/estates/ecs-eks/README.md, "Untaggable types".
	"aws_iam_group",
}

func init() {
	registerCohortStamp(taggableIamEcr, untaggableIamEcr, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified IAM and ECR batch (#40, #44, issue #26). Taggable
			// per the real provider's documented Argument Reference, except the
			// three ECR registry-level singletons, whose Argument Reference names
			// no tags block at all.
			"aws_ecr_repository":                      taggedSchema("id", "arn", "name", "registry_id", "repository_url"),
			"aws_ecr_registry_policy":                 untaggedSchema("id", "registry_id", "policy"),
			"aws_ecr_registry_scanning_configuration": untaggedSchema("id", "registry_id", "scan_type"),
			"aws_ecr_replication_configuration":       untaggedSchema("id", "registry_id"),
			"aws_iam_instance_profile":                taggedSchema("id", "arn", "name", "role"),
			"aws_iam_service_linked_role":             taggedSchema("id", "arn", "name", "aws_service_name"),
			"aws_iam_user":                            taggedSchema("id", "arn", "name"),
			// Registry-ratified ECS/EKS batch (#40, #44, issue #65). Taggable
			// per the real provider's documented Argument Reference, except
			// aws_ecs_cluster_capacity_providers and
			// aws_eks_access_policy_association, whose Argument Reference names
			// no tags block at all, and the deferred aws_iam_group (#54
			// unblocked it; IAM groups have no TagGroup API to begin with).
			"aws_iam_group": untaggedSchema("id", "arn", "name"),
		})
	})
}
