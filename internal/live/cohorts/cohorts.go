// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package cohorts is the roster of the per-cohort verification estates
// tools/estate-gen renders - what used to be the 32 committed directories
// under live/e2e/estates (issue #699).
//
// Those directories are gone. They were generator output, committed: every
// working copy accumulated an ignored .terraform/ inside each one, .gitignore
// carried a scoped exception block to manage them, and a regeneration was a
// 213-file diff that nobody read. tools/terralith-gen had already chosen the
// other model - live/e2e/terralith-scale/run.sh generates its estate into the
// run's own work directory and commits nothing - and this package is what
// lets the cohort tier do the same: the roster is committed, the rendering is
// not.
//
// What is committed here is exactly what the deleted trees' GENERATED.md
// files recorded as their regeneration command, extracted verbatim on
// 2026-09-06 before the deletion:
//
//	go run ./tools/estate-gen -cohort <name> -types <Types...> -out <dir>
//
// Types is the pinned roster, not a live read of the admission table.
// Pinning is deliberate and predates this package: a cohort whose roster
// followed admission growth would gain a resource block as a side effect of
// ratifying a type somewhere else, and the s3 cohort did exactly that once
// (a newly-mapped aws_s3control_multi_region_access_point walked into the
// acceptance tier on a generator defect). Growing a cohort is an edit here,
// judged by an acceptance run.
//
// Supporting is the other half of what a rendered cohort declares: the
// resources the generator adds because some roster type's required argument
// names them (gen.go's supporting pass, the NeedsSupporting/NeedsIAMRole
// overrides, and the parent references the identity table implies). It is a
// measurement, not an input - the generator derives it - recorded so that the
// ungated guards which used to read the committed .tf files still have the
// same universe of types to check against. TestGeneratedCohortsDeclareTheRecordedTypes
// in tools/estate-gen holds it to the generator's real output.
package cohorts

import "sort"

// Cohort is one verification estate: its name, the pinned roster
// tools/estate-gen renders, and the supporting types that rendering adds.
type Cohort struct {
	// Name is the cohort's directory name under the run's work directory,
	// and the -cohort argument.
	Name string

	// Types is the -types roster, sorted.
	Types []string

	// Supporting is every resource type the rendered cohort declares that
	// Types does not name, sorted. Derived by the generator; recorded.
	Supporting []string
}

// all is the roster. Sorted by name; keep it that way.
var all = []Cohort{
	{
		Name: "ai-location",
		Types: []string{
			"aws_bedrock_guardrail",
			"aws_bedrock_inference_profile",
			"aws_bedrockagent_agent",
			"aws_bedrockagent_agent_alias",
			"aws_bedrockagent_flow",
			"aws_bedrockagent_knowledge_base",
			"aws_bedrockagent_prompt",
			"aws_bedrockagentcore_agent_runtime",
			"aws_bedrockagentcore_agent_runtime_endpoint",
			"aws_bedrockagentcore_api_key_credential_provider",
			"aws_bedrockagentcore_browser",
			"aws_bedrockagentcore_browser_profile",
			"aws_bedrockagentcore_code_interpreter",
			"aws_bedrockagentcore_evaluator",
			"aws_bedrockagentcore_gateway",
			"aws_bedrockagentcore_harness",
			"aws_bedrockagentcore_memory",
			"aws_bedrockagentcore_oauth2_credential_provider",
			"aws_bedrockagentcore_online_evaluation_config",
			"aws_bedrockagentcore_policy_engine",
			"aws_bedrockagentcore_resource_policy",
			"aws_comprehend_document_classifier",
			"aws_kendra_index",
			"aws_lexv2models_bot",
			"aws_lexv2models_bot_locale",
			"aws_location_geofence_collection",
			"aws_location_map",
			"aws_location_place_index",
			"aws_location_route_calculator",
			"aws_location_tracker",
			"aws_location_tracker_association",
			"aws_qbusiness_application",
			"aws_rekognition_collection",
			"aws_rekognition_project",
			"aws_rekognition_stream_processor",
		},
		Supporting: []string{
			"aws_iam_role",
		},
	},
	{
		Name: "apigateway",
		Types: []string{
			"aws_api_gateway_api_key",
			"aws_api_gateway_base_path_mapping",
			"aws_api_gateway_client_certificate",
			"aws_api_gateway_documentation_version",
			"aws_api_gateway_domain_name",
			"aws_api_gateway_domain_name_access_association",
			"aws_api_gateway_gateway_response",
			"aws_api_gateway_integration",
			"aws_api_gateway_integration_response",
			"aws_api_gateway_method",
			"aws_api_gateway_method_response",
			"aws_api_gateway_method_settings",
			"aws_api_gateway_model",
			"aws_api_gateway_rest_api",
			"aws_api_gateway_rest_api_policy",
			"aws_api_gateway_stage",
			"aws_api_gateway_usage_plan",
			"aws_api_gateway_usage_plan_key",
			"aws_api_gateway_vpc_link",
			"aws_apigatewayv2_api",
			"aws_apigatewayv2_domain_name",
			"aws_apigatewayv2_stage",
			"aws_apigatewayv2_vpc_link",
		},
		Supporting: []string{
			"aws_lb",
			"aws_subnet",
			"aws_vpc",
		},
	},
	{
		Name: "aps",
		Types: []string{
			"aws_prometheus_alert_manager_definition",
			"aws_prometheus_query_logging_configuration",
			"aws_prometheus_scraper",
			"aws_prometheus_scraper_logging_configuration",
			"aws_prometheus_workspace",
		},
		Supporting: nil,
	},
	{
		Name: "compute-platforms",
		Types: []string{
			"aws_amplify_app",
			"aws_amplify_branch",
			"aws_apprunner_auto_scaling_configuration_version",
			"aws_apprunner_observability_configuration",
			"aws_apprunner_service",
			"aws_apprunner_vpc_connector",
			"aws_apprunner_vpc_ingress_connection",
			"aws_batch_compute_environment",
			"aws_batch_job_definition",
			"aws_batch_job_queue",
			"aws_batch_scheduling_policy",
			"aws_elastic_beanstalk_application",
			"aws_elastic_beanstalk_environment",
			"aws_emr_cluster",
			"aws_emr_security_configuration",
			"aws_emr_studio",
			"aws_emrcontainers_virtual_cluster",
			"aws_emrserverless_application",
			"aws_lightsail_bucket",
			"aws_lightsail_certificate",
			"aws_lightsail_container_service",
			"aws_lightsail_database",
			"aws_lightsail_disk",
			"aws_lightsail_distribution",
			"aws_lightsail_instance",
			"aws_lightsail_lb",
			"aws_lightsail_lb_certificate",
			"aws_lightsail_static_ip",
		},
		Supporting: []string{
			"aws_iam_role",
		},
	},
	{
		Name: "connect-euc",
		Types: []string{
			"aws_appstream_fleet_stack_association",
			"aws_appstream_stack",
			"aws_appstream_user",
			"aws_connect_contact_flow",
			"aws_connect_contact_flow_module",
			"aws_connect_hours_of_operation",
			"aws_connect_instance",
			"aws_connect_phone_number",
			"aws_connect_queue",
			"aws_connect_quick_connect",
			"aws_connect_routing_profile",
			"aws_connect_security_profile",
			"aws_connect_user",
			"aws_connect_user_hierarchy_group",
			"aws_connect_user_hierarchy_structure",
			"aws_workspaces_connection_alias",
			"aws_workspaces_ip_group",
			"aws_workspaces_pool",
			"aws_workspaces_workspace",
			"aws_workspacesweb_browser_settings",
			"aws_workspacesweb_browser_settings_association",
			"aws_workspacesweb_data_protection_settings",
			"aws_workspacesweb_data_protection_settings_association",
			"aws_workspacesweb_identity_provider",
			"aws_workspacesweb_ip_access_settings",
			"aws_workspacesweb_ip_access_settings_association",
			"aws_workspacesweb_network_settings",
			"aws_workspacesweb_network_settings_association",
			"aws_workspacesweb_portal",
			"aws_workspacesweb_session_logger",
			"aws_workspacesweb_session_logger_association",
			"aws_workspacesweb_trust_store",
			"aws_workspacesweb_trust_store_association",
			"aws_workspacesweb_user_access_logging_settings",
			"aws_workspacesweb_user_access_logging_settings_association",
			"aws_workspacesweb_user_settings",
			"aws_workspacesweb_user_settings_association",
		},
		Supporting: nil,
	},
	{
		Name: "data",
		Types: []string{
			"aws_athena_data_catalog",
			"aws_athena_workgroup",
			"aws_glue_catalog_database",
			"aws_glue_catalog_table",
			"aws_glue_classifier",
			"aws_glue_connection",
			"aws_glue_crawler",
			"aws_glue_data_catalog_encryption_settings",
			"aws_glue_job",
			"aws_glue_ml_transform",
			"aws_glue_registry",
			"aws_glue_trigger",
			"aws_kinesis_firehose_delivery_stream",
			"aws_kinesis_stream",
			"aws_kinesis_stream_consumer",
		},
		Supporting: []string{
			"aws_iam_role",
			"aws_kms_key",
			"aws_s3_bucket",
		},
	},
	{
		Name: "data-movement",
		Types: []string{
			"aws_appintegrations_data_integration",
			"aws_appintegrations_event_integration",
			"aws_datasync_agent",
			"aws_datasync_location_azure_blob",
			"aws_datasync_location_efs",
			"aws_datasync_location_fsx_lustre_file_system",
			"aws_datasync_location_fsx_ontap_file_system",
			"aws_datasync_location_fsx_openzfs_file_system",
			"aws_datasync_location_fsx_windows_file_system",
			"aws_datasync_location_hdfs",
			"aws_datasync_location_nfs",
			"aws_datasync_location_object_storage",
			"aws_datasync_location_s3",
			"aws_datasync_location_smb",
			"aws_datasync_task",
			"aws_dms_certificate",
			"aws_dms_endpoint",
			"aws_dms_event_subscription",
			"aws_dms_replication_config",
			"aws_dms_replication_instance",
			"aws_dms_replication_subnet_group",
			"aws_dms_replication_task",
			"aws_dms_s3_endpoint",
			"aws_transfer_connector",
			"aws_transfer_server",
			"aws_transfer_user",
			"aws_transfer_workflow",
		},
		Supporting: []string{
			"aws_iam_role",
			"aws_s3_bucket",
			"aws_secretsmanager_secret",
		},
	},
	{
		Name: "databases",
		Types: []string{
			"aws_docdb_event_subscription",
			"aws_docdbelastic_cluster",
			"aws_elasticsearch_domain",
			"aws_keyspaces_keyspace",
			"aws_keyspaces_table",
			"aws_memorydb_acl",
			"aws_memorydb_cluster",
			"aws_memorydb_multi_region_cluster",
			"aws_memorydb_parameter_group",
			"aws_memorydb_subnet_group",
			"aws_memorydb_user",
			"aws_neptune_cluster_parameter_group",
			"aws_neptune_parameter_group",
			"aws_neptune_subnet_group",
			"aws_opensearch_domain",
			"aws_opensearchserverless_access_policy",
			"aws_opensearchserverless_collection",
			"aws_opensearchserverless_collection_group",
			"aws_opensearchserverless_lifecycle_policy",
			"aws_opensearchserverless_security_policy",
			"aws_qldb_ledger",
			"aws_redshift_cluster",
			"aws_redshift_endpoint_access",
			"aws_redshift_parameter_group",
			"aws_redshift_snapshot_schedule",
			"aws_redshift_subnet_group",
			"aws_redshiftserverless_namespace",
			"aws_redshiftserverless_workgroup",
			"aws_timestreaminfluxdb_db_cluster",
			"aws_timestreaminfluxdb_db_instance",
			"aws_timestreamquery_scheduled_query",
			"aws_timestreamwrite_database",
			"aws_timestreamwrite_table",
		},
		Supporting: []string{
			"aws_iam_role",
			"aws_security_group",
			"aws_subnet",
		},
	},
	{
		Name: "devtools",
		Types: []string{
			"aws_codeartifact_domain",
			"aws_codeartifact_domain_permissions_policy",
			"aws_codeartifact_repository",
			"aws_codeartifact_repository_permissions_policy",
			"aws_codebuild_fleet",
			"aws_codebuild_project",
			"aws_codebuild_report_group",
			"aws_codebuild_webhook",
			"aws_codecommit_repository",
			"aws_codeconnections_connection",
			"aws_codedeploy_app",
			"aws_codedeploy_deployment_config",
			"aws_codedeploy_deployment_group",
			"aws_codepipeline",
			"aws_codepipeline_custom_action_type",
			"aws_codepipeline_webhook",
			"aws_codestarconnections_connection",
			"aws_codestarnotifications_notification_rule",
			"aws_ecrpublic_repository",
			"aws_ecrpublic_repository_policy",
		},
		Supporting: []string{
			"aws_iam_role",
		},
	},
	{
		Name: "dynamodb-elasticache",
		Types: []string{
			"aws_dynamodb_global_table",
			"aws_dynamodb_resource_policy",
			"aws_elasticache_cluster",
			"aws_elasticache_parameter_group",
			"aws_elasticache_replication_group",
			"aws_elasticache_serverless_cache",
			"aws_elasticache_subnet_group",
			"aws_elasticache_user",
			"aws_elasticache_user_group",
		},
		Supporting: []string{
			"aws_subnet",
		},
	},
	{
		Name: "ec2-core",
		Types: []string{
			"aws_ebs_snapshot_block_public_access",
			"aws_ec2_capacity_reservation",
			"aws_ec2_fleet",
			"aws_ec2_host",
			"aws_instance",
			"aws_key_pair",
			"aws_network_interface",
			"aws_placement_group",
			"aws_spot_fleet_request",
			"aws_volume_attachment",
		},
		Supporting: []string{
			"aws_ebs_volume",
			"aws_security_group",
			"aws_subnet",
		},
	},
	{
		Name: "ec2-networking",
		Types: []string{
			"aws_customer_gateway",
			"aws_ec2_client_vpn_endpoint",
			"aws_ec2_client_vpn_route",
			"aws_ec2_managed_prefix_list",
			"aws_ec2_managed_prefix_list_entry",
			"aws_ec2_transit_gateway",
			"aws_ec2_transit_gateway_connect",
			"aws_ec2_transit_gateway_connect_peer",
			"aws_ec2_transit_gateway_metering_policy",
			"aws_ec2_transit_gateway_metering_policy_entry",
			"aws_ec2_transit_gateway_multicast_domain",
			"aws_ec2_transit_gateway_peering_attachment",
			"aws_ec2_transit_gateway_policy_table",
			"aws_ec2_transit_gateway_policy_table_association",
			"aws_ec2_transit_gateway_route",
			"aws_ec2_transit_gateway_route_table",
			"aws_ec2_transit_gateway_route_table_association",
			"aws_ec2_transit_gateway_route_table_propagation",
			"aws_ec2_transit_gateway_vpc_attachment",
			"aws_flow_log",
			"aws_nat_gateway",
			"aws_nat_gateway_eip_association",
			"aws_network_acl",
			"aws_network_acl_rule",
			"aws_security_group_rule",
			"aws_vpc_dhcp_options",
			"aws_vpc_dhcp_options_association",
			"aws_vpc_endpoint",
			"aws_vpc_endpoint_policy",
			"aws_vpc_endpoint_private_dns",
			"aws_vpc_endpoint_route_table_association",
			"aws_vpc_endpoint_security_group_association",
			"aws_vpc_endpoint_service",
			"aws_vpc_endpoint_subnet_association",
			"aws_vpc_ipam",
			"aws_vpc_ipam_pool",
			"aws_vpc_ipam_pool_cidr",
			"aws_vpc_ipam_resource_discovery",
			"aws_vpc_ipam_resource_discovery_association",
			"aws_vpc_ipam_scope",
			"aws_vpc_peering_connection",
			"aws_vpn_connection",
			"aws_vpn_gateway",
		},
		Supporting: []string{
			"aws_eip",
			"aws_lb",
			"aws_route_table",
			"aws_security_group",
			"aws_subnet",
			"aws_vpc",
		},
	},
	{
		Name: "ecs-eks",
		Types: []string{
			"aws_appautoscaling_target",
			"aws_ecs_capacity_provider",
			"aws_ecs_cluster_capacity_providers",
			"aws_ecs_daemon",
			"aws_ecs_daemon_task_definition",
			"aws_ecs_service",
			"aws_eks_access_entry",
			"aws_eks_access_policy_association",
			"aws_eks_addon",
			"aws_eks_capability",
			"aws_eks_cluster",
			"aws_eks_fargate_profile",
			"aws_eks_node_group",
		},
		Supporting: []string{
			"aws_dynamodb_table",
			"aws_ecs_cluster",
			"aws_ecs_task_definition",
			"aws_iam_role",
		},
	},
	{
		Name: "governance",
		Types: []string{
			"aws_auditmanager_assessment",
			"aws_auditmanager_framework",
			"aws_budgets_budget_action",
			"aws_config_config_rule",
			"aws_config_configuration_aggregator",
			"aws_config_conformance_pack",
			"aws_config_organization_conformance_pack",
			"aws_config_remediation_configuration",
			"aws_controltower_baseline",
			"aws_controltower_control",
			"aws_controltower_landing_zone",
			"aws_organizations_account",
			"aws_organizations_organizational_unit",
			"aws_organizations_policy",
			"aws_organizations_resource_policy",
			"aws_resourceexplorer2_index",
			"aws_resourceexplorer2_view",
			"aws_resourcegroups_group",
			"aws_servicecatalog_portfolio",
			"aws_servicecatalog_portfolio_share",
			"aws_servicecatalog_product",
			"aws_servicecatalog_provisioned_product",
			"aws_servicecatalogappregistry_application",
			"aws_servicecatalogappregistry_attribute_group",
			"aws_servicecatalogappregistry_attribute_group_association",
		},
		Supporting: []string{
			"aws_iam_policy",
			"aws_iam_role",
		},
	},
	{
		Name: "iam-ecr",
		Types: []string{
			"aws_ecr_repository",
			"aws_iam_group",
			"aws_iam_instance_profile",
			"aws_iam_service_linked_role",
			"aws_iam_user",
		},
		Supporting: []string{
			"aws_iam_role",
		},
	},
	{
		Name: "identity",
		Types: []string{
			"aws_cognito_identity_pool",
			"aws_cognito_identity_pool_provider_principal_tag",
			"aws_cognito_identity_pool_roles_attachment",
			"aws_cognito_identity_provider",
			"aws_cognito_resource_server",
			"aws_cognito_user",
			"aws_cognito_user_group",
			"aws_cognito_user_in_group",
			"aws_cognito_user_pool",
			"aws_cognito_user_pool_domain",
			"aws_iam_group_policy",
			"aws_iam_group_policy_attachment",
			"aws_iam_openid_connect_provider",
			"aws_iam_policy",
			"aws_iam_server_certificate",
			"aws_iam_user_policy",
			"aws_iam_user_policy_attachment",
			"aws_ssoadmin_account_assignment",
			"aws_ssoadmin_application",
			"aws_ssoadmin_application_assignment",
			"aws_ssoadmin_instance_access_control_attributes",
			"aws_ssoadmin_permission_set",
		},
		Supporting: []string{
			"aws_iam_group",
			"aws_iam_saml_provider",
			"aws_iam_user",
		},
	},
	{
		Name: "iot",
		Types: []string{
			"aws_iot_authorizer",
			"aws_iot_billing_group",
			"aws_iot_domain_configuration",
			"aws_iot_policy",
			"aws_iot_provisioning_template",
			"aws_iot_role_alias",
			"aws_iot_thing",
			"aws_iot_thing_group",
			"aws_iot_thing_type",
			"aws_iot_topic_rule",
		},
		Supporting: []string{
			"aws_acm_certificate",
			"aws_iam_role",
		},
	},
	{
		Name: "lambda",
		Types: []string{
			"aws_lambda_capacity_provider",
			"aws_lambda_code_signing_config",
			"aws_lambda_event_source_mapping",
			"aws_lambda_function",
			"aws_lambda_function_event_invoke_config",
			"aws_lambda_layer_version_permission",
			"aws_lambda_permission",
		},
		Supporting: []string{
			"aws_api_gateway_rest_api",
			"aws_iam_role",
		},
	},
	{
		Name: "media",
		Types: []string{
			"aws_ivs_channel",
			"aws_ivs_recording_configuration",
			"aws_ivschat_logging_configuration",
			"aws_ivschat_room",
			"aws_media_package_channel",
			"aws_media_packagev2_channel_group",
			"aws_medialive_multiplex",
		},
		Supporting: nil,
	},
	{
		Name: "messaging",
		Types: []string{
			"aws_cloudwatch_composite_alarm",
			"aws_cloudwatch_dashboard",
			"aws_cloudwatch_metric_stream",
			"aws_sns_topic",
			"aws_sns_topic_policy",
			"aws_sqs_queue",
			"aws_sqs_queue_policy",
		},
		Supporting: []string{
			"aws_cloudwatch_metric_alarm",
			"aws_iam_role",
		},
	},
	{
		Name: "networking-advanced",
		Types: []string{
			"aws_globalaccelerator_accelerator",
			"aws_globalaccelerator_cross_account_attachment",
			"aws_networkfirewall_firewall",
			"aws_networkfirewall_firewall_policy",
			"aws_networkfirewall_logging_configuration",
			"aws_networkfirewall_rule_group",
			"aws_networkfirewall_tls_inspection_configuration",
			"aws_networkfirewall_vpc_endpoint_association",
			"aws_networkmanager_connect_attachment",
			"aws_networkmanager_connect_peer",
			"aws_networkmanager_core_network",
			"aws_networkmanager_customer_gateway_association",
			"aws_networkmanager_device",
			"aws_networkmanager_dx_gateway_attachment",
			"aws_networkmanager_global_network",
			"aws_networkmanager_link",
			"aws_networkmanager_link_association",
			"aws_networkmanager_prefix_list_association",
			"aws_networkmanager_site",
			"aws_networkmanager_site_to_site_vpn_attachment",
			"aws_networkmanager_transit_gateway_peering",
			"aws_networkmanager_transit_gateway_registration",
			"aws_networkmanager_transit_gateway_route_table_attachment",
			"aws_networkmanager_vpc_attachment",
			"aws_route53recoveryreadiness_cell",
			"aws_route53recoveryreadiness_readiness_check",
			"aws_route53recoveryreadiness_recovery_group",
			"aws_route53recoveryreadiness_resource_set",
			"aws_vpclattice_access_log_subscription",
			"aws_vpclattice_auth_policy",
			"aws_vpclattice_domain_verification",
			"aws_vpclattice_listener",
			"aws_vpclattice_listener_rule",
			"aws_vpclattice_resource_configuration",
			"aws_vpclattice_resource_gateway",
			"aws_vpclattice_resource_policy",
			"aws_vpclattice_service",
			"aws_vpclattice_service_network",
			"aws_vpclattice_service_network_resource_association",
			"aws_vpclattice_service_network_service_association",
			"aws_vpclattice_service_network_vpc_association",
			"aws_vpclattice_target_group",
		},
		Supporting: nil,
	},
	{
		Name: "observability",
		Types: []string{
			"aws_cloudwatch_alarm_mute_rule",
			"aws_cloudwatch_contributor_insight_rule",
			"aws_cloudwatch_event_api_destination",
			"aws_cloudwatch_event_archive",
			"aws_cloudwatch_event_bus",
			"aws_cloudwatch_event_connection",
			"aws_cloudwatch_event_endpoint",
			"aws_cloudwatch_event_permission",
			"aws_cloudwatch_event_rule",
			"aws_cloudwatch_event_target",
			"aws_cloudwatch_log_account_policy",
			"aws_cloudwatch_log_anomaly_detector",
			"aws_cloudwatch_log_delivery",
			"aws_cloudwatch_log_delivery_destination",
			"aws_cloudwatch_log_delivery_source",
			"aws_cloudwatch_log_destination",
			"aws_cloudwatch_log_metric_filter",
			"aws_cloudwatch_log_resource_policy",
			"aws_cloudwatch_log_stream",
			"aws_cloudwatch_log_subscription_filter",
			"aws_cloudwatch_log_transformer",
			"aws_grafana_workspace",
			"aws_rum_app_monitor",
			"aws_sfn_activity",
			"aws_synthetics_canary",
			"aws_synthetics_group",
			"aws_xray_group",
			"aws_xray_resource_policy",
			"aws_xray_sampling_rule",
		},
		Supporting: []string{
			"aws_cloudwatch_log_group",
			"aws_iam_role",
		},
	},
	{
		Name: "rds",
		Types: []string{
			"aws_db_event_subscription",
			"aws_db_instance",
			"aws_db_instance_role_association",
			"aws_db_option_group",
			"aws_db_parameter_group",
			"aws_db_proxy",
			"aws_db_proxy_default_target_group",
			"aws_db_proxy_endpoint",
			"aws_db_subnet_group",
			"aws_rds_cluster",
			"aws_rds_cluster_instance",
			"aws_rds_cluster_parameter_group",
			"aws_rds_cluster_role_association",
			"aws_rds_custom_db_engine_version",
			"aws_rds_global_cluster",
			"aws_rds_integration",
			"aws_rds_shard_group",
		},
		Supporting: []string{
			"aws_subnet",
		},
	},
	{
		Name: "remainder",
		Types: []string{
			"aws_appconfig_application",
			"aws_appconfig_configuration_profile",
			"aws_appconfig_deployment",
			"aws_appconfig_environment",
			"aws_arcregionswitch_plan",
			"aws_billing_view",
			"aws_cleanrooms_collaboration",
			"aws_cloud9_environment_ec2",
			"aws_cloudtrail",
			"aws_codegurureviewer_repository_association",
			"aws_datazone_domain",
			"aws_datazone_environment_blueprint_configuration",
			"aws_detective_graph",
			"aws_dsql_cluster",
			"aws_dx_connection",
			"aws_ec2_instance_connect_endpoint",
			"aws_evidently_project",
			"aws_gamelift_fleet",
			"aws_glue_workflow",
			"aws_imagebuilder_image_pipeline",
			"aws_paymentcryptography_key",
			"aws_paymentcryptography_key_alias",
			"aws_sesv2_contact_list",
			"aws_sesv2_email_identity",
		},
		Supporting: []string{
			"aws_cloudwatch_metric_alarm",
			"aws_codecommit_repository",
			"aws_iam_role",
			"aws_kms_key",
			"aws_lambda_function",
			"aws_subnet",
		},
	},
	{
		Name: "route53-cloudfront",
		Types: []string{
			"aws_cloudfront_anycast_ip_list",
			"aws_cloudfront_connection_function",
			"aws_cloudfront_connection_group",
			"aws_cloudfront_distribution",
			"aws_cloudfront_distribution_tenant",
			"aws_cloudfront_function",
			"aws_cloudfront_key_value_store",
			"aws_cloudfront_monitoring_subscription",
			"aws_cloudfront_multitenant_distribution",
			"aws_cloudfront_realtime_log_config",
			"aws_cloudfront_trust_store",
			"aws_cloudfront_vpc_origin",
			"aws_route53_health_check",
			"aws_route53_hosted_zone_dnssec",
			"aws_route53_key_signing_key",
			"aws_route53_resolver_endpoint",
			"aws_route53_resolver_firewall_domain_list",
			"aws_route53_resolver_firewall_rule",
			"aws_route53_resolver_firewall_rule_group",
			"aws_route53_resolver_firewall_rule_group_association",
			"aws_route53_resolver_query_log_config",
			"aws_route53_resolver_rule",
			"aws_route53_zone_association",
			"aws_route53profiles_association",
			"aws_route53profiles_profile",
			"aws_route53recoverycontrolconfig_cluster",
			"aws_route53recoverycontrolconfig_control_panel",
			"aws_route53recoverycontrolconfig_safety_rule",
		},
		Supporting: []string{
			"aws_route53_zone",
			"aws_vpc",
		},
	},
	{
		Name: "s3",
		Types: []string{
			"aws_s3_bucket",
			"aws_s3_bucket_lifecycle_configuration",
			"aws_s3_bucket_policy",
			"aws_s3_bucket_public_access_block",
			"aws_s3_bucket_server_side_encryption_configuration",
			"aws_s3_bucket_versioning",
		},
		Supporting: nil,
	},
	{
		Name: "sagemaker",
		Types: []string{
			"aws_sagemaker_algorithm",
			"aws_sagemaker_app",
			"aws_sagemaker_app_image_config",
			"aws_sagemaker_code_repository",
			"aws_sagemaker_data_quality_job_definition",
			"aws_sagemaker_device_fleet",
			"aws_sagemaker_domain",
			"aws_sagemaker_endpoint",
			"aws_sagemaker_endpoint_configuration",
			"aws_sagemaker_feature_group",
			"aws_sagemaker_hub",
			"aws_sagemaker_image",
			"aws_sagemaker_mlflow_app",
			"aws_sagemaker_mlflow_tracking_server",
			"aws_sagemaker_model",
			"aws_sagemaker_model_card",
			"aws_sagemaker_model_package_group",
			"aws_sagemaker_model_package_group_policy",
			"aws_sagemaker_monitoring_schedule",
			"aws_sagemaker_notebook_instance",
			"aws_sagemaker_notebook_instance_lifecycle_configuration",
			"aws_sagemaker_pipeline",
			"aws_sagemaker_project",
			"aws_sagemaker_space",
			"aws_sagemaker_studio_lifecycle_config",
			"aws_sagemaker_user_profile",
			"aws_sagemaker_workteam",
		},
		Supporting: []string{
			"aws_iam_role",
			"aws_s3_bucket",
			"aws_servicecatalog_product",
			"aws_subnet",
			"aws_vpc",
		},
	},
	{
		Name: "security",
		Types: []string{
			"aws_acmpca_certificate_authority",
			"aws_acmpca_certificate_authority_certificate",
			"aws_acmpca_policy",
			"aws_guardduty_detector",
			"aws_guardduty_filter",
			"aws_guardduty_ipset",
			"aws_guardduty_malware_protection_plan",
			"aws_guardduty_member",
			"aws_guardduty_organization_admin_account",
			"aws_guardduty_organization_configuration",
			"aws_guardduty_publishing_destination",
			"aws_guardduty_threatintelset",
			"aws_inspector2_delegated_admin_account",
			"aws_inspector2_filter",
			"aws_inspector2_member_association",
			"aws_kms_external_key",
			"aws_kms_replica_key",
			"aws_macie2_classification_job",
			"aws_macie2_custom_data_identifier",
			"aws_macie2_findings_filter",
			"aws_macie2_member",
			"aws_macie2_organization_admin_account",
			"aws_secretsmanager_secret",
			"aws_secretsmanager_secret_policy",
			"aws_secretsmanager_secret_rotation",
			"aws_securityhub_account_v2",
			"aws_securityhub_aggregator_v2",
			"aws_securityhub_automation_rule",
			"aws_securityhub_automation_rule_v2",
			"aws_securityhub_configuration_policy_association",
			"aws_securityhub_connector_v2",
			"aws_securityhub_member",
			"aws_securityhub_organization_admin_account",
			"aws_securityhub_standards_control",
			"aws_securityhub_standards_control_association",
			"aws_ssm_association",
			"aws_ssm_maintenance_window",
			"aws_ssm_patch_baseline",
			"aws_ssm_patch_group",
			"aws_ssm_resource_data_sync",
			"aws_ssm_service_setting",
			"aws_wafv2_ip_set",
			"aws_wafv2_regex_pattern_set",
			"aws_wafv2_rule_group",
			"aws_wafv2_web_acl",
			"aws_wafv2_web_acl_logging_configuration",
			"aws_wafv2_web_acl_rule",
		},
		Supporting: []string{
			"aws_iam_role",
			"aws_lambda_function",
		},
	},
	{
		Name: "storage",
		Types: []string{
			"aws_backup_framework",
			"aws_backup_logically_air_gapped_vault",
			"aws_backup_plan",
			"aws_backup_report_plan",
			"aws_backup_restore_testing_plan",
			"aws_backup_vault",
			"aws_efs_access_point",
			"aws_efs_file_system",
			"aws_fsx_data_repository_association",
			"aws_fsx_lustre_file_system",
			"aws_fsx_ontap_file_system",
			"aws_fsx_ontap_storage_virtual_machine",
			"aws_fsx_ontap_volume",
			"aws_fsx_openzfs_file_system",
			"aws_fsx_openzfs_snapshot",
			"aws_fsx_openzfs_volume",
			"aws_fsx_s3_access_point_attachment",
			"aws_fsx_windows_file_system",
		},
		Supporting: []string{
			"aws_kms_key",
			"aws_s3_bucket",
			"aws_subnet",
		},
	},
	{
		Name: "stragglers",
		Types: []string{
			"aws_ecr_lifecycle_policy",
			"aws_ecr_pull_through_cache_rule",
			"aws_ecr_pull_time_update_exclusion",
			"aws_ecr_repository_creation_template",
			"aws_ecr_repository_policy",
			"aws_networkmanager_core_network_policy_attachment",
			"aws_storagegateway_tape_pool",
			"aws_transfer_agreement",
			"aws_transfer_certificate",
			"aws_transfer_profile",
			"aws_transfer_web_app",
			"aws_transfer_web_app_customization",
		},
		Supporting: []string{
			"aws_ecr_repository",
		},
	},
	{
		Name: "streaming",
		Types: []string{
			"aws_appflow_connector_profile",
			"aws_appflow_flow",
			"aws_appsync_channel_namespace",
			"aws_appsync_domain_name",
			"aws_appsync_graphql_api",
			"aws_mq_broker",
			"aws_mq_configuration",
			"aws_msk_cluster",
			"aws_msk_scram_secret_association",
			"aws_msk_serverless_cluster",
			"aws_msk_topic",
			"aws_mskconnect_connector",
			"aws_mskconnect_custom_plugin",
			"aws_mskconnect_worker_configuration",
			"aws_pipes_pipe",
			"aws_scheduler_schedule_group",
		},
		Supporting: []string{
			"aws_iam_role",
		},
	},
}

// All returns every cohort, sorted by name.
func All() []Cohort {
	out := make([]Cohort, len(all))
	copy(out, all)
	return out
}

// Names returns every cohort name, sorted.
func Names() []string {
	out := make([]string, 0, len(all))
	for _, c := range all {
		out = append(out, c.Name)
	}
	return out
}

// Lookup returns the named cohort.
func Lookup(name string) (Cohort, bool) {
	for _, c := range all {
		if c.Name == name {
			return c, true
		}
	}
	return Cohort{}, false
}

// DeclaredTypes returns every resource type a cohort's rendered tree
// declares: its roster plus the supporting resources the generator adds.
func (c Cohort) DeclaredTypes() []string {
	out := make([]string, 0, len(c.Types)+len(c.Supporting))
	out = append(out, c.Types...)
	out = append(out, c.Supporting...)
	sort.Strings(out)
	return out
}

// FixtureTypes returns the union of every cohort's declared types, sorted.
// This is the universe the union pin (#48's "table == union(estate,
// estates/*)") used to read off the committed .tf files.
func FixtureTypes() []string {
	seen := map[string]bool{}
	for _, c := range all {
		for _, t := range c.DeclaredTypes() {
			seen[t] = true
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// CohortsDeclaring returns the names of the cohorts whose rendered tree
// declares the given type, sorted.
func CohortsDeclaring(resourceType string) []string {
	var out []string
	for _, c := range all {
		for _, t := range c.DeclaredTypes() {
			if t == resourceType {
				out = append(out, c.Name)
				break
			}
		}
	}
	return out
}
