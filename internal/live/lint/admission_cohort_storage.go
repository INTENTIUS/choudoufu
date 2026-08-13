// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesStorage is the storage cohort's slice of [admittedTypesV0]:
// the types the storage ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesStorage = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): fourth batch, storage (EFS, FSx,
	// ---- Backup, issue #65). Same tools/row-gen pipeline as the three
	// ---- batches above, cross-checked against live/import-grammar.json
	// ---- (the AWS provider's own documented Import sections, fetched at
	// ---- the pinned v6.58.0 tag) and, where that was ambiguous, against
	// ---- the provider's real GetProviderSchema response and its rendered
	// ---- website docs — see internal/live/identity/table.go for the
	// ---- per-type evidence, the three row-gen proposals this batch
	// ---- rejected, and live/e2e/estates/storage/README.md for the floci
	// ---- coverage this batch found (EFS and FSx are unserved by the
	// ---- pinned image). Cohort estate: live/e2e/estates/storage.
	//
	// aws_efs_file_system is issue #47's own worked example: the provider
	// ships it no native list resource in v6.58.0 at all (confirmed
	// against the real GetProviderSchema response's list_resource_schemas,
	// zero EFS/FSx/Backup entries among 183), so its enumeration now comes
	// from the Cloud Control fallback rather than the marker path's usual
	// native list — live/mapping.json maps it to AWS::EFS::FileSystem via
	// "name", and live/registry.json records that CFN type as listable
	// with no required input, so internal/live/registry.Roster's
	// EnumerationSource resolves it and internal/live/discovery/
	// cloudcontrol.go's scanTypeCloudControl is reached whenever a caller
	// supplies Request.CloudControl and Request.Roster. Today's production
	// path does not: internal/command/live_plan.go builds discovery.Request
	// with neither field set, so this fallback is proven at the discovery
	// package's own test tier (cloudcontrol_test.go's fake-server suite,
	// keyed on this exact type) but not yet reachable from a real run —
	// wiring the command layer to it is follow-on work, not this batch's.
	"aws_efs_access_point": {},
	"aws_efs_file_system":  {},

	// The FSx family: four Terraform resource types (lustre, ontap,
	// windows, openzfs) all map to CloudFormation's one generic
	// AWS::FSx::FileSystem via live/mapping.json's "alias" rows, and none
	// of the four has a name argument at all — every one imports by the
	// server-assigned id alone. AWS::FSx::FileSystem is not listable in
	// live/registry.json (handlers.list is false), so unlike
	// aws_efs_file_system above, no enumeration mechanism reaches these
	// four today, Cloud Control included — see
	// live/e2e/estates/storage/README.md.
	"aws_fsx_lustre_file_system":  {},
	"aws_fsx_ontap_file_system":   {},
	"aws_fsx_windows_file_system": {},
	"aws_fsx_openzfs_file_system": {},
	// The child types one level under a file system: a storage virtual
	// machine (ONTAP only), a volume (ONTAP and OpenZFS share
	// AWS::FSx::Volume, same fold as the file systems above), a snapshot
	// (OpenZFS only), and a data repository association (Lustre only).
	// All four import by a server-assigned id none of their create-only
	// arguments reconstructs.
	"aws_fsx_ontap_storage_virtual_machine": {},
	"aws_fsx_ontap_volume":                  {},
	"aws_fsx_openzfs_volume":                {},
	"aws_fsx_openzfs_snapshot":              {},
	"aws_fsx_data_repository_association":   {},
	// The one client-named FSx type this batch found: an S3 access point
	// attachment imports by its own name argument, verbatim.
	"aws_fsx_s3_access_point_attachment": {},

	// Backup. aws_backup_plan is genuinely server-assigned — its id is a
	// system-generated plan identifier distinct from the display name, per
	// the provider's own Attribute Reference ("The id of the backup
	// plan."). The other five ratified here are all client-named via
	// "name", three of them (framework, report plan, logically air-gapped
	// vault) corrections of a row-gen proposal that read the CloudFormation
	// registry's opaque, read-only Arn/Id field as the identity when the
	// provider's own documented Import section says plainly that its id
	// "corresponds to the name" — see table.go for the per-type
	// cross-check and the two rejections (aws_backup_selection,
	// aws_backup_restore_testing_selection) this batch declined to ratify.
	"aws_backup_plan":                       {},
	"aws_backup_vault":                      {},
	"aws_backup_framework":                  {},
	"aws_backup_report_plan":                {},
	"aws_backup_restore_testing_plan":       {},
	"aws_backup_logically_air_gapped_vault": {},
}

func init() { registerCohortAdmitted(admittedTypesStorage) }
