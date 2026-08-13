// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableRemainder is the remainder cohort's slice of [DefaultTable]:
// the identity rows the remainder ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableRemainder = buildTable(
	// ---- Rejected, stragglers batch --------------------------------------
	//
	// aws_transfer_ssh_key: the documented import composite is
	// server_id/user_name/ssh_public_key_id. server_id and user_name are
	// both real Required arguments, but ssh_public_key_id is absent from
	// both the Argument Reference and the Attribute Reference entirely
	// ("This resource exports no additional attributes") - it exists only
	// inside the opaque id string the provider mints at create time. Unlike
	// aws_transfer_web_app_customization above, server_id/user_name is not
	// a singleton key either: a single user can hold more than one SSH key,
	// so the pair does not uniquely determine the resource the way a
	// bucket name determines its policy. The provider ships no tags
	// argument for this type at all (Argument Reference: region, server_id,
	// user_name, body only), so there is no marker path either. No
	// admission path recovers it - the same genuine, unrecoverable gap as
	// aws_qldb_stream and aws_elasticache_global_replication_group's own
	// rejections, not a row-gen misclassification.

	// ---- Registry-ratified (#40, #44, #65): the REMAINDER ratification
	// ---- batch. Same scope and exclusions as admission.go own matching
	// ---- banner above admittedTypesV0. Every entry below carries the
	// ---- correction a verification pass made to row-gen own raw proposal
	// ---- in its own comment where one was needed (a wrong identity
	// ---- field, a wrong argument name or case, a fixed-sentinel import
	// ---- id in place of a fabricated server-assigned one, or - for
	// ---- aws_devopsguru_resource_collection and the two AppSync
	// ---- parent-derived corrections - the whole admission path corrected
	// ---- from server-assigned to client-named); every other entry is
	// ---- accepted as row-gen printed it, its reason text kept rather
	// ---- than embellished. Twenty-eight types this batch also verified
	// ---- are deliberately absent here: 21 (DataSync/DMS/Transfer
	// ---- remainder, two AppIntegrations types, APS scraper and
	// ---- workspace) plus 7 more (ECR pull-through-cache family,
	// ---- StorageGateway tape pool, Transfer certificate/profile/web-app)
	// ---- to concurrently-landed batches that admitted them first, and one
	// ---- (aws_elasticache_global_replication_group) to a concurrent
	// ---- batch own reasoned rejection this batch defers to - see
	// ---- live/e2e/estates/remainder/README.md.
	serverAssigned("aws_datapipeline_pipeline",
		"the DataPipeline service assigns this identity at create time; no argument reconstructs it.",
		"PIPELINEID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_datazone_domain",
		"the DataZone service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_detective_graph",
		"the Detective service assigns this identity at create time; no argument reconstructs it.",
		"GRAPHARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque
		// ResourceCollectionType; the provider's own documented import command
		// instead uses the required "type" argument verbatim (a small closed
		// enum, e.g. AWS_CLOUD_FORMATION) - client-named, not server-assigned.
		Type:          "aws_devopsguru_resource_collection",
		Components:    []Component{attr("type")},
		ImportSyntax:  "TYPE",
		IdentityAttrs: []string{"type"},
	},
	serverAssigned("aws_dlm_lifecycle_policy",
		"the DLM service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_dsql_cluster",
		"the DSQL service assigns this identity at create time; no argument reconstructs it.",
		"IDENTIFIER",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_dx_connection",
		"the DirectConnect service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_dx_gateway",
		"the DirectConnect service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_dx_lag",
		"the DirectConnect service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_dx_private_virtual_interface",
		"the DirectConnect service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_dx_public_virtual_interface",
		"the DirectConnect service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_dx_transit_virtual_interface",
		"the DirectConnect service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_instance_connect_endpoint",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_local_gateway_route_table",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"LOCALGATEWAYROUTETABLEID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_local_gateway_route_table_virtual_interface_group_association",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"LOCALGATEWAYROUTETABLEVIRTUALINTERFACEGROUPASSOCIATIONID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_local_gateway_route_table_vpc_association",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"LOCALGATEWAYROUTETABLEVPCASSOCIATIONID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_network_insights_access_scope",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"NETWORKINSIGHTSACCESSSCOPEID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_network_insights_analysis",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"NETWORKINSIGHTSANALYSISID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_network_insights_path",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"NETWORKINSIGHTSPATHID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_traffic_mirror_filter",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_traffic_mirror_session",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ec2_traffic_mirror_target",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ecs_daemon_task_definition",
		"the ECS service assigns this identity at create time; no argument reconstructs it.",
		"DAEMONTASKDEFINITIONARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ecs_express_gateway_service",
		"the ECS service assigns this identity at create time; no argument reconstructs it.",
		"SERVICEARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ecs_task_definition",
		"the ECS service assigns this identity at create time; no argument reconstructs it.",
		"TASKDEFINITIONARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_efs_mount_target",
		"the EFS service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_egress_only_internet_gateway",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_elb",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_evidently_project",
		"the Evidently service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_evidently_segment",
		"the Evidently service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_fis_experiment_template",
		"the FIS service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_fms_resource_set",
		"the FMS service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_gamelift_alias",
		"the GameLift service assigns this identity at create time; no argument reconstructs it.",
		"ALIASID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_gamelift_build",
		"the GameLift service assigns this identity at create time; no argument reconstructs it.",
		"BUILDID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_gamelift_fleet",
		"the GameLift service assigns this identity at create time; no argument reconstructs it.",
		"FLEETID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_gamelift_script",
		"the GameLift service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_glue_data_quality_ruleset",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_glue_schema",
		"the Glue service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_glue_security_configuration",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		Type:          "aws_glue_workflow",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_iam_access_key",
		"the IAM service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_iam_saml_provider",
		"the IAM service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_iam_virtual_mfa_device",
		"the IAM service assigns this identity at create time; no argument reconstructs it.",
		"SERIALNUMBER",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_component",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_container_recipe",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_distribution_configuration",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_image",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_image_pipeline",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_image_recipe",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_infrastructure_configuration",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_lifecycle_policy",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_imagebuilder_workflow",
		"the ImageBuilder service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_inspector_assessment_target",
		"the Inspector service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_inspector_assessment_template",
		"the Inspector service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_internetmonitor_monitor",
		Components:    []Component{attr("monitor_name")},
		ImportSyntax:  "MONITOR_NAME",
		IdentityAttrs: []string{"monitor_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_invoicing_invoice_unit",
		"the Invoicing service assigns this identity at create time; no argument reconstructs it.",
		"INVOICEUNITARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_kinesis_analytics_application",
		"the KinesisAnalytics service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_kinesis_resource_policy",
		Components:    []Component{attr("resource_arn")},
		ImportSyntax:  "RESOURCE_ARN",
		IdentityAttrs: []string{"resource_arn"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_lb_listener_rule",
		"the ElasticLoadBalancingV2 service assigns this identity at create time; no argument reconstructs it.",
		"RULEARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_lb_trust_store",
		"the ElasticLoadBalancingV2 service assigns this identity at create time; no argument reconstructs it.",
		"TRUSTSTOREARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_m2_application",
		"the M2 service assigns this identity at create time; no argument reconstructs it.",
		"APPLICATIONID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_m2_environment",
		"the M2 service assigns this identity at create time; no argument reconstructs it.",
		"ENVIRONMENTID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_macie2_account",
		"the Macie service assigns this identity at create time; no argument reconstructs it.",
		"AWSACCOUNTID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_mailmanager_rule_set",
		"the SES service assigns this identity at create time; no argument reconstructs it.",
		"RULESETID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_mailmanager_traffic_policy",
		"the SES service assigns this identity at create time; no argument reconstructs it.",
		"TRAFFICPOLICYID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_msk_cluster_policy",
		Components:    []Component{attr("cluster_arn")},
		ImportSyntax:  "CLUSTER_ARN",
		IdentityAttrs: []string{"cluster_arn"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_msk_replicator",
		"the MSK service assigns this identity at create time; no argument reconstructs it.",
		"REPLICATORARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_msk_vpc_connection",
		"the MSK service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_mwaa_environment",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_notifications_event_rule",
		"the Notifications service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_notifications_notification_configuration",
		"the Notifications service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_notifications_notification_hub",
		Components:    []Component{attr("notification_hub_region")},
		ImportSyntax:  "NOTIFICATION_HUB_REGION",
		IdentityAttrs: []string{"notification_hub_region"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_notificationscontacts_email_contact",
		"the NotificationsContacts service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_oam_link",
		"the Oam service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_oam_sink",
		"the Oam service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_observabilityadmin_s3_table_integration",
		"the ObservabilityAdmin service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_observabilityadmin_telemetry_pipeline",
		"the ObservabilityAdmin service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_odb_cloud_autonomous_vm_cluster",
		"the ODB service assigns this identity at create time; no argument reconstructs it.",
		"CLOUDAUTONOMOUSVMCLUSTERID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_odb_cloud_exadata_infrastructure",
		"the ODB service assigns this identity at create time; no argument reconstructs it.",
		"CLOUDEXADATAINFRASTRUCTUREID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_odb_cloud_vm_cluster",
		"the ODB service assigns this identity at create time; no argument reconstructs it.",
		"CLOUDVMCLUSTERID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_odb_network",
		"the ODB service assigns this identity at create time; no argument reconstructs it.",
		"ODBNETWORKID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_odb_network_peering_connection",
		"the ODB service assigns this identity at create time; no argument reconstructs it.",
		"ODBPEERINGCONNECTIONID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_paymentcryptography_key",
		"the PaymentCryptography service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_pinpointsmsvoicev2_configuration_set",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		Type:          "aws_pinpointsmsvoicev2_opt_out_list",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_pinpointsmsvoicev2_phone_number",
		"the SMSVOICE service assigns this identity at create time; no argument reconstructs it.",
		"PHONENUMBERID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_pinpointsmsvoicev2_pool",
		"the SMSVOICE service assigns this identity at create time; no argument reconstructs it.",
		"POOLID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ram_permission",
		"the RAM service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ram_resource_share",
		"the RAM service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_rbin_rule",
		"the Rbin service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_resiliencehub_resiliency_policy",
		"the ResilienceHub service assigns this identity at create time; no argument reconstructs it.",
		"POLICYARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_resiliencehubv2_policy",
		"the ResilienceHubV2 service assigns this identity at create time; no argument reconstructs it.",
		"POLICYARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_rolesanywhere_profile",
		"the RolesAnywhere service assigns this identity at create time; no argument reconstructs it.",
		"PROFILEID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_rolesanywhere_trust_anchor",
		"the RolesAnywhere service assigns this identity at create time; no argument reconstructs it.",
		"TRUSTANCHORID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_route53_cidr_collection",
		"the Route53 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_route53_resolver_dnssec_config",
		"the Route53Resolver service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_route53_resolver_query_log_config_association",
		"the Route53Resolver service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_route53profiles_resource_association",
		"the Route53Profiles service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_route53recoverycontrolconfig_routing_control",
		"the Route53RecoveryControl service assigns this identity at create time; no argument reconstructs it.",
		"ROUTINGCONTROLARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_s3_directory_bucket",
		Components:    []Component{attr("bucket")},
		ImportSyntax:  "BUCKET",
		IdentityAttrs: []string{"bucket"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_s3control_bucket",
		"the S3Outposts service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_s3control_multi_region_access_point",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_s3files_access_point",
		"the S3Files service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_s3files_file_system",
		"the S3Files service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_s3files_file_system_policy",
		Components:    []Component{attr("file_system_id")},
		ImportSyntax:  "FILE_SYSTEM_ID",
		IdentityAttrs: []string{"file_system_id"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_s3files_mount_target",
		"the S3Files service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_s3tables_table_bucket",
		"the S3Tables service assigns this identity at create time; no argument reconstructs it.",
		"TABLEBUCKETARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_s3tables_table_bucket_policy",
		Components:    []Component{attr("table_bucket_arn")},
		ImportSyntax:  "TABLE_BUCKET_ARN",
		IdentityAttrs: []string{"table_bucket_arn"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_s3vectors_index",
		"the S3Vectors service assigns this identity at create time; no argument reconstructs it.",
		"INDEXARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_s3vectors_vector_bucket",
		"the S3Vectors service assigns this identity at create time; no argument reconstructs it.",
		"VECTORBUCKETARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_s3vectors_vector_bucket_policy",
		Components:    []Component{attr("vector_bucket_arn")},
		ImportSyntax:  "VECTOR_BUCKET_ARN",
		IdentityAttrs: []string{"vector_bucket_arn"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_schemas_discoverer",
		"the EventSchemas service assigns this identity at create time; no argument reconstructs it.",
		"DISCOVERERID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_securityhub_account",
		"Security Hub subscribes exactly one Hub per account; the provider imports it by the AWS account id, which the resource does not export as a distinct configuration argument.",
		"ACCOUNT_ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_securityhub_configuration_policy",
		"the SecurityHub service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_securityhub_finding_aggregator",
		"the SecurityHub service assigns this identity at create time; no argument reconstructs it.",
		"FINDINGAGGREGATORARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_securityhub_insight",
		"the SecurityHub service assigns this identity at create time; no argument reconstructs it.",
		"INSIGHTARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_securityhub_organization_configuration",
		"Security Hub organization configuration is a singleton per delegated administrator account; the provider imports it by the AWS account id, which the resource does not export as a distinct configuration argument.",
		"ACCOUNT_ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_securityhub_standards_subscription",
		"the SecurityHub service assigns this identity at create time; no argument reconstructs it.",
		"STANDARDSSUBSCRIPTIONARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_securitylake_data_lake",
		"the SecurityLake service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_securitylake_subscriber",
		"the SecurityLake service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_service_discovery_http_namespace",
		"the ServiceDiscovery service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_service_discovery_public_dns_namespace",
		"the ServiceDiscovery service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_service_discovery_service",
		"the ServiceDiscovery service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_sesv2_account_vdm_attributes",
		"SES maintains exactly one account-level VDM attributes record per account; its documented import id is the fixed sentinel string \"ses-account-vdm-attributes\" rather than a per-resource value, so no argument reconstructs it.",
		"ses-account-vdm-attributes",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_sesv2_configuration_set",
		Components:    []Component{attr("configuration_set_name")},
		ImportSyntax:  "CONFIGURATION_SET_NAME",
		IdentityAttrs: []string{"configuration_set_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		Type:          "aws_sesv2_dedicated_ip_pool",
		Components:    []Component{attr("pool_name")},
		ImportSyntax:  "POOL_NAME",
		IdentityAttrs: []string{"pool_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		Type:          "aws_sesv2_email_identity",
		Components:    []Component{attr("email_identity")},
		ImportSyntax:  "EMAIL_IDENTITY",
		IdentityAttrs: []string{"email_identity"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		Type:          "aws_sesv2_tenant",
		Components:    []Component{attr("tenant_name")},
		ImportSyntax:  "TENANT_NAME",
		IdentityAttrs: []string{"tenant_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_sfn_alias",
		"the StepFunctions service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_shield_proactive_engagement",
		"the Shield service assigns this identity at create time; no argument reconstructs it.",
		"ACCOUNTID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_shield_protection",
		"the Shield service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_sns_topic_subscription",
		"the SNS service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ssmcontacts_contact",
		"the SSMContacts service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ssmcontacts_contact_channel",
		"the SSMContacts service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ssmcontacts_plan",
		"the SSMContacts service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ssmcontacts_rotation",
		"the SSMContacts service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ssmincidents_replication_set",
		"Incident Manager maintains exactly one replication set per account; its documented import id is the fixed sentinel string \"import\" rather than a per-resource value, so no argument reconstructs it.",
		"import",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ssmincidents_response_plan",
		"the SSMIncidents service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ssmquicksetup_configuration_manager",
		"the SSMQuickSetup service assigns this identity at create time; no argument reconstructs it.",
		"MANAGERARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_verifiedaccess_endpoint",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"VERIFIEDACCESSENDPOINTID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_verifiedaccess_instance",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"VERIFIEDACCESSINSTANCEID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_verifiedaccess_trust_provider",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"VERIFIEDACCESSTRUSTPROVIDERID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_verifiedpermissions_policy_store",
		"the VerifiedPermissions service assigns this identity at create time; no argument reconstructs it.",
		"POLICYSTOREID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_vpc_block_public_access_exclusion",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"EXCLUSIONID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_vpc_encryption_control",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"VPCENCRYPTIONCONTROLID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_vpc_route_server",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_vpc_route_server_endpoint",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_vpc_route_server_peer",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_vpn_concentrator",
		"the EC2 service assigns this identity at create time; no argument reconstructs it.",
		"VPNCONCENTRATORID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	// ---- Registry-ratified (#40, #44, #65): the REMAINDER ratification
	// ---- batch, second slice. Same scope as admission.go own matching
	// ---- banner above.
	serverAssigned("aws_prometheus_rule_group_namespace",
		"the APS service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_arcregionswitch_plan",
		"the ARCRegionSwitch service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_arczonalshift_zonal_autoshift_configuration",
		Components:    []Component{attr("resource_arn")},
		ImportSyntax:  "RESOURCE_ARN",
		IdentityAttrs: []string{"resource_arn"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_appconfig_application",
		"the AppConfig service assigns this identity at create time; no argument reconstructs it.",
		"APPLICATIONID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_appconfig_deployment_strategy",
		"the AppConfig service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_appconfig_extension",
		"the AppConfig service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_appconfig_extension_association",
		"the AppConfig service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_athena_named_query",
		"the Athena service assigns this identity at create time; no argument reconstructs it.",
		"NAMEDQUERYID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_autoscaling_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		Type:          "aws_launch_configuration",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_bcmdataexports_export",
		"the BCMDataExports service assigns this identity at create time; no argument reconstructs it.",
		"EXPORTARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_billing_view",
		"the Billing service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ce_anomaly_monitor",
		"the CE service assigns this identity at create time; no argument reconstructs it.",
		"MONITORARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ce_anomaly_subscription",
		"the CE service assigns this identity at create time; no argument reconstructs it.",
		"SUBSCRIPTIONARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_ce_cost_category",
		"the CE service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		Type:          "aws_cur_report_definition",
		Components:    []Component{attr("report_name")},
		ImportSyntax:  "REPORT_NAME",
		IdentityAttrs: []string{"report_name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_chatbot_slack_channel_configuration",
		"the Chatbot service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cleanrooms_collaboration",
		"the CleanRooms service assigns this identity at create time; no argument reconstructs it.",
		"COLLABORATIONIDENTIFIER",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cleanrooms_configured_table",
		"the CleanRooms service assigns this identity at create time; no argument reconstructs it.",
		"CONFIGUREDTABLEIDENTIFIER",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cleanrooms_membership",
		"the CleanRooms service assigns this identity at create time; no argument reconstructs it.",
		"MEMBERSHIPIDENTIFIER",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cloud9_environment_ec2",
		"the Cloud9 service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cloudfront_cache_policy",
		"the CloudFront service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cloudfront_continuous_deployment_policy",
		"the CloudFront service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cloudfront_key_group",
		"the CloudFront service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cloudfront_origin_access_identity",
		"the CloudFront service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cloudfront_origin_request_policy",
		"the CloudFront service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cloudfront_public_key",
		"the CloudFront service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_cloudfront_response_headers_policy",
		"the CloudFront service assigns this identity at create time; no argument reconstructs it.",
		"ID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	// row-gen proposed this client-named via "arn", reading the provider's
	// own identity schema's required_for_import field name literally as a
	// settable configuration argument - but the pinned v6.58.0 provider's
	// own schema marks "arn" Computed-only for this type (confirmed by
	// `terraform validate`: "Can't configure a value for \"arn\": its
	// value will be decided automatically based on the result of applying
	// this configuration"). CloudTrail mints the trail's own ARN at
	// create time; no argument reconstructs it, so this is server-assigned
	// via the marker path, not client-named.
	serverAssigned("aws_cloudtrail",
		"the CloudTrail service assigns this identity at create time; no argument reconstructs it.",
		"ARN",
	),
	serverAssigned("aws_cloudtrail_event_data_store",
		"the CloudTrail service assigns this identity at create time; no argument reconstructs it.",
		"EVENTDATASTOREARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_codegurureviewer_repository_association",
		"the CodeGuruReviewer service assigns this identity at create time; no argument reconstructs it.",
		"ASSOCIATIONARN",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	serverAssigned("aws_appsync_api",
		"the AppSync service assigns this identity at create time; no argument reconstructs it.",
		"APIID",
		// IdentityAttrs intentionally omitted: whether this type's own "id"
		// attribute equals the identity above is the id-alias inference row-gen
		// does not make (issue #44 non-goals). Add "id" and any other alias
		// only after confirming it against the provider schema or docs.
	),
	TypeIdentity{
		// row-gen filed this needs-hand-separator against the registry's
		// composite primaryIdentifier (AccountId, Region) - but the real
		// identity is simpler than the registry's own evidence implied: this
		// resource is a per-region singleton (a zonal shift practice run
		// observer's notification-status toggle for the run's own region),
		// and the provider's own documented import command uses the region
		// alone, not an account-id-region composite.
		Type:          "aws_arczonalshift_autoshift_observer_notification_status",
		Components:    []Component{cloud(CloudRegion)},
		ImportSyntax:  "REGION",
		IdentityAttrs: []string{},
	},
	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque Id,
		// but the provider's own documented import command uses the required
		// api_id argument verbatim - a named-singleton child of aws_appsync_api
		// (at most one cache per API), the same shape as aws_s3_bucket_policy
		// keyed on its bucket.
		Type:          "aws_appsync_api_cache",
		Components:    []Component{attr("api_id")},
		ImportSyntax:  "API_ID",
		IdentityAttrs: []string{},
	},
	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque
		// ApiAssociationIdentifier, but the provider's own documented import
		// command uses the required domain_name argument verbatim.
		Type:          "aws_appsync_domain_name_api_association",
		Components:    []Component{attr("domain_name")},
		ImportSyntax:  "DOMAIN_NAME",
		IdentityAttrs: []string{},
	},
)

func init() { registerCohortTable(identityTableRemainder) }
