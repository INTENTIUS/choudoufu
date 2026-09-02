// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The data-movement cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableDataMovement = []string{
	// Registry-ratified data-movement batch (#40, #44, issue #65): all
	// twenty-seven types this batch ratified carry a top-level tags
	// argument in the pinned provider's own wire schema, confirmed
	// against the provider's documented Argument Reference for each —
	// this batch has no untaggable rows at all. See
	// live/e2e/estates/data-movement/README.md.
	"aws_transfer_server",
	"aws_transfer_user",
	"aws_transfer_workflow",
	"aws_transfer_connector",
	"aws_datasync_agent",
	"aws_datasync_task",
	"aws_datasync_location_s3",
	"aws_datasync_location_efs",
	"aws_datasync_location_nfs",
	"aws_datasync_location_smb",
	"aws_datasync_location_hdfs",
	"aws_datasync_location_object_storage",
	"aws_datasync_location_azure_blob",
	"aws_datasync_location_fsx_lustre_file_system",
	"aws_datasync_location_fsx_ontap_file_system",
	"aws_datasync_location_fsx_openzfs_file_system",
	"aws_datasync_location_fsx_windows_file_system",
	"aws_dms_certificate",
	"aws_dms_endpoint",
	"aws_dms_s3_endpoint",
	"aws_dms_event_subscription",
	"aws_dms_replication_config",
	"aws_dms_replication_instance",
	"aws_dms_replication_subnet_group",
	"aws_dms_replication_task",
	"aws_appintegrations_data_integration",
	"aws_appintegrations_event_integration",
}

var untaggableDataMovement = []string{}

func init() {
	registerCohortStamp(taggableDataMovement, untaggableDataMovement, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified data-movement batch (#40, #44, issue #65). All
			// twenty-seven types are taggable per the real provider's
			// documented Argument Reference for each.
			"aws_transfer_server":                           taggedSchema("id", "arn"),
			"aws_transfer_user":                             taggedSchema("id", "arn", "server_id", "user_name"),
			"aws_transfer_workflow":                         taggedSchema("id", "arn"),
			"aws_transfer_connector":                        taggedSchema("id", "arn", "connector_id"),
			"aws_datasync_agent":                            taggedSchema("id", "arn"),
			"aws_datasync_task":                             taggedSchema("id", "arn"),
			"aws_datasync_location_s3":                      taggedSchema("id", "arn"),
			"aws_datasync_location_efs":                     taggedSchema("id", "arn"),
			"aws_datasync_location_nfs":                     taggedSchema("id", "arn"),
			"aws_datasync_location_smb":                     taggedSchema("id", "arn"),
			"aws_datasync_location_hdfs":                    taggedSchema("id", "arn"),
			"aws_datasync_location_object_storage":          taggedSchema("id", "arn"),
			"aws_datasync_location_azure_blob":              taggedSchema("id", "arn"),
			"aws_datasync_location_fsx_lustre_file_system":  taggedSchema("id", "arn"),
			"aws_datasync_location_fsx_ontap_file_system":   taggedSchema("id", "arn"),
			"aws_datasync_location_fsx_openzfs_file_system": taggedSchema("id", "arn"),
			"aws_datasync_location_fsx_windows_file_system": taggedSchema("id", "arn"),
			"aws_dms_certificate":                           taggedSchema("id", "certificate_arn", "certificate_id"),
			"aws_dms_endpoint":                              taggedSchema("id", "endpoint_arn", "endpoint_id"),
			"aws_dms_s3_endpoint":                           taggedSchema("id", "endpoint_arn", "endpoint_id"),
			"aws_dms_event_subscription":                    taggedSchema("id", "arn", "name"),
			"aws_dms_replication_config":                    taggedSchema("id", "arn"),
			"aws_dms_replication_instance":                  taggedSchema("id", "replication_instance_arn", "replication_instance_id"),
			"aws_dms_replication_subnet_group":              taggedSchema("id", "replication_subnet_group_id"),
			"aws_dms_replication_task":                      taggedSchema("id", "replication_task_arn", "replication_task_id"),
			"aws_appintegrations_data_integration":          taggedSchema("id", "arn"),
			"aws_appintegrations_event_integration":         taggedSchema("id", "arn", "name"),
		})
	})
}
