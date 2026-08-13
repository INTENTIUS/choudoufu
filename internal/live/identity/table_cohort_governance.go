// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableGovernance is the governance cohort's slice of [DefaultTable]:
// the identity rows the governance ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableGovernance = buildTable(
	// ---- Registry-ratified (#40, #44, #65): governance batch (Config
	// ---- remainder, Control Tower, License Manager, Organizations,
	// ---- Resource Explorer, Resource Groups, Service Catalog remainder
	// ---- plus AppRegistry, Audit Manager). Same pipeline as the batches
	// ---- above: every row started as a tools/row-gen proposal from
	// ---- live/registry.json, cross-checked against the AWS provider's
	// ---- documented Argument Reference, Attribute Reference and Import
	// ---- section (fetched from the provider's own website/docs/r/ source),
	// ---- not accepted on the registry's classification alone. See
	// ---- internal/live/lint/admission.go for the batch-level rejection and
	// ---- deferral summary (Config's registry-laggard recorder/delivery-
	// ---- channel pair, the OrganizationConfigRule aliases, the
	// ---- accept_language literal-fallback gap that keeps two Service
	// ---- Catalog association types unratified) and
	// ---- live/e2e/estates/governance/README.md for the full account,
	// ---- including the Organizations blast-radius note.

	// Config remainder.
	TypeIdentity{
		// registry.json: primaryIdentifier=[ConfigRuleName], client-named,
		// proposed correctly. Confirmed against the provider's documented
		// import command (terraform import aws_config_config_rule.example
		// example) and its identity schema, whose sole required attribute is
		// name.
		Type:          "aws_config_config_rule",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ConfigurationAggregatorName],
		// client-named, proposed correctly. Confirmed against the
		// provider's documented import command (terraform import
		// aws_config_configuration_aggregator.example example).
		Type:          "aws_config_configuration_aggregator",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ConformancePackName],
		// client-named, proposed correctly. Confirmed against the
		// provider's documented import command (terraform import
		// aws_config_conformance_pack.example example).
		Type:          "aws_config_conformance_pack",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[OrganizationConformancePackName],
		// client-named, proposed correctly. Confirmed against the
		// provider's documented import command (terraform import
		// aws_config_organization_conformance_pack.example example).
		Type:          "aws_config_organization_conformance_pack",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ConfigRuleName], client-named,
		// proposed correctly (argument config_rule_name, not row-gen's
		// default "name" guess - the provider's identity schema names it
		// explicitly). Confirmed against the provider's documented import
		// command (terraform import
		// aws_config_remediation_configuration.example example).
		Type:          "aws_config_remediation_configuration",
		Components:    []Component{attr("config_rule_name")},
		ImportSyntax:  "CONFIG_RULE_NAME",
		IdentityAttrs: []string{"config_rule_name"},
	},
	// Not ratified this batch, per its own named scope of "clean proposals
	// only": aws_config_aggregate_authorization (needs-hand-separator;
	// confirmed against the provider's docs to be a colon-joined
	// account_id:authorized_region composite, but out of scope),
	// aws_config_configuration_recorder and aws_config_delivery_channel
	// (both registry-laggard - row-gen calls them evidence-only because
	// registry.json's primaryIdentifier is the opaque Id CloudFormation
	// never actually returns for either type, but the provider's own docs
	// document a clean name-based import for both; still out of scope), and
	// the three OrganizationConfigRule aliases
	// (aws_config_organization_custom_policy_rule,
	// aws_config_organization_custom_rule,
	// aws_config_organization_managed_rule - same registry-laggard shape,
	// same out-of-scope call). See the cohort README.

	// Control Tower.
	serverAssigned("aws_controltower_baseline",
		"the ControlTower service assigns the enabled baseline its own ARN at create time; target_identifier and baseline_identifier are required arguments but name the OU and the baseline being enabled, not this enablement record itself.",
		"ID", "id"),
	// aws_controltower_control: row-gen classified this needs-hand-separator
	// (registry primaryIdentifier ["TargetIdentifier", "ControlIdentifier"],
	// composite, no separator in any schema). The provider's own documented
	// import command supplies it directly: target_identifier and
	// control_identifier, comma-joined (terraform import
	// aws_controltower_control.example
	// arn:aws:organizations::123456789101:ou/o-qqaejywet/ou-qg5o-ufbhdtv3,arn:aws:controltower:us-east-1::control/WTDSMKDKDNLE)
	// - both required, already-configured arguments, matching the registry's
	// own primaryIdentifier field-for-field once the separator is supplied.
	TypeIdentity{
		Type: "aws_controltower_control",
		Components: []Component{
			attr("target_identifier"),
			sep(","),
			attr("control_identifier"),
		},
		ImportSyntax:  "TARGETIDENTIFIER,CONTROLIDENTIFIER",
		IdentityAttrs: nil,
	},
	serverAssigned("aws_controltower_landing_zone",
		"an AWS account has at most one landing zone, and the ControlTower service assigns it its own identifier when it is created; nothing in configuration names it.",
		"ID", "id"),

	// License Manager. Not ratified: aws_licensemanager_grant is row-gen's
	// only proposal in this service, and its identity is genuinely clean
	// (server-assigned ARN, confirmed against the provider's Import
	// section) - but live/survey-full.json's real-schema signal says it is
	// untaggable with no native list resource in the pinned v6.59.0
	// provider, which means none of this package's four admission paths
	// (internal/live/doc.go) actually recovers an existing grant: no
	// marker (untaggable), no list-and-content-match (no list resource),
	// and no client-named or parent-derived path either, since the ARN is
	// wholly server-minted. A clean import grammar is not the same claim
	// as a working admission path, and row-gen's own proposal only speaks
	// to the former. See the cohort README.

	// Organizations. Ratified on clean identity evidence for four of the
	// five types row-gen's own service scope named: accounts, OUs,
	// policies and the resource policy singleton are all server-assigned
	// and taggable (live/survey-full.json), so the marker path recovers
	// them the same way it recovers aws_kms_key. The organization singleton
	// itself is not ratified - see below. Ratifying identity is not the
	// same as exercising these types against live infrastructure: this
	// batch's cohort estate generates and validates HCL for all four but
	// does not run terraform apply for them against the pinned floci image
	// (an AWS emulator, but an account/organization-scoped one whose
	// coverage of Organizations' control-plane operations this batch did
	// not confirm) - see live/e2e/estates/governance/README.md's "A
	// deliberate floci gap" section. No delegated-admin type
	// (AWS::Organizations::* has none; the DelegatedAdmin shape lives on
	// individual services like SecurityHub, out of this batch's scope)
	// appeared in this cycle's row-gen pool.
	serverAssigned("aws_organizations_account",
		"the Organizations service assigns the member account its own account ID at create time; email and name are required arguments but do not identify an existing account the way the ID does.",
		"ID", "id"),
	// Not ratified: aws_organizations_organization is row-gen's proposal
	// for the org singleton (server-assigned ID, confirmed against the
	// provider's docs), but live/survey-full.json says it is untaggable
	// with no native list resource - the same unrecoverable shape as the
	// LicenseManager grant above, and the reason this batch's own scope
	// (see the cohort README) named only "accounts, OUs, policies" and not
	// the organization singleton itself.
	serverAssigned("aws_organizations_organizational_unit",
		"the Organizations service assigns the OU its own ID at create time; parent_id and name are required arguments but do not reconstruct this OU's own ID.",
		"ID", "id"),
	serverAssigned("aws_organizations_policy",
		"the Organizations service assigns the policy its own ID at create time; type is a required argument but does not identify a specific policy.",
		"ID", "id"),
	serverAssigned("aws_organizations_resource_policy",
		"an AWS Organization has at most one resource-based delegation policy, and the service assigns its ID when the policy is attached; nothing in configuration names it.",
		"ID", "id"),
	// aws_organizations_policy_attachment (row-gen: evidence-only,
	// property-child of AWS::Organizations::Policy) is not ratified this
	// batch; a parent-derived admission keyed on the policy marker above is
	// follow-on work, not named in this batch's scope.

	// Resource Explorer.
	serverAssigned("aws_resourceexplorer2_index",
		"an AWS account has at most one Resource Explorer index per region, and the service assigns the index its own ARN at create time; type (LOCAL or AGGREGATOR) is a required argument but does not identify this index.",
		"ARN", "arn"),
	serverAssigned("aws_resourceexplorer2_view",
		"the Resource Explorer service assigns the view its own ARN at create time, embedding a server-minted suffix beyond the client-chosen view_name; the provider's identity schema names arn, not id, as the required import attribute.",
		"ARN", "arn"),

	// Resource Groups.
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], client-named, proposed
		// correctly (row-gen's argument line came from
		// live/import-grammar.json). Confirmed against the provider's
		// documented import command (terraform import
		// aws_resourcegroups_group.foo resource-group-name).
		Type:          "aws_resourcegroups_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_resourcegroups_resource (row-gen: evidence-only, property-child of
	// AWS::ResourceGroups::Group) is not ratified this batch; same
	// parent-derived follow-on as the Organizations policy attachment above.

	// Service Catalog remainder. aws_servicecatalog_constraint stays
	// reasoned-none per this batch's own instruction and is not touched
	// here at all - it does not even appear in row-gen's pool.
	serverAssigned("aws_servicecatalog_portfolio",
		"the Service Catalog service assigns the portfolio its own ID at create time; portfolio names are not unique, so provider_name does not identify a specific portfolio.",
		"ID", "id"),
	// aws_servicecatalog_portfolio_share: row-gen classified this
	// needs-hand-separator (registry primaryIdentifier ["PortfolioId",
	// "AccountId"], composite, no separator in any schema - and
	// undercounting the real grammar besides). The provider's own
	// documented import command is a three-part, colon-joined composite
	// (terraform import aws_servicecatalog_portfolio_share.example
	// port-12344321:ACCOUNT:123456789012): portfolio_id, type and
	// principal_id, all three required, already-configured arguments (type
	// is one of ACCOUNT, ORGANIZATION, ORGANIZATIONAL_UNIT or
	// ORGANIZATION_MEMBER_ACCOUNT; the registry only named the AccountId
	// half of the ACCOUNT case).
	TypeIdentity{
		Type: "aws_servicecatalog_portfolio_share",
		Components: []Component{
			attr("portfolio_id"),
			sep(":"),
			attr("type"),
			sep(":"),
			attr("principal_id"),
		},
		ImportSyntax:  "PORTFOLIOID:TYPE:PRINCIPALID",
		IdentityAttrs: nil,
	},
	serverAssigned("aws_servicecatalog_product",
		"the Service Catalog service assigns the product its own ID at create time; product names are not unique, so name and owner do not identify a specific product.",
		"ID", "id"),
	serverAssigned("aws_servicecatalog_provisioned_product",
		"the Service Catalog service assigns the provisioned product its own ID at create time; the product/provisioning-artifact references are required arguments but do not identify this particular provisioned instance.",
		"ID", "id"),
	// Not ratified: aws_servicecatalog_service_action and
	// aws_servicecatalog_tag_option are both row-gen proposals with clean,
	// confirmed server-assigned import grammars, but
	// live/survey-full.json says both are untaggable with no native list
	// resource - the same unrecoverable shape as the LicenseManager grant
	// and Organizations singleton above. aws_servicecatalog_tag_option_
	// resource_association's own composite identity (tag_option_id and
	// resource_id, colon-joined per the provider's documented import
	// command, correcting row-gen's needs-hand-separator classification
	// the same way the portfolio share above does) is mechanically fine on
	// its own terms - both halves are plain, already-configured arguments,
	// needing no live read - but with the tag option type itself carrying
	// no admission path, a config referencing an admitted
	// aws_servicecatalog_tag_option resource could never exist; deferred
	// alongside tag_option rather than admitted on that technicality. See
	// the cohort README.
	//
	// Not ratified this batch either, on independent verification rather
	// than row-gen's own classification: aws_servicecatalog_principal_
	// portfolio_association and aws_servicecatalog_product_portfolio_
	// association both document an import ID that requires accept_language
	// (an optional, defaulted argument) as one of its parts - a literal
	// fallback for an omitted argument, the same table-mechanism gap the
	// messaging batch's aws_cloudwatch_event_rule left unratified. See the
	// cohort README.

	// Service Catalog AppRegistry.
	serverAssigned("aws_servicecatalogappregistry_application",
		"the ServiceCatalogAppRegistry service assigns the application its own ID at create time; name is a required argument but does not reconstruct it.",
		"ID", "id"),
	serverAssigned("aws_servicecatalogappregistry_attribute_group",
		"the ServiceCatalogAppRegistry service assigns the attribute group its own ID at create time; name is a required argument but does not reconstruct it.",
		"ID", "id"),
	// aws_servicecatalogappregistry_attribute_group_association: row-gen
	// classified this evidence-only (registry primaryIdentifier
	// ["ApplicationArn", "AttributeGroupArn"], both read-only - but the
	// provider's own required arguments are application_id and
	// attribute_group_id, not the ARNs the registry named). The provider's
	// documented import command is a comma-joined pair of those IDs
	// (terraform import
	// aws_servicecatalogappregistry_attribute_group_association.example
	// 12456778723424sdffsdfsdq34,12234t3564dsfsdf34asff4ww3) -
	// application_id through the application marker above,
	// attribute_group_id through the attribute group marker above.
	TypeIdentity{
		Type: "aws_servicecatalogappregistry_attribute_group_association",
		Components: []Component{
			attr("application_id"),
			sep(","),
			attr("attribute_group_id"),
		},
		ImportSyntax:  "APPLICATIONID,ATTRIBUTEGROUPID",
		IdentityAttrs: nil,
	},

	// Audit Manager.
	serverAssigned("aws_auditmanager_assessment",
		"the AuditManager service assigns the assessment its own ID at create time; framework_id is a required argument but does not identify this particular assessment.",
		"ID", "id"),
	serverAssigned("aws_auditmanager_framework",
		"the AuditManager service assigns the framework its own ID at create time; name is a required argument but does not reconstruct it.",
		"ID", "id"),
)

func init() { registerCohortTable(identityTableGovernance) }
