// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The governance cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableGovernance = []string{
	// Registry-ratified governance batch (#40, #44, issue #65). See
	// live/e2e/estates/governance/README.md.
	"aws_config_config_rule",
	"aws_config_configuration_aggregator",
	"aws_controltower_baseline",
	"aws_controltower_landing_zone",
	"aws_organizations_account",
	"aws_organizations_organizational_unit",
	"aws_organizations_policy",
	"aws_organizations_resource_policy",
	"aws_resourceexplorer2_index",
	"aws_resourceexplorer2_view",
	"aws_resourcegroups_group",
	"aws_servicecatalog_portfolio",
	"aws_servicecatalog_product",
	"aws_servicecatalog_provisioned_product",
	"aws_servicecatalogappregistry_application",
	"aws_servicecatalogappregistry_attribute_group",
	"aws_auditmanager_assessment",
	"aws_auditmanager_framework",
}

var untaggableGovernance = []string{
	// Registry-ratified governance batch (#40, #44, issue #65): six
	// types with no tags argument at all, per
	// live/survey-full.json's real-schema signal. See
	// live/e2e/estates/governance/README.md.
	"aws_config_conformance_pack",
	"aws_config_organization_conformance_pack",
	"aws_config_remediation_configuration",
	"aws_controltower_control",
	"aws_servicecatalog_portfolio_share",
	"aws_servicecatalogappregistry_attribute_group_association",
}

func init() {
	registerCohortStamp(taggableGovernance, untaggableGovernance, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified governance batch (#40, #44, issue #65).
			// Taggable/untaggable per live/survey-full.json's real-schema signal
			// for each type: aws_config_conformance_pack,
			// aws_config_organization_conformance_pack,
			// aws_config_remediation_configuration, aws_controltower_control,
			// aws_servicecatalog_portfolio_share and
			// aws_servicecatalogappregistry_attribute_group_association carry no
			// tags argument at all. See live/e2e/estates/governance/README.md.
			"aws_config_config_rule":                                    taggedSchema("id", "arn", "name"),
			"aws_config_configuration_aggregator":                       taggedSchema("id", "arn", "name"),
			"aws_config_conformance_pack":                               untaggedSchema("id", "arn", "name"),
			"aws_config_organization_conformance_pack":                  untaggedSchema("id", "arn", "name"),
			"aws_config_remediation_configuration":                      untaggedSchema("id", "config_rule_name"),
			"aws_controltower_baseline":                                 taggedSchema("id", "arn", "target_identifier"),
			"aws_controltower_control":                                  untaggedSchema("id", "target_identifier", "control_identifier"),
			"aws_controltower_landing_zone":                             taggedSchema("id", "arn", "manifest_json"),
			"aws_organizations_account":                                 taggedSchema("id", "arn", "name", "email"),
			"aws_organizations_organizational_unit":                     taggedSchema("id", "arn", "name", "parent_id"),
			"aws_organizations_policy":                                  taggedSchema("id", "arn", "name", "type"),
			"aws_organizations_resource_policy":                         taggedSchema("id", "arn", "content"),
			"aws_resourceexplorer2_index":                               taggedSchema("id", "arn", "type"),
			"aws_resourceexplorer2_view":                                taggedSchema("id", "arn", "name"),
			"aws_resourcegroups_group":                                  taggedSchema("id", "arn", "name"),
			"aws_servicecatalog_portfolio":                              taggedSchema("id", "arn", "name"),
			"aws_servicecatalog_portfolio_share":                        untaggedSchema("id", "portfolio_id", "type", "principal_id"),
			"aws_servicecatalog_product":                                taggedSchema("id", "arn", "name"),
			"aws_servicecatalog_provisioned_product":                    taggedSchema("id", "arn", "name"),
			"aws_servicecatalogappregistry_application":                 taggedSchema("id", "arn", "name"),
			"aws_servicecatalogappregistry_attribute_group":             taggedSchema("id", "arn", "name"),
			"aws_servicecatalogappregistry_attribute_group_association": untaggedSchema("id", "application_id", "attribute_group_id"),
			"aws_auditmanager_assessment":                               taggedSchema("id", "arn", "name", "framework_id"),
			"aws_auditmanager_framework":                                taggedSchema("id", "arn", "name"),
		})
	})
}
