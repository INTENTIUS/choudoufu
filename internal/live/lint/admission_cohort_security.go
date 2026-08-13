// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesSecurity is the security cohort's slice of [admittedTypesV0]:
// the types the security ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesSecurity = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): sixth batch, security and
	// ---- secrets (Secrets Manager, KMS remainder, SSM remainder, ACM-PCA,
	// ---- GuardDuty, Macie2, SecurityHub, Inspector2, WAFv2 — issue #65's
	// ---- ratification campaign). Same tools/row-gen pipeline and
	// ---- verification standard as the batches above, cross-checked against
	// ---- the AWS provider's documented import behaviour, live/survey-full.json's
	// ---- taggability signal (the real provider schema, not merely the
	// ---- CloudFormation Registry's own tagging claim — SecurityHub's legacy
	// ---- v1 types are where those two disagree, the "newer-API-generation
	// ---- false friend" the ram-servicecatalog sweep flagged) and a live
	// ---- floci probe. See internal/live/identity/table.go for the per-type
	// ---- evidence, the rejected proposals, and the credential-adjacent
	// ---- exclusions this batch calls out explicitly (extending
	// ---- opsExcluded's reasoning to aws_kms_grant and
	// ---- aws_kms_custom_key_store without touching that hand table, since
	// ---- both already resolve to "moves to Ops" on ordinary
	// ---- recoverability grounds). Cohort estate: live/e2e/estates/security.
	"aws_secretsmanager_secret":                        {},
	"aws_secretsmanager_secret_policy":                 {},
	"aws_secretsmanager_secret_rotation":               {},
	"aws_kms_external_key":                             {},
	"aws_kms_replica_key":                              {},
	"aws_ssm_association":                              {},
	"aws_ssm_maintenance_window":                       {},
	"aws_ssm_patch_baseline":                           {},
	"aws_ssm_patch_group":                              {},
	"aws_ssm_resource_data_sync":                       {},
	"aws_ssm_service_setting":                          {},
	"aws_acmpca_certificate_authority":                 {},
	"aws_acmpca_certificate_authority_certificate":     {},
	"aws_acmpca_policy":                                {},
	"aws_guardduty_detector":                           {},
	"aws_guardduty_filter":                             {},
	"aws_guardduty_ipset":                              {},
	"aws_guardduty_threatintelset":                     {},
	"aws_guardduty_malware_protection_plan":            {},
	"aws_guardduty_member":                             {},
	"aws_guardduty_publishing_destination":             {},
	"aws_guardduty_organization_admin_account":         {},
	"aws_guardduty_organization_configuration":         {},
	"aws_macie2_custom_data_identifier":                {},
	"aws_macie2_findings_filter":                       {},
	"aws_macie2_classification_job":                    {},
	"aws_macie2_member":                                {},
	"aws_macie2_organization_admin_account":            {},
	"aws_securityhub_account_v2":                       {},
	"aws_securityhub_aggregator_v2":                    {},
	"aws_securityhub_automation_rule":                  {},
	"aws_securityhub_automation_rule_v2":               {},
	"aws_securityhub_configuration_policy_association": {},
	"aws_securityhub_connector_v2":                     {},
	"aws_securityhub_organization_admin_account":       {},
	"aws_securityhub_standards_control":                {},
	"aws_securityhub_standards_control_association":    {},
	"aws_securityhub_member":                           {},
	"aws_inspector2_filter":                            {},
	"aws_inspector2_delegated_admin_account":           {},
	"aws_inspector2_member_association":                {},
	"aws_wafv2_ip_set":                                 {},
	"aws_wafv2_regex_pattern_set":                      {},
	"aws_wafv2_rule_group":                             {},
	"aws_wafv2_web_acl":                                {},
	"aws_wafv2_web_acl_rule":                           {},
}

func init() { registerCohortAdmitted(admittedTypesSecurity) }
