// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesGovernance is the governance cohort's slice of [admittedTypesV0]:
// the types the governance ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesGovernance = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): governance batch (Config
	// ---- remainder, Control Tower, License Manager, Organizations,
	// ---- Resource Explorer, Resource Groups, Service Catalog remainder
	// ---- plus AppRegistry, Audit Manager; issue #65's own next-batch
	// ---- list). Same tools/row-gen pipeline as the batches above,
	// ---- cross-checked against the AWS provider's documented
	// ---- Argument/Attribute/Import sections and against
	// ---- live/survey-full.json's real-schema taggable/list_resource
	// ---- signals, not accepted on the registry's classification alone -
	// ---- the second check caught five of row-gen's clean-looking
	// ---- proposals (aws_licensemanager_grant, the whole License Manager
	// ---- service; aws_organizations_organization; aws_servicecatalog_
	// ---- service_action and aws_servicecatalog_tag_option) whose
	// ---- server-assigned identity is real but whose live/survey-full.json
	// ---- signals are untaggable with no native list resource, which
	// ---- leaves none of this package's four admission paths
	// ---- (internal/live/doc.go) able to recover an existing instance - a
	// ---- clean import grammar is not the same claim as a working
	// ---- admission path. Three of row-gen's needs-hand-separator
	// ---- proposals in this batch's scope are corrected rather than
	// ---- deferred, the same way the GuardDuty filter and WAFv2 web ACL
	// ---- rule corrections read in earlier batches: aws_controltower_
	// ---- control and aws_servicecatalog_portfolio_share both have an
	// ---- unambiguous, documented separator and component arguments the
	// ---- registry's own composite primaryIdentifier either names outright
	// ---- or undercounts by one field; aws_servicecatalog_tag_option_
	// ---- resource_association's own composite is equally clean but stays
	// ---- unratified anyway because its own tag_option_id half names a
	// ---- type this batch just rejected. Two more siblings
	// ---- (aws_servicecatalog_principal_portfolio_association and
	// ---- aws_servicecatalog_product_portfolio_association) are unratified
	// ---- for a different reason: both documented import IDs require
	// ---- accept_language, an optional, defaulted argument this table's
	// ---- Component vocabulary has no way to supply when it is omitted
	// ---- from configuration - the same literal-fallback gap the messaging
	// ---- batch's aws_cloudwatch_event_rule left unratified.
	// ---- aws_config_configuration_recorder and aws_config_delivery_channel
	// ---- are registry-laggard (row-gen's own evidence-only classification
	// ---- undersells a real, clean name-based import this batch confirmed
	// ---- against the provider's docs) but out of this batch's mandate,
	// ---- which named only Config's clean proposals; left for a future
	// ---- batch alongside aws_config_aggregate_authorization and the three
	// ---- OrganizationConfigRule aliases (also confirmed importable, also
	// ---- out of this batch's named scope). aws_servicecatalog_constraint
	// ---- stays reasoned-none, untouched. No AWS::WellArchitected::* rows
	// ---- appeared in this cycle's row-gen pool at all. The four
	// ---- Organizations types that are ratified (accounts, OUs, policies
	// ---- and the resource policy singleton) go in on clean identity
	// ---- evidence alone - but this batch's cohort estate does not
	// ---- exercise any Organizations create against the pinned floci
	// ---- image; see live/e2e/estates/governance/README.md for why.
	// ---- Cohort estate: live/e2e/estates/governance.
	"aws_config_config_rule":                                    {},
	"aws_config_configuration_aggregator":                       {},
	"aws_config_conformance_pack":                               {},
	"aws_config_organization_conformance_pack":                  {},
	"aws_config_remediation_configuration":                      {},
	"aws_controltower_baseline":                                 {},
	"aws_controltower_control":                                  {},
	"aws_controltower_landing_zone":                             {},
	"aws_organizations_account":                                 {},
	"aws_organizations_organizational_unit":                     {},
	"aws_organizations_policy":                                  {},
	"aws_organizations_resource_policy":                         {},
	"aws_resourceexplorer2_index":                               {},
	"aws_resourceexplorer2_view":                                {},
	"aws_resourcegroups_group":                                  {},
	"aws_servicecatalog_portfolio":                              {},
	"aws_servicecatalog_portfolio_share":                        {},
	"aws_servicecatalog_product":                                {},
	"aws_servicecatalog_provisioned_product":                    {},
	"aws_servicecatalogappregistry_application":                 {},
	"aws_servicecatalogappregistry_attribute_group":             {},
	"aws_servicecatalogappregistry_attribute_group_association": {},
	"aws_auditmanager_assessment":                               {},
	"aws_auditmanager_framework":                                {},
}

func init() { registerCohortAdmitted(admittedTypesGovernance) }
