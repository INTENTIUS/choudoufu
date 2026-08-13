// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableConnectEuc is the connect-euc cohort's slice of [DefaultTable]:
// the identity rows the connect-euc ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableConnectEuc = buildTable(
	// ---- Registry-ratified (#40, #44, #65): eighth batch, Connect and
	// ---- end-user computing ------------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own website/docs/r/
	// source at the pinned v6.59.0 tag), not accepted on the registry's
	// classification alone. Cohort estate: live/e2e/estates/connect-euc.
	//
	// Scope: Amazon Connect (13 mapped types), WorkSpaces (4) and
	// WorkSpacesWeb (18, 10 of them a type of their own and 8
	// property-child folds of AWS::WorkSpacesWeb::Portal). AppStream is a
	// deprecated AWS service and was never evaluated here — out of scope
	// by this batch's recipe, not rejected on the merits. WorkSpaces'
	// wider surface stays out too, for a different reason: an
	// aws_workspaces_directory resource is real in the provider but
	// live/mapping.json carries it via "cfn-unmodeled" ("real WorkSpaces
	// directory registration with no CFN model — the registry's four
	// AWS::WorkSpaces::* types carry no Directory type at all"), so
	// row-gen — which walks the CFN registry — never proposes it; a
	// aws_workspaces_bundle resource does not exist in the pinned
	// provider release at all (bundles are a data source only). Neither
	// is a row-gen proposal this batch could ratify or reject.
	//
	// Nine of the twelve Connect rows below are corrections. row-gen's own
	// registry evidence reads each of aws_connect_contact_flow,
	// _contact_flow_module, _hours_of_operation, _queue, _quick_connect,
	// _routing_profile, _security_profile, _user and _user_hierarchy_group
	// as primaryIdentifier ⊆ readOnlyProperties (a clean server-assigned
	// shape by rule 1), but issue #55's applyImportGrammarDemotions caught
	// every one first: live/import-grammar.json shows each documented
	// import id is argument-composed, so row-gen printed evidence-only
	// rather than a wrong pastable row. Reading each provider Import
	// section directly confirms the same shape nine times over: Connect
	// requires instance_id (a Required, client-supplied argument naming
	// the hosting instance) in configuration, and mints the child's own id
	// itself, colon-joining the two into the documented import string and
	// exporting the join verbatim as the resource's own "id" attribute.
	// The child's own id half is never a configuration argument — this
	// table's Components vocabulary has no way to compose a
	// not-yet-created output, the same gap the streaming batch's
	// aws_appsync_function rejection named — so none of the nine builds an
	// import string. All nine are taggable (a real tags argument
	// confirmed in each provider doc), which is what makes them
	// ratifiable anyway: the same marker path (server-assigned, taggable,
	// recovered by tag-filtered list rather than by building the import
	// string) aws_ssoadmin_application's entry in the identity batch above
	// already established. aws_connect_instance_storage_config is the
	// tenth row-gen evidence-only Connect proposal in scope and is the one
	// this batch rejects: its own composite (instance_id, its own
	// server-minted association_id, and resource_type, colon-joined) is
	// the same shape as the nine ratified above, but its Argument
	// Reference carries no tags block at all — untaggable, so no marker
	// path recovers it either, the same "no admission path recovers it"
	// verdict the identity batch already gave
	// aws_identitystore_group(_membership).
	//
	// aws_connect_user_hierarchy_structure is a different correction
	// entirely: row-gen's registry evidence (primaryIdentifier=
	// [UserHierarchyStructureArn], wholly read-only) again reads
	// server-assigned, demoted the same way by the import-grammar check.
	// But the real Import section shows no composite and no server-minted
	// second half at all — the documented import id is instance_id alone,
	// a Required, already-in-configuration argument, because a Connect
	// instance has at most one hierarchy structure. The registry's
	// read-only-ARN claim oversold the real grammar the same way the EC2
	// batch's aws_vpc_dhcp_options_association correction found: a
	// named-singleton-child of an already-admitted parent, Components-built
	// from that parent's own id argument alone.
	//
	// aws_connect_instance and aws_connect_phone_number are row-gen's own
	// clean server-assigned proposals, confirmed against the real docs
	// with one correction each: row-gen's TEMPLATED "ARN" import-syntax
	// guess is wrong for both — the provider's own Identity Schema and
	// documented import command both use the bare "id" attribute, not the
	// arn, so IdentityAttrs below names "id" rather than "arn".
	//
	// WorkSpaces' four types and WorkSpacesWeb's ten non-fold types are
	// all row-gen's own clean server-assigned proposals (primaryIdentifier
	// ⊆ readOnlyProperties, list-free or single-parent-scoped enumeration,
	// no import-grammar demotion), confirmed against the real docs with no
	// correction needed beyond naming the exact exported attribute each
	// provider doc gives (a bare "id" for the WorkSpaces family's older
	// SDK-based resources; a dedicated *_arn attribute for the
	// WorkSpacesWeb family's newer plugin-framework resources, which
	// export no generic "id" at all).
	//
	// WorkSpacesWeb's eight *_association property-children of
	// AWS::WorkSpacesWeb::Portal (browser_settings, data_protection_settings,
	// ip_access_settings, network_settings, session_logger, trust_store,
	// user_access_logging_settings and user_settings, each doubled into a
	// standalone type above and a Portal-scoped association fold) ratify
	// too, below. row-gen's own notes propose each as "parent-derived
	// admission keyed on aws_workspacesweb_portal once it is ratified" —
	// this batch started under the assumption that needed issue #68's
	// fold-child admission path (identity.FoldParentTypes,
	// discovery.foldChildReadSweep), on a branch that had not merged to
	// main when this batch's recipe was first read. It merged mid-batch;
	// re-checking (grep for a fold-child section in
	// internal/live/lint/admission.go on main) found it landed. Reading
	// each type's real Import section directly settles the question that
	// prompted the re-check either way: none of the eight actually needs
	// [FoldParentTypes] at all. Each is an ordinary two-argument concrete
	// composite — the child's own settings-type ARN and portal_arn,
	// comma-joined, both already-Required configuration arguments of the
	// association type itself — the same shape aws_eks_access_entry and
	// aws_iam_role_policy already ratify elsewhere in this table, not the
	// API Gateway four's "duplicate the parent's whole composite Components
	// verbatim" shape [FoldParentTypes] exists for. None of the eight is
	// taggable (confirmed against each Argument Reference: settings-type
	// ARN, portal_arn and region only, no tags block), so none can carry an
	// ownership marker of its own — the same accepted removal-sweep gap
	// live/LIMITATIONS.md's "Untaggable types cannot be removed by the
	// sweep" entry already carries for aws_iam_role_policy_attachment and
	// aws_vpc_dhcp_options_association above. Declared-instance resolution
	// (plan, apply, read-back) is unaffected either way.

	serverAssigned("aws_connect_instance",
		"Connect assigns the instance's own id at create time; instance_alias is client-chosen (required only when directory_id is not set) but is not the import identity. Confirmed against the provider's own Identity Schema (required: id) and its documented import command (terraform import aws_connect_instance.example f1288a1f-6193-445a-b47e-af739b2) — row-gen's own ARN guess corrected to the bare id its own Attribute Reference and Identity Schema both document.",
		"ID", "id"),
	serverAssigned("aws_connect_phone_number",
		"Connect assigns the phone number's own id at create time; target_arn names the Connect instance (or traffic distribution group) the number is claimed to, but is not the phone number's own identity. Confirmed against the provider's own Identity Schema (required: id) and its documented import command (terraform import aws_connect_phone_number.example 12345678-abcd-1234-efgh-9876543210ab) — row-gen's own ARN guess corrected to the bare id its own Attribute Reference and Identity Schema both document.",
		"ID", "id"),

	serverAssigned("aws_connect_contact_flow",
		"row-gen filed this evidence-only (import-grammar demotion): Connect mints the contact flow's own id at create time; instance_id (Required) names the hosting instance but not the contact flow itself, and the documented import id is instance_id:contact_flow_id, colon-joined, exported verbatim as the resource's own id (\"The identifier of the hosting Amazon Connect Instance and identifier of the Contact Flow separated by a colon\"). contact_flow_id is never a configuration argument, so no Components string build reaches it — taggable (a real tags argument), so recovered by tag-filtered list instead, the same marker-path shape as aws_ssoadmin_application above.",
		"INSTANCEID:CONTACTFLOWID", "id"),
	serverAssigned("aws_connect_contact_flow_module",
		"Same shape as aws_connect_contact_flow above, the contact-flow-module sibling: row-gen filed this evidence-only (import-grammar demotion), the real Import section documents instance_id:contact_flow_module_id colon-joined, exported verbatim as id, and contact_flow_module_id is never a configuration argument. Taggable, recovered by tag-filtered list.",
		"INSTANCEID:CONTACTFLOWMODULEID", "id"),
	serverAssigned("aws_connect_hours_of_operation",
		"Same shape as aws_connect_contact_flow above: row-gen filed this evidence-only (import-grammar demotion), the real Import section documents instance_id:hours_of_operation_id colon-joined, exported verbatim as id, and hours_of_operation_id is never a configuration argument. Taggable, recovered by tag-filtered list.",
		"INSTANCEID:HOURSOFOPERATIONID", "id"),
	serverAssigned("aws_connect_queue",
		"Same shape as aws_connect_contact_flow above: row-gen filed this evidence-only (import-grammar demotion), the real Import section documents instance_id:queue_id colon-joined, exported verbatim as id, and queue_id is never a configuration argument. Taggable, recovered by tag-filtered list.",
		"INSTANCEID:QUEUEID", "id"),
	serverAssigned("aws_connect_quick_connect",
		"Same shape as aws_connect_contact_flow above: row-gen filed this evidence-only (import-grammar demotion), the real Import section documents instance_id:quick_connect_id colon-joined, exported verbatim as id, and quick_connect_id is never a configuration argument. Taggable, recovered by tag-filtered list.",
		"INSTANCEID:QUICKCONNECTID", "id"),
	serverAssigned("aws_connect_routing_profile",
		"Same shape as aws_connect_contact_flow above: row-gen filed this evidence-only (import-grammar demotion), the real Import section documents instance_id:routing_profile_id colon-joined, exported verbatim as id, and routing_profile_id is never a configuration argument. Taggable, recovered by tag-filtered list.",
		"INSTANCEID:ROUTINGPROFILEID", "id"),
	serverAssigned("aws_connect_security_profile",
		"Same shape as aws_connect_contact_flow above: row-gen filed this evidence-only (import-grammar demotion), the real Import section documents instance_id:security_profile_id colon-joined, exported verbatim as id, and security_profile_id is never a configuration argument. Taggable, recovered by tag-filtered list.",
		"INSTANCEID:SECURITYPROFILEID", "id"),
	serverAssigned("aws_connect_user",
		"Same shape as aws_connect_contact_flow above: row-gen filed this evidence-only (import-grammar demotion), the real Import section documents instance_id:user_id colon-joined, exported verbatim as id, and user_id is never a configuration argument. Taggable, recovered by tag-filtered list. routing_profile_id and security_profile_ids are both Required arguments referencing aws_connect_routing_profile and aws_connect_security_profile above, but that is a data-flow dependency, not part of this type's own identity.",
		"INSTANCEID:USERID", "id"),
	serverAssigned("aws_connect_user_hierarchy_group",
		"Same shape as aws_connect_contact_flow above: row-gen filed this evidence-only (import-grammar demotion), the real Import section documents instance_id:hierarchy_group_id colon-joined, exported verbatim as id, and hierarchy_group_id is never a configuration argument. Taggable, recovered by tag-filtered list.",
		"INSTANCEID:HIERARCHYGROUPID", "id"),

	TypeIdentity{
		// row-gen filed this evidence-only (import-grammar demotion of a
		// primaryIdentifier=[UserHierarchyStructureArn], wholly
		// read-only registry claim). The real Import section shows no
		// composite and no server-minted second half at all: the
		// documented import id is instance_id alone (terraform import
		// aws_connect_user_hierarchy_structure.example
		// f1288a1f-6193-445a-b47e-af739b2), because a Connect instance has
		// at most one hierarchy structure — a named-singleton child of the
		// already-admitted aws_connect_instance above, the same
		// "registry's composite/read-only evidence oversold the real
		// grammar" correction the EC2 batch's own
		// aws_vpc_dhcp_options_association made. The provider's own
		// Attribute Reference confirms it: "id - The identifier of the
		// hosting Amazon Connect Instance."
		Type:          "aws_connect_user_hierarchy_structure",
		Components:    []Component{attr("instance_id")},
		ImportSyntax:  "INSTANCEID",
		IdentityAttrs: []string{"id"},
	},

	serverAssigned("aws_workspaces_connection_alias",
		"WorkSpaces assigns the connection alias's own id (rft-…) at create time; connection_string is client-chosen but is not the import identity. Confirmed against the provider's documented import command (terraform import aws_workspaces_connection_alias.example rft-8012925589) and its Attribute Reference (\"id - The identifier of the connection alias\").",
		"ID", "id"),
	serverAssigned("aws_workspaces_ip_group",
		"WorkSpaces assigns the IP group's own id (wsipg-…) at create time; group_name is client-chosen but is not the import identity. Confirmed against the provider's documented import command (terraform import aws_workspaces_ip_group.example wsipg-488lrtl3k) and its Attribute Reference (\"id - The IP group identifier\").",
		"ID", "id"),
	serverAssigned("aws_workspaces_pool",
		"WorkSpaces assigns the pool's own id (wspool-…) at create time; pool_name is client-chosen but is not the import identity. Confirmed against the provider's own Identity Schema (required: pool_id) and its documented import command (terraform import aws_workspaces_pool.example wspool-12345678).",
		"POOLID", "pool_id"),
	serverAssigned("aws_workspaces_workspace",
		"WorkSpaces assigns the workspace's own id (ws-…) at create time; user_name and directory_id configure it but do not identify it. Confirmed against the provider's documented import command (terraform import aws_workspaces_workspace.example ws-9z9zmbkhv) and its Attribute Reference (\"id - The workspaces ID\").",
		"ID", "id"),

	serverAssigned("aws_workspacesweb_browser_settings",
		"WorkSpacesWeb assigns the browser settings' own ARN at create time; customer_managed_key configures encryption but does not identify the resource. Confirmed against the provider's documented import command (terraform import aws_workspacesweb_browser_settings.example arn:aws:workspaces-web:us-west-2:123456789012:browsersettings/abcdef12345) and its Attribute Reference, which exports the ARN as browser_settings_arn — this newer plugin-framework resource has no generic id attribute at all.",
		"BROWSERSETTINGSARN", "browser_settings_arn"),
	serverAssigned("aws_workspacesweb_data_protection_settings",
		"Same shape as aws_workspacesweb_browser_settings above: WorkSpacesWeb assigns the data protection settings' own ARN at create time, exported as data_protection_settings_arn, no generic id attribute.",
		"DATAPROTECTIONSETTINGSARN", "data_protection_settings_arn"),
	serverAssigned("aws_workspacesweb_identity_provider",
		"WorkSpacesWeb assigns the identity provider's own ARN at create time, embedding the required portal_arn argument (a reference to the already-admitted aws_workspacesweb_portal below) as a path segment the provider computes rather than one this table treats as reconstructible. Confirmed against the provider's documented import command and its Attribute Reference, which exports the ARN as identity_provider_arn — no generic id attribute.",
		"IDENTITYPROVIDERARN", "identity_provider_arn"),
	serverAssigned("aws_workspacesweb_ip_access_settings",
		"Same shape as aws_workspacesweb_browser_settings above: WorkSpacesWeb assigns the IP access settings' own ARN at create time, exported as ip_access_settings_arn, no generic id attribute.",
		"IPACCESSSETTINGSARN", "ip_access_settings_arn"),
	serverAssigned("aws_workspacesweb_network_settings",
		"Same shape as aws_workspacesweb_browser_settings above: WorkSpacesWeb assigns the network settings' own ARN at create time, exported as network_settings_arn, no generic id attribute.",
		"NETWORKSETTINGSARN", "network_settings_arn"),
	serverAssigned("aws_workspacesweb_portal",
		"WorkSpacesWeb assigns the portal's own ARN at create time; display_name and instance_type configure it but do not identify it. Confirmed against the provider's documented import command (terraform import aws_workspacesweb_portal.example arn:aws:workspaces-web:us-west-2:123456789012:portal/abcdef12345678) and its Attribute Reference, which exports the ARN as portal_arn — no generic id attribute. Every WorkSpacesWeb *_settings type above whose Attribute Reference lists an AssociatedPortalArns/portal-scoped attribute references this type once it is created, but none composes this type's own identity.",
		"PORTALARN", "portal_arn"),
	serverAssigned("aws_workspacesweb_session_logger",
		"Same shape as aws_workspacesweb_browser_settings above: WorkSpacesWeb assigns the session logger's own ARN at create time, exported as session_logger_arn, no generic id attribute.",
		"SESSIONLOGGERARN", "session_logger_arn"),
	serverAssigned("aws_workspacesweb_trust_store",
		"Same shape as aws_workspacesweb_browser_settings above: WorkSpacesWeb assigns the trust store's own ARN at create time, exported as trust_store_arn, no generic id attribute.",
		"TRUSTSTOREARN", "trust_store_arn"),
	serverAssigned("aws_workspacesweb_user_access_logging_settings",
		"Same shape as aws_workspacesweb_browser_settings above: WorkSpacesWeb assigns the user access logging settings' own ARN at create time, exported as user_access_logging_settings_arn, no generic id attribute.",
		"USERACCESSLOGGINGSETTINGSARN", "user_access_logging_settings_arn"),
	serverAssigned("aws_workspacesweb_user_settings",
		"Same shape as aws_workspacesweb_browser_settings above: WorkSpacesWeb assigns the user settings' own ARN at create time, exported as user_settings_arn, no generic id attribute.",
		"USERSETTINGSARN", "user_settings_arn"),

	// WorkSpacesWeb's eight *_association property-children of
	// AWS::WorkSpacesWeb::Portal: row-gen filed all eight evidence-only
	// (via==fold, no CFN type of their own), but every one has a real,
	// documented import grammar the provider's own docs give directly —
	// the settings type's own ARN and portal_arn, comma-joined, in that
	// order, confirmed against each type's real Import section
	// individually (not inferred from one and assumed for the rest). Both
	// halves are Required configuration arguments of the association type
	// itself, so this is an ordinary concrete composite, not a
	// [FoldParentOf] case — see the batch banner comment above.
	TypeIdentity{
		Type: "aws_workspacesweb_browser_settings_association",
		Components: []Component{
			attr("browser_settings_arn"), sep(","), attr("portal_arn"),
		},
		ImportSyntax:  "BROWSERSETTINGSARN,PORTALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_workspacesweb_data_protection_settings_association",
		Components: []Component{
			attr("data_protection_settings_arn"), sep(","), attr("portal_arn"),
		},
		ImportSyntax:  "DATAPROTECTIONSETTINGSARN,PORTALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_workspacesweb_ip_access_settings_association",
		Components: []Component{
			attr("ip_access_settings_arn"), sep(","), attr("portal_arn"),
		},
		ImportSyntax:  "IPACCESSSETTINGSARN,PORTALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_workspacesweb_network_settings_association",
		Components: []Component{
			attr("network_settings_arn"), sep(","), attr("portal_arn"),
		},
		ImportSyntax:  "NETWORKSETTINGSARN,PORTALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_workspacesweb_session_logger_association",
		Components: []Component{
			attr("session_logger_arn"), sep(","), attr("portal_arn"),
		},
		ImportSyntax:  "SESSIONLOGGERARN,PORTALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_workspacesweb_trust_store_association",
		Components: []Component{
			attr("trust_store_arn"), sep(","), attr("portal_arn"),
		},
		ImportSyntax:  "TRUSTSTOREARN,PORTALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_workspacesweb_user_access_logging_settings_association",
		Components: []Component{
			attr("user_access_logging_settings_arn"), sep(","), attr("portal_arn"),
		},
		ImportSyntax:  "USERACCESSLOGGINGSETTINGSARN,PORTALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_workspacesweb_user_settings_association",
		Components: []Component{
			attr("user_settings_arn"), sep(","), attr("portal_arn"),
		},
		ImportSyntax:  "USERSETTINGSARN,PORTALARN",
		IdentityAttrs: nil,
	},
)

func init() { registerCohortTable(identityTableConnectEuc) }
