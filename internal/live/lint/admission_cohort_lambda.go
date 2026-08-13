// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesLambda is the lambda cohort's slice of [admittedTypesV0]:
// the types the lambda ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesLambda = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): identity evidence comes from the
	// ---- CloudFormation Registry (live/registry.json) via
	// ---- tools/row-gen, joined against live/mapping.json, rather than
	// ---- from the provider's own identity schema. Each row below was
	// ---- proposed by row-gen and independently checked against the AWS
	// ---- provider's documented import behaviour before landing here — see
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- for the two row-gen proposals this batch rejected. Cohort
	// ---- estate: live/e2e/estates/lambda (#48's per-cohort mechanism).
	// First Lambda batch (8 row-gen proposals; 1 needs-hand-separator
	// skipped per #44's non-goals, 2 rejected — see table.go).
	"aws_lambda_capacity_provider":    {},
	"aws_lambda_code_signing_config":  {},
	"aws_lambda_event_source_mapping": {},
	"aws_lambda_function":             {},
	"aws_lambda_layer_version":        {},
}

func init() { registerCohortAdmitted(admittedTypesLambda) }
