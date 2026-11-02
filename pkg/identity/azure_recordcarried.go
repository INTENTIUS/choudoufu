// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// AzureRecordCarriedReason is why every type in [AzureRecordCarriedTypes]
// reaches the record rung, worded as [MarkerlessReason] is for AWS.
const AzureRecordCarriedReason = "the type has no tags argument and no id of its own to tell it from its parent: " +
	"an azurerm settings object takes its parent's ARM id as its own, and azapi_data_plane_resource is a " +
	"data-plane object with no tags and no ARM id, so no marker can name it and the estate's record is what finds it again"

// AzureRecordCarriedTypes is the maintainer's 2026-10-23 ruling on
// INTENTIUS/choudoufu#2201 (items 2 and 8; built by #2203): the 37 azurerm
// parent-id settings objects and azapi_data_plane_resource are admitted as
// record-carried BY DECLARATION, located by the record rung keyed by type and
// parent id, as AWS's [MarkerlessTypes] rule does for its untaggable types.
//
// The list is a declaration, not a derivation. The 37 are the "parent-id"
// population of the #2147 audit (azurerm 5.10.0): the id azurerm stores is
// the parent's own ARM id, so the object cannot be told apart from the parent
// by id, and it has no tags argument to carry a marker. The object is found
// again by the record, which holds the applied object's id (the parent's id)
// under the block's address; the pair (type, parent id) is the key in the sense
// that two blocks of one type over one parent name the same object and the
// plan refuses the collision. Moving a type in or out of this list is a ruling.
//
// [IsMarkerless] answers true for a member, so the lint veto, the check
// roster and [LocatedType] treat it as they treat an AWS member: with a
// record_store declared the type is admitted ([ClassRecordLocated]); without
// one it is refused as markerless, with [AzureRecordCarriedReason].
// [LocatedType] additionally requires, for a member, that the schema carries
// no marker surface, so a type that gains a tags argument at a provider bump
// leaves this route for the ordinary one rather than being recorded.
var AzureRecordCarriedTypes = map[string]struct{}{
	"azapi_data_plane_resource":                                   {},
	"azurerm_api_management_api_operation_policy":                 {},
	"azurerm_api_management_api_policy":                           {},
	"azurerm_api_management_policy":                               {},
	"azurerm_api_management_product_policy":                       {},
	"azurerm_api_management_workspace_policy":                     {},
	"azurerm_app_service_source_control":                          {},
	"azurerm_app_service_virtual_network_swift_connection":        {},
	"azurerm_cognitive_account_customer_managed_key":              {},
	"azurerm_container_app_environment_custom_domain":             {},
	"azurerm_data_factory_customer_managed_key":                   {},
	"azurerm_data_protection_backup_vault_customer_managed_key":   {},
	"azurerm_databricks_workspace_root_dbfs_customer_managed_key": {},
	"azurerm_eventgrid_partner_configuration":                     {},
	"azurerm_eventhub_namespace_customer_managed_key":             {},
	"azurerm_iotcentral_application_network_rule_set":             {},
	"azurerm_iothub_file_upload":                                  {},
	"azurerm_kubernetes_cluster_deployment_safeguard":             {},
	"azurerm_kusto_cluster_customer_managed_key":                  {},
	"azurerm_log_analytics_cluster_customer_managed_key":          {},
	"azurerm_managed_disk_sas_token":                              {},
	"azurerm_managed_redis_geo_replication":                       {},
	"azurerm_mssql_job_schedule":                                  {},
	"azurerm_netapp_account_encryption":                           {},
	"azurerm_servicebus_namespace_customer_managed_key":           {},
	"azurerm_signalr_service_network_acl":                         {},
	"azurerm_spring_cloud_active_deployment":                      {},
	"azurerm_storage_account_customer_managed_key":                {},
	"azurerm_storage_account_network_rules":                       {},
	"azurerm_storage_account_queue_properties":                    {},
	"azurerm_storage_account_static_website":                      {},
	"azurerm_storage_account_table_properties":                    {},
	"azurerm_storage_blob_inventory_policy":                       {},
	"azurerm_stream_analytics_job_storage_account":                {},
	"azurerm_subnet_nat_gateway_association":                      {},
	"azurerm_subnet_network_security_group_association":           {},
	"azurerm_subnet_route_table_association":                      {},
	"azurerm_web_pubsub_network_acl":                              {},
}
