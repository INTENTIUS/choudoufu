---
title: "Resource tier lookup"
weight: 1
---

# Resource tier lookup

"Every type stock supports is admitted" is the type-parity promise. It says
nothing about how an admitted type's identity survives the loss of anything -
a record store, a state file, the tool itself. "100% coverage for AWS" is a
claim people will eventually make about this fork, and this page is the
answer to what that claim actually covers: every one of the provider's
resource types, assigned exactly one of four readiness tiers, by what
recovers its identity and at what cost when the strongest recovery path is
gone.

"100% coverage" has to mean "every type is classified," not "every type is
marker-carried." Roughly half the provider's resource types have no tag
surface at all, so no scheme that requires every type to reach the strongest
tier can describe AWS honestly. The full definition, in the vocabulary this
page uses, is
[`rfc/20260828-readiness-tiers.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rfc/20260828-readiness-tiers.md).

## The four tiers

### Marker-carried

Every taggable type: the schema carries a settable top-level `tags`
argument. The tag on the object is both its identity - which configuration
address it binds to - and the governance surface an IAM condition can name.
This is the one tier where losing the record store is not a structural
loss: a lost record is rebuilt from tags where tags exist, using nothing but
the live cloud - no record, no state, no memory of a prior run.

### Declaration-carried

Untaggable types whose identity is fully supplied by the configuration
itself, or composed from a parent's live identity plus configuration data -
the classic case is a client-assigned name, or an attachment named by the
two things it attaches. No marker is ever written for a declaration-carried
instance, because there is no `tags` argument to write one into. Losing the
record store is recoverable by recomputing the same formula against the
current configuration and the parent's current identity, though an
instance derived from a parent's identity is only as recoverable as that
parent is.

### Record-carried

Untaggable *and* server-minted: the provider mints this type's identity at
create time and the type carries no `tags` argument, so every instance
would need marker discovery to be found again and there is nowhere to write
the marker. Where the record-located mechanism already reaches a type, a
declared `record_store` holds its identity and recovers it. For the rest,
losing the record loses the object, the same way losing a stock state file
loses it under plain OpenTofu.

### Excluded by design

Two types today, ruled out ahead of whatever tier their own schema would
otherwise assign: admitting them would force this fork to persist plaintext
credential material it can never read back and verify again, independent of
how recoverable the identity itself is. No record, located or backed, is
ever written for an excluded-by-design type, so there is nothing to lose
because nothing is ever kept.

## Coverage today

Every tier crossed with every status, tallied from `live/readiness.json`'s
own per-type rows. `in-contract` is the only status that means "usable
today" - every other one is a form of not yet, and the lookup table below
says why for each type that carries it.

<!-- readiness-gen:begin readiness-tiers -->
| Tier | in-contract | pending-ratification | needs-separator | needs-evidence | pending-mechanism | excluded | Total |
|---|---|---|---|---|---|---|---|
| marker-carried | 682 | 161 | 1 | 2 | 0 | 0 | 846 |
| declaration-carried | 341 | 37 | 0 | 1 | 0 | 0 | 379 |
| record-carried | 99 | 294 | 3 | 16 | 59 | 0 | 471 |
| excluded by design | 0 | 0 | 0 | 0 | 0 | 3 | 3 |
| **Total** | 1122 | 492 | 4 | 19 | 59 | 3 | 1699 |
<!-- readiness-gen:end readiness-tiers -->

## Look up your own resource type

Every AWS resource type this fork's provider roster knows about, tiered and
statused exactly once, generated from `live/readiness.json`. Search this
page (Ctrl+F or Cmd+F on most browsers) for the types your own configuration
declares - `aws_instance`, `aws_s3_bucket`, whatever they are - and read off
the tier, the status, and, for anything short of in-contract, why.

`pending-ratification`, `needs-separator` and `needs-evidence` are ordinary
admission debt: a future ratification batch clears them, the same way past
batches admitted `aws_nat_gateway` and `aws_cloudwatch_event_rule`.
`pending-mechanism` is a record-carried type waiting on the located-record
mechanism to reach it. `excluded` is the one status nothing clears - two
types, ruled out by standing policy rather than left pending.

<!-- readiness-gen:begin readiness-types -->
<div class="readiness-table-wrap" style="overflow-x: auto;">

| Type | Tier | Status | Reason |
|---|---|---|---|
| `aws_accessanalyzer_analyzer` | marker-carried | in-contract |  |
| `aws_accessanalyzer_archive_rule` | declaration-carried | in-contract |  |
| `aws_account_alternate_contact` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_account_primary_contact` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_account_region` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_acm_certificate` | marker-carried | in-contract |  |
| `aws_acm_certificate_validation` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_acmpca_certificate` | record-carried | in-contract |  |
| `aws_acmpca_certificate_authority` | marker-carried | in-contract |  |
| `aws_acmpca_certificate_authority_certificate` | declaration-carried | in-contract |  |
| `aws_acmpca_permission` | record-carried | needs-separator | a composite identity with no worked import example to read its separator from. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_acmpca_policy` | declaration-carried | in-contract |  |
| `aws_alb` | marker-carried | in-contract |  |
| `aws_alb_listener` | marker-carried | in-contract |  |
| `aws_alb_listener_certificate` | declaration-carried | in-contract |  |
| `aws_alb_listener_rule` | marker-carried | in-contract |  |
| `aws_alb_target_group` | marker-carried | in-contract |  |
| `aws_alb_target_group_attachment` | declaration-carried | in-contract |  |
| `aws_ami` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ami_copy` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ami_from_instance` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ami_launch_permission` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_amplify_app` | marker-carried | in-contract |  |
| `aws_amplify_backend_environment` | declaration-carried | in-contract |  |
| `aws_amplify_branch` | marker-carried | in-contract |  |
| `aws_amplify_domain_association` | declaration-carried | in-contract |  |
| `aws_amplify_webhook` | record-carried | in-contract |  |
| `aws_api_gateway_account` | record-carried | in-contract |  |
| `aws_api_gateway_api_key` | marker-carried | in-contract |  |
| `aws_api_gateway_authorizer` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_api_gateway_base_path_mapping` | declaration-carried | in-contract |  |
| `aws_api_gateway_client_certificate` | marker-carried | in-contract |  |
| `aws_api_gateway_deployment` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_api_gateway_documentation_part` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_api_gateway_documentation_version` | declaration-carried | in-contract |  |
| `aws_api_gateway_domain_name` | marker-carried | in-contract |  |
| `aws_api_gateway_domain_name_access_association` | marker-carried | in-contract |  |
| `aws_api_gateway_gateway_response` | declaration-carried | in-contract |  |
| `aws_api_gateway_integration` | declaration-carried | in-contract |  |
| `aws_api_gateway_integration_response` | declaration-carried | in-contract |  |
| `aws_api_gateway_method` | declaration-carried | in-contract |  |
| `aws_api_gateway_method_response` | declaration-carried | in-contract |  |
| `aws_api_gateway_method_settings` | declaration-carried | in-contract |  |
| `aws_api_gateway_model` | declaration-carried | in-contract |  |
| `aws_api_gateway_request_validator` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_api_gateway_resource` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_api_gateway_rest_api` | marker-carried | in-contract |  |
| `aws_api_gateway_rest_api_policy` | declaration-carried | in-contract |  |
| `aws_api_gateway_rest_api_put` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_api_gateway_stage` | marker-carried | in-contract |  |
| `aws_api_gateway_usage_plan` | marker-carried | in-contract |  |
| `aws_api_gateway_usage_plan_key` | declaration-carried | in-contract |  |
| `aws_api_gateway_vpc_link` | marker-carried | in-contract |  |
| `aws_apigatewayv2_api` | marker-carried | in-contract |  |
| `aws_apigatewayv2_api_mapping` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_apigatewayv2_authorizer` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_apigatewayv2_deployment` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_apigatewayv2_domain_name` | marker-carried | in-contract |  |
| `aws_apigatewayv2_integration` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_apigatewayv2_integration_response` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_apigatewayv2_model` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_apigatewayv2_route` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_apigatewayv2_route_response` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_apigatewayv2_routing_rule` | record-carried | in-contract |  |
| `aws_apigatewayv2_stage` | marker-carried | in-contract |  |
| `aws_apigatewayv2_vpc_link` | marker-carried | in-contract |  |
| `aws_app_cookie_stickiness_policy` | declaration-carried | in-contract |  |
| `aws_appautoscaling_policy` | declaration-carried | in-contract |  |
| `aws_appautoscaling_scheduled_action` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appautoscaling_target` | marker-carried | in-contract |  |
| `aws_appconfig_application` | marker-carried | in-contract |  |
| `aws_appconfig_configuration_profile` | marker-carried | in-contract |  |
| `aws_appconfig_deployment` | marker-carried | in-contract |  |
| `aws_appconfig_deployment_strategy` | marker-carried | in-contract |  |
| `aws_appconfig_environment` | marker-carried | in-contract |  |
| `aws_appconfig_extension` | marker-carried | in-contract |  |
| `aws_appconfig_extension_association` | record-carried | in-contract |  |
| `aws_appconfig_hosted_configuration_version` | record-carried | in-contract |  |
| `aws_appfabric_app_authorization` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appfabric_app_authorization_connection` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appfabric_app_bundle` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appfabric_ingestion` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appfabric_ingestion_destination` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appflow_connector_profile` | declaration-carried | in-contract |  |
| `aws_appflow_flow` | marker-carried | in-contract |  |
| `aws_appintegrations_data_integration` | marker-carried | in-contract |  |
| `aws_appintegrations_event_integration` | marker-carried | in-contract |  |
| `aws_applicationinsights_application` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appmesh_gateway_route` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appmesh_mesh` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appmesh_route` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appmesh_virtual_gateway` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appmesh_virtual_node` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appmesh_virtual_router` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appmesh_virtual_service` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_apprunner_auto_scaling_configuration_version` | marker-carried | in-contract |  |
| `aws_apprunner_connection` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_apprunner_custom_domain_association` | declaration-carried | in-contract |  |
| `aws_apprunner_default_auto_scaling_configuration_version` | declaration-carried | in-contract |  |
| `aws_apprunner_deployment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_apprunner_observability_configuration` | marker-carried | in-contract |  |
| `aws_apprunner_service` | marker-carried | in-contract |  |
| `aws_apprunner_vpc_connector` | marker-carried | in-contract |  |
| `aws_apprunner_vpc_ingress_connection` | marker-carried | in-contract |  |
| `aws_appstream_directory_config` | excluded by design | excluded | excluded by design: generates credential material this fork can never read back and verify again (maintainer ruling, 2026-08-15, issue #175). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appstream_fleet` | marker-carried | in-contract |  |
| `aws_appstream_fleet_stack_association` | declaration-carried | in-contract |  |
| `aws_appstream_image_builder` | marker-carried | in-contract |  |
| `aws_appstream_stack` | marker-carried | in-contract |  |
| `aws_appstream_user` | declaration-carried | in-contract |  |
| `aws_appstream_user_stack_association` | declaration-carried | in-contract |  |
| `aws_appsync_api` | marker-carried | in-contract |  |
| `aws_appsync_api_cache` | declaration-carried | in-contract |  |
| `aws_appsync_api_key` | record-carried | in-contract |  |
| `aws_appsync_channel_namespace` | marker-carried | in-contract |  |
| `aws_appsync_datasource` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appsync_domain_name` | declaration-carried | in-contract |  |
| `aws_appsync_domain_name_api_association` | declaration-carried | in-contract |  |
| `aws_appsync_function` | record-carried | in-contract |  |
| `aws_appsync_graphql_api` | marker-carried | in-contract |  |
| `aws_appsync_resolver` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_appsync_source_api_association` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_appsync_type` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_arcregionswitch_plan` | marker-carried | in-contract |  |
| `aws_arczonalshift_autoshift_observer_notification_status` | declaration-carried | in-contract |  |
| `aws_arczonalshift_zonal_autoshift_configuration` | declaration-carried | in-contract |  |
| `aws_athena_capacity_reservation` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_athena_data_catalog` | marker-carried | in-contract |  |
| `aws_athena_database` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_athena_named_query` | record-carried | in-contract |  |
| `aws_athena_prepared_statement` | declaration-carried | in-contract |  |
| `aws_athena_workgroup` | marker-carried | in-contract |  |
| `aws_auditmanager_account_registration` | declaration-carried | in-contract |  |
| `aws_auditmanager_assessment` | marker-carried | in-contract |  |
| `aws_auditmanager_assessment_delegation` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_auditmanager_assessment_report` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_auditmanager_control` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_auditmanager_framework` | marker-carried | in-contract |  |
| `aws_auditmanager_framework_share` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_auditmanager_organization_admin_account_registration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_autoscaling_attachment` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_autoscaling_group` | declaration-carried | in-contract |  |
| `aws_autoscaling_group_tag` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_autoscaling_lifecycle_hook` | declaration-carried | in-contract |  |
| `aws_autoscaling_notification` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_autoscaling_policy` | declaration-carried | in-contract |  |
| `aws_autoscaling_schedule` | declaration-carried | in-contract |  |
| `aws_autoscaling_traffic_source_attachment` | declaration-carried | in-contract |  |
| `aws_autoscalingplans_scaling_plan` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_backup_framework` | marker-carried | in-contract |  |
| `aws_backup_global_settings` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_backup_logically_air_gapped_vault` | marker-carried | in-contract |  |
| `aws_backup_plan` | marker-carried | in-contract |  |
| `aws_backup_region_settings` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_backup_report_plan` | marker-carried | in-contract |  |
| `aws_backup_restore_testing_plan` | marker-carried | in-contract |  |
| `aws_backup_restore_testing_selection` | declaration-carried | in-contract |  |
| `aws_backup_selection` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_backup_vault` | marker-carried | in-contract |  |
| `aws_backup_vault_lock_configuration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_backup_vault_notifications` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_backup_vault_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_batch_compute_environment` | marker-carried | in-contract |  |
| `aws_batch_job_definition` | marker-carried | in-contract |  |
| `aws_batch_job_queue` | marker-carried | in-contract |  |
| `aws_batch_scheduling_policy` | marker-carried | in-contract |  |
| `aws_bcmdataexports_export` | marker-carried | in-contract |  |
| `aws_bedrock_custom_model` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrock_evaluation_job` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrock_foundation_model_agreement` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrock_guardrail` | marker-carried | in-contract |  |
| `aws_bedrock_guardrail_version` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_bedrock_inference_profile` | marker-carried | in-contract |  |
| `aws_bedrock_model_invocation_logging_configuration` | declaration-carried | in-contract |  |
| `aws_bedrock_provisioned_model_throughput` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrock_use_case_for_model_access` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrockagent_agent` | marker-carried | in-contract |  |
| `aws_bedrockagent_agent_action_group` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrockagent_agent_alias` | marker-carried | in-contract |  |
| `aws_bedrockagent_agent_collaborator` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrockagent_agent_knowledge_base_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrockagent_data_source` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_bedrockagent_flow` | marker-carried | in-contract |  |
| `aws_bedrockagent_knowledge_base` | marker-carried | in-contract |  |
| `aws_bedrockagent_prompt` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_agent_runtime` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_agent_runtime_endpoint` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_api_key_credential_provider` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_browser` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_browser_profile` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_code_interpreter` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_evaluator` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_gateway` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_gateway_rule` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_bedrockagentcore_gateway_target` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_bedrockagentcore_harness` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_memory` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_memory_strategy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrockagentcore_oauth2_credential_provider` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_online_evaluation_config` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_policy` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_bedrockagentcore_policy_engine` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_registry` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrockagentcore_resource_policy` | declaration-carried | in-contract |  |
| `aws_bedrockagentcore_token_vault_cmk` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_bedrockagentcore_workload_identity` | declaration-carried | in-contract |  |
| `aws_billing_view` | marker-carried | in-contract |  |
| `aws_budgets_budget` | marker-carried | in-contract |  |
| `aws_budgets_budget_action` | marker-carried | in-contract |  |
| `aws_ce_anomaly_monitor` | marker-carried | in-contract |  |
| `aws_ce_anomaly_subscription` | marker-carried | in-contract |  |
| `aws_ce_cost_allocation_tag` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ce_cost_category` | marker-carried | in-contract |  |
| `aws_chatbot_slack_channel_configuration` | marker-carried | in-contract |  |
| `aws_chatbot_teams_channel_configuration` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chime_voice_connector` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chime_voice_connector_group` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chime_voice_connector_logging` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chime_voice_connector_origination` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chime_voice_connector_streaming` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chime_voice_connector_termination` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chime_voice_connector_termination_credentials` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chimesdkmediapipelines_media_insights_pipeline_configuration` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chimesdkvoice_global_settings` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chimesdkvoice_sip_media_application` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_chimesdkvoice_sip_rule` | record-carried | in-contract |  |
| `aws_chimesdkvoice_voice_profile_domain` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cleanrooms_collaboration` | marker-carried | in-contract |  |
| `aws_cleanrooms_configured_table` | marker-carried | in-contract |  |
| `aws_cleanrooms_membership` | marker-carried | in-contract |  |
| `aws_cloud9_environment_ec2` | marker-carried | in-contract |  |
| `aws_cloud9_environment_membership` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudcontrolapi_resource` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudformation_stack` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudformation_stack_instances` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudformation_stack_set` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudformation_stack_set_instance` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudformation_type` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudfront_anycast_ip_list` | marker-carried | in-contract |  |
| `aws_cloudfront_cache_policy` | declaration-carried | in-contract |  |
| `aws_cloudfront_connection_function` | marker-carried | in-contract |  |
| `aws_cloudfront_connection_group` | marker-carried | in-contract |  |
| `aws_cloudfront_continuous_deployment_policy` | record-carried | in-contract |  |
| `aws_cloudfront_distribution` | marker-carried | in-contract |  |
| `aws_cloudfront_distribution_tenant` | marker-carried | in-contract |  |
| `aws_cloudfront_field_level_encryption_config` | record-carried | in-contract |  |
| `aws_cloudfront_field_level_encryption_profile` | record-carried | in-contract |  |
| `aws_cloudfront_function` | marker-carried | in-contract |  |
| `aws_cloudfront_key_group` | record-carried | in-contract |  |
| `aws_cloudfront_key_value_store` | marker-carried | in-contract |  |
| `aws_cloudfront_monitoring_subscription` | declaration-carried | in-contract |  |
| `aws_cloudfront_multitenant_distribution` | marker-carried | in-contract |  |
| `aws_cloudfront_origin_access_control` | record-carried | in-contract |  |
| `aws_cloudfront_origin_access_identity` | record-carried | in-contract |  |
| `aws_cloudfront_origin_request_policy` | declaration-carried | in-contract |  |
| `aws_cloudfront_public_key` | record-carried | in-contract |  |
| `aws_cloudfront_realtime_log_config` | declaration-carried | in-contract |  |
| `aws_cloudfront_response_headers_policy` | declaration-carried | in-contract |  |
| `aws_cloudfront_trust_store` | marker-carried | in-contract |  |
| `aws_cloudfront_vpc_origin` | marker-carried | in-contract |  |
| `aws_cloudfrontkeyvaluestore_key` | declaration-carried | in-contract |  |
| `aws_cloudfrontkeyvaluestore_keys_exclusive` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudhsm_v2_cluster` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudhsm_v2_hsm` | record-carried | in-contract |  |
| `aws_cloudsearch_domain` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudsearch_domain_service_access_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudtrail` | marker-carried | in-contract |  |
| `aws_cloudtrail_event_data_store` | marker-carried | in-contract |  |
| `aws_cloudtrail_organization_delegated_admin_account` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudwatch_alarm_mute_rule` | marker-carried | in-contract |  |
| `aws_cloudwatch_composite_alarm` | marker-carried | in-contract |  |
| `aws_cloudwatch_contributor_insight_rule` | marker-carried | in-contract |  |
| `aws_cloudwatch_contributor_managed_insight_rule` | marker-carried | in-contract |  |
| `aws_cloudwatch_dashboard` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_api_destination` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_archive` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_bus` | marker-carried | in-contract |  |
| `aws_cloudwatch_event_bus_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudwatch_event_connection` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_endpoint` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_permission` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_rule` | marker-carried | in-contract |  |
| `aws_cloudwatch_event_target` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_account_policy` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_anomaly_detector` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_data_protection_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudwatch_log_delivery` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_delivery_destination` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_delivery_destination_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudwatch_log_delivery_source` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_destination` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_destination_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudwatch_log_group` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_index_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudwatch_log_metric_filter` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_resource_policy` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_s3_table_integration_source` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_cloudwatch_log_storage_tier_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cloudwatch_log_stream` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_subscription_filter` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_transformer` | declaration-carried | in-contract |  |
| `aws_cloudwatch_metric_alarm` | marker-carried | in-contract |  |
| `aws_cloudwatch_metric_stream` | marker-carried | in-contract |  |
| `aws_cloudwatch_otel_enrichment` | declaration-carried | in-contract |  |
| `aws_cloudwatch_query_definition` | record-carried | in-contract |  |
| `aws_codeartifact_domain` | marker-carried | in-contract |  |
| `aws_codeartifact_domain_permissions_policy` | declaration-carried | in-contract |  |
| `aws_codeartifact_repository` | marker-carried | in-contract |  |
| `aws_codeartifact_repository_permissions_policy` | declaration-carried | in-contract |  |
| `aws_codebuild_fleet` | marker-carried | in-contract |  |
| `aws_codebuild_project` | marker-carried | in-contract |  |
| `aws_codebuild_report_group` | marker-carried | in-contract |  |
| `aws_codebuild_resource_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codebuild_source_credential` | record-carried | in-contract |  |
| `aws_codebuild_webhook` | declaration-carried | in-contract |  |
| `aws_codecatalyst_dev_environment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codecatalyst_project` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codecatalyst_source_repository` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codecommit_approval_rule_template` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codecommit_approval_rule_template_association` | declaration-carried | in-contract |  |
| `aws_codecommit_repository` | marker-carried | in-contract |  |
| `aws_codecommit_trigger` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codeconnections_connection` | marker-carried | in-contract |  |
| `aws_codeconnections_host` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codedeploy_app` | marker-carried | in-contract |  |
| `aws_codedeploy_deployment_config` | declaration-carried | in-contract |  |
| `aws_codedeploy_deployment_group` | marker-carried | in-contract |  |
| `aws_codeguruprofiler_profiling_group` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codegurureviewer_repository_association` | marker-carried | in-contract |  |
| `aws_codepipeline` | marker-carried | in-contract |  |
| `aws_codepipeline_custom_action_type` | marker-carried | in-contract |  |
| `aws_codepipeline_webhook` | marker-carried | in-contract |  |
| `aws_codestarconnections_connection` | marker-carried | in-contract |  |
| `aws_codestarconnections_host` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_codestarnotifications_notification_rule` | marker-carried | in-contract |  |
| `aws_cognito_identity_pool` | marker-carried | in-contract |  |
| `aws_cognito_identity_pool_provider_principal_tag` | declaration-carried | in-contract |  |
| `aws_cognito_identity_pool_roles_attachment` | declaration-carried | in-contract |  |
| `aws_cognito_identity_provider` | declaration-carried | in-contract |  |
| `aws_cognito_log_delivery_configuration` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cognito_managed_login_branding` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_cognito_managed_user_pool_client` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_cognito_resource_server` | declaration-carried | in-contract |  |
| `aws_cognito_risk_configuration` | declaration-carried | in-contract |  |
| `aws_cognito_user` | declaration-carried | in-contract |  |
| `aws_cognito_user_group` | declaration-carried | in-contract |  |
| `aws_cognito_user_in_group` | declaration-carried | in-contract |  |
| `aws_cognito_user_pool` | marker-carried | in-contract |  |
| `aws_cognito_user_pool_client` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_cognito_user_pool_domain` | declaration-carried | in-contract |  |
| `aws_cognito_user_pool_ui_customization` | declaration-carried | in-contract |  |
| `aws_comprehend_document_classifier` | marker-carried | in-contract |  |
| `aws_comprehend_entity_recognizer` | marker-carried | in-contract |  |
| `aws_computeoptimizer_enrollment_status` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_computeoptimizer_recommendation_preferences` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_config_aggregate_authorization` | marker-carried | in-contract |  |
| `aws_config_config_rule` | marker-carried | in-contract |  |
| `aws_config_configuration_aggregator` | marker-carried | in-contract |  |
| `aws_config_configuration_recorder` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_config_configuration_recorder_status` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_config_conformance_pack` | declaration-carried | in-contract |  |
| `aws_config_delivery_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_config_organization_conformance_pack` | declaration-carried | in-contract |  |
| `aws_config_organization_custom_policy_rule` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_config_organization_custom_rule` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_config_organization_managed_rule` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_config_remediation_configuration` | declaration-carried | in-contract |  |
| `aws_config_retention_configuration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_connect_bot_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_connect_contact_flow` | marker-carried | in-contract |  |
| `aws_connect_contact_flow_module` | marker-carried | in-contract |  |
| `aws_connect_hours_of_operation` | marker-carried | in-contract |  |
| `aws_connect_instance` | marker-carried | in-contract |  |
| `aws_connect_instance_storage_config` | record-carried | in-contract |  |
| `aws_connect_lambda_function_association` | declaration-carried | in-contract |  |
| `aws_connect_phone_number` | marker-carried | in-contract |  |
| `aws_connect_phone_number_contact_flow_association` | declaration-carried | in-contract |  |
| `aws_connect_queue` | marker-carried | in-contract |  |
| `aws_connect_quick_connect` | marker-carried | in-contract |  |
| `aws_connect_routing_profile` | marker-carried | in-contract |  |
| `aws_connect_security_profile` | marker-carried | in-contract |  |
| `aws_connect_user` | marker-carried | in-contract |  |
| `aws_connect_user_hierarchy_group` | marker-carried | in-contract |  |
| `aws_connect_user_hierarchy_structure` | declaration-carried | in-contract |  |
| `aws_connect_vocabulary` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_controltower_baseline` | marker-carried | in-contract |  |
| `aws_controltower_control` | declaration-carried | in-contract |  |
| `aws_controltower_landing_zone` | marker-carried | in-contract |  |
| `aws_costoptimizationhub_enrollment_status` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_costoptimizationhub_preferences` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_cur_report_definition` | marker-carried | in-contract |  |
| `aws_customer_gateway` | marker-carried | in-contract |  |
| `aws_customerprofiles_domain` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_customerprofiles_profile` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dataexchange_data_set` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dataexchange_event_action` | record-carried | in-contract |  |
| `aws_dataexchange_revision` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dataexchange_revision_assets` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_datapipeline_pipeline` | marker-carried | in-contract |  |
| `aws_datapipeline_pipeline_definition` | declaration-carried | in-contract |  |
| `aws_datasync_agent` | marker-carried | in-contract |  |
| `aws_datasync_location_azure_blob` | marker-carried | in-contract |  |
| `aws_datasync_location_efs` | marker-carried | in-contract |  |
| `aws_datasync_location_fsx_lustre_file_system` | marker-carried | in-contract |  |
| `aws_datasync_location_fsx_ontap_file_system` | marker-carried | in-contract |  |
| `aws_datasync_location_fsx_openzfs_file_system` | marker-carried | in-contract |  |
| `aws_datasync_location_fsx_windows_file_system` | marker-carried | in-contract |  |
| `aws_datasync_location_hdfs` | marker-carried | in-contract |  |
| `aws_datasync_location_nfs` | marker-carried | in-contract |  |
| `aws_datasync_location_object_storage` | marker-carried | in-contract |  |
| `aws_datasync_location_s3` | marker-carried | in-contract |  |
| `aws_datasync_location_smb` | marker-carried | in-contract |  |
| `aws_datasync_task` | marker-carried | in-contract |  |
| `aws_datazone_asset_type` | declaration-carried | in-contract |  |
| `aws_datazone_domain` | marker-carried | in-contract |  |
| `aws_datazone_environment` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_datazone_environment_blueprint_configuration` | declaration-carried | in-contract |  |
| `aws_datazone_environment_profile` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_datazone_form_type` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_datazone_glossary` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_datazone_glossary_term` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_datazone_project` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_datazone_user_profile` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_dax_cluster` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dax_parameter_group` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dax_subnet_group` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_db_cluster_snapshot` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_db_event_subscription` | marker-carried | in-contract |  |
| `aws_db_instance` | marker-carried | in-contract |  |
| `aws_db_instance_automated_backups_replication` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_db_instance_role_association` | declaration-carried | in-contract |  |
| `aws_db_option_group` | marker-carried | in-contract |  |
| `aws_db_parameter_group` | marker-carried | in-contract |  |
| `aws_db_proxy` | marker-carried | in-contract |  |
| `aws_db_proxy_default_target_group` | declaration-carried | in-contract |  |
| `aws_db_proxy_endpoint` | marker-carried | in-contract |  |
| `aws_db_proxy_target` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_db_snapshot` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_db_snapshot_copy` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_db_subnet_group` | marker-carried | in-contract |  |
| `aws_default_network_acl` | marker-carried | in-contract |  |
| `aws_default_route_table` | marker-carried | in-contract |  |
| `aws_default_security_group` | marker-carried | in-contract |  |
| `aws_default_subnet` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_default_vpc` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_default_vpc_dhcp_options` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_detective_graph` | marker-carried | in-contract |  |
| `aws_detective_invitation_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_detective_member` | declaration-carried | in-contract |  |
| `aws_detective_organization_admin_account` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_detective_organization_configuration` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_devicefarm_device_pool` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_devicefarm_instance_profile` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_devicefarm_network_profile` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_devicefarm_project` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_devicefarm_test_grid_project` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_devicefarm_upload` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_devopsguru_event_sources_config` | declaration-carried | in-contract |  |
| `aws_devopsguru_notification_channel` | record-carried | in-contract |  |
| `aws_devopsguru_resource_collection` | declaration-carried | in-contract |  |
| `aws_devopsguru_service_integration` | declaration-carried | in-contract |  |
| `aws_directory_service_conditional_forwarder` | declaration-carried | in-contract |  |
| `aws_directory_service_directory` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_directory_service_log_subscription` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_directory_service_radius_settings` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_directory_service_region` | marker-carried | in-contract |  |
| `aws_directory_service_shared_directory` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_directory_service_shared_directory_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_directory_service_trust` | declaration-carried | in-contract |  |
| `aws_dlm_lifecycle_policy` | marker-carried | in-contract |  |
| `aws_dms_certificate` | marker-carried | in-contract |  |
| `aws_dms_endpoint` | marker-carried | in-contract |  |
| `aws_dms_event_subscription` | marker-carried | in-contract |  |
| `aws_dms_replication_config` | marker-carried | in-contract |  |
| `aws_dms_replication_instance` | marker-carried | in-contract |  |
| `aws_dms_replication_subnet_group` | marker-carried | in-contract |  |
| `aws_dms_replication_task` | marker-carried | in-contract |  |
| `aws_dms_s3_endpoint` | marker-carried | in-contract |  |
| `aws_docdb_cluster` | marker-carried | in-contract |  |
| `aws_docdb_cluster_instance` | marker-carried | in-contract |  |
| `aws_docdb_cluster_parameter_group` | marker-carried | in-contract |  |
| `aws_docdb_cluster_snapshot` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_docdb_event_subscription` | marker-carried | in-contract |  |
| `aws_docdb_global_cluster` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_docdb_subnet_group` | marker-carried | in-contract |  |
| `aws_docdbelastic_cluster` | marker-carried | in-contract |  |
| `aws_drs_replication_configuration_template` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dsql_cluster` | marker-carried | in-contract |  |
| `aws_dsql_cluster_peering` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_bgp_peer` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_connection` | marker-carried | in-contract |  |
| `aws_dx_connection_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_connection_confirmation` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_gateway` | marker-carried | in-contract |  |
| `aws_dx_gateway_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_gateway_association_proposal` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_hosted_connection` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_hosted_private_virtual_interface` | record-carried | in-contract |  |
| `aws_dx_hosted_private_virtual_interface_accepter` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_hosted_public_virtual_interface` | record-carried | in-contract |  |
| `aws_dx_hosted_public_virtual_interface_accepter` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_hosted_transit_virtual_interface` | record-carried | in-contract |  |
| `aws_dx_hosted_transit_virtual_interface_accepter` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_lag` | marker-carried | in-contract |  |
| `aws_dx_macsec_key_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dx_private_virtual_interface` | marker-carried | in-contract |  |
| `aws_dx_public_virtual_interface` | marker-carried | in-contract |  |
| `aws_dx_transit_virtual_interface` | marker-carried | in-contract |  |
| `aws_dynamodb_contributor_insights` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dynamodb_global_secondary_index` | declaration-carried | in-contract |  |
| `aws_dynamodb_global_table` | declaration-carried | in-contract |  |
| `aws_dynamodb_kinesis_streaming_destination` | declaration-carried | in-contract |  |
| `aws_dynamodb_resource_policy` | declaration-carried | in-contract |  |
| `aws_dynamodb_table` | marker-carried | in-contract |  |
| `aws_dynamodb_table_export` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dynamodb_table_item` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dynamodb_table_replica` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_dynamodb_tag` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ebs_default_kms_key` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ebs_encryption_by_default` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ebs_fast_snapshot_restore` | declaration-carried | in-contract |  |
| `aws_ebs_snapshot` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ebs_snapshot_block_public_access` | declaration-carried | in-contract |  |
| `aws_ebs_snapshot_copy` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ebs_snapshot_import` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ebs_volume` | marker-carried | in-contract |  |
| `aws_ebs_volume_copy` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_allowed_images_settings` | declaration-carried | in-contract |  |
| `aws_ec2_availability_zone_group` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_capacity_block_reservation` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_capacity_reservation` | marker-carried | in-contract |  |
| `aws_ec2_carrier_gateway` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_client_vpn_authorization_rule` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_ec2_client_vpn_endpoint` | marker-carried | in-contract |  |
| `aws_ec2_client_vpn_network_association` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_ec2_client_vpn_route` | declaration-carried | in-contract |  |
| `aws_ec2_default_credit_specification` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_fleet` | marker-carried | in-contract |  |
| `aws_ec2_host` | marker-carried | in-contract |  |
| `aws_ec2_image_block_public_access` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_instance_connect_endpoint` | marker-carried | in-contract |  |
| `aws_ec2_instance_metadata_defaults` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_instance_state` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_local_gateway_route` | declaration-carried | in-contract |  |
| `aws_ec2_local_gateway_route_table` | marker-carried | in-contract |  |
| `aws_ec2_local_gateway_route_table_virtual_interface_group_association` | marker-carried | in-contract |  |
| `aws_ec2_local_gateway_route_table_vpc_association` | marker-carried | in-contract |  |
| `aws_ec2_managed_prefix_list` | marker-carried | in-contract |  |
| `aws_ec2_managed_prefix_list_entry` | declaration-carried | in-contract |  |
| `aws_ec2_network_insights_access_scope` | marker-carried | in-contract |  |
| `aws_ec2_network_insights_analysis` | marker-carried | in-contract |  |
| `aws_ec2_network_insights_path` | marker-carried | in-contract |  |
| `aws_ec2_secondary_network` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_secondary_subnet` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_serial_console_access` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_subnet_cidr_reservation` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_tag` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_traffic_mirror_filter` | marker-carried | in-contract |  |
| `aws_ec2_traffic_mirror_filter_rule` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_ec2_traffic_mirror_session` | marker-carried | in-contract |  |
| `aws_ec2_traffic_mirror_target` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_connect` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_connect_peer` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_default_route_table_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_transit_gateway_default_route_table_propagation` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_transit_gateway_metering_policy` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_metering_policy_entry` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_multicast_domain` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_multicast_domain_association` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_transit_gateway_multicast_group_member` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_transit_gateway_multicast_group_source` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_transit_gateway_peering_attachment` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_peering_attachment_accepter` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_transit_gateway_policy_table` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_policy_table_association` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_prefix_list_reference` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ec2_transit_gateway_route` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_route_table` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_route_table_association` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_route_table_propagation` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_vpc_attachment` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_vpc_attachment_accepter` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ecr_account_setting` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ecr_lifecycle_policy` | declaration-carried | in-contract |  |
| `aws_ecr_pull_through_cache_rule` | declaration-carried | in-contract |  |
| `aws_ecr_pull_time_update_exclusion` | declaration-carried | in-contract |  |
| `aws_ecr_registry_policy` | record-carried | in-contract |  |
| `aws_ecr_registry_scanning_configuration` | record-carried | in-contract |  |
| `aws_ecr_replication_configuration` | record-carried | in-contract |  |
| `aws_ecr_repository` | marker-carried | in-contract |  |
| `aws_ecr_repository_creation_template` | declaration-carried | in-contract |  |
| `aws_ecr_repository_policy` | declaration-carried | in-contract |  |
| `aws_ecrpublic_repository` | marker-carried | in-contract |  |
| `aws_ecrpublic_repository_policy` | declaration-carried | in-contract |  |
| `aws_ecs_account_setting_default` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ecs_capacity_provider` | marker-carried | in-contract |  |
| `aws_ecs_cluster` | marker-carried | in-contract |  |
| `aws_ecs_cluster_capacity_providers` | declaration-carried | in-contract |  |
| `aws_ecs_daemon` | marker-carried | in-contract |  |
| `aws_ecs_daemon_task_definition` | marker-carried | in-contract |  |
| `aws_ecs_express_gateway_service` | marker-carried | in-contract |  |
| `aws_ecs_service` | marker-carried | in-contract |  |
| `aws_ecs_tag` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ecs_task_definition` | marker-carried | in-contract |  |
| `aws_ecs_task_set` | marker-carried | in-contract |  |
| `aws_efs_access_point` | marker-carried | in-contract |  |
| `aws_efs_backup_policy` | declaration-carried | in-contract |  |
| `aws_efs_file_system` | marker-carried | in-contract |  |
| `aws_efs_file_system_policy` | declaration-carried | in-contract |  |
| `aws_efs_mount_target` | record-carried | in-contract |  |
| `aws_efs_replication_configuration` | declaration-carried | in-contract |  |
| `aws_egress_only_internet_gateway` | marker-carried | in-contract |  |
| `aws_eip` | marker-carried | in-contract |  |
| `aws_eip_association` | record-carried | in-contract |  |
| `aws_eip_domain_name` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_eks_access_entry` | marker-carried | in-contract |  |
| `aws_eks_access_policy_association` | declaration-carried | in-contract |  |
| `aws_eks_addon` | marker-carried | in-contract |  |
| `aws_eks_capability` | marker-carried | in-contract |  |
| `aws_eks_cluster` | marker-carried | in-contract |  |
| `aws_eks_fargate_profile` | marker-carried | in-contract |  |
| `aws_eks_identity_provider_config` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_eks_node_group` | marker-carried | in-contract |  |
| `aws_eks_pod_identity_association` | marker-carried | in-contract |  |
| `aws_elastic_beanstalk_application` | marker-carried | in-contract |  |
| `aws_elastic_beanstalk_application_version` | marker-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_elastic_beanstalk_configuration_template` | record-carried | pending-mechanism | record-carried, markerless, but the provider offers no import support for this type at all. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_elastic_beanstalk_environment` | marker-carried | in-contract |  |
| `aws_elasticache_cluster` | marker-carried | in-contract |  |
| `aws_elasticache_global_replication_group` | record-carried | in-contract |  |
| `aws_elasticache_parameter_group` | marker-carried | in-contract |  |
| `aws_elasticache_replication_group` | marker-carried | in-contract |  |
| `aws_elasticache_reserved_cache_node` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_elasticache_serverless_cache` | marker-carried | in-contract |  |
| `aws_elasticache_subnet_group` | marker-carried | in-contract |  |
| `aws_elasticache_user` | marker-carried | in-contract |  |
| `aws_elasticache_user_group` | marker-carried | in-contract |  |
| `aws_elasticache_user_group_association` | declaration-carried | in-contract |  |
| `aws_elasticsearch_domain` | marker-carried | in-contract |  |
| `aws_elasticsearch_domain_policy` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_elasticsearch_domain_saml_options` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_elasticsearch_vpc_endpoint` | record-carried | in-contract |  |
| `aws_elastictranscoder_pipeline` | record-carried | in-contract |  |
| `aws_elastictranscoder_preset` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_elb` | marker-carried | in-contract |  |
| `aws_elb_attachment` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_emr_block_public_access_configuration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_emr_cluster` | marker-carried | in-contract |  |
| `aws_emr_instance_fleet` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_emr_instance_group` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_emr_managed_scaling_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_emr_security_configuration` | declaration-carried | in-contract |  |
| `aws_emr_studio` | marker-carried | in-contract |  |
| `aws_emr_studio_session_mapping` | declaration-carried | in-contract |  |
| `aws_emrcontainers_job_template` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_emrcontainers_virtual_cluster` | marker-carried | in-contract |  |
| `aws_emrserverless_application` | marker-carried | in-contract |  |
| `aws_evidently_feature` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_evidently_launch` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_evidently_project` | marker-carried | in-contract |  |
| `aws_evidently_segment` | marker-carried | in-contract |  |
| `aws_finspace_kx_cluster` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_finspace_kx_database` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_finspace_kx_dataview` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_finspace_kx_environment` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_finspace_kx_scaling_group` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_finspace_kx_user` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_finspace_kx_volume` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_fis_experiment_template` | marker-carried | in-contract |  |
| `aws_fis_target_account_configuration` | declaration-carried | in-contract |  |
| `aws_flow_log` | marker-carried | in-contract |  |
| `aws_fms_admin_account` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_fms_policy` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_fms_resource_set` | marker-carried | in-contract |  |
| `aws_fsx_backup` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_fsx_data_repository_association` | marker-carried | in-contract |  |
| `aws_fsx_file_cache` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_fsx_lustre_file_system` | marker-carried | in-contract |  |
| `aws_fsx_ontap_file_system` | marker-carried | in-contract |  |
| `aws_fsx_ontap_storage_virtual_machine` | marker-carried | in-contract |  |
| `aws_fsx_ontap_volume` | marker-carried | in-contract |  |
| `aws_fsx_openzfs_file_system` | marker-carried | in-contract |  |
| `aws_fsx_openzfs_snapshot` | marker-carried | in-contract |  |
| `aws_fsx_openzfs_volume` | marker-carried | in-contract |  |
| `aws_fsx_s3_access_point_attachment` | declaration-carried | in-contract |  |
| `aws_fsx_windows_file_system` | marker-carried | in-contract |  |
| `aws_gamelift_alias` | marker-carried | in-contract |  |
| `aws_gamelift_build` | marker-carried | in-contract |  |
| `aws_gamelift_fleet` | marker-carried | in-contract |  |
| `aws_gamelift_game_server_group` | marker-carried | in-contract |  |
| `aws_gamelift_game_session_queue` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_gamelift_script` | marker-carried | in-contract |  |
| `aws_glacier_vault` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_glacier_vault_lock` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_globalaccelerator_accelerator` | marker-carried | in-contract |  |
| `aws_globalaccelerator_cross_account_attachment` | marker-carried | in-contract |  |
| `aws_globalaccelerator_custom_routing_accelerator` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_globalaccelerator_custom_routing_endpoint_group` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_globalaccelerator_custom_routing_listener` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_globalaccelerator_endpoint_group` | record-carried | in-contract |  |
| `aws_globalaccelerator_listener` | record-carried | in-contract |  |
| `aws_glue_catalog` | marker-carried | in-contract |  |
| `aws_glue_catalog_database` | marker-carried | in-contract |  |
| `aws_glue_catalog_table` | declaration-carried | in-contract |  |
| `aws_glue_catalog_table_optimizer` | declaration-carried | in-contract |  |
| `aws_glue_classifier` | declaration-carried | in-contract |  |
| `aws_glue_connection` | marker-carried | in-contract |  |
| `aws_glue_crawler` | marker-carried | in-contract |  |
| `aws_glue_data_catalog_encryption_settings` | declaration-carried | in-contract |  |
| `aws_glue_data_quality_ruleset` | marker-carried | in-contract |  |
| `aws_glue_dev_endpoint` | marker-carried | in-contract |  |
| `aws_glue_job` | marker-carried | in-contract |  |
| `aws_glue_ml_transform` | marker-carried | in-contract |  |
| `aws_glue_partition` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_glue_partition_index` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_glue_registry` | marker-carried | in-contract |  |
| `aws_glue_resource_policy` | declaration-carried | in-contract |  |
| `aws_glue_schema` | marker-carried | in-contract |  |
| `aws_glue_security_configuration` | declaration-carried | in-contract |  |
| `aws_glue_trigger` | marker-carried | in-contract |  |
| `aws_glue_user_defined_function` | declaration-carried | in-contract |  |
| `aws_glue_workflow` | marker-carried | in-contract |  |
| `aws_grafana_license_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_grafana_role_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_grafana_workspace` | marker-carried | in-contract |  |
| `aws_grafana_workspace_api_key` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_grafana_workspace_saml_configuration` | declaration-carried | in-contract |  |
| `aws_grafana_workspace_service_account` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_grafana_workspace_service_account_token` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_guardduty_detector` | marker-carried | in-contract |  |
| `aws_guardduty_detector_feature` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_guardduty_filter` | marker-carried | in-contract |  |
| `aws_guardduty_invite_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_guardduty_ipset` | marker-carried | in-contract |  |
| `aws_guardduty_malware_protection_plan` | marker-carried | in-contract |  |
| `aws_guardduty_member` | declaration-carried | in-contract |  |
| `aws_guardduty_member_detector_feature` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_guardduty_organization_admin_account` | declaration-carried | in-contract |  |
| `aws_guardduty_organization_configuration` | declaration-carried | in-contract |  |
| `aws_guardduty_organization_configuration_feature` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_guardduty_publishing_destination` | marker-carried | in-contract |  |
| `aws_guardduty_threatintelset` | marker-carried | in-contract |  |
| `aws_iam_access_key` | record-carried | in-contract |  |
| `aws_iam_account_alias` | declaration-carried | in-contract |  |
| `aws_iam_account_password_policy` | declaration-carried | in-contract |  |
| `aws_iam_group` | declaration-carried | in-contract |  |
| `aws_iam_group_membership` | record-carried | pending-mechanism | record-carried, markerless, but the provider offers no import support for this type at all. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_iam_group_policies_exclusive` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_group_policy` | declaration-carried | in-contract |  |
| `aws_iam_group_policy_attachment` | declaration-carried | in-contract |  |
| `aws_iam_group_policy_attachments_exclusive` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_instance_profile` | marker-carried | in-contract |  |
| `aws_iam_openid_connect_provider` | marker-carried | in-contract |  |
| `aws_iam_organizations_features` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_outbound_web_identity_federation` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_policy` | marker-carried | in-contract |  |
| `aws_iam_policy_attachment` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_role` | marker-carried | in-contract |  |
| `aws_iam_role_policies_exclusive` | declaration-carried | in-contract |  |
| `aws_iam_role_policy` | declaration-carried | in-contract |  |
| `aws_iam_role_policy_attachment` | declaration-carried | in-contract |  |
| `aws_iam_role_policy_attachments_exclusive` | declaration-carried | in-contract |  |
| `aws_iam_saml_provider` | marker-carried | in-contract |  |
| `aws_iam_security_token_service_preferences` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_server_certificate` | marker-carried | in-contract |  |
| `aws_iam_service_linked_role` | marker-carried | in-contract |  |
| `aws_iam_service_specific_credential` | record-carried | in-contract |  |
| `aws_iam_signing_certificate` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_user` | marker-carried | in-contract |  |
| `aws_iam_user_group_membership` | declaration-carried | in-contract |  |
| `aws_iam_user_login_profile` | declaration-carried | in-contract |  |
| `aws_iam_user_policies_exclusive` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_user_policy` | declaration-carried | in-contract |  |
| `aws_iam_user_policy_attachment` | declaration-carried | in-contract |  |
| `aws_iam_user_policy_attachments_exclusive` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_user_ssh_key` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iam_virtual_mfa_device` | marker-carried | in-contract |  |
| `aws_identitystore_group` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_identitystore_group_membership` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_identitystore_user` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_imagebuilder_component` | marker-carried | in-contract |  |
| `aws_imagebuilder_container_recipe` | marker-carried | in-contract |  |
| `aws_imagebuilder_distribution_configuration` | marker-carried | in-contract |  |
| `aws_imagebuilder_image` | marker-carried | in-contract |  |
| `aws_imagebuilder_image_pipeline` | marker-carried | in-contract |  |
| `aws_imagebuilder_image_recipe` | marker-carried | in-contract |  |
| `aws_imagebuilder_infrastructure_configuration` | marker-carried | in-contract |  |
| `aws_imagebuilder_lifecycle_policy` | marker-carried | in-contract |  |
| `aws_imagebuilder_workflow` | marker-carried | in-contract |  |
| `aws_inspector2_delegated_admin_account` | declaration-carried | in-contract |  |
| `aws_inspector2_enabler` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_inspector2_filter` | marker-carried | in-contract |  |
| `aws_inspector2_member_association` | declaration-carried | in-contract |  |
| `aws_inspector2_organization_configuration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_inspector_assessment_target` | record-carried | in-contract |  |
| `aws_inspector_assessment_template` | marker-carried | in-contract |  |
| `aws_inspector_resource_group` | marker-carried | in-contract |  |
| `aws_instance` | marker-carried | in-contract |  |
| `aws_internet_gateway` | marker-carried | in-contract |  |
| `aws_internet_gateway_attachment` | declaration-carried | in-contract |  |
| `aws_internetmonitor_monitor` | marker-carried | in-contract |  |
| `aws_invoicing_invoice_unit` | marker-carried | in-contract |  |
| `aws_iot_authorizer` | marker-carried | in-contract |  |
| `aws_iot_billing_group` | marker-carried | in-contract |  |
| `aws_iot_ca_certificate` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iot_certificate` | record-carried | pending-mechanism | record-carried, markerless, but the provider offers no import support for this type at all. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_iot_domain_configuration` | marker-carried | in-contract |  |
| `aws_iot_event_configurations` | declaration-carried | in-contract |  |
| `aws_iot_indexing_configuration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iot_logging_options` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_iot_policy` | marker-carried | in-contract |  |
| `aws_iot_policy_attachment` | record-carried | pending-mechanism | record-carried, markerless, but the provider offers no import support for this type at all. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_iot_provisioning_template` | marker-carried | in-contract |  |
| `aws_iot_role_alias` | marker-carried | in-contract |  |
| `aws_iot_thing` | declaration-carried | in-contract |  |
| `aws_iot_thing_group` | marker-carried | in-contract |  |
| `aws_iot_thing_group_membership` | declaration-carried | in-contract |  |
| `aws_iot_thing_principal_attachment` | record-carried | in-contract |  |
| `aws_iot_thing_type` | marker-carried | in-contract |  |
| `aws_iot_topic_rule` | marker-carried | in-contract |  |
| `aws_iot_topic_rule_destination` | record-carried | in-contract |  |
| `aws_ivs_channel` | marker-carried | in-contract |  |
| `aws_ivs_playback_key_pair` | excluded by design | excluded | excluded by design: generates credential material this fork can never read back and verify again (maintainer ruling, 2026-08-15, issue #175). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ivs_recording_configuration` | marker-carried | in-contract |  |
| `aws_ivschat_logging_configuration` | marker-carried | in-contract |  |
| `aws_ivschat_room` | marker-carried | in-contract |  |
| `aws_kendra_data_source` | marker-carried | in-contract |  |
| `aws_kendra_experience` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_kendra_faq` | marker-carried | in-contract |  |
| `aws_kendra_index` | marker-carried | in-contract |  |
| `aws_kendra_query_suggestions_block_list` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_kendra_thesaurus` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_key_pair` | marker-carried | in-contract |  |
| `aws_keyspaces_keyspace` | marker-carried | in-contract |  |
| `aws_keyspaces_table` | marker-carried | in-contract |  |
| `aws_kinesis_account_settings` | declaration-carried | in-contract |  |
| `aws_kinesis_analytics_application` | marker-carried | in-contract |  |
| `aws_kinesis_firehose_delivery_stream` | marker-carried | in-contract |  |
| `aws_kinesis_resource_policy` | declaration-carried | in-contract |  |
| `aws_kinesis_stream` | marker-carried | in-contract |  |
| `aws_kinesis_stream_consumer` | marker-carried | in-contract |  |
| `aws_kinesis_video_stream` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_kinesisanalyticsv2_application` | marker-carried | in-contract |  |
| `aws_kinesisanalyticsv2_application_snapshot` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_kms_alias` | declaration-carried | in-contract |  |
| `aws_kms_ciphertext` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_kms_custom_key_store` | record-carried | in-contract |  |
| `aws_kms_external_key` | marker-carried | in-contract |  |
| `aws_kms_grant` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_kms_key` | marker-carried | in-contract |  |
| `aws_kms_key_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_kms_replica_external_key` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_kms_replica_key` | marker-carried | in-contract |  |
| `aws_lakeformation_data_cells_filter` | declaration-carried | in-contract |  |
| `aws_lakeformation_data_lake_settings` | record-carried | in-contract |  |
| `aws_lakeformation_identity_center_configuration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lakeformation_lf_tag` | declaration-carried | in-contract |  |
| `aws_lakeformation_lf_tag_expression` | declaration-carried | in-contract |  |
| `aws_lakeformation_opt_in` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lakeformation_permissions` | record-carried | pending-mechanism | record-carried, markerless, but the provider offers no import support for this type at all. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_lakeformation_resource` | record-carried | pending-mechanism | record-carried, markerless, but the provider offers no import support for this type at all. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_lakeformation_resource_lf_tag` | record-carried | pending-mechanism | record-carried, markerless, but the provider offers no import support for this type at all. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_lakeformation_resource_lf_tags` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lambda_alias` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lambda_capacity_provider` | marker-carried | in-contract |  |
| `aws_lambda_code_signing_config` | marker-carried | in-contract |  |
| `aws_lambda_event_source_mapping` | marker-carried | in-contract |  |
| `aws_lambda_function` | marker-carried | in-contract |  |
| `aws_lambda_function_event_invoke_config` | declaration-carried | in-contract |  |
| `aws_lambda_function_recursion_config` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lambda_function_scaling_config` | declaration-carried | in-contract |  |
| `aws_lambda_function_url` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lambda_invocation` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lambda_layer_version` | record-carried | in-contract |  |
| `aws_lambda_layer_version_permission` | declaration-carried | in-contract |  |
| `aws_lambda_permission` | declaration-carried | in-contract |  |
| `aws_lambda_provisioned_concurrency_config` | declaration-carried | in-contract |  |
| `aws_lambda_runtime_management_config` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_launch_configuration` | declaration-carried | in-contract |  |
| `aws_launch_template` | marker-carried | in-contract |  |
| `aws_lb` | marker-carried | in-contract |  |
| `aws_lb_cookie_stickiness_policy` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lb_listener` | marker-carried | in-contract |  |
| `aws_lb_listener_certificate` | declaration-carried | in-contract |  |
| `aws_lb_listener_rule` | marker-carried | in-contract |  |
| `aws_lb_ssl_negotiation_policy` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lb_target_group` | marker-carried | in-contract |  |
| `aws_lb_target_group_attachment` | declaration-carried | in-contract |  |
| `aws_lb_trust_store` | marker-carried | in-contract |  |
| `aws_lb_trust_store_revocation` | declaration-carried | in-contract |  |
| `aws_lex_bot` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lex_bot_alias` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lex_intent` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lex_slot_type` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lexv2models_bot` | marker-carried | in-contract |  |
| `aws_lexv2models_bot_locale` | declaration-carried | in-contract |  |
| `aws_lexv2models_bot_version` | record-carried | in-contract |  |
| `aws_lexv2models_intent` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lexv2models_slot` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lexv2models_slot_type` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_licensemanager_association` | declaration-carried | in-contract |  |
| `aws_licensemanager_grant` | record-carried | in-contract |  |
| `aws_licensemanager_grant_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_licensemanager_license_configuration` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_bucket` | marker-carried | in-contract |  |
| `aws_lightsail_bucket_access_key` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_bucket_resource_access` | declaration-carried | in-contract |  |
| `aws_lightsail_certificate` | marker-carried | in-contract |  |
| `aws_lightsail_container_service` | marker-carried | in-contract |  |
| `aws_lightsail_container_service_deployment_version` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_database` | marker-carried | in-contract |  |
| `aws_lightsail_disk` | marker-carried | in-contract |  |
| `aws_lightsail_disk_attachment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_distribution` | marker-carried | in-contract |  |
| `aws_lightsail_domain` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_domain_entry` | declaration-carried | in-contract |  |
| `aws_lightsail_instance` | marker-carried | in-contract |  |
| `aws_lightsail_instance_public_ports` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_key_pair` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_lb` | marker-carried | in-contract |  |
| `aws_lightsail_lb_attachment` | declaration-carried | in-contract |  |
| `aws_lightsail_lb_certificate` | declaration-carried | in-contract |  |
| `aws_lightsail_lb_certificate_attachment` | declaration-carried | in-contract |  |
| `aws_lightsail_lb_https_redirection_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_lb_stickiness_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_lightsail_static_ip` | declaration-carried | in-contract |  |
| `aws_lightsail_static_ip_attachment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_load_balancer_backend_server_policy` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_load_balancer_listener_policy` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_load_balancer_policy` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_location_geofence_collection` | marker-carried | in-contract |  |
| `aws_location_map` | marker-carried | in-contract |  |
| `aws_location_place_index` | marker-carried | in-contract |  |
| `aws_location_route_calculator` | marker-carried | in-contract |  |
| `aws_location_tracker` | marker-carried | in-contract |  |
| `aws_location_tracker_association` | declaration-carried | in-contract |  |
| `aws_m2_application` | marker-carried | in-contract |  |
| `aws_m2_deployment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_m2_environment` | marker-carried | in-contract |  |
| `aws_macie2_account` | record-carried | in-contract |  |
| `aws_macie2_classification_export_configuration` | declaration-carried | in-contract |  |
| `aws_macie2_classification_job` | marker-carried | in-contract |  |
| `aws_macie2_custom_data_identifier` | marker-carried | in-contract |  |
| `aws_macie2_findings_filter` | marker-carried | in-contract |  |
| `aws_macie2_invitation_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_macie2_member` | marker-carried | in-contract |  |
| `aws_macie2_organization_admin_account` | declaration-carried | in-contract |  |
| `aws_macie2_organization_configuration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_mailmanager_ingress_point` | marker-carried | in-contract |  |
| `aws_mailmanager_rule_set` | marker-carried | in-contract |  |
| `aws_mailmanager_traffic_policy` | marker-carried | in-contract |  |
| `aws_main_route_table_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_media_convert_queue` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_media_package_channel` | marker-carried | in-contract |  |
| `aws_media_packagev2_channel_group` | marker-carried | in-contract |  |
| `aws_media_store_container` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_media_store_container_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_medialive_channel` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_medialive_input` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_medialive_input_security_group` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_medialive_multiplex` | marker-carried | in-contract |  |
| `aws_medialive_multiplex_program` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_memorydb_acl` | marker-carried | in-contract |  |
| `aws_memorydb_cluster` | marker-carried | in-contract |  |
| `aws_memorydb_multi_region_cluster` | marker-carried | in-contract |  |
| `aws_memorydb_parameter_group` | marker-carried | in-contract |  |
| `aws_memorydb_snapshot` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_memorydb_subnet_group` | marker-carried | in-contract |  |
| `aws_memorydb_user` | marker-carried | in-contract |  |
| `aws_mq_broker` | marker-carried | in-contract |  |
| `aws_mq_configuration` | marker-carried | in-contract |  |
| `aws_msk_cluster` | marker-carried | in-contract |  |
| `aws_msk_cluster_policy` | declaration-carried | in-contract |  |
| `aws_msk_configuration` | record-carried | in-contract |  |
| `aws_msk_replicator` | marker-carried | in-contract |  |
| `aws_msk_scram_secret_association` | declaration-carried | in-contract |  |
| `aws_msk_serverless_cluster` | marker-carried | in-contract |  |
| `aws_msk_single_scram_secret_association` | declaration-carried | in-contract |  |
| `aws_msk_topic` | declaration-carried | in-contract |  |
| `aws_msk_vpc_connection` | marker-carried | in-contract |  |
| `aws_mskconnect_connector` | marker-carried | in-contract |  |
| `aws_mskconnect_custom_plugin` | marker-carried | in-contract |  |
| `aws_mskconnect_worker_configuration` | marker-carried | in-contract |  |
| `aws_mwaa_environment` | marker-carried | in-contract |  |
| `aws_nat_gateway` | marker-carried | in-contract |  |
| `aws_nat_gateway_eip_association` | declaration-carried | in-contract |  |
| `aws_neptune_cluster` | marker-carried | in-contract |  |
| `aws_neptune_cluster_endpoint` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_neptune_cluster_instance` | marker-carried | in-contract |  |
| `aws_neptune_cluster_parameter_group` | marker-carried | in-contract |  |
| `aws_neptune_cluster_snapshot` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_neptune_event_subscription` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_neptune_global_cluster` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_neptune_parameter_group` | marker-carried | in-contract |  |
| `aws_neptune_subnet_group` | marker-carried | in-contract |  |
| `aws_neptunegraph_graph` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_neptunegraph_private_graph_endpoint` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_network_acl` | marker-carried | in-contract |  |
| `aws_network_acl_association` | record-carried | in-contract |  |
| `aws_network_acl_rule` | declaration-carried | in-contract |  |
| `aws_network_interface` | marker-carried | in-contract |  |
| `aws_network_interface_attachment` | record-carried | in-contract |  |
| `aws_network_interface_permission` | record-carried | in-contract |  |
| `aws_network_interface_sg_attachment` | declaration-carried | in-contract |  |
| `aws_networkfirewall_container_association` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_networkfirewall_firewall` | marker-carried | in-contract |  |
| `aws_networkfirewall_firewall_policy` | marker-carried | in-contract |  |
| `aws_networkfirewall_firewall_transit_gateway_attachment_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_networkfirewall_logging_configuration` | declaration-carried | in-contract |  |
| `aws_networkfirewall_resource_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_networkfirewall_rule_group` | marker-carried | in-contract |  |
| `aws_networkfirewall_tls_inspection_configuration` | marker-carried | in-contract |  |
| `aws_networkfirewall_vpc_endpoint_association` | marker-carried | in-contract |  |
| `aws_networkflowmonitor_monitor` | marker-carried | in-contract |  |
| `aws_networkflowmonitor_scope` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_networkmanager_attachment_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_networkmanager_attachment_routing_policy_label` | declaration-carried | in-contract |  |
| `aws_networkmanager_connect_attachment` | marker-carried | in-contract |  |
| `aws_networkmanager_connect_peer` | marker-carried | in-contract |  |
| `aws_networkmanager_connection` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_networkmanager_core_network` | marker-carried | in-contract |  |
| `aws_networkmanager_core_network_policy_attachment` | declaration-carried | in-contract |  |
| `aws_networkmanager_customer_gateway_association` | declaration-carried | in-contract |  |
| `aws_networkmanager_device` | marker-carried | in-contract |  |
| `aws_networkmanager_dx_gateway_attachment` | marker-carried | in-contract |  |
| `aws_networkmanager_global_network` | marker-carried | in-contract |  |
| `aws_networkmanager_link` | marker-carried | in-contract |  |
| `aws_networkmanager_link_association` | declaration-carried | in-contract |  |
| `aws_networkmanager_prefix_list_association` | declaration-carried | in-contract |  |
| `aws_networkmanager_site` | marker-carried | in-contract |  |
| `aws_networkmanager_site_to_site_vpn_attachment` | marker-carried | in-contract |  |
| `aws_networkmanager_transit_gateway_connect_peer_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_networkmanager_transit_gateway_peering` | marker-carried | in-contract |  |
| `aws_networkmanager_transit_gateway_registration` | declaration-carried | in-contract |  |
| `aws_networkmanager_transit_gateway_route_table_attachment` | marker-carried | in-contract |  |
| `aws_networkmanager_vpc_attachment` | marker-carried | in-contract |  |
| `aws_networkmonitor_monitor` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_networkmonitor_probe` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_notifications_channel_association` | declaration-carried | in-contract |  |
| `aws_notifications_event_rule` | record-carried | in-contract |  |
| `aws_notifications_managed_notification_account_contact_association` | declaration-carried | in-contract |  |
| `aws_notifications_managed_notification_additional_channel_association` | declaration-carried | in-contract |  |
| `aws_notifications_notification_configuration` | marker-carried | in-contract |  |
| `aws_notifications_notification_hub` | declaration-carried | in-contract |  |
| `aws_notifications_organizational_unit_association` | declaration-carried | in-contract |  |
| `aws_notifications_organizations_access` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_notificationscontacts_email_contact` | marker-carried | in-contract |  |
| `aws_oam_link` | marker-carried | in-contract |  |
| `aws_oam_sink` | marker-carried | in-contract |  |
| `aws_oam_sink_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_observabilityadmin_centralization_rule_for_organization` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_observabilityadmin_s3_table_integration` | marker-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_enrichment` | declaration-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_evaluation` | declaration-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_evaluation_for_organization` | declaration-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_pipeline` | marker-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_rule` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_observabilityadmin_telemetry_rule_for_organization` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_odb_cloud_autonomous_vm_cluster` | marker-carried | in-contract |  |
| `aws_odb_cloud_exadata_infrastructure` | marker-carried | in-contract |  |
| `aws_odb_cloud_vm_cluster` | marker-carried | in-contract |  |
| `aws_odb_network` | marker-carried | in-contract |  |
| `aws_odb_network_peering_connection` | marker-carried | in-contract |  |
| `aws_opensearch_application` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_opensearch_authorize_vpc_endpoint_access` | declaration-carried | in-contract |  |
| `aws_opensearch_domain` | marker-carried | in-contract |  |
| `aws_opensearch_domain_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_opensearch_domain_saml_options` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_opensearch_inbound_connection_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_opensearch_outbound_connection` | record-carried | in-contract |  |
| `aws_opensearch_package` | record-carried | in-contract |  |
| `aws_opensearch_package_association` | declaration-carried | in-contract |  |
| `aws_opensearch_vpc_endpoint` | record-carried | in-contract |  |
| `aws_opensearchserverless_access_policy` | declaration-carried | in-contract |  |
| `aws_opensearchserverless_collection` | marker-carried | in-contract |  |
| `aws_opensearchserverless_collection_group` | marker-carried | in-contract |  |
| `aws_opensearchserverless_lifecycle_policy` | declaration-carried | in-contract |  |
| `aws_opensearchserverless_security_config` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_opensearchserverless_security_policy` | declaration-carried | in-contract |  |
| `aws_opensearchserverless_vpc_endpoint` | record-carried | in-contract |  |
| `aws_organizations_account` | marker-carried | in-contract |  |
| `aws_organizations_aws_service_access` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_organizations_delegated_administrator` | declaration-carried | in-contract |  |
| `aws_organizations_organization` | record-carried | in-contract |  |
| `aws_organizations_organizational_unit` | marker-carried | in-contract |  |
| `aws_organizations_policy` | marker-carried | in-contract |  |
| `aws_organizations_policy_attachment` | declaration-carried | in-contract |  |
| `aws_organizations_resource_policy` | marker-carried | in-contract |  |
| `aws_organizations_tag` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_osis_pipeline` | marker-carried | in-contract |  |
| `aws_osis_pipeline_endpoint` | record-carried | in-contract |  |
| `aws_osis_resource_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_outposts_capacity_task` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_paymentcryptography_key` | marker-carried | in-contract |  |
| `aws_paymentcryptography_key_alias` | declaration-carried | in-contract |  |
| `aws_pinpoint_adm_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_apns_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_apns_sandbox_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_apns_voip_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_apns_voip_sandbox_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_app` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_baidu_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_email_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_email_template` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_event_stream` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_gcm_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpoint_sms_channel` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_pinpointsmsvoicev2_configuration_set` | marker-carried | in-contract |  |
| `aws_pinpointsmsvoicev2_event_destination` | declaration-carried | in-contract |  |
| `aws_pinpointsmsvoicev2_opt_out_list` | marker-carried | in-contract |  |
| `aws_pinpointsmsvoicev2_phone_number` | marker-carried | in-contract |  |
| `aws_pinpointsmsvoicev2_pool` | marker-carried | in-contract |  |
| `aws_pinpointsmsvoicev2_resource_policy` | declaration-carried | in-contract |  |
| `aws_pinpointsmsvoicev2_sender_id` | marker-carried | in-contract |  |
| `aws_pipes_pipe` | marker-carried | in-contract |  |
| `aws_placement_group` | marker-carried | in-contract |  |
| `aws_prometheus_alert_manager_definition` | declaration-carried | in-contract |  |
| `aws_prometheus_anomaly_detector` | marker-carried | in-contract |  |
| `aws_prometheus_query_logging_configuration` | declaration-carried | in-contract |  |
| `aws_prometheus_resource_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_prometheus_rule_group_namespace` | marker-carried | in-contract |  |
| `aws_prometheus_scraper` | marker-carried | in-contract |  |
| `aws_prometheus_scraper_logging_configuration` | declaration-carried | in-contract |  |
| `aws_prometheus_workspace` | marker-carried | in-contract |  |
| `aws_prometheus_workspace_configuration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_proxy_protocol_policy` | record-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_qbusiness_application` | marker-carried | in-contract |  |
| `aws_qldb_ledger` | marker-carried | in-contract |  |
| `aws_qldb_stream` | marker-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_account_settings` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_account_subscription` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_analysis` | marker-carried | in-contract |  |
| `aws_quicksight_custom_permissions` | marker-carried | in-contract |  |
| `aws_quicksight_dashboard` | marker-carried | in-contract |  |
| `aws_quicksight_data_set` | marker-carried | in-contract |  |
| `aws_quicksight_data_source` | marker-carried | in-contract |  |
| `aws_quicksight_folder` | marker-carried | needs-separator | a composite identity with no worked import example to read its separator from. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_folder_membership` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_group` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_group_membership` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_iam_policy_assignment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_ingestion` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_ip_restriction` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_key_registration` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_namespace` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_refresh_schedule` | declaration-carried | in-contract |  |
| `aws_quicksight_role_custom_permission` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_role_membership` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_template` | marker-carried | in-contract |  |
| `aws_quicksight_template_alias` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_theme` | marker-carried | in-contract |  |
| `aws_quicksight_user` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_user_custom_permission` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_quicksight_vpc_connection` | marker-carried | in-contract |  |
| `aws_ram_permission` | marker-carried | in-contract |  |
| `aws_ram_principal_association` | declaration-carried | in-contract |  |
| `aws_ram_resource_association` | declaration-carried | in-contract |  |
| `aws_ram_resource_share` | marker-carried | in-contract |  |
| `aws_ram_resource_share_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ram_resource_share_associations_exclusive` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ram_sharing_with_organization` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_rbin_rule` | marker-carried | in-contract |  |
| `aws_rds_certificate` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_rds_cluster` | marker-carried | in-contract |  |
| `aws_rds_cluster_activity_stream` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_rds_cluster_endpoint` | marker-carried | in-contract |  |
| `aws_rds_cluster_instance` | marker-carried | in-contract |  |
| `aws_rds_cluster_parameter_group` | marker-carried | in-contract |  |
| `aws_rds_cluster_role_association` | declaration-carried | in-contract |  |
| `aws_rds_cluster_snapshot_copy` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_rds_custom_db_engine_version` | marker-carried | in-contract |  |
| `aws_rds_export_task` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_rds_global_cluster` | marker-carried | in-contract |  |
| `aws_rds_instance_state` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_rds_integration` | marker-carried | in-contract |  |
| `aws_rds_reserved_instance` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_rds_shard_group` | marker-carried | in-contract |  |
| `aws_redshift_authentication_profile` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_cluster` | marker-carried | in-contract |  |
| `aws_redshift_cluster_iam_roles` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_cluster_snapshot` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_data_share_authorization` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_data_share_consumer_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_endpoint_access` | declaration-carried | in-contract |  |
| `aws_redshift_endpoint_authorization` | declaration-carried | in-contract |  |
| `aws_redshift_event_subscription` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_hsm_client_certificate` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_hsm_configuration` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_idc_application` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_integration` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_logging` | declaration-carried | in-contract |  |
| `aws_redshift_namespace_registration` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_parameter_group` | marker-carried | in-contract |  |
| `aws_redshift_partner` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_resource_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_scheduled_action` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_snapshot_copy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_snapshot_copy_grant` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_snapshot_schedule` | marker-carried | in-contract |  |
| `aws_redshift_snapshot_schedule_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshift_subnet_group` | marker-carried | in-contract |  |
| `aws_redshift_usage_limit` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshiftdata_statement` | record-carried | in-contract |  |
| `aws_redshiftserverless_custom_domain_association` | declaration-carried | in-contract |  |
| `aws_redshiftserverless_endpoint_access` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshiftserverless_namespace` | marker-carried | in-contract |  |
| `aws_redshiftserverless_resource_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshiftserverless_snapshot` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshiftserverless_usage_limit` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_redshiftserverless_workgroup` | marker-carried | in-contract |  |
| `aws_rekognition_collection` | marker-carried | in-contract |  |
| `aws_rekognition_project` | marker-carried | in-contract |  |
| `aws_rekognition_stream_processor` | marker-carried | in-contract |  |
| `aws_resiliencehub_resiliency_policy` | marker-carried | in-contract |  |
| `aws_resiliencehubv2_policy` | marker-carried | in-contract |  |
| `aws_resiliencehubv2_service` | marker-carried | in-contract |  |
| `aws_resiliencehubv2_system` | marker-carried | in-contract |  |
| `aws_resourceexplorer2_index` | marker-carried | in-contract |  |
| `aws_resourceexplorer2_view` | marker-carried | in-contract |  |
| `aws_resourcegroups_group` | marker-carried | in-contract |  |
| `aws_resourcegroups_resource` | declaration-carried | in-contract |  |
| `aws_rolesanywhere_profile` | marker-carried | in-contract |  |
| `aws_rolesanywhere_trust_anchor` | marker-carried | in-contract |  |
| `aws_route` | declaration-carried | in-contract |  |
| `aws_route53_cidr_collection` | declaration-carried | in-contract |  |
| `aws_route53_cidr_location` | declaration-carried | in-contract |  |
| `aws_route53_delegation_set` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_route53_health_check` | marker-carried | in-contract |  |
| `aws_route53_hosted_zone_dnssec` | declaration-carried | in-contract |  |
| `aws_route53_key_signing_key` | declaration-carried | in-contract |  |
| `aws_route53_query_log` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_route53_record` | declaration-carried | in-contract |  |
| `aws_route53_records_exclusive` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_route53_resolver_config` | record-carried | in-contract |  |
| `aws_route53_resolver_dnssec_config` | record-carried | in-contract |  |
| `aws_route53_resolver_endpoint` | marker-carried | in-contract |  |
| `aws_route53_resolver_firewall_config` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_route53_resolver_firewall_domain_list` | marker-carried | in-contract |  |
| `aws_route53_resolver_firewall_rule` | declaration-carried | in-contract |  |
| `aws_route53_resolver_firewall_rule_group` | marker-carried | in-contract |  |
| `aws_route53_resolver_firewall_rule_group_association` | marker-carried | in-contract |  |
| `aws_route53_resolver_query_log_config` | marker-carried | in-contract |  |
| `aws_route53_resolver_query_log_config_association` | record-carried | in-contract |  |
| `aws_route53_resolver_rule` | marker-carried | in-contract |  |
| `aws_route53_resolver_rule_association` | record-carried | in-contract |  |
| `aws_route53_traffic_policy` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_route53_traffic_policy_instance` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_route53_vpc_association_authorization` | declaration-carried | in-contract |  |
| `aws_route53_zone` | marker-carried | in-contract |  |
| `aws_route53_zone_association` | declaration-carried | in-contract |  |
| `aws_route53domains_delegation_signer_record` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_route53domains_domain` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_route53domains_registered_domain` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_route53profiles_association` | marker-carried | in-contract |  |
| `aws_route53profiles_profile` | marker-carried | in-contract |  |
| `aws_route53profiles_resource_association` | record-carried | in-contract |  |
| `aws_route53recoverycontrolconfig_cluster` | marker-carried | in-contract |  |
| `aws_route53recoverycontrolconfig_control_panel` | marker-carried | in-contract |  |
| `aws_route53recoverycontrolconfig_routing_control` | record-carried | in-contract |  |
| `aws_route53recoverycontrolconfig_safety_rule` | marker-carried | in-contract |  |
| `aws_route53recoveryreadiness_cell` | marker-carried | in-contract |  |
| `aws_route53recoveryreadiness_readiness_check` | marker-carried | in-contract |  |
| `aws_route53recoveryreadiness_recovery_group` | marker-carried | in-contract |  |
| `aws_route53recoveryreadiness_resource_set` | marker-carried | in-contract |  |
| `aws_route_table` | marker-carried | in-contract |  |
| `aws_route_table_association` | declaration-carried | in-contract |  |
| `aws_rum_app_monitor` | marker-carried | in-contract |  |
| `aws_rum_metrics_destination` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_access_point` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_account_public_access_block` | declaration-carried | in-contract |  |
| `aws_s3_bucket` | marker-carried | in-contract |  |
| `aws_s3_bucket_abac` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_bucket_accelerate_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_acl` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_bucket_analytics_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_cors_configuration` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_bucket_intelligent_tiering_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_inventory` | declaration-carried | in-contract |  |
| `aws_s3_bucket_lifecycle_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_logging` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_bucket_metadata_configuration` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_bucket_metric` | declaration-carried | in-contract |  |
| `aws_s3_bucket_notification` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_bucket_object` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_bucket_object_lock_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_ownership_controls` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_bucket_policy` | declaration-carried | in-contract |  |
| `aws_s3_bucket_public_access_block` | declaration-carried | in-contract |  |
| `aws_s3_bucket_replication_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_request_payment_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_server_side_encryption_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_versioning` | declaration-carried | in-contract |  |
| `aws_s3_bucket_website_configuration` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_directory_bucket` | marker-carried | in-contract |  |
| `aws_s3_object` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3_object_copy` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_access_grant` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_access_grants_instance` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_access_grants_instance_resource_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_access_grants_location` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_access_point_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_bucket` | marker-carried | in-contract |  |
| `aws_s3control_bucket_lifecycle_configuration` | declaration-carried | in-contract |  |
| `aws_s3control_bucket_policy` | declaration-carried | in-contract |  |
| `aws_s3control_directory_bucket_access_point_scope` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_multi_region_access_point` | declaration-carried | in-contract |  |
| `aws_s3control_multi_region_access_point_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_multi_region_access_point_routes` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3control_object_lambda_access_point` | declaration-carried | in-contract |  |
| `aws_s3control_object_lambda_access_point_policy` | declaration-carried | in-contract |  |
| `aws_s3control_storage_lens_configuration` | marker-carried | in-contract |  |
| `aws_s3files_access_point` | marker-carried | in-contract |  |
| `aws_s3files_file_system` | marker-carried | in-contract |  |
| `aws_s3files_file_system_policy` | declaration-carried | in-contract |  |
| `aws_s3files_mount_target` | record-carried | in-contract |  |
| `aws_s3files_synchronization_configuration` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3outposts_endpoint` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_s3tables_namespace` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3tables_table` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3tables_table_bucket` | marker-carried | in-contract |  |
| `aws_s3tables_table_bucket_policy` | declaration-carried | in-contract |  |
| `aws_s3tables_table_bucket_replication` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3tables_table_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3tables_table_replication` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_s3vectors_index` | marker-carried | in-contract |  |
| `aws_s3vectors_vector_bucket` | marker-carried | in-contract |  |
| `aws_s3vectors_vector_bucket_policy` | declaration-carried | in-contract |  |
| `aws_sagemaker_algorithm` | marker-carried | in-contract |  |
| `aws_sagemaker_app` | marker-carried | in-contract |  |
| `aws_sagemaker_app_image_config` | marker-carried | in-contract |  |
| `aws_sagemaker_code_repository` | marker-carried | in-contract |  |
| `aws_sagemaker_data_quality_job_definition` | marker-carried | in-contract |  |
| `aws_sagemaker_device` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sagemaker_device_fleet` | marker-carried | in-contract |  |
| `aws_sagemaker_domain` | marker-carried | in-contract |  |
| `aws_sagemaker_endpoint` | marker-carried | in-contract |  |
| `aws_sagemaker_endpoint_configuration` | marker-carried | in-contract |  |
| `aws_sagemaker_feature_group` | marker-carried | in-contract |  |
| `aws_sagemaker_flow_definition` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sagemaker_hub` | marker-carried | in-contract |  |
| `aws_sagemaker_hub_content_reference` | marker-carried | in-contract |  |
| `aws_sagemaker_human_task_ui` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sagemaker_hyper_parameter_tuning_job` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sagemaker_image` | marker-carried | in-contract |  |
| `aws_sagemaker_image_version` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_sagemaker_labeling_job` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sagemaker_mlflow_app` | marker-carried | in-contract |  |
| `aws_sagemaker_mlflow_tracking_server` | marker-carried | in-contract |  |
| `aws_sagemaker_model` | marker-carried | in-contract |  |
| `aws_sagemaker_model_card` | marker-carried | in-contract |  |
| `aws_sagemaker_model_card_export_job` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sagemaker_model_package_group` | marker-carried | in-contract |  |
| `aws_sagemaker_model_package_group_policy` | declaration-carried | in-contract |  |
| `aws_sagemaker_monitoring_schedule` | marker-carried | in-contract |  |
| `aws_sagemaker_notebook_instance` | marker-carried | in-contract |  |
| `aws_sagemaker_notebook_instance_lifecycle_configuration` | marker-carried | in-contract |  |
| `aws_sagemaker_pipeline` | marker-carried | in-contract |  |
| `aws_sagemaker_project` | marker-carried | in-contract |  |
| `aws_sagemaker_servicecatalog_portfolio_status` | declaration-carried | in-contract |  |
| `aws_sagemaker_space` | marker-carried | in-contract |  |
| `aws_sagemaker_studio_lifecycle_config` | marker-carried | in-contract |  |
| `aws_sagemaker_training_job` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sagemaker_user_profile` | marker-carried | in-contract |  |
| `aws_sagemaker_workforce` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sagemaker_workteam` | marker-carried | in-contract |  |
| `aws_savingsplans_savings_plan` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_scheduler_schedule` | declaration-carried | in-contract |  |
| `aws_scheduler_schedule_group` | marker-carried | in-contract |  |
| `aws_schemas_discoverer` | marker-carried | in-contract |  |
| `aws_schemas_registry` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_schemas_registry_policy` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_schemas_schema` | marker-carried | in-contract |  |
| `aws_secretsmanager_secret` | marker-carried | in-contract |  |
| `aws_secretsmanager_secret_policy` | declaration-carried | in-contract |  |
| `aws_secretsmanager_secret_rotation` | declaration-carried | in-contract |  |
| `aws_secretsmanager_secret_version` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_secretsmanager_tag` | declaration-carried | in-contract |  |
| `aws_security_group` | marker-carried | in-contract |  |
| `aws_security_group_rule` | declaration-carried | in-contract |  |
| `aws_securityhub_account` | record-carried | in-contract |  |
| `aws_securityhub_account_v2` | marker-carried | in-contract |  |
| `aws_securityhub_action_target` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_securityhub_aggregator_v2` | marker-carried | in-contract |  |
| `aws_securityhub_automation_rule` | marker-carried | in-contract |  |
| `aws_securityhub_automation_rule_v2` | marker-carried | in-contract |  |
| `aws_securityhub_configuration_policy` | record-carried | in-contract |  |
| `aws_securityhub_configuration_policy_association` | declaration-carried | in-contract |  |
| `aws_securityhub_connector_v2` | marker-carried | in-contract |  |
| `aws_securityhub_finding_aggregator` | record-carried | in-contract |  |
| `aws_securityhub_insight` | record-carried | in-contract |  |
| `aws_securityhub_invite_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_securityhub_member` | declaration-carried | in-contract |  |
| `aws_securityhub_organization_admin_account` | declaration-carried | in-contract |  |
| `aws_securityhub_organization_configuration` | record-carried | in-contract |  |
| `aws_securityhub_product_subscription` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_securityhub_standards_control` | declaration-carried | in-contract |  |
| `aws_securityhub_standards_control_association` | declaration-carried | in-contract |  |
| `aws_securityhub_standards_subscription` | record-carried | in-contract |  |
| `aws_securitylake_aws_log_source` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_securitylake_custom_log_source` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_securitylake_data_lake` | marker-carried | in-contract |  |
| `aws_securitylake_subscriber` | marker-carried | in-contract |  |
| `aws_securitylake_subscriber_notification` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_serverlessapplicationrepository_cloudformation_stack` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_service_discovery_http_namespace` | marker-carried | in-contract |  |
| `aws_service_discovery_instance` | declaration-carried | in-contract |  |
| `aws_service_discovery_private_dns_namespace` | marker-carried | in-contract |  |
| `aws_service_discovery_public_dns_namespace` | marker-carried | in-contract |  |
| `aws_service_discovery_service` | marker-carried | in-contract |  |
| `aws_servicecatalog_budget_resource_association` | declaration-carried | in-contract |  |
| `aws_servicecatalog_constraint` | record-carried | in-contract |  |
| `aws_servicecatalog_organizations_access` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_servicecatalog_portfolio` | marker-carried | in-contract |  |
| `aws_servicecatalog_portfolio_share` | declaration-carried | in-contract |  |
| `aws_servicecatalog_principal_portfolio_association` | declaration-carried | in-contract |  |
| `aws_servicecatalog_product` | marker-carried | in-contract |  |
| `aws_servicecatalog_product_portfolio_association` | declaration-carried | in-contract |  |
| `aws_servicecatalog_provisioned_product` | marker-carried | in-contract |  |
| `aws_servicecatalog_provisioning_artifact` | record-carried | in-contract |  |
| `aws_servicecatalog_service_action` | record-carried | in-contract |  |
| `aws_servicecatalog_tag_option` | record-carried | in-contract |  |
| `aws_servicecatalog_tag_option_resource_association` | declaration-carried | in-contract |  |
| `aws_servicecatalogappregistry_application` | marker-carried | in-contract |  |
| `aws_servicecatalogappregistry_attribute_group` | marker-carried | in-contract |  |
| `aws_servicecatalogappregistry_attribute_group_association` | declaration-carried | in-contract |  |
| `aws_servicequotas_auto_management` | declaration-carried | in-contract |  |
| `aws_servicequotas_service_quota` | declaration-carried | in-contract |  |
| `aws_servicequotas_template` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_servicequotas_template_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_active_receipt_rule_set` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_configuration_set` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_domain_dkim` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_domain_identity` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_domain_identity_verification` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_domain_mail_from` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_email_identity` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_event_destination` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_identity_notification_topic` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ses_identity_policy` | declaration-carried | in-contract |  |
| `aws_ses_receipt_filter` | declaration-carried | in-contract |  |
| `aws_ses_receipt_rule` | declaration-carried | in-contract |  |
| `aws_ses_receipt_rule_set` | declaration-carried | in-contract |  |
| `aws_ses_template` | declaration-carried | in-contract |  |
| `aws_sesv2_account_suppression_attributes` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sesv2_account_vdm_attributes` | declaration-carried | in-contract |  |
| `aws_sesv2_configuration_set` | marker-carried | in-contract |  |
| `aws_sesv2_configuration_set_event_destination` | record-carried | in-contract |  |
| `aws_sesv2_contact_list` | marker-carried | in-contract |  |
| `aws_sesv2_dedicated_ip_assignment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sesv2_dedicated_ip_pool` | marker-carried | in-contract |  |
| `aws_sesv2_email_identity` | marker-carried | in-contract |  |
| `aws_sesv2_email_identity_feedback_attributes` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sesv2_email_identity_mail_from_attributes` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sesv2_email_identity_policy` | declaration-carried | in-contract |  |
| `aws_sesv2_tenant` | marker-carried | in-contract |  |
| `aws_sesv2_tenant_resource_association` | declaration-carried | in-contract |  |
| `aws_sfn_activity` | marker-carried | in-contract |  |
| `aws_sfn_alias` | record-carried | in-contract |  |
| `aws_sfn_state_machine` | marker-carried | in-contract |  |
| `aws_shield_application_layer_automatic_response` | declaration-carried | needs-evidence | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_shield_drt_access_log_bucket_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_shield_drt_access_role_arn_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_shield_proactive_engagement` | record-carried | in-contract |  |
| `aws_shield_protection` | marker-carried | in-contract |  |
| `aws_shield_protection_group` | marker-carried | in-contract |  |
| `aws_shield_protection_health_check_association` | declaration-carried | in-contract |  |
| `aws_shield_subscription` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_signer_signing_job` | record-carried | in-contract |  |
| `aws_signer_signing_profile` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_signer_signing_profile_permission` | declaration-carried | in-contract |  |
| `aws_snapshot_create_volume_permission` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sns_platform_application` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sns_sms_preferences` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sns_topic` | marker-carried | in-contract |  |
| `aws_sns_topic_data_protection_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sns_topic_policy` | declaration-carried | in-contract |  |
| `aws_sns_topic_subscription` | record-carried | in-contract |  |
| `aws_spot_datafeed_subscription` | declaration-carried | in-contract |  |
| `aws_spot_fleet_request` | marker-carried | in-contract |  |
| `aws_spot_instance_request` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sqs_queue` | marker-carried | in-contract |  |
| `aws_sqs_queue_policy` | declaration-carried | in-contract |  |
| `aws_sqs_queue_redrive_allow_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_sqs_queue_redrive_policy` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ssm_activation` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ssm_association` | marker-carried | in-contract |  |
| `aws_ssm_default_patch_baseline` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ssm_document` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ssm_maintenance_window` | marker-carried | in-contract |  |
| `aws_ssm_maintenance_window_target` | declaration-carried | in-contract |  |
| `aws_ssm_maintenance_window_task` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_ssm_parameter` | marker-carried | in-contract |  |
| `aws_ssm_patch_baseline` | marker-carried | in-contract |  |
| `aws_ssm_patch_group` | declaration-carried | in-contract |  |
| `aws_ssm_resource_data_sync` | declaration-carried | in-contract |  |
| `aws_ssm_service_setting` | declaration-carried | in-contract |  |
| `aws_ssmcontacts_contact` | marker-carried | in-contract |  |
| `aws_ssmcontacts_contact_channel` | record-carried | in-contract |  |
| `aws_ssmcontacts_plan` | record-carried | in-contract |  |
| `aws_ssmcontacts_rotation` | marker-carried | in-contract |  |
| `aws_ssmincidents_replication_set` | marker-carried | in-contract |  |
| `aws_ssmincidents_response_plan` | marker-carried | in-contract |  |
| `aws_ssmquicksetup_configuration_manager` | marker-carried | in-contract |  |
| `aws_ssoadmin_account_assignment` | declaration-carried | in-contract |  |
| `aws_ssoadmin_application` | marker-carried | in-contract |  |
| `aws_ssoadmin_application_access_scope` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ssoadmin_application_assignment` | declaration-carried | in-contract |  |
| `aws_ssoadmin_application_assignment_configuration` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ssoadmin_customer_managed_policy_attachment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_ssoadmin_customer_managed_policy_attachments_exclusive` | declaration-carried | in-contract |  |
| `aws_ssoadmin_instance_access_control_attributes` | declaration-carried | in-contract |  |
| `aws_ssoadmin_managed_policy_attachment` | declaration-carried | in-contract |  |
| `aws_ssoadmin_managed_policy_attachments_exclusive` | declaration-carried | in-contract |  |
| `aws_ssoadmin_permission_set` | marker-carried | in-contract |  |
| `aws_ssoadmin_permission_set_inline_policy` | declaration-carried | in-contract |  |
| `aws_ssoadmin_permissions_boundary_attachment` | declaration-carried | in-contract |  |
| `aws_ssoadmin_region` | declaration-carried | in-contract |  |
| `aws_ssoadmin_trusted_token_issuer` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_cache` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_cached_iscsi_volume` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_file_system_association` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_gateway` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_nfs_file_share` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_smb_file_share` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_stored_iscsi_volume` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_tape_pool` | marker-carried | in-contract |  |
| `aws_storagegateway_upload_buffer` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_storagegateway_working_storage` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_subnet` | marker-carried | in-contract |  |
| `aws_swf_domain` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_synthetics_canary` | marker-carried | in-contract |  |
| `aws_synthetics_group` | marker-carried | in-contract |  |
| `aws_synthetics_group_association` | declaration-carried | in-contract |  |
| `aws_timestreaminfluxdb_db_cluster` | marker-carried | in-contract |  |
| `aws_timestreaminfluxdb_db_instance` | marker-carried | in-contract |  |
| `aws_timestreamquery_scheduled_query` | marker-carried | in-contract |  |
| `aws_timestreamwrite_database` | marker-carried | in-contract |  |
| `aws_timestreamwrite_table` | marker-carried | in-contract |  |
| `aws_transcribe_language_model` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_transcribe_medical_vocabulary` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_transcribe_vocabulary` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_transcribe_vocabulary_filter` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_transfer_access` | declaration-carried | in-contract |  |
| `aws_transfer_agreement` | marker-carried | in-contract |  |
| `aws_transfer_certificate` | marker-carried | in-contract |  |
| `aws_transfer_connector` | marker-carried | in-contract |  |
| `aws_transfer_host_key` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_transfer_profile` | marker-carried | in-contract |  |
| `aws_transfer_server` | marker-carried | in-contract |  |
| `aws_transfer_ssh_key` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_transfer_tag` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_transfer_user` | marker-carried | in-contract |  |
| `aws_transfer_web_app` | marker-carried | in-contract |  |
| `aws_transfer_web_app_customization` | declaration-carried | in-contract |  |
| `aws_transfer_workflow` | marker-carried | in-contract |  |
| `aws_uxc_account_customizations` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_verifiedaccess_endpoint` | marker-carried | in-contract |  |
| `aws_verifiedaccess_group` | marker-carried | in-contract |  |
| `aws_verifiedaccess_instance` | marker-carried | in-contract |  |
| `aws_verifiedaccess_instance_logging_configuration` | declaration-carried | in-contract |  |
| `aws_verifiedaccess_instance_trust_provider_attachment` | declaration-carried | in-contract |  |
| `aws_verifiedaccess_trust_provider` | marker-carried | in-contract |  |
| `aws_verifiedpermissions_identity_source` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_verifiedpermissions_policy` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_verifiedpermissions_policy_store` | marker-carried | in-contract |  |
| `aws_verifiedpermissions_policy_template` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_verifiedpermissions_schema` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_volume_attachment` | declaration-carried | in-contract |  |
| `aws_vpc` | marker-carried | in-contract |  |
| `aws_vpc_block_public_access_exclusion` | marker-carried | in-contract |  |
| `aws_vpc_block_public_access_options` | declaration-carried | in-contract |  |
| `aws_vpc_dhcp_options` | marker-carried | in-contract |  |
| `aws_vpc_dhcp_options_association` | declaration-carried | in-contract |  |
| `aws_vpc_encryption_control` | marker-carried | in-contract |  |
| `aws_vpc_endpoint` | marker-carried | in-contract |  |
| `aws_vpc_endpoint_connection_accepter` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_endpoint_connection_notification` | record-carried | in-contract |  |
| `aws_vpc_endpoint_policy` | declaration-carried | in-contract |  |
| `aws_vpc_endpoint_private_dns` | declaration-carried | in-contract |  |
| `aws_vpc_endpoint_route_table_association` | declaration-carried | in-contract |  |
| `aws_vpc_endpoint_security_group_association` | declaration-carried | in-contract |  |
| `aws_vpc_endpoint_service` | marker-carried | in-contract |  |
| `aws_vpc_endpoint_service_allowed_principal` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_endpoint_service_private_dns_verification` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_endpoint_subnet_association` | declaration-carried | in-contract |  |
| `aws_vpc_ipam` | marker-carried | in-contract |  |
| `aws_vpc_ipam_organization_admin_account` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_ipam_pool` | marker-carried | in-contract |  |
| `aws_vpc_ipam_pool_cidr` | declaration-carried | in-contract |  |
| `aws_vpc_ipam_pool_cidr_allocation` | marker-carried | in-contract |  |
| `aws_vpc_ipam_preview_next_cidr` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_ipam_resource_discovery` | marker-carried | in-contract |  |
| `aws_vpc_ipam_resource_discovery_association` | marker-carried | in-contract |  |
| `aws_vpc_ipam_scope` | marker-carried | in-contract |  |
| `aws_vpc_ipv4_cidr_block_association` | record-carried | in-contract |  |
| `aws_vpc_ipv6_cidr_block_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_network_performance_metric_subscription` | record-carried | needs-separator | a composite identity with no worked import example to read its separator from. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_peering_connection` | marker-carried | in-contract |  |
| `aws_vpc_peering_connection_accepter` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_peering_connection_options` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpc_route_server` | marker-carried | in-contract |  |
| `aws_vpc_route_server_endpoint` | marker-carried | in-contract |  |
| `aws_vpc_route_server_peer` | marker-carried | in-contract |  |
| `aws_vpc_route_server_propagation` | declaration-carried | in-contract |  |
| `aws_vpc_route_server_vpc_association` | declaration-carried | in-contract |  |
| `aws_vpc_security_group_egress_rule` | marker-carried | in-contract |  |
| `aws_vpc_security_group_ingress_rule` | marker-carried | in-contract |  |
| `aws_vpc_security_group_rules_exclusive` | declaration-carried | in-contract |  |
| `aws_vpc_security_group_vpc_association` | declaration-carried | in-contract |  |
| `aws_vpclattice_access_log_subscription` | marker-carried | in-contract |  |
| `aws_vpclattice_auth_policy` | declaration-carried | in-contract |  |
| `aws_vpclattice_domain_verification` | marker-carried | in-contract |  |
| `aws_vpclattice_listener` | marker-carried | in-contract |  |
| `aws_vpclattice_listener_rule` | marker-carried | in-contract |  |
| `aws_vpclattice_resource_configuration` | marker-carried | in-contract |  |
| `aws_vpclattice_resource_gateway` | marker-carried | in-contract |  |
| `aws_vpclattice_resource_policy` | declaration-carried | in-contract |  |
| `aws_vpclattice_service` | marker-carried | in-contract |  |
| `aws_vpclattice_service_network` | marker-carried | in-contract |  |
| `aws_vpclattice_service_network_resource_association` | marker-carried | in-contract |  |
| `aws_vpclattice_service_network_service_association` | marker-carried | in-contract |  |
| `aws_vpclattice_service_network_vpc_association` | marker-carried | in-contract |  |
| `aws_vpclattice_target_group` | marker-carried | in-contract |  |
| `aws_vpclattice_target_group_attachment` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpn_concentrator` | marker-carried | in-contract |  |
| `aws_vpn_connection` | marker-carried | in-contract |  |
| `aws_vpn_connection_route` | record-carried | needs-separator | a composite identity with no worked import example to read its separator from. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_vpn_gateway` | marker-carried | in-contract |  |
| `aws_vpn_gateway_attachment` | record-carried | in-contract |  |
| `aws_vpn_gateway_route_propagation` | record-carried | in-contract |  |
| `aws_waf_byte_match_set` | record-carried | in-contract |  |
| `aws_waf_geo_match_set` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_waf_ipset` | record-carried | in-contract |  |
| `aws_waf_rate_based_rule` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_waf_regex_match_set` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_waf_regex_pattern_set` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_waf_rule` | marker-carried | in-contract |  |
| `aws_waf_rule_group` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_waf_size_constraint_set` | record-carried | in-contract |  |
| `aws_waf_sql_injection_match_set` | record-carried | in-contract |  |
| `aws_waf_web_acl` | marker-carried | in-contract |  |
| `aws_waf_xss_match_set` | record-carried | in-contract |  |
| `aws_wafregional_byte_match_set` | record-carried | in-contract |  |
| `aws_wafregional_geo_match_set` | record-carried | in-contract |  |
| `aws_wafregional_ipset` | record-carried | in-contract |  |
| `aws_wafregional_rate_based_rule` | marker-carried | in-contract |  |
| `aws_wafregional_regex_match_set` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_wafregional_regex_pattern_set` | record-carried | in-contract |  |
| `aws_wafregional_rule` | marker-carried | in-contract |  |
| `aws_wafregional_rule_group` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_wafregional_size_constraint_set` | record-carried | in-contract |  |
| `aws_wafregional_sql_injection_match_set` | record-carried | in-contract |  |
| `aws_wafregional_web_acl` | marker-carried | in-contract |  |
| `aws_wafregional_web_acl_association` | declaration-carried | in-contract |  |
| `aws_wafregional_xss_match_set` | record-carried | in-contract |  |
| `aws_wafv2_api_key` | excluded by design | excluded | excluded by design: generates credential material this fork can never read back and verify again (maintainer ruling, 2026-08-15, issue #175). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_wafv2_ip_set` | marker-carried | in-contract |  |
| `aws_wafv2_regex_pattern_set` | marker-carried | in-contract |  |
| `aws_wafv2_rule_group` | marker-carried | in-contract |  |
| `aws_wafv2_web_acl` | marker-carried | in-contract |  |
| `aws_wafv2_web_acl_association` | declaration-carried | in-contract |  |
| `aws_wafv2_web_acl_logging_configuration` | declaration-carried | in-contract |  |
| `aws_wafv2_web_acl_rule` | declaration-carried | in-contract |  |
| `aws_wafv2_web_acl_rule_group_association` | record-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_workmail_default_domain` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_workmail_domain` | declaration-carried | in-contract |  |
| `aws_workmail_group` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_workmail_organization` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_workmail_user` | record-carried | pending-mechanism | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| `aws_workspaces_connection_alias` | marker-carried | in-contract |  |
| `aws_workspaces_directory` | marker-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_workspaces_ip_group` | marker-carried | in-contract |  |
| `aws_workspaces_pool` | marker-carried | in-contract |  |
| `aws_workspaces_workspace` | marker-carried | in-contract |  |
| `aws_workspacesweb_browser_settings` | marker-carried | in-contract |  |
| `aws_workspacesweb_browser_settings_association` | declaration-carried | in-contract |  |
| `aws_workspacesweb_data_protection_settings` | marker-carried | in-contract |  |
| `aws_workspacesweb_data_protection_settings_association` | declaration-carried | in-contract |  |
| `aws_workspacesweb_identity_provider` | marker-carried | in-contract |  |
| `aws_workspacesweb_ip_access_settings` | marker-carried | in-contract |  |
| `aws_workspacesweb_ip_access_settings_association` | declaration-carried | in-contract |  |
| `aws_workspacesweb_network_settings` | marker-carried | in-contract |  |
| `aws_workspacesweb_network_settings_association` | declaration-carried | in-contract |  |
| `aws_workspacesweb_portal` | marker-carried | in-contract |  |
| `aws_workspacesweb_session_logger` | marker-carried | in-contract |  |
| `aws_workspacesweb_session_logger_association` | declaration-carried | in-contract |  |
| `aws_workspacesweb_trust_store` | marker-carried | in-contract |  |
| `aws_workspacesweb_trust_store_association` | declaration-carried | in-contract |  |
| `aws_workspacesweb_user_access_logging_settings` | marker-carried | in-contract |  |
| `aws_workspacesweb_user_access_logging_settings_association` | declaration-carried | in-contract |  |
| `aws_workspacesweb_user_settings` | marker-carried | in-contract |  |
| `aws_workspacesweb_user_settings_association` | declaration-carried | in-contract |  |
| `aws_xray_encryption_config` | declaration-carried | in-contract |  |
| `aws_xray_group` | marker-carried | in-contract |  |
| `aws_xray_indexing_rule` | declaration-carried | pending-ratification | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| `aws_xray_resource_policy` | declaration-carried | in-contract |  |
| `aws_xray_sampling_rule` | marker-carried | in-contract |  |
| `aws_xray_trace_segment_destination` | declaration-carried | in-contract |  |

</div>
<!-- readiness-gen:end readiness-types -->
