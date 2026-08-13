// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesConnectEuc is the connect-euc cohort's slice of [admittedTypesV0]:
// the types the connect-euc ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesConnectEuc = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): eighth batch, Connect and
	// ---- end-user computing (Amazon Connect, WorkSpaces, WorkSpacesWeb).
	// ---- Same tools/row-gen pipeline as the batches above, cross-checked
	// ---- against the AWS provider's documented Argument/Attribute/Import
	// ---- sections at the pinned v6.59.0 tag, not accepted on row-gen's own
	// ---- classification alone. AppStream is a deprecated AWS service and
	// ---- was never evaluated (out of scope by this batch's own recipe,
	// ---- not rejected on the merits). WorkSpaces' directory and bundle
	// ---- surface is likewise absent here on purpose:
	// ---- aws_workspaces_directory is real but registry-absent
	// ---- (live/mapping.json: via "cfn-unmodeled", "real WorkSpaces
	// ---- directory registration with no CFN model") and
	// ---- aws_workspaces_bundle does not exist as a managed resource in
	// ---- the pinned provider at all (a data source only) — row-gen prints
	// ---- nothing for either, so there is no proposal to ratify.
	// ----
	// ---- Nine of the twelve Connect rows below are corrections: row-gen's
	// ---- own registry evidence reads each as primaryIdentifier ⊆
	// ---- readOnlyProperties (a clean server-assigned shape), but issue
	// ---- #55's applyImportGrammarDemotions caught every one first —
	// ---- live/import-grammar.json shows each documented import id is
	// ---- argument-composed (instance_id, colon-joined with the type's own
	// ---- id) — so row-gen printed evidence-only rather than a
	// ---- string-buildable row. Reading each Import section directly
	// ---- confirms the composite is real but not reconstructable from
	// ---- configuration alone (the second half is a server-assigned
	// ---- output), the same shape aws_ssoadmin_application's own entry in
	// ---- the identity batch above already ratified: taggable, so
	// ---- recoverable by tag-filtered list rather than by building the
	// ---- import string. This is not the streaming batch's
	// ---- aws_appsync_function shape, which was rejected for the same
	// ---- composite pattern — the deciding difference is taggability, and
	// ---- every Connect child below carries a real tags argument;
	// ---- aws_connect_instance_storage_config does not, and is rejected on
	// ---- exactly that ground. See internal/live/identity/table.go for the
	// ---- per-type evidence. WorkSpacesWeb's eight *_association
	// ---- property-children of AWS::WorkSpacesWeb::Portal ratify too: this
	// ---- batch found issue #68's fold-child branch merged to main
	// ---- mid-write (re-checked by grep against this file immediately
	// ---- before finishing, per this batch's own recipe), but none of the
	// ---- eight actually needs identity.FoldParentTypes' machinery at all —
	// ---- each is an ordinary two-argument concrete composite
	// ---- (SETTINGSARN,PORTALARN, both already-required configuration
	// ---- arguments of the child's own, comma-joined), the same shape
	// ---- aws_eks_access_entry and aws_iam_role_policy already ratify
	// ---- elsewhere in this table, not the API Gateway four's "duplicate
	// ---- the parent's whole composite" shape that machinery exists for.
	// ---- Untaggable (no tags argument in any of the eight), so removal
	// ---- sweep coverage stays the same accepted gap
	// ---- live/LIMITATIONS.md's "Untaggable types cannot be removed by the
	// ---- sweep" entry already carries; declared-instance resolution
	// ---- (plan, apply, read-back) is unaffected either way. See
	// ---- internal/live/identity/table.go for the per-type evidence.
	// ---- Cohort estate: live/e2e/estates/connect-euc.
	"aws_connect_instance":                                       {},
	"aws_connect_phone_number":                                   {},
	"aws_connect_contact_flow":                                   {},
	"aws_connect_contact_flow_module":                            {},
	"aws_connect_hours_of_operation":                             {},
	"aws_connect_queue":                                          {},
	"aws_connect_quick_connect":                                  {},
	"aws_connect_routing_profile":                                {},
	"aws_connect_security_profile":                               {},
	"aws_connect_user":                                           {},
	"aws_connect_user_hierarchy_group":                           {},
	"aws_connect_user_hierarchy_structure":                       {},
	"aws_workspaces_connection_alias":                            {},
	"aws_workspaces_ip_group":                                    {},
	"aws_workspaces_pool":                                        {},
	"aws_workspaces_workspace":                                   {},
	"aws_workspacesweb_browser_settings":                         {},
	"aws_workspacesweb_data_protection_settings":                 {},
	"aws_workspacesweb_identity_provider":                        {},
	"aws_workspacesweb_ip_access_settings":                       {},
	"aws_workspacesweb_network_settings":                         {},
	"aws_workspacesweb_portal":                                   {},
	"aws_workspacesweb_session_logger":                           {},
	"aws_workspacesweb_trust_store":                              {},
	"aws_workspacesweb_user_access_logging_settings":             {},
	"aws_workspacesweb_user_settings":                            {},
	"aws_workspacesweb_browser_settings_association":             {},
	"aws_workspacesweb_data_protection_settings_association":     {},
	"aws_workspacesweb_ip_access_settings_association":           {},
	"aws_workspacesweb_network_settings_association":             {},
	"aws_workspacesweb_session_logger_association":               {},
	"aws_workspacesweb_trust_store_association":                  {},
	"aws_workspacesweb_user_access_logging_settings_association": {},
	"aws_workspacesweb_user_settings_association":                {},
}

func init() { registerCohortAdmitted(admittedTypesConnectEuc) }
