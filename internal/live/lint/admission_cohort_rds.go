// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesRds is the rds cohort's slice of [admittedTypesV0]:
// the types the rds ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesRds = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): fourth batch, RDS (issue #65's
	// ---- ratification campaign). Same tools/row-gen pipeline as the
	// ---- earlier batches (18 row-gen proposals in the RDS service section;
	// ---- 17 ratified, 1 rejected — see internal/live/identity/table.go for
	// ---- the per-type evidence, including five corrections where row-gen's
	// ---- own classification undersold a real, documented import grammar
	// ---- the same way the messaging batch's aws_sns_topic_policy correction
	// ---- did). aws_db_instance keeps SURVEY.md's own recorded wrinkle: the
	// ---- survey filed it under marker (taggable, no identity schema in
	// ---- v6.58.0), but its documented import ID is the client-chosen
	// ---- "identifier" argument, so it wires client-named here rather than
	// ---- through a marker, per live/SURVEY.md's own note that "a wiring
	// ---- batch that reaches RDS should expect to admit it by name." Cohort
	// ---- estate: live/e2e/estates/rds.
	"aws_db_event_subscription":         {},
	"aws_db_instance":                   {},
	"aws_db_instance_role_association":  {},
	"aws_db_option_group":               {},
	"aws_db_parameter_group":            {},
	"aws_db_proxy":                      {},
	"aws_db_proxy_default_target_group": {},
	"aws_db_proxy_endpoint":             {},
	"aws_db_subnet_group":               {},
	"aws_rds_cluster":                   {},
	"aws_rds_cluster_instance":          {},
	"aws_rds_cluster_parameter_group":   {},
	"aws_rds_cluster_role_association":  {},
	"aws_rds_custom_db_engine_version":  {},
	"aws_rds_global_cluster":            {},
	"aws_rds_integration":               {},
	"aws_rds_shard_group":               {},
}

func init() { registerCohortAdmitted(admittedTypesRds) }
