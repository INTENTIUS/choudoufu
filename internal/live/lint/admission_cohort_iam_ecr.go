// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesIamEcr is the iam-ecr cohort's slice of [admittedTypesV0]:
// the types the iam-ecr ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesIamEcr = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): second batch, IAM and ECR
	// ---- (issue #26). Same evidence source and verification standard as
	// ---- the first Lambda batch above; see internal/live/identity/table.go
	// ---- for the per-type evidence and for the row-gen proposals this
	// ---- batch rejected or deferred. Cohort estate:
	// ---- live/e2e/estates/iam-ecr.
	// tools/row-gen proposed 13 pastable rows across the two services
	// (plus evidence-only and needs-hand-separator rows this batch never
	// touches); 7 ratified here, 5 rejected, 1 deferred — see table.go.
	// #26's two named types, aws_ecr_repository and aws_iam_user, are both
	// in this batch.
	"aws_ecr_registry_policy":                 {},
	"aws_ecr_registry_scanning_configuration": {},
	"aws_ecr_replication_configuration":       {},
	"aws_ecr_repository":                      {},
	"aws_iam_instance_profile":                {},
	"aws_iam_service_linked_role":             {},
	"aws_iam_user":                            {},
	// aws_iam_group: deferred by this IAM/ECR batch pending #54, which has
	// since generalized live/LIMITATIONS.md's "Untaggable types" derivation
	// past the curated 68 (see internal/live/identity/table.go for the
	// evidence, unchanged since this batch's own deferral note). Ratified
	// here by the ECS/EKS batch (#65), which lands the two #54-unblocked
	// deferrals alongside its own cohort rather than opening a two-type
	// cohort.
	"aws_iam_group": {},
}

func init() { registerCohortAdmitted(admittedTypesIamEcr) }
