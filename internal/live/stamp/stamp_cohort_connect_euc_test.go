// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The connect-euc cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableConnectEuc = []string{
	// Registry-ratified Connect and end-user computing batch (#40, #44,
	// issue #65). Every type below carries a real tags argument,
	// confirmed against the provider's Argument Reference for each.
	// aws_connect_user_hierarchy_structure, this batch's one
	// Components-built (not marker-path) admission, carries no tags
	// argument at all and is pinned untaggable below instead;
	// aws_connect_instance_storage_config, this batch's one rejected
	// proposal, is untaggable too and never reached the admission table.
	// See live/e2e/estates/connect-euc/README.md.
	"aws_connect_instance",
	"aws_connect_phone_number",
	"aws_connect_contact_flow",
	"aws_connect_contact_flow_module",
	"aws_connect_hours_of_operation",
	"aws_connect_queue",
	"aws_connect_quick_connect",
	"aws_connect_routing_profile",
	"aws_connect_security_profile",
	"aws_connect_user",
	"aws_connect_user_hierarchy_group",
	"aws_workspaces_connection_alias",
	"aws_workspaces_ip_group",
	"aws_workspaces_pool",
	"aws_workspaces_workspace",
	"aws_workspacesweb_browser_settings",
	"aws_workspacesweb_data_protection_settings",
	"aws_workspacesweb_identity_provider",
	"aws_workspacesweb_ip_access_settings",
	"aws_workspacesweb_network_settings",
	"aws_workspacesweb_portal",
	"aws_workspacesweb_session_logger",
	"aws_workspacesweb_trust_store",
	"aws_workspacesweb_user_access_logging_settings",
	"aws_workspacesweb_user_settings",
	// #175 ratification batch (PROPOSE, issue #65), 2026-08-15:
	// taggability per the provider schema survey (live/survey-full.json,
	// v6.59.0 signals.taggable).
	"aws_appstream_stack",
}

var untaggableConnectEuc = []string{
	// Registry-ratified Connect and end-user computing batch (#40, #44,
	// issue #65): aws_connect_user_hierarchy_structure's Argument
	// Reference names no tags block at all — it is this batch's one
	// Components-built entry (instance_id alone), not a marker-path
	// admission, so untaggability does not block it. See
	// live/e2e/estates/connect-euc/README.md, "Untaggable types".
	"aws_connect_user_hierarchy_structure",
	// Same batch: WorkSpacesWeb's eight *_association fold-children of
	// AWS::WorkSpacesWeb::Portal, ratified once issue #68's fold-child
	// path was found merged mid-batch. Each Argument Reference lists
	// only its own settings-type ARN, portal_arn and region — no tags
	// block, confirmed against every one of the eight individually.
	// See live/e2e/estates/connect-euc/README.md, "Untaggable types".
	"aws_workspacesweb_browser_settings_association",
	"aws_workspacesweb_data_protection_settings_association",
	"aws_workspacesweb_ip_access_settings_association",
	"aws_workspacesweb_network_settings_association",
	"aws_workspacesweb_session_logger_association",
	"aws_workspacesweb_trust_store_association",
	"aws_workspacesweb_user_access_logging_settings_association",
	"aws_workspacesweb_user_settings_association",
	// #175 ratification batch (PROPOSE, issue #65), 2026-08-15:
	// taggability per the provider schema survey (live/survey-full.json,
	// v6.59.0 signals.taggable).
	"aws_appstream_fleet_stack_association",
	"aws_appstream_user",
	"aws_connect_instance_storage_config",
}

func init() {
	registerCohortStamp(taggableConnectEuc, untaggableConnectEuc, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified Connect and end-user computing batch (#40, #44,
			// issue #65). Taggable/untaggable per the real provider's documented
			// Argument Reference for each type: aws_connect_user_hierarchy_structure
			// carries no tags argument at all (this batch's one Components-built,
			// not marker-path, entry).
			"aws_connect_instance":                                       taggedSchema("id", "arn", "instance_alias"),
			"aws_connect_phone_number":                                   taggedSchema("id", "arn", "target_arn"),
			"aws_connect_contact_flow":                                   taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_contact_flow_module":                            taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_hours_of_operation":                             taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_queue":                                          taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_quick_connect":                                  taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_routing_profile":                                taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_security_profile":                               taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_user":                                           taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_user_hierarchy_group":                           taggedSchema("id", "arn", "instance_id", "name"),
			"aws_connect_user_hierarchy_structure":                       untaggedSchema("id", "instance_id"),
			"aws_workspaces_connection_alias":                            taggedSchema("id", "connection_string"),
			"aws_workspaces_ip_group":                                    taggedSchema("id", "arn", "name"),
			"aws_workspaces_pool":                                        taggedSchema("id", "pool_arn", "pool_id", "pool_name"),
			"aws_workspaces_workspace":                                   taggedSchema("id", "user_name", "directory_id"),
			"aws_workspacesweb_browser_settings":                         taggedSchema("browser_settings_arn"),
			"aws_workspacesweb_data_protection_settings":                 taggedSchema("data_protection_settings_arn"),
			"aws_workspacesweb_identity_provider":                        taggedSchema("identity_provider_arn", "portal_arn"),
			"aws_workspacesweb_ip_access_settings":                       taggedSchema("ip_access_settings_arn"),
			"aws_workspacesweb_network_settings":                         taggedSchema("network_settings_arn"),
			"aws_workspacesweb_portal":                                   taggedSchema("portal_arn"),
			"aws_workspacesweb_session_logger":                           taggedSchema("session_logger_arn"),
			"aws_workspacesweb_trust_store":                              taggedSchema("trust_store_arn"),
			"aws_workspacesweb_user_access_logging_settings":             taggedSchema("user_access_logging_settings_arn"),
			"aws_workspacesweb_user_settings":                            taggedSchema("user_settings_arn"),
			"aws_workspacesweb_browser_settings_association":             untaggedSchema("browser_settings_arn", "portal_arn"),
			"aws_workspacesweb_data_protection_settings_association":     untaggedSchema("data_protection_settings_arn", "portal_arn"),
			"aws_workspacesweb_ip_access_settings_association":           untaggedSchema("ip_access_settings_arn", "portal_arn"),
			"aws_workspacesweb_network_settings_association":             untaggedSchema("network_settings_arn", "portal_arn"),
			"aws_workspacesweb_session_logger_association":               untaggedSchema("session_logger_arn", "portal_arn"),
			"aws_workspacesweb_trust_store_association":                  untaggedSchema("trust_store_arn", "portal_arn"),
			"aws_workspacesweb_user_access_logging_settings_association": untaggedSchema("user_access_logging_settings_arn", "portal_arn"),
			"aws_workspacesweb_user_settings_association":                untaggedSchema("user_settings_arn", "portal_arn"),
			// #175 ratification batch (PROPOSE, issue #65), 2026-08-15.
			"aws_appstream_fleet_stack_association": untaggedSchema("id", "fleet_name", "stack_name"),
			"aws_appstream_stack":                   taggedSchema("id", "arn", "name"),
			"aws_appstream_user":                    untaggedSchema("id", "arn", "user_name", "authentication_type"),
			"aws_connect_instance_storage_config":   untaggedSchema("id", "instance_id", "association_id", "resource_type"),
		})
	})
}
