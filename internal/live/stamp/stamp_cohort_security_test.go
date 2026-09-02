// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The security cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableSecurity = []string{
	// Registry-ratified security and secrets batch (#40, #44, issue
	// #65). See live/e2e/estates/security/README.md, "Untaggable
	// types", for this batch's untaggable rows, below.
	"aws_secretsmanager_secret",
	"aws_kms_external_key",
	"aws_kms_replica_key",
	"aws_ssm_association",
	"aws_ssm_maintenance_window",
	"aws_ssm_patch_baseline",
	"aws_acmpca_certificate_authority",
	"aws_guardduty_detector",
	"aws_guardduty_filter",
	"aws_guardduty_ipset",
	"aws_guardduty_threatintelset",
	"aws_guardduty_malware_protection_plan",
	"aws_guardduty_publishing_destination",
	"aws_macie2_custom_data_identifier",
	"aws_macie2_findings_filter",
	"aws_macie2_classification_job",
	"aws_macie2_member",
	"aws_securityhub_account_v2",
	"aws_securityhub_aggregator_v2",
	"aws_securityhub_automation_rule",
	"aws_securityhub_automation_rule_v2",
	"aws_securityhub_connector_v2",
	"aws_inspector2_filter",
	"aws_wafv2_ip_set",
	"aws_wafv2_regex_pattern_set",
	"aws_wafv2_rule_group",
	"aws_wafv2_web_acl",
}

var untaggableSecurity = []string{
	// Registry-ratified security and secrets batch (#40, #44, issue
	// #65): parent-derived children and account-id-keyed singletons
	// with no tags argument at all. See
	// live/e2e/estates/security/README.md, "Untaggable types".
	"aws_secretsmanager_secret_policy",
	"aws_secretsmanager_secret_rotation",
	"aws_ssm_patch_group",
	"aws_ssm_resource_data_sync",
	"aws_ssm_service_setting",
	"aws_acmpca_certificate_authority_certificate",
	"aws_acmpca_policy",
	"aws_guardduty_member",
	"aws_guardduty_organization_admin_account",
	"aws_guardduty_organization_configuration",
	"aws_macie2_organization_admin_account",
	"aws_securityhub_configuration_policy_association",
	"aws_securityhub_organization_admin_account",
	"aws_securityhub_standards_control",
	"aws_securityhub_standards_control_association",
	"aws_securityhub_member",
	"aws_inspector2_delegated_admin_account",
	"aws_inspector2_member_association",
	"aws_wafv2_web_acl_rule",
	// #175 ratification batch (PROPOSE, issue #65), 2026-08-15:
	// taggability per the provider schema survey (live/survey-full.json,
	// v6.59.0 signals.taggable).
	"aws_wafv2_web_acl_logging_configuration",
}

func init() {
	registerCohortStamp(taggableSecurity, untaggableSecurity, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified security and secrets batch (#40, #44, issue #65).
			"aws_secretsmanager_secret":                        taggedSchema("id", "arn", "name"),
			"aws_secretsmanager_secret_policy":                 untaggedSchema("id", "secret_arn", "policy"),
			"aws_secretsmanager_secret_rotation":               untaggedSchema("id", "secret_id"),
			"aws_kms_external_key":                             taggedSchema("id", "key_id", "arn"),
			"aws_kms_replica_key":                              taggedSchema("id", "key_id", "arn", "primary_key_arn"),
			"aws_ssm_association":                              taggedSchema("id", "association_id", "name"),
			"aws_ssm_maintenance_window":                       taggedSchema("id", "name", "schedule"),
			"aws_ssm_patch_baseline":                           taggedSchema("id", "name"),
			"aws_ssm_patch_group":                              untaggedSchema("id", "baseline_id", "patch_group"),
			"aws_ssm_resource_data_sync":                       untaggedSchema("id", "name"),
			"aws_ssm_service_setting":                          untaggedSchema("id", "setting_id", "setting_value"),
			"aws_acmpca_certificate_authority":                 taggedSchema("id", "arn"),
			"aws_acmpca_certificate_authority_certificate":     untaggedSchema("id", "certificate_authority_arn", "certificate"),
			"aws_acmpca_policy":                                untaggedSchema("id", "resource_arn", "policy"),
			"aws_guardduty_detector":                           taggedSchema("id"),
			"aws_guardduty_filter":                             taggedSchema("id", "detector_id", "name"),
			"aws_guardduty_ipset":                              taggedSchema("id", "detector_id", "name"),
			"aws_guardduty_threatintelset":                     taggedSchema("id", "detector_id", "name"),
			"aws_guardduty_malware_protection_plan":            taggedSchema("id", "arn", "role"),
			"aws_guardduty_member":                             untaggedSchema("id", "detector_id", "account_id"),
			"aws_guardduty_publishing_destination":             taggedSchema("id", "detector_id"),
			"aws_guardduty_organization_admin_account":         untaggedSchema("id", "admin_account_id"),
			"aws_guardduty_organization_configuration":         untaggedSchema("id", "detector_id"),
			"aws_macie2_custom_data_identifier":                taggedSchema("id", "name"),
			"aws_macie2_findings_filter":                       taggedSchema("id", "name"),
			"aws_macie2_classification_job":                    taggedSchema("id", "job_id", "name"),
			"aws_macie2_member":                                taggedSchema("id", "account_id"),
			"aws_macie2_organization_admin_account":            untaggedSchema("id", "admin_account_id"),
			"aws_securityhub_account_v2":                       taggedSchema("id", "arn"),
			"aws_securityhub_aggregator_v2":                    taggedSchema("id", "arn"),
			"aws_securityhub_automation_rule":                  taggedSchema("id", "arn", "rule_name"),
			"aws_securityhub_automation_rule_v2":               taggedSchema("id", "arn", "rule_name"),
			"aws_securityhub_configuration_policy_association": untaggedSchema("id", "target_id", "policy_id"),
			"aws_securityhub_connector_v2":                     taggedSchema("id", "arn", "connector_id", "name"),
			"aws_securityhub_organization_admin_account":       untaggedSchema("id", "admin_account_id"),
			"aws_securityhub_standards_control":                untaggedSchema("id", "standards_control_arn"),
			"aws_securityhub_standards_control_association":    untaggedSchema("id", "security_control_id", "standards_arn"),
			"aws_securityhub_member":                           untaggedSchema("id", "account_id"),
			"aws_inspector2_filter":                            taggedSchema("id", "arn", "name"),
			"aws_inspector2_delegated_admin_account":           untaggedSchema("id", "account_id"),
			"aws_inspector2_member_association":                untaggedSchema("id", "account_id"),
			"aws_wafv2_ip_set":                                 taggedSchema("id", "arn", "name", "scope"),
			"aws_wafv2_regex_pattern_set":                      taggedSchema("id", "arn", "name", "scope"),
			"aws_wafv2_rule_group":                             taggedSchema("id", "arn", "name", "scope"),
			"aws_wafv2_web_acl":                                taggedSchema("id", "arn", "name", "scope"),
			"aws_wafv2_web_acl_rule":                           untaggedSchema("id", "web_acl_arn", "name"),
			// #175 ratification batch (PROPOSE, issue #65), 2026-08-15.
			"aws_paymentcryptography_key_alias":       untaggedSchema("id", "alias_name", "key_arn"),
			"aws_ssm_maintenance_window_task":         untaggedSchema("id", "window_id", "window_task_id"),
			"aws_verifiedpermissions_policy":          untaggedSchema("id", "policy_id", "policy_store_id"),
			"aws_verifiedpermissions_policy_template": untaggedSchema("id", "policy_store_id", "policy_template_id"),
			"aws_wafv2_web_acl_logging_configuration": untaggedSchema("id", "resource_arn"),
		})
	})
}
