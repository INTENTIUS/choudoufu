// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableDataMovement is the data-movement cohort's slice of [DefaultTable]:
// the identity rows the data-movement ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableDataMovement = buildTable(
	// ---- Registry-ratified (#40, #44, #65): sixth batch, data movement and
	// ---- transfer (issue #65) ----------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal, cross-checked against the AWS provider's
	// documented Argument Reference, Attribute Reference and Import section
	// (fetched from the provider's own website/docs/r/ source at the pinned
	// v6.58.0 tag), not accepted on the registry's classification alone.
	// Cohort estate: live/e2e/estates/data-movement.
	//
	// Transfer Family: row-gen's registry evidence gets two of this batch's
	// four types wrong. aws_transfer_server's registry primaryIdentifier is
	// the opaque "Arn", but the provider's own Attribute Reference documents
	// "id" as the Server ID ("s-12345678"), a distinct, shorter value the
	// documented import command uses directly — the
	// registry-says-ARN-but-the-provider-disagrees shape aws_transfer_user
	// repeats one level down: its registry primaryIdentifier is also "Arn",
	// but the provider's documented import command
	// (terraform import aws_transfer_user.bar s-12345678/test-username)
	// joins its two required arguments, server_id and user_name, with a
	// slash — row-gen flagged this one itself ("needs hand separator" /
	// "no pastable row"), and the separator is confirmed directly against
	// the docs rather than chosen blind. aws_transfer_workflow and
	// aws_transfer_connector both confirm row-gen's registry-derived
	// primaryIdentifier (WorkflowId, ConnectorId) against the provider's own
	// documented import command and Attribute Reference without correction.
	// Six further Transfer Family types are outside this batch's named scope
	// (issue #65 names servers, users, workflows and connectors only) and
	// are left for a future batch: aws_transfer_certificate,
	// aws_transfer_profile, aws_transfer_web_app,
	// aws_transfer_web_app_customization (a property-child of web_app),
	// aws_transfer_agreement (row-gen's own "needs hand separator" — a
	// composite of ServerId and a registry-opaque AgreementId, unlike the
	// user's clean server_id/user_name pair) and aws_transfer_ssh_key (a
	// property-child of user, admittable via the parent-derived path now
	// that aws_transfer_user is ratified, but not claimed here since it is
	// outside the named scope). aws_transfer_tag is not a row-gen proposal
	// at all: live/mapping.json's own sweep evidence records it as a
	// generic tag escape-hatch with no CFN resource of its own (via
	// "tf-only"), the same shape as issue #53's other tf-only types.
	//
	// DataSync: all thirteen of row-gen's proposals — the agent, all eleven
	// location types, and the task — are server-assigned, and row-gen's
	// registry-derived ImportSyntax placeholder is confirmed as the real
	// documented import grammar (a plain ARN) for nine of them. Two of the
	// FSx-backed locations, ONTAP and OpenZFS, correct that placeholder: the
	// provider's documented import command joins the DataSync location's own
	// ARN and the FSx filesystem's ARN with "#" (DataSync-ARN#FSx-ARN), the
	// same compound-ARN grammar the other two FSx-backed locations (Lustre,
	// Windows) also document — row-gen's flat "LOCATIONARN" placeholder
	// undersold all four the same way it undersold aws_sns_topic and
	// aws_cloudfront_realtime_log_config in earlier batches, just with a
	// second ARN concatenated on rather than a literal prefix. The location
	// type's own "arn"/"id" attributes still name the location alone
	// (confirmed per-type below), which is what a referencing resource such
	// as aws_datasync_task's source_location_arn/destination_location_arn
	// consumes — the compound string is an import-command peculiarity, not a
	// second identity. Five of the eleven location types' provider docs
	// (SMB, HDFS, Object Storage, Azure Blob, and — despite its compound
	// import grammar — ONTAP) do not carry an explicit "id" line in their
	// Attribute Reference, unlike the other six; IdentityAttrs below claims
	// only "arn" for those five, the same documentation-gap standard of care
	// aws_codebuild_fleet's rejection of "id" got in the devtools batch.
	//
	// DMS: three types correct row-gen's evidence-only "no pastable row"
	// entries once the provider's own documented Import section is read
	// directly. aws_dms_certificate and aws_dms_endpoint (with its
	// aws_dms_s3_endpoint alias, which live/mapping.json's own sweep
	// evidence already records against the same CFN type,
	// AWS::DMS::Endpoint) all carry a registry primaryIdentifier of an
	// opaque Arn, but every one of their provider-documented import commands
	// uses a plain, required, client-chosen identifier argument instead
	// (certificate_id, endpoint_id) — row-gen's own note on each
	// ("import docs show argument-composed ID: test-dms-certificate-tf" and
	// the like) is exactly this gap, flagged but not resolved, because
	// resolving it means reading the docs rather than the registry.
	// aws_dms_event_subscription's row-gen proposal (client-named via
	// "name") is confirmed as-is. aws_dms_replication_config's row-gen
	// proposal (server-assigned via the registry's opaque
	// ReplicationConfigArn) is also confirmed as-is — its documented import
	// command uses the full ARN, whose suffix is an opaque
	// service-generated token, not the resource's own
	// replication_config_identifier argument, the same account-derived-ARN
	// shape as aws_dms_replication_config's sibling entries above; unlike
	// most ARN-shaped identities in this table, the provider's Attribute
	// Reference for this one does not separately document "id", so
	// IdentityAttrs claims only "arn".
	//
	// Three more DMS types are the registry-laggard cohort issue #65 names
	// explicitly: AWS::DMS::ReplicationInstance, ::ReplicationSubnetGroup
	// and ::ReplicationTask all ship every CFN Registry handler false
	// (confirmed against live/registry.json directly), which is why row-gen
	// enumerates each as "not listable -> client-named only" and pastes
	// nothing. But the CFN Registry's laggardness is a CloudFormation-side
	// gap, not evidence about the underlying DMS API or the AWS provider,
	// which documents a clean, unambiguous, client-named import command for
	// all three (replication_instance_id, replication_subnet_group_id,
	// replication_task_id — each a required, provider-validated argument),
	// the same registry-disagrees-but-the-provider-is-clear shape the
	// devtools batch's CodeBuild::Project and CodeCommit::Repository
	// corrections established for a registry-handler-less CFN type. All
	// three are admitted on that provider-native evidence alone.
	//
	// AppIntegrations: both of row-gen's proposals are confirmed as-is.
	// aws_appintegrations_data_integration is server-assigned via the
	// registry's opaque "Id", confirmed against the provider's own
	// Attribute Reference ("id - Identifier of the Data Integration") and
	// its documented import command, which uses a service-generated UUID.
	// aws_appintegrations_event_integration is client-named via "name",
	// confirmed against the provider's own Attribute Reference ("id -
	// Identifier of the Event Integration which is the name of the Event
	// Integration") and its documented import command.
	//
	// Left out of this batch entirely: Storage Gateway is registry-absent
	// beyond a single CFN type, AWS::StorageGateway::TapePool — every other
	// real, actively-used Storage Gateway resource (Cache, Gateway, Volume,
	// FileShare) has no CFN Registry entry at all
	// (live/mapping.json's own sweep evidence records the gap type by
	// type), and issue #65's recipe calls the service cfn-unmodeled and
	// skips it entirely rather than admitting the one sliver the registry
	// happens to cover. row-gen does propose aws_storagegateway_tape_pool
	// on that one sliver; it is deliberately left unratified here. MGN and
	// DRS have no CFN Registry footprint of any kind — row-gen proposes
	// nothing for either service, and there is nothing here to ratify or
	// reject.

	// row-gen proposed server-assigned via the registry's opaque "Arn" —
	// the registry-says-ARN-but-the-provider-disagrees shape. The
	// provider's own Attribute Reference documents "id" as the Server ID
	// ("s-12345678"), distinct from "arn", and its documented import
	// command (terraform import aws_transfer_server.example s-12345678)
	// uses that shorter value directly, not the ARN.
	serverAssigned("aws_transfer_server",
		"the Transfer service assigns the server ID at create time; no argument reconstructs it.",
		"SERVERID", "id"),
	TypeIdentity{
		// row-gen flagged this one itself ("needs hand separator" for the
		// sibling aws_transfer_agreement type; this type's own registry
		// evidence read "no pastable row" with a note pointing at the
		// documented import example). The provider's Argument Reference
		// makes both halves required arguments (server_id, user_name), and
		// the documented import command
		// (terraform import aws_transfer_user.bar s-12345678/test-username)
		// joins them with a slash — the registry's opaque "Arn"
		// primaryIdentifier plays no part in it. The type's own Attribute
		// Reference documents only "arn", not a separate "id" equal to the
		// composite, so no IdentityAttrs are claimed.
		Type: "aws_transfer_user",
		Components: []Component{
			attr("server_id"),
			sep("/"),
			attr("user_name"),
		},
		ImportSyntax:  "SERVER-ID/USER-NAME",
		IdentityAttrs: nil,
	},
	// row-gen's registry-derived proposal (server-assigned via the opaque
	// WorkflowId) is confirmed as-is: the provider's own Attribute
	// Reference documents "id" as "The Workflow id", and no argument
	// reconstructs it (Steps/OnExceptionSteps/Description are the type's
	// only create-time arguments, none of them a name).
	serverAssigned("aws_transfer_workflow",
		"the Transfer service assigns this identity at create time; no argument reconstructs it.",
		"WORKFLOWID", "id"),
	// row-gen's registry-derived proposal (server-assigned via the opaque
	// ConnectorId) is confirmed as-is: the provider's own Attribute
	// Reference documents "connector_id" as "The unique identifier for the
	// AS2 profile or SFTP Profile", and both the import block and the
	// classic import command use it directly. The Attribute Reference does
	// not separately document "id" as equal to it, so only "connector_id"
	// is claimed.
	serverAssigned("aws_transfer_connector",
		"the Transfer service assigns this identity at create time; no argument reconstructs it.",
		"CONNECTORID", "connector_id"),
	// row-gen's registry-derived proposal (server-assigned via the opaque
	// AgentArn) is confirmed as-is: the provider's own Attribute Reference
	// documents both "id" and "arn" as the Agent's ARN, and the documented
	// import command uses the ARN directly.
	serverAssigned("aws_datasync_agent",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "id", "arn"),
	// Same confirmation as the agent above: "id" and "arn" both documented
	// as the Task's ARN, documented import command uses the ARN directly.
	serverAssigned("aws_datasync_task",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "id", "arn"),
	// Same confirmation as the agent above, applied to the S3 location:
	// "id" and "arn" both documented as the Location's ARN, documented
	// import command uses the ARN directly.
	serverAssigned("aws_datasync_location_s3",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "id", "arn"),
	// Same confirmation as aws_datasync_location_s3, applied to the EFS
	// location.
	serverAssigned("aws_datasync_location_efs",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "id", "arn"),
	// Same confirmation as aws_datasync_location_s3, applied to the NFS
	// location.
	serverAssigned("aws_datasync_location_nfs",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "id", "arn"),
	// Same plain-ARN import grammar as aws_datasync_location_s3, but this
	// type's own Attribute Reference documents only "arn", not a separate
	// "id" line — the documentation-gap standard of care
	// aws_codebuild_fleet's rejection of "id" got in the devtools batch.
	// Only "arn" is claimed.
	serverAssigned("aws_datasync_location_smb",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "arn"),
	// Same documentation-gap shape as aws_datasync_location_smb above:
	// plain-ARN import, but only "arn" is documented, not "id".
	serverAssigned("aws_datasync_location_hdfs",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "arn"),
	// Same documentation-gap shape as aws_datasync_location_smb above:
	// plain-ARN import, but only "arn" (and "uri") is documented, not
	// "id".
	serverAssigned("aws_datasync_location_object_storage",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "arn"),
	// Same documentation-gap shape as aws_datasync_location_smb above:
	// plain-ARN import, but only "arn" is documented, not "id".
	serverAssigned("aws_datasync_location_azure_blob",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "arn"),
	// row-gen's flat "LOCATIONARN" placeholder undersells this one: the
	// provider's documented import command joins the DataSync location's
	// own ARN and the FSx Lustre file system's ARN with "#" (terraform
	// import aws_datasync_location_fsx_lustre_file_system.example
	// arn:aws:datasync:...:location/loc-...#arn:aws:fsx:...:file-system/fs-...),
	// the same compound-ARN shape aws_sns_topic's account-derived
	// correction gets in the messaging batch, just concatenating a second
	// ARN instead of a literal prefix. The location's own "id" and "arn"
	// attributes are still documented as the location's ARN alone, which
	// is what a referencing resource such as aws_datasync_task's
	// source_location_arn consumes.
	serverAssigned("aws_datasync_location_fsx_lustre_file_system",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"DATASYNC-LOCATION-ARN#FSX-LUSTRE-ARN", "id", "arn"),
	// Same compound-ARN correction as the Lustre location above (terraform
	// import aws_datasync_location_fsx_ontap_file_system.example
	// arn:aws:datasync:...:location/loc-...#arn:aws:fsx:...:storage-virtual-machine/svm-...) —
	// note the second half is the FSx ONTAP storage virtual machine's ARN,
	// not the file system's. This type's own Attribute Reference documents
	// only "arn" (and "fsx_filesystem_arn", "uri"), not a separate "id"
	// line, so only "arn" is claimed.
	serverAssigned("aws_datasync_location_fsx_ontap_file_system",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"DATASYNC-LOCATION-ARN#FSX-ONTAP-SVM-ARN", "arn"),
	// Same compound-ARN correction as the Lustre location above (terraform
	// import aws_datasync_location_fsx_openzfs_file_system.example
	// arn:aws:datasync:...:location/loc-...#arn:aws:fsx:...:file-system/fs-...).
	// Unlike ONTAP, this type's Attribute Reference does document both
	// "id" and "arn" as the DataSync location's own ARN.
	serverAssigned("aws_datasync_location_fsx_openzfs_file_system",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"DATASYNC-LOCATION-ARN#FSX-OPENZFS-ARN", "id", "arn"),
	// Same compound-ARN correction as the Lustre location above (terraform
	// import aws_datasync_location_fsx_windows_file_system.example
	// arn:aws:datasync:...:location/loc-...#arn:aws:fsx:...:file-system/fs-...).
	// Both "id" and "arn" are documented as the DataSync location's own
	// ARN.
	serverAssigned("aws_datasync_location_fsx_windows_file_system",
		"the DataSync service assigns this identity at create time; no argument reconstructs it.",
		"DATASYNC-LOCATION-ARN#FSX-WINDOWS-ARN", "id", "arn"),
	TypeIdentity{
		// row-gen read this evidence-only ("no pastable row"), noting the
		// documented import example was a plain string
		// ("test-dms-certificate-tf") rather than the registry's opaque
		// "CertificateArn" primaryIdentifier — a
		// registry-says-ARN-but-the-provider-disagrees shape. The provider's
		// Argument Reference makes "certificate_id" a required argument,
		// and the documented import command
		// (terraform import aws_dms_certificate.test test-dms-certificate-tf)
		// uses it verbatim. The Attribute Reference documents only
		// "certificate_arn", not a separate "id" equal to certificate_id.
		Type:          "aws_dms_certificate",
		Components:    []Component{attr("certificate_id")},
		ImportSyntax:  "CERTIFICATE-ID",
		IdentityAttrs: []string{"certificate_id"},
	},
	TypeIdentity{
		// Same registry-says-ARN-but-the-provider-disagrees shape as
		// aws_dms_certificate above: row-gen read this evidence-only, noting
		// the documented import example was a plain string
		// ("test-dms-endpoint-tf") rather than the registry's opaque
		// "EndpointArn". The provider's Argument Reference makes
		// "endpoint_id" a required argument, and the documented import
		// command (terraform import aws_dms_endpoint.test test-dms-endpoint-tf)
		// uses it verbatim. The Attribute Reference documents only
		// "endpoint_arn", not a separate "id".
		Type:          "aws_dms_endpoint",
		Components:    []Component{attr("endpoint_id")},
		ImportSyntax:  "ENDPOINT-ID",
		IdentityAttrs: []string{"endpoint_id"},
	},
	TypeIdentity{
		// live/mapping.json's own sweep evidence records this type as an
		// alias of the same CFN type as aws_dms_endpoint above,
		// AWS::DMS::Endpoint, and the correction is identical: the
		// provider's Argument Reference makes "endpoint_id" a required
		// argument, and the documented import command
		// (terraform import aws_dms_s3_endpoint.example example-dms-endpoint-tf)
		// uses it verbatim, not the registry's opaque "EndpointArn". The
		// Attribute Reference documents "endpoint_arn", "engine_display_name",
		// "external_id" and "status", not a separate "id".
		Type:          "aws_dms_s3_endpoint",
		Components:    []Component{attr("endpoint_id")},
		ImportSyntax:  "ENDPOINT-ID",
		IdentityAttrs: []string{"endpoint_id"},
	},
	TypeIdentity{
		// row-gen's registry-derived proposal (client-named via the
		// required "name" argument, from live/import-grammar.json's scraped
		// separator evidence) is confirmed as-is against the provider's own
		// documented import command
		// (terraform import aws_dms_event_subscription.test my-awesome-event-subscription).
		Type:          "aws_dms_event_subscription",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// row-gen's registry-derived proposal (server-assigned via the opaque
	// ReplicationConfigArn) is confirmed as-is: the provider's documented
	// import command uses the full ARN (terraform import
	// aws_dms_replication_config.example
	// arn:aws:dms:us-east-1:123456789012:replication-config:UX6OL6MHMMJKFFOXE3H7LLJCMEKBDUG4ZV7DRSI),
	// whose suffix is an opaque, service-generated token — not the
	// resource's own replication_config_identifier argument — the same
	// account-derived-ARN-but-server-minted-suffix shape as
	// aws_codebuild_report_group's ARN in the devtools batch. The Attribute
	// Reference documents only "arn", not a separate "id".
	serverAssigned("aws_dms_replication_config",
		"the DMS service assigns this identity at create time; no argument reconstructs it.",
		"ARN", "arn"),
	TypeIdentity{
		// AWS::DMS::ReplicationInstance ships every CFN Registry handler
		// false (confirmed against live/registry.json directly), which is
		// why row-gen enumerates this type "not listable -> client-named
		// only" and pastes nothing — the same registry-handler-less shape
		// the devtools batch's CodeBuild::Project correction established.
		// The provider's own Argument Reference makes
		// "replication_instance_id" a required argument, and the documented
		// import command
		// (terraform import aws_dms_replication_instance.test test-dms-replication-instance-tf)
		// uses it verbatim. The Attribute Reference documents
		// "replication_instance_arn", not a separate "id".
		Type:          "aws_dms_replication_instance",
		Components:    []Component{attr("replication_instance_id")},
		ImportSyntax:  "REPLICATION-INSTANCE-ID",
		IdentityAttrs: []string{"replication_instance_id"},
	},
	TypeIdentity{
		// Same registry-handler-less shape as aws_dms_replication_instance
		// above (AWS::DMS::ReplicationSubnetGroup also ships every handler
		// false). The provider's own Argument Reference makes
		// "replication_subnet_group_id" a required argument, and the
		// documented import command
		// (terraform import aws_dms_replication_subnet_group.test test-dms-replication-subnet-group-tf)
		// uses it verbatim. The Attribute Reference documents "vpc_id",
		// not a separate "id".
		Type:          "aws_dms_replication_subnet_group",
		Components:    []Component{attr("replication_subnet_group_id")},
		ImportSyntax:  "REPLICATION-SUBNET-GROUP-ID",
		IdentityAttrs: []string{"replication_subnet_group_id"},
	},
	TypeIdentity{
		// Same registry-handler-less shape as aws_dms_replication_instance
		// above (AWS::DMS::ReplicationTask also ships every handler false).
		// The provider's own Argument Reference makes "replication_task_id"
		// a required argument, and the documented import command
		// (terraform import aws_dms_replication_task.test test-dms-replication-task-tf)
		// uses it verbatim. The Attribute Reference documents
		// "replication_task_arn" and "status", not a separate "id".
		Type:          "aws_dms_replication_task",
		Components:    []Component{attr("replication_task_id")},
		ImportSyntax:  "REPLICATION-TASK-ID",
		IdentityAttrs: []string{"replication_task_id"},
	},
	// row-gen's registry-derived proposal (server-assigned via the opaque
	// "Id") is confirmed as-is: the provider's own Attribute Reference
	// documents "id" as "Identifier of the Data Integration", and the
	// documented import command uses a service-generated UUID.
	serverAssigned("aws_appintegrations_data_integration",
		"the AppIntegrations service assigns this identity at create time; no argument reconstructs it.",
		"ID", "id"),
	TypeIdentity{
		// row-gen's registry-derived proposal (client-named via the
		// required "name" argument, from live/import-grammar.json's scraped
		// separator evidence) is confirmed as-is against the provider's own
		// Attribute Reference ("id - Identifier of the Event Integration
		// which is the name of the Event Integration") and its documented
		// import command.
		Type:          "aws_appintegrations_event_integration",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name", "id"},
	},
)

func init() { registerCohortTable(identityTableDataMovement) }
