// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesStragglers is the stragglers cohort's slice of [admittedTypesV0]:
// the types the stragglers ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesStragglers = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): stragglers batch. Not a new
	// ---- service sweep — every row here is a type an earlier ratified
	// ---- batch's own README named as reachable but left outside that
	// ---- batch's stated named scope (Transfer Family beyond
	// ---- server/user/workflow/connector, NetworkManager's
	// ---- core-network-policy fold, Storage Gateway's one registry-present
	// ---- type, and the IAM/ECR batch's own deferred ECR remainder). Same
	// ---- tools/row-gen pipeline as every batch above, cross-checked
	// ---- against the pinned v6.58.0 provider docs fetched directly (the
	// ---- Transfer Family entries) or already-scraped
	// ---- live/import-grammar.json evidence (NetworkManager, Storage
	// ---- Gateway, ECR) rather than accepted on the registry's
	// ---- classification alone. One proposal in this batch's own reach,
	// ---- aws_transfer_ssh_key, is rejected on independent verification
	// ---- (see internal/live/identity/table.go for the evidence). Cohort
	// ---- estate: live/e2e/estates/stragglers.
	"aws_transfer_certificate":                          {},
	"aws_transfer_profile":                              {},
	"aws_transfer_web_app":                              {},
	"aws_transfer_web_app_customization":                {},
	"aws_transfer_agreement":                            {},
	"aws_networkmanager_core_network_policy_attachment": {},
	"aws_storagegateway_tape_pool":                      {},
	"aws_ecr_lifecycle_policy":                          {},
	"aws_ecr_pull_through_cache_rule":                   {},
	"aws_ecr_pull_time_update_exclusion":                {},
	"aws_ecr_repository_creation_template":              {},
	"aws_ecr_repository_policy":                         {},
}

func init() { registerCohortAdmitted(admittedTypesStragglers) }
