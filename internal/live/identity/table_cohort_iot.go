// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableIot is the iot cohort's slice of [DefaultTable]:
// the identity rows the iot ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableIot = buildTable(
	// ---- Registry-ratified (#40, #44, #65): sixth batch, IoT core
	// ---- (issue #65) ------------------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own website/docs/r/
	// source at the pinned v6.59.0 tag — this checkout's merge of origin/main
	// moved the pin from v6.58.0, the tag the fifth batch cited, to v6.59.0),
	// not accepted on the registry's classification alone. Six of the eleven
	// rows below (aws_iot_authorizer, aws_iot_billing_group,
	// aws_iot_domain_configuration, aws_iot_thing, aws_iot_thing_group,
	// aws_iot_thing_type) were row-gen "evidence-only" proposals it declined
	// to paste, because their sole identifying argument was GUESSED by
	// snake-casing the CFN property name rather than backed by a provider
	// identity schema or live/import-grammar.json; the provider's own docs
	// confirm all six actually import by the plain `name` argument, not
	// row-gen's guessed `authorizer_name`, `billing_group_name`,
	// `domain_configuration_name`, `thing_name`, `thing_group_name` or
	// `thing_type_name` — the argument these resources document is shorter
	// than the CFN property name row-gen snake-cased it from. A seventh,
	// aws_iot_role_alias, is the same evidence-only shape but the correction
	// runs the other way: the provider's required argument is the bare
	// `alias`, not row-gen's guessed `role_alias`. aws_iot_policy is an
	// eighth evidence-only row-gen left entirely unpasted ("no pastable
	// row") with a note that the provider's own import docs show an
	// argument-composed ID (`PubSubToAnyTopic`); the provider's Import
	// section confirms that value is the required `name` argument verbatim.
	// aws_iot_provisioning_template and aws_iot_topic_rule are the two rows
	// row-gen proposed correctly the first time (client-named via `name`,
	// sourced from live/import-grammar.json rather than a guess), confirmed
	// unchanged against the provider's docs. aws_iot_topic_rule_destination
	// is the one server-assigned row row-gen proposed, confirmed unchanged:
	// the provider's own documented import command uses the type's `arn`
	// verbatim, no account/region reconstruction needed since the resource
	// carries no argument that would rebuild it. Cohort estate:
	// live/e2e/estates/iot.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_iot_ca_certificate: row-gen proposed server-assigned via the
	//     registry's opaque "Id". The provider's own docs (fetched as raw
	//     markdown source, not the rendered page, to rule out a fetch
	//     artifact) carry no "## Import" heading anywhere in the file — no
	//     classic import command, no import-block identity schema, nothing.
	//     A CA certificate is client-supplied (`ca_certificate_pem` is a
	//     required argument, not a server-generated output) so this
	//     rejection is not the credential-material one below; it is the
	//     plainer "the provider documents no way in" rejection, the same
	//     kind aws_codebuild_source_credential got in the developer-tools
	//     batch, just for a structural reason (no Import section at all)
	//     rather than a missing-argument one.
	//   - aws_iot_certificate: row-gen proposed server-assigned via the
	//     registry's opaque "Id". Same structural gap as the CA certificate
	//     above — no "## Import" heading anywhere in the provider's raw doc
	//     source — but this type also fails a second, independent bar this
	//     batch was asked to check it against explicitly: live/SURVEY.md's
	//     "three the rule excludes" permanently bars a type that is a
	//     credential born server-side alongside a secret that can never be
	//     read again, the same standing exclusion that keeps
	//     aws_iam_access_key out (internal/live/identity/table.go's own IAM
	//     batch comment above, and live/SURVEY.md's "The three the rule
	//     excludes" section). The provider's own Attribute Reference for
	//     this type: "When neither CSR nor certificate is provided, the
	//     public key" / "...the private key" — confirmed against the
	//     example usage's own "Without CSR" case, which creates the
	//     resource with no `csr` and no `certificate_pem` argument set at
	//     all. A caller who omits both arguments (a legal, documented
	//     configuration) gets a resource whose live state carries a private
	//     key AWS mints once and never returns again — exactly the
	//     forwarding-to-Ops shape live/SURVEY.md draws for
	//     aws_iam_access_key and aws_secretsmanager_secret_version. Ruling:
	//     excluded by that rule, independent of and in addition to the
	//     missing Import section. Unlike aws_iam_access_key, whether a given
	//     instance actually carries key material depends on how it was
	//     created (a `csr`- or `certificate_pem`-supplied instance carries
	//     none) — but the admission table has no per-instance granularity,
	//     only per-type, so the type as a whole is excluded rather than
	//     admitted on the optimistic case.
	//   - aws_iot_policy_attachment: row-gen proposed server-assigned via
	//     the registry's opaque "Id" (registry primary_identifier=["Id"],
	//     but this type's own CFN read_only_properties list is also just
	//     ["Id"] with no Arn alongside it, already a thinner evidence shape
	//     than most server-assigned proposals in this table). The provider's
	//     own docs carry no "## Import" heading at all, and its Attribute
	//     Reference is explicit that the resource "exports no additional
	//     attributes" beyond the two required arguments (`policy`,
	//     `target`) — no id-bearing output of any kind for an import
	//     mechanism to key off even if one existed.
	//   - aws_iot_thing_principal_attachment: the same shape as the policy
	//     attachment just above, one binding away (`principal`, `thing`
	//     instead of `policy`, `target`): no "## Import" heading in the
	//     provider's docs, and an Attribute Reference that "exports no
	//     additional attributes" beyond the two required arguments.
	//
	// Out of this batch's named scope, per issue #65's own recipe wording
	// ("IoT SiteWise, IoT TwinMaker if proposed with clean evidence" and
	// "GreengrassV2 if proposed"): IoT Events, IoT Analytics, Greengrass
	// (v1 and v2), IoT SiteWise and IoT TwinMaker are none of them proposed.
	// live/registry.json carries CFN schemas for all five services'
	// resources (AWS::IoTEvents::*, AWS::IoTAnalytics::*,
	// AWS::Greengrass::* and AWS::GreengrassV2::*, AWS::IoTSiteWise::*,
	// AWS::IoTTwinMaker::*), but live/mapping.json carries zero rows for
	// any of them, and tools/row-gen's own service listing has no section
	// for any of the five — confirmed independently against the pinned
	// provider's own website/docs/r/ directory listing at the v6.59.0 tag,
	// which ships no aws_iotevents_*, aws_iotanalytics_*, aws_greengrass*
	// (either version) aws_iotsitewise_* or aws_iottwinmaker_* resource at
	// all. This is a provider gap, not a live/mapping.json curation gap:
	// there is nothing to admit or reject for any of the five services in
	// this checkout's pinned provider release. Greengrass V1 would be
	// deprecated-service skip regardless, per issue #65's recipe.
	// live/mapping.json's own aws_iot_event_configurations,
	// aws_iot_indexing_configuration, aws_iot_logging_options and
	// aws_iot_thing_group_membership rows are marked "tf-only" (no CFN
	// model), so row-gen's registry-driven pipeline does not - and
	// structurally cannot - propose them either; they are left for a batch
	// prepared to evidence tf-only rows some other way.

	TypeIdentity{
		// row-gen filed this evidence-only (registry primaryIdentifier
		// ["AuthorizerName"], GUESSED argument "authorizer_name" from the
		// snake-cased CFN property name, not backed by a provider identity
		// schema or live/import-grammar.json). The provider's own Argument
		// Reference names the required argument plain "name", not
		// "authorizer_name", confirmed against the documented import
		// command (terraform import aws_iot_authorizer.example example),
		// which uses that "name" argument verbatim.
		Type:          "aws_iot_authorizer",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// Same correction as aws_iot_authorizer above: row-gen guessed
		// "billing_group_name" (evidence-only), the provider's actual
		// required argument is plain "name", confirmed against the
		// documented import command (terraform import
		// aws_iot_billing_group.example example). The type's own "id" is
		// documented as "The Billing Group ID", a distinct field from name
		// rather than a restatement of it, so it is not claimed here (the
		// same caution this table already applies to aws_codebuild_project
		// in the developer-tools batch above).
		Type:          "aws_iot_billing_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// Same correction again: row-gen guessed "domain_configuration_name"
		// (evidence-only), the provider's actual required argument is plain
		// "name", confirmed against the documented import command
		// (terraform import aws_iot_domain_configuration.example example).
		// The type's own "id" is documented as "The name of the created
		// domain configuration" — literally the name — but IdentityAttrs
		// still claims only "name" itself, consistent with this table's
		// standing non-goal of not inferring an id-alias without a row-gen
		// mechanism to do it (issue #44 non-goals).
		Type:          "aws_iot_domain_configuration",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen left this "no pastable row" — its own note reads "import
		// docs show argument-composed ID: PubSubToAnyTopic" rather than a
		// guess, flagging that the registry's Id-keyed evidence disagreed
		// with what the docs already showed. The provider's documented
		// import command (terraform import aws_iot_policy.pubsub
		// PubSubToAnyTopic) confirms the value is the required "name"
		// argument verbatim; the Attribute Reference echoes "name - The
		// name of this policy" but documents no separate "id" field at all
		// for this type, so only "name" is claimed.
		Type:          "aws_iot_policy",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_iot_provisioning_template.fleet FleetProvisioningTemplate),
		// which uses the required "name" argument verbatim.
		Type:          "aws_iot_provisioning_template",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen filed this evidence-only with a guess that does not
		// survive the check: "role_alias" (a snake-cased echo of the CFN
		// property "RoleAlias"). The provider's actual required argument is
		// the bare "alias", confirmed against the documented import command
		// (terraform import aws_iot_role_alias.example myalias), which uses
		// "alias" verbatim — the correction runs the opposite direction
		// from aws_iot_authorizer and its siblings above, where row-gen's
		// guess was too long rather than the wrong word.
		Type:          "aws_iot_role_alias",
		Components:    []Component{attr("alias")},
		ImportSyntax:  "ALIAS",
		IdentityAttrs: []string{"alias"},
	},
	TypeIdentity{
		// row-gen filed this evidence-only (GUESSED "thing_name"). The
		// provider's actual required argument is plain "name", confirmed
		// against the documented import command (terraform import
		// aws_iot_thing.example example). The Attribute Reference documents
		// default_client_id, version and arn but no separate "id" field, so
		// only "name" is claimed.
		Type:          "aws_iot_thing",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// Same correction as aws_iot_thing above: row-gen guessed
		// "thing_group_name" (evidence-only), the provider's actual
		// required argument is plain "name", confirmed against the
		// documented import command (terraform import
		// aws_iot_thing_group.example example). The type's own "id" is
		// documented as "The Thing Group ID", a distinct field, so it is
		// not claimed here, the same caution as aws_iot_billing_group
		// above.
		Type:          "aws_iot_thing_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// Same correction again: row-gen guessed "thing_type_name"
		// (evidence-only), the provider's actual required argument is
		// plain "name", confirmed against the documented import command
		// (terraform import aws_iot_thing_type.example example).
		Type:          "aws_iot_thing_type",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_iot_topic_rule.rule <name>), which uses the required "name"
		// argument verbatim.
		Type:          "aws_iot_topic_rule",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	serverAssigned("aws_iot_topic_rule_destination",
		"IoT mints the rule destination's own ARN at create time, embedding a random UUID; the required vpc_configuration block (role_arn, subnet_ids, vpc_id, and the optional security_groups) describes the VPC endpoint being confirmed, not what comes back. The pinned v6.59.0 provider's Argument Reference offers only vpc_configuration — no http_url_config alternative — so the arn's \"vpc\" path segment is the only shape this provider release documents.",
		"arn:aws:iot:REGION:ACCOUNT:ruledestination/vpc/UUID", "arn", "id"),
)

func init() { registerCohortTable(identityTableIot) }
