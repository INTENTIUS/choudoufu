// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesEcsEks is the ecs-eks cohort's slice of [admittedTypesV0]:
// the types the ecs-eks ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesEcsEks = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): fourth batch, ECS and EKS (issue
	// ---- #65). Same tools/row-gen pipeline and verification standard as
	// ---- the batches above; see internal/live/identity/table.go for the
	// ---- per-type evidence and for the row-gen proposals this batch
	// ---- rejected. Cohort estate: live/e2e/estates/ecs-eks.
	"aws_ecs_cluster_capacity_providers": {},
	"aws_ecs_daemon":                     {},
	"aws_eks_access_entry":               {},
	"aws_eks_access_policy_association":  {},
	"aws_eks_addon":                      {},
	"aws_eks_capability":                 {},
	"aws_eks_cluster":                    {},
	"aws_eks_fargate_profile":            {},
	"aws_eks_node_group":                 {},
}

func init() { registerCohortAdmitted(admittedTypesEcsEks) }
