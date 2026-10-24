# Readiness tiers

Every provider resource type the roster knows about, with its tier and its
status. The tiers are marker-carried, declaration-carried, record-carried and
excluded by design. A type short of in-contract carries a reason;
[LIMITATIONS.md](LIMITATIONS.md) has the detail behind each one.

Rendered from `live/readiness.json` by `go run ./tools/readiness-gen -render`
(the data) and `go run ./tools/gauntlet render` (this page). Edit the text
around the generated markers, never inside them.

## Tier by status

<!-- readiness-gen:begin tiers -->
| Tier | in-contract | pending-ratification | needs-separator | needs-evidence | pending-mechanism | excluded | Total |
|---|---|---|---|---|---|---|---|
| marker-carried | 682 | 161 | 1 | 2 | 0 | 0 | 846 |
| declaration-carried | 341 | 37 | 0 | 1 | 0 | 0 | 379 |
| record-carried | 96 | 294 | 3 | 16 | 62 | 0 | 471 |
| excluded by design | 0 | 0 | 0 | 0 | 0 | 3 | 3 |
| Total | 1119 | 492 | 4 | 19 | 62 | 3 | 1699 |

`live/readiness.json` last committed at commit `03ec86e17b` on 2026-10-23T21:48:50-06:00. Regenerate with `go run ./tools/readiness-gen` and re-render with `go run ./tools/readiness-gen -render` before trusting this against a newer commit.
<!-- readiness-gen:end tiers -->

## How each type comes back

The tier says what carries a type's identity. This table splits each tier
by what the identity is rebuilt from on a run with no cache. It is read
from the same facts as the tier (`live/survey-full.json`, the identity
table, `live/mapping.json` for a parent's service) by
`tools/readiness-gen/paths.go`, and nothing stores it.

<!-- readiness-gen:begin paths -->
| How it comes back | Tier | Types | What it means |
|---|---|---|---|
| marker | marker-carried | 846 | The `tofu-address` tag on the object is the identity; the sweep reads it back. |
| client-named | declaration-carried | 160 | The configuration names it, so the name re-derives every run. |
| parent-derived | declaration-carried | 184 | Composed over a parent type the configuration also declares. |
| account-derived | declaration-carried | 31 | The configuration plus the run's account or region rebuilds it. |
| unique-name | declaration-carried | 4 | AWS refuses to issue the name twice in an account and region, so discovery binds by name. |
| server-minted | record-carried | 158 | The API mints the identity and the object carries no marker; only a record holds it. |
| unbindable | record-carried | 313 | No marker, and the declaration does not rebuild it; a record is the only copy. |
| excluded by design | excluded by design | 3 | A credential this fork declines to manage. |
<!-- readiness-gen:end paths -->

## Two counts called record-carried

Two populations share the record-carried tier's name. `identity.MarkerlessTypes`
(`internal/live/identity/markerless_generated.go`) is **159** types, derived
from taggability and server-assignment. The table above counts **471**:
the 158 of those 159 that land here (the 159th, `aws_wafv2_api_key`, is
excluded by design instead) plus **313** more `tools/readiness-gen` adds by
elimination - untaggable, unadmitted, and in no other tier. **159** answers
"how many types does the located mechanism cover"; **471** answers "how many types does this tier show". Recount from
`live/readiness.json` (`facts.markerless`, `tier`) and
`markerless_generated.go`; `live/readiness_docs_pin_test.go` fails when
these figures move.

## Kubernetes

The Kubernetes substrate's admitted types are counted apart from the AWS
tiers above.

<!-- readiness-gen:begin k8s-types -->
hashicorp/kubernetes 3.2.1: 4 admitted type(s), 0 awaiting a tier ruling.

| Type | Tier | Status |
|---|---|---|
| `kubernetes_cluster_role_binding` | marker-carried | in-contract |
| `kubernetes_config_map` | marker-carried | in-contract |
| `kubernetes_namespace` | marker-carried | in-contract |
| `kubernetes_storage_class` | marker-carried | in-contract |
<!-- readiness-gen:end k8s-types -->

## Azure

The Azure family's two providers, counted apart from the AWS tiers above
and from each other (#2119, #1120): azurerm's rows are Terraform resource
types, azapi's are the ARM types one `azapi_resource` block can declare.
The rule is AWS's. A type that takes ARM tags is marker-carried. An
untaggable type whose ARM id the declaration supplies is
declaration-carried; in survey-path terms these are the client-named and
parent-derived shapes (an azurerm id is a template over the type's own
`name`, its `resource_group_name` and its parents' names or ids, the
subscription coming from the provider block the way an AWS account does).
An untaggable type nothing admits is record-carried. The 196 azurerm
types the provider serves an identity schema for (subnets, routes and
security rules among them) keep that route (#1122), which reads
`subscription_id` from the provider block. It admits 103 of them on the
schema alone and 78 through the generated template, once a guard has held
the template to the identity attributes azurerm's own identity tests read
(six of those templates take a segment's argument or constant from that
test, where the segment's name found none, #2138); the 13 with no template
and the 2 whose template the guard withholds are refused. Each type's route
is recorded in `live/azurerm-ids.json` (#1120 unit 17). A record can hold
the whole identity of 191 of the 196, each attribute the block does not
declare read from the source azurerm's identity test states for it (a
parent's id parsed by its id grammar, a renamed argument, a constant), and
refuses the other 5 by name (#2138). The sources and the guard's check of
them come from one `tools/azurerm-id-gen` run over azurerm's source, so
their agreement with azurerm's identity tests is a self-consistency check
of that run, not independent evidence. A parent's id is parsed as
azurerm's validator for the argument parses it, constant and fixed
segments checked, strictly or case-insensitively as that validator does
(#2140).

Thirty-seven azurerm parent-id settings objects (customer-managed keys,
network rules and ACLs, `subnet_*_association`, API Management `*_policy`
and the like) and `azapi_data_plane_resource` are record-carried by
declaration (#2203, the 2026-10-23 ruling on #2201). Each takes its
parent's ARM id as its own, or has no ARM id at all, and has no tags
argument, so no marker can tell it from the parent. The list is
`identity.AzureRecordCarriedTypes`; with a `record_store` declared the
type is admitted through the record rung, which holds the applied
object's id under the block's address, so the object is located by type
and parent id as AWS's `MarkerlessTypes` members are. Without a
`record_store` it is refused as markerless. A listed type that gains a
tags argument leaves the route. 36 of the 37 azurerm types move to
in-contract here; `azurerm_eventgrid_partner_configuration` takes tags
and stays where it was. `azapi_data_plane_resource` is not an ARM type,
so it has no row in the azapi counts.

No stock carrier exists for an untaggable Azure type. azurerm and azapi
write nothing onto an ARM object but its own properties, and ARM keeps no
per-writer field the way Kubernetes keeps `managedFields`; `managedBy` is
a single property a parent service sets, already read as controller-held
evidence, and `systemData.createdBy` names the principal that wrote the
object, which several estates can share.

An untaggable, server-minted type is held by its record (record-located),
as on AWS, and refused by name only where no record can hold its
identity. No Azure type is minted outright: every admitted azurerm type's
id components are arguments the configuration sets (none is a
Computed-only attribute at 5.10.0), so the AWS markerless list gains no
Azure member. That holds only because no type whose id the server mints is
admitted (#2147). Seven admitted untaggable types make the naming argument
optional and mint a GUID when it is left out (`azurerm_management_group`,
`azurerm_role_definition` and five more); an instance that leaves it out
is record-located through the record store every live block implies, the
way an AWS `name_prefix` instance is. An `azapi_resource` with no `name`
is named by its provider block's `default_name`, read like its
`subscription_id`; with none, azapi refuses the block itself (#2142).
Seventeen unadmitted azurerm types take part of their id from the create
response, a UUID minted inside create, or an object looked up at apply
(#2147): among them `azurerm_key_vault_key`, `azurerm_key_vault_secret`,
`azurerm_key_vault_certificate` and
`azurerm_sentinel_threat_intelligence_indicator`. Each gets both of AWS's answers (#2205, the
2026-10-23 ruling): with a record_store the type is record-located, and the
five that carry the ARM tags map are also found by their marker without
one (`identity.AzureServerMintedTypes`). An untaggable member with no live
block is still refused. The Key Vault ones are not in Resource Graph;
`discovery.DataPlaneSweep` lists them behind an interface tested with
fakes, and its listing against a real vault is not run.

A removed declaration-carried block is found again through its record,
as on AWS: the Resource Graph pass runs the record-store removal leg
without AWS's admission-table sweep, and turns a record that holds an
identity schema's attributes back into the ARM id by the type's template.
A record missing a segment of that id is not guessed at; the plan warns,
naming the type and the estate. An untaggable ARM type declared through
`azapi_resource` is found the same way, from its `azapi_resource` record.

<!-- readiness-gen:begin azure -->
hashicorp/azurerm 5.10.0: 1106 resource types.

| Tier | in-contract | pending-ratification | needs-separator | needs-evidence | pending-mechanism | excluded | Total |
|---|---|---|---|---|---|---|---|
| marker-carried | 357 | 0 | 0 | 30 | 0 | 0 | 387 |
| declaration-carried | 492 | 0 | 0 | 0 | 0 | 0 | 492 |
| record-carried | 36 | 0 | 0 | 191 | 0 | 0 | 227 |
| excluded by design | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| Total | 885 | 0 | 0 | 221 | 0 | 0 | 1106 |

azure/azapi 2.13.0: 3397 ARM resource types declared through azapi_resource.

| Tier | in-contract | pending-ratification | needs-separator | needs-evidence | pending-mechanism | excluded | Total |
|---|---|---|---|---|---|---|---|
| marker-carried | 1031 | 0 | 0 | 0 | 0 | 0 | 1031 |
| declaration-carried | 2366 | 0 | 0 | 0 | 0 | 0 | 2366 |
| record-carried | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| excluded by design | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| Total | 3397 | 0 | 0 | 0 | 0 | 0 | 3397 |

Each azapi ARM type is tiered by its latest API version; 84 change taggability between versions, and the stamp reads the version a block names.

The fence for azurerm, by tier and by where the type lives:

| Tier | resource-group | from-argument | subscription | tenant | unknown | What Policy adds to the group RBAC |
|---|---|---|---|---|---|---|
| marker-carried | 375 | 1 | 1 | 3 | 7 | the deny on a write lacking or changing tofu-estate, and the delete lock when deleteLock is on |
| declaration-carried | 451 | 21 | 12 | 8 | 0 | nothing: Policy's Indexed mode never evaluates an untaggable type, and deleting its parent removes it unseen |
| record-carried | 168 | 20 | 5 | 3 | 31 | nothing, and the type is not admitted |
| excluded by design | 0 | 0 | 0 | 0 | 0 | not admitted |

| Scope | What the estate's group RBAC reaches |
|---|---|
| resource-group | the estate's group RBAC covers it |
| from-argument | covered when the argument names something inside one of the estate's groups, not otherwise |
| subscription | outside every group: the grant does not reach it |
| tenant | outside every group: the grant does not reach it |
| unknown | no id template, so where it lives is not known |

The azurerm types short of in-contract:

| Reason | Why the type is not in contract |
|---|---|
| 1 | no ARM id template could be read from azurerm's id parsers for it (live/azurerm-ids.json's reason), so it is refused as unadmitted. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| 2 | its id template is the parent's, or not an object's own, so admission withholds it rather than risk a wrong marker (internal/live/substrate's azurermRowShapeProblem). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| 3 | the identity-schema route keeps it (#1122), and refuses it: no ARM id template could be read for it to fill the identity schema's attributes no argument supplies. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |

| Type | Tier | Status | Reason |
|---|---|---|---|
| `azurerm_aadb2c_directory` | marker-carried | needs-evidence | 1 |
| `azurerm_active_directory_domain_service` | marker-carried | needs-evidence | 1 |
| `azurerm_active_directory_domain_service_replica_set` | record-carried | needs-evidence | 1 |
| `azurerm_advanced_threat_protection` | record-carried | needs-evidence | 1 |
| `azurerm_ai_foundry_project` | marker-carried | needs-evidence | 1 |
| `azurerm_api_management_api_diagnostic` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_api_tag_description` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_custom_domain` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_diagnostic` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_gateway_api` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_gateway_certificate_authority` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_identity_provider_aad` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_identity_provider_aadb2c` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_identity_provider_facebook` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_identity_provider_google` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_identity_provider_microsoft` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_identity_provider_twitter` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_notification_recipient_email` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_notification_recipient_user` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_product_api` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_product_group` | record-carried | needs-evidence | 1 |
| `azurerm_api_management_subscription` | record-carried | needs-evidence | 1 |
| `azurerm_app_configuration_feature` | marker-carried | needs-evidence | 1 |
| `azurerm_app_configuration_key` | marker-carried | needs-evidence | 1 |
| `azurerm_app_service_certificate_binding` | record-carried | needs-evidence | 1 |
| `azurerm_app_service_managed_certificate` | marker-carried | needs-evidence | 1 |
| `azurerm_app_service_public_certificate` | record-carried | needs-evidence | 1 |
| `azurerm_app_service_slot_custom_hostname_binding` | record-carried | needs-evidence | 1 |
| `azurerm_app_service_slot_virtual_network_swift_connection` | record-carried | needs-evidence | 1 |
| `azurerm_app_service_source_control_slot` | record-carried | needs-evidence | 1 |
| `azurerm_application_insights_analytics_item` | record-carried | needs-evidence | 2 |
| `azurerm_arc_kubernetes_cluster_extension` | record-carried | needs-evidence | 1 |
| `azurerm_arc_machine_automanage_configuration_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_automation_hybrid_runbook_worker` | record-carried | needs-evidence | 1 |
| `azurerm_automation_job_schedule` | record-carried | needs-evidence | 1 |
| `azurerm_backup_container_storage_account` | record-carried | needs-evidence | 1 |
| `azurerm_backup_policy_file_share` | record-carried | needs-evidence | 1 |
| `azurerm_backup_policy_vm_workload` | record-carried | needs-evidence | 1 |
| `azurerm_backup_protected_file_share` | record-carried | needs-evidence | 1 |
| `azurerm_backup_protected_vm` | record-carried | needs-evidence | 1 |
| `azurerm_blueprint_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_alexa` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_direct_line_speech` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_directline` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_email` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_facebook` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_line` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_ms_teams` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_slack` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_sms` | record-carried | needs-evidence | 1 |
| `azurerm_bot_channel_web_chat` | record-carried | needs-evidence | 1 |
| `azurerm_cdn_frontdoor_custom_domain_association` | record-carried | needs-evidence | 1 |
| `azurerm_chaos_studio_capability` | record-carried | needs-evidence | 1 |
| `azurerm_chaos_studio_target` | record-carried | needs-evidence | 1 |
| `azurerm_communication_service_email_domain_association` | record-carried | needs-evidence | 1 |
| `azurerm_container_registry_task_schedule_run_now` | record-carried | needs-evidence | 1 |
| `azurerm_container_registry_token_password` | record-carried | needs-evidence | 1 |
| `azurerm_cosmosdb_mongo_role_definition` | record-carried | needs-evidence | 1 |
| `azurerm_cosmosdb_mongo_user_definition` | record-carried | needs-evidence | 1 |
| `azurerm_cosmosdb_sql_dedicated_gateway` | record-carried | needs-evidence | 1 |
| `azurerm_cost_anomaly_alert` | record-carried | needs-evidence | 1 |
| `azurerm_cost_management_scheduled_action` | record-carried | needs-evidence | 1 |
| `azurerm_data_factory_managed_private_endpoint` | record-carried | needs-evidence | 1 |
| `azurerm_dev_test_global_vm_shutdown_schedule` | marker-carried | needs-evidence | 1 |
| `azurerm_dns_aaaa_record` | marker-carried | needs-evidence | 1 |
| `azurerm_dns_caa_record` | marker-carried | needs-evidence | 1 |
| `azurerm_dns_mx_record` | marker-carried | needs-evidence | 1 |
| `azurerm_dns_ns_record` | marker-carried | needs-evidence | 1 |
| `azurerm_dns_ptr_record` | marker-carried | needs-evidence | 1 |
| `azurerm_dns_srv_record` | marker-carried | needs-evidence | 1 |
| `azurerm_eventgrid_partner_configuration` | marker-carried | needs-evidence | 2 |
| `azurerm_eventgrid_system_topic_event_subscription` | record-carried | needs-evidence | 1 |
| `azurerm_express_route_circuit_peering` | record-carried | needs-evidence | 1 |
| `azurerm_frontdoor_custom_https_configuration` | record-carried | needs-evidence | 1 |
| `azurerm_function_app_active_slot` | record-carried | needs-evidence | 1 |
| `azurerm_function_app_hybrid_connection` | record-carried | needs-evidence | 1 |
| `azurerm_iotcentral_organization` | record-carried | needs-evidence | 1 |
| `azurerm_iothub_dps_certificate` | record-carried | needs-evidence | 1 |
| `azurerm_iothub_enrichment` | record-carried | needs-evidence | 1 |
| `azurerm_iothub_fallback_route` | record-carried | needs-evidence | 1 |
| `azurerm_ip_group_cidr` | record-carried | needs-evidence | 1 |
| `azurerm_key_vault_access_policy` | record-carried | needs-evidence | 1 |
| `azurerm_key_vault_certificate` | marker-carried | needs-evidence | 1 |
| `azurerm_key_vault_certificate_contacts` | record-carried | needs-evidence | 1 |
| `azurerm_key_vault_certificate_issuer` | record-carried | needs-evidence | 1 |
| `azurerm_key_vault_key` | marker-carried | needs-evidence | 1 |
| `azurerm_key_vault_managed_hardware_security_module_key` | marker-carried | needs-evidence | 1 |
| `azurerm_key_vault_managed_hardware_security_module_key_rotation_policy` | record-carried | needs-evidence | 1 |
| `azurerm_key_vault_managed_hardware_security_module_role_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_key_vault_managed_hardware_security_module_role_definition` | record-carried | needs-evidence | 1 |
| `azurerm_key_vault_managed_storage_account` | marker-carried | needs-evidence | 1 |
| `azurerm_key_vault_managed_storage_account_sas_token_definition` | marker-carried | needs-evidence | 1 |
| `azurerm_key_vault_secret` | marker-carried | needs-evidence | 1 |
| `azurerm_kubernetes_cluster_extension` | record-carried | needs-evidence | 1 |
| `azurerm_log_analytics_data_export_rule` | record-carried | needs-evidence | 1 |
| `azurerm_log_analytics_linked_service` | record-carried | needs-evidence | 1 |
| `azurerm_maintenance_assignment_dedicated_host` | record-carried | needs-evidence | 1 |
| `azurerm_maintenance_assignment_virtual_machine` | record-carried | needs-evidence | 1 |
| `azurerm_maintenance_assignment_virtual_machine_scale_set` | record-carried | needs-evidence | 1 |
| `azurerm_managed_redis_access_policy_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_management_group_subscription_association` | record-carried | needs-evidence | 1 |
| `azurerm_marketplace_agreement` | record-carried | needs-evidence | 1 |
| `azurerm_marketplace_role_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_mongo_cluster_user` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_database_extended_auditing_policy` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_job_agent` | marker-carried | needs-evidence | 3 |
| `azurerm_mssql_managed_instance_active_directory_administrator` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_managed_instance_failover_group` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_managed_instance_security_alert_policy` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_managed_instance_start_stop_schedule` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_managed_instance_transparent_data_encryption` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_managed_instance_vulnerability_assessment` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_server_extended_auditing_policy` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_server_microsoft_support_auditing_policy` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_server_security_alert_policy` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_server_transparent_data_encryption` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_server_vulnerability_assessment` | record-carried | needs-evidence | 1 |
| `azurerm_mssql_virtual_machine` | marker-carried | needs-evidence | 3 |
| `azurerm_mysql_flexible_server_active_directory_administrator` | record-carried | needs-evidence | 1 |
| `azurerm_nat_gateway_public_ip_association` | record-carried | needs-evidence | 1 |
| `azurerm_nat_gateway_public_ip_prefix_association` | record-carried | needs-evidence | 1 |
| `azurerm_netapp_volume_bucket` | record-carried | needs-evidence | 2 |
| `azurerm_netapp_volume_bucket_with_server` | record-carried | needs-evidence | 2 |
| `azurerm_network_interface_application_gateway_backend_address_pool_association` | record-carried | needs-evidence | 1 |
| `azurerm_network_interface_application_security_group_association` | record-carried | needs-evidence | 1 |
| `azurerm_network_interface_backend_address_pool_association` | record-carried | needs-evidence | 1 |
| `azurerm_network_interface_nat_rule_association` | record-carried | needs-evidence | 1 |
| `azurerm_network_interface_security_group_association` | record-carried | needs-evidence | 1 |
| `azurerm_network_manager_deployment` | record-carried | needs-evidence | 1 |
| `azurerm_network_security_perimeter_association` | record-carried | needs-evidence | 1 |
| `azurerm_new_relic_tag_rule` | record-carried | needs-evidence | 1 |
| `azurerm_nginx_configuration` | record-carried | needs-evidence | 1 |
| `azurerm_palo_alto_local_rulestack_outbound_trust_certificate_association` | record-carried | needs-evidence | 1 |
| `azurerm_palo_alto_local_rulestack_outbound_untrust_certificate_association` | record-carried | needs-evidence | 1 |
| `azurerm_palo_alto_virtual_network_appliance` | record-carried | needs-evidence | 1 |
| `azurerm_pim_active_role_assignment` | record-carried | needs-evidence | 2 |
| `azurerm_pim_eligible_role_assignment` | record-carried | needs-evidence | 2 |
| `azurerm_policy_definition` | record-carried | needs-evidence | 1 |
| `azurerm_portal_tenant_configuration` | record-carried | needs-evidence | 1 |
| `azurerm_postgresql_flexible_server_virtual_endpoint` | record-carried | needs-evidence | 1 |
| `azurerm_private_dns_a_record` | marker-carried | needs-evidence | 3 |
| `azurerm_private_dns_aaaa_record` | marker-carried | needs-evidence | 1 |
| `azurerm_private_dns_cname_record` | marker-carried | needs-evidence | 3 |
| `azurerm_private_dns_mx_record` | marker-carried | needs-evidence | 1 |
| `azurerm_private_dns_ptr_record` | marker-carried | needs-evidence | 1 |
| `azurerm_private_dns_srv_record` | marker-carried | needs-evidence | 1 |
| `azurerm_private_dns_txt_record` | marker-carried | needs-evidence | 1 |
| `azurerm_private_endpoint_application_security_group_association` | record-carried | needs-evidence | 1 |
| `azurerm_recovery_services_vault_resource_guard_association` | record-carried | needs-evidence | 1 |
| `azurerm_redis_linked_server` | record-carried | needs-evidence | 1 |
| `azurerm_role_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_role_management_policy` | record-carried | needs-evidence | 2 |
| `azurerm_security_center_assessment` | record-carried | needs-evidence | 1 |
| `azurerm_security_center_assessment_policy` | record-carried | needs-evidence | 1 |
| `azurerm_security_center_server_vulnerability_assessment_virtual_machine` | record-carried | needs-evidence | 1 |
| `azurerm_security_center_server_vulnerability_assessments_setting` | record-carried | needs-evidence | 1 |
| `azurerm_security_center_storage_defender` | record-carried | needs-evidence | 1 |
| `azurerm_security_center_subscription_pricing` | record-carried | needs-evidence | 1 |
| `azurerm_security_center_workspace` | record-carried | needs-evidence | 1 |
| `azurerm_sentinel_alert_rule_anomaly_duplicate` | record-carried | needs-evidence | 1 |
| `azurerm_sentinel_alert_rule_fusion` | record-carried | needs-evidence | 1 |
| `azurerm_sentinel_log_analytics_workspace_onboarding` | record-carried | needs-evidence | 1 |
| `azurerm_sentinel_threat_intelligence_indicator` | record-carried | needs-evidence | 1 |
| `azurerm_servicebus_namespace_disaster_recovery_config` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_fabric` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_hyperv_network_mapping` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_hyperv_replication_policy` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_hyperv_replication_policy_association` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_network_mapping` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_protection_container` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_protection_container_mapping` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_replicated_vm` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_replication_policy` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_replication_recovery_plan` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_services_vault_hyperv_site` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_vmware_replicated_vm` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_vmware_replication_policy` | record-carried | needs-evidence | 1 |
| `azurerm_site_recovery_vmware_replication_policy_association` | record-carried | needs-evidence | 1 |
| `azurerm_source_control_token` | record-carried | needs-evidence | 1 |
| `azurerm_spring_cloud_builder` | record-carried | needs-evidence | 1 |
| `azurerm_stack_hci_deployment_setting` | record-carried | needs-evidence | 1 |
| `azurerm_static_web_app_custom_domain` | record-carried | needs-evidence | 1 |
| `azurerm_static_web_app_function_app_registration` | record-carried | needs-evidence | 1 |
| `azurerm_storage_blob` | record-carried | needs-evidence | 1 |
| `azurerm_storage_container_immutability_policy` | record-carried | needs-evidence | 1 |
| `azurerm_storage_data_lake_gen2_filesystem` | record-carried | needs-evidence | 1 |
| `azurerm_storage_data_lake_gen2_path` | record-carried | needs-evidence | 1 |
| `azurerm_storage_management_policy` | record-carried | needs-evidence | 1 |
| `azurerm_storage_object_replication` | record-carried | needs-evidence | 1 |
| `azurerm_storage_share_directory` | record-carried | needs-evidence | 1 |
| `azurerm_storage_share_file` | record-carried | needs-evidence | 1 |
| `azurerm_storage_table_entity` | record-carried | needs-evidence | 1 |
| `azurerm_stream_analytics_job_schedule` | record-carried | needs-evidence | 1 |
| `azurerm_subscription` | marker-carried | needs-evidence | 1 |
| `azurerm_synapse_managed_private_endpoint` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_role_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_sql_pool_extended_auditing_policy` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_sql_pool_security_alert_policy` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_sql_pool_vulnerability_assessment` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_workspace_aad_admin` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_workspace_extended_auditing_policy` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_workspace_key` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_workspace_security_alert_policy` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_workspace_sql_aad_admin` | record-carried | needs-evidence | 1 |
| `azurerm_synapse_workspace_vulnerability_assessment` | record-carried | needs-evidence | 1 |
| `azurerm_system_center_virtual_machine_manager_virtual_machine_instance` | record-carried | needs-evidence | 1 |
| `azurerm_system_center_virtual_machine_manager_virtual_machine_instance_guest_agent` | record-carried | needs-evidence | 1 |
| `azurerm_traffic_manager_azure_endpoint` | record-carried | needs-evidence | 1 |
| `azurerm_traffic_manager_external_endpoint` | record-carried | needs-evidence | 1 |
| `azurerm_traffic_manager_nested_endpoint` | record-carried | needs-evidence | 1 |
| `azurerm_virtual_desktop_host_pool_registration_info` | record-carried | needs-evidence | 1 |
| `azurerm_virtual_desktop_scaling_plan_host_pool_association` | record-carried | needs-evidence | 1 |
| `azurerm_virtual_desktop_workspace_application_group_association` | record-carried | needs-evidence | 1 |
| `azurerm_virtual_hub_route_table_route` | record-carried | needs-evidence | 1 |
| `azurerm_virtual_machine_automanage_configuration_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_virtual_machine_data_disk_attachment` | record-carried | needs-evidence | 1 |
| `azurerm_virtual_machine_gallery_application_assignment` | record-carried | needs-evidence | 1 |
| `azurerm_virtual_network_dns_servers` | record-carried | needs-evidence | 1 |
| `azurerm_vmware_cluster` | record-carried | needs-evidence | 1 |
| `azurerm_web_app_active_slot` | record-carried | needs-evidence | 1 |
| `azurerm_web_app_hybrid_connection` | record-carried | needs-evidence | 1 |
<!-- readiness-gen:end azure -->

## Google Cloud

hashicorp/google and google-beta, counted apart from the AWS tiers above.
A type with the google-labels surface (a settable `labels` beside the
computed `effective_labels`) is marker-carried. An unlabelled type is
declaration-carried when its row's id, or the identity-schema route,
re-derives it from the declaration, and record-carried otherwise. Rows and
reasons come from `tools/gcp-row-gen` (`live/gcp-survey.json`), read from
the provider's own source at the pin.

<!-- readiness-gen:begin gcp-paths -->
| How it comes back | Tier | Types | What it means |
|---|---|---|---|
| marker | marker-carried | 318 | The `tofu-address` label on the object is the identity; the sweep reads it back. |
| client-named | declaration-carried | 337 | The configuration names it, under a project or location it also names or the provider supplies. |
| parent-derived | declaration-carried | 597 | Composed over a containing resource the configuration also names; every IAM type is this shape. |
| project-derived | declaration-carried | 24 | Only project, region or zone names it: one per project or region. |
| unique-name | - | 0 | Nothing in the google source states a name the API refuses to issue twice, so no type is here. |
| server-minted | record-carried | 135 | The API mints the identity and the type has no labels; only a record holds it. |
| unbindable | record-carried | 74 | No row and no identity-schema route; a record is the only copy. |
<!-- readiness-gen:end gcp-paths -->

<!-- readiness-gen:begin gcp-types -->
hashicorp/google and hashicorp/google-beta 8.6.0: 1485 resource types.

| Tier | in-contract | pending-ratification | needs-separator | needs-evidence | pending-mechanism | excluded | Total |
|---|---|---|---|---|---|---|---|
| marker-carried | 302 | 12 | 0 | 4 | 0 | 0 | 318 |
| declaration-carried | 931 | 27 | 0 | 0 | 0 | 0 | 958 |
| record-carried | 0 | 43 | 0 | 31 | 135 | 0 | 209 |
| excluded by design | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| Total | 1233 | 82 | 0 | 35 | 135 | 0 | 1485 |

| Reason | Why the type is not in contract |
|---|---|
| 1 | no row; the provider's identity schema admits it at run time, as it would an unrowed AWS type. |
| 2 | the API mints its identity and it has no labels, so nothing on the object or in configuration finds it again: it is held by its record under a record store (the located-record route, as on AWS), and vetoed as markerless where no record can hold its identity. |
| 3 | the provider source states no id format a row can be read from, and the provider serves no identity schema that admits it. |
| 4 | no row: its id format needs a reading tools/gcp-row-gen does not make (see live/gcp-survey.json's reason). |

| Type | Tier | Status | Reason |
|---|---|---|---|
| `google_access_context_manager_access_level` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_access_level_condition` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_access_levels` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_access_policy` | record-carried | pending-mechanism | 2 |
| `google_access_context_manager_access_policy_iam_binding` | declaration-carried | in-contract |  |
| `google_access_context_manager_access_policy_iam_member` | declaration-carried | in-contract |  |
| `google_access_context_manager_access_policy_iam_policy` | declaration-carried | in-contract |  |
| `google_access_context_manager_authorized_orgs_desc` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_egress_policy` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_gcp_user_access_binding` | record-carried | pending-mechanism | 2 |
| `google_access_context_manager_ingress_policy` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_service_perimeter` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_service_perimeter_dry_run_egress_policy` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_service_perimeter_dry_run_ingress_policy` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_service_perimeter_dry_run_resource` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_service_perimeter_egress_policy` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_service_perimeter_ingress_policy` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_service_perimeter_resource` | declaration-carried | pending-ratification | 1 |
| `google_access_context_manager_service_perimeters` | declaration-carried | pending-ratification | 1 |
| `google_active_directory_domain` | marker-carried | in-contract |  |
| `google_active_directory_domain_trust` | declaration-carried | in-contract |  |
| `google_active_directory_peering` | marker-carried | needs-evidence | 3 |
| `google_agent_identity_auth_provider` | marker-carried | in-contract |  |
| `google_agent_registry_binding` | declaration-carried | in-contract |  |
| `google_agent_registry_service` | declaration-carried | in-contract |  |
| `google_agentic_applications_analyst_agent_persona` | declaration-carried | in-contract |  |
| `google_alloydb_backup` | marker-carried | in-contract |  |
| `google_alloydb_cluster` | marker-carried | in-contract |  |
| `google_alloydb_instance` | marker-carried | in-contract |  |
| `google_alloydb_user` | declaration-carried | in-contract |  |
| `google_api_gateway_api` | marker-carried | in-contract |  |
| `google_api_gateway_api_config` | marker-carried | in-contract |  |
| `google_api_gateway_api_config_iam_binding` | declaration-carried | in-contract |  |
| `google_api_gateway_api_config_iam_member` | declaration-carried | in-contract |  |
| `google_api_gateway_api_config_iam_policy` | declaration-carried | in-contract |  |
| `google_api_gateway_api_iam_binding` | declaration-carried | in-contract |  |
| `google_api_gateway_api_iam_member` | declaration-carried | in-contract |  |
| `google_api_gateway_api_iam_policy` | declaration-carried | in-contract |  |
| `google_api_gateway_gateway` | marker-carried | in-contract |  |
| `google_api_gateway_gateway_iam_binding` | declaration-carried | in-contract |  |
| `google_api_gateway_gateway_iam_member` | declaration-carried | in-contract |  |
| `google_api_gateway_gateway_iam_policy` | declaration-carried | in-contract |  |
| `google_apigee_addons_config` | declaration-carried | in-contract |  |
| `google_apigee_api` | declaration-carried | in-contract |  |
| `google_apigee_api_deployment` | declaration-carried | in-contract |  |
| `google_apigee_api_product` | declaration-carried | in-contract |  |
| `google_apigee_app_group` | declaration-carried | in-contract |  |
| `google_apigee_control_plane_access` | declaration-carried | in-contract |  |
| `google_apigee_data_collector` | declaration-carried | in-contract |  |
| `google_apigee_datastore` | record-carried | pending-mechanism | 2 |
| `google_apigee_developer` | declaration-carried | in-contract |  |
| `google_apigee_developer_app` | declaration-carried | in-contract |  |
| `google_apigee_dns_zone` | record-carried | pending-ratification | 4 |
| `google_apigee_endpoint_attachment` | declaration-carried | pending-ratification | 1 |
| `google_apigee_env_keystore` | declaration-carried | in-contract |  |
| `google_apigee_env_references` | declaration-carried | in-contract |  |
| `google_apigee_envgroup` | declaration-carried | in-contract |  |
| `google_apigee_envgroup_attachment` | record-carried | pending-mechanism | 2 |
| `google_apigee_environment` | declaration-carried | in-contract |  |
| `google_apigee_environment_addons_config` | declaration-carried | pending-ratification | 1 |
| `google_apigee_environment_api_revision_deployment` | declaration-carried | in-contract |  |
| `google_apigee_environment_debugmask` | declaration-carried | in-contract |  |
| `google_apigee_environment_iam_binding` | declaration-carried | in-contract |  |
| `google_apigee_environment_iam_member` | declaration-carried | in-contract |  |
| `google_apigee_environment_iam_policy` | declaration-carried | in-contract |  |
| `google_apigee_environment_keyvaluemaps` | declaration-carried | in-contract |  |
| `google_apigee_environment_keyvaluemaps_entries` | declaration-carried | in-contract |  |
| `google_apigee_flowhook` | declaration-carried | in-contract |  |
| `google_apigee_instance` | declaration-carried | in-contract |  |
| `google_apigee_instance_attachment` | record-carried | pending-mechanism | 2 |
| `google_apigee_keystores_aliases_key_cert_file` | record-carried | needs-evidence | 3 |
| `google_apigee_keystores_aliases_pkcs12` | declaration-carried | in-contract |  |
| `google_apigee_keystores_aliases_self_signed_cert` | declaration-carried | in-contract |  |
| `google_apigee_nat_address` | declaration-carried | in-contract |  |
| `google_apigee_organization` | record-carried | pending-mechanism | 2 |
| `google_apigee_security_action` | declaration-carried | in-contract |  |
| `google_apigee_security_feedback` | declaration-carried | in-contract |  |
| `google_apigee_security_monitoring_condition` | declaration-carried | in-contract |  |
| `google_apigee_security_profile_v2` | declaration-carried | in-contract |  |
| `google_apigee_sharedflow` | declaration-carried | in-contract |  |
| `google_apigee_sharedflow_deployment` | declaration-carried | in-contract |  |
| `google_apigee_space` | declaration-carried | in-contract |  |
| `google_apigee_sync_authorization` | declaration-carried | in-contract |  |
| `google_apigee_target_server` | declaration-carried | in-contract |  |
| `google_apihub_api_hub_instance` | marker-carried | in-contract |  |
| `google_apihub_curation` | declaration-carried | in-contract |  |
| `google_apihub_host_project_registration` | declaration-carried | in-contract |  |
| `google_apihub_plugin` | declaration-carried | in-contract |  |
| `google_apihub_plugin_instance` | declaration-carried | in-contract |  |
| `google_apihub_runtime_project_attachment` | declaration-carried | in-contract |  |
| `google_apikeys_key` | declaration-carried | in-contract |  |
| `google_app_engine_application` | declaration-carried | in-contract |  |
| `google_app_engine_application_url_dispatch_rules` | declaration-carried | in-contract |  |
| `google_app_engine_domain_mapping` | declaration-carried | in-contract |  |
| `google_app_engine_firewall_rule` | declaration-carried | in-contract |  |
| `google_app_engine_flexible_app_version` | declaration-carried | in-contract |  |
| `google_app_engine_service_network_settings` | declaration-carried | in-contract |  |
| `google_app_engine_service_split_traffic` | declaration-carried | in-contract |  |
| `google_app_engine_standard_app_version` | declaration-carried | in-contract |  |
| `google_apphub_application` | declaration-carried | in-contract |  |
| `google_apphub_boundary` | declaration-carried | in-contract |  |
| `google_apphub_service` | declaration-carried | in-contract |  |
| `google_apphub_service_project_attachment` | declaration-carried | in-contract |  |
| `google_apphub_workload` | declaration-carried | in-contract |  |
| `google_artifact_registry_project_config` | declaration-carried | in-contract |  |
| `google_artifact_registry_repository` | marker-carried | in-contract |  |
| `google_artifact_registry_repository_iam_binding` | declaration-carried | in-contract |  |
| `google_artifact_registry_repository_iam_member` | declaration-carried | in-contract |  |
| `google_artifact_registry_repository_iam_policy` | declaration-carried | in-contract |  |
| `google_artifact_registry_rule` | declaration-carried | in-contract |  |
| `google_artifact_registry_vpcsc_config` | declaration-carried | in-contract |  |
| `google_assured_workloads_workload` | marker-carried | in-contract |  |
| `google_backup_dr_backup_plan` | declaration-carried | in-contract |  |
| `google_backup_dr_backup_plan_association` | declaration-carried | in-contract |  |
| `google_backup_dr_backup_vault` | marker-carried | in-contract |  |
| `google_backup_dr_management_server` | declaration-carried | in-contract |  |
| `google_backup_dr_restore_workload` | record-carried | pending-mechanism | 2 |
| `google_backup_dr_service_config` | record-carried | needs-evidence | 3 |
| `google_beyondcorp_security_gateway` | declaration-carried | in-contract |  |
| `google_beyondcorp_security_gateway_application` | declaration-carried | in-contract |  |
| `google_beyondcorp_security_gateway_application_iam_binding` | declaration-carried | in-contract |  |
| `google_beyondcorp_security_gateway_application_iam_member` | declaration-carried | in-contract |  |
| `google_beyondcorp_security_gateway_application_iam_policy` | declaration-carried | in-contract |  |
| `google_beyondcorp_security_gateway_iam_binding` | declaration-carried | in-contract |  |
| `google_beyondcorp_security_gateway_iam_member` | declaration-carried | in-contract |  |
| `google_beyondcorp_security_gateway_iam_policy` | declaration-carried | in-contract |  |
| `google_biglake_catalog` | declaration-carried | in-contract |  |
| `google_biglake_database` | declaration-carried | in-contract |  |
| `google_biglake_hive_catalog` | declaration-carried | in-contract |  |
| `google_biglake_hive_catalog_iam_binding` | declaration-carried | in-contract |  |
| `google_biglake_hive_catalog_iam_member` | declaration-carried | in-contract |  |
| `google_biglake_hive_catalog_iam_policy` | declaration-carried | in-contract |  |
| `google_biglake_hive_database` | declaration-carried | in-contract |  |
| `google_biglake_hive_database_iam_binding` | declaration-carried | in-contract |  |
| `google_biglake_hive_database_iam_member` | declaration-carried | in-contract |  |
| `google_biglake_hive_database_iam_policy` | declaration-carried | in-contract |  |
| `google_biglake_hive_table` | declaration-carried | in-contract |  |
| `google_biglake_hive_table_iam_binding` | declaration-carried | in-contract |  |
| `google_biglake_hive_table_iam_member` | declaration-carried | in-contract |  |
| `google_biglake_hive_table_iam_policy` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_catalog` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_catalog_iam_binding` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_catalog_iam_member` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_catalog_iam_policy` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_namespace` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_namespace_iam_binding` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_namespace_iam_member` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_namespace_iam_policy` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_table` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_table_iam_binding` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_table_iam_member` | declaration-carried | in-contract |  |
| `google_biglake_iceberg_table_iam_policy` | declaration-carried | in-contract |  |
| `google_biglake_table` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_data_exchange` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_data_exchange_iam_binding` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_data_exchange_iam_member` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_data_exchange_iam_policy` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_data_exchange_subscription` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_listing` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_listing_iam_binding` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_listing_iam_member` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_listing_iam_policy` | declaration-carried | in-contract |  |
| `google_bigquery_analytics_hub_listing_subscription` | record-carried | pending-mechanism | 2 |
| `google_bigquery_analytics_hub_query_template` | declaration-carried | in-contract |  |
| `google_bigquery_bi_reservation` | declaration-carried | in-contract |  |
| `google_bigquery_capacity_commitment` | record-carried | pending-mechanism | 2 |
| `google_bigquery_connection` | declaration-carried | in-contract |  |
| `google_bigquery_connection_iam_binding` | declaration-carried | in-contract |  |
| `google_bigquery_connection_iam_member` | declaration-carried | in-contract |  |
| `google_bigquery_connection_iam_policy` | declaration-carried | in-contract |  |
| `google_bigquery_data_transfer_config` | record-carried | pending-mechanism | 2 |
| `google_bigquery_data_transfer_data_source_enrollment` | declaration-carried | in-contract |  |
| `google_bigquery_datapolicy_data_policy` | declaration-carried | in-contract |  |
| `google_bigquery_datapolicy_data_policy_iam_binding` | declaration-carried | in-contract |  |
| `google_bigquery_datapolicy_data_policy_iam_member` | declaration-carried | in-contract |  |
| `google_bigquery_datapolicy_data_policy_iam_policy` | declaration-carried | in-contract |  |
| `google_bigquery_datapolicyv2_data_policy` | declaration-carried | in-contract |  |
| `google_bigquery_datapolicyv2_data_policy_iam_binding` | declaration-carried | in-contract |  |
| `google_bigquery_datapolicyv2_data_policy_iam_member` | declaration-carried | in-contract |  |
| `google_bigquery_datapolicyv2_data_policy_iam_policy` | declaration-carried | in-contract |  |
| `google_bigquery_dataset` | marker-carried | in-contract |  |
| `google_bigquery_dataset_access` | declaration-carried | pending-ratification | 1 |
| `google_bigquery_dataset_iam_binding` | declaration-carried | in-contract |  |
| `google_bigquery_dataset_iam_member` | declaration-carried | in-contract |  |
| `google_bigquery_dataset_iam_policy` | declaration-carried | in-contract |  |
| `google_bigquery_job` | marker-carried | in-contract |  |
| `google_bigquery_reservation` | marker-carried | in-contract |  |
| `google_bigquery_reservation_assignment` | record-carried | pending-ratification | 4 |
| `google_bigquery_reservation_group` | declaration-carried | in-contract |  |
| `google_bigquery_routine` | declaration-carried | in-contract |  |
| `google_bigquery_routine_iam_binding` | declaration-carried | in-contract |  |
| `google_bigquery_routine_iam_member` | declaration-carried | in-contract |  |
| `google_bigquery_routine_iam_policy` | declaration-carried | in-contract |  |
| `google_bigquery_row_access_policy` | declaration-carried | in-contract |  |
| `google_bigquery_table` | marker-carried | in-contract |  |
| `google_bigquery_table_iam_binding` | declaration-carried | in-contract |  |
| `google_bigquery_table_iam_member` | declaration-carried | in-contract |  |
| `google_bigquery_table_iam_policy` | declaration-carried | in-contract |  |
| `google_bigtable_app_profile` | declaration-carried | in-contract |  |
| `google_bigtable_authorized_view` | declaration-carried | in-contract |  |
| `google_bigtable_gc_policy` | record-carried | needs-evidence | 3 |
| `google_bigtable_instance` | marker-carried | in-contract |  |
| `google_bigtable_instance_iam_binding` | declaration-carried | in-contract |  |
| `google_bigtable_instance_iam_member` | declaration-carried | in-contract |  |
| `google_bigtable_instance_iam_policy` | declaration-carried | in-contract |  |
| `google_bigtable_logical_view` | declaration-carried | in-contract |  |
| `google_bigtable_materialized_view` | declaration-carried | in-contract |  |
| `google_bigtable_schema_bundle` | declaration-carried | in-contract |  |
| `google_bigtable_table` | declaration-carried | in-contract |  |
| `google_bigtable_table_iam_binding` | declaration-carried | in-contract |  |
| `google_bigtable_table_iam_member` | declaration-carried | in-contract |  |
| `google_bigtable_table_iam_policy` | declaration-carried | in-contract |  |
| `google_billing_account_iam_binding` | declaration-carried | in-contract |  |
| `google_billing_account_iam_member` | declaration-carried | in-contract |  |
| `google_billing_account_iam_policy` | declaration-carried | in-contract |  |
| `google_billing_budget` | record-carried | pending-mechanism | 2 |
| `google_billing_project_info` | declaration-carried | in-contract |  |
| `google_billing_subaccount` | record-carried | pending-mechanism | 2 |
| `google_binary_authorization_attestor` | declaration-carried | in-contract |  |
| `google_binary_authorization_attestor_iam_binding` | declaration-carried | in-contract |  |
| `google_binary_authorization_attestor_iam_member` | declaration-carried | in-contract |  |
| `google_binary_authorization_attestor_iam_policy` | declaration-carried | in-contract |  |
| `google_binary_authorization_policy` | declaration-carried | in-contract |  |
| `google_blockchain_node_engine_blockchain_nodes` | marker-carried | in-contract |  |
| `google_certificate_manager_certificate` | marker-carried | in-contract |  |
| `google_certificate_manager_certificate_issuance_config` | marker-carried | in-contract |  |
| `google_certificate_manager_certificate_map` | marker-carried | in-contract |  |
| `google_certificate_manager_certificate_map_entry` | marker-carried | in-contract |  |
| `google_certificate_manager_dns_authorization` | marker-carried | in-contract |  |
| `google_certificate_manager_trust_config` | marker-carried | in-contract |  |
| `google_ces_agent` | declaration-carried | in-contract |  |
| `google_ces_app` | declaration-carried | in-contract |  |
| `google_ces_app_root_agent_association` | declaration-carried | pending-ratification | 1 |
| `google_ces_app_version` | declaration-carried | in-contract |  |
| `google_ces_deployment` | record-carried | pending-mechanism | 2 |
| `google_ces_evaluation` | declaration-carried | in-contract |  |
| `google_ces_example` | declaration-carried | in-contract |  |
| `google_ces_guardrail` | declaration-carried | in-contract |  |
| `google_ces_security_settings` | declaration-carried | in-contract |  |
| `google_ces_tool` | declaration-carried | in-contract |  |
| `google_ces_toolset` | declaration-carried | in-contract |  |
| `google_chronicle_big_query_export` | declaration-carried | in-contract |  |
| `google_chronicle_case_close_definition` | record-carried | pending-mechanism | 2 |
| `google_chronicle_case_stage_definition` | record-carried | pending-mechanism | 2 |
| `google_chronicle_case_tag_definition` | record-carried | pending-mechanism | 2 |
| `google_chronicle_custom_list` | record-carried | pending-mechanism | 2 |
| `google_chronicle_dashboard_chart` | record-carried | pending-mechanism | 2 |
| `google_chronicle_data_access_label` | declaration-carried | in-contract |  |
| `google_chronicle_data_access_scope` | declaration-carried | in-contract |  |
| `google_chronicle_data_export` | record-carried | pending-mechanism | 2 |
| `google_chronicle_data_table` | declaration-carried | in-contract |  |
| `google_chronicle_data_table_row` | record-carried | pending-mechanism | 2 |
| `google_chronicle_environment` | record-carried | pending-mechanism | 2 |
| `google_chronicle_environment_group` | record-carried | pending-mechanism | 2 |
| `google_chronicle_feed` | declaration-carried | in-contract |  |
| `google_chronicle_findings_refinement` | record-carried | pending-mechanism | 2 |
| `google_chronicle_findings_refinement_deployment` | declaration-carried | in-contract |  |
| `google_chronicle_native_dashboard` | record-carried | pending-mechanism | 2 |
| `google_chronicle_parser` | record-carried | pending-mechanism | 2 |
| `google_chronicle_parser_extension` | record-carried | pending-mechanism | 2 |
| `google_chronicle_reference_list` | declaration-carried | in-contract |  |
| `google_chronicle_retrohunt` | declaration-carried | in-contract |  |
| `google_chronicle_rule` | declaration-carried | in-contract |  |
| `google_chronicle_rule_deployment` | declaration-carried | in-contract |  |
| `google_chronicle_soar_domain` | record-carried | pending-mechanism | 2 |
| `google_chronicle_soar_network` | record-carried | pending-mechanism | 2 |
| `google_chronicle_watchlist` | declaration-carried | in-contract |  |
| `google_cloud_asset_folder_feed` | record-carried | pending-mechanism | 2 |
| `google_cloud_asset_organization_feed` | declaration-carried | in-contract |  |
| `google_cloud_asset_project_feed` | record-carried | pending-mechanism | 2 |
| `google_cloud_identity_group` | record-carried | pending-mechanism | 2 |
| `google_cloud_identity_group_membership` | record-carried | pending-mechanism | 2 |
| `google_cloud_identity_policy` | record-carried | pending-mechanism | 2 |
| `google_cloud_ids_endpoint` | declaration-carried | in-contract |  |
| `google_cloud_quotas_quota_adjuster_settings` | declaration-carried | in-contract |  |
| `google_cloud_quotas_quota_preference` | declaration-carried | in-contract |  |
| `google_cloud_run_domain_mapping` | declaration-carried | in-contract |  |
| `google_cloud_run_service` | declaration-carried | in-contract |  |
| `google_cloud_run_service_iam_binding` | declaration-carried | in-contract |  |
| `google_cloud_run_service_iam_member` | declaration-carried | in-contract |  |
| `google_cloud_run_service_iam_policy` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_job` | marker-carried | in-contract |  |
| `google_cloud_run_v2_job_iam_binding` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_job_iam_member` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_job_iam_policy` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_service` | marker-carried | in-contract |  |
| `google_cloud_run_v2_service_iam_binding` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_service_iam_member` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_service_iam_policy` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_worker_pool` | marker-carried | in-contract |  |
| `google_cloud_run_v2_worker_pool_iam_binding` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_worker_pool_iam_member` | declaration-carried | in-contract |  |
| `google_cloud_run_v2_worker_pool_iam_policy` | declaration-carried | in-contract |  |
| `google_cloud_scheduler_job` | declaration-carried | in-contract |  |
| `google_cloud_security_compliance_cloud_control` | record-carried | needs-evidence | 3 |
| `google_cloud_security_compliance_framework` | record-carried | needs-evidence | 3 |
| `google_cloud_security_compliance_framework_deployment` | record-carried | needs-evidence | 3 |
| `google_cloud_support_support_event_subscription` | record-carried | pending-mechanism | 2 |
| `google_cloud_tasks_queue` | declaration-carried | in-contract |  |
| `google_cloud_tasks_queue_iam_binding` | declaration-carried | in-contract |  |
| `google_cloud_tasks_queue_iam_member` | declaration-carried | in-contract |  |
| `google_cloud_tasks_queue_iam_policy` | declaration-carried | in-contract |  |
| `google_cloudbuild_bitbucket_server_config` | declaration-carried | in-contract |  |
| `google_cloudbuild_trigger` | record-carried | pending-mechanism | 2 |
| `google_cloudbuild_worker_pool` | declaration-carried | in-contract |  |
| `google_cloudbuildv2_connection` | declaration-carried | in-contract |  |
| `google_cloudbuildv2_connection_iam_binding` | declaration-carried | in-contract |  |
| `google_cloudbuildv2_connection_iam_member` | declaration-carried | in-contract |  |
| `google_cloudbuildv2_connection_iam_policy` | declaration-carried | in-contract |  |
| `google_cloudbuildv2_repository` | declaration-carried | pending-ratification | 1 |
| `google_clouddeploy_automation` | marker-carried | in-contract |  |
| `google_clouddeploy_custom_target_type` | marker-carried | in-contract |  |
| `google_clouddeploy_custom_target_type_iam_binding` | declaration-carried | in-contract |  |
| `google_clouddeploy_custom_target_type_iam_member` | declaration-carried | in-contract |  |
| `google_clouddeploy_custom_target_type_iam_policy` | declaration-carried | in-contract |  |
| `google_clouddeploy_delivery_pipeline` | marker-carried | in-contract |  |
| `google_clouddeploy_delivery_pipeline_iam_binding` | declaration-carried | in-contract |  |
| `google_clouddeploy_delivery_pipeline_iam_member` | declaration-carried | in-contract |  |
| `google_clouddeploy_delivery_pipeline_iam_policy` | declaration-carried | in-contract |  |
| `google_clouddeploy_deploy_policy` | marker-carried | in-contract |  |
| `google_clouddeploy_target` | marker-carried | in-contract |  |
| `google_clouddeploy_target_iam_binding` | declaration-carried | in-contract |  |
| `google_clouddeploy_target_iam_member` | declaration-carried | in-contract |  |
| `google_clouddeploy_target_iam_policy` | declaration-carried | in-contract |  |
| `google_clouddomains_registration` | marker-carried | in-contract |  |
| `google_cloudfunctions2_function` | marker-carried | in-contract |  |
| `google_cloudfunctions2_function_iam_binding` | declaration-carried | in-contract |  |
| `google_cloudfunctions2_function_iam_member` | declaration-carried | in-contract |  |
| `google_cloudfunctions2_function_iam_policy` | declaration-carried | in-contract |  |
| `google_cloudfunctions_function` | marker-carried | in-contract |  |
| `google_cloudfunctions_function_iam_binding` | declaration-carried | in-contract |  |
| `google_cloudfunctions_function_iam_member` | declaration-carried | in-contract |  |
| `google_cloudfunctions_function_iam_policy` | declaration-carried | in-contract |  |
| `google_colab_notebook_execution` | declaration-carried | in-contract |  |
| `google_colab_runtime` | declaration-carried | in-contract |  |
| `google_colab_runtime_template` | marker-carried | in-contract |  |
| `google_colab_runtime_template_iam_binding` | declaration-carried | in-contract |  |
| `google_colab_runtime_template_iam_member` | declaration-carried | in-contract |  |
| `google_colab_runtime_template_iam_policy` | declaration-carried | in-contract |  |
| `google_colab_schedule` | record-carried | pending-mechanism | 2 |
| `google_composer_environment` | marker-carried | in-contract |  |
| `google_composer_user_workloads_config_map` | declaration-carried | in-contract |  |
| `google_composer_user_workloads_secret` | declaration-carried | in-contract |  |
| `google_compute_address` | marker-carried | in-contract |  |
| `google_compute_attached_disk` | record-carried | needs-evidence | 3 |
| `google_compute_autoscaler` | declaration-carried | in-contract |  |
| `google_compute_backend_bucket` | declaration-carried | in-contract |  |
| `google_compute_backend_bucket_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_backend_bucket_iam_member` | declaration-carried | in-contract |  |
| `google_compute_backend_bucket_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_backend_bucket_signed_url_key` | declaration-carried | pending-ratification | 1 |
| `google_compute_backend_service` | declaration-carried | in-contract |  |
| `google_compute_backend_service_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_backend_service_iam_member` | declaration-carried | in-contract |  |
| `google_compute_backend_service_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_backend_service_signed_url_key` | declaration-carried | pending-ratification | 1 |
| `google_compute_bulk_per_instance_config` | declaration-carried | in-contract |  |
| `google_compute_cross_site_network` | declaration-carried | in-contract |  |
| `google_compute_disk` | marker-carried | in-contract |  |
| `google_compute_disk_async_replication` | record-carried | needs-evidence | 3 |
| `google_compute_disk_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_disk_iam_member` | declaration-carried | in-contract |  |
| `google_compute_disk_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_disk_resource_policy_attachment` | declaration-carried | in-contract |  |
| `google_compute_external_vpn_gateway` | marker-carried | in-contract |  |
| `google_compute_firewall` | declaration-carried | in-contract |  |
| `google_compute_firewall_policy` | record-carried | pending-mechanism | 2 |
| `google_compute_firewall_policy_association` | declaration-carried | in-contract |  |
| `google_compute_firewall_policy_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_firewall_policy_iam_member` | declaration-carried | in-contract |  |
| `google_compute_firewall_policy_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_firewall_policy_rule` | declaration-carried | in-contract |  |
| `google_compute_firewall_policy_with_rules` | record-carried | pending-mechanism | 2 |
| `google_compute_forwarding_rule` | marker-carried | in-contract |  |
| `google_compute_future_reservation` | declaration-carried | in-contract |  |
| `google_compute_global_address` | marker-carried | in-contract |  |
| `google_compute_global_forwarding_rule` | marker-carried | in-contract |  |
| `google_compute_global_network_endpoint` | declaration-carried | in-contract |  |
| `google_compute_global_network_endpoint_group` | declaration-carried | in-contract |  |
| `google_compute_global_vm_extension_policy` | declaration-carried | in-contract |  |
| `google_compute_ha_vpn_gateway` | marker-carried | in-contract |  |
| `google_compute_health_check` | declaration-carried | in-contract |  |
| `google_compute_http_health_check` | declaration-carried | in-contract |  |
| `google_compute_https_health_check` | declaration-carried | in-contract |  |
| `google_compute_image` | marker-carried | in-contract |  |
| `google_compute_image_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_image_iam_member` | declaration-carried | in-contract |  |
| `google_compute_image_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_instance` | marker-carried | in-contract |  |
| `google_compute_instance_from_machine_image` | marker-carried | pending-ratification | 4 |
| `google_compute_instance_from_template` | marker-carried | pending-ratification | 4 |
| `google_compute_instance_group` | declaration-carried | in-contract |  |
| `google_compute_instance_group_manager` | declaration-carried | in-contract |  |
| `google_compute_instance_group_membership` | declaration-carried | in-contract |  |
| `google_compute_instance_group_named_port` | declaration-carried | in-contract |  |
| `google_compute_instance_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_instance_iam_member` | declaration-carried | in-contract |  |
| `google_compute_instance_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_instance_settings` | declaration-carried | in-contract |  |
| `google_compute_instance_template` | declaration-carried | in-contract |  |
| `google_compute_instance_template_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_instance_template_iam_member` | declaration-carried | in-contract |  |
| `google_compute_instance_template_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_instant_snapshot` | marker-carried | in-contract |  |
| `google_compute_instant_snapshot_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_instant_snapshot_iam_member` | declaration-carried | in-contract |  |
| `google_compute_instant_snapshot_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_interconnect` | marker-carried | in-contract |  |
| `google_compute_interconnect_attachment` | marker-carried | in-contract |  |
| `google_compute_interconnect_attachment_group` | declaration-carried | in-contract |  |
| `google_compute_interconnect_group` | declaration-carried | in-contract |  |
| `google_compute_machine_image` | declaration-carried | in-contract |  |
| `google_compute_machine_image_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_machine_image_iam_member` | declaration-carried | in-contract |  |
| `google_compute_machine_image_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_managed_ssl_certificate` | declaration-carried | in-contract |  |
| `google_compute_network` | declaration-carried | in-contract |  |
| `google_compute_network_attachment` | declaration-carried | in-contract |  |
| `google_compute_network_edge_security_service` | declaration-carried | in-contract |  |
| `google_compute_network_endpoint` | declaration-carried | in-contract |  |
| `google_compute_network_endpoint_group` | declaration-carried | in-contract |  |
| `google_compute_network_endpoints` | declaration-carried | pending-ratification | 1 |
| `google_compute_network_firewall_policy` | declaration-carried | in-contract |  |
| `google_compute_network_firewall_policy_association` | declaration-carried | in-contract |  |
| `google_compute_network_firewall_policy_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_network_firewall_policy_iam_member` | declaration-carried | in-contract |  |
| `google_compute_network_firewall_policy_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_network_firewall_policy_packet_mirroring_rule` | declaration-carried | in-contract |  |
| `google_compute_network_firewall_policy_rule` | declaration-carried | in-contract |  |
| `google_compute_network_firewall_policy_with_rules` | declaration-carried | in-contract |  |
| `google_compute_network_peering` | record-carried | needs-evidence | 3 |
| `google_compute_network_peering_routes_config` | declaration-carried | in-contract |  |
| `google_compute_node_group` | declaration-carried | in-contract |  |
| `google_compute_node_template` | declaration-carried | in-contract |  |
| `google_compute_organization_security_policy` | record-carried | pending-mechanism | 2 |
| `google_compute_organization_security_policy_association` | declaration-carried | in-contract |  |
| `google_compute_organization_security_policy_rule` | declaration-carried | in-contract |  |
| `google_compute_packet_mirroring` | declaration-carried | in-contract |  |
| `google_compute_per_instance_config` | declaration-carried | in-contract |  |
| `google_compute_preview_feature` | declaration-carried | in-contract |  |
| `google_compute_project_cloud_armor_tier` | declaration-carried | in-contract |  |
| `google_compute_project_default_network_tier` | declaration-carried | in-contract |  |
| `google_compute_project_metadata` | declaration-carried | in-contract |  |
| `google_compute_project_metadata_item` | declaration-carried | in-contract |  |
| `google_compute_public_advertised_prefix` | declaration-carried | in-contract |  |
| `google_compute_public_delegated_prefix` | declaration-carried | in-contract |  |
| `google_compute_region_autoscaler` | declaration-carried | in-contract |  |
| `google_compute_region_backend_bucket` | declaration-carried | in-contract |  |
| `google_compute_region_backend_bucket_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_region_backend_bucket_iam_member` | declaration-carried | in-contract |  |
| `google_compute_region_backend_bucket_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_region_backend_service` | declaration-carried | in-contract |  |
| `google_compute_region_backend_service_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_region_backend_service_iam_member` | declaration-carried | in-contract |  |
| `google_compute_region_backend_service_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_region_commitment` | declaration-carried | in-contract |  |
| `google_compute_region_composite_health_check` | declaration-carried | in-contract |  |
| `google_compute_region_disk` | marker-carried | in-contract |  |
| `google_compute_region_disk_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_region_disk_iam_member` | declaration-carried | in-contract |  |
| `google_compute_region_disk_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_region_disk_resource_policy_attachment` | declaration-carried | in-contract |  |
| `google_compute_region_health_aggregation_policy` | declaration-carried | in-contract |  |
| `google_compute_region_health_check` | declaration-carried | in-contract |  |
| `google_compute_region_health_source` | declaration-carried | in-contract |  |
| `google_compute_region_instance_group_manager` | declaration-carried | in-contract |  |
| `google_compute_region_instance_template` | declaration-carried | in-contract |  |
| `google_compute_region_instant_snapshot` | marker-carried | in-contract |  |
| `google_compute_region_instant_snapshot_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_region_instant_snapshot_iam_member` | declaration-carried | in-contract |  |
| `google_compute_region_instant_snapshot_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_region_network_endpoint` | declaration-carried | in-contract |  |
| `google_compute_region_network_endpoint_group` | declaration-carried | in-contract |  |
| `google_compute_region_network_firewall_policy` | declaration-carried | in-contract |  |
| `google_compute_region_network_firewall_policy_association` | declaration-carried | in-contract |  |
| `google_compute_region_network_firewall_policy_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_region_network_firewall_policy_iam_member` | declaration-carried | in-contract |  |
| `google_compute_region_network_firewall_policy_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_region_network_firewall_policy_rule` | declaration-carried | in-contract |  |
| `google_compute_region_network_firewall_policy_with_rules` | declaration-carried | in-contract |  |
| `google_compute_region_network_policy` | declaration-carried | in-contract |  |
| `google_compute_region_network_policy_traffic_classification_rule` | declaration-carried | in-contract |  |
| `google_compute_region_per_instance_config` | declaration-carried | in-contract |  |
| `google_compute_region_resize_request` | declaration-carried | in-contract |  |
| `google_compute_region_security_policy` | declaration-carried | in-contract |  |
| `google_compute_region_security_policy_rule` | declaration-carried | in-contract |  |
| `google_compute_region_ssl_certificate` | declaration-carried | in-contract |  |
| `google_compute_region_ssl_policy` | declaration-carried | in-contract |  |
| `google_compute_region_target_http_proxy` | declaration-carried | in-contract |  |
| `google_compute_region_target_https_proxy` | declaration-carried | in-contract |  |
| `google_compute_region_target_tcp_proxy` | declaration-carried | in-contract |  |
| `google_compute_region_url_map` | declaration-carried | in-contract |  |
| `google_compute_reservation` | declaration-carried | in-contract |  |
| `google_compute_resize_request` | declaration-carried | in-contract |  |
| `google_compute_resource_policy` | declaration-carried | in-contract |  |
| `google_compute_resource_policy_attachment` | declaration-carried | in-contract |  |
| `google_compute_rollout_plan` | declaration-carried | in-contract |  |
| `google_compute_route` | declaration-carried | in-contract |  |
| `google_compute_router` | declaration-carried | in-contract |  |
| `google_compute_router_interface` | record-carried | pending-ratification | 4 |
| `google_compute_router_named_set` | declaration-carried | in-contract |  |
| `google_compute_router_nat` | declaration-carried | in-contract |  |
| `google_compute_router_nat_address` | declaration-carried | in-contract |  |
| `google_compute_router_peer` | declaration-carried | in-contract |  |
| `google_compute_router_route_policy` | declaration-carried | in-contract |  |
| `google_compute_security_policy` | marker-carried | in-contract |  |
| `google_compute_security_policy_rule` | declaration-carried | in-contract |  |
| `google_compute_service_attachment` | declaration-carried | in-contract |  |
| `google_compute_shared_vpc_host_project` | declaration-carried | in-contract |  |
| `google_compute_shared_vpc_service_project` | declaration-carried | in-contract |  |
| `google_compute_snapshot` | marker-carried | in-contract |  |
| `google_compute_snapshot_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_snapshot_iam_member` | declaration-carried | in-contract |  |
| `google_compute_snapshot_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_snapshot_settings` | declaration-carried | in-contract |  |
| `google_compute_ssl_certificate` | declaration-carried | in-contract |  |
| `google_compute_ssl_policy` | declaration-carried | in-contract |  |
| `google_compute_storage_pool` | marker-carried | in-contract |  |
| `google_compute_storage_pool_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_storage_pool_iam_member` | declaration-carried | in-contract |  |
| `google_compute_storage_pool_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_subnetwork` | declaration-carried | in-contract |  |
| `google_compute_subnetwork_iam_binding` | declaration-carried | in-contract |  |
| `google_compute_subnetwork_iam_member` | declaration-carried | in-contract |  |
| `google_compute_subnetwork_iam_policy` | declaration-carried | in-contract |  |
| `google_compute_target_grpc_proxy` | declaration-carried | in-contract |  |
| `google_compute_target_http_proxy` | declaration-carried | in-contract |  |
| `google_compute_target_https_proxy` | declaration-carried | in-contract |  |
| `google_compute_target_instance` | declaration-carried | in-contract |  |
| `google_compute_target_pool` | declaration-carried | in-contract |  |
| `google_compute_target_ssl_proxy` | declaration-carried | in-contract |  |
| `google_compute_target_tcp_proxy` | declaration-carried | in-contract |  |
| `google_compute_url_map` | declaration-carried | in-contract |  |
| `google_compute_vpn_gateway` | declaration-carried | in-contract |  |
| `google_compute_vpn_tunnel` | marker-carried | in-contract |  |
| `google_compute_wire_group` | declaration-carried | in-contract |  |
| `google_compute_zone_vm_extension_policy` | declaration-carried | in-contract |  |
| `google_config_deployment` | marker-carried | in-contract |  |
| `google_contact_center_insights_analysis_rule` | record-carried | pending-mechanism | 2 |
| `google_contact_center_insights_assessment_rule` | declaration-carried | in-contract |  |
| `google_contact_center_insights_auto_labeling_rule` | declaration-carried | in-contract |  |
| `google_contact_center_insights_encryption_spec` | declaration-carried | in-contract |  |
| `google_contact_center_insights_qa_question` | record-carried | pending-mechanism | 2 |
| `google_contact_center_insights_qa_scorecard` | declaration-carried | in-contract |  |
| `google_contact_center_insights_qa_scorecard_revision` | declaration-carried | in-contract |  |
| `google_contact_center_insights_view` | record-carried | pending-mechanism | 2 |
| `google_container_analysis_note` | declaration-carried | in-contract |  |
| `google_container_analysis_note_iam_binding` | declaration-carried | in-contract |  |
| `google_container_analysis_note_iam_member` | declaration-carried | in-contract |  |
| `google_container_analysis_note_iam_policy` | declaration-carried | in-contract |  |
| `google_container_analysis_occurrence` | record-carried | pending-mechanism | 2 |
| `google_container_attached_cluster` | declaration-carried | in-contract |  |
| `google_container_aws_cluster` | declaration-carried | in-contract |  |
| `google_container_aws_node_pool` | declaration-carried | in-contract |  |
| `google_container_azure_client` | declaration-carried | in-contract |  |
| `google_container_azure_cluster` | declaration-carried | in-contract |  |
| `google_container_azure_node_pool` | declaration-carried | in-contract |  |
| `google_container_cluster` | declaration-carried | in-contract |  |
| `google_container_node_pool` | record-carried | pending-ratification | 4 |
| `google_container_registry` | record-carried | pending-ratification | 4 |
| `google_data_catalog_entry` | record-carried | pending-mechanism | 2 |
| `google_data_catalog_entry_group` | record-carried | pending-mechanism | 2 |
| `google_data_catalog_entry_group_iam_binding` | declaration-carried | in-contract |  |
| `google_data_catalog_entry_group_iam_member` | declaration-carried | in-contract |  |
| `google_data_catalog_entry_group_iam_policy` | declaration-carried | in-contract |  |
| `google_data_catalog_policy_tag` | record-carried | pending-mechanism | 2 |
| `google_data_catalog_policy_tag_iam_binding` | declaration-carried | in-contract |  |
| `google_data_catalog_policy_tag_iam_member` | declaration-carried | in-contract |  |
| `google_data_catalog_policy_tag_iam_policy` | declaration-carried | in-contract |  |
| `google_data_catalog_tag` | record-carried | pending-mechanism | 2 |
| `google_data_catalog_tag_template` | record-carried | pending-mechanism | 2 |
| `google_data_catalog_tag_template_iam_binding` | declaration-carried | in-contract |  |
| `google_data_catalog_tag_template_iam_member` | declaration-carried | in-contract |  |
| `google_data_catalog_tag_template_iam_policy` | declaration-carried | in-contract |  |
| `google_data_catalog_taxonomy` | record-carried | pending-mechanism | 2 |
| `google_data_catalog_taxonomy_iam_binding` | declaration-carried | in-contract |  |
| `google_data_catalog_taxonomy_iam_member` | declaration-carried | in-contract |  |
| `google_data_catalog_taxonomy_iam_policy` | declaration-carried | in-contract |  |
| `google_data_fusion_instance` | marker-carried | in-contract |  |
| `google_data_fusion_instance_iam_binding` | declaration-carried | in-contract |  |
| `google_data_fusion_instance_iam_member` | declaration-carried | in-contract |  |
| `google_data_fusion_instance_iam_policy` | declaration-carried | in-contract |  |
| `google_data_lineage_config` | declaration-carried | in-contract |  |
| `google_data_loss_prevention_content_policy` | record-carried | pending-mechanism | 2 |
| `google_data_loss_prevention_deidentify_template` | declaration-carried | in-contract |  |
| `google_data_loss_prevention_discovery_config` | record-carried | pending-mechanism | 2 |
| `google_data_loss_prevention_inspect_template` | declaration-carried | in-contract |  |
| `google_data_loss_prevention_job_trigger` | declaration-carried | in-contract |  |
| `google_data_loss_prevention_stored_info_type` | declaration-carried | in-contract |  |
| `google_data_pipeline_pipeline` | declaration-carried | in-contract |  |
| `google_database_migration_service_connection_profile` | marker-carried | in-contract |  |
| `google_database_migration_service_migration_job` | marker-carried | in-contract |  |
| `google_database_migration_service_private_connection` | marker-carried | in-contract |  |
| `google_dataflow_flex_template_job` | marker-carried | needs-evidence | 3 |
| `google_dataflow_job` | marker-carried | needs-evidence | 3 |
| `google_dataform_config` | declaration-carried | in-contract |  |
| `google_dataform_folder` | record-carried | pending-mechanism | 2 |
| `google_dataform_repository` | marker-carried | in-contract |  |
| `google_dataform_repository_iam_binding` | declaration-carried | in-contract |  |
| `google_dataform_repository_iam_member` | declaration-carried | in-contract |  |
| `google_dataform_repository_iam_policy` | declaration-carried | in-contract |  |
| `google_dataform_repository_release_config` | declaration-carried | in-contract |  |
| `google_dataform_repository_workflow_config` | declaration-carried | in-contract |  |
| `google_dataform_team_folder` | record-carried | pending-mechanism | 2 |
| `google_dataplex_aspect_type` | marker-carried | in-contract |  |
| `google_dataplex_aspect_type_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_aspect_type_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_aspect_type_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_asset` | marker-carried | in-contract |  |
| `google_dataplex_asset_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_asset_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_asset_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_data_asset` | marker-carried | in-contract |  |
| `google_dataplex_data_product` | marker-carried | in-contract |  |
| `google_dataplex_data_product_data_asset` | marker-carried | in-contract |  |
| `google_dataplex_data_product_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_data_product_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_data_product_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_datascan` | marker-carried | in-contract |  |
| `google_dataplex_datascan_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_datascan_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_datascan_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_entry` | record-carried | needs-evidence | 3 |
| `google_dataplex_entry_group` | marker-carried | in-contract |  |
| `google_dataplex_entry_group_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_entry_group_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_entry_group_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_entry_link` | declaration-carried | in-contract |  |
| `google_dataplex_entry_type` | marker-carried | in-contract |  |
| `google_dataplex_entry_type_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_entry_type_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_entry_type_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_glossary` | marker-carried | in-contract |  |
| `google_dataplex_glossary_category` | marker-carried | in-contract |  |
| `google_dataplex_glossary_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_glossary_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_glossary_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_glossary_term` | marker-carried | in-contract |  |
| `google_dataplex_lake` | marker-carried | in-contract |  |
| `google_dataplex_lake_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_lake_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_lake_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_metadata_feed` | marker-carried | in-contract |  |
| `google_dataplex_task` | marker-carried | in-contract |  |
| `google_dataplex_task_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_task_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_task_iam_policy` | declaration-carried | in-contract |  |
| `google_dataplex_zone` | marker-carried | in-contract |  |
| `google_dataplex_zone_iam_binding` | declaration-carried | in-contract |  |
| `google_dataplex_zone_iam_member` | declaration-carried | in-contract |  |
| `google_dataplex_zone_iam_policy` | declaration-carried | in-contract |  |
| `google_dataproc_autoscaling_policy` | declaration-carried | in-contract |  |
| `google_dataproc_autoscaling_policy_iam_binding` | declaration-carried | in-contract |  |
| `google_dataproc_autoscaling_policy_iam_member` | declaration-carried | in-contract |  |
| `google_dataproc_autoscaling_policy_iam_policy` | declaration-carried | in-contract |  |
| `google_dataproc_batch` | marker-carried | in-contract |  |
| `google_dataproc_cluster` | marker-carried | pending-ratification | 4 |
| `google_dataproc_cluster_iam_binding` | declaration-carried | in-contract |  |
| `google_dataproc_cluster_iam_member` | declaration-carried | in-contract |  |
| `google_dataproc_cluster_iam_policy` | declaration-carried | in-contract |  |
| `google_dataproc_gdc_application_environment` | marker-carried | in-contract |  |
| `google_dataproc_gdc_service_instance` | marker-carried | in-contract |  |
| `google_dataproc_gdc_spark_application` | marker-carried | in-contract |  |
| `google_dataproc_job` | marker-carried | pending-ratification | 4 |
| `google_dataproc_job_iam_binding` | declaration-carried | in-contract |  |
| `google_dataproc_job_iam_member` | declaration-carried | in-contract |  |
| `google_dataproc_job_iam_policy` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_database_iam_binding` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_database_iam_member` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_database_iam_policy` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_federation` | marker-carried | in-contract |  |
| `google_dataproc_metastore_federation_iam_binding` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_federation_iam_member` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_federation_iam_policy` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_service` | marker-carried | in-contract |  |
| `google_dataproc_metastore_service_iam_binding` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_service_iam_member` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_service_iam_policy` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_table_iam_binding` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_table_iam_member` | declaration-carried | in-contract |  |
| `google_dataproc_metastore_table_iam_policy` | declaration-carried | in-contract |  |
| `google_dataproc_session_template` | marker-carried | pending-ratification | 1 |
| `google_dataproc_workflow_template` | marker-carried | in-contract |  |
| `google_datastream_connection_profile` | marker-carried | in-contract |  |
| `google_datastream_private_connection` | marker-carried | in-contract |  |
| `google_datastream_stream` | marker-carried | in-contract |  |
| `google_deployment_manager_deployment` | declaration-carried | in-contract |  |
| `google_developer_connect_account_connector` | marker-carried | in-contract |  |
| `google_developer_connect_connection` | marker-carried | in-contract |  |
| `google_developer_connect_git_repository_link` | marker-carried | in-contract |  |
| `google_developer_connect_insights_config` | marker-carried | in-contract |  |
| `google_dialogflow_agent` | declaration-carried | in-contract |  |
| `google_dialogflow_conversation_profile` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_agent` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_entity_type` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_environment` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_flow` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_generative_settings` | declaration-carried | in-contract |  |
| `google_dialogflow_cx_generator` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_intent` | marker-carried | in-contract |  |
| `google_dialogflow_cx_page` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_playbook` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_security_settings` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_test_case` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_tool` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_tool_version` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_version` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_cx_webhook` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_encryption_spec` | record-carried | needs-evidence | 3 |
| `google_dialogflow_entity_type` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_environment` | declaration-carried | in-contract |  |
| `google_dialogflow_fulfillment` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_generator` | declaration-carried | in-contract |  |
| `google_dialogflow_intent` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_sip_trunk` | record-carried | pending-mechanism | 2 |
| `google_dialogflow_tool` | declaration-carried | in-contract |  |
| `google_dialogflow_version` | record-carried | pending-mechanism | 2 |
| `google_discovery_engine_acl_config` | declaration-carried | in-contract |  |
| `google_discovery_engine_assistant` | declaration-carried | in-contract |  |
| `google_discovery_engine_chat_engine` | declaration-carried | in-contract |  |
| `google_discovery_engine_cmek_config` | declaration-carried | in-contract |  |
| `google_discovery_engine_control` | declaration-carried | in-contract |  |
| `google_discovery_engine_data_connector` | declaration-carried | in-contract |  |
| `google_discovery_engine_data_store` | declaration-carried | in-contract |  |
| `google_discovery_engine_license_config` | declaration-carried | in-contract |  |
| `google_discovery_engine_recommendation_engine` | declaration-carried | in-contract |  |
| `google_discovery_engine_schema` | declaration-carried | in-contract |  |
| `google_discovery_engine_search_engine` | declaration-carried | in-contract |  |
| `google_discovery_engine_search_engine_iam_binding` | declaration-carried | in-contract |  |
| `google_discovery_engine_search_engine_iam_member` | declaration-carried | in-contract |  |
| `google_discovery_engine_search_engine_iam_policy` | declaration-carried | in-contract |  |
| `google_discovery_engine_serving_config` | declaration-carried | in-contract |  |
| `google_discovery_engine_sitemap` | record-carried | pending-mechanism | 2 |
| `google_discovery_engine_target_site` | record-carried | pending-mechanism | 2 |
| `google_discovery_engine_user_store` | declaration-carried | in-contract |  |
| `google_discovery_engine_widget_config` | declaration-carried | in-contract |  |
| `google_dns_managed_zone` | marker-carried | in-contract |  |
| `google_dns_managed_zone_iam_binding` | declaration-carried | in-contract |  |
| `google_dns_managed_zone_iam_member` | declaration-carried | in-contract |  |
| `google_dns_managed_zone_iam_policy` | declaration-carried | in-contract |  |
| `google_dns_policy` | declaration-carried | in-contract |  |
| `google_dns_record_set` | declaration-carried | in-contract |  |
| `google_dns_response_policy` | declaration-carried | in-contract |  |
| `google_dns_response_policy_rule` | declaration-carried | in-contract |  |
| `google_document_ai_processor` | record-carried | pending-mechanism | 2 |
| `google_document_ai_processor_default_version` | declaration-carried | in-contract |  |
| `google_document_ai_schema` | marker-carried | in-contract |  |
| `google_document_ai_warehouse_document_schema` | record-carried | pending-mechanism | 2 |
| `google_document_ai_warehouse_location` | record-carried | needs-evidence | 3 |
| `google_edgecontainer_cluster` | marker-carried | in-contract |  |
| `google_edgecontainer_node_pool` | marker-carried | in-contract |  |
| `google_edgecontainer_vpn_connection` | marker-carried | in-contract |  |
| `google_edgenetwork_interconnect_attachment` | marker-carried | in-contract |  |
| `google_edgenetwork_network` | marker-carried | in-contract |  |
| `google_edgenetwork_subnet` | marker-carried | in-contract |  |
| `google_endpoints_service` | record-carried | pending-ratification | 4 |
| `google_endpoints_service_consumers_iam_binding` | declaration-carried | in-contract |  |
| `google_endpoints_service_consumers_iam_member` | declaration-carried | in-contract |  |
| `google_endpoints_service_consumers_iam_policy` | declaration-carried | in-contract |  |
| `google_endpoints_service_iam_binding` | declaration-carried | in-contract |  |
| `google_endpoints_service_iam_member` | declaration-carried | in-contract |  |
| `google_endpoints_service_iam_policy` | declaration-carried | in-contract |  |
| `google_essential_contacts_contact` | record-carried | pending-mechanism | 2 |
| `google_eventarc_channel` | marker-carried | in-contract |  |
| `google_eventarc_enrollment` | marker-carried | in-contract |  |
| `google_eventarc_google_api_source` | marker-carried | in-contract |  |
| `google_eventarc_google_channel_config` | declaration-carried | in-contract |  |
| `google_eventarc_message_bus` | marker-carried | in-contract |  |
| `google_eventarc_pipeline` | marker-carried | in-contract |  |
| `google_eventarc_pipeline_iam_binding` | declaration-carried | in-contract |  |
| `google_eventarc_pipeline_iam_member` | declaration-carried | in-contract |  |
| `google_eventarc_pipeline_iam_policy` | declaration-carried | in-contract |  |
| `google_eventarc_trigger` | marker-carried | in-contract |  |
| `google_filestore_backup` | marker-carried | in-contract |  |
| `google_filestore_instance` | marker-carried | pending-ratification | 1 |
| `google_filestore_snapshot` | marker-carried | in-contract |  |
| `google_firebase_ai_logic_config` | declaration-carried | in-contract |  |
| `google_firebase_ai_logic_prompt_template` | declaration-carried | in-contract |  |
| `google_firebase_ai_logic_prompt_template_lock` | declaration-carried | in-contract |  |
| `google_firebase_android_app` | record-carried | pending-mechanism | 2 |
| `google_firebase_app_check_app_attest_config` | declaration-carried | in-contract |  |
| `google_firebase_app_check_debug_token` | record-carried | pending-mechanism | 2 |
| `google_firebase_app_check_device_check_config` | declaration-carried | in-contract |  |
| `google_firebase_app_check_play_integrity_config` | declaration-carried | in-contract |  |
| `google_firebase_app_check_recaptcha_enterprise_config` | declaration-carried | in-contract |  |
| `google_firebase_app_check_recaptcha_v3_config` | declaration-carried | in-contract |  |
| `google_firebase_app_check_resource_policy` | record-carried | pending-mechanism | 2 |
| `google_firebase_app_check_service_config` | declaration-carried | in-contract |  |
| `google_firebase_app_hosting_backend` | marker-carried | in-contract |  |
| `google_firebase_app_hosting_build` | marker-carried | in-contract |  |
| `google_firebase_app_hosting_default_domain` | declaration-carried | in-contract |  |
| `google_firebase_app_hosting_domain` | declaration-carried | in-contract |  |
| `google_firebase_app_hosting_traffic` | declaration-carried | in-contract |  |
| `google_firebase_apple_app` | record-carried | pending-mechanism | 2 |
| `google_firebase_data_connect_service` | marker-carried | in-contract |  |
| `google_firebase_database_instance` | declaration-carried | in-contract |  |
| `google_firebase_extensions_instance` | declaration-carried | in-contract |  |
| `google_firebase_hosting_channel` | marker-carried | in-contract |  |
| `google_firebase_hosting_custom_domain` | declaration-carried | in-contract |  |
| `google_firebase_hosting_release` | record-carried | pending-mechanism | 2 |
| `google_firebase_hosting_site` | declaration-carried | in-contract |  |
| `google_firebase_hosting_version` | record-carried | pending-mechanism | 2 |
| `google_firebase_project` | declaration-carried | in-contract |  |
| `google_firebase_remote_config_remote_config` | declaration-carried | in-contract |  |
| `google_firebase_storage_bucket` | declaration-carried | in-contract |  |
| `google_firebase_storage_default_bucket` | declaration-carried | in-contract |  |
| `google_firebase_web_app` | record-carried | pending-mechanism | 2 |
| `google_firebaserules_release` | declaration-carried | in-contract |  |
| `google_firebaserules_ruleset` | record-carried | needs-evidence | 3 |
| `google_firestore_backup_schedule` | record-carried | pending-mechanism | 2 |
| `google_firestore_change_stream` | declaration-carried | in-contract |  |
| `google_firestore_database` | declaration-carried | in-contract |  |
| `google_firestore_document` | record-carried | pending-mechanism | 2 |
| `google_firestore_field` | record-carried | pending-mechanism | 2 |
| `google_firestore_index` | record-carried | pending-mechanism | 2 |
| `google_firestore_user_creds` | declaration-carried | in-contract |  |
| `google_folder` | record-carried | needs-evidence | 3 |
| `google_folder_access_approval_settings` | declaration-carried | in-contract |  |
| `google_folder_iam_audit_config` | declaration-carried | in-contract |  |
| `google_folder_iam_binding` | declaration-carried | in-contract |  |
| `google_folder_iam_member` | declaration-carried | in-contract |  |
| `google_folder_iam_policy` | declaration-carried | in-contract |  |
| `google_folder_organization_policy` | declaration-carried | in-contract |  |
| `google_folder_service_identity` | record-carried | needs-evidence | 3 |
| `google_gemini_code_repository_index` | marker-carried | in-contract |  |
| `google_gemini_code_tools_setting` | marker-carried | in-contract |  |
| `google_gemini_code_tools_setting_binding` | marker-carried | in-contract |  |
| `google_gemini_data_sharing_with_google_setting` | marker-carried | in-contract |  |
| `google_gemini_data_sharing_with_google_setting_binding` | marker-carried | in-contract |  |
| `google_gemini_gda_observability_setting` | marker-carried | in-contract |  |
| `google_gemini_gda_observability_setting_binding` | marker-carried | in-contract |  |
| `google_gemini_gemini_gcp_enablement_setting` | marker-carried | in-contract |  |
| `google_gemini_gemini_gcp_enablement_setting_binding` | marker-carried | in-contract |  |
| `google_gemini_gibq_observability_setting` | marker-carried | in-contract |  |
| `google_gemini_gibq_observability_setting_binding` | marker-carried | in-contract |  |
| `google_gemini_logging_setting` | marker-carried | in-contract |  |
| `google_gemini_logging_setting_binding` | marker-carried | in-contract |  |
| `google_gemini_release_channel_setting` | marker-carried | in-contract |  |
| `google_gemini_release_channel_setting_binding` | marker-carried | in-contract |  |
| `google_gemini_repository_group` | marker-carried | in-contract |  |
| `google_gemini_repository_group_iam_binding` | declaration-carried | in-contract |  |
| `google_gemini_repository_group_iam_member` | declaration-carried | in-contract |  |
| `google_gemini_repository_group_iam_policy` | declaration-carried | in-contract |  |
| `google_gke_backup_backup_channel` | marker-carried | in-contract |  |
| `google_gke_backup_backup_plan` | marker-carried | in-contract |  |
| `google_gke_backup_backup_plan_iam_binding` | declaration-carried | in-contract |  |
| `google_gke_backup_backup_plan_iam_member` | declaration-carried | in-contract |  |
| `google_gke_backup_backup_plan_iam_policy` | declaration-carried | in-contract |  |
| `google_gke_backup_restore_channel` | marker-carried | in-contract |  |
| `google_gke_backup_restore_plan` | marker-carried | in-contract |  |
| `google_gke_backup_restore_plan_iam_binding` | declaration-carried | in-contract |  |
| `google_gke_backup_restore_plan_iam_member` | declaration-carried | in-contract |  |
| `google_gke_backup_restore_plan_iam_policy` | declaration-carried | in-contract |  |
| `google_gke_hub_feature` | marker-carried | in-contract |  |
| `google_gke_hub_feature_iam_binding` | declaration-carried | in-contract |  |
| `google_gke_hub_feature_iam_member` | declaration-carried | in-contract |  |
| `google_gke_hub_feature_iam_policy` | declaration-carried | in-contract |  |
| `google_gke_hub_feature_membership` | declaration-carried | in-contract |  |
| `google_gke_hub_fleet` | marker-carried | in-contract |  |
| `google_gke_hub_membership` | marker-carried | in-contract |  |
| `google_gke_hub_membership_binding` | marker-carried | in-contract |  |
| `google_gke_hub_membership_iam_binding` | declaration-carried | in-contract |  |
| `google_gke_hub_membership_iam_member` | declaration-carried | in-contract |  |
| `google_gke_hub_membership_iam_policy` | declaration-carried | in-contract |  |
| `google_gke_hub_membership_rbac_role_binding` | declaration-carried | in-contract |  |
| `google_gke_hub_namespace` | marker-carried | in-contract |  |
| `google_gke_hub_rollout_sequence` | marker-carried | in-contract |  |
| `google_gke_hub_scope` | marker-carried | in-contract |  |
| `google_gke_hub_scope_iam_binding` | declaration-carried | in-contract |  |
| `google_gke_hub_scope_iam_member` | declaration-carried | in-contract |  |
| `google_gke_hub_scope_iam_policy` | declaration-carried | in-contract |  |
| `google_gke_hub_scope_rbac_role_binding` | marker-carried | in-contract |  |
| `google_gkeonprem_bare_metal_admin_cluster` | declaration-carried | in-contract |  |
| `google_gkeonprem_bare_metal_cluster` | declaration-carried | in-contract |  |
| `google_gkeonprem_bare_metal_node_pool` | declaration-carried | in-contract |  |
| `google_gkeonprem_vmware_admin_cluster` | declaration-carried | in-contract |  |
| `google_gkeonprem_vmware_cluster` | declaration-carried | in-contract |  |
| `google_gkeonprem_vmware_node_pool` | declaration-carried | in-contract |  |
| `google_healthcare_consent_store` | marker-carried | in-contract |  |
| `google_healthcare_consent_store_iam_binding` | declaration-carried | in-contract |  |
| `google_healthcare_consent_store_iam_member` | declaration-carried | in-contract |  |
| `google_healthcare_consent_store_iam_policy` | declaration-carried | in-contract |  |
| `google_healthcare_dataset` | declaration-carried | in-contract |  |
| `google_healthcare_dataset_iam_binding` | record-carried | pending-ratification | 4 |
| `google_healthcare_dataset_iam_member` | record-carried | pending-ratification | 4 |
| `google_healthcare_dataset_iam_policy` | record-carried | pending-ratification | 4 |
| `google_healthcare_dicom_store` | marker-carried | pending-ratification | 1 |
| `google_healthcare_dicom_store_iam_binding` | record-carried | pending-ratification | 4 |
| `google_healthcare_dicom_store_iam_member` | record-carried | pending-ratification | 4 |
| `google_healthcare_dicom_store_iam_policy` | record-carried | pending-ratification | 4 |
| `google_healthcare_fhir_store` | marker-carried | pending-ratification | 1 |
| `google_healthcare_fhir_store_iam_binding` | record-carried | pending-ratification | 4 |
| `google_healthcare_fhir_store_iam_member` | record-carried | pending-ratification | 4 |
| `google_healthcare_fhir_store_iam_policy` | record-carried | pending-ratification | 4 |
| `google_healthcare_hl7_v2_store` | marker-carried | pending-ratification | 1 |
| `google_healthcare_hl7_v2_store_iam_binding` | record-carried | pending-ratification | 4 |
| `google_healthcare_hl7_v2_store_iam_member` | record-carried | pending-ratification | 4 |
| `google_healthcare_hl7_v2_store_iam_policy` | record-carried | pending-ratification | 4 |
| `google_healthcare_pipeline_job` | marker-carried | in-contract |  |
| `google_healthcare_workspace` | marker-carried | in-contract |  |
| `google_hypercomputecluster_cluster` | marker-carried | in-contract |  |
| `google_iam_access_boundary_policy` | declaration-carried | in-contract |  |
| `google_iam_deny_policy` | declaration-carried | in-contract |  |
| `google_iam_folder_access_policy` | declaration-carried | in-contract |  |
| `google_iam_folders_policy_binding` | declaration-carried | in-contract |  |
| `google_iam_oauth_client` | declaration-carried | in-contract |  |
| `google_iam_oauth_client_credential` | declaration-carried | in-contract |  |
| `google_iam_organization_access_policy` | declaration-carried | in-contract |  |
| `google_iam_organizations_policy_binding` | declaration-carried | in-contract |  |
| `google_iam_principal_access_boundary_policy` | declaration-carried | in-contract |  |
| `google_iam_project_access_policy` | declaration-carried | in-contract |  |
| `google_iam_projects_policy_binding` | declaration-carried | in-contract |  |
| `google_iam_workforce_pool` | declaration-carried | in-contract |  |
| `google_iam_workforce_pool_iam_binding` | declaration-carried | in-contract |  |
| `google_iam_workforce_pool_iam_member` | declaration-carried | in-contract |  |
| `google_iam_workforce_pool_iam_policy` | declaration-carried | in-contract |  |
| `google_iam_workforce_pool_provider` | declaration-carried | in-contract |  |
| `google_iam_workforce_pool_provider_key` | declaration-carried | in-contract |  |
| `google_iam_workforce_pool_provider_scim_tenant` | declaration-carried | in-contract |  |
| `google_iam_workforce_pool_provider_scim_token` | declaration-carried | in-contract |  |
| `google_iam_workload_identity_pool` | declaration-carried | in-contract |  |
| `google_iam_workload_identity_pool_iam_binding` | declaration-carried | in-contract |  |
| `google_iam_workload_identity_pool_iam_member` | declaration-carried | in-contract |  |
| `google_iam_workload_identity_pool_iam_policy` | declaration-carried | in-contract |  |
| `google_iam_workload_identity_pool_managed_identity` | declaration-carried | in-contract |  |
| `google_iam_workload_identity_pool_namespace` | declaration-carried | in-contract |  |
| `google_iam_workload_identity_pool_provider` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_agent_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_agent_iam_member` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_agent_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_endpoint_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_endpoint_iam_member` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_endpoint_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_iam_member` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_mcp_server_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_mcp_server_iam_member` | declaration-carried | in-contract |  |
| `google_iap_agent_registry_mcp_server_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_app_engine_service_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_app_engine_service_iam_member` | declaration-carried | in-contract |  |
| `google_iap_app_engine_service_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_app_engine_version_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_app_engine_version_iam_member` | declaration-carried | in-contract |  |
| `google_iap_app_engine_version_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_location_web_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_location_web_iam_member` | declaration-carried | in-contract |  |
| `google_iap_location_web_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_settings` | declaration-carried | in-contract |  |
| `google_iap_tunnel_dest_group` | declaration-carried | in-contract |  |
| `google_iap_tunnel_dest_group_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_tunnel_dest_group_iam_member` | declaration-carried | in-contract |  |
| `google_iap_tunnel_dest_group_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_tunnel_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_tunnel_iam_member` | declaration-carried | in-contract |  |
| `google_iap_tunnel_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_tunnel_instance_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_tunnel_instance_iam_member` | declaration-carried | in-contract |  |
| `google_iap_tunnel_instance_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_web_backend_service_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_web_backend_service_iam_member` | declaration-carried | in-contract |  |
| `google_iap_web_backend_service_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_web_cloud_run_service_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_web_cloud_run_service_iam_member` | declaration-carried | in-contract |  |
| `google_iap_web_cloud_run_service_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_web_forwarding_rule_service_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_web_forwarding_rule_service_iam_member` | declaration-carried | in-contract |  |
| `google_iap_web_forwarding_rule_service_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_web_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_web_iam_member` | declaration-carried | in-contract |  |
| `google_iap_web_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_web_region_backend_service_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_web_region_backend_service_iam_member` | declaration-carried | in-contract |  |
| `google_iap_web_region_backend_service_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_web_region_forwarding_rule_service_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_web_region_forwarding_rule_service_iam_member` | declaration-carried | in-contract |  |
| `google_iap_web_region_forwarding_rule_service_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_web_type_app_engine_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_web_type_app_engine_iam_member` | declaration-carried | in-contract |  |
| `google_iap_web_type_app_engine_iam_policy` | declaration-carried | in-contract |  |
| `google_iap_web_type_compute_iam_binding` | declaration-carried | in-contract |  |
| `google_iap_web_type_compute_iam_member` | declaration-carried | in-contract |  |
| `google_iap_web_type_compute_iam_policy` | declaration-carried | in-contract |  |
| `google_identity_platform_config` | declaration-carried | in-contract |  |
| `google_identity_platform_default_supported_idp_config` | declaration-carried | in-contract |  |
| `google_identity_platform_inbound_saml_config` | declaration-carried | in-contract |  |
| `google_identity_platform_oauth_idp_config` | declaration-carried | in-contract |  |
| `google_identity_platform_tenant` | record-carried | pending-mechanism | 2 |
| `google_identity_platform_tenant_default_supported_idp_config` | declaration-carried | in-contract |  |
| `google_identity_platform_tenant_inbound_saml_config` | declaration-carried | in-contract |  |
| `google_identity_platform_tenant_oauth_idp_config` | declaration-carried | in-contract |  |
| `google_integration_connectors_connection` | marker-carried | in-contract |  |
| `google_integration_connectors_endpoint_attachment` | marker-carried | in-contract |  |
| `google_integration_connectors_managed_zone` | marker-carried | in-contract |  |
| `google_integrations_auth_config` | record-carried | pending-mechanism | 2 |
| `google_integrations_client` | declaration-carried | in-contract |  |
| `google_kms_autokey_config` | declaration-carried | in-contract |  |
| `google_kms_crypto_key` | marker-carried | pending-ratification | 1 |
| `google_kms_crypto_key_iam_binding` | record-carried | pending-ratification | 4 |
| `google_kms_crypto_key_iam_member` | record-carried | pending-ratification | 4 |
| `google_kms_crypto_key_iam_policy` | record-carried | pending-ratification | 4 |
| `google_kms_crypto_key_version` | record-carried | pending-mechanism | 2 |
| `google_kms_ekm_connection` | declaration-carried | in-contract |  |
| `google_kms_ekm_connection_iam_binding` | declaration-carried | in-contract |  |
| `google_kms_ekm_connection_iam_member` | declaration-carried | in-contract |  |
| `google_kms_ekm_connection_iam_policy` | declaration-carried | in-contract |  |
| `google_kms_folder_kaj_policy_config` | declaration-carried | in-contract |  |
| `google_kms_key_handle` | declaration-carried | in-contract |  |
| `google_kms_key_ring` | declaration-carried | in-contract |  |
| `google_kms_key_ring_iam_binding` | record-carried | pending-ratification | 4 |
| `google_kms_key_ring_iam_member` | record-carried | pending-ratification | 4 |
| `google_kms_key_ring_iam_policy` | record-carried | pending-ratification | 4 |
| `google_kms_key_ring_import_job` | declaration-carried | in-contract |  |
| `google_kms_organization_kaj_policy_config` | declaration-carried | in-contract |  |
| `google_kms_project_autokey_config` | declaration-carried | in-contract |  |
| `google_kms_project_kaj_policy_config` | declaration-carried | in-contract |  |
| `google_kms_secret_ciphertext` | record-carried | pending-mechanism | 2 |
| `google_license_manager_configuration` | marker-carried | in-contract |  |
| `google_logging_billing_account_bucket_config` | record-carried | needs-evidence | 3 |
| `google_logging_billing_account_exclusion` | declaration-carried | in-contract |  |
| `google_logging_billing_account_sink` | declaration-carried | in-contract |  |
| `google_logging_folder_bucket_config` | record-carried | needs-evidence | 3 |
| `google_logging_folder_exclusion` | declaration-carried | in-contract |  |
| `google_logging_folder_settings` | declaration-carried | in-contract |  |
| `google_logging_folder_sink` | declaration-carried | in-contract |  |
| `google_logging_linked_dataset` | declaration-carried | pending-ratification | 1 |
| `google_logging_log_scope` | declaration-carried | pending-ratification | 1 |
| `google_logging_log_view` | declaration-carried | pending-ratification | 1 |
| `google_logging_log_view_iam_binding` | declaration-carried | in-contract |  |
| `google_logging_log_view_iam_member` | declaration-carried | in-contract |  |
| `google_logging_log_view_iam_policy` | declaration-carried | in-contract |  |
| `google_logging_metric` | declaration-carried | pending-ratification | 1 |
| `google_logging_organization_bucket_config` | record-carried | needs-evidence | 3 |
| `google_logging_organization_exclusion` | declaration-carried | in-contract |  |
| `google_logging_organization_settings` | declaration-carried | in-contract |  |
| `google_logging_organization_sink` | declaration-carried | in-contract |  |
| `google_logging_project_bucket_config` | record-carried | pending-ratification | 4 |
| `google_logging_project_exclusion` | declaration-carried | in-contract |  |
| `google_logging_project_sink` | declaration-carried | in-contract |  |
| `google_logging_saved_query` | declaration-carried | in-contract |  |
| `google_looker_instance` | declaration-carried | in-contract |  |
| `google_lustre_instance` | marker-carried | in-contract |  |
| `google_managed_kafka_acl` | declaration-carried | in-contract |  |
| `google_managed_kafka_cluster` | marker-carried | in-contract |  |
| `google_managed_kafka_connect_cluster` | marker-carried | in-contract |  |
| `google_managed_kafka_connector` | declaration-carried | in-contract |  |
| `google_managed_kafka_topic` | declaration-carried | in-contract |  |
| `google_memcache_instance` | marker-carried | in-contract |  |
| `google_memorystore_acl_policy` | declaration-carried | in-contract |  |
| `google_memorystore_instance` | marker-carried | in-contract |  |
| `google_memorystore_instance_desired_user_created_endpoints` | declaration-carried | in-contract |  |
| `google_migration_center_assets_export_job` | marker-carried | in-contract |  |
| `google_migration_center_discovery_client` | marker-carried | in-contract |  |
| `google_migration_center_group` | marker-carried | in-contract |  |
| `google_migration_center_import_data_file` | declaration-carried | in-contract |  |
| `google_migration_center_import_job` | marker-carried | in-contract |  |
| `google_migration_center_preference_set` | declaration-carried | in-contract |  |
| `google_migration_center_report` | declaration-carried | in-contract |  |
| `google_migration_center_report_config` | declaration-carried | in-contract |  |
| `google_migration_center_settings` | declaration-carried | in-contract |  |
| `google_migration_center_source` | declaration-carried | in-contract |  |
| `google_model_armor_floorsetting` | declaration-carried | in-contract |  |
| `google_model_armor_template` | marker-carried | in-contract |  |
| `google_monitoring_alert_policy` | record-carried | pending-mechanism | 2 |
| `google_monitoring_custom_service` | record-carried | pending-mechanism | 2 |
| `google_monitoring_dashboard` | record-carried | pending-mechanism | 2 |
| `google_monitoring_group` | record-carried | pending-mechanism | 2 |
| `google_monitoring_metric_descriptor` | record-carried | pending-mechanism | 2 |
| `google_monitoring_monitored_project` | declaration-carried | in-contract |  |
| `google_monitoring_notification_channel` | record-carried | pending-mechanism | 2 |
| `google_monitoring_service` | declaration-carried | in-contract |  |
| `google_monitoring_slo` | record-carried | pending-mechanism | 2 |
| `google_monitoring_snooze` | record-carried | pending-mechanism | 2 |
| `google_monitoring_uptime_check_config` | record-carried | pending-mechanism | 2 |
| `google_netapp_active_directory` | marker-carried | in-contract |  |
| `google_netapp_backup` | marker-carried | in-contract |  |
| `google_netapp_backup_policy` | marker-carried | in-contract |  |
| `google_netapp_backup_vault` | marker-carried | in-contract |  |
| `google_netapp_host_group` | marker-carried | in-contract |  |
| `google_netapp_kmsconfig` | marker-carried | in-contract |  |
| `google_netapp_storage_pool` | marker-carried | in-contract |  |
| `google_netapp_volume` | marker-carried | in-contract |  |
| `google_netapp_volume_quota_rule` | marker-carried | in-contract |  |
| `google_netapp_volume_replication` | marker-carried | in-contract |  |
| `google_netapp_volume_snapshot` | marker-carried | in-contract |  |
| `google_network_connectivity_destination` | marker-carried | in-contract |  |
| `google_network_connectivity_gateway_advertised_route` | marker-carried | in-contract |  |
| `google_network_connectivity_group` | marker-carried | in-contract |  |
| `google_network_connectivity_hub` | marker-carried | in-contract |  |
| `google_network_connectivity_hub_iam_binding` | declaration-carried | in-contract |  |
| `google_network_connectivity_hub_iam_member` | declaration-carried | in-contract |  |
| `google_network_connectivity_hub_iam_policy` | declaration-carried | in-contract |  |
| `google_network_connectivity_internal_range` | marker-carried | in-contract |  |
| `google_network_connectivity_multicloud_data_transfer_config` | marker-carried | in-contract |  |
| `google_network_connectivity_policy_based_route` | marker-carried | in-contract |  |
| `google_network_connectivity_regional_endpoint` | marker-carried | in-contract |  |
| `google_network_connectivity_service_connection_policy` | marker-carried | in-contract |  |
| `google_network_connectivity_spoke` | marker-carried | in-contract |  |
| `google_network_connectivity_transport` | marker-carried | in-contract |  |
| `google_network_management_connectivity_test` | marker-carried | in-contract |  |
| `google_network_management_network_monitoring_provider` | declaration-carried | in-contract |  |
| `google_network_management_organization_vpc_flow_logs_config` | marker-carried | in-contract |  |
| `google_network_management_vpc_flow_logs_config` | marker-carried | in-contract |  |
| `google_network_security_address_group` | marker-carried | in-contract |  |
| `google_network_security_address_group_iam_binding` | declaration-carried | in-contract |  |
| `google_network_security_address_group_iam_member` | declaration-carried | in-contract |  |
| `google_network_security_address_group_iam_policy` | declaration-carried | in-contract |  |
| `google_network_security_authorization_policy` | marker-carried | in-contract |  |
| `google_network_security_authz_policy` | marker-carried | in-contract |  |
| `google_network_security_backend_authentication_config` | marker-carried | in-contract |  |
| `google_network_security_client_tls_policy` | marker-carried | in-contract |  |
| `google_network_security_dns_threat_detector` | marker-carried | in-contract |  |
| `google_network_security_firewall_endpoint` | marker-carried | in-contract |  |
| `google_network_security_firewall_endpoint_association` | marker-carried | in-contract |  |
| `google_network_security_gateway_security_policy` | declaration-carried | in-contract |  |
| `google_network_security_gateway_security_policy_rule` | declaration-carried | in-contract |  |
| `google_network_security_intercept_deployment` | marker-carried | in-contract |  |
| `google_network_security_intercept_deployment_group` | marker-carried | in-contract |  |
| `google_network_security_intercept_endpoint_group` | marker-carried | in-contract |  |
| `google_network_security_intercept_endpoint_group_association` | marker-carried | in-contract |  |
| `google_network_security_mirroring_deployment` | marker-carried | in-contract |  |
| `google_network_security_mirroring_deployment_group` | marker-carried | in-contract |  |
| `google_network_security_mirroring_endpoint` | marker-carried | in-contract |  |
| `google_network_security_mirroring_endpoint_group` | marker-carried | in-contract |  |
| `google_network_security_mirroring_endpoint_group_association` | marker-carried | in-contract |  |
| `google_network_security_sac_attachment` | marker-carried | in-contract |  |
| `google_network_security_sac_realm` | marker-carried | in-contract |  |
| `google_network_security_security_profile` | marker-carried | in-contract |  |
| `google_network_security_security_profile_group` | marker-carried | in-contract |  |
| `google_network_security_server_tls_policy` | marker-carried | in-contract |  |
| `google_network_security_tls_inspection_policy` | declaration-carried | in-contract |  |
| `google_network_security_ull_mirroring_collector` | marker-carried | in-contract |  |
| `google_network_security_ull_mirroring_collector_rule` | marker-carried | in-contract |  |
| `google_network_security_ull_mirroring_engine` | marker-carried | in-contract |  |
| `google_network_security_url_lists` | declaration-carried | in-contract |  |
| `google_network_services_agent_connectivity_template` | marker-carried | in-contract |  |
| `google_network_services_agent_gateway` | marker-carried | in-contract |  |
| `google_network_services_authz_extension` | marker-carried | in-contract |  |
| `google_network_services_edge_cache_keyset` | marker-carried | in-contract |  |
| `google_network_services_edge_cache_origin` | marker-carried | in-contract |  |
| `google_network_services_edge_cache_service` | marker-carried | in-contract |  |
| `google_network_services_endpoint_policy` | marker-carried | in-contract |  |
| `google_network_services_gateway` | marker-carried | in-contract |  |
| `google_network_services_grpc_route` | marker-carried | in-contract |  |
| `google_network_services_http_route` | marker-carried | in-contract |  |
| `google_network_services_lb_edge_extension` | marker-carried | in-contract |  |
| `google_network_services_lb_route_extension` | marker-carried | in-contract |  |
| `google_network_services_lb_traffic_extension` | marker-carried | in-contract |  |
| `google_network_services_mesh` | marker-carried | in-contract |  |
| `google_network_services_multicast_consumer_association` | marker-carried | in-contract |  |
| `google_network_services_multicast_domain` | marker-carried | in-contract |  |
| `google_network_services_multicast_domain_activation` | marker-carried | in-contract |  |
| `google_network_services_multicast_domain_group` | marker-carried | in-contract |  |
| `google_network_services_multicast_group_consumer_activation` | marker-carried | in-contract |  |
| `google_network_services_multicast_group_producer_activation` | marker-carried | in-contract |  |
| `google_network_services_multicast_group_range` | marker-carried | in-contract |  |
| `google_network_services_multicast_group_range_activation` | marker-carried | in-contract |  |
| `google_network_services_multicast_producer_association` | marker-carried | in-contract |  |
| `google_network_services_service_binding` | marker-carried | in-contract |  |
| `google_network_services_service_lb_policies` | marker-carried | in-contract |  |
| `google_network_services_tcp_route` | marker-carried | in-contract |  |
| `google_network_services_tls_route` | declaration-carried | in-contract |  |
| `google_network_services_wasm_plugin` | marker-carried | in-contract |  |
| `google_observability_bucket` | declaration-carried | in-contract |  |
| `google_observability_folder_settings` | declaration-carried | in-contract |  |
| `google_observability_link` | declaration-carried | in-contract |  |
| `google_observability_organization_settings` | declaration-carried | in-contract |  |
| `google_observability_project_settings` | declaration-carried | in-contract |  |
| `google_observability_trace_scope` | declaration-carried | in-contract |  |
| `google_oracle_database_autonomous_database` | marker-carried | in-contract |  |
| `google_oracle_database_cloud_exadata_infrastructure` | marker-carried | in-contract |  |
| `google_oracle_database_cloud_exadata_infrastructure_exascale_config` | declaration-carried | in-contract |  |
| `google_oracle_database_cloud_vm_cluster` | marker-carried | in-contract |  |
| `google_oracle_database_db_system` | marker-carried | in-contract |  |
| `google_oracle_database_exadb_vm_cluster` | marker-carried | in-contract |  |
| `google_oracle_database_exascale_db_storage_vault` | marker-carried | in-contract |  |
| `google_oracle_database_goldengate_connection` | marker-carried | in-contract |  |
| `google_oracle_database_goldengate_connection_assignment` | marker-carried | in-contract |  |
| `google_oracle_database_goldengate_deployment` | marker-carried | in-contract |  |
| `google_oracle_database_odb_network` | marker-carried | in-contract |  |
| `google_oracle_database_odb_subnet` | marker-carried | in-contract |  |
| `google_org_policy_custom_constraint` | declaration-carried | in-contract |  |
| `google_org_policy_policy` | declaration-carried | in-contract |  |
| `google_organization_access_approval_settings` | declaration-carried | in-contract |  |
| `google_organization_iam_audit_config` | declaration-carried | in-contract |  |
| `google_organization_iam_binding` | declaration-carried | in-contract |  |
| `google_organization_iam_custom_role` | record-carried | pending-mechanism | 2 |
| `google_organization_iam_member` | declaration-carried | in-contract |  |
| `google_organization_iam_policy` | declaration-carried | in-contract |  |
| `google_organization_policy` | declaration-carried | in-contract |  |
| `google_organization_service_identity` | record-carried | needs-evidence | 3 |
| `google_os_config_guest_policies` | declaration-carried | in-contract |  |
| `google_os_config_os_policy_assignment` | declaration-carried | in-contract |  |
| `google_os_config_patch_deployment` | record-carried | pending-mechanism | 2 |
| `google_os_config_v2_policy_orchestrator` | marker-carried | in-contract |  |
| `google_os_config_v2_policy_orchestrator_for_folder` | marker-carried | in-contract |  |
| `google_os_config_v2_policy_orchestrator_for_organization` | marker-carried | in-contract |  |
| `google_os_login_ssh_public_key` | record-carried | pending-mechanism | 2 |
| `google_parallelstore_instance` | marker-carried | in-contract |  |
| `google_parameter_manager_parameter` | marker-carried | in-contract |  |
| `google_parameter_manager_parameter_version` | record-carried | needs-evidence | 3 |
| `google_parameter_manager_regional_parameter` | marker-carried | in-contract |  |
| `google_parameter_manager_regional_parameter_version` | record-carried | needs-evidence | 3 |
| `google_parameter_manager_template` | marker-carried | in-contract |  |
| `google_privateca_ca_pool` | marker-carried | in-contract |  |
| `google_privateca_ca_pool_iam_binding` | declaration-carried | in-contract |  |
| `google_privateca_ca_pool_iam_member` | declaration-carried | in-contract |  |
| `google_privateca_ca_pool_iam_policy` | declaration-carried | in-contract |  |
| `google_privateca_certificate` | marker-carried | in-contract |  |
| `google_privateca_certificate_authority` | marker-carried | in-contract |  |
| `google_privateca_certificate_template` | marker-carried | in-contract |  |
| `google_privateca_certificate_template_iam_binding` | declaration-carried | in-contract |  |
| `google_privateca_certificate_template_iam_member` | declaration-carried | in-contract |  |
| `google_privateca_certificate_template_iam_policy` | declaration-carried | in-contract |  |
| `google_privileged_access_manager_entitlement` | declaration-carried | in-contract |  |
| `google_privileged_access_manager_settings` | declaration-carried | in-contract |  |
| `google_project` | marker-carried | pending-ratification | 1 |
| `google_project_access_approval_settings` | declaration-carried | in-contract |  |
| `google_project_default_service_accounts` | record-carried | pending-ratification | 4 |
| `google_project_iam_audit_config` | declaration-carried | in-contract |  |
| `google_project_iam_binding` | declaration-carried | in-contract |  |
| `google_project_iam_custom_role` | declaration-carried | in-contract |  |
| `google_project_iam_member` | declaration-carried | in-contract |  |
| `google_project_iam_member_remove` | record-carried | pending-ratification | 4 |
| `google_project_iam_policy` | declaration-carried | in-contract |  |
| `google_project_organization_policy` | declaration-carried | in-contract |  |
| `google_project_service` | declaration-carried | pending-ratification | 1 |
| `google_project_service_identity` | record-carried | needs-evidence | 3 |
| `google_project_usage_export_bucket` | declaration-carried | in-contract |  |
| `google_public_ca_external_account_key` | record-carried | pending-mechanism | 2 |
| `google_pubsub_lite_reservation` | declaration-carried | in-contract |  |
| `google_pubsub_lite_subscription` | declaration-carried | in-contract |  |
| `google_pubsub_lite_topic` | declaration-carried | in-contract |  |
| `google_pubsub_schema` | declaration-carried | in-contract |  |
| `google_pubsub_schema_iam_binding` | declaration-carried | in-contract |  |
| `google_pubsub_schema_iam_member` | declaration-carried | in-contract |  |
| `google_pubsub_schema_iam_policy` | declaration-carried | in-contract |  |
| `google_pubsub_subscription` | marker-carried | in-contract |  |
| `google_pubsub_subscription_iam_binding` | declaration-carried | in-contract |  |
| `google_pubsub_subscription_iam_member` | declaration-carried | in-contract |  |
| `google_pubsub_subscription_iam_policy` | declaration-carried | in-contract |  |
| `google_pubsub_topic` | marker-carried | in-contract |  |
| `google_pubsub_topic_iam_binding` | declaration-carried | in-contract |  |
| `google_pubsub_topic_iam_member` | declaration-carried | in-contract |  |
| `google_pubsub_topic_iam_policy` | declaration-carried | in-contract |  |
| `google_recaptcha_enterprise_key` | marker-carried | in-contract |  |
| `google_redis_cluster` | marker-carried | in-contract |  |
| `google_redis_cluster_acl_policy` | declaration-carried | in-contract |  |
| `google_redis_cluster_user_created_connections` | declaration-carried | in-contract |  |
| `google_redis_instance` | marker-carried | in-contract |  |
| `google_resource_manager_capability` | declaration-carried | in-contract |  |
| `google_resource_manager_capability_config` | declaration-carried | in-contract |  |
| `google_resource_manager_lien` | record-carried | pending-mechanism | 2 |
| `google_runtimeconfig_config` | declaration-carried | in-contract |  |
| `google_runtimeconfig_config_iam_binding` | declaration-carried | in-contract |  |
| `google_runtimeconfig_config_iam_member` | declaration-carried | in-contract |  |
| `google_runtimeconfig_config_iam_policy` | declaration-carried | in-contract |  |
| `google_runtimeconfig_variable` | declaration-carried | in-contract |  |
| `google_saas_runtime_release` | marker-carried | in-contract |  |
| `google_saas_runtime_rollout_kind` | marker-carried | in-contract |  |
| `google_saas_runtime_saas` | marker-carried | in-contract |  |
| `google_saas_runtime_tenant` | marker-carried | in-contract |  |
| `google_saas_runtime_unit` | marker-carried | in-contract |  |
| `google_saas_runtime_unit_kind` | marker-carried | in-contract |  |
| `google_saas_runtime_unit_operation` | marker-carried | in-contract |  |
| `google_scc_event_threat_detection_custom_module` | record-carried | pending-mechanism | 2 |
| `google_scc_folder_custom_module` | record-carried | pending-mechanism | 2 |
| `google_scc_folder_notification_config` | declaration-carried | in-contract |  |
| `google_scc_folder_scc_big_query_export` | declaration-carried | in-contract |  |
| `google_scc_management_folder_security_health_analytics_custom_module` | record-carried | pending-mechanism | 2 |
| `google_scc_management_organization_event_threat_detection_custom_module` | record-carried | pending-mechanism | 2 |
| `google_scc_management_organization_security_health_analytics_custom_module` | record-carried | pending-mechanism | 2 |
| `google_scc_management_project_security_health_analytics_custom_module` | record-carried | pending-mechanism | 2 |
| `google_scc_mute_config` | record-carried | pending-mechanism | 2 |
| `google_scc_notification_config` | declaration-carried | in-contract |  |
| `google_scc_notification_service_account` | record-carried | needs-evidence | 3 |
| `google_scc_organization_custom_module` | record-carried | pending-mechanism | 2 |
| `google_scc_organization_scc_big_query_export` | declaration-carried | in-contract |  |
| `google_scc_project_custom_module` | record-carried | pending-mechanism | 2 |
| `google_scc_project_notification_config` | record-carried | pending-mechanism | 2 |
| `google_scc_project_scc_big_query_export` | declaration-carried | in-contract |  |
| `google_scc_source` | record-carried | pending-mechanism | 2 |
| `google_scc_source_iam_binding` | declaration-carried | in-contract |  |
| `google_scc_source_iam_member` | declaration-carried | in-contract |  |
| `google_scc_source_iam_policy` | declaration-carried | in-contract |  |
| `google_scc_v2_folder_mute_config` | declaration-carried | in-contract |  |
| `google_scc_v2_folder_notification_config` | declaration-carried | in-contract |  |
| `google_scc_v2_folder_scc_big_query_export` | declaration-carried | in-contract |  |
| `google_scc_v2_organization_mute_config` | declaration-carried | in-contract |  |
| `google_scc_v2_organization_notification_config` | declaration-carried | in-contract |  |
| `google_scc_v2_organization_scc_big_query_export` | declaration-carried | in-contract |  |
| `google_scc_v2_organization_scc_big_query_exports` | declaration-carried | in-contract |  |
| `google_scc_v2_organization_source` | record-carried | pending-mechanism | 2 |
| `google_scc_v2_organization_source_iam_binding` | declaration-carried | in-contract |  |
| `google_scc_v2_organization_source_iam_member` | declaration-carried | in-contract |  |
| `google_scc_v2_organization_source_iam_policy` | declaration-carried | in-contract |  |
| `google_scc_v2_project_mute_config` | declaration-carried | in-contract |  |
| `google_scc_v2_project_notification_config` | record-carried | pending-mechanism | 2 |
| `google_scc_v2_project_scc_big_query_export` | declaration-carried | in-contract |  |
| `google_secret_manager_regional_secret` | marker-carried | in-contract |  |
| `google_secret_manager_regional_secret_iam_binding` | declaration-carried | in-contract |  |
| `google_secret_manager_regional_secret_iam_member` | declaration-carried | in-contract |  |
| `google_secret_manager_regional_secret_iam_policy` | declaration-carried | in-contract |  |
| `google_secret_manager_regional_secret_version` | record-carried | pending-mechanism | 2 |
| `google_secret_manager_secret` | marker-carried | in-contract |  |
| `google_secret_manager_secret_iam_binding` | declaration-carried | in-contract |  |
| `google_secret_manager_secret_iam_member` | declaration-carried | in-contract |  |
| `google_secret_manager_secret_iam_policy` | declaration-carried | in-contract |  |
| `google_secret_manager_secret_version` | record-carried | pending-mechanism | 2 |
| `google_secure_source_manager_branch_rule` | declaration-carried | in-contract |  |
| `google_secure_source_manager_hook` | declaration-carried | in-contract |  |
| `google_secure_source_manager_instance` | marker-carried | in-contract |  |
| `google_secure_source_manager_instance_iam_binding` | declaration-carried | in-contract |  |
| `google_secure_source_manager_instance_iam_member` | declaration-carried | in-contract |  |
| `google_secure_source_manager_instance_iam_policy` | declaration-carried | in-contract |  |
| `google_secure_source_manager_repository` | declaration-carried | in-contract |  |
| `google_secure_source_manager_repository_iam_binding` | declaration-carried | in-contract |  |
| `google_secure_source_manager_repository_iam_member` | declaration-carried | in-contract |  |
| `google_secure_source_manager_repository_iam_policy` | declaration-carried | in-contract |  |
| `google_security_scanner_scan_config` | record-carried | pending-mechanism | 2 |
| `google_securityposture_posture` | declaration-carried | in-contract |  |
| `google_securityposture_posture_deployment` | declaration-carried | in-contract |  |
| `google_service_account` | declaration-carried | in-contract |  |
| `google_service_account_iam_binding` | declaration-carried | in-contract |  |
| `google_service_account_iam_member` | declaration-carried | in-contract |  |
| `google_service_account_iam_policy` | declaration-carried | in-contract |  |
| `google_service_account_key` | record-carried | needs-evidence | 3 |
| `google_service_directory_endpoint` | record-carried | pending-mechanism | 2 |
| `google_service_directory_namespace` | marker-carried | in-contract |  |
| `google_service_directory_namespace_iam_binding` | record-carried | pending-ratification | 4 |
| `google_service_directory_namespace_iam_member` | record-carried | pending-ratification | 4 |
| `google_service_directory_namespace_iam_policy` | record-carried | pending-ratification | 4 |
| `google_service_directory_service` | record-carried | pending-mechanism | 2 |
| `google_service_directory_service_iam_binding` | record-carried | pending-ratification | 4 |
| `google_service_directory_service_iam_member` | record-carried | pending-ratification | 4 |
| `google_service_directory_service_iam_policy` | record-carried | pending-ratification | 4 |
| `google_service_networking_connection` | record-carried | needs-evidence | 3 |
| `google_service_networking_peered_dns_domain` | record-carried | needs-evidence | 3 |
| `google_service_networking_vpc_service_controls` | declaration-carried | in-contract |  |
| `google_service_usage_consumer_quota_override` | record-carried | pending-mechanism | 2 |
| `google_service_usage_v2_consumer_policy` | declaration-carried | in-contract |  |
| `google_site_verification_owner` | declaration-carried | in-contract |  |
| `google_site_verification_web_resource` | record-carried | pending-mechanism | 2 |
| `google_sourcerepo_repository` | declaration-carried | in-contract |  |
| `google_sourcerepo_repository_iam_binding` | declaration-carried | in-contract |  |
| `google_sourcerepo_repository_iam_member` | declaration-carried | in-contract |  |
| `google_sourcerepo_repository_iam_policy` | declaration-carried | in-contract |  |
| `google_spanner_backup_schedule` | declaration-carried | in-contract |  |
| `google_spanner_database` | declaration-carried | in-contract |  |
| `google_spanner_database_iam_binding` | declaration-carried | in-contract |  |
| `google_spanner_database_iam_member` | declaration-carried | in-contract |  |
| `google_spanner_database_iam_policy` | declaration-carried | in-contract |  |
| `google_spanner_instance` | marker-carried | in-contract |  |
| `google_spanner_instance_config` | marker-carried | in-contract |  |
| `google_spanner_instance_iam_binding` | declaration-carried | in-contract |  |
| `google_spanner_instance_iam_member` | declaration-carried | in-contract |  |
| `google_spanner_instance_iam_policy` | declaration-carried | in-contract |  |
| `google_spanner_instance_partition` | declaration-carried | in-contract |  |
| `google_sql_database` | declaration-carried | in-contract |  |
| `google_sql_database_instance` | declaration-carried | in-contract |  |
| `google_sql_provision_script` | record-carried | pending-ratification | 4 |
| `google_sql_source_representation_instance` | declaration-carried | in-contract |  |
| `google_sql_ssl_cert` | record-carried | pending-ratification | 4 |
| `google_sql_user` | record-carried | pending-ratification | 4 |
| `google_storage_anywhere_cache` | record-carried | pending-mechanism | 2 |
| `google_storage_batch_operations_job` | declaration-carried | in-contract |  |
| `google_storage_bucket` | marker-carried | pending-ratification | 1 |
| `google_storage_bucket_access_control` | declaration-carried | in-contract |  |
| `google_storage_bucket_acl` | record-carried | pending-ratification | 4 |
| `google_storage_bucket_iam_binding` | declaration-carried | in-contract |  |
| `google_storage_bucket_iam_member` | declaration-carried | in-contract |  |
| `google_storage_bucket_iam_policy` | declaration-carried | in-contract |  |
| `google_storage_bucket_object` | record-carried | pending-ratification | 4 |
| `google_storage_control_folder_intelligence_config` | declaration-carried | in-contract |  |
| `google_storage_control_organization_intelligence_config` | declaration-carried | in-contract |  |
| `google_storage_control_project_intelligence_config` | declaration-carried | in-contract |  |
| `google_storage_default_object_access_control` | declaration-carried | in-contract |  |
| `google_storage_default_object_acl` | record-carried | pending-ratification | 4 |
| `google_storage_folder` | declaration-carried | in-contract |  |
| `google_storage_ftp_server` | marker-carried | in-contract |  |
| `google_storage_ftp_user` | marker-carried | in-contract |  |
| `google_storage_hmac_key` | record-carried | pending-mechanism | 2 |
| `google_storage_insights_dataset_config` | declaration-carried | in-contract |  |
| `google_storage_insights_report_config` | record-carried | pending-mechanism | 2 |
| `google_storage_managed_folder` | declaration-carried | in-contract |  |
| `google_storage_managed_folder_iam_binding` | declaration-carried | in-contract |  |
| `google_storage_managed_folder_iam_member` | declaration-carried | in-contract |  |
| `google_storage_managed_folder_iam_policy` | declaration-carried | in-contract |  |
| `google_storage_notification` | record-carried | needs-evidence | 3 |
| `google_storage_object_access_control` | declaration-carried | in-contract |  |
| `google_storage_object_acl` | record-carried | pending-ratification | 4 |
| `google_storage_transfer_agent_pool` | declaration-carried | in-contract |  |
| `google_storage_transfer_job` | record-carried | needs-evidence | 3 |
| `google_tags_location_tag_binding` | record-carried | pending-mechanism | 2 |
| `google_tags_tag_binding` | record-carried | pending-mechanism | 2 |
| `google_tags_tag_binding_collection` | record-carried | needs-evidence | 3 |
| `google_tags_tag_key` | record-carried | pending-mechanism | 2 |
| `google_tags_tag_key_iam_binding` | declaration-carried | in-contract |  |
| `google_tags_tag_key_iam_member` | declaration-carried | in-contract |  |
| `google_tags_tag_key_iam_policy` | declaration-carried | in-contract |  |
| `google_tags_tag_value` | record-carried | pending-mechanism | 2 |
| `google_tags_tag_value_iam_binding` | declaration-carried | in-contract |  |
| `google_tags_tag_value_iam_member` | declaration-carried | in-contract |  |
| `google_tags_tag_value_iam_policy` | declaration-carried | in-contract |  |
| `google_tpu_v2_queued_resource` | declaration-carried | in-contract |  |
| `google_tpu_v2_vm` | marker-carried | in-contract |  |
| `google_transcoder_job` | marker-carried | in-contract |  |
| `google_transcoder_job_template` | marker-carried | in-contract |  |
| `google_vector_search_collection` | marker-carried | in-contract |  |
| `google_vector_search_data_object` | declaration-carried | in-contract |  |
| `google_vector_search_index` | marker-carried | in-contract |  |
| `google_vertex_ai_agent_anomaly_detection_scope` | declaration-carried | in-contract |  |
| `google_vertex_ai_cache_config` | declaration-carried | in-contract |  |
| `google_vertex_ai_dataset` | marker-carried | in-contract |  |
| `google_vertex_ai_deployment_resource_pool` | declaration-carried | in-contract |  |
| `google_vertex_ai_endpoint` | marker-carried | in-contract |  |
| `google_vertex_ai_endpoint_iam_binding` | declaration-carried | in-contract |  |
| `google_vertex_ai_endpoint_iam_member` | declaration-carried | in-contract |  |
| `google_vertex_ai_endpoint_iam_policy` | declaration-carried | in-contract |  |
| `google_vertex_ai_endpoint_with_model_garden_deployment` | record-carried | pending-mechanism | 2 |
| `google_vertex_ai_evaluation_metric` | marker-carried | in-contract |  |
| `google_vertex_ai_feature_group` | marker-carried | in-contract |  |
| `google_vertex_ai_feature_group_feature` | marker-carried | in-contract |  |
| `google_vertex_ai_feature_group_iam_binding` | declaration-carried | in-contract |  |
| `google_vertex_ai_feature_group_iam_member` | declaration-carried | in-contract |  |
| `google_vertex_ai_feature_group_iam_policy` | declaration-carried | in-contract |  |
| `google_vertex_ai_feature_online_store` | marker-carried | in-contract |  |
| `google_vertex_ai_feature_online_store_featureview` | marker-carried | in-contract |  |
| `google_vertex_ai_feature_online_store_featureview_iam_binding` | record-carried | pending-ratification | 4 |
| `google_vertex_ai_feature_online_store_featureview_iam_member` | record-carried | pending-ratification | 4 |
| `google_vertex_ai_feature_online_store_featureview_iam_policy` | record-carried | pending-ratification | 4 |
| `google_vertex_ai_feature_online_store_iam_binding` | declaration-carried | in-contract |  |
| `google_vertex_ai_feature_online_store_iam_member` | declaration-carried | in-contract |  |
| `google_vertex_ai_feature_online_store_iam_policy` | declaration-carried | in-contract |  |
| `google_vertex_ai_featurestore` | marker-carried | in-contract |  |
| `google_vertex_ai_featurestore_entitytype` | marker-carried | in-contract |  |
| `google_vertex_ai_featurestore_entitytype_feature` | marker-carried | in-contract |  |
| `google_vertex_ai_featurestore_entitytype_iam_binding` | declaration-carried | in-contract |  |
| `google_vertex_ai_featurestore_entitytype_iam_member` | declaration-carried | in-contract |  |
| `google_vertex_ai_featurestore_entitytype_iam_policy` | declaration-carried | in-contract |  |
| `google_vertex_ai_featurestore_iam_binding` | declaration-carried | in-contract |  |
| `google_vertex_ai_featurestore_iam_member` | declaration-carried | in-contract |  |
| `google_vertex_ai_featurestore_iam_policy` | declaration-carried | in-contract |  |
| `google_vertex_ai_index` | marker-carried | in-contract |  |
| `google_vertex_ai_index_endpoint` | marker-carried | in-contract |  |
| `google_vertex_ai_index_endpoint_deployed_index` | declaration-carried | in-contract |  |
| `google_vertex_ai_metadata_store` | declaration-carried | in-contract |  |
| `google_vertex_ai_model_garden_enable_model` | record-carried | needs-evidence | 3 |
| `google_vertex_ai_persistent_resource` | marker-carried | in-contract |  |
| `google_vertex_ai_rag_corpus` | record-carried | pending-mechanism | 2 |
| `google_vertex_ai_rag_engine_config` | declaration-carried | in-contract |  |
| `google_vertex_ai_reasoning_engine` | marker-carried | in-contract |  |
| `google_vertex_ai_reasoning_engine_iam_binding` | declaration-carried | in-contract |  |
| `google_vertex_ai_reasoning_engine_iam_member` | declaration-carried | in-contract |  |
| `google_vertex_ai_reasoning_engine_iam_policy` | declaration-carried | in-contract |  |
| `google_vertex_ai_semantic_governance_policy` | declaration-carried | in-contract |  |
| `google_vertex_ai_semantic_governance_policy_engine` | declaration-carried | in-contract |  |
| `google_vertex_ai_tensorboard` | marker-carried | in-contract |  |
| `google_vertex_ai_tensorboard_experiment` | marker-carried | in-contract |  |
| `google_vertex_ai_tensorboard_run` | marker-carried | in-contract |  |
| `google_vmwareengine_cluster` | declaration-carried | in-contract |  |
| `google_vmwareengine_datastore` | declaration-carried | in-contract |  |
| `google_vmwareengine_external_access_rule` | declaration-carried | in-contract |  |
| `google_vmwareengine_external_address` | declaration-carried | in-contract |  |
| `google_vmwareengine_network` | declaration-carried | in-contract |  |
| `google_vmwareengine_network_peering` | declaration-carried | in-contract |  |
| `google_vmwareengine_network_policy` | declaration-carried | in-contract |  |
| `google_vmwareengine_private_cloud` | declaration-carried | in-contract |  |
| `google_vmwareengine_subnet` | declaration-carried | in-contract |  |
| `google_vpc_access_connector` | declaration-carried | in-contract |  |
| `google_workbench_instance` | marker-carried | in-contract |  |
| `google_workbench_instance_iam_binding` | declaration-carried | in-contract |  |
| `google_workbench_instance_iam_member` | declaration-carried | in-contract |  |
| `google_workbench_instance_iam_policy` | declaration-carried | in-contract |  |
| `google_workflows_workflow` | marker-carried | needs-evidence | 3 |
| `google_workload_identity_service_agent` | record-carried | needs-evidence | 3 |
| `google_workstations_workstation` | marker-carried | in-contract |  |
| `google_workstations_workstation_cluster` | marker-carried | in-contract |  |
| `google_workstations_workstation_config` | marker-carried | in-contract |  |
| `google_workstations_workstation_config_iam_binding` | declaration-carried | in-contract |  |
| `google_workstations_workstation_config_iam_member` | declaration-carried | in-contract |  |
| `google_workstations_workstation_config_iam_policy` | declaration-carried | in-contract |  |
| `google_workstations_workstation_iam_binding` | declaration-carried | in-contract |  |
| `google_workstations_workstation_iam_member` | declaration-carried | in-contract |  |
| `google_workstations_workstation_iam_policy` | declaration-carried | in-contract |  |
<!-- readiness-gen:end gcp-types -->

## Every AWS type

<!-- readiness-gen:begin types -->
| Reason | Why the type is not in contract |
|---|---|
| 1 | no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| 2 | a composite identity with no worked import example to read its separator from. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| 3 | record-carried, markerless, but its identity is composite and the record can only carry a flat id today (issue #429). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| 4 | record-carried, markerless, but the located-record mechanism does not reach it yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |
| 5 | the provider documents no import example for this type yet. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| 6 | excluded by design: generates credential material this fork can never read back and verify again (maintainer ruling, 2026-08-15, issue #175). See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#unadmitted-type). |
| 7 | record-carried, markerless, but the provider offers no import support for this type at all. See [LIMITATIONS.md](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#markerless-type). |

| Type | Tier | Status | Reason |
|---|---|---|---|
| `aws_accessanalyzer_analyzer` | marker-carried | in-contract |  |
| `aws_accessanalyzer_archive_rule` | declaration-carried | in-contract |  |
| `aws_account_alternate_contact` | record-carried | pending-ratification | 1 |
| `aws_account_primary_contact` | record-carried | pending-ratification | 1 |
| `aws_account_region` | record-carried | pending-ratification | 1 |
| `aws_acm_certificate` | marker-carried | in-contract |  |
| `aws_acm_certificate_validation` | declaration-carried | pending-ratification | 1 |
| `aws_acmpca_certificate` | record-carried | in-contract |  |
| `aws_acmpca_certificate_authority` | marker-carried | in-contract |  |
| `aws_acmpca_certificate_authority_certificate` | declaration-carried | in-contract |  |
| `aws_acmpca_permission` | record-carried | needs-separator | 2 |
| `aws_acmpca_policy` | declaration-carried | in-contract |  |
| `aws_alb` | marker-carried | in-contract |  |
| `aws_alb_listener` | marker-carried | in-contract |  |
| `aws_alb_listener_certificate` | declaration-carried | in-contract |  |
| `aws_alb_listener_rule` | marker-carried | in-contract |  |
| `aws_alb_target_group` | marker-carried | in-contract |  |
| `aws_alb_target_group_attachment` | declaration-carried | in-contract |  |
| `aws_ami` | marker-carried | pending-ratification | 1 |
| `aws_ami_copy` | marker-carried | pending-ratification | 1 |
| `aws_ami_from_instance` | marker-carried | pending-ratification | 1 |
| `aws_ami_launch_permission` | record-carried | pending-ratification | 1 |
| `aws_amplify_app` | marker-carried | in-contract |  |
| `aws_amplify_backend_environment` | declaration-carried | in-contract |  |
| `aws_amplify_branch` | marker-carried | in-contract |  |
| `aws_amplify_domain_association` | declaration-carried | in-contract |  |
| `aws_amplify_webhook` | record-carried | in-contract |  |
| `aws_api_gateway_account` | record-carried | in-contract |  |
| `aws_api_gateway_api_key` | marker-carried | in-contract |  |
| `aws_api_gateway_authorizer` | record-carried | pending-mechanism | 3 |
| `aws_api_gateway_base_path_mapping` | declaration-carried | in-contract |  |
| `aws_api_gateway_client_certificate` | marker-carried | in-contract |  |
| `aws_api_gateway_deployment` | record-carried | pending-mechanism | 3 |
| `aws_api_gateway_documentation_part` | record-carried | pending-mechanism | 3 |
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
| `aws_api_gateway_request_validator` | record-carried | pending-mechanism | 3 |
| `aws_api_gateway_resource` | record-carried | pending-mechanism | 3 |
| `aws_api_gateway_rest_api` | marker-carried | in-contract |  |
| `aws_api_gateway_rest_api_policy` | declaration-carried | in-contract |  |
| `aws_api_gateway_rest_api_put` | record-carried | pending-ratification | 1 |
| `aws_api_gateway_stage` | marker-carried | in-contract |  |
| `aws_api_gateway_usage_plan` | marker-carried | in-contract |  |
| `aws_api_gateway_usage_plan_key` | declaration-carried | in-contract |  |
| `aws_api_gateway_vpc_link` | marker-carried | in-contract |  |
| `aws_apigatewayv2_api` | marker-carried | in-contract |  |
| `aws_apigatewayv2_api_mapping` | record-carried | pending-mechanism | 3 |
| `aws_apigatewayv2_authorizer` | record-carried | pending-mechanism | 3 |
| `aws_apigatewayv2_deployment` | record-carried | pending-mechanism | 3 |
| `aws_apigatewayv2_domain_name` | marker-carried | in-contract |  |
| `aws_apigatewayv2_integration` | record-carried | pending-mechanism | 3 |
| `aws_apigatewayv2_integration_response` | record-carried | pending-mechanism | 3 |
| `aws_apigatewayv2_model` | record-carried | pending-mechanism | 3 |
| `aws_apigatewayv2_route` | record-carried | pending-mechanism | 3 |
| `aws_apigatewayv2_route_response` | record-carried | pending-mechanism | 3 |
| `aws_apigatewayv2_routing_rule` | record-carried | pending-mechanism | 4 |
| `aws_apigatewayv2_stage` | marker-carried | in-contract |  |
| `aws_apigatewayv2_vpc_link` | marker-carried | in-contract |  |
| `aws_app_cookie_stickiness_policy` | declaration-carried | in-contract |  |
| `aws_appautoscaling_policy` | declaration-carried | in-contract |  |
| `aws_appautoscaling_scheduled_action` | record-carried | needs-evidence | 5 |
| `aws_appautoscaling_target` | marker-carried | in-contract |  |
| `aws_appconfig_application` | marker-carried | in-contract |  |
| `aws_appconfig_configuration_profile` | marker-carried | in-contract |  |
| `aws_appconfig_deployment` | marker-carried | in-contract |  |
| `aws_appconfig_deployment_strategy` | marker-carried | in-contract |  |
| `aws_appconfig_environment` | marker-carried | in-contract |  |
| `aws_appconfig_extension` | marker-carried | in-contract |  |
| `aws_appconfig_extension_association` | record-carried | in-contract |  |
| `aws_appconfig_hosted_configuration_version` | record-carried | in-contract |  |
| `aws_appfabric_app_authorization` | marker-carried | pending-ratification | 1 |
| `aws_appfabric_app_authorization_connection` | record-carried | pending-ratification | 1 |
| `aws_appfabric_app_bundle` | marker-carried | pending-ratification | 1 |
| `aws_appfabric_ingestion` | marker-carried | pending-ratification | 1 |
| `aws_appfabric_ingestion_destination` | marker-carried | pending-ratification | 1 |
| `aws_appflow_connector_profile` | declaration-carried | in-contract |  |
| `aws_appflow_flow` | marker-carried | in-contract |  |
| `aws_appintegrations_data_integration` | marker-carried | in-contract |  |
| `aws_appintegrations_event_integration` | marker-carried | in-contract |  |
| `aws_applicationinsights_application` | marker-carried | pending-ratification | 1 |
| `aws_appmesh_gateway_route` | marker-carried | pending-ratification | 1 |
| `aws_appmesh_mesh` | marker-carried | pending-ratification | 1 |
| `aws_appmesh_route` | marker-carried | pending-ratification | 1 |
| `aws_appmesh_virtual_gateway` | marker-carried | pending-ratification | 1 |
| `aws_appmesh_virtual_node` | marker-carried | pending-ratification | 1 |
| `aws_appmesh_virtual_router` | marker-carried | pending-ratification | 1 |
| `aws_appmesh_virtual_service` | marker-carried | pending-ratification | 1 |
| `aws_apprunner_auto_scaling_configuration_version` | marker-carried | in-contract |  |
| `aws_apprunner_connection` | marker-carried | pending-ratification | 1 |
| `aws_apprunner_custom_domain_association` | declaration-carried | in-contract |  |
| `aws_apprunner_default_auto_scaling_configuration_version` | declaration-carried | in-contract |  |
| `aws_apprunner_deployment` | record-carried | pending-ratification | 1 |
| `aws_apprunner_observability_configuration` | marker-carried | in-contract |  |
| `aws_apprunner_service` | marker-carried | in-contract |  |
| `aws_apprunner_vpc_connector` | marker-carried | in-contract |  |
| `aws_apprunner_vpc_ingress_connection` | marker-carried | in-contract |  |
| `aws_appstream_directory_config` | excluded by design | excluded | 6 |
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
| `aws_appsync_datasource` | record-carried | pending-ratification | 1 |
| `aws_appsync_domain_name` | declaration-carried | in-contract |  |
| `aws_appsync_domain_name_api_association` | declaration-carried | in-contract |  |
| `aws_appsync_function` | record-carried | in-contract |  |
| `aws_appsync_graphql_api` | marker-carried | in-contract |  |
| `aws_appsync_resolver` | record-carried | pending-ratification | 1 |
| `aws_appsync_source_api_association` | record-carried | pending-mechanism | 3 |
| `aws_appsync_type` | record-carried | pending-ratification | 1 |
| `aws_arcregionswitch_plan` | marker-carried | in-contract |  |
| `aws_arczonalshift_autoshift_observer_notification_status` | declaration-carried | in-contract |  |
| `aws_arczonalshift_zonal_autoshift_configuration` | declaration-carried | in-contract |  |
| `aws_athena_capacity_reservation` | marker-carried | pending-ratification | 1 |
| `aws_athena_data_catalog` | marker-carried | in-contract |  |
| `aws_athena_database` | record-carried | pending-ratification | 1 |
| `aws_athena_named_query` | record-carried | in-contract |  |
| `aws_athena_prepared_statement` | declaration-carried | in-contract |  |
| `aws_athena_workgroup` | marker-carried | in-contract |  |
| `aws_auditmanager_account_registration` | declaration-carried | in-contract |  |
| `aws_auditmanager_assessment` | marker-carried | in-contract |  |
| `aws_auditmanager_assessment_delegation` | record-carried | pending-ratification | 1 |
| `aws_auditmanager_assessment_report` | record-carried | pending-ratification | 1 |
| `aws_auditmanager_control` | marker-carried | pending-ratification | 1 |
| `aws_auditmanager_framework` | marker-carried | in-contract |  |
| `aws_auditmanager_framework_share` | record-carried | pending-ratification | 1 |
| `aws_auditmanager_organization_admin_account_registration` | record-carried | pending-ratification | 1 |
| `aws_autoscaling_attachment` | record-carried | needs-evidence | 5 |
| `aws_autoscaling_group` | declaration-carried | in-contract |  |
| `aws_autoscaling_group_tag` | record-carried | pending-ratification | 1 |
| `aws_autoscaling_lifecycle_hook` | declaration-carried | in-contract |  |
| `aws_autoscaling_notification` | record-carried | needs-evidence | 5 |
| `aws_autoscaling_policy` | declaration-carried | in-contract |  |
| `aws_autoscaling_schedule` | declaration-carried | in-contract |  |
| `aws_autoscaling_traffic_source_attachment` | declaration-carried | in-contract |  |
| `aws_autoscalingplans_scaling_plan` | record-carried | pending-ratification | 1 |
| `aws_backup_framework` | marker-carried | in-contract |  |
| `aws_backup_global_settings` | record-carried | pending-ratification | 1 |
| `aws_backup_logically_air_gapped_vault` | marker-carried | in-contract |  |
| `aws_backup_plan` | marker-carried | in-contract |  |
| `aws_backup_region_settings` | record-carried | pending-ratification | 1 |
| `aws_backup_report_plan` | marker-carried | in-contract |  |
| `aws_backup_restore_testing_plan` | marker-carried | in-contract |  |
| `aws_backup_restore_testing_selection` | declaration-carried | in-contract |  |
| `aws_backup_selection` | record-carried | pending-mechanism | 3 |
| `aws_backup_vault` | marker-carried | in-contract |  |
| `aws_backup_vault_lock_configuration` | record-carried | pending-ratification | 1 |
| `aws_backup_vault_notifications` | record-carried | pending-ratification | 1 |
| `aws_backup_vault_policy` | record-carried | pending-ratification | 1 |
| `aws_batch_compute_environment` | marker-carried | in-contract |  |
| `aws_batch_job_definition` | marker-carried | in-contract |  |
| `aws_batch_job_queue` | marker-carried | in-contract |  |
| `aws_batch_scheduling_policy` | marker-carried | in-contract |  |
| `aws_bcmdataexports_export` | marker-carried | in-contract |  |
| `aws_bedrock_custom_model` | marker-carried | pending-ratification | 1 |
| `aws_bedrock_evaluation_job` | marker-carried | pending-ratification | 1 |
| `aws_bedrock_foundation_model_agreement` | declaration-carried | pending-ratification | 1 |
| `aws_bedrock_guardrail` | marker-carried | in-contract |  |
| `aws_bedrock_guardrail_version` | record-carried | pending-mechanism | 3 |
| `aws_bedrock_inference_profile` | marker-carried | in-contract |  |
| `aws_bedrock_model_invocation_logging_configuration` | declaration-carried | in-contract |  |
| `aws_bedrock_provisioned_model_throughput` | marker-carried | pending-ratification | 1 |
| `aws_bedrock_use_case_for_model_access` | record-carried | pending-ratification | 1 |
| `aws_bedrockagent_agent` | marker-carried | in-contract |  |
| `aws_bedrockagent_agent_action_group` | record-carried | pending-ratification | 1 |
| `aws_bedrockagent_agent_alias` | marker-carried | in-contract |  |
| `aws_bedrockagent_agent_collaborator` | record-carried | pending-ratification | 1 |
| `aws_bedrockagent_agent_knowledge_base_association` | record-carried | pending-ratification | 1 |
| `aws_bedrockagent_data_source` | record-carried | pending-mechanism | 3 |
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
| `aws_bedrockagentcore_gateway_rule` | record-carried | pending-mechanism | 3 |
| `aws_bedrockagentcore_gateway_target` | record-carried | pending-mechanism | 3 |
| `aws_bedrockagentcore_harness` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_memory` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_memory_strategy` | record-carried | pending-ratification | 1 |
| `aws_bedrockagentcore_oauth2_credential_provider` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_online_evaluation_config` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_policy` | record-carried | pending-mechanism | 3 |
| `aws_bedrockagentcore_policy_engine` | marker-carried | in-contract |  |
| `aws_bedrockagentcore_registry` | record-carried | pending-ratification | 1 |
| `aws_bedrockagentcore_resource_policy` | declaration-carried | in-contract |  |
| `aws_bedrockagentcore_token_vault_cmk` | record-carried | pending-ratification | 1 |
| `aws_bedrockagentcore_workload_identity` | declaration-carried | in-contract |  |
| `aws_billing_view` | marker-carried | in-contract |  |
| `aws_budgets_budget` | marker-carried | in-contract |  |
| `aws_budgets_budget_action` | marker-carried | in-contract |  |
| `aws_ce_anomaly_monitor` | marker-carried | in-contract |  |
| `aws_ce_anomaly_subscription` | marker-carried | in-contract |  |
| `aws_ce_cost_allocation_tag` | record-carried | pending-ratification | 1 |
| `aws_ce_cost_category` | marker-carried | in-contract |  |
| `aws_chatbot_slack_channel_configuration` | marker-carried | in-contract |  |
| `aws_chatbot_teams_channel_configuration` | marker-carried | pending-ratification | 1 |
| `aws_chime_voice_connector` | marker-carried | pending-ratification | 1 |
| `aws_chime_voice_connector_group` | record-carried | pending-ratification | 1 |
| `aws_chime_voice_connector_logging` | record-carried | pending-ratification | 1 |
| `aws_chime_voice_connector_origination` | record-carried | pending-ratification | 1 |
| `aws_chime_voice_connector_streaming` | record-carried | pending-ratification | 1 |
| `aws_chime_voice_connector_termination` | record-carried | pending-ratification | 1 |
| `aws_chime_voice_connector_termination_credentials` | record-carried | pending-ratification | 1 |
| `aws_chimesdkmediapipelines_media_insights_pipeline_configuration` | marker-carried | pending-ratification | 1 |
| `aws_chimesdkvoice_global_settings` | record-carried | pending-ratification | 1 |
| `aws_chimesdkvoice_sip_media_application` | marker-carried | pending-ratification | 1 |
| `aws_chimesdkvoice_sip_rule` | record-carried | in-contract |  |
| `aws_chimesdkvoice_voice_profile_domain` | marker-carried | pending-ratification | 1 |
| `aws_cleanrooms_collaboration` | marker-carried | in-contract |  |
| `aws_cleanrooms_configured_table` | marker-carried | in-contract |  |
| `aws_cleanrooms_membership` | marker-carried | in-contract |  |
| `aws_cloud9_environment_ec2` | marker-carried | in-contract |  |
| `aws_cloud9_environment_membership` | record-carried | pending-ratification | 1 |
| `aws_cloudcontrolapi_resource` | record-carried | pending-ratification | 1 |
| `aws_cloudformation_stack` | marker-carried | pending-ratification | 1 |
| `aws_cloudformation_stack_instances` | record-carried | pending-ratification | 1 |
| `aws_cloudformation_stack_set` | marker-carried | pending-ratification | 1 |
| `aws_cloudformation_stack_set_instance` | record-carried | pending-ratification | 1 |
| `aws_cloudformation_type` | record-carried | pending-ratification | 1 |
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
| `aws_cloudfrontkeyvaluestore_keys_exclusive` | record-carried | pending-ratification | 1 |
| `aws_cloudhsm_v2_cluster` | marker-carried | pending-ratification | 1 |
| `aws_cloudhsm_v2_hsm` | record-carried | in-contract |  |
| `aws_cloudsearch_domain` | record-carried | pending-ratification | 1 |
| `aws_cloudsearch_domain_service_access_policy` | record-carried | pending-ratification | 1 |
| `aws_cloudtrail` | marker-carried | in-contract |  |
| `aws_cloudtrail_event_data_store` | marker-carried | in-contract |  |
| `aws_cloudtrail_organization_delegated_admin_account` | record-carried | pending-ratification | 1 |
| `aws_cloudwatch_alarm_mute_rule` | marker-carried | in-contract |  |
| `aws_cloudwatch_composite_alarm` | marker-carried | in-contract |  |
| `aws_cloudwatch_contributor_insight_rule` | marker-carried | in-contract |  |
| `aws_cloudwatch_contributor_managed_insight_rule` | marker-carried | in-contract |  |
| `aws_cloudwatch_dashboard` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_api_destination` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_archive` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_bus` | marker-carried | in-contract |  |
| `aws_cloudwatch_event_bus_policy` | record-carried | pending-ratification | 1 |
| `aws_cloudwatch_event_connection` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_endpoint` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_permission` | declaration-carried | in-contract |  |
| `aws_cloudwatch_event_rule` | marker-carried | in-contract |  |
| `aws_cloudwatch_event_target` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_account_policy` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_anomaly_detector` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_data_protection_policy` | declaration-carried | pending-ratification | 1 |
| `aws_cloudwatch_log_delivery` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_delivery_destination` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_delivery_destination_policy` | declaration-carried | pending-ratification | 1 |
| `aws_cloudwatch_log_delivery_source` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_destination` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_destination_policy` | declaration-carried | pending-ratification | 1 |
| `aws_cloudwatch_log_group` | marker-carried | in-contract |  |
| `aws_cloudwatch_log_index_policy` | declaration-carried | pending-ratification | 1 |
| `aws_cloudwatch_log_metric_filter` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_resource_policy` | declaration-carried | in-contract |  |
| `aws_cloudwatch_log_s3_table_integration_source` | record-carried | pending-mechanism | 3 |
| `aws_cloudwatch_log_storage_tier_policy` | record-carried | pending-ratification | 1 |
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
| `aws_codebuild_resource_policy` | declaration-carried | pending-ratification | 1 |
| `aws_codebuild_source_credential` | record-carried | in-contract |  |
| `aws_codebuild_webhook` | declaration-carried | in-contract |  |
| `aws_codecatalyst_dev_environment` | record-carried | pending-ratification | 1 |
| `aws_codecatalyst_project` | record-carried | pending-ratification | 1 |
| `aws_codecatalyst_source_repository` | record-carried | pending-ratification | 1 |
| `aws_codecommit_approval_rule_template` | record-carried | pending-ratification | 1 |
| `aws_codecommit_approval_rule_template_association` | declaration-carried | in-contract |  |
| `aws_codecommit_repository` | marker-carried | in-contract |  |
| `aws_codecommit_trigger` | record-carried | pending-ratification | 1 |
| `aws_codeconnections_connection` | marker-carried | in-contract |  |
| `aws_codeconnections_host` | marker-carried | pending-ratification | 1 |
| `aws_codedeploy_app` | marker-carried | in-contract |  |
| `aws_codedeploy_deployment_config` | declaration-carried | in-contract |  |
| `aws_codedeploy_deployment_group` | marker-carried | in-contract |  |
| `aws_codeguruprofiler_profiling_group` | marker-carried | pending-ratification | 1 |
| `aws_codegurureviewer_repository_association` | marker-carried | in-contract |  |
| `aws_codepipeline` | marker-carried | in-contract |  |
| `aws_codepipeline_custom_action_type` | marker-carried | in-contract |  |
| `aws_codepipeline_webhook` | marker-carried | in-contract |  |
| `aws_codestarconnections_connection` | marker-carried | in-contract |  |
| `aws_codestarconnections_host` | record-carried | pending-ratification | 1 |
| `aws_codestarnotifications_notification_rule` | marker-carried | in-contract |  |
| `aws_cognito_identity_pool` | marker-carried | in-contract |  |
| `aws_cognito_identity_pool_provider_principal_tag` | declaration-carried | in-contract |  |
| `aws_cognito_identity_pool_roles_attachment` | declaration-carried | in-contract |  |
| `aws_cognito_identity_provider` | declaration-carried | in-contract |  |
| `aws_cognito_log_delivery_configuration` | declaration-carried | pending-ratification | 1 |
| `aws_cognito_managed_login_branding` | record-carried | pending-mechanism | 3 |
| `aws_cognito_managed_user_pool_client` | record-carried | pending-mechanism | 3 |
| `aws_cognito_resource_server` | declaration-carried | in-contract |  |
| `aws_cognito_risk_configuration` | declaration-carried | in-contract |  |
| `aws_cognito_user` | declaration-carried | in-contract |  |
| `aws_cognito_user_group` | declaration-carried | in-contract |  |
| `aws_cognito_user_in_group` | declaration-carried | in-contract |  |
| `aws_cognito_user_pool` | marker-carried | in-contract |  |
| `aws_cognito_user_pool_client` | record-carried | pending-mechanism | 3 |
| `aws_cognito_user_pool_domain` | declaration-carried | in-contract |  |
| `aws_cognito_user_pool_ui_customization` | declaration-carried | in-contract |  |
| `aws_comprehend_document_classifier` | marker-carried | in-contract |  |
| `aws_comprehend_entity_recognizer` | marker-carried | in-contract |  |
| `aws_computeoptimizer_enrollment_status` | record-carried | pending-ratification | 1 |
| `aws_computeoptimizer_recommendation_preferences` | record-carried | pending-ratification | 1 |
| `aws_config_aggregate_authorization` | marker-carried | in-contract |  |
| `aws_config_config_rule` | marker-carried | in-contract |  |
| `aws_config_configuration_aggregator` | marker-carried | in-contract |  |
| `aws_config_configuration_recorder` | record-carried | pending-ratification | 1 |
| `aws_config_configuration_recorder_status` | declaration-carried | pending-ratification | 1 |
| `aws_config_conformance_pack` | declaration-carried | in-contract |  |
| `aws_config_delivery_channel` | record-carried | pending-ratification | 1 |
| `aws_config_organization_conformance_pack` | declaration-carried | in-contract |  |
| `aws_config_organization_custom_policy_rule` | declaration-carried | pending-ratification | 1 |
| `aws_config_organization_custom_rule` | declaration-carried | pending-ratification | 1 |
| `aws_config_organization_managed_rule` | declaration-carried | pending-ratification | 1 |
| `aws_config_remediation_configuration` | declaration-carried | in-contract |  |
| `aws_config_retention_configuration` | record-carried | pending-ratification | 1 |
| `aws_connect_bot_association` | record-carried | pending-ratification | 1 |
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
| `aws_connect_vocabulary` | marker-carried | pending-ratification | 1 |
| `aws_controltower_baseline` | marker-carried | in-contract |  |
| `aws_controltower_control` | declaration-carried | in-contract |  |
| `aws_controltower_landing_zone` | marker-carried | in-contract |  |
| `aws_costoptimizationhub_enrollment_status` | record-carried | pending-ratification | 1 |
| `aws_costoptimizationhub_preferences` | record-carried | pending-ratification | 1 |
| `aws_cur_report_definition` | marker-carried | in-contract |  |
| `aws_customer_gateway` | marker-carried | in-contract |  |
| `aws_customerprofiles_domain` | marker-carried | pending-ratification | 1 |
| `aws_customerprofiles_profile` | record-carried | pending-ratification | 1 |
| `aws_dataexchange_data_set` | marker-carried | pending-ratification | 1 |
| `aws_dataexchange_event_action` | record-carried | in-contract |  |
| `aws_dataexchange_revision` | marker-carried | pending-ratification | 1 |
| `aws_dataexchange_revision_assets` | marker-carried | pending-ratification | 1 |
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
| `aws_datazone_environment` | record-carried | pending-mechanism | 3 |
| `aws_datazone_environment_blueprint_configuration` | declaration-carried | in-contract |  |
| `aws_datazone_environment_profile` | record-carried | pending-mechanism | 3 |
| `aws_datazone_form_type` | record-carried | pending-mechanism | 3 |
| `aws_datazone_glossary` | record-carried | pending-mechanism | 3 |
| `aws_datazone_glossary_term` | record-carried | pending-ratification | 1 |
| `aws_datazone_project` | record-carried | pending-mechanism | 3 |
| `aws_datazone_user_profile` | record-carried | pending-mechanism | 3 |
| `aws_dax_cluster` | marker-carried | pending-ratification | 1 |
| `aws_dax_parameter_group` | record-carried | pending-ratification | 1 |
| `aws_dax_subnet_group` | record-carried | pending-ratification | 1 |
| `aws_db_cluster_snapshot` | marker-carried | pending-ratification | 1 |
| `aws_db_event_subscription` | marker-carried | in-contract |  |
| `aws_db_instance` | marker-carried | in-contract |  |
| `aws_db_instance_automated_backups_replication` | record-carried | pending-ratification | 1 |
| `aws_db_instance_role_association` | declaration-carried | in-contract |  |
| `aws_db_option_group` | marker-carried | in-contract |  |
| `aws_db_parameter_group` | marker-carried | in-contract |  |
| `aws_db_proxy` | marker-carried | in-contract |  |
| `aws_db_proxy_default_target_group` | declaration-carried | in-contract |  |
| `aws_db_proxy_endpoint` | marker-carried | in-contract |  |
| `aws_db_proxy_target` | record-carried | pending-ratification | 1 |
| `aws_db_snapshot` | marker-carried | pending-ratification | 1 |
| `aws_db_snapshot_copy` | marker-carried | pending-ratification | 1 |
| `aws_db_subnet_group` | marker-carried | in-contract |  |
| `aws_default_network_acl` | marker-carried | in-contract |  |
| `aws_default_route_table` | marker-carried | in-contract |  |
| `aws_default_security_group` | marker-carried | in-contract |  |
| `aws_default_subnet` | marker-carried | pending-ratification | 1 |
| `aws_default_vpc` | marker-carried | pending-ratification | 1 |
| `aws_default_vpc_dhcp_options` | marker-carried | pending-ratification | 1 |
| `aws_detective_graph` | marker-carried | in-contract |  |
| `aws_detective_invitation_accepter` | record-carried | pending-ratification | 1 |
| `aws_detective_member` | declaration-carried | in-contract |  |
| `aws_detective_organization_admin_account` | record-carried | pending-ratification | 1 |
| `aws_detective_organization_configuration` | declaration-carried | pending-ratification | 1 |
| `aws_devicefarm_device_pool` | marker-carried | pending-ratification | 1 |
| `aws_devicefarm_instance_profile` | marker-carried | pending-ratification | 1 |
| `aws_devicefarm_network_profile` | marker-carried | pending-ratification | 1 |
| `aws_devicefarm_project` | marker-carried | pending-ratification | 1 |
| `aws_devicefarm_test_grid_project` | marker-carried | pending-ratification | 1 |
| `aws_devicefarm_upload` | record-carried | pending-ratification | 1 |
| `aws_devopsguru_event_sources_config` | declaration-carried | in-contract |  |
| `aws_devopsguru_notification_channel` | record-carried | in-contract |  |
| `aws_devopsguru_resource_collection` | declaration-carried | in-contract |  |
| `aws_devopsguru_service_integration` | declaration-carried | in-contract |  |
| `aws_directory_service_conditional_forwarder` | declaration-carried | in-contract |  |
| `aws_directory_service_directory` | marker-carried | pending-ratification | 1 |
| `aws_directory_service_log_subscription` | record-carried | pending-ratification | 1 |
| `aws_directory_service_radius_settings` | record-carried | pending-ratification | 1 |
| `aws_directory_service_region` | marker-carried | in-contract |  |
| `aws_directory_service_shared_directory` | record-carried | pending-ratification | 1 |
| `aws_directory_service_shared_directory_accepter` | record-carried | pending-ratification | 1 |
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
| `aws_docdb_cluster_snapshot` | record-carried | pending-ratification | 1 |
| `aws_docdb_event_subscription` | marker-carried | in-contract |  |
| `aws_docdb_global_cluster` | record-carried | pending-ratification | 1 |
| `aws_docdb_subnet_group` | marker-carried | in-contract |  |
| `aws_docdbelastic_cluster` | marker-carried | in-contract |  |
| `aws_drs_replication_configuration_template` | marker-carried | pending-ratification | 1 |
| `aws_dsql_cluster` | marker-carried | in-contract |  |
| `aws_dsql_cluster_peering` | record-carried | pending-ratification | 1 |
| `aws_dx_bgp_peer` | record-carried | pending-ratification | 1 |
| `aws_dx_connection` | marker-carried | in-contract |  |
| `aws_dx_connection_association` | record-carried | pending-ratification | 1 |
| `aws_dx_connection_confirmation` | record-carried | pending-ratification | 1 |
| `aws_dx_gateway` | marker-carried | in-contract |  |
| `aws_dx_gateway_association` | record-carried | pending-ratification | 1 |
| `aws_dx_gateway_association_proposal` | record-carried | pending-ratification | 1 |
| `aws_dx_hosted_connection` | record-carried | pending-ratification | 1 |
| `aws_dx_hosted_private_virtual_interface` | record-carried | in-contract |  |
| `aws_dx_hosted_private_virtual_interface_accepter` | marker-carried | pending-ratification | 1 |
| `aws_dx_hosted_public_virtual_interface` | record-carried | in-contract |  |
| `aws_dx_hosted_public_virtual_interface_accepter` | marker-carried | pending-ratification | 1 |
| `aws_dx_hosted_transit_virtual_interface` | record-carried | in-contract |  |
| `aws_dx_hosted_transit_virtual_interface_accepter` | marker-carried | pending-ratification | 1 |
| `aws_dx_lag` | marker-carried | in-contract |  |
| `aws_dx_macsec_key_association` | record-carried | pending-ratification | 1 |
| `aws_dx_private_virtual_interface` | marker-carried | in-contract |  |
| `aws_dx_public_virtual_interface` | marker-carried | in-contract |  |
| `aws_dx_transit_virtual_interface` | marker-carried | in-contract |  |
| `aws_dynamodb_contributor_insights` | record-carried | pending-ratification | 1 |
| `aws_dynamodb_global_secondary_index` | declaration-carried | in-contract |  |
| `aws_dynamodb_global_table` | declaration-carried | in-contract |  |
| `aws_dynamodb_kinesis_streaming_destination` | declaration-carried | in-contract |  |
| `aws_dynamodb_resource_policy` | declaration-carried | in-contract |  |
| `aws_dynamodb_table` | marker-carried | in-contract |  |
| `aws_dynamodb_table_export` | record-carried | pending-ratification | 1 |
| `aws_dynamodb_table_item` | record-carried | pending-ratification | 1 |
| `aws_dynamodb_table_replica` | marker-carried | pending-ratification | 1 |
| `aws_dynamodb_tag` | record-carried | pending-ratification | 1 |
| `aws_ebs_default_kms_key` | record-carried | pending-ratification | 1 |
| `aws_ebs_encryption_by_default` | record-carried | pending-ratification | 1 |
| `aws_ebs_fast_snapshot_restore` | declaration-carried | in-contract |  |
| `aws_ebs_snapshot` | marker-carried | pending-ratification | 1 |
| `aws_ebs_snapshot_block_public_access` | declaration-carried | in-contract |  |
| `aws_ebs_snapshot_copy` | marker-carried | pending-ratification | 1 |
| `aws_ebs_snapshot_import` | marker-carried | pending-ratification | 1 |
| `aws_ebs_volume` | marker-carried | in-contract |  |
| `aws_ebs_volume_copy` | marker-carried | pending-ratification | 1 |
| `aws_ec2_allowed_images_settings` | declaration-carried | in-contract |  |
| `aws_ec2_availability_zone_group` | record-carried | pending-ratification | 1 |
| `aws_ec2_capacity_block_reservation` | marker-carried | pending-ratification | 1 |
| `aws_ec2_capacity_reservation` | marker-carried | in-contract |  |
| `aws_ec2_carrier_gateway` | marker-carried | pending-ratification | 1 |
| `aws_ec2_client_vpn_authorization_rule` | record-carried | pending-mechanism | 3 |
| `aws_ec2_client_vpn_endpoint` | marker-carried | in-contract |  |
| `aws_ec2_client_vpn_network_association` | record-carried | pending-mechanism | 3 |
| `aws_ec2_client_vpn_route` | declaration-carried | in-contract |  |
| `aws_ec2_default_credit_specification` | record-carried | pending-ratification | 1 |
| `aws_ec2_fleet` | marker-carried | in-contract |  |
| `aws_ec2_host` | marker-carried | in-contract |  |
| `aws_ec2_image_block_public_access` | record-carried | pending-ratification | 1 |
| `aws_ec2_instance_connect_endpoint` | marker-carried | in-contract |  |
| `aws_ec2_instance_metadata_defaults` | record-carried | pending-ratification | 1 |
| `aws_ec2_instance_state` | record-carried | pending-ratification | 1 |
| `aws_ec2_local_gateway_route` | declaration-carried | in-contract |  |
| `aws_ec2_local_gateway_route_table` | marker-carried | in-contract |  |
| `aws_ec2_local_gateway_route_table_virtual_interface_group_association` | marker-carried | in-contract |  |
| `aws_ec2_local_gateway_route_table_vpc_association` | marker-carried | in-contract |  |
| `aws_ec2_managed_prefix_list` | marker-carried | in-contract |  |
| `aws_ec2_managed_prefix_list_entry` | declaration-carried | in-contract |  |
| `aws_ec2_network_insights_access_scope` | marker-carried | in-contract |  |
| `aws_ec2_network_insights_analysis` | marker-carried | in-contract |  |
| `aws_ec2_network_insights_path` | marker-carried | in-contract |  |
| `aws_ec2_secondary_network` | marker-carried | pending-ratification | 1 |
| `aws_ec2_secondary_subnet` | marker-carried | pending-ratification | 1 |
| `aws_ec2_serial_console_access` | record-carried | pending-ratification | 1 |
| `aws_ec2_subnet_cidr_reservation` | record-carried | pending-ratification | 1 |
| `aws_ec2_tag` | record-carried | pending-ratification | 1 |
| `aws_ec2_traffic_mirror_filter` | marker-carried | in-contract |  |
| `aws_ec2_traffic_mirror_filter_rule` | record-carried | pending-mechanism | 3 |
| `aws_ec2_traffic_mirror_session` | marker-carried | in-contract |  |
| `aws_ec2_traffic_mirror_target` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_connect` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_connect_peer` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_default_route_table_association` | record-carried | pending-ratification | 1 |
| `aws_ec2_transit_gateway_default_route_table_propagation` | record-carried | pending-ratification | 1 |
| `aws_ec2_transit_gateway_metering_policy` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_metering_policy_entry` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_multicast_domain` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_multicast_domain_association` | record-carried | needs-evidence | 5 |
| `aws_ec2_transit_gateway_multicast_group_member` | record-carried | needs-evidence | 5 |
| `aws_ec2_transit_gateway_multicast_group_source` | record-carried | needs-evidence | 5 |
| `aws_ec2_transit_gateway_peering_attachment` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_peering_attachment_accepter` | marker-carried | pending-ratification | 1 |
| `aws_ec2_transit_gateway_policy_table` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_policy_table_association` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_prefix_list_reference` | record-carried | pending-ratification | 1 |
| `aws_ec2_transit_gateway_route` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_route_table` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_route_table_association` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_route_table_propagation` | declaration-carried | in-contract |  |
| `aws_ec2_transit_gateway_vpc_attachment` | marker-carried | in-contract |  |
| `aws_ec2_transit_gateway_vpc_attachment_accepter` | marker-carried | pending-ratification | 1 |
| `aws_ecr_account_setting` | record-carried | pending-ratification | 1 |
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
| `aws_ecs_account_setting_default` | record-carried | pending-ratification | 1 |
| `aws_ecs_capacity_provider` | marker-carried | in-contract |  |
| `aws_ecs_cluster` | marker-carried | in-contract |  |
| `aws_ecs_cluster_capacity_providers` | declaration-carried | in-contract |  |
| `aws_ecs_daemon` | marker-carried | in-contract |  |
| `aws_ecs_daemon_task_definition` | marker-carried | in-contract |  |
| `aws_ecs_express_gateway_service` | marker-carried | in-contract |  |
| `aws_ecs_service` | marker-carried | in-contract |  |
| `aws_ecs_tag` | record-carried | pending-ratification | 1 |
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
| `aws_eip_domain_name` | record-carried | pending-ratification | 1 |
| `aws_eks_access_entry` | marker-carried | in-contract |  |
| `aws_eks_access_policy_association` | declaration-carried | in-contract |  |
| `aws_eks_addon` | marker-carried | in-contract |  |
| `aws_eks_capability` | marker-carried | in-contract |  |
| `aws_eks_cluster` | marker-carried | in-contract |  |
| `aws_eks_fargate_profile` | marker-carried | in-contract |  |
| `aws_eks_identity_provider_config` | marker-carried | pending-ratification | 1 |
| `aws_eks_node_group` | marker-carried | in-contract |  |
| `aws_eks_pod_identity_association` | marker-carried | in-contract |  |
| `aws_elastic_beanstalk_application` | marker-carried | in-contract |  |
| `aws_elastic_beanstalk_application_version` | marker-carried | needs-evidence | 5 |
| `aws_elastic_beanstalk_configuration_template` | record-carried | pending-mechanism | 7 |
| `aws_elastic_beanstalk_environment` | marker-carried | in-contract |  |
| `aws_elasticache_cluster` | marker-carried | in-contract |  |
| `aws_elasticache_global_replication_group` | record-carried | in-contract |  |
| `aws_elasticache_parameter_group` | marker-carried | in-contract |  |
| `aws_elasticache_replication_group` | marker-carried | in-contract |  |
| `aws_elasticache_reserved_cache_node` | marker-carried | pending-ratification | 1 |
| `aws_elasticache_serverless_cache` | marker-carried | in-contract |  |
| `aws_elasticache_subnet_group` | marker-carried | in-contract |  |
| `aws_elasticache_user` | marker-carried | in-contract |  |
| `aws_elasticache_user_group` | marker-carried | in-contract |  |
| `aws_elasticache_user_group_association` | declaration-carried | in-contract |  |
| `aws_elasticsearch_domain` | marker-carried | in-contract |  |
| `aws_elasticsearch_domain_policy` | record-carried | needs-evidence | 5 |
| `aws_elasticsearch_domain_saml_options` | record-carried | pending-ratification | 1 |
| `aws_elasticsearch_vpc_endpoint` | record-carried | in-contract |  |
| `aws_elastictranscoder_pipeline` | record-carried | in-contract |  |
| `aws_elastictranscoder_preset` | record-carried | pending-ratification | 1 |
| `aws_elb` | marker-carried | in-contract |  |
| `aws_elb_attachment` | record-carried | needs-evidence | 5 |
| `aws_emr_block_public_access_configuration` | record-carried | pending-ratification | 1 |
| `aws_emr_cluster` | marker-carried | in-contract |  |
| `aws_emr_instance_fleet` | record-carried | pending-mechanism | 3 |
| `aws_emr_instance_group` | record-carried | pending-mechanism | 3 |
| `aws_emr_managed_scaling_policy` | record-carried | pending-ratification | 1 |
| `aws_emr_security_configuration` | declaration-carried | in-contract |  |
| `aws_emr_studio` | marker-carried | in-contract |  |
| `aws_emr_studio_session_mapping` | declaration-carried | in-contract |  |
| `aws_emrcontainers_job_template` | marker-carried | pending-ratification | 1 |
| `aws_emrcontainers_virtual_cluster` | marker-carried | in-contract |  |
| `aws_emrserverless_application` | marker-carried | in-contract |  |
| `aws_evidently_feature` | marker-carried | pending-ratification | 1 |
| `aws_evidently_launch` | marker-carried | pending-ratification | 1 |
| `aws_evidently_project` | marker-carried | in-contract |  |
| `aws_evidently_segment` | marker-carried | in-contract |  |
| `aws_finspace_kx_cluster` | marker-carried | pending-ratification | 1 |
| `aws_finspace_kx_database` | marker-carried | pending-ratification | 1 |
| `aws_finspace_kx_dataview` | marker-carried | pending-ratification | 1 |
| `aws_finspace_kx_environment` | marker-carried | pending-ratification | 1 |
| `aws_finspace_kx_scaling_group` | marker-carried | pending-ratification | 1 |
| `aws_finspace_kx_user` | marker-carried | pending-ratification | 1 |
| `aws_finspace_kx_volume` | marker-carried | pending-ratification | 1 |
| `aws_fis_experiment_template` | marker-carried | in-contract |  |
| `aws_fis_target_account_configuration` | declaration-carried | in-contract |  |
| `aws_flow_log` | marker-carried | in-contract |  |
| `aws_fms_admin_account` | record-carried | pending-ratification | 1 |
| `aws_fms_policy` | marker-carried | pending-ratification | 1 |
| `aws_fms_resource_set` | marker-carried | in-contract |  |
| `aws_fsx_backup` | marker-carried | pending-ratification | 1 |
| `aws_fsx_data_repository_association` | marker-carried | in-contract |  |
| `aws_fsx_file_cache` | marker-carried | pending-ratification | 1 |
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
| `aws_gamelift_game_session_queue` | marker-carried | pending-ratification | 1 |
| `aws_gamelift_script` | marker-carried | in-contract |  |
| `aws_glacier_vault` | marker-carried | pending-ratification | 1 |
| `aws_glacier_vault_lock` | record-carried | pending-ratification | 1 |
| `aws_globalaccelerator_accelerator` | marker-carried | in-contract |  |
| `aws_globalaccelerator_cross_account_attachment` | marker-carried | in-contract |  |
| `aws_globalaccelerator_custom_routing_accelerator` | marker-carried | pending-ratification | 1 |
| `aws_globalaccelerator_custom_routing_endpoint_group` | record-carried | pending-ratification | 1 |
| `aws_globalaccelerator_custom_routing_listener` | record-carried | pending-ratification | 1 |
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
| `aws_glue_partition` | record-carried | pending-mechanism | 3 |
| `aws_glue_partition_index` | record-carried | pending-ratification | 1 |
| `aws_glue_registry` | marker-carried | in-contract |  |
| `aws_glue_resource_policy` | declaration-carried | in-contract |  |
| `aws_glue_schema` | marker-carried | in-contract |  |
| `aws_glue_security_configuration` | declaration-carried | in-contract |  |
| `aws_glue_trigger` | marker-carried | in-contract |  |
| `aws_glue_user_defined_function` | declaration-carried | in-contract |  |
| `aws_glue_workflow` | marker-carried | in-contract |  |
| `aws_grafana_license_association` | record-carried | pending-ratification | 1 |
| `aws_grafana_role_association` | record-carried | pending-ratification | 1 |
| `aws_grafana_workspace` | marker-carried | in-contract |  |
| `aws_grafana_workspace_api_key` | record-carried | pending-ratification | 1 |
| `aws_grafana_workspace_saml_configuration` | declaration-carried | in-contract |  |
| `aws_grafana_workspace_service_account` | record-carried | pending-mechanism | 3 |
| `aws_grafana_workspace_service_account_token` | record-carried | pending-ratification | 1 |
| `aws_guardduty_detector` | marker-carried | in-contract |  |
| `aws_guardduty_detector_feature` | record-carried | needs-evidence | 5 |
| `aws_guardduty_filter` | marker-carried | in-contract |  |
| `aws_guardduty_invite_accepter` | record-carried | pending-ratification | 1 |
| `aws_guardduty_ipset` | marker-carried | in-contract |  |
| `aws_guardduty_malware_protection_plan` | marker-carried | in-contract |  |
| `aws_guardduty_member` | declaration-carried | in-contract |  |
| `aws_guardduty_member_detector_feature` | record-carried | pending-ratification | 1 |
| `aws_guardduty_organization_admin_account` | declaration-carried | in-contract |  |
| `aws_guardduty_organization_configuration` | declaration-carried | in-contract |  |
| `aws_guardduty_organization_configuration_feature` | record-carried | pending-ratification | 1 |
| `aws_guardduty_publishing_destination` | marker-carried | in-contract |  |
| `aws_guardduty_threatintelset` | marker-carried | in-contract |  |
| `aws_iam_access_key` | record-carried | in-contract |  |
| `aws_iam_account_alias` | declaration-carried | in-contract |  |
| `aws_iam_account_password_policy` | declaration-carried | in-contract |  |
| `aws_iam_group` | declaration-carried | in-contract |  |
| `aws_iam_group_membership` | record-carried | pending-mechanism | 7 |
| `aws_iam_group_policies_exclusive` | record-carried | pending-ratification | 1 |
| `aws_iam_group_policy` | declaration-carried | in-contract |  |
| `aws_iam_group_policy_attachment` | declaration-carried | in-contract |  |
| `aws_iam_group_policy_attachments_exclusive` | record-carried | pending-ratification | 1 |
| `aws_iam_instance_profile` | marker-carried | in-contract |  |
| `aws_iam_openid_connect_provider` | marker-carried | in-contract |  |
| `aws_iam_organizations_features` | record-carried | pending-ratification | 1 |
| `aws_iam_outbound_web_identity_federation` | record-carried | pending-ratification | 1 |
| `aws_iam_policy` | marker-carried | in-contract |  |
| `aws_iam_policy_attachment` | declaration-carried | pending-ratification | 1 |
| `aws_iam_role` | marker-carried | in-contract |  |
| `aws_iam_role_policies_exclusive` | declaration-carried | in-contract |  |
| `aws_iam_role_policy` | declaration-carried | in-contract |  |
| `aws_iam_role_policy_attachment` | declaration-carried | in-contract |  |
| `aws_iam_role_policy_attachments_exclusive` | declaration-carried | in-contract |  |
| `aws_iam_saml_provider` | marker-carried | in-contract |  |
| `aws_iam_security_token_service_preferences` | record-carried | pending-ratification | 1 |
| `aws_iam_server_certificate` | marker-carried | in-contract |  |
| `aws_iam_service_linked_role` | marker-carried | in-contract |  |
| `aws_iam_service_specific_credential` | record-carried | in-contract |  |
| `aws_iam_signing_certificate` | record-carried | pending-ratification | 1 |
| `aws_iam_user` | marker-carried | in-contract |  |
| `aws_iam_user_group_membership` | declaration-carried | in-contract |  |
| `aws_iam_user_login_profile` | declaration-carried | in-contract |  |
| `aws_iam_user_policies_exclusive` | record-carried | pending-ratification | 1 |
| `aws_iam_user_policy` | declaration-carried | in-contract |  |
| `aws_iam_user_policy_attachment` | declaration-carried | in-contract |  |
| `aws_iam_user_policy_attachments_exclusive` | record-carried | pending-ratification | 1 |
| `aws_iam_user_ssh_key` | record-carried | pending-ratification | 1 |
| `aws_iam_virtual_mfa_device` | marker-carried | in-contract |  |
| `aws_identitystore_group` | record-carried | pending-mechanism | 3 |
| `aws_identitystore_group_membership` | record-carried | pending-mechanism | 3 |
| `aws_identitystore_user` | record-carried | pending-ratification | 1 |
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
| `aws_inspector2_enabler` | record-carried | pending-ratification | 1 |
| `aws_inspector2_filter` | marker-carried | in-contract |  |
| `aws_inspector2_member_association` | declaration-carried | in-contract |  |
| `aws_inspector2_organization_configuration` | record-carried | pending-ratification | 1 |
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
| `aws_iot_ca_certificate` | marker-carried | pending-ratification | 1 |
| `aws_iot_certificate` | record-carried | pending-mechanism | 7 |
| `aws_iot_domain_configuration` | marker-carried | in-contract |  |
| `aws_iot_event_configurations` | declaration-carried | in-contract |  |
| `aws_iot_indexing_configuration` | record-carried | pending-ratification | 1 |
| `aws_iot_logging_options` | record-carried | pending-ratification | 1 |
| `aws_iot_policy` | marker-carried | in-contract |  |
| `aws_iot_policy_attachment` | record-carried | pending-mechanism | 7 |
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
| `aws_ivs_playback_key_pair` | excluded by design | excluded | 6 |
| `aws_ivs_recording_configuration` | marker-carried | in-contract |  |
| `aws_ivschat_logging_configuration` | marker-carried | in-contract |  |
| `aws_ivschat_room` | marker-carried | in-contract |  |
| `aws_kendra_data_source` | marker-carried | in-contract |  |
| `aws_kendra_experience` | record-carried | pending-ratification | 1 |
| `aws_kendra_faq` | marker-carried | in-contract |  |
| `aws_kendra_index` | marker-carried | in-contract |  |
| `aws_kendra_query_suggestions_block_list` | marker-carried | pending-ratification | 1 |
| `aws_kendra_thesaurus` | marker-carried | pending-ratification | 1 |
| `aws_key_pair` | marker-carried | in-contract |  |
| `aws_keyspaces_keyspace` | marker-carried | in-contract |  |
| `aws_keyspaces_table` | marker-carried | in-contract |  |
| `aws_kinesis_account_settings` | declaration-carried | in-contract |  |
| `aws_kinesis_analytics_application` | marker-carried | in-contract |  |
| `aws_kinesis_firehose_delivery_stream` | marker-carried | in-contract |  |
| `aws_kinesis_resource_policy` | declaration-carried | in-contract |  |
| `aws_kinesis_stream` | marker-carried | in-contract |  |
| `aws_kinesis_stream_consumer` | marker-carried | in-contract |  |
| `aws_kinesis_video_stream` | marker-carried | pending-ratification | 1 |
| `aws_kinesisanalyticsv2_application` | marker-carried | in-contract |  |
| `aws_kinesisanalyticsv2_application_snapshot` | record-carried | pending-ratification | 1 |
| `aws_kms_alias` | declaration-carried | in-contract |  |
| `aws_kms_ciphertext` | record-carried | pending-ratification | 1 |
| `aws_kms_custom_key_store` | record-carried | in-contract |  |
| `aws_kms_external_key` | marker-carried | in-contract |  |
| `aws_kms_grant` | record-carried | pending-mechanism | 3 |
| `aws_kms_key` | marker-carried | in-contract |  |
| `aws_kms_key_policy` | record-carried | pending-ratification | 1 |
| `aws_kms_replica_external_key` | marker-carried | pending-ratification | 1 |
| `aws_kms_replica_key` | marker-carried | in-contract |  |
| `aws_lakeformation_data_cells_filter` | declaration-carried | in-contract |  |
| `aws_lakeformation_data_lake_settings` | record-carried | in-contract |  |
| `aws_lakeformation_identity_center_configuration` | record-carried | pending-ratification | 1 |
| `aws_lakeformation_lf_tag` | declaration-carried | in-contract |  |
| `aws_lakeformation_lf_tag_expression` | declaration-carried | in-contract |  |
| `aws_lakeformation_opt_in` | record-carried | pending-ratification | 1 |
| `aws_lakeformation_permissions` | record-carried | pending-mechanism | 7 |
| `aws_lakeformation_resource` | record-carried | pending-mechanism | 7 |
| `aws_lakeformation_resource_lf_tag` | record-carried | pending-mechanism | 7 |
| `aws_lakeformation_resource_lf_tags` | record-carried | pending-ratification | 1 |
| `aws_lambda_alias` | record-carried | pending-ratification | 1 |
| `aws_lambda_capacity_provider` | marker-carried | in-contract |  |
| `aws_lambda_code_signing_config` | marker-carried | in-contract |  |
| `aws_lambda_event_source_mapping` | marker-carried | in-contract |  |
| `aws_lambda_function` | marker-carried | in-contract |  |
| `aws_lambda_function_event_invoke_config` | declaration-carried | in-contract |  |
| `aws_lambda_function_recursion_config` | record-carried | pending-ratification | 1 |
| `aws_lambda_function_scaling_config` | declaration-carried | in-contract |  |
| `aws_lambda_function_url` | record-carried | pending-ratification | 1 |
| `aws_lambda_invocation` | record-carried | pending-ratification | 1 |
| `aws_lambda_layer_version` | record-carried | in-contract |  |
| `aws_lambda_layer_version_permission` | declaration-carried | in-contract |  |
| `aws_lambda_permission` | declaration-carried | in-contract |  |
| `aws_lambda_provisioned_concurrency_config` | declaration-carried | in-contract |  |
| `aws_lambda_runtime_management_config` | record-carried | pending-ratification | 1 |
| `aws_launch_configuration` | declaration-carried | in-contract |  |
| `aws_launch_template` | marker-carried | in-contract |  |
| `aws_lb` | marker-carried | in-contract |  |
| `aws_lb_cookie_stickiness_policy` | record-carried | needs-evidence | 5 |
| `aws_lb_listener` | marker-carried | in-contract |  |
| `aws_lb_listener_certificate` | declaration-carried | in-contract |  |
| `aws_lb_listener_rule` | marker-carried | in-contract |  |
| `aws_lb_ssl_negotiation_policy` | record-carried | needs-evidence | 5 |
| `aws_lb_target_group` | marker-carried | in-contract |  |
| `aws_lb_target_group_attachment` | declaration-carried | in-contract |  |
| `aws_lb_trust_store` | marker-carried | in-contract |  |
| `aws_lb_trust_store_revocation` | declaration-carried | in-contract |  |
| `aws_lex_bot` | record-carried | pending-ratification | 1 |
| `aws_lex_bot_alias` | record-carried | pending-ratification | 1 |
| `aws_lex_intent` | record-carried | pending-ratification | 1 |
| `aws_lex_slot_type` | record-carried | pending-ratification | 1 |
| `aws_lexv2models_bot` | marker-carried | in-contract |  |
| `aws_lexv2models_bot_locale` | declaration-carried | in-contract |  |
| `aws_lexv2models_bot_version` | record-carried | in-contract |  |
| `aws_lexv2models_intent` | record-carried | pending-ratification | 1 |
| `aws_lexv2models_slot` | record-carried | pending-ratification | 1 |
| `aws_lexv2models_slot_type` | record-carried | pending-ratification | 1 |
| `aws_licensemanager_association` | declaration-carried | in-contract |  |
| `aws_licensemanager_grant` | record-carried | in-contract |  |
| `aws_licensemanager_grant_accepter` | record-carried | pending-ratification | 1 |
| `aws_licensemanager_license_configuration` | marker-carried | pending-ratification | 1 |
| `aws_lightsail_bucket` | marker-carried | in-contract |  |
| `aws_lightsail_bucket_access_key` | record-carried | pending-ratification | 1 |
| `aws_lightsail_bucket_resource_access` | declaration-carried | in-contract |  |
| `aws_lightsail_certificate` | marker-carried | in-contract |  |
| `aws_lightsail_container_service` | marker-carried | in-contract |  |
| `aws_lightsail_container_service_deployment_version` | record-carried | pending-ratification | 1 |
| `aws_lightsail_database` | marker-carried | in-contract |  |
| `aws_lightsail_disk` | marker-carried | in-contract |  |
| `aws_lightsail_disk_attachment` | record-carried | pending-ratification | 1 |
| `aws_lightsail_distribution` | marker-carried | in-contract |  |
| `aws_lightsail_domain` | record-carried | pending-ratification | 1 |
| `aws_lightsail_domain_entry` | declaration-carried | in-contract |  |
| `aws_lightsail_instance` | marker-carried | in-contract |  |
| `aws_lightsail_instance_public_ports` | record-carried | needs-evidence | 5 |
| `aws_lightsail_key_pair` | marker-carried | pending-ratification | 1 |
| `aws_lightsail_lb` | marker-carried | in-contract |  |
| `aws_lightsail_lb_attachment` | declaration-carried | in-contract |  |
| `aws_lightsail_lb_certificate` | declaration-carried | in-contract |  |
| `aws_lightsail_lb_certificate_attachment` | declaration-carried | in-contract |  |
| `aws_lightsail_lb_https_redirection_policy` | record-carried | pending-ratification | 1 |
| `aws_lightsail_lb_stickiness_policy` | record-carried | pending-ratification | 1 |
| `aws_lightsail_static_ip` | declaration-carried | in-contract |  |
| `aws_lightsail_static_ip_attachment` | record-carried | pending-ratification | 1 |
| `aws_load_balancer_backend_server_policy` | record-carried | needs-evidence | 5 |
| `aws_load_balancer_listener_policy` | record-carried | needs-evidence | 5 |
| `aws_load_balancer_policy` | record-carried | needs-evidence | 5 |
| `aws_location_geofence_collection` | marker-carried | in-contract |  |
| `aws_location_map` | marker-carried | in-contract |  |
| `aws_location_place_index` | marker-carried | in-contract |  |
| `aws_location_route_calculator` | marker-carried | in-contract |  |
| `aws_location_tracker` | marker-carried | in-contract |  |
| `aws_location_tracker_association` | declaration-carried | in-contract |  |
| `aws_m2_application` | marker-carried | in-contract |  |
| `aws_m2_deployment` | record-carried | pending-ratification | 1 |
| `aws_m2_environment` | marker-carried | in-contract |  |
| `aws_macie2_account` | record-carried | in-contract |  |
| `aws_macie2_classification_export_configuration` | declaration-carried | in-contract |  |
| `aws_macie2_classification_job` | marker-carried | in-contract |  |
| `aws_macie2_custom_data_identifier` | marker-carried | in-contract |  |
| `aws_macie2_findings_filter` | marker-carried | in-contract |  |
| `aws_macie2_invitation_accepter` | record-carried | pending-ratification | 1 |
| `aws_macie2_member` | marker-carried | in-contract |  |
| `aws_macie2_organization_admin_account` | declaration-carried | in-contract |  |
| `aws_macie2_organization_configuration` | record-carried | pending-ratification | 1 |
| `aws_mailmanager_ingress_point` | marker-carried | in-contract |  |
| `aws_mailmanager_rule_set` | marker-carried | in-contract |  |
| `aws_mailmanager_traffic_policy` | marker-carried | in-contract |  |
| `aws_main_route_table_association` | record-carried | pending-ratification | 1 |
| `aws_media_convert_queue` | marker-carried | pending-ratification | 1 |
| `aws_media_package_channel` | marker-carried | in-contract |  |
| `aws_media_packagev2_channel_group` | marker-carried | in-contract |  |
| `aws_media_store_container` | marker-carried | pending-ratification | 1 |
| `aws_media_store_container_policy` | record-carried | pending-ratification | 1 |
| `aws_medialive_channel` | marker-carried | pending-ratification | 1 |
| `aws_medialive_input` | marker-carried | pending-ratification | 1 |
| `aws_medialive_input_security_group` | marker-carried | pending-ratification | 1 |
| `aws_medialive_multiplex` | marker-carried | in-contract |  |
| `aws_medialive_multiplex_program` | record-carried | pending-ratification | 1 |
| `aws_memorydb_acl` | marker-carried | in-contract |  |
| `aws_memorydb_cluster` | marker-carried | in-contract |  |
| `aws_memorydb_multi_region_cluster` | marker-carried | in-contract |  |
| `aws_memorydb_parameter_group` | marker-carried | in-contract |  |
| `aws_memorydb_snapshot` | marker-carried | pending-ratification | 1 |
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
| `aws_neptune_cluster_endpoint` | marker-carried | pending-ratification | 1 |
| `aws_neptune_cluster_instance` | marker-carried | in-contract |  |
| `aws_neptune_cluster_parameter_group` | marker-carried | in-contract |  |
| `aws_neptune_cluster_snapshot` | record-carried | pending-ratification | 1 |
| `aws_neptune_event_subscription` | marker-carried | pending-ratification | 1 |
| `aws_neptune_global_cluster` | record-carried | pending-ratification | 1 |
| `aws_neptune_parameter_group` | marker-carried | in-contract |  |
| `aws_neptune_subnet_group` | marker-carried | in-contract |  |
| `aws_neptunegraph_graph` | marker-carried | pending-ratification | 1 |
| `aws_neptunegraph_private_graph_endpoint` | declaration-carried | pending-ratification | 1 |
| `aws_network_acl` | marker-carried | in-contract |  |
| `aws_network_acl_association` | record-carried | in-contract |  |
| `aws_network_acl_rule` | declaration-carried | in-contract |  |
| `aws_network_interface` | marker-carried | in-contract |  |
| `aws_network_interface_attachment` | record-carried | in-contract |  |
| `aws_network_interface_permission` | record-carried | pending-mechanism | 4 |
| `aws_network_interface_sg_attachment` | declaration-carried | in-contract |  |
| `aws_networkfirewall_container_association` | marker-carried | pending-ratification | 1 |
| `aws_networkfirewall_firewall` | marker-carried | in-contract |  |
| `aws_networkfirewall_firewall_policy` | marker-carried | in-contract |  |
| `aws_networkfirewall_firewall_transit_gateway_attachment_accepter` | record-carried | pending-ratification | 1 |
| `aws_networkfirewall_logging_configuration` | declaration-carried | in-contract |  |
| `aws_networkfirewall_resource_policy` | record-carried | pending-ratification | 1 |
| `aws_networkfirewall_rule_group` | marker-carried | in-contract |  |
| `aws_networkfirewall_tls_inspection_configuration` | marker-carried | in-contract |  |
| `aws_networkfirewall_vpc_endpoint_association` | marker-carried | in-contract |  |
| `aws_networkflowmonitor_monitor` | marker-carried | in-contract |  |
| `aws_networkflowmonitor_scope` | marker-carried | pending-ratification | 1 |
| `aws_networkmanager_attachment_accepter` | record-carried | pending-ratification | 1 |
| `aws_networkmanager_attachment_routing_policy_label` | declaration-carried | in-contract |  |
| `aws_networkmanager_connect_attachment` | marker-carried | in-contract |  |
| `aws_networkmanager_connect_peer` | marker-carried | in-contract |  |
| `aws_networkmanager_connection` | marker-carried | pending-ratification | 1 |
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
| `aws_networkmanager_transit_gateway_connect_peer_association` | record-carried | pending-ratification | 1 |
| `aws_networkmanager_transit_gateway_peering` | marker-carried | in-contract |  |
| `aws_networkmanager_transit_gateway_registration` | declaration-carried | in-contract |  |
| `aws_networkmanager_transit_gateway_route_table_attachment` | marker-carried | in-contract |  |
| `aws_networkmanager_vpc_attachment` | marker-carried | in-contract |  |
| `aws_networkmonitor_monitor` | marker-carried | pending-ratification | 1 |
| `aws_networkmonitor_probe` | marker-carried | pending-ratification | 1 |
| `aws_notifications_channel_association` | declaration-carried | in-contract |  |
| `aws_notifications_event_rule` | record-carried | pending-mechanism | 4 |
| `aws_notifications_managed_notification_account_contact_association` | declaration-carried | in-contract |  |
| `aws_notifications_managed_notification_additional_channel_association` | declaration-carried | in-contract |  |
| `aws_notifications_notification_configuration` | marker-carried | in-contract |  |
| `aws_notifications_notification_hub` | declaration-carried | in-contract |  |
| `aws_notifications_organizational_unit_association` | declaration-carried | in-contract |  |
| `aws_notifications_organizations_access` | record-carried | pending-ratification | 1 |
| `aws_notificationscontacts_email_contact` | marker-carried | in-contract |  |
| `aws_oam_link` | marker-carried | in-contract |  |
| `aws_oam_sink` | marker-carried | in-contract |  |
| `aws_oam_sink_policy` | record-carried | pending-ratification | 1 |
| `aws_observabilityadmin_centralization_rule_for_organization` | marker-carried | pending-ratification | 1 |
| `aws_observabilityadmin_s3_table_integration` | marker-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_enrichment` | declaration-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_evaluation` | declaration-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_evaluation_for_organization` | declaration-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_pipeline` | marker-carried | in-contract |  |
| `aws_observabilityadmin_telemetry_rule` | marker-carried | pending-ratification | 1 |
| `aws_observabilityadmin_telemetry_rule_for_organization` | marker-carried | pending-ratification | 1 |
| `aws_odb_cloud_autonomous_vm_cluster` | marker-carried | in-contract |  |
| `aws_odb_cloud_exadata_infrastructure` | marker-carried | in-contract |  |
| `aws_odb_cloud_vm_cluster` | marker-carried | in-contract |  |
| `aws_odb_network` | marker-carried | in-contract |  |
| `aws_odb_network_peering_connection` | marker-carried | in-contract |  |
| `aws_opensearch_application` | marker-carried | pending-ratification | 1 |
| `aws_opensearch_authorize_vpc_endpoint_access` | declaration-carried | in-contract |  |
| `aws_opensearch_domain` | marker-carried | in-contract |  |
| `aws_opensearch_domain_policy` | record-carried | pending-ratification | 1 |
| `aws_opensearch_domain_saml_options` | record-carried | pending-ratification | 1 |
| `aws_opensearch_inbound_connection_accepter` | record-carried | pending-ratification | 1 |
| `aws_opensearch_outbound_connection` | record-carried | in-contract |  |
| `aws_opensearch_package` | record-carried | in-contract |  |
| `aws_opensearch_package_association` | declaration-carried | in-contract |  |
| `aws_opensearch_vpc_endpoint` | record-carried | in-contract |  |
| `aws_opensearchserverless_access_policy` | declaration-carried | in-contract |  |
| `aws_opensearchserverless_collection` | marker-carried | in-contract |  |
| `aws_opensearchserverless_collection_group` | marker-carried | in-contract |  |
| `aws_opensearchserverless_lifecycle_policy` | declaration-carried | in-contract |  |
| `aws_opensearchserverless_security_config` | declaration-carried | pending-ratification | 1 |
| `aws_opensearchserverless_security_policy` | declaration-carried | in-contract |  |
| `aws_opensearchserverless_vpc_endpoint` | record-carried | in-contract |  |
| `aws_organizations_account` | marker-carried | in-contract |  |
| `aws_organizations_aws_service_access` | declaration-carried | pending-ratification | 1 |
| `aws_organizations_delegated_administrator` | declaration-carried | in-contract |  |
| `aws_organizations_organization` | record-carried | in-contract |  |
| `aws_organizations_organizational_unit` | marker-carried | in-contract |  |
| `aws_organizations_policy` | marker-carried | in-contract |  |
| `aws_organizations_policy_attachment` | declaration-carried | in-contract |  |
| `aws_organizations_resource_policy` | marker-carried | in-contract |  |
| `aws_organizations_tag` | record-carried | pending-ratification | 1 |
| `aws_osis_pipeline` | marker-carried | in-contract |  |
| `aws_osis_pipeline_endpoint` | record-carried | in-contract |  |
| `aws_osis_resource_policy` | declaration-carried | pending-ratification | 1 |
| `aws_outposts_capacity_task` | record-carried | pending-mechanism | 3 |
| `aws_paymentcryptography_key` | marker-carried | in-contract |  |
| `aws_paymentcryptography_key_alias` | declaration-carried | in-contract |  |
| `aws_pinpoint_adm_channel` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_apns_channel` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_apns_sandbox_channel` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_apns_voip_channel` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_apns_voip_sandbox_channel` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_app` | marker-carried | pending-ratification | 1 |
| `aws_pinpoint_baidu_channel` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_email_channel` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_email_template` | marker-carried | pending-ratification | 1 |
| `aws_pinpoint_event_stream` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_gcm_channel` | record-carried | pending-ratification | 1 |
| `aws_pinpoint_sms_channel` | record-carried | pending-ratification | 1 |
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
| `aws_prometheus_resource_policy` | record-carried | pending-ratification | 1 |
| `aws_prometheus_rule_group_namespace` | marker-carried | in-contract |  |
| `aws_prometheus_scraper` | marker-carried | in-contract |  |
| `aws_prometheus_scraper_logging_configuration` | declaration-carried | in-contract |  |
| `aws_prometheus_workspace` | marker-carried | in-contract |  |
| `aws_prometheus_workspace_configuration` | record-carried | pending-ratification | 1 |
| `aws_proxy_protocol_policy` | record-carried | needs-evidence | 5 |
| `aws_qbusiness_application` | marker-carried | in-contract |  |
| `aws_qldb_ledger` | marker-carried | in-contract |  |
| `aws_qldb_stream` | marker-carried | needs-evidence | 5 |
| `aws_quicksight_account_settings` | record-carried | pending-ratification | 1 |
| `aws_quicksight_account_subscription` | record-carried | pending-ratification | 1 |
| `aws_quicksight_analysis` | marker-carried | in-contract |  |
| `aws_quicksight_custom_permissions` | marker-carried | in-contract |  |
| `aws_quicksight_dashboard` | marker-carried | in-contract |  |
| `aws_quicksight_data_set` | marker-carried | in-contract |  |
| `aws_quicksight_data_source` | marker-carried | in-contract |  |
| `aws_quicksight_folder` | marker-carried | needs-separator | 2 |
| `aws_quicksight_folder_membership` | record-carried | pending-ratification | 1 |
| `aws_quicksight_group` | record-carried | pending-ratification | 1 |
| `aws_quicksight_group_membership` | record-carried | pending-ratification | 1 |
| `aws_quicksight_iam_policy_assignment` | record-carried | pending-ratification | 1 |
| `aws_quicksight_ingestion` | record-carried | pending-ratification | 1 |
| `aws_quicksight_ip_restriction` | record-carried | pending-ratification | 1 |
| `aws_quicksight_key_registration` | record-carried | pending-ratification | 1 |
| `aws_quicksight_namespace` | marker-carried | pending-ratification | 1 |
| `aws_quicksight_refresh_schedule` | declaration-carried | in-contract |  |
| `aws_quicksight_role_custom_permission` | record-carried | pending-ratification | 1 |
| `aws_quicksight_role_membership` | record-carried | pending-ratification | 1 |
| `aws_quicksight_template` | marker-carried | in-contract |  |
| `aws_quicksight_template_alias` | record-carried | pending-ratification | 1 |
| `aws_quicksight_theme` | marker-carried | in-contract |  |
| `aws_quicksight_user` | record-carried | pending-ratification | 1 |
| `aws_quicksight_user_custom_permission` | record-carried | pending-ratification | 1 |
| `aws_quicksight_vpc_connection` | marker-carried | in-contract |  |
| `aws_ram_permission` | marker-carried | in-contract |  |
| `aws_ram_principal_association` | declaration-carried | in-contract |  |
| `aws_ram_resource_association` | declaration-carried | in-contract |  |
| `aws_ram_resource_share` | marker-carried | in-contract |  |
| `aws_ram_resource_share_accepter` | record-carried | pending-ratification | 1 |
| `aws_ram_resource_share_associations_exclusive` | declaration-carried | pending-ratification | 1 |
| `aws_ram_sharing_with_organization` | record-carried | pending-ratification | 1 |
| `aws_rbin_rule` | marker-carried | in-contract |  |
| `aws_rds_certificate` | record-carried | pending-ratification | 1 |
| `aws_rds_cluster` | marker-carried | in-contract |  |
| `aws_rds_cluster_activity_stream` | record-carried | pending-ratification | 1 |
| `aws_rds_cluster_endpoint` | marker-carried | in-contract |  |
| `aws_rds_cluster_instance` | marker-carried | in-contract |  |
| `aws_rds_cluster_parameter_group` | marker-carried | in-contract |  |
| `aws_rds_cluster_role_association` | declaration-carried | in-contract |  |
| `aws_rds_cluster_snapshot_copy` | marker-carried | pending-ratification | 1 |
| `aws_rds_custom_db_engine_version` | marker-carried | in-contract |  |
| `aws_rds_export_task` | record-carried | pending-ratification | 1 |
| `aws_rds_global_cluster` | marker-carried | in-contract |  |
| `aws_rds_instance_state` | record-carried | pending-ratification | 1 |
| `aws_rds_integration` | marker-carried | in-contract |  |
| `aws_rds_reserved_instance` | marker-carried | pending-ratification | 1 |
| `aws_rds_shard_group` | marker-carried | in-contract |  |
| `aws_redshift_authentication_profile` | record-carried | pending-ratification | 1 |
| `aws_redshift_cluster` | marker-carried | in-contract |  |
| `aws_redshift_cluster_iam_roles` | record-carried | pending-ratification | 1 |
| `aws_redshift_cluster_snapshot` | marker-carried | pending-ratification | 1 |
| `aws_redshift_data_share_authorization` | record-carried | pending-ratification | 1 |
| `aws_redshift_data_share_consumer_association` | record-carried | pending-ratification | 1 |
| `aws_redshift_endpoint_access` | declaration-carried | in-contract |  |
| `aws_redshift_endpoint_authorization` | declaration-carried | in-contract |  |
| `aws_redshift_event_subscription` | marker-carried | pending-ratification | 1 |
| `aws_redshift_hsm_client_certificate` | marker-carried | pending-ratification | 1 |
| `aws_redshift_hsm_configuration` | marker-carried | pending-ratification | 1 |
| `aws_redshift_idc_application` | marker-carried | pending-ratification | 1 |
| `aws_redshift_integration` | marker-carried | pending-ratification | 1 |
| `aws_redshift_logging` | declaration-carried | in-contract |  |
| `aws_redshift_namespace_registration` | declaration-carried | pending-ratification | 1 |
| `aws_redshift_parameter_group` | marker-carried | in-contract |  |
| `aws_redshift_partner` | record-carried | pending-ratification | 1 |
| `aws_redshift_resource_policy` | record-carried | pending-ratification | 1 |
| `aws_redshift_scheduled_action` | record-carried | pending-ratification | 1 |
| `aws_redshift_snapshot_copy` | record-carried | pending-ratification | 1 |
| `aws_redshift_snapshot_copy_grant` | marker-carried | pending-ratification | 1 |
| `aws_redshift_snapshot_schedule` | marker-carried | in-contract |  |
| `aws_redshift_snapshot_schedule_association` | record-carried | pending-ratification | 1 |
| `aws_redshift_subnet_group` | marker-carried | in-contract |  |
| `aws_redshift_usage_limit` | marker-carried | pending-ratification | 1 |
| `aws_redshiftdata_statement` | record-carried | in-contract |  |
| `aws_redshiftserverless_custom_domain_association` | declaration-carried | in-contract |  |
| `aws_redshiftserverless_endpoint_access` | record-carried | pending-ratification | 1 |
| `aws_redshiftserverless_namespace` | marker-carried | in-contract |  |
| `aws_redshiftserverless_resource_policy` | record-carried | pending-ratification | 1 |
| `aws_redshiftserverless_snapshot` | record-carried | pending-ratification | 1 |
| `aws_redshiftserverless_usage_limit` | record-carried | pending-ratification | 1 |
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
| `aws_route53_delegation_set` | record-carried | pending-ratification | 1 |
| `aws_route53_health_check` | marker-carried | in-contract |  |
| `aws_route53_hosted_zone_dnssec` | declaration-carried | in-contract |  |
| `aws_route53_key_signing_key` | declaration-carried | in-contract |  |
| `aws_route53_query_log` | record-carried | pending-ratification | 1 |
| `aws_route53_record` | declaration-carried | in-contract |  |
| `aws_route53_records_exclusive` | record-carried | pending-ratification | 1 |
| `aws_route53_resolver_config` | record-carried | in-contract |  |
| `aws_route53_resolver_dnssec_config` | record-carried | in-contract |  |
| `aws_route53_resolver_endpoint` | marker-carried | in-contract |  |
| `aws_route53_resolver_firewall_config` | record-carried | pending-ratification | 1 |
| `aws_route53_resolver_firewall_domain_list` | marker-carried | in-contract |  |
| `aws_route53_resolver_firewall_rule` | declaration-carried | in-contract |  |
| `aws_route53_resolver_firewall_rule_group` | marker-carried | in-contract |  |
| `aws_route53_resolver_firewall_rule_group_association` | marker-carried | in-contract |  |
| `aws_route53_resolver_query_log_config` | marker-carried | in-contract |  |
| `aws_route53_resolver_query_log_config_association` | record-carried | in-contract |  |
| `aws_route53_resolver_rule` | marker-carried | in-contract |  |
| `aws_route53_resolver_rule_association` | record-carried | in-contract |  |
| `aws_route53_traffic_policy` | record-carried | pending-mechanism | 3 |
| `aws_route53_traffic_policy_instance` | record-carried | pending-ratification | 1 |
| `aws_route53_vpc_association_authorization` | declaration-carried | in-contract |  |
| `aws_route53_zone` | marker-carried | in-contract |  |
| `aws_route53_zone_association` | declaration-carried | in-contract |  |
| `aws_route53domains_delegation_signer_record` | record-carried | pending-mechanism | 3 |
| `aws_route53domains_domain` | marker-carried | pending-ratification | 1 |
| `aws_route53domains_registered_domain` | marker-carried | pending-ratification | 1 |
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
| `aws_rum_metrics_destination` | record-carried | pending-ratification | 1 |
| `aws_s3_access_point` | marker-carried | pending-ratification | 1 |
| `aws_s3_account_public_access_block` | declaration-carried | in-contract |  |
| `aws_s3_bucket` | marker-carried | in-contract |  |
| `aws_s3_bucket_abac` | record-carried | pending-ratification | 1 |
| `aws_s3_bucket_accelerate_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_acl` | declaration-carried | pending-ratification | 1 |
| `aws_s3_bucket_analytics_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_cors_configuration` | declaration-carried | pending-ratification | 1 |
| `aws_s3_bucket_intelligent_tiering_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_inventory` | declaration-carried | in-contract |  |
| `aws_s3_bucket_lifecycle_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_logging` | declaration-carried | pending-ratification | 1 |
| `aws_s3_bucket_metadata_configuration` | declaration-carried | pending-ratification | 1 |
| `aws_s3_bucket_metric` | declaration-carried | in-contract |  |
| `aws_s3_bucket_notification` | declaration-carried | pending-ratification | 1 |
| `aws_s3_bucket_object` | marker-carried | pending-ratification | 1 |
| `aws_s3_bucket_object_lock_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_ownership_controls` | declaration-carried | pending-ratification | 1 |
| `aws_s3_bucket_policy` | declaration-carried | in-contract |  |
| `aws_s3_bucket_public_access_block` | declaration-carried | in-contract |  |
| `aws_s3_bucket_replication_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_request_payment_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_server_side_encryption_configuration` | declaration-carried | in-contract |  |
| `aws_s3_bucket_versioning` | declaration-carried | in-contract |  |
| `aws_s3_bucket_website_configuration` | declaration-carried | pending-ratification | 1 |
| `aws_s3_directory_bucket` | marker-carried | in-contract |  |
| `aws_s3_object` | marker-carried | pending-ratification | 1 |
| `aws_s3_object_copy` | marker-carried | pending-ratification | 1 |
| `aws_s3control_access_grant` | marker-carried | pending-ratification | 1 |
| `aws_s3control_access_grants_instance` | marker-carried | pending-ratification | 1 |
| `aws_s3control_access_grants_instance_resource_policy` | record-carried | pending-ratification | 1 |
| `aws_s3control_access_grants_location` | marker-carried | pending-ratification | 1 |
| `aws_s3control_access_point_policy` | record-carried | pending-ratification | 1 |
| `aws_s3control_bucket` | marker-carried | in-contract |  |
| `aws_s3control_bucket_lifecycle_configuration` | declaration-carried | in-contract |  |
| `aws_s3control_bucket_policy` | declaration-carried | in-contract |  |
| `aws_s3control_directory_bucket_access_point_scope` | record-carried | pending-ratification | 1 |
| `aws_s3control_multi_region_access_point` | declaration-carried | in-contract |  |
| `aws_s3control_multi_region_access_point_policy` | record-carried | pending-ratification | 1 |
| `aws_s3control_multi_region_access_point_routes` | declaration-carried | pending-ratification | 1 |
| `aws_s3control_object_lambda_access_point` | declaration-carried | in-contract |  |
| `aws_s3control_object_lambda_access_point_policy` | declaration-carried | in-contract |  |
| `aws_s3control_storage_lens_configuration` | marker-carried | in-contract |  |
| `aws_s3files_access_point` | marker-carried | in-contract |  |
| `aws_s3files_file_system` | marker-carried | in-contract |  |
| `aws_s3files_file_system_policy` | declaration-carried | in-contract |  |
| `aws_s3files_mount_target` | record-carried | in-contract |  |
| `aws_s3files_synchronization_configuration` | declaration-carried | pending-ratification | 1 |
| `aws_s3outposts_endpoint` | record-carried | pending-mechanism | 3 |
| `aws_s3tables_namespace` | record-carried | pending-ratification | 1 |
| `aws_s3tables_table` | marker-carried | pending-ratification | 1 |
| `aws_s3tables_table_bucket` | marker-carried | in-contract |  |
| `aws_s3tables_table_bucket_policy` | declaration-carried | in-contract |  |
| `aws_s3tables_table_bucket_replication` | declaration-carried | pending-ratification | 1 |
| `aws_s3tables_table_policy` | record-carried | pending-ratification | 1 |
| `aws_s3tables_table_replication` | declaration-carried | pending-ratification | 1 |
| `aws_s3vectors_index` | marker-carried | in-contract |  |
| `aws_s3vectors_vector_bucket` | marker-carried | in-contract |  |
| `aws_s3vectors_vector_bucket_policy` | declaration-carried | in-contract |  |
| `aws_sagemaker_algorithm` | marker-carried | in-contract |  |
| `aws_sagemaker_app` | marker-carried | in-contract |  |
| `aws_sagemaker_app_image_config` | marker-carried | in-contract |  |
| `aws_sagemaker_code_repository` | marker-carried | in-contract |  |
| `aws_sagemaker_data_quality_job_definition` | marker-carried | in-contract |  |
| `aws_sagemaker_device` | record-carried | pending-ratification | 1 |
| `aws_sagemaker_device_fleet` | marker-carried | in-contract |  |
| `aws_sagemaker_domain` | marker-carried | in-contract |  |
| `aws_sagemaker_endpoint` | marker-carried | in-contract |  |
| `aws_sagemaker_endpoint_configuration` | marker-carried | in-contract |  |
| `aws_sagemaker_feature_group` | marker-carried | in-contract |  |
| `aws_sagemaker_flow_definition` | marker-carried | pending-ratification | 1 |
| `aws_sagemaker_hub` | marker-carried | in-contract |  |
| `aws_sagemaker_hub_content_reference` | marker-carried | in-contract |  |
| `aws_sagemaker_human_task_ui` | marker-carried | pending-ratification | 1 |
| `aws_sagemaker_hyper_parameter_tuning_job` | marker-carried | pending-ratification | 1 |
| `aws_sagemaker_image` | marker-carried | in-contract |  |
| `aws_sagemaker_image_version` | record-carried | pending-mechanism | 3 |
| `aws_sagemaker_labeling_job` | marker-carried | pending-ratification | 1 |
| `aws_sagemaker_mlflow_app` | marker-carried | in-contract |  |
| `aws_sagemaker_mlflow_tracking_server` | marker-carried | in-contract |  |
| `aws_sagemaker_model` | marker-carried | in-contract |  |
| `aws_sagemaker_model_card` | marker-carried | in-contract |  |
| `aws_sagemaker_model_card_export_job` | record-carried | pending-ratification | 1 |
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
| `aws_sagemaker_training_job` | marker-carried | pending-ratification | 1 |
| `aws_sagemaker_user_profile` | marker-carried | in-contract |  |
| `aws_sagemaker_workforce` | record-carried | pending-ratification | 1 |
| `aws_sagemaker_workteam` | marker-carried | in-contract |  |
| `aws_savingsplans_savings_plan` | marker-carried | pending-ratification | 1 |
| `aws_scheduler_schedule` | declaration-carried | in-contract |  |
| `aws_scheduler_schedule_group` | marker-carried | in-contract |  |
| `aws_schemas_discoverer` | marker-carried | in-contract |  |
| `aws_schemas_registry` | marker-carried | pending-ratification | 1 |
| `aws_schemas_registry_policy` | record-carried | pending-ratification | 1 |
| `aws_schemas_schema` | marker-carried | in-contract |  |
| `aws_secretsmanager_secret` | marker-carried | in-contract |  |
| `aws_secretsmanager_secret_policy` | declaration-carried | in-contract |  |
| `aws_secretsmanager_secret_rotation` | declaration-carried | in-contract |  |
| `aws_secretsmanager_secret_version` | record-carried | pending-ratification | 1 |
| `aws_secretsmanager_tag` | declaration-carried | in-contract |  |
| `aws_security_group` | marker-carried | in-contract |  |
| `aws_security_group_rule` | declaration-carried | in-contract |  |
| `aws_securityhub_account` | record-carried | in-contract |  |
| `aws_securityhub_account_v2` | marker-carried | in-contract |  |
| `aws_securityhub_action_target` | record-carried | pending-ratification | 1 |
| `aws_securityhub_aggregator_v2` | marker-carried | in-contract |  |
| `aws_securityhub_automation_rule` | marker-carried | in-contract |  |
| `aws_securityhub_automation_rule_v2` | marker-carried | in-contract |  |
| `aws_securityhub_configuration_policy` | record-carried | in-contract |  |
| `aws_securityhub_configuration_policy_association` | declaration-carried | in-contract |  |
| `aws_securityhub_connector_v2` | marker-carried | in-contract |  |
| `aws_securityhub_finding_aggregator` | record-carried | in-contract |  |
| `aws_securityhub_insight` | record-carried | in-contract |  |
| `aws_securityhub_invite_accepter` | record-carried | pending-ratification | 1 |
| `aws_securityhub_member` | declaration-carried | in-contract |  |
| `aws_securityhub_organization_admin_account` | declaration-carried | in-contract |  |
| `aws_securityhub_organization_configuration` | record-carried | in-contract |  |
| `aws_securityhub_product_subscription` | record-carried | pending-mechanism | 3 |
| `aws_securityhub_standards_control` | declaration-carried | in-contract |  |
| `aws_securityhub_standards_control_association` | declaration-carried | in-contract |  |
| `aws_securityhub_standards_subscription` | record-carried | in-contract |  |
| `aws_securitylake_aws_log_source` | record-carried | pending-ratification | 1 |
| `aws_securitylake_custom_log_source` | record-carried | pending-ratification | 1 |
| `aws_securitylake_data_lake` | marker-carried | in-contract |  |
| `aws_securitylake_subscriber` | marker-carried | in-contract |  |
| `aws_securitylake_subscriber_notification` | record-carried | pending-ratification | 1 |
| `aws_serverlessapplicationrepository_cloudformation_stack` | marker-carried | pending-ratification | 1 |
| `aws_service_discovery_http_namespace` | marker-carried | in-contract |  |
| `aws_service_discovery_instance` | declaration-carried | in-contract |  |
| `aws_service_discovery_private_dns_namespace` | marker-carried | in-contract |  |
| `aws_service_discovery_public_dns_namespace` | marker-carried | in-contract |  |
| `aws_service_discovery_service` | marker-carried | in-contract |  |
| `aws_servicecatalog_budget_resource_association` | declaration-carried | in-contract |  |
| `aws_servicecatalog_constraint` | record-carried | in-contract |  |
| `aws_servicecatalog_organizations_access` | record-carried | pending-ratification | 1 |
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
| `aws_servicequotas_template` | record-carried | pending-ratification | 1 |
| `aws_servicequotas_template_association` | record-carried | pending-ratification | 1 |
| `aws_ses_active_receipt_rule_set` | record-carried | pending-ratification | 1 |
| `aws_ses_configuration_set` | record-carried | pending-ratification | 1 |
| `aws_ses_domain_dkim` | record-carried | pending-ratification | 1 |
| `aws_ses_domain_identity` | record-carried | pending-ratification | 1 |
| `aws_ses_domain_identity_verification` | record-carried | pending-ratification | 1 |
| `aws_ses_domain_mail_from` | record-carried | pending-ratification | 1 |
| `aws_ses_email_identity` | record-carried | pending-ratification | 1 |
| `aws_ses_event_destination` | record-carried | pending-ratification | 1 |
| `aws_ses_identity_notification_topic` | record-carried | pending-ratification | 1 |
| `aws_ses_identity_policy` | declaration-carried | in-contract |  |
| `aws_ses_receipt_filter` | declaration-carried | in-contract |  |
| `aws_ses_receipt_rule` | declaration-carried | in-contract |  |
| `aws_ses_receipt_rule_set` | declaration-carried | in-contract |  |
| `aws_ses_template` | declaration-carried | in-contract |  |
| `aws_sesv2_account_suppression_attributes` | record-carried | pending-ratification | 1 |
| `aws_sesv2_account_vdm_attributes` | declaration-carried | in-contract |  |
| `aws_sesv2_configuration_set` | marker-carried | in-contract |  |
| `aws_sesv2_configuration_set_event_destination` | record-carried | in-contract |  |
| `aws_sesv2_contact_list` | marker-carried | in-contract |  |
| `aws_sesv2_dedicated_ip_assignment` | record-carried | pending-ratification | 1 |
| `aws_sesv2_dedicated_ip_pool` | marker-carried | in-contract |  |
| `aws_sesv2_email_identity` | marker-carried | in-contract |  |
| `aws_sesv2_email_identity_feedback_attributes` | record-carried | pending-ratification | 1 |
| `aws_sesv2_email_identity_mail_from_attributes` | record-carried | pending-ratification | 1 |
| `aws_sesv2_email_identity_policy` | declaration-carried | in-contract |  |
| `aws_sesv2_tenant` | marker-carried | in-contract |  |
| `aws_sesv2_tenant_resource_association` | declaration-carried | in-contract |  |
| `aws_sfn_activity` | marker-carried | in-contract |  |
| `aws_sfn_alias` | record-carried | in-contract |  |
| `aws_sfn_state_machine` | marker-carried | in-contract |  |
| `aws_shield_application_layer_automatic_response` | declaration-carried | needs-evidence | 5 |
| `aws_shield_drt_access_log_bucket_association` | record-carried | pending-ratification | 1 |
| `aws_shield_drt_access_role_arn_association` | record-carried | pending-ratification | 1 |
| `aws_shield_proactive_engagement` | record-carried | in-contract |  |
| `aws_shield_protection` | marker-carried | in-contract |  |
| `aws_shield_protection_group` | marker-carried | in-contract |  |
| `aws_shield_protection_health_check_association` | declaration-carried | in-contract |  |
| `aws_shield_subscription` | record-carried | pending-ratification | 1 |
| `aws_signer_signing_job` | record-carried | in-contract |  |
| `aws_signer_signing_profile` | marker-carried | pending-ratification | 1 |
| `aws_signer_signing_profile_permission` | declaration-carried | in-contract |  |
| `aws_snapshot_create_volume_permission` | record-carried | pending-ratification | 1 |
| `aws_sns_platform_application` | record-carried | pending-ratification | 1 |
| `aws_sns_sms_preferences` | record-carried | pending-ratification | 1 |
| `aws_sns_topic` | marker-carried | in-contract |  |
| `aws_sns_topic_data_protection_policy` | declaration-carried | pending-ratification | 1 |
| `aws_sns_topic_policy` | declaration-carried | in-contract |  |
| `aws_sns_topic_subscription` | record-carried | in-contract |  |
| `aws_spot_datafeed_subscription` | declaration-carried | in-contract |  |
| `aws_spot_fleet_request` | marker-carried | in-contract |  |
| `aws_spot_instance_request` | marker-carried | pending-ratification | 1 |
| `aws_sqs_queue` | marker-carried | in-contract |  |
| `aws_sqs_queue_policy` | declaration-carried | in-contract |  |
| `aws_sqs_queue_redrive_allow_policy` | declaration-carried | pending-ratification | 1 |
| `aws_sqs_queue_redrive_policy` | declaration-carried | pending-ratification | 1 |
| `aws_ssm_activation` | marker-carried | pending-ratification | 1 |
| `aws_ssm_association` | marker-carried | in-contract |  |
| `aws_ssm_default_patch_baseline` | record-carried | pending-ratification | 1 |
| `aws_ssm_document` | marker-carried | pending-ratification | 1 |
| `aws_ssm_maintenance_window` | marker-carried | in-contract |  |
| `aws_ssm_maintenance_window_target` | declaration-carried | in-contract |  |
| `aws_ssm_maintenance_window_task` | record-carried | pending-mechanism | 3 |
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
| `aws_ssoadmin_application_access_scope` | record-carried | pending-ratification | 1 |
| `aws_ssoadmin_application_assignment` | declaration-carried | in-contract |  |
| `aws_ssoadmin_application_assignment_configuration` | declaration-carried | pending-ratification | 1 |
| `aws_ssoadmin_customer_managed_policy_attachment` | record-carried | pending-ratification | 1 |
| `aws_ssoadmin_customer_managed_policy_attachments_exclusive` | declaration-carried | in-contract |  |
| `aws_ssoadmin_instance_access_control_attributes` | declaration-carried | in-contract |  |
| `aws_ssoadmin_managed_policy_attachment` | declaration-carried | in-contract |  |
| `aws_ssoadmin_managed_policy_attachments_exclusive` | declaration-carried | in-contract |  |
| `aws_ssoadmin_permission_set` | marker-carried | in-contract |  |
| `aws_ssoadmin_permission_set_inline_policy` | declaration-carried | in-contract |  |
| `aws_ssoadmin_permissions_boundary_attachment` | declaration-carried | in-contract |  |
| `aws_ssoadmin_region` | declaration-carried | in-contract |  |
| `aws_ssoadmin_trusted_token_issuer` | marker-carried | pending-ratification | 1 |
| `aws_storagegateway_cache` | record-carried | pending-ratification | 1 |
| `aws_storagegateway_cached_iscsi_volume` | marker-carried | pending-ratification | 1 |
| `aws_storagegateway_file_system_association` | marker-carried | pending-ratification | 1 |
| `aws_storagegateway_gateway` | marker-carried | pending-ratification | 1 |
| `aws_storagegateway_nfs_file_share` | marker-carried | pending-ratification | 1 |
| `aws_storagegateway_smb_file_share` | marker-carried | pending-ratification | 1 |
| `aws_storagegateway_stored_iscsi_volume` | marker-carried | pending-ratification | 1 |
| `aws_storagegateway_tape_pool` | marker-carried | in-contract |  |
| `aws_storagegateway_upload_buffer` | record-carried | pending-ratification | 1 |
| `aws_storagegateway_working_storage` | record-carried | pending-ratification | 1 |
| `aws_subnet` | marker-carried | in-contract |  |
| `aws_swf_domain` | marker-carried | pending-ratification | 1 |
| `aws_synthetics_canary` | marker-carried | in-contract |  |
| `aws_synthetics_group` | marker-carried | in-contract |  |
| `aws_synthetics_group_association` | declaration-carried | in-contract |  |
| `aws_timestreaminfluxdb_db_cluster` | marker-carried | in-contract |  |
| `aws_timestreaminfluxdb_db_instance` | marker-carried | in-contract |  |
| `aws_timestreamquery_scheduled_query` | marker-carried | in-contract |  |
| `aws_timestreamwrite_database` | marker-carried | in-contract |  |
| `aws_timestreamwrite_table` | marker-carried | in-contract |  |
| `aws_transcribe_language_model` | marker-carried | pending-ratification | 1 |
| `aws_transcribe_medical_vocabulary` | marker-carried | pending-ratification | 1 |
| `aws_transcribe_vocabulary` | marker-carried | pending-ratification | 1 |
| `aws_transcribe_vocabulary_filter` | marker-carried | pending-ratification | 1 |
| `aws_transfer_access` | declaration-carried | in-contract |  |
| `aws_transfer_agreement` | marker-carried | in-contract |  |
| `aws_transfer_certificate` | marker-carried | in-contract |  |
| `aws_transfer_connector` | marker-carried | in-contract |  |
| `aws_transfer_host_key` | marker-carried | pending-ratification | 1 |
| `aws_transfer_profile` | marker-carried | in-contract |  |
| `aws_transfer_server` | marker-carried | in-contract |  |
| `aws_transfer_ssh_key` | record-carried | pending-ratification | 1 |
| `aws_transfer_tag` | record-carried | pending-ratification | 1 |
| `aws_transfer_user` | marker-carried | in-contract |  |
| `aws_transfer_web_app` | marker-carried | in-contract |  |
| `aws_transfer_web_app_customization` | declaration-carried | in-contract |  |
| `aws_transfer_workflow` | marker-carried | in-contract |  |
| `aws_uxc_account_customizations` | record-carried | pending-ratification | 1 |
| `aws_verifiedaccess_endpoint` | marker-carried | in-contract |  |
| `aws_verifiedaccess_group` | marker-carried | in-contract |  |
| `aws_verifiedaccess_instance` | marker-carried | in-contract |  |
| `aws_verifiedaccess_instance_logging_configuration` | declaration-carried | in-contract |  |
| `aws_verifiedaccess_instance_trust_provider_attachment` | declaration-carried | in-contract |  |
| `aws_verifiedaccess_trust_provider` | marker-carried | in-contract |  |
| `aws_verifiedpermissions_identity_source` | record-carried | pending-mechanism | 3 |
| `aws_verifiedpermissions_policy` | record-carried | pending-mechanism | 3 |
| `aws_verifiedpermissions_policy_store` | marker-carried | in-contract |  |
| `aws_verifiedpermissions_policy_template` | record-carried | pending-mechanism | 3 |
| `aws_verifiedpermissions_schema` | record-carried | pending-ratification | 1 |
| `aws_volume_attachment` | declaration-carried | in-contract |  |
| `aws_vpc` | marker-carried | in-contract |  |
| `aws_vpc_block_public_access_exclusion` | marker-carried | in-contract |  |
| `aws_vpc_block_public_access_options` | declaration-carried | in-contract |  |
| `aws_vpc_dhcp_options` | marker-carried | in-contract |  |
| `aws_vpc_dhcp_options_association` | declaration-carried | in-contract |  |
| `aws_vpc_encryption_control` | marker-carried | in-contract |  |
| `aws_vpc_endpoint` | marker-carried | in-contract |  |
| `aws_vpc_endpoint_connection_accepter` | record-carried | pending-ratification | 1 |
| `aws_vpc_endpoint_connection_notification` | record-carried | in-contract |  |
| `aws_vpc_endpoint_policy` | declaration-carried | in-contract |  |
| `aws_vpc_endpoint_private_dns` | declaration-carried | in-contract |  |
| `aws_vpc_endpoint_route_table_association` | declaration-carried | in-contract |  |
| `aws_vpc_endpoint_security_group_association` | declaration-carried | in-contract |  |
| `aws_vpc_endpoint_service` | marker-carried | in-contract |  |
| `aws_vpc_endpoint_service_allowed_principal` | record-carried | pending-ratification | 1 |
| `aws_vpc_endpoint_service_private_dns_verification` | record-carried | pending-ratification | 1 |
| `aws_vpc_endpoint_subnet_association` | declaration-carried | in-contract |  |
| `aws_vpc_ipam` | marker-carried | in-contract |  |
| `aws_vpc_ipam_organization_admin_account` | record-carried | pending-ratification | 1 |
| `aws_vpc_ipam_pool` | marker-carried | in-contract |  |
| `aws_vpc_ipam_pool_cidr` | declaration-carried | in-contract |  |
| `aws_vpc_ipam_pool_cidr_allocation` | marker-carried | in-contract |  |
| `aws_vpc_ipam_preview_next_cidr` | record-carried | pending-ratification | 1 |
| `aws_vpc_ipam_resource_discovery` | marker-carried | in-contract |  |
| `aws_vpc_ipam_resource_discovery_association` | marker-carried | in-contract |  |
| `aws_vpc_ipam_scope` | marker-carried | in-contract |  |
| `aws_vpc_ipv4_cidr_block_association` | record-carried | in-contract |  |
| `aws_vpc_ipv6_cidr_block_association` | record-carried | pending-ratification | 1 |
| `aws_vpc_network_performance_metric_subscription` | record-carried | needs-separator | 2 |
| `aws_vpc_peering_connection` | marker-carried | in-contract |  |
| `aws_vpc_peering_connection_accepter` | marker-carried | pending-ratification | 1 |
| `aws_vpc_peering_connection_options` | record-carried | pending-ratification | 1 |
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
| `aws_vpclattice_target_group_attachment` | record-carried | pending-ratification | 1 |
| `aws_vpn_concentrator` | marker-carried | in-contract |  |
| `aws_vpn_connection` | marker-carried | in-contract |  |
| `aws_vpn_connection_route` | record-carried | needs-separator | 2 |
| `aws_vpn_gateway` | marker-carried | in-contract |  |
| `aws_vpn_gateway_attachment` | record-carried | in-contract |  |
| `aws_vpn_gateway_route_propagation` | record-carried | in-contract |  |
| `aws_waf_byte_match_set` | record-carried | in-contract |  |
| `aws_waf_geo_match_set` | record-carried | pending-ratification | 1 |
| `aws_waf_ipset` | record-carried | in-contract |  |
| `aws_waf_rate_based_rule` | marker-carried | pending-ratification | 1 |
| `aws_waf_regex_match_set` | record-carried | pending-ratification | 1 |
| `aws_waf_regex_pattern_set` | record-carried | pending-ratification | 1 |
| `aws_waf_rule` | marker-carried | in-contract |  |
| `aws_waf_rule_group` | marker-carried | pending-ratification | 1 |
| `aws_waf_size_constraint_set` | record-carried | in-contract |  |
| `aws_waf_sql_injection_match_set` | record-carried | in-contract |  |
| `aws_waf_web_acl` | marker-carried | in-contract |  |
| `aws_waf_xss_match_set` | record-carried | in-contract |  |
| `aws_wafregional_byte_match_set` | record-carried | in-contract |  |
| `aws_wafregional_geo_match_set` | record-carried | in-contract |  |
| `aws_wafregional_ipset` | record-carried | in-contract |  |
| `aws_wafregional_rate_based_rule` | marker-carried | in-contract |  |
| `aws_wafregional_regex_match_set` | record-carried | pending-ratification | 1 |
| `aws_wafregional_regex_pattern_set` | record-carried | in-contract |  |
| `aws_wafregional_rule` | marker-carried | in-contract |  |
| `aws_wafregional_rule_group` | marker-carried | pending-ratification | 1 |
| `aws_wafregional_size_constraint_set` | record-carried | in-contract |  |
| `aws_wafregional_sql_injection_match_set` | record-carried | in-contract |  |
| `aws_wafregional_web_acl` | marker-carried | in-contract |  |
| `aws_wafregional_web_acl_association` | declaration-carried | in-contract |  |
| `aws_wafregional_xss_match_set` | record-carried | in-contract |  |
| `aws_wafv2_api_key` | excluded by design | excluded | 6 |
| `aws_wafv2_ip_set` | marker-carried | in-contract |  |
| `aws_wafv2_regex_pattern_set` | marker-carried | in-contract |  |
| `aws_wafv2_rule_group` | marker-carried | in-contract |  |
| `aws_wafv2_web_acl` | marker-carried | in-contract |  |
| `aws_wafv2_web_acl_association` | declaration-carried | in-contract |  |
| `aws_wafv2_web_acl_logging_configuration` | declaration-carried | in-contract |  |
| `aws_wafv2_web_acl_rule` | declaration-carried | in-contract |  |
| `aws_wafv2_web_acl_rule_group_association` | record-carried | pending-ratification | 1 |
| `aws_workmail_default_domain` | declaration-carried | pending-ratification | 1 |
| `aws_workmail_domain` | declaration-carried | in-contract |  |
| `aws_workmail_group` | record-carried | pending-mechanism | 3 |
| `aws_workmail_organization` | marker-carried | pending-ratification | 1 |
| `aws_workmail_user` | record-carried | pending-mechanism | 3 |
| `aws_workspaces_connection_alias` | marker-carried | in-contract |  |
| `aws_workspaces_directory` | marker-carried | pending-ratification | 1 |
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
| `aws_xray_indexing_rule` | declaration-carried | pending-ratification | 1 |
| `aws_xray_resource_policy` | declaration-carried | in-contract |  |
| `aws_xray_sampling_rule` | marker-carried | in-contract |  |
| `aws_xray_trace_segment_destination` | declaration-carried | in-contract |  |
<!-- readiness-gen:end types -->
