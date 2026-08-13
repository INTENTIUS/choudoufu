// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesDataMovement is the data-movement cohort's slice of [admittedTypesV0]:
// the types the data-movement ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesDataMovement = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): sixth batch, data movement and
	// ---- transfer (Transfer Family's server/user/workflow/connector core,
	// ---- DataSync's agents/locations/tasks in full, DMS including its
	// ---- three registry-laggard replication types, AppIntegrations). Same
	// ---- tools/row-gen pipeline as the batches above, cross-checked
	// ---- against the AWS provider's documented Argument/Attribute/Import
	// ---- sections fetched from the pinned v6.58.0 tag directly. Transfer
	// ---- Server's row-gen proposal (ARN) does not survive that check — the
	// ---- documented import and "id" attribute are both the ServerID — and
	// ---- Transfer User's registry-says-ARN evidence is corrected to its
	// ---- real composite (server_id/user_name). DMS's Certificate and the
	// ---- two Endpoint types (including the S3 endpoint alias the sweep
	// ---- recorded against the same CFN type) correct the same way, to
	// ---- their documented client-named identifiers. DMS's replication
	// ---- instance, subnet group and task are registry-laggard (the CFN
	// ---- Registry ships every handler false for all three), but their
	// ---- provider-documented import commands are clean, unambiguous
	// ---- client-named identifiers untouched by that gap, the same
	// ---- registry-disagrees-but-the-provider-is-clear shape the devtools
	// ---- batch's CodeBuild/CodeCommit corrections established. Two of
	// ---- DataSync's FSx-backed locations (ONTAP, OpenZFS) correct
	// ---- row-gen's plain-ARN ImportSyntax to the provider's documented
	// ---- compound "DataSync-ARN#FSx-ARN" grammar; the identity itself
	// ---- stays server-assigned. Storage Gateway is registry-absent beyond
	// ---- a single TapePool type and is skipped entirely, and MGN/DRS have
	// ---- no CFN Registry footprint at all — row-gen proposes nothing for
	// ---- either. See internal/live/identity/table.go for the per-type
	// ---- evidence and the six Transfer Family types (certificate,
	// ---- profile, web_app, web_app_customization, agreement, ssh_key) left
	// ---- outside this batch's named scope. Cohort estate:
	// ---- live/e2e/estates/data-movement.
	"aws_transfer_server":                           {},
	"aws_transfer_user":                             {},
	"aws_transfer_workflow":                         {},
	"aws_transfer_connector":                        {},
	"aws_datasync_agent":                            {},
	"aws_datasync_task":                             {},
	"aws_datasync_location_s3":                      {},
	"aws_datasync_location_efs":                     {},
	"aws_datasync_location_nfs":                     {},
	"aws_datasync_location_smb":                     {},
	"aws_datasync_location_hdfs":                    {},
	"aws_datasync_location_object_storage":          {},
	"aws_datasync_location_azure_blob":              {},
	"aws_datasync_location_fsx_lustre_file_system":  {},
	"aws_datasync_location_fsx_ontap_file_system":   {},
	"aws_datasync_location_fsx_openzfs_file_system": {},
	"aws_datasync_location_fsx_windows_file_system": {},
	"aws_dms_certificate":                           {},
	"aws_dms_endpoint":                              {},
	"aws_dms_s3_endpoint":                           {},
	"aws_dms_event_subscription":                    {},
	"aws_dms_replication_config":                    {},
	"aws_dms_replication_instance":                  {},
	"aws_dms_replication_subnet_group":              {},
	"aws_dms_replication_task":                      {},
	"aws_appintegrations_data_integration":          {},
	"aws_appintegrations_event_integration":         {},
}

func init() { registerCohortAdmitted(admittedTypesDataMovement) }
