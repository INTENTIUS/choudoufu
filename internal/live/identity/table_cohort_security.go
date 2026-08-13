// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableSecurity is the security cohort's slice of [DefaultTable]:
// the identity rows the security ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableSecurity = buildTable(
	// ---- Registry-ratified (#40, #44, #65): sixth batch, security and
	// ---- secrets. Same tools/row-gen pipeline as the batches above,
	// ---- cross-checked against the AWS provider's documented import
	// ---- behaviour, live/survey-full.json's per-type signals (built from
	// ---- the real provider schema, not the CloudFormation Registry's own
	// ---- tagging claim, which disagrees with it for several SecurityHub v1
	// ---- types below), and a live floci probe of every service in scope.
	// ---- Cohort estate: live/e2e/estates/security. See that cohort's own
	// ---- README for the full rejected/deferred list and the
	// ---- credential-adjacent exclusions this batch calls out explicitly.
	//
	// aws_secretsmanager_secret: row-gen proposed this server-assigned via
	// the registry's opaque "Id", which undersells it: the type is
	// `tools/survey-gen/survey_gen_test.go`'s own `pathExceptions` entry
	// (cohort "account-derived, not yet wired") — its required import
	// attribute is the secret's ARN, and that ARN carries a six-character
	// suffix Secrets Manager mints per secret (confirmed live: a floci
	// `CreateSecret` for name "my-test-secret" came back
	// "...secret:my-test-secret-YOZ450") that no account/region template
	// reconstructs. `live/SURVEY.md`'s own row records exactly this and
	// defers to the marker path the type is already taggable for; this
	// batch honors that deferral rather than re-deriving it. Verified live:
	// `secretsmanager:TagResource` and `ListSecrets` round-trip the marker
	// tag cleanly against floci.
	serverAssigned("aws_secretsmanager_secret",
		"Secrets Manager assigns the secret's ARN at create time, with a six-character suffix minted per secret that no account/region template reconstructs (live/SURVEY.md's own recorded wrinkle); the name argument is client-chosen but is not the import identity.",
		"ARN", "arn", "id"),
	// aws_secretsmanager_secret_policy and aws_secretsmanager_secret_rotation:
	// both row-gen classified evidence-only (folded onto
	// AWS::SecretsManager::Secret's own registry entry). Both are real,
	// separate CFN types with their own Identity Schema
	// (live/import-grammar.json): a single required argument — secret_arn
	// for the policy, secret_id for the rotation schedule — that is also
	// each resource's own required config argument, referencing the parent
	// secret's ARN through the aws_secretsmanager_secret marker above. Both
	// would self-admit through [SynthesizeTypeIdentity] given schemas; hand
	// rows are added anyway, the same way aws_route53_hosted_zone_dnssec and
	// aws_cloudfront_monitoring_subscription above carry hand rows despite
	// being single-attribute self-admit candidates too, so the cohort estate
	// documents them explicitly rather than depending on schema
	// availability at resolution time.
	TypeIdentity{
		Type:          "aws_secretsmanager_secret_policy",
		Components:    []Component{attr("secret_arn")},
		ImportSyntax:  "SECRETARN",
		IdentityAttrs: []string{"secret_arn"},
	},
	TypeIdentity{
		Type:          "aws_secretsmanager_secret_rotation",
		Components:    []Component{attr("secret_id")},
		ImportSyntax:  "SECRETID",
		IdentityAttrs: []string{"secret_id"},
	},
	// aws_secretsmanager_secret_version (credential: secret_id plus a
	// server-assigned version UUID, the secret unreadable after create) and
	// aws_secretsmanager_tag (untaggable, no native list resource, no
	// identity schema in v6.59.0) are not repeated here — see the cohort
	// README's "Rejected" section.

	// KMS remainder (issue #65's own "grants, replica keys, custom key
	// stores" suggestion). aws_kms_external_key and aws_kms_replica_key both
	// map to CFN's AWS::KMS::Key/ReplicaKey types, both taggable
	// (live/survey-full.json), and both ship no identity schema in v6.59.0,
	// the same docs-tier shape aws_kms_key itself was admitted under
	// originally: no name argument reconstructs the server-minted key ID,
	// so both go through the marker path — verified live: floci's
	// `kms:CreateKey`, `TagResource` and `ListResourceTags` round-trip
	// cleanly (the same API family aws_kms_key already proved). Neither
	// aws_kms_grant nor aws_kms_custom_key_store is ratified: see the
	// cohort README's "Credential-adjacent exclusions" section — both are
	// cfn-unmodeled, untaggable, and ship no native provider list resource
	// (live/survey-full.json: "moves to Ops" for both, independent of any
	// credential concern), and aws_kms_custom_key_store's own
	// `key_store_password` argument is a literal credential value this
	// batch declines to plumb through a live read on principle, extending
	// opsExcluded's reasoning explicitly even though the ordinary
	// recoverability rule already excludes it.
	serverAssigned("aws_kms_external_key",
		"KMS assigns the key ID (a UUID) at create time, the same shape as aws_kms_key; the type has no name argument and no identity schema in v6.59.0, so nothing in configuration reconstructs it.",
		"KEYID", "id", "key_id"),
	serverAssigned("aws_kms_replica_key",
		"KMS assigns the replica key's own ID (a UUID) at create time; the primary_key_arn argument names the key it replicates, not this key's own identity.",
		"KEYID", "id", "key_id"),

	// SSM remainder (documents, maintenance windows, patch baselines,
	// associations). A live floci probe found the entire remainder blocked
	// at create time (ssm:CreateAssociation, CreatePatchBaseline and
	// CreateMaintenanceWindow all answer UnsupportedOperation, the same
	// answer aws_ssm_document's own CreateDocument already gets and
	// live/residue.go already records) — this batch ratifies the remainder
	// on identity grounds anyway, the same stance aws_efs_file_system and
	// the whole FSx family took in the storage batch despite the pinned
	// image serving neither, and leaves aws_ssm_document itself alone: it
	// is live/residue.go's one deliberately curated "kept out of a wiring
	// slice entirely" example (EmulatorBlocked, Admitted: false), and nothing
	// about this batch's own findings changes that judgment or forces a
	// swap to a new example. See the cohort README for the full account.
	//
	// aws_ssm_association: taggable, server-assigned association_id
	// (live/survey-full.json), no native list resource in v6.59.0 — the
	// marker path, the same shape as aws_ssm_patch_baseline and
	// aws_ssm_maintenance_window below.
	serverAssigned("aws_ssm_association",
		"SSM assigns the association its own ID at create time; the name argument names the document, not the association.",
		"ASSOCIATIONID", "id", "association_id"),
	serverAssigned("aws_ssm_maintenance_window",
		"SSM assigns the maintenance window its own ID (mw-…) at create time; the name argument is client-chosen but is not the import identity.",
		"WINDOWID", "id"),
	serverAssigned("aws_ssm_patch_baseline",
		"SSM assigns the patch baseline its own ID (pb-…) at create time; the name argument is client-chosen but is not the import identity.",
		"BASELINEID", "id"),
	// aws_ssm_patch_group: row-gen classified this a fold (property-child of
	// AWS::SSM::PatchBaseline), evidence-only. The provider's own Identity
	// Schema settles it directly (live/import-grammar.json): required
	// baseline_id and patch_group, both already-admitted-or-configured
	// (baseline_id through the aws_ssm_patch_baseline marker above,
	// patch_group a client-chosen string), comma-joined in the documented
	// order (terraform import aws_ssm_patch_group.example
	// patch-group-name,pb-1234567890abcdef0). A composite of two required
	// attributes is exactly what [SynthesizeTypeIdentity] refuses to
	// self-admit, the same reason aws_route53_zone_association needs a hand
	// row despite a full Identity Schema too.
	TypeIdentity{
		Type: "aws_ssm_patch_group",
		Components: []Component{
			attr("patch_group"),
			sep(","),
			attr("baseline_id"),
		},
		ImportSyntax:  "PATCHGROUP,BASELINEID",
		IdentityAttrs: nil,
	},
	// aws_ssm_resource_data_sync: row-gen's own evidence shows a
	// docs-tier gap (no identity schema in v6.59.0) that its mechanical
	// classifier reads as "moves to Ops" — but the provider's own Import
	// section is unambiguous: import by the "name" argument alone
	// (terraform import aws_ssm_resource_data_sync.example example-name),
	// the type's own required, client-chosen argument. Client-named,
	// verified against the documented import command directly rather than
	// against any identity schema, the same docs-tier correction
	// aws_cloudfront_function's own entry above makes.
	TypeIdentity{
		Type:          "aws_ssm_resource_data_sync",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_ssm_service_setting: same docs-tier gap and same correction. The
	// provider's Import section documents import by the "setting_id"
	// argument alone — not a server-minted token but the full, fixed
	// service-setting path the caller must already name in configuration
	// to update an existing, AWS-predefined setting (e.g.
	// "arn:aws:ssm:REGION:ACCOUNT:servicesetting/ssm/parameter-store/high-throughput-enabled").
	// Client-named.
	TypeIdentity{
		Type:          "aws_ssm_service_setting",
		Components:    []Component{attr("setting_id")},
		ImportSyntax:  "SETTINGID",
		IdentityAttrs: []string{"setting_id"},
	},
	// aws_ssm_default_patch_baseline, aws_ssm_maintenance_window_target and
	// aws_ssm_maintenance_window_task are not ratified — see the cohort
	// README's "Rejected" section (the latter two are untaggable with
	// list_required_input set, the exact internal/live/registry.Roster
	// shape that already excludes aws_efs_mount_target in the storage
	// batch).

	// ACM-PCA.
	//
	// aws_acmpca_certificate_authority: taggable, server-assigned ARN
	// (live/survey-full.json, live/import-grammar.json's Identity Schema) —
	// the marker path.
	serverAssigned("aws_acmpca_certificate_authority",
		"ACM Private CA assigns the certificate authority's own ARN at create time; the subject and key parameters describe it but do not identify it.",
		"CERTIFICATEAUTHORITYARN", "arn", "id"),
	// aws_acmpca_certificate_authority_certificate: row-gen proposed this
	// correctly (client-named): a single required argument,
	// certificate_authority_arn, already in configuration through the
	// marker above — the CA's own activation record, at most one per CA,
	// the same named-singleton-child shape as aws_route53_hosted_zone_dnssec.
	TypeIdentity{
		Type:          "aws_acmpca_certificate_authority_certificate",
		Components:    []Component{attr("certificate_authority_arn")},
		ImportSyntax:  "CERTIFICATEAUTHORITYARN",
		IdentityAttrs: []string{"certificate_authority_arn"},
	},
	// aws_acmpca_policy: cfn-unmodeled (no AWS::ACMPCA::Policy type exists;
	// searched the registry directly). live/survey-full.json's own
	// heuristic mislinks its parent as "aws_api_gateway_resource" — a false
	// match on the shared argument name "resource_arn" rather than a real
	// relationship; the provider's own Identity Schema is unambiguous that
	// this resource_arn is "the ACM PCA certificate authority" ARN,
	// confirmed against live/import-grammar.json's evidence excerpt. Wired
	// through the CA marker above instead, correcting the mislink the same
	// way this batch corrects aws_securityhub_organization_admin_account's
	// and aws_securityhub_configuration_policy_association's below.
	TypeIdentity{
		Type:          "aws_acmpca_policy",
		Components:    []Component{attr("resource_arn")},
		ImportSyntax:  "RESOURCEARN",
		IdentityAttrs: []string{"resource_arn"},
	},
	// aws_acmpca_certificate (untaggable, no native list resource — a leaf
	// certificate the CA mints per request) and aws_acmpca_permission
	// (untaggable, no identity schema) are not ratified — see the cohort
	// README.

	// GuardDuty.
	//
	// aws_guardduty_detector: taggable, server-assigned ID
	// (live/survey-full.json) — the marker path, the account-wide detector
	// every other GuardDuty type below is scoped under.
	serverAssigned("aws_guardduty_detector",
		"GuardDuty assigns the detector its own ID at create time; nothing in configuration names it.",
		"DETECTORID", "id"),
	// aws_guardduty_filter: row-gen classified this needs-hand-separator
	// (registry primaryIdentifier ["DetectorId", "Name"], composite, no
	// separator in any schema). The provider's own documented import
	// command supplies it directly: detector_id and name, colon-joined
	// (terraform import aws_guardduty_filter.MyFilter
	// 00b00fd5aecc0ab60a708659477e9617:MyFilter) — detector_id through the
	// marker above, name a client-chosen, already-configured argument. Not
	// wired through the marker's own tag-filtered discovery, because the
	// composite is fully derivable from configuration and needs no live
	// read to build.
	TypeIdentity{
		Type: "aws_guardduty_filter",
		Components: []Component{
			attr("detector_id"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "DETECTORID:NAME",
		IdentityAttrs: nil,
	},
	// aws_guardduty_ipset, aws_guardduty_threatintelset and
	// aws_guardduty_publishing_destination: all three document a
	// "detectorId:id" composite import string, but unlike the filter above,
	// the second half is a server-minted ID none of the three types' own
	// arguments supplies — detector_id is a plain, already-known
	// configuration argument, not something a Component needs to compose,
	// and the set/destination's own id is exactly the aws_kms_key shape:
	// server-assigned, recovered by the marker's tag-filtered list rather
	// than built from configuration. All three are taggable
	// (live/survey-full.json).
	serverAssigned("aws_guardduty_ipset",
		"GuardDuty assigns the IPSet its own ID at create time; detector_id is a required argument but does not identify this set, and the format argument describes the file, not the set.",
		"DETECTORID:ID", "id"),
	serverAssigned("aws_guardduty_threatintelset",
		"GuardDuty assigns the ThreatIntelSet its own ID at create time; detector_id is a required argument but does not identify this set.",
		"DETECTORID:ID", "id"),
	serverAssigned("aws_guardduty_publishing_destination",
		"GuardDuty assigns the publishing destination its own ID at create time; detector_id is a required argument but does not identify this destination.",
		"DETECTORID:ID", "id"),
	// aws_guardduty_malware_protection_plan: row-gen proposed this
	// correctly (server-assigned): taggable, the plan's own ID minted at
	// create time.
	serverAssigned("aws_guardduty_malware_protection_plan",
		"GuardDuty assigns the malware protection plan its own ID at create time; nothing in configuration names it.",
		"MALWAREPROTECTIONPLANID", "id"),
	// aws_guardduty_member: row-gen's mechanical classifier calls this
	// "moves to Ops" (no identity schema in v6.59.0, untaggable), but the
	// provider's own Import section is unambiguous — detector_id and
	// account_id, colon-joined (terraform import aws_guardduty_member.MyMember
	// 00b00fd5aecc0ab60a708659477e9617:123456789012) — both the type's own
	// required, already-configured arguments (you must name which detector
	// and which account to invite), the same docs-tier correction
	// aws_ssm_resource_data_sync's entry above makes.
	TypeIdentity{
		Type: "aws_guardduty_member",
		Components: []Component{
			attr("detector_id"),
			sep(":"),
			attr("account_id"),
		},
		ImportSyntax:  "DETECTORID:ACCOUNTID",
		IdentityAttrs: nil,
	},
	// aws_guardduty_organization_admin_account: same docs-tier correction —
	// import by the admin_account_id argument alone (a required,
	// already-configured account ID designating the org's GuardDuty
	// delegated administrator), client-named rather than "moves to Ops".
	TypeIdentity{
		Type:          "aws_guardduty_organization_admin_account",
		Components:    []Component{attr("admin_account_id")},
		ImportSyntax:  "ADMINACCOUNTID",
		IdentityAttrs: []string{"admin_account_id"},
	},
	// aws_guardduty_organization_configuration: import by the detector_id
	// argument alone (terraform import
	// aws_guardduty_organization_configuration.example
	// 00b00fd5aecc0ab60a708659477e9617) — the same named-singleton-child
	// shape as aws_route53_hosted_zone_dnssec, through the detector marker
	// above.
	TypeIdentity{
		Type:          "aws_guardduty_organization_configuration",
		Components:    []Component{attr("detector_id")},
		ImportSyntax:  "DETECTORID",
		IdentityAttrs: nil,
	},
	// aws_guardduty_detector_feature (untaggable fold-child),
	// aws_guardduty_organization_configuration_feature (no cached evidence
	// for its exact import grammar — not guessed) and
	// aws_guardduty_invite_accepter (a waiter: flips a pending invitation
	// to accepted, with no cloud resource of its own, the same shape
	// opsExcluded's aws_acm_certificate_validation entry already carries)
	// are not ratified — see the cohort README.

	// Macie2. Confirmed not an AWS-deprecated service before proceeding
	// (issue #65's own instruction): Macie Classic's aws_macie_* resources
	// are gone from the provider entirely (live/mapping.json has none), and
	// Macie2 itself carries no AWS end-of-support notice, unlike this
	// batch's WAF-Classic-adjacent neighbors already in
	// live/residue.go's DeprecatedServices.
	//
	// aws_macie2_custom_data_identifier, aws_macie2_findings_filter: both
	// taggable, both server-assigned IDs (live/survey-full.json,
	// live/registry.json) — the marker path.
	serverAssigned("aws_macie2_custom_data_identifier",
		"Macie assigns the custom data identifier its own ID at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_macie2_findings_filter",
		"Macie assigns the findings filter its own ID at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	// aws_macie2_classification_job: cfn-unmodeled (registry search: no
	// AWS::Macie::ClassificationJob type), but live/survey-full.json's own
	// signal, read off the real provider schema, shows it taggable — the
	// same TagResource/ListTagsForResource surface
	// aws_macie2_custom_data_identifier and aws_macie2_findings_filter
	// share, hand-verified since no CFN registry entry backs it.
	serverAssigned("aws_macie2_classification_job",
		"Macie assigns the classification job its own ID at create time; the name argument is client-chosen but is not the import identity.",
		"JOBID", "id"),
	// aws_macie2_member: row-gen's mechanical classifier calls this
	// "moves to Ops"; the provider's own Import section documents import by
	// the account ID of the member account alone (terraform import
	// aws_macie2_member.example 123456789012), the type's own required
	// account_id argument. Client-named, the same docs-tier correction
	// aws_guardduty_member's account half makes.
	TypeIdentity{
		Type:          "aws_macie2_member",
		Components:    []Component{attr("account_id")},
		ImportSyntax:  "ACCOUNTID",
		IdentityAttrs: []string{"account_id"},
	},
	// aws_macie2_organization_admin_account: same docs-tier correction as
	// aws_guardduty_organization_admin_account above — import by
	// admin_account_id alone, client-named.
	TypeIdentity{
		Type:          "aws_macie2_organization_admin_account",
		Components:    []Component{attr("admin_account_id")},
		ImportSyntax:  "ADMINACCOUNTID",
		IdentityAttrs: []string{"admin_account_id"},
	},
	// aws_macie2_account and aws_macie2_classification_export_configuration
	// (both untaggable, per-account-and-region singletons with no
	// distinguishing argument — the identity is the run's own AWS account
	// ID, which nothing in this fork's identity resolution can supply; see
	// internal/live/identity's CloudContext doc comment) and
	// aws_macie2_invitation_accepter (a waiter) are not ratified — see the
	// cohort README.

	// SecurityHub. The registry-vs-schema mismatch issue #65 warned about:
	// several legacy v1 types' CFN registry entries claim `tagging.taggable:
	// true`, but live/survey-full.json's signal — read off the real
	// provider schema, not the registry — shows the v1 resource itself
	// ships no tags argument at all. The newer v2 generation (HubV2,
	// AggregatorV2, AutomationRuleV2, ConnectorV2) is where the two sources
	// agree and the marker path is real; v1's aws_securityhub_automation_rule
	// is the one v1 type this batch found where the schema itself confirms
	// real tagging support, so it ratifies alongside its v2 siblings.
	//
	// aws_securityhub_account_v2, aws_securityhub_aggregator_v2,
	// aws_securityhub_automation_rule, aws_securityhub_automation_rule_v2:
	// all four taggable per live/survey-full.json, all four server-assigned
	// ARNs.
	serverAssigned("aws_securityhub_account_v2",
		"Security Hub assigns the v2 hub's own ARN at create time; nothing in configuration names it.",
		"ARN", "arn", "id"),
	serverAssigned("aws_securityhub_aggregator_v2",
		"Security Hub assigns the v2 aggregator's own ARN at create time; nothing in configuration names it.",
		"ARN", "arn", "id"),
	serverAssigned("aws_securityhub_automation_rule",
		"Security Hub assigns the automation rule's own ARN at create time; the rule's own schema (unlike aws_securityhub_account's) carries a real tags argument, confirmed against live/survey-full.json.",
		"ARN", "arn", "id"),
	serverAssigned("aws_securityhub_automation_rule_v2",
		"Security Hub assigns the v2 automation rule's own ARN at create time; nothing in configuration names it.",
		"ARN", "arn", "id"),
	// aws_securityhub_connector_v2: taggable, server-assigned connector_id
	// (live/import-grammar.json's Identity Schema).
	serverAssigned("aws_securityhub_connector_v2",
		"Security Hub assigns the v2 connector's own ID at create time; the provider/name arguments describe it but do not identify it.",
		"CONNECTORID", "id", "connector_id"),
	// aws_securityhub_configuration_policy_association: row-gen and
	// live/survey-full.json's own heuristic mislink its parent as
	// "aws_db_proxy_target" — a false match on the shared argument name
	// "target_id" rather than a real relationship. The provider's own
	// Identity Schema names target_id as the type's sole required import
	// attribute, and it is a plain, already-configured string (the account,
	// OU or root ID the policy applies to) — client-named, not
	// parent-derived through any type this table admits.
	TypeIdentity{
		Type:          "aws_securityhub_configuration_policy_association",
		Components:    []Component{attr("target_id")},
		ImportSyntax:  "TARGETID",
		IdentityAttrs: []string{"target_id"},
	},
	// aws_securityhub_organization_admin_account: same mislink correction
	// (live/survey-full.json names its bogus parent "aws_fms_admin_account"
	// on the same shared-argument-name basis) — import by admin_account_id
	// alone, client-named.
	TypeIdentity{
		Type:          "aws_securityhub_organization_admin_account",
		Components:    []Component{attr("admin_account_id")},
		ImportSyntax:  "ADMINACCOUNTID",
		IdentityAttrs: []string{"admin_account_id"},
	},
	// aws_securityhub_standards_control: the ram-servicecatalog family
	// sweep (issue #53) already found and recorded the false friend here —
	// AWS::SecurityHub::SecurityControl's own primary identifier is
	// SecurityControlId alone (the newer, standard-independent unified
	// control view), not the per-standard-scoped standards_control_arn this
	// type actually manages, so live/mapping.json correctly leaves this
	// type unmapped to that CFN type rather than folding onto it. The
	// provider's own Identity Schema settles the real identity directly: a
	// single required argument, standards_control_arn, already in
	// configuration. Client-named.
	TypeIdentity{
		Type:          "aws_securityhub_standards_control",
		Components:    []Component{attr("standards_control_arn")},
		ImportSyntax:  "STANDARDSCONTROLARN",
		IdentityAttrs: []string{"standards_control_arn"},
	},
	// aws_securityhub_standards_control_association: the same false-friend
	// avoidance as the type above, for the same reason (SecurityControlId
	// alone, no standards_arn dimension). Two required arguments,
	// comma-joined per the documented import command (terraform import
	// aws_securityhub_standards_control_association.example
	// IAM.1,arn:aws:securityhub:...) — both plain, already-configured
	// strings, not composed through any type this table admits.
	TypeIdentity{
		Type: "aws_securityhub_standards_control_association",
		Components: []Component{
			attr("security_control_id"),
			sep(","),
			attr("standards_arn"),
		},
		ImportSyntax:  "SECURITYCONTROLID,STANDARDSARN",
		IdentityAttrs: nil,
	},
	// aws_securityhub_member: the provider's Identity Schema names the
	// required import attribute "member_account_id", but the resource's own
	// configuration argument is named "account_id" — a name mismatch
	// [SynthesizeTypeIdentity] cannot bridge (it only ever reads an
	// argument under its own identity-attribute name), so this needs a hand
	// row even though the shape is otherwise the simplest client-named case
	// in this batch. Confirmed against the provider's Argument Reference
	// (required: account_id) and Import section (member AWS account ID).
	TypeIdentity{
		Type: "aws_securityhub_member",
		Components: []Component{
			inAttr("member_account_id", attr("account_id")),
		},
		ImportSyntax:  "MEMBERACCOUNTID",
		IdentityAttrs: nil,
	},
	// aws_securityhub_account (untaggable per the real schema despite the
	// registry's taggable claim; a per-account singleton needing a
	// CloudContext this fork's resolver does not supply),
	// aws_securityhub_configuration_policy, aws_securityhub_finding_aggregator,
	// aws_securityhub_insight, aws_securityhub_organization_configuration,
	// aws_securityhub_product_subscription, aws_securityhub_standards_subscription
	// and aws_securityhub_invite_accepter (a waiter) are not ratified — see
	// the cohort README.

	// Inspector2.
	//
	// aws_inspector2_filter: taggable, server-assigned ARN
	// (live/survey-full.json, live/import-grammar.json's Identity Schema).
	serverAssigned("aws_inspector2_filter",
		"Inspector2 assigns the filter its own ARN at create time; the name argument is client-chosen but is not the import identity.",
		"ARN", "arn", "id"),
	// aws_inspector2_delegated_admin_account, aws_inspector2_member_association:
	// row-gen's mechanical classifier calls both "moves to Ops"; both
	// documented import commands are unambiguous — the account_id argument
	// alone, the type's own required, already-configured argument in both
	// cases. Client-named.
	TypeIdentity{
		Type:          "aws_inspector2_delegated_admin_account",
		Components:    []Component{attr("account_id")},
		ImportSyntax:  "ACCOUNTID",
		IdentityAttrs: []string{"account_id"},
	},
	TypeIdentity{
		Type:          "aws_inspector2_member_association",
		Components:    []Component{attr("account_id")},
		ImportSyntax:  "ACCOUNTID",
		IdentityAttrs: []string{"account_id"},
	},
	// aws_inspector2_organization_configuration (untaggable singleton, no
	// distinguishing argument) and aws_inspector2_enabler (a dynamic,
	// sorted-list-encoded composite of account_ids and resource_types this
	// table's Component vocabulary cannot express without guessing) are not
	// ratified — see the cohort README.

	// WAFv2. All four "needs hand separator" marker candidates below
	// document a composite ID/Name/Scope import string, but Id is the part
	// CloudFront — sorry, WAFv2 — actually mints at create time; Name and
	// Scope are configured arguments the marker path does not need to
	// compose, the same aws_kms_key shape as the GuardDuty set/destination
	// types above. All four are taggable (live/survey-full.json and a live
	// floci probe: `wafv2:CreateIPSet` and `ListIPSets` both work).
	// internal/live/registry.Roster.Listable reports every one of them
	// unlistable through the Cloud Control fallback today (Scope is a
	// required list input, the same EnumerabilityParentInput shape that
	// already excludes aws_efs_mount_target in the storage batch), and no
	// native provider list resource exists either
	// (live/survey-full.json: list_resource false) — admitted anyway on
	// identity grounds, the same stance the storage batch's whole FSx
	// family took despite an identical zero-enumeration gap; see the
	// cohort README.
	serverAssigned("aws_wafv2_ip_set",
		"WAFv2 assigns the IP set its own ID at create time; the documented import string composes it with the client-chosen name and scope, but neither reconstructs the ID itself.",
		"ID/NAME/SCOPE", "id", "arn"),
	serverAssigned("aws_wafv2_regex_pattern_set",
		"WAFv2 assigns the regex pattern set its own ID at create time; the documented import string composes it with the client-chosen name and scope, but neither reconstructs the ID itself.",
		"ID/NAME/SCOPE", "id", "arn"),
	serverAssigned("aws_wafv2_rule_group",
		"WAFv2 assigns the rule group its own ID at create time; the documented import string composes it with the client-chosen name and scope, but neither reconstructs the ID itself.",
		"ID/NAME/SCOPE", "id", "arn"),
	serverAssigned("aws_wafv2_web_acl",
		"WAFv2 assigns the web ACL its own ID at create time; the documented import string composes it with the client-chosen name and scope, but neither reconstructs the ID itself.",
		"ID/NAME/SCOPE", "id", "arn"),
	// aws_wafv2_web_acl_rule: row-gen folded this onto AWS::WAFv2::WebACL's
	// own registry entry and proposed parent-derived admission "once [the
	// web ACL] is ratified" — ratified above, in this same batch. Two
	// required arguments per the provider's own Identity Schema, comma-joined
	// per the documented import command (terraform import
	// aws_wafv2_web_acl_rule.example
	// arn:aws:wafv2:...:regional/webacl/example/abc123def456,my-rule):
	// web_acl_arn through the web ACL marker above, name a client-chosen,
	// already-configured argument.
	TypeIdentity{
		Type: "aws_wafv2_web_acl_rule",
		Components: []Component{
			attr("web_acl_arn"),
			sep(","),
			attr("name"),
		},
		ImportSyntax:  "WEBACLARN,NAME",
		IdentityAttrs: nil,
	},
)

func init() { registerCohortTable(identityTableSecurity) }
