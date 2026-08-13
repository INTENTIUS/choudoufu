// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesIot is the iot cohort's slice of [admittedTypesV0]:
// the types the iot ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesIot = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): sixth batch, IoT core
	// ---- (things, thing types/groups, policies, topic rules;
	// ---- issue #65's recipe). Same tools/row-gen pipeline as the earlier
	// ---- batches, cross-checked against the AWS provider's documented
	// ---- Argument/Attribute/Import sections fetched from the pinned
	// ---- v6.59.0 tag directly, not accepted on row-gen's own
	// ---- classification: six of these eleven rows are evidence-only
	// ---- GUESSED-argument proposals row-gen itself declined to paste,
	// ---- promoted here only after the provider's own docs confirmed (or,
	// ---- for aws_iot_role_alias, corrected) the guessed argument name.
	// ---- Four rows are rejected outright: aws_iot_certificate and
	// ---- aws_iot_ca_certificate, aws_iot_policy_attachment and
	// ---- aws_iot_thing_principal_attachment carry no "## Import" section
	// ---- anywhere in the pinned provider's docs at all - confirmed by
	// ---- fetching the raw doc source, not merely its rendered page - so
	// ---- no admission path is provider-documented for them.
	// ---- aws_iot_certificate carries a second, independent
	// ---- disqualification: evaluated explicitly against the
	// ---- credential-material bar aws_iam_access_key is excluded by
	// ---- (live/SURVEY.md's "three the rule excludes"), because when
	// ---- created with neither `csr` nor `certificate_pem` the provider's
	// ---- own Attribute Reference has it mint and export `private_key` -
	// ---- a secret a live read would transit and that AWS never returns
	// ---- again after create. Excluded by that rule, independent of the
	// ---- missing Import section. IoT Events, IoT Analytics, Greengrass
	// ---- (v1 and v2), IoT SiteWise and IoT TwinMaker are all named in
	// ---- issue #65's recipe as this batch's scope but are not admitted
	// ---- here: the pinned provider ships no resources for any of the
	// ---- five services at all (confirmed against the provider's own
	// ---- website/docs/r/ directory listing at the pinned tag), so
	// ---- live/mapping.json carries no rows and tools/row-gen emits no
	// ---- proposals for them - there is nothing this batch could ratify
	// ---- or reject. See internal/live/identity/table.go for the
	// ---- per-type evidence. Cohort estate: live/e2e/estates/iot.
	"aws_iot_authorizer":             {},
	"aws_iot_billing_group":          {},
	"aws_iot_domain_configuration":   {},
	"aws_iot_policy":                 {},
	"aws_iot_provisioning_template":  {},
	"aws_iot_role_alias":             {},
	"aws_iot_thing":                  {},
	"aws_iot_thing_group":            {},
	"aws_iot_thing_type":             {},
	"aws_iot_topic_rule":             {},
	"aws_iot_topic_rule_destination": {},
}

func init() { registerCohortAdmitted(admittedTypesIot) }
