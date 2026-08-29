# AWS Provider Coverage

choudoufu supports one provider today: AWS. This page is the ledger of
how much of the AWS provider live markers cover, layer by layer: what is
proven now, what is generated and waiting on ratification, what needs one
small decision per type, and the small residue that will never map, each
entry with its reason.

The usage-weighted summary comes first, because raw percentages
undersell it. The services estates are actually made of (EC2/VPC, S3,
IAM, Lambda, RDS, DynamoDB, SQS/SNS, EKS/ECS, ELB, Route53, KMS,
CloudWatch) are all admitted or in the pastable-proposal set. The tail
that will never map is disproportionately dead or exotic services, and
every type in it gets a named, one-sentence answer in
`live/LIMITATIONS.md` and in the lint refusal itself.

## The layers at a glance

Every count below is rendered from a committed artifact (issue #139):
`live/rowgen-buckets.json` for the classifier's buckets,
`live/mapping.json` for the taxonomy, `live/cohort-acceptance.json` for
the round trip. Run `go run ./tools/survey-gen -render` after any of
them moves. The prose on this page quotes none of the numbers.

<!-- survey-gen:begin coverage-layers -->
| Layer | Count | What stands between it and support |
| ----- | ----- | ---------------------------------- |
| Round-trip proven against the emulator | 4 of 31 cohorts | Nothing. Applied, state deleted, replanned empty (`live/cohort-acceptance.json`). |
| Admitted (the shipped table) | 1049 types | Nothing at lint. Runtime support varies by type; see the layers below. |
| Pastable proposals (server-assigned 575, client-named 477, composite 152, assembled 33) | 1237 types | A ratification batch: paste, fixture, test. |
| Needs a hand separator | 76 types | One one-character import-separator decision each. |
| Evidence-only | 314 types | An identity-argument name no current evidence source states. |
| Fold-children | 72 types | Nothing of their own; identity is the parent's. |
| Classified in total | 1699 of 1699 provider types | The layers above partition this set. |
| Of those, with no CloudFormation model | 310 cfn-unmodeled, 116 tf-only, 0 deprecated-service, 13 unclassified | Classified from the provider's own import documentation alone, not from the CFN registry. See `live/LIMITATIONS.md`'s exclusion cohorts. |<!-- survey-gen:end coverage-layers -->

## The admitted set

The admission table is the entire subset of resource types live markers can
manage. It holds <!-- survey-gen:begin contract-count -->
1049<!-- survey-gen:end contract-count --> types: <!-- survey-gen:begin contract-types -->
  `aws_accessanalyzer_analyzer`, `aws_accessanalyzer_archive_rule`,
  `aws_acm_certificate`, `aws_acmpca_certificate_authority`,
  `aws_acmpca_certificate_authority_certificate`, `aws_acmpca_policy`,
  `aws_alb`, `aws_alb_listener`, `aws_alb_listener_certificate`,
  `aws_alb_listener_rule`, `aws_alb_target_group`,
  `aws_alb_target_group_attachment`, `aws_amplify_app`,
  `aws_amplify_backend_environment`, `aws_amplify_branch`,
  `aws_amplify_domain_association`, `aws_api_gateway_api_key`,
  `aws_api_gateway_base_path_mapping`, `aws_api_gateway_client_certificate`,
  `aws_api_gateway_documentation_version`, `aws_api_gateway_domain_name`,
  `aws_api_gateway_domain_name_access_association`,
  `aws_api_gateway_gateway_response`, `aws_api_gateway_integration`,
  `aws_api_gateway_integration_response`, `aws_api_gateway_method`,
  `aws_api_gateway_method_response`, `aws_api_gateway_method_settings`,
  `aws_api_gateway_model`, `aws_api_gateway_rest_api`,
  `aws_api_gateway_rest_api_policy`, `aws_api_gateway_stage`,
  `aws_api_gateway_usage_plan`, `aws_api_gateway_usage_plan_key`,
  `aws_api_gateway_vpc_link`, `aws_apigatewayv2_api`,
  `aws_apigatewayv2_domain_name`, `aws_apigatewayv2_stage`,
  `aws_apigatewayv2_vpc_link`, `aws_app_cookie_stickiness_policy`,
  `aws_appautoscaling_policy`, `aws_appautoscaling_target`,
  `aws_appconfig_application`, `aws_appconfig_configuration_profile`,
  `aws_appconfig_deployment`, `aws_appconfig_deployment_strategy`,
  `aws_appconfig_environment`, `aws_appconfig_extension`,
  `aws_appflow_connector_profile`, `aws_appflow_flow`,
  `aws_appintegrations_data_integration`,
  `aws_appintegrations_event_integration`,
  `aws_apprunner_auto_scaling_configuration_version`,
  `aws_apprunner_custom_domain_association`,
  `aws_apprunner_default_auto_scaling_configuration_version`,
  `aws_apprunner_observability_configuration`, `aws_apprunner_service`,
  `aws_apprunner_vpc_connector`, `aws_apprunner_vpc_ingress_connection`,
  `aws_appstream_fleet`, `aws_appstream_fleet_stack_association`,
  `aws_appstream_image_builder`, `aws_appstream_stack`, `aws_appstream_user`,
  `aws_appstream_user_stack_association`, `aws_appsync_api`,
  `aws_appsync_api_cache`, `aws_appsync_channel_namespace`,
  `aws_appsync_domain_name`, `aws_appsync_domain_name_api_association`,
  `aws_appsync_graphql_api`, `aws_arcregionswitch_plan`,
  `aws_arczonalshift_autoshift_observer_notification_status`,
  `aws_arczonalshift_zonal_autoshift_configuration`,
  `aws_athena_data_catalog`, `aws_athena_prepared_statement`,
  `aws_athena_workgroup`, `aws_auditmanager_account_registration`,
  `aws_auditmanager_assessment`, `aws_auditmanager_framework`,
  `aws_autoscaling_group`, `aws_autoscaling_lifecycle_hook`,
  `aws_autoscaling_policy`, `aws_autoscaling_schedule`,
  `aws_autoscaling_traffic_source_attachment`, `aws_backup_framework`,
  `aws_backup_logically_air_gapped_vault`, `aws_backup_plan`,
  `aws_backup_report_plan`, `aws_backup_restore_testing_plan`,
  `aws_backup_restore_testing_selection`, `aws_backup_vault`,
  `aws_batch_compute_environment`, `aws_batch_job_definition`,
  `aws_batch_job_queue`, `aws_batch_scheduling_policy`,
  `aws_bcmdataexports_export`, `aws_bedrock_guardrail`,
  `aws_bedrock_inference_profile`,
  `aws_bedrock_model_invocation_logging_configuration`,
  `aws_bedrockagent_agent`, `aws_bedrockagent_agent_alias`,
  `aws_bedrockagent_flow`, `aws_bedrockagent_knowledge_base`,
  `aws_bedrockagent_prompt`, `aws_bedrockagentcore_agent_runtime`,
  `aws_bedrockagentcore_agent_runtime_endpoint`,
  `aws_bedrockagentcore_api_key_credential_provider`,
  `aws_bedrockagentcore_browser`, `aws_bedrockagentcore_browser_profile`,
  `aws_bedrockagentcore_code_interpreter`, `aws_bedrockagentcore_evaluator`,
  `aws_bedrockagentcore_gateway`, `aws_bedrockagentcore_harness`,
  `aws_bedrockagentcore_memory`,
  `aws_bedrockagentcore_oauth2_credential_provider`,
  `aws_bedrockagentcore_online_evaluation_config`,
  `aws_bedrockagentcore_policy_engine`,
  `aws_bedrockagentcore_resource_policy`,
  `aws_bedrockagentcore_workload_identity`, `aws_billing_view`,
  `aws_budgets_budget`, `aws_budgets_budget_action`, `aws_ce_anomaly_monitor`,
  `aws_ce_anomaly_subscription`, `aws_ce_cost_category`,
  `aws_chatbot_slack_channel_configuration`, `aws_cleanrooms_collaboration`,
  `aws_cleanrooms_configured_table`, `aws_cleanrooms_membership`,
  `aws_cloud9_environment_ec2`, `aws_cloudfront_anycast_ip_list`,
  `aws_cloudfront_cache_policy`, `aws_cloudfront_connection_function`,
  `aws_cloudfront_connection_group`, `aws_cloudfront_distribution`,
  `aws_cloudfront_distribution_tenant`, `aws_cloudfront_function`,
  `aws_cloudfront_key_value_store`, `aws_cloudfront_monitoring_subscription`,
  `aws_cloudfront_multitenant_distribution`,
  `aws_cloudfront_origin_request_policy`,
  `aws_cloudfront_realtime_log_config`,
  `aws_cloudfront_response_headers_policy`, `aws_cloudfront_trust_store`,
  `aws_cloudfront_vpc_origin`, `aws_cloudfrontkeyvaluestore_key`,
  `aws_cloudtrail`, `aws_cloudtrail_event_data_store`,
  `aws_cloudwatch_alarm_mute_rule`, `aws_cloudwatch_composite_alarm`,
  `aws_cloudwatch_contributor_insight_rule`,
  `aws_cloudwatch_contributor_managed_insight_rule`,
  `aws_cloudwatch_dashboard`, `aws_cloudwatch_event_api_destination`,
  `aws_cloudwatch_event_archive`, `aws_cloudwatch_event_bus`,
  `aws_cloudwatch_event_connection`, `aws_cloudwatch_event_endpoint`,
  `aws_cloudwatch_event_permission`, `aws_cloudwatch_event_rule`,
  `aws_cloudwatch_event_target`, `aws_cloudwatch_log_account_policy`,
  `aws_cloudwatch_log_anomaly_detector`, `aws_cloudwatch_log_delivery`,
  `aws_cloudwatch_log_delivery_destination`,
  `aws_cloudwatch_log_delivery_source`, `aws_cloudwatch_log_destination`,
  `aws_cloudwatch_log_group`, `aws_cloudwatch_log_metric_filter`,
  `aws_cloudwatch_log_resource_policy`, `aws_cloudwatch_log_stream`,
  `aws_cloudwatch_log_subscription_filter`, `aws_cloudwatch_log_transformer`,
  `aws_cloudwatch_metric_alarm`, `aws_cloudwatch_metric_stream`,
  `aws_cloudwatch_otel_enrichment`, `aws_codeartifact_domain`,
  `aws_codeartifact_domain_permissions_policy`, `aws_codeartifact_repository`,
  `aws_codeartifact_repository_permissions_policy`, `aws_codebuild_fleet`,
  `aws_codebuild_project`, `aws_codebuild_report_group`,
  `aws_codebuild_webhook`,
  `aws_codecommit_approval_rule_template_association`,
  `aws_codecommit_repository`, `aws_codeconnections_connection`,
  `aws_codedeploy_app`, `aws_codedeploy_deployment_config`,
  `aws_codedeploy_deployment_group`,
  `aws_codegurureviewer_repository_association`, `aws_codepipeline`,
  `aws_codepipeline_custom_action_type`, `aws_codepipeline_webhook`,
  `aws_codestarconnections_connection`,
  `aws_codestarnotifications_notification_rule`, `aws_cognito_identity_pool`,
  `aws_cognito_identity_pool_provider_principal_tag`,
  `aws_cognito_identity_pool_roles_attachment`,
  `aws_cognito_identity_provider`, `aws_cognito_resource_server`,
  `aws_cognito_risk_configuration`, `aws_cognito_user`,
  `aws_cognito_user_group`, `aws_cognito_user_in_group`,
  `aws_cognito_user_pool`, `aws_cognito_user_pool_domain`,
  `aws_cognito_user_pool_ui_customization`,
  `aws_comprehend_document_classifier`, `aws_comprehend_entity_recognizer`,
  `aws_config_aggregate_authorization`, `aws_config_config_rule`,
  `aws_config_configuration_aggregator`, `aws_config_conformance_pack`,
  `aws_config_organization_conformance_pack`,
  `aws_config_remediation_configuration`, `aws_connect_contact_flow`,
  `aws_connect_contact_flow_module`, `aws_connect_hours_of_operation`,
  `aws_connect_instance`, `aws_connect_lambda_function_association`,
  `aws_connect_phone_number`,
  `aws_connect_phone_number_contact_flow_association`, `aws_connect_queue`,
  `aws_connect_quick_connect`, `aws_connect_routing_profile`,
  `aws_connect_security_profile`, `aws_connect_user`,
  `aws_connect_user_hierarchy_group`, `aws_connect_user_hierarchy_structure`,
  `aws_controltower_baseline`, `aws_controltower_control`,
  `aws_controltower_landing_zone`, `aws_cur_report_definition`,
  `aws_customer_gateway`, `aws_datapipeline_pipeline`,
  `aws_datapipeline_pipeline_definition`, `aws_datasync_agent`,
  `aws_datasync_location_azure_blob`, `aws_datasync_location_efs`,
  `aws_datasync_location_fsx_lustre_file_system`,
  `aws_datasync_location_fsx_ontap_file_system`,
  `aws_datasync_location_fsx_openzfs_file_system`,
  `aws_datasync_location_fsx_windows_file_system`,
  `aws_datasync_location_hdfs`, `aws_datasync_location_nfs`,
  `aws_datasync_location_object_storage`, `aws_datasync_location_s3`,
  `aws_datasync_location_smb`, `aws_datasync_task`, `aws_datazone_asset_type`,
  `aws_datazone_domain`, `aws_datazone_environment_blueprint_configuration`,
  `aws_db_event_subscription`, `aws_db_instance`,
  `aws_db_instance_role_association`, `aws_db_option_group`,
  `aws_db_parameter_group`, `aws_db_proxy`,
  `aws_db_proxy_default_target_group`, `aws_db_proxy_endpoint`,
  `aws_db_subnet_group`, `aws_default_network_acl`, `aws_default_route_table`,
  `aws_default_security_group`, `aws_detective_graph`, `aws_detective_member`,
  `aws_devopsguru_event_sources_config`, `aws_devopsguru_resource_collection`,
  `aws_devopsguru_service_integration`,
  `aws_directory_service_conditional_forwarder`,
  `aws_directory_service_region`, `aws_directory_service_trust`,
  `aws_dlm_lifecycle_policy`, `aws_dms_certificate`, `aws_dms_endpoint`,
  `aws_dms_event_subscription`, `aws_dms_replication_config`,
  `aws_dms_replication_instance`, `aws_dms_replication_subnet_group`,
  `aws_dms_replication_task`, `aws_dms_s3_endpoint`, `aws_docdb_cluster`,
  `aws_docdb_cluster_instance`, `aws_docdb_cluster_parameter_group`,
  `aws_docdb_event_subscription`, `aws_docdb_subnet_group`,
  `aws_docdbelastic_cluster`, `aws_dsql_cluster`, `aws_dx_connection`,
  `aws_dx_gateway`, `aws_dx_lag`, `aws_dx_private_virtual_interface`,
  `aws_dx_public_virtual_interface`, `aws_dx_transit_virtual_interface`,
  `aws_dynamodb_global_secondary_index`, `aws_dynamodb_global_table`,
  `aws_dynamodb_kinesis_streaming_destination`,
  `aws_dynamodb_resource_policy`, `aws_dynamodb_table`,
  `aws_ebs_fast_snapshot_restore`, `aws_ebs_snapshot_block_public_access`,
  `aws_ebs_volume`, `aws_ec2_allowed_images_settings`,
  `aws_ec2_capacity_reservation`, `aws_ec2_client_vpn_endpoint`,
  `aws_ec2_client_vpn_route`, `aws_ec2_fleet`, `aws_ec2_host`,
  `aws_ec2_instance_connect_endpoint`, `aws_ec2_local_gateway_route`,
  `aws_ec2_local_gateway_route_table`,
  `aws_ec2_local_gateway_route_table_virtual_interface_group_association`,
  `aws_ec2_local_gateway_route_table_vpc_association`,
  `aws_ec2_managed_prefix_list`, `aws_ec2_managed_prefix_list_entry`,
  `aws_ec2_network_insights_access_scope`,
  `aws_ec2_network_insights_analysis`, `aws_ec2_network_insights_path`,
  `aws_ec2_traffic_mirror_filter`, `aws_ec2_traffic_mirror_session`,
  `aws_ec2_traffic_mirror_target`, `aws_ec2_transit_gateway`,
  `aws_ec2_transit_gateway_connect`, `aws_ec2_transit_gateway_connect_peer`,
  `aws_ec2_transit_gateway_metering_policy`,
  `aws_ec2_transit_gateway_metering_policy_entry`,
  `aws_ec2_transit_gateway_multicast_domain`,
  `aws_ec2_transit_gateway_peering_attachment`,
  `aws_ec2_transit_gateway_policy_table`,
  `aws_ec2_transit_gateway_policy_table_association`,
  `aws_ec2_transit_gateway_route`, `aws_ec2_transit_gateway_route_table`,
  `aws_ec2_transit_gateway_route_table_association`,
  `aws_ec2_transit_gateway_route_table_propagation`,
  `aws_ec2_transit_gateway_vpc_attachment`, `aws_ecr_lifecycle_policy`,
  `aws_ecr_pull_through_cache_rule`, `aws_ecr_pull_time_update_exclusion`,
  `aws_ecr_repository`, `aws_ecr_repository_creation_template`,
  `aws_ecr_repository_policy`, `aws_ecrpublic_repository`,
  `aws_ecrpublic_repository_policy`, `aws_ecs_capacity_provider`,
  `aws_ecs_cluster`, `aws_ecs_cluster_capacity_providers`, `aws_ecs_daemon`,
  `aws_ecs_daemon_task_definition`, `aws_ecs_express_gateway_service`,
  `aws_ecs_service`, `aws_ecs_task_definition`, `aws_ecs_task_set`,
  `aws_efs_access_point`, `aws_efs_backup_policy`, `aws_efs_file_system`,
  `aws_efs_file_system_policy`, `aws_efs_replication_configuration`,
  `aws_egress_only_internet_gateway`, `aws_eip`, `aws_eks_access_entry`,
  `aws_eks_access_policy_association`, `aws_eks_addon`, `aws_eks_capability`,
  `aws_eks_cluster`, `aws_eks_fargate_profile`, `aws_eks_node_group`,
  `aws_eks_pod_identity_association`, `aws_elastic_beanstalk_application`,
  `aws_elastic_beanstalk_environment`, `aws_elasticache_cluster`,
  `aws_elasticache_parameter_group`, `aws_elasticache_replication_group`,
  `aws_elasticache_serverless_cache`, `aws_elasticache_subnet_group`,
  `aws_elasticache_user`, `aws_elasticache_user_group`,
  `aws_elasticache_user_group_association`, `aws_elasticsearch_domain`,
  `aws_elb`, `aws_emr_cluster`, `aws_emr_security_configuration`,
  `aws_emr_studio`, `aws_emr_studio_session_mapping`,
  `aws_emrcontainers_virtual_cluster`, `aws_emrserverless_application`,
  `aws_evidently_project`, `aws_evidently_segment`,
  `aws_fis_experiment_template`, `aws_fis_target_account_configuration`,
  `aws_flow_log`, `aws_fms_resource_set`,
  `aws_fsx_data_repository_association`, `aws_fsx_lustre_file_system`,
  `aws_fsx_ontap_file_system`, `aws_fsx_ontap_storage_virtual_machine`,
  `aws_fsx_ontap_volume`, `aws_fsx_openzfs_file_system`,
  `aws_fsx_openzfs_snapshot`, `aws_fsx_openzfs_volume`,
  `aws_fsx_s3_access_point_attachment`, `aws_fsx_windows_file_system`,
  `aws_gamelift_alias`, `aws_gamelift_build`, `aws_gamelift_fleet`,
  `aws_gamelift_game_server_group`, `aws_gamelift_script`,
  `aws_globalaccelerator_accelerator`,
  `aws_globalaccelerator_cross_account_attachment`, `aws_glue_catalog`,
  `aws_glue_catalog_database`, `aws_glue_catalog_table`,
  `aws_glue_catalog_table_optimizer`, `aws_glue_classifier`,
  `aws_glue_connection`, `aws_glue_crawler`,
  `aws_glue_data_catalog_encryption_settings`,
  `aws_glue_data_quality_ruleset`, `aws_glue_dev_endpoint`, `aws_glue_job`,
  `aws_glue_ml_transform`, `aws_glue_registry`, `aws_glue_resource_policy`,
  `aws_glue_schema`, `aws_glue_security_configuration`, `aws_glue_trigger`,
  `aws_glue_user_defined_function`, `aws_glue_workflow`,
  `aws_grafana_workspace`, `aws_grafana_workspace_saml_configuration`,
  `aws_guardduty_detector`, `aws_guardduty_filter`, `aws_guardduty_ipset`,
  `aws_guardduty_malware_protection_plan`, `aws_guardduty_member`,
  `aws_guardduty_organization_admin_account`,
  `aws_guardduty_organization_configuration`,
  `aws_guardduty_publishing_destination`, `aws_guardduty_threatintelset`,
  `aws_iam_account_alias`, `aws_iam_account_password_policy`, `aws_iam_group`,
  `aws_iam_group_policy`, `aws_iam_group_policy_attachment`,
  `aws_iam_instance_profile`, `aws_iam_openid_connect_provider`,
  `aws_iam_policy`, `aws_iam_role`, `aws_iam_role_policies_exclusive`,
  `aws_iam_role_policy`, `aws_iam_role_policy_attachment`,
  `aws_iam_role_policy_attachments_exclusive`, `aws_iam_saml_provider`,
  `aws_iam_server_certificate`, `aws_iam_service_linked_role`, `aws_iam_user`,
  `aws_iam_user_group_membership`, `aws_iam_user_login_profile`,
  `aws_iam_user_policy`, `aws_iam_user_policy_attachment`,
  `aws_iam_virtual_mfa_device`, `aws_imagebuilder_component`,
  `aws_imagebuilder_container_recipe`,
  `aws_imagebuilder_distribution_configuration`, `aws_imagebuilder_image`,
  `aws_imagebuilder_image_pipeline`, `aws_imagebuilder_image_recipe`,
  `aws_imagebuilder_infrastructure_configuration`,
  `aws_imagebuilder_lifecycle_policy`, `aws_imagebuilder_workflow`,
  `aws_inspector2_delegated_admin_account`, `aws_inspector2_filter`,
  `aws_inspector2_member_association`, `aws_inspector_assessment_template`,
  `aws_inspector_resource_group`, `aws_instance`, `aws_internet_gateway`,
  `aws_internet_gateway_attachment`, `aws_internetmonitor_monitor`,
  `aws_invoicing_invoice_unit`, `aws_iot_authorizer`, `aws_iot_billing_group`,
  `aws_iot_domain_configuration`, `aws_iot_event_configurations`,
  `aws_iot_policy`, `aws_iot_provisioning_template`, `aws_iot_role_alias`,
  `aws_iot_thing`, `aws_iot_thing_group`, `aws_iot_thing_group_membership`,
  `aws_iot_thing_type`, `aws_iot_topic_rule`, `aws_ivs_channel`,
  `aws_ivs_recording_configuration`, `aws_ivschat_logging_configuration`,
  `aws_ivschat_room`, `aws_kendra_data_source`, `aws_kendra_faq`,
  `aws_kendra_index`, `aws_key_pair`, `aws_keyspaces_keyspace`,
  `aws_keyspaces_table`, `aws_kinesis_account_settings`,
  `aws_kinesis_analytics_application`, `aws_kinesis_firehose_delivery_stream`,
  `aws_kinesis_resource_policy`, `aws_kinesis_stream`,
  `aws_kinesis_stream_consumer`, `aws_kinesisanalyticsv2_application`,
  `aws_kms_alias`, `aws_kms_external_key`, `aws_kms_key`,
  `aws_kms_replica_key`, `aws_lakeformation_data_cells_filter`,
  `aws_lakeformation_lf_tag`, `aws_lakeformation_lf_tag_expression`,
  `aws_lambda_capacity_provider`, `aws_lambda_code_signing_config`,
  `aws_lambda_event_source_mapping`, `aws_lambda_function`,
  `aws_lambda_function_event_invoke_config`,
  `aws_lambda_function_scaling_config`, `aws_lambda_layer_version_permission`,
  `aws_lambda_permission`, `aws_lambda_provisioned_concurrency_config`,
  `aws_launch_configuration`, `aws_launch_template`, `aws_lb`,
  `aws_lb_listener`, `aws_lb_listener_certificate`, `aws_lb_listener_rule`,
  `aws_lb_target_group`, `aws_lb_target_group_attachment`,
  `aws_lb_trust_store`, `aws_lb_trust_store_revocation`,
  `aws_lexv2models_bot`, `aws_lexv2models_bot_locale`,
  `aws_licensemanager_association`, `aws_lightsail_bucket`,
  `aws_lightsail_bucket_resource_access`, `aws_lightsail_certificate`,
  `aws_lightsail_container_service`, `aws_lightsail_database`,
  `aws_lightsail_disk`, `aws_lightsail_distribution`,
  `aws_lightsail_domain_entry`, `aws_lightsail_instance`, `aws_lightsail_lb`,
  `aws_lightsail_lb_attachment`, `aws_lightsail_lb_certificate`,
  `aws_lightsail_lb_certificate_attachment`, `aws_lightsail_static_ip`,
  `aws_location_geofence_collection`, `aws_location_map`,
  `aws_location_place_index`, `aws_location_route_calculator`,
  `aws_location_tracker`, `aws_location_tracker_association`,
  `aws_m2_application`, `aws_m2_environment`,
  `aws_macie2_classification_export_configuration`,
  `aws_macie2_classification_job`, `aws_macie2_custom_data_identifier`,
  `aws_macie2_findings_filter`, `aws_macie2_member`,
  `aws_macie2_organization_admin_account`, `aws_mailmanager_ingress_point`,
  `aws_mailmanager_rule_set`, `aws_mailmanager_traffic_policy`,
  `aws_media_package_channel`, `aws_media_packagev2_channel_group`,
  `aws_medialive_multiplex`, `aws_memorydb_acl`, `aws_memorydb_cluster`,
  `aws_memorydb_multi_region_cluster`, `aws_memorydb_parameter_group`,
  `aws_memorydb_subnet_group`, `aws_memorydb_user`, `aws_mq_broker`,
  `aws_mq_configuration`, `aws_msk_cluster`, `aws_msk_cluster_policy`,
  `aws_msk_replicator`, `aws_msk_scram_secret_association`,
  `aws_msk_serverless_cluster`, `aws_msk_single_scram_secret_association`,
  `aws_msk_topic`, `aws_msk_vpc_connection`, `aws_mskconnect_connector`,
  `aws_mskconnect_custom_plugin`, `aws_mskconnect_worker_configuration`,
  `aws_mwaa_environment`, `aws_nat_gateway`,
  `aws_nat_gateway_eip_association`, `aws_neptune_cluster`,
  `aws_neptune_cluster_instance`, `aws_neptune_cluster_parameter_group`,
  `aws_neptune_parameter_group`, `aws_neptune_subnet_group`,
  `aws_network_acl`, `aws_network_acl_rule`, `aws_network_interface`,
  `aws_network_interface_sg_attachment`, `aws_networkfirewall_firewall`,
  `aws_networkfirewall_firewall_policy`,
  `aws_networkfirewall_logging_configuration`,
  `aws_networkfirewall_rule_group`,
  `aws_networkfirewall_tls_inspection_configuration`,
  `aws_networkfirewall_vpc_endpoint_association`,
  `aws_networkflowmonitor_monitor`,
  `aws_networkmanager_attachment_routing_policy_label`,
  `aws_networkmanager_connect_attachment`, `aws_networkmanager_connect_peer`,
  `aws_networkmanager_core_network`,
  `aws_networkmanager_core_network_policy_attachment`,
  `aws_networkmanager_customer_gateway_association`,
  `aws_networkmanager_device`, `aws_networkmanager_dx_gateway_attachment`,
  `aws_networkmanager_global_network`, `aws_networkmanager_link`,
  `aws_networkmanager_link_association`,
  `aws_networkmanager_prefix_list_association`, `aws_networkmanager_site`,
  `aws_networkmanager_site_to_site_vpn_attachment`,
  `aws_networkmanager_transit_gateway_peering`,
  `aws_networkmanager_transit_gateway_registration`,
  `aws_networkmanager_transit_gateway_route_table_attachment`,
  `aws_networkmanager_vpc_attachment`,
  `aws_notifications_channel_association`,
  `aws_notifications_managed_notification_account_contact_association`,
  `aws_notifications_managed_notification_additional_channel_association`,
  `aws_notifications_notification_configuration`,
  `aws_notifications_notification_hub`,
  `aws_notifications_organizational_unit_association`,
  `aws_notificationscontacts_email_contact`, `aws_oam_link`, `aws_oam_sink`,
  `aws_observabilityadmin_s3_table_integration`,
  `aws_observabilityadmin_telemetry_enrichment`,
  `aws_observabilityadmin_telemetry_evaluation`,
  `aws_observabilityadmin_telemetry_evaluation_for_organization`,
  `aws_observabilityadmin_telemetry_pipeline`,
  `aws_odb_cloud_autonomous_vm_cluster`,
  `aws_odb_cloud_exadata_infrastructure`, `aws_odb_cloud_vm_cluster`,
  `aws_odb_network`, `aws_odb_network_peering_connection`,
  `aws_opensearch_authorize_vpc_endpoint_access`, `aws_opensearch_domain`,
  `aws_opensearch_package_association`,
  `aws_opensearchserverless_access_policy`,
  `aws_opensearchserverless_collection`,
  `aws_opensearchserverless_collection_group`,
  `aws_opensearchserverless_lifecycle_policy`,
  `aws_opensearchserverless_security_policy`, `aws_organizations_account`,
  `aws_organizations_delegated_administrator`,
  `aws_organizations_organizational_unit`, `aws_organizations_policy`,
  `aws_organizations_policy_attachment`, `aws_organizations_resource_policy`,
  `aws_osis_pipeline`, `aws_paymentcryptography_key`,
  `aws_paymentcryptography_key_alias`,
  `aws_pinpointsmsvoicev2_configuration_set`,
  `aws_pinpointsmsvoicev2_event_destination`,
  `aws_pinpointsmsvoicev2_opt_out_list`,
  `aws_pinpointsmsvoicev2_phone_number`, `aws_pinpointsmsvoicev2_pool`,
  `aws_pinpointsmsvoicev2_resource_policy`,
  `aws_pinpointsmsvoicev2_sender_id`, `aws_pipes_pipe`, `aws_placement_group`,
  `aws_prometheus_alert_manager_definition`,
  `aws_prometheus_anomaly_detector`,
  `aws_prometheus_query_logging_configuration`,
  `aws_prometheus_rule_group_namespace`, `aws_prometheus_scraper`,
  `aws_prometheus_scraper_logging_configuration`, `aws_prometheus_workspace`,
  `aws_qbusiness_application`, `aws_qldb_ledger`, `aws_quicksight_analysis`,
  `aws_quicksight_custom_permissions`, `aws_quicksight_dashboard`,
  `aws_quicksight_data_set`, `aws_quicksight_data_source`,
  `aws_quicksight_refresh_schedule`, `aws_quicksight_template`,
  `aws_quicksight_theme`, `aws_quicksight_vpc_connection`,
  `aws_ram_permission`, `aws_ram_principal_association`,
  `aws_ram_resource_association`, `aws_ram_resource_share`, `aws_rbin_rule`,
  `aws_rds_cluster`, `aws_rds_cluster_endpoint`, `aws_rds_cluster_instance`,
  `aws_rds_cluster_parameter_group`, `aws_rds_cluster_role_association`,
  `aws_rds_custom_db_engine_version`, `aws_rds_global_cluster`,
  `aws_rds_integration`, `aws_rds_shard_group`, `aws_redshift_cluster`,
  `aws_redshift_endpoint_access`, `aws_redshift_endpoint_authorization`,
  `aws_redshift_logging`, `aws_redshift_parameter_group`,
  `aws_redshift_snapshot_schedule`, `aws_redshift_subnet_group`,
  `aws_redshiftserverless_custom_domain_association`,
  `aws_redshiftserverless_namespace`, `aws_redshiftserverless_workgroup`,
  `aws_rekognition_collection`, `aws_rekognition_project`,
  `aws_rekognition_stream_processor`, `aws_resiliencehub_resiliency_policy`,
  `aws_resiliencehubv2_policy`, `aws_resiliencehubv2_service`,
  `aws_resiliencehubv2_system`, `aws_resourceexplorer2_index`,
  `aws_resourceexplorer2_view`, `aws_resourcegroups_group`,
  `aws_resourcegroups_resource`, `aws_rolesanywhere_profile`,
  `aws_rolesanywhere_trust_anchor`, `aws_route`,
  `aws_route53_cidr_collection`, `aws_route53_cidr_location`,
  `aws_route53_health_check`, `aws_route53_hosted_zone_dnssec`,
  `aws_route53_key_signing_key`, `aws_route53_record`,
  `aws_route53_resolver_endpoint`,
  `aws_route53_resolver_firewall_domain_list`,
  `aws_route53_resolver_firewall_rule`,
  `aws_route53_resolver_firewall_rule_group`,
  `aws_route53_resolver_firewall_rule_group_association`,
  `aws_route53_resolver_query_log_config`, `aws_route53_resolver_rule`,
  `aws_route53_vpc_association_authorization`, `aws_route53_zone`,
  `aws_route53_zone_association`, `aws_route53profiles_association`,
  `aws_route53profiles_profile`, `aws_route53recoverycontrolconfig_cluster`,
  `aws_route53recoverycontrolconfig_control_panel`,
  `aws_route53recoverycontrolconfig_safety_rule`,
  `aws_route53recoveryreadiness_cell`,
  `aws_route53recoveryreadiness_readiness_check`,
  `aws_route53recoveryreadiness_recovery_group`,
  `aws_route53recoveryreadiness_resource_set`, `aws_route_table`,
  `aws_route_table_association`, `aws_rum_app_monitor`,
  `aws_s3_account_public_access_block`, `aws_s3_bucket`,
  `aws_s3_bucket_accelerate_configuration`,
  `aws_s3_bucket_analytics_configuration`,
  `aws_s3_bucket_intelligent_tiering_configuration`,
  `aws_s3_bucket_inventory`, `aws_s3_bucket_lifecycle_configuration`,
  `aws_s3_bucket_metric`, `aws_s3_bucket_object_lock_configuration`,
  `aws_s3_bucket_policy`, `aws_s3_bucket_public_access_block`,
  `aws_s3_bucket_replication_configuration`,
  `aws_s3_bucket_request_payment_configuration`,
  `aws_s3_bucket_server_side_encryption_configuration`,
  `aws_s3_bucket_versioning`, `aws_s3_directory_bucket`,
  `aws_s3control_bucket`, `aws_s3control_bucket_lifecycle_configuration`,
  `aws_s3control_bucket_policy`, `aws_s3control_multi_region_access_point`,
  `aws_s3control_object_lambda_access_point`,
  `aws_s3control_object_lambda_access_point_policy`,
  `aws_s3control_storage_lens_configuration`, `aws_s3files_access_point`,
  `aws_s3files_file_system`, `aws_s3files_file_system_policy`,
  `aws_s3tables_table_bucket`, `aws_s3tables_table_bucket_policy`,
  `aws_s3vectors_index`, `aws_s3vectors_vector_bucket`,
  `aws_s3vectors_vector_bucket_policy`, `aws_sagemaker_algorithm`,
  `aws_sagemaker_app`, `aws_sagemaker_app_image_config`,
  `aws_sagemaker_code_repository`,
  `aws_sagemaker_data_quality_job_definition`, `aws_sagemaker_device_fleet`,
  `aws_sagemaker_domain`, `aws_sagemaker_endpoint`,
  `aws_sagemaker_endpoint_configuration`, `aws_sagemaker_feature_group`,
  `aws_sagemaker_hub`, `aws_sagemaker_hub_content_reference`,
  `aws_sagemaker_image`, `aws_sagemaker_mlflow_app`,
  `aws_sagemaker_mlflow_tracking_server`, `aws_sagemaker_model`,
  `aws_sagemaker_model_card`, `aws_sagemaker_model_package_group`,
  `aws_sagemaker_model_package_group_policy`,
  `aws_sagemaker_monitoring_schedule`, `aws_sagemaker_notebook_instance`,
  `aws_sagemaker_notebook_instance_lifecycle_configuration`,
  `aws_sagemaker_pipeline`, `aws_sagemaker_project`,
  `aws_sagemaker_servicecatalog_portfolio_status`, `aws_sagemaker_space`,
  `aws_sagemaker_studio_lifecycle_config`, `aws_sagemaker_user_profile`,
  `aws_sagemaker_workteam`, `aws_scheduler_schedule`,
  `aws_scheduler_schedule_group`, `aws_schemas_discoverer`,
  `aws_schemas_schema`, `aws_secretsmanager_secret`,
  `aws_secretsmanager_secret_policy`, `aws_secretsmanager_secret_rotation`,
  `aws_secretsmanager_tag`, `aws_security_group`, `aws_security_group_rule`,
  `aws_securityhub_account_v2`, `aws_securityhub_aggregator_v2`,
  `aws_securityhub_automation_rule`, `aws_securityhub_automation_rule_v2`,
  `aws_securityhub_configuration_policy_association`,
  `aws_securityhub_connector_v2`, `aws_securityhub_member`,
  `aws_securityhub_organization_admin_account`,
  `aws_securityhub_standards_control`,
  `aws_securityhub_standards_control_association`,
  `aws_securitylake_data_lake`, `aws_securitylake_subscriber`,
  `aws_service_discovery_http_namespace`, `aws_service_discovery_instance`,
  `aws_service_discovery_private_dns_namespace`,
  `aws_service_discovery_public_dns_namespace`,
  `aws_service_discovery_service`,
  `aws_servicecatalog_budget_resource_association`,
  `aws_servicecatalog_portfolio`, `aws_servicecatalog_portfolio_share`,
  `aws_servicecatalog_principal_portfolio_association`,
  `aws_servicecatalog_product`,
  `aws_servicecatalog_product_portfolio_association`,
  `aws_servicecatalog_provisioned_product`,
  `aws_servicecatalog_tag_option_resource_association`,
  `aws_servicecatalogappregistry_application`,
  `aws_servicecatalogappregistry_attribute_group`,
  `aws_servicecatalogappregistry_attribute_group_association`,
  `aws_servicequotas_auto_management`, `aws_servicequotas_service_quota`,
  `aws_ses_identity_policy`, `aws_ses_receipt_filter`, `aws_ses_receipt_rule`,
  `aws_ses_receipt_rule_set`, `aws_ses_template`,
  `aws_sesv2_account_vdm_attributes`, `aws_sesv2_configuration_set`,
  `aws_sesv2_contact_list`, `aws_sesv2_dedicated_ip_pool`,
  `aws_sesv2_email_identity`, `aws_sesv2_email_identity_policy`,
  `aws_sesv2_tenant`, `aws_sesv2_tenant_resource_association`,
  `aws_sfn_activity`, `aws_sfn_state_machine`, `aws_shield_protection`,
  `aws_shield_protection_group`,
  `aws_shield_protection_health_check_association`,
  `aws_signer_signing_profile_permission`, `aws_sns_topic`,
  `aws_sns_topic_policy`, `aws_spot_datafeed_subscription`,
  `aws_spot_fleet_request`, `aws_sqs_queue`, `aws_sqs_queue_policy`,
  `aws_ssm_association`, `aws_ssm_maintenance_window`,
  `aws_ssm_maintenance_window_target`, `aws_ssm_parameter`,
  `aws_ssm_patch_baseline`, `aws_ssm_patch_group`,
  `aws_ssm_resource_data_sync`, `aws_ssm_service_setting`,
  `aws_ssmcontacts_contact`, `aws_ssmcontacts_rotation`,
  `aws_ssmincidents_replication_set`, `aws_ssmincidents_response_plan`,
  `aws_ssmquicksetup_configuration_manager`,
  `aws_ssoadmin_account_assignment`, `aws_ssoadmin_application`,
  `aws_ssoadmin_application_assignment`,
  `aws_ssoadmin_customer_managed_policy_attachments_exclusive`,
  `aws_ssoadmin_instance_access_control_attributes`,
  `aws_ssoadmin_managed_policy_attachment`,
  `aws_ssoadmin_managed_policy_attachments_exclusive`,
  `aws_ssoadmin_permission_set`, `aws_ssoadmin_permission_set_inline_policy`,
  `aws_ssoadmin_permissions_boundary_attachment`, `aws_ssoadmin_region`,
  `aws_storagegateway_tape_pool`, `aws_subnet`, `aws_synthetics_canary`,
  `aws_synthetics_group`, `aws_synthetics_group_association`,
  `aws_timestreaminfluxdb_db_cluster`, `aws_timestreaminfluxdb_db_instance`,
  `aws_timestreamquery_scheduled_query`, `aws_timestreamwrite_database`,
  `aws_timestreamwrite_table`, `aws_transfer_access`,
  `aws_transfer_agreement`, `aws_transfer_certificate`,
  `aws_transfer_connector`, `aws_transfer_profile`, `aws_transfer_server`,
  `aws_transfer_user`, `aws_transfer_web_app`,
  `aws_transfer_web_app_customization`, `aws_transfer_workflow`,
  `aws_verifiedaccess_endpoint`, `aws_verifiedaccess_group`,
  `aws_verifiedaccess_instance`,
  `aws_verifiedaccess_instance_logging_configuration`,
  `aws_verifiedaccess_instance_trust_provider_attachment`,
  `aws_verifiedaccess_trust_provider`, `aws_verifiedpermissions_policy_store`,
  `aws_volume_attachment`, `aws_vpc`, `aws_vpc_block_public_access_exclusion`,
  `aws_vpc_block_public_access_options`, `aws_vpc_dhcp_options`,
  `aws_vpc_dhcp_options_association`, `aws_vpc_encryption_control`,
  `aws_vpc_endpoint`, `aws_vpc_endpoint_policy`,
  `aws_vpc_endpoint_private_dns`, `aws_vpc_endpoint_route_table_association`,
  `aws_vpc_endpoint_security_group_association`, `aws_vpc_endpoint_service`,
  `aws_vpc_endpoint_subnet_association`, `aws_vpc_ipam`, `aws_vpc_ipam_pool`,
  `aws_vpc_ipam_pool_cidr`, `aws_vpc_ipam_pool_cidr_allocation`,
  `aws_vpc_ipam_resource_discovery`,
  `aws_vpc_ipam_resource_discovery_association`, `aws_vpc_ipam_scope`,
  `aws_vpc_peering_connection`, `aws_vpc_route_server`,
  `aws_vpc_route_server_endpoint`, `aws_vpc_route_server_peer`,
  `aws_vpc_route_server_propagation`, `aws_vpc_route_server_vpc_association`,
  `aws_vpc_security_group_egress_rule`, `aws_vpc_security_group_ingress_rule`,
  `aws_vpc_security_group_rules_exclusive`,
  `aws_vpc_security_group_vpc_association`,
  `aws_vpclattice_access_log_subscription`, `aws_vpclattice_auth_policy`,
  `aws_vpclattice_domain_verification`, `aws_vpclattice_listener`,
  `aws_vpclattice_listener_rule`, `aws_vpclattice_resource_configuration`,
  `aws_vpclattice_resource_gateway`, `aws_vpclattice_resource_policy`,
  `aws_vpclattice_service`, `aws_vpclattice_service_network`,
  `aws_vpclattice_service_network_resource_association`,
  `aws_vpclattice_service_network_service_association`,
  `aws_vpclattice_service_network_vpc_association`,
  `aws_vpclattice_target_group`, `aws_vpn_concentrator`, `aws_vpn_connection`,
  `aws_vpn_gateway`, `aws_waf_rule`, `aws_waf_web_acl`,
  `aws_wafregional_rate_based_rule`, `aws_wafregional_rule`,
  `aws_wafregional_web_acl`, `aws_wafregional_web_acl_association`,
  `aws_wafv2_ip_set`, `aws_wafv2_regex_pattern_set`, `aws_wafv2_rule_group`,
  `aws_wafv2_web_acl`, `aws_wafv2_web_acl_association`,
  `aws_wafv2_web_acl_logging_configuration`, `aws_wafv2_web_acl_rule`,
  `aws_workmail_domain`, `aws_workspaces_connection_alias`,
  `aws_workspaces_ip_group`, `aws_workspaces_pool`,
  `aws_workspaces_workspace`, `aws_workspacesweb_browser_settings`,
  `aws_workspacesweb_browser_settings_association`,
  `aws_workspacesweb_data_protection_settings`,
  `aws_workspacesweb_data_protection_settings_association`,
  `aws_workspacesweb_identity_provider`,
  `aws_workspacesweb_ip_access_settings`,
  `aws_workspacesweb_ip_access_settings_association`,
  `aws_workspacesweb_network_settings`,
  `aws_workspacesweb_network_settings_association`,
  `aws_workspacesweb_portal`, `aws_workspacesweb_session_logger`,
  `aws_workspacesweb_session_logger_association`,
  `aws_workspacesweb_trust_store`,
  `aws_workspacesweb_trust_store_association`,
  `aws_workspacesweb_user_access_logging_settings`,
  `aws_workspacesweb_user_access_logging_settings_association`,
  `aws_workspacesweb_user_settings`,
  `aws_workspacesweb_user_settings_association`, `aws_xray_encryption_config`,
  `aws_xray_group`, `aws_xray_resource_policy`, `aws_xray_sampling_rule`,
  `aws_xray_trace_segment_destination`, `kubernetes_cluster_role_binding`,
  `kubernetes_config_map`, `kubernetes_namespace`, `kubernetes_storage_class`,
  `local_file`, `local_sensitive_file`, `null_resource`, `random_bytes`,
  `random_id`, `random_integer`, `random_password`, `random_pet`,
  `random_shuffle`, `random_string`, `random_uuid`, `random_uuid4`,
  `random_uuid7`, `terraform_data`, `time_offset`, `time_rotating`,
  `time_sleep`, `time_static`, `tls_cert_request`, `tls_locally_signed_cert`,
  `tls_private_key`, and `tls_self_signed_cert`<!-- survey-gen:end contract-types -->

## Round-trip proven

The strongest layer is measured by `live/cohort-acceptance.json`: apply a
generated cohort estate against the floci emulator with stock terraform,
delete the state file, `live-plan` from markers, and require an empty
plan. A cohort that passes has demonstrated the whole product claim for
its types. The artifact is a ratchet, so a recorded pass that stops
passing fails the tier. The table above quotes its totals, and the
artifact carries the per-cohort verdicts with the phase each failure
died in.

## Pastable proposals

The classifier puts most mapped types into a bucket with printed registry
evidence: server-assigned identifiers, client-named types, and composites
whose separator the documentation states. These wait only on ratification
batches (paste, fixture, test). This is real generated coverage that has
not passed the human gate yet, by design. Nothing here lands without a
fixture and a test.

## PROPOSE: automatic high-confidence proposals (issue #65)

The weekly admission-pipeline PR (`.github/workflows/admission-pipeline.yml`,
`tools/admission-pipeline`) carries a PROPOSE stage on top of the ordinary
generate-and-batch flow above: `go run ./tools/row-gen -propose` prints
ready-to-paste `admittedTypesV0` and `DefaultTable` entries for logical
types whose classification rule has never once needed a human correction.

A rule class, meaning a bucket (server-assigned, client-named or composite)
plus the exact evidence rule that produced it, qualifies only when both
hold:

- Every one of its historical instances, re-run today against
  `internal/live/identity.DefaultTable`, reproduces byte-for-byte what a
  human independently ratified (a 100% match), over at least five
  instances, enough that the streak is not two coincidences. `live/rowgen-
  convergence.json`'s `types[]` is where that comparison already lives.
  PROPOSE only regroups it by rule instead of by admitted type.
- The candidate is not recorded in `tools/row-gen/rejected.json`, the ledger
  of types a batch looked at by name and declined: a second, independent,
  deliberately over-inclusive check, because a rejected type is invisible to
  the first measurement (it was never admitted, so it never enters that
  comparison at all). The ledger and the admission table are disjoint by
  test, so a type cannot be both.

PROPOSE's printed report carries the full rule-class ledger every run,
qualifying or not, so that state is never hidden. See the output of `go run
./tools/row-gen -propose`, or the `## PROPOSE` section of any
admission-pipeline PR. That report's own summary line is the current figure,
and it is the one to quote rather than any number written here.

**The spot-check contract.** Approving a PROPOSE-emitted entry is not
re-deriving the classification. It is, per proposed type:

1. Opening the provider's documentation for the type (the Import
   section, or the Identity Schema block where the entry says so) and
   confirming the pasted argument or attribute is what that section
   documents.
2. Confirming the type creates or exports no credential material (the
   standing exclusion `aws_iam_access_key` and `aws_iot_certificate` are
   held to). If it does, record the rejection by name in
   `tools/row-gen/rejected.json` with a `reason`, so PROPOSE never offers it
   again. Not in `table.go` or `admission.go`: both are generated in full
   (issue #96) and the next `-emit` run overwrites anything written there.
3. Pasting the two printed blocks unedited.
4. Building the cohort estate, running the suites, and getting a floci
   probe before merging, the same as any hand-ratified batch. PROPOSE
   shortens the classification decision, and the verification that
   follows one is unchanged.

What is being trusted: that a classification rule with a spotless record
over its past instances will also be right on the next one. That is an
inductive claim about the rule, evidenced by a stated count, not a proof
about the specific type. PROPOSE never claims floci or live-account
proof for anything it emits.

## Behind small, known hand-work

Two buckets in the table need one bounded decision per type: a composite
whose one-character import separator no evidence source states, and an
identity argument whose name no current source confirms. Each decision
is a line in a ratification batch. Every extractor improvement that
recovers a class shrinks these buckets, and the ledger of what still
resists extraction is `tools/row-gen/annotations.json`, where every
entry names what a fuller extraction would have to capture to retire it.

Issue #427 re-derived the needs-hand-separator bucket's current
membership (76, by `live/rowgen-buckets.json`'s own count) rather than
trusting the figure its own plan document cited, and found every one of
the 76 already had, or now has, a disposition: 49 hand-ratified already
(`tools/row-gen/annotations.json`), 6 already vetoed with full evidence,
1 already admitted through the record-located mechanism instead of a
table row (no separator needed at all), and 21 that carried only bare
provenance until this sweep read the provider's own doc cache and the
registry evidence for each and recorded it in `tools/row-gen/rejected.json`
- 14 with a separator and worked example a ratification batch can paste
directly, 6 with no worked example anywhere in the doc (the
`aws_acmpca_permission` precedent). `tools/row-gen/separator-evidence.json`
is the review index over all 76; it admits nothing itself.

## The unmapped tail is a taxonomy, not a backlog

Earlier versions of this page described a 900-type unmapped set that was
mostly a naming problem (Terraform files types under `vpc_`,
`cloudwatch_` and `db_` while CloudFormation files them under `EC2`,
`Logs` and `RDS`). That work landed as service-scoped matching with
alias families and a false-positive guard. What remains unmapped is
classified and counted in the table above, and every type in it gets a
generated, named entry in `live/LIMITATIONS.md`'s exclusion cohorts:
types CloudFormation does not model (such as `aws_s3_object`),
Terraform-only types whose identity is their parent's, deprecated
services, and a small unclassified residue. A type that is not covered
is always refused with a stated reason.

## Other providers

AWS is the only provider today. Azure and Google Cloud are coming, and
appear greyed out in the docs navigation until they land.
