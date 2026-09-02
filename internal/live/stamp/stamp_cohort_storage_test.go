// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The storage cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableStorage = []string{
	// Registry-ratified storage batch (#40, #44, issue #65): EFS, FSx,
	// Backup. See live/e2e/estates/storage/README.md.
	"aws_efs_access_point",
	"aws_efs_file_system",
	"aws_fsx_lustre_file_system",
	"aws_fsx_ontap_file_system",
	"aws_fsx_windows_file_system",
	"aws_fsx_openzfs_file_system",
	"aws_fsx_ontap_storage_virtual_machine",
	"aws_fsx_ontap_volume",
	"aws_fsx_openzfs_volume",
	"aws_fsx_openzfs_snapshot",
	"aws_fsx_data_repository_association",
	"aws_backup_plan",
	"aws_backup_vault",
	"aws_backup_framework",
	"aws_backup_report_plan",
	"aws_backup_restore_testing_plan",
	"aws_backup_logically_air_gapped_vault",
}

var untaggableStorage = []string{
	// Registry-ratified storage batch (#40, #44, issue #65): the one
	// untaggable type this batch ratified — a client-named FSx
	// attachment with no tags argument at all. See
	// live/e2e/estates/storage/README.md, "Untaggable types".
	"aws_fsx_s3_access_point_attachment",
}

func init() {
	registerCohortStamp(taggableStorage, untaggableStorage, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified storage batch (#40, #44, issue #65): EFS, FSx,
			// Backup. Taggable/untaggable per the real provider's documented
			// Argument Reference for each type; aws_fsx_s3_access_point_attachment
			// is the batch's one untaggable type — its Argument Reference names
			// no tags block at all.
			"aws_efs_file_system":                   taggedSchema("id", "arn", "creation_token"),
			"aws_efs_access_point":                  taggedSchema("id", "arn", "file_system_id"),
			"aws_fsx_lustre_file_system":            taggedSchema("id", "arn", "subnet_ids"),
			"aws_fsx_ontap_file_system":             taggedSchema("id", "arn", "subnet_ids", "deployment_type", "preferred_subnet_id", "storage_capacity"),
			"aws_fsx_windows_file_system":           taggedSchema("id", "arn", "subnet_ids", "throughput_capacity"),
			"aws_fsx_openzfs_file_system":           taggedSchema("id", "arn", "subnet_ids", "deployment_type", "throughput_capacity"),
			"aws_fsx_ontap_storage_virtual_machine": taggedSchema("id", "arn", "file_system_id", "name"),
			"aws_fsx_ontap_volume":                  taggedSchema("id", "arn", "name", "storage_virtual_machine_id"),
			"aws_fsx_openzfs_volume":                taggedSchema("id", "arn", "name", "parent_volume_id"),
			"aws_fsx_openzfs_snapshot":              taggedSchema("id", "arn", "name", "volume_id"),
			"aws_fsx_data_repository_association":   taggedSchema("id", "arn", "association_id", "file_system_id", "file_system_path", "data_repository_path"),
			"aws_fsx_s3_access_point_attachment":    untaggedSchema("name", "type", "s3_access_point_arn", "s3_access_point_alias"),
			"aws_backup_plan":                       taggedSchema("id", "arn", "name", "version"),
			"aws_backup_vault":                      taggedSchema("id", "arn", "name"),
			"aws_backup_framework":                  taggedSchema("id", "arn", "name"),
			"aws_backup_report_plan":                taggedSchema("id", "arn", "name"),
			"aws_backup_restore_testing_plan":       taggedSchema("arn", "name", "schedule_expression"),
			"aws_backup_logically_air_gapped_vault": taggedSchema("id", "arn", "name", "min_retention_days", "max_retention_days"),
		})
	})
}
