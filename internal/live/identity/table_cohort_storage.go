// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableStorage is the storage cohort's slice of [DefaultTable]:
// the identity rows the storage ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableStorage = buildTable(
	// ---- Registry-ratified (#40, #44): fourth batch, storage (EFS, FSx,
	// ---- Backup, issue #65) ----------------------------------------------
	//
	// Same tools/row-gen pipeline as the three batches above: every row
	// started as a proposal from live/registry.json, and every row that
	// landed here was independently cross-checked, not accepted on the
	// registry's classification alone. The cross-check for this batch
	// mostly used live/import-grammar.json — tools/importdocs-gen's parse
	// of the AWS provider's own website/docs/r/ Import sections at the
	// pinned v6.58.0 tag — rather than the provider's identity schema,
	// because live/survey-full.json records that none of EFS, FSx or
	// Backup ships an identity schema in this provider release at all
	// ("identity_schema": false for every type in scope); three rows below
	// also needed the provider's rendered Attribute Reference fetched
	// directly, because the grammar excerpt alone did not say what the
	// import-grammar's own "id" resolved to. Cohort estate:
	// live/e2e/estates/storage.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_backup_selection: row-gen proposed server-assigned via the
	//     registry's opaque, undocumented "Id". The provider's real,
	//     documented import command (terraform import
	//     aws_backup_selection.example plan-id|selection-id) is a two-part
	//     composite, and unlike aws_iam_role_policy's ROLENAME:POLICYNAME
	//     or aws_route53_record's zone/name/type, the second half — the
	//     selection's own Backup Selection identifier — is exactly as
	//     server-assigned as the registry said, with no argument in
	//     configuration that reconstructs it (plan_id is a live reference,
	//     but the selection id is minted by AWS Backup and appears nowhere
	//     until after create). The type also carries no tags argument, so
	//     the marker path cannot substitute either. This is not a
	//     CFN-says-server-assigned-provider-disagrees case like the three
	//     corrections below; here the registry and the provider agree, and
	//     the answer is still "no admission path recovers it" — exactly
	//     what live/survey-full.json's own mechanical classifier says for
	//     this type ("moves to Ops").
	//   - aws_backup_restore_testing_selection: row-gen marks this
	//     needs-hand-separator (a composite RestoreTestingPlanName +
	//     RestoreTestingSelectionName primary identifier, per its own
	//     non-goals). Unusually for that class, live/import-grammar.json
	//     does name the separator this time — "name:restore_testing_plan_name"
	//     — and the provider's schema confirms both halves are required,
	//     client-supplied arguments (name, restore_testing_plan_name), not
	//     a server-assigned value like the selection above. That makes it
	//     a strong candidate for a future batch, but this batch keeps the
	//     standing discipline the Lambda, IAM/ECR and messaging batches
	//     already established: never hand-write a composite row-gen itself
	//     declined to propose. Left for a batch prepared to extend the
	//     table's own component vocabulary test coverage around it.
	//   - aws_efs_mount_target: row-gen proposed server-assigned via the
	//     registry's "Id", and the identity classification is not in
	//     question — the provider's documented import command (terraform
	//     import aws_efs_mount_target.alpha fsmt-52a643fb) confirms a
	//     server-assigned MountTargetId with nothing in configuration
	//     (ip_address, subnet_id, file_system_id) that reconstructs it.
	//     What sinks it is that no admission path recovers that id from a
	//     stateless run at all: the type carries no tags argument (the
	//     provider's docs list no tags block, confirmed against
	//     live/registry.json's AWS::EFS::MountTarget tagging.taggable:
	//     false), so the marker path has nothing to search on, and
	//     live/registry.json's handlers.list_required_input for the same
	//     CFN type is ["FileSystemId"] rather than empty, so
	//     internal/live/registry.Roster.Listable reports false and the
	//     Cloud Control fallback (#47, the mechanism aws_efs_file_system
	//     below relies on) does not apply either — scanTypeCloudControl
	//     only ever calls ListResources with no input. Same verdict as
	//     aws_backup_selection above, and the same one
	//     live/survey-full.json's classifier reaches independently
	//     ("moves to Ops").
	//
	// Corrected, not merely accepted — three rows where row-gen's registry
	// heuristic (primaryIdentifier ⊆ readOnlyProperties ⇒ server-assigned)
	// pointed at a CloudFormation-only field the provider does not use for
	// import, the same failure shape as the Lambda batch's aws_lambda_alias
	// and the IAM batch's aws_iam_policy:
	//
	//   - aws_backup_framework: row-gen proposed server-assigned via the
	//     registry's FrameworkArn. The provider's documented Import section
	//     says otherwise, in so many words: "import Backup Framework using
	//     the `id` which corresponds to the name of the Backup Framework."
	//     `name` is a required argument already in configuration
	//     (live/survey-full.json's schema pull confirms it; the registry's
	//     own create_only_properties row even names FrameworkName). Wired
	//     below as client-named via name, not server-assigned via ARN.
	//   - aws_backup_report_plan: the same shape and the same sentence,
	//     verbatim, in the provider's docs ("...using the `id` which
	//     corresponds to the name of the Backup Report Plan."), against
	//     row-gen's registry-driven ReportPlanArn proposal. Wired
	//     client-named via name.
	//   - aws_backup_logically_air_gapped_vault: row-gen would not propose
	//     this one at all — its own argument-name guess (backup_vault_name)
	//     was unbacked by any schema, so it surfaced evidence-only, no
	//     pastable row, per #44's non-goals. The provider's rendered docs
	//     (website/docs/r/backup_logically_air_gapped_vault.html.markdown)
	//     resolve it cleanly: "The `id` ... The name of the Logically Air
	//     Gapped Backup Vault," and `name` is a required argument. Wired
	//     client-named via name, the same shape as aws_backup_vault.
	//
	// aws_efs_file_system is issue #47's own worked example — see
	// internal/live/lint/admission.go's comment above this batch's
	// admittedTypesV0 rows for the Cloud Control enumeration-source note,
	// which belongs to the discovery package rather than to this table.

	serverAssigned("aws_efs_file_system",
		"EFS assigns the file system ID (fs-…) at create time; AvailabilityZoneName, Encrypted, KmsKeyId and PerformanceMode describe it but do not name it.",
		"FILESYSTEMID", "id"),
	serverAssigned("aws_efs_access_point",
		"EFS assigns the access point ID (fsap-…) at create time; FileSystemId names the parent, not the access point itself.",
		"ACCESSPOINTID", "id"),

	// The FSx family: four TF resource types folded onto CloudFormation's
	// one generic AWS::FSx::FileSystem (live/mapping.json's "alias" rows),
	// none with a name argument at all — confirmed against the provider's
	// own schema pull (no `name` attribute on any of the four) as well as
	// against live/import-grammar.json, which documents all four importing
	// "using the `id`" with no argument composition.
	serverAssigned("aws_fsx_lustre_file_system",
		"FSx assigns the file system ID (fs-…) at create time; the type has no name argument, and KmsKeyId, SecurityGroupIds, SubnetIds and BackupId describe it but do not name it.",
		"ID", "id"),
	serverAssigned("aws_fsx_ontap_file_system",
		"FSx assigns the file system ID (fs-…) at create time; the type has no name argument, and KmsKeyId, SecurityGroupIds, SubnetIds and BackupId describe it but do not name it.",
		"ID", "id"),
	serverAssigned("aws_fsx_windows_file_system",
		"FSx assigns the file system ID (fs-…) at create time; the type has no name argument, and KmsKeyId, SecurityGroupIds, SubnetIds and BackupId describe it but do not name it.",
		"ID", "id"),
	serverAssigned("aws_fsx_openzfs_file_system",
		"FSx assigns the file system ID (fs-…) at create time; the type has no name argument, and KmsKeyId, SecurityGroupIds, SubnetIds and BackupId describe it but do not name it.",
		"ID", "id"),
	serverAssigned("aws_fsx_ontap_storage_virtual_machine",
		"FSx assigns the storage virtual machine ID (svm-…) at create time; the name argument names the SVM but the provider's documented import identity is the ID, not the name.",
		"ID", "id"),
	serverAssigned("aws_fsx_ontap_volume",
		"FSx assigns the volume ID (fsvol-…) at create time; the name argument names the volume but the provider's documented import identity is the ID, not the name.",
		"VOLUMEID", "id"),
	serverAssigned("aws_fsx_openzfs_volume",
		"FSx assigns the volume ID (fsvol-…) at create time; the name argument names the volume but the provider's documented import identity is the ID, not the name.",
		"VOLUMEID", "id"),
	serverAssigned("aws_fsx_openzfs_snapshot",
		"FSx assigns the snapshot ID (fsvolsnap-…) at create time, distinct from the name argument; the provider's own Attribute Reference documents id and name as two separate values.",
		"ID", "id"),
	serverAssigned("aws_fsx_data_repository_association",
		"FSx assigns the data repository association ID (dra-…) at create time; FileSystemId, FileSystemPath and DataRepositoryPath describe it but do not name it.",
		"ASSOCIATIONID", "id"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, proposed correctly.
		// Confirmed against the provider's documented import command
		// (terraform import aws_fsx_s3_access_point_attachment.example
		// example-attachment) and against the provider's own schema pull,
		// which has no id attribute for this type at all — the same
		// standard of care aws_route's synthesized id gets.
		Type:          "aws_fsx_s3_access_point_attachment",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	serverAssigned("aws_backup_plan",
		"Backup mints the plan's own id at create time, a UUID distinct from the client-chosen name; the provider's Attribute Reference documents id as \"the id of the backup plan,\" separate from name and from version.",
		"BACKUPPLANID", "id"),

	TypeIdentity{
		// Confirmed against the provider's documented import command
		// (terraform import aws_backup_vault.test-vault TestVault) and its
		// Attribute Reference, which states id is "the name of the vault."
		Type:          "aws_backup_vault",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// Corrected from row-gen's server-assigned FrameworkArn proposal —
		// see the rejected/corrected note above. Confirmed against the
		// provider's documented import command (terraform import
		// aws_backup_framework.test <id>, where the doc states id
		// "corresponds to the name of the Backup Framework") and its
		// Argument Reference, which requires name.
		Type:          "aws_backup_framework",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// Corrected from row-gen's server-assigned ReportPlanArn proposal —
		// the same shape and the same corroborating sentence in the
		// provider's docs as aws_backup_framework above.
		Type:          "aws_backup_report_plan",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json proposed this one correctly (client-named via
		// name). Confirmed against the provider's documented import
		// command (terraform import aws_backup_restore_testing_plan.example
		// my_testing_plan); the provider's own schema pull has no id
		// attribute for this type at all, so none is claimed here.
		Type:          "aws_backup_restore_testing_plan",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen proposed nothing pastable here at all — its own
		// argument-name guess (backup_vault_name) was unbacked by any
		// schema, so this surfaced evidence-only per #44's non-goals. The
		// provider's rendered docs resolve it: "the `id` ... The name of
		// the Logically Air Gapped Backup Vault," with name required. See
		// the corrected note above.
		Type:          "aws_backup_logically_air_gapped_vault",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
)

func init() { registerCohortTable(identityTableStorage) }
