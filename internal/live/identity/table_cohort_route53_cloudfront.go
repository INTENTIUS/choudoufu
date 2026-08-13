// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableRoute53Cloudfront is the route53-cloudfront cohort's slice of [DefaultTable]:
// the identity rows the route53-cloudfront ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableRoute53Cloudfront = buildTable(
	// ---- Registry-ratified (#40, #44, #65): fourth batch, Route53
	// ---- remainder and CloudFront ----------------------------------------
	//
	// Same pipeline as the earlier batches: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour (its Argument
	// Reference, Attribute Reference and Import section, fetched from the
	// provider's own website/docs/r/ source at the pinned v6.58.0 tag)
	// rather than accepted on the registry's word alone. This batch adds a
	// second kind of check row-gen itself cannot make: whether a
	// server-assigned type is actually *recoverable* at all. row-gen's
	// registry evidence only says an identity is server-minted, not whether
	// this fork's tag-filtered marker discovery can ever find the specific
	// live instance a resource block names — that needs
	// live/tag-verbs.json (which AWS API operation, if any, tags a given
	// service's resources) and live/survey-full.json's per-type taggable
	// signal (the AWS provider's own schema, which does not expose a tags
	// argument the underlying API does not support). aws_route53_zone's
	// marker row and aws_route53_record's zone-keyed composite (both above,
	// pre-registry) are this table's oldest precedent for both shapes this
	// batch leans on: a taggable server-assigned type needs nothing but its
	// own marker row, and a type whose import identity is literally built
	// from an already-admitted parent's live value plus configuration
	// arguments is parent-derived, not server-assigned, however row-gen
	// itself classified it. Cohort estate:
	// live/e2e/estates/route53-cloudfront.
	//
	// Rejected, and deliberately absent from this table — every one
	// confirmed server-assigned on the provider's own docs (row-gen was
	// right about that much), but genuinely unrecoverable: untaggable (no
	// live/tag-verbs.json service-wide TagResource/ChangeTagsForResource
	// covers the type, and the provider's own schema carries no tags
	// argument), not an account- or parent-wide singleton the way
	// aws_ecr_registry_policy's account-scoped ECR registry ID is (the
	// second and third IAM/ECR batch's precedent for admitting an untaggable
	// server-assigned type anyway), and not composable from an admitted
	// parent's value the way the parent-derived rows below are — an account
	// may hold arbitrarily many of them, and nothing in configuration or in
	// an admitted parent's identity picks out one:
	//
	//   - aws_route53_cidr_collection: row-gen proposed server-assigned via
	//     the registry's opaque "Id", confirmed against the provider's
	//     Import section (id alone, unrelated to the required "name"
	//     argument). Route53's own tagging operation, ChangeTagsForResource
	//     (live/tag-verbs.json), takes a ResourceType parameter that AWS
	//     documents as accepting only "healthcheck" and "hostedzone" — a
	//     CIDR collection is neither, and the provider's schema for this
	//     type carries no tags argument at all. An account may hold
	//     arbitrarily many collections with no marker to tell them apart.
	//   - aws_route53_cidr_location: row-gen folded this into
	//     AWS::Route53::CidrCollection's own registry entry and proposed
	//     parent-derived admission "once [the collection] is ratified". On
	//     independent verification its import grammar does turn out to be
	//     exactly that composite — the collection's own ID plus the
	//     location's "name" argument, comma-separated
	//     (COLLECTIONID,LOCATIONNAME, confirmed against the provider's
	//     Import section and Argument Reference) — the same shape as the
	//     parent-derived rows below. It stays out anyway, and only for the
	//     reason just above: its parent, aws_route53_cidr_collection, is
	//     rejected, so nothing admits the live collection ID a
	//     cidr_collection_id argument would need to resolve to in a
	//     stateless run.
	//   - aws_route53_query_log: row-gen folded this into
	//     AWS::Route53::HostedZone's own registry entry and proposed
	//     parent-derived admission through aws_route53_zone, the same
	//     speculative note as the cidr_location fold above. Verification
	//     does not bear it out here: the provider's documented import ID
	//     (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx) is a bare, opaque UUID the
	//     query-logging subscription's own create call mints — not the
	//     hosted_zone_id argument, and not any concatenation of it with
	//     another configured value. Query logging is not its own
	//     CloudFormation-registered resource type at all (it is a nested
	//     property of AWS::Route53::HostedZone), so there is no Cloud
	//     Control list handler to enumerate it by either, and the type
	//     carries no tags argument. row-gen's own "no pastable row" verdict
	//     was the honest one; the fold's speculative note does not survive
	//     independent verification.
	//   - aws_route53recoverycontrolconfig_routing_control: row-gen
	//     proposed server-assigned via the registry's opaque
	//     "RoutingControlArn", confirmed against the provider's own Import
	//     section and Attribute Reference. Unlike its three
	//     Route53RecoveryControl siblings ratified below, this type's
	//     schema carries no tags argument (confirmed against
	//     live/survey-full.json and against live/registry.json, whose
	//     createOnlyProperties for this type omits "Tags" where the
	//     cluster's and control panel's do not), and the ARN's own
	//     routing-control segment (d5d90e587870494b in the documented
	//     example) is a second, independent server-minted token — not the
	//     required "name" argument the provider docs also list, so it is
	//     not literally composable from configuration plus an admitted
	//     parent's ARN either.
	//   - aws_route53_resolver_dnssec_config: row-gen proposed
	//     server-assigned via the registry's opaque "Id", confirmed against
	//     the provider's Import section (rdsc-… id, not the type's sole
	//     "resource_id" argument). resource_id names a VPC — aws_vpc is
	//     already admitted above — but the DNSSEC config's own import id is
	//     a second, unrelated server-minted token, so unlike
	//     aws_route53_hosted_zone_dnssec below (whose import id *is*
	//     literally its parent's own id) this one cannot be built as a
	//     parent-derived composite. The type is untaggable and
	//     Route53Resolver's own generic TagResource operation
	//     (live/tag-verbs.json) does not change that: the provider's schema
	//     for this specific type carries no tags argument to attach through
	//     it.
	//   - aws_route53_resolver_query_log_config_association: row-gen
	//     proposed server-assigned via the registry's opaque "Id",
	//     confirmed against the provider's Import section (rqlca-… id,
	//     unrelated to the "resolver_query_log_config_id"/"resource_id"
	//     arguments it composes from at create time). Untaggable, same
	//     shape as the DNSSEC config just above.
	//   - aws_route53profiles_resource_association: row-gen proposed
	//     server-assigned via the registry's opaque "Id", confirmed against
	//     the provider's Import section (rpa-id-… id, unrelated to its
	//     "profile_id"/"resource_arn"/"name" arguments). Unlike its two
	//     Route53Profiles siblings ratified below, live/registry.json shows
	//     no "Tags" in this type's createOnlyProperties, and
	//     live/survey-full.json confirms the provider's schema carries none
	//     either — Route53Profiles' own generic TagResource operation
	//     (live/tag-verbs.json) has nothing to attach to for this type.
	//   - aws_cloudfront_cache_policy, aws_cloudfront_continuous_deployment_policy,
	//     aws_cloudfront_key_group, aws_cloudfront_origin_access_identity,
	//     aws_cloudfront_origin_request_policy, aws_cloudfront_public_key,
	//     aws_cloudfront_response_headers_policy: row-gen proposed all seven
	//     server-assigned via each type's registry-opaque "Id", confirmed
	//     against the provider's Import sections (each imports by a bare,
	//     server-minted id unrelated to the type's own required arguments).
	//     None carries a tags argument (CloudFront's own generic
	//     TagResource operation in live/tag-verbs.json exists but is not
	//     composable by tools/tagverbs-gen's own accounting, and none of
	//     these seven types' provider schemas expose a tags argument to
	//     drive it through regardless), an account may hold arbitrarily many
	//     of each, and none is a parent-derived composite the way
	//     aws_cloudfront_monitoring_subscription below is. This is not
	//     aws_cloudfront_origin_access_control's own shape, despite looking
	//     identical on the surface — see its entry below for the mechanism
	//     that tells the two apart.
	//
	// Out of scope, never proposed by row-gen at all: aws_route53_resolver_config
	// (evidence-only — its "resource_id" argument name is guessed, not
	// backed by a provider identity schema or the carve seed) and the whole
	// Route53RecoveryReadiness service (all four types evidence-only, same
	// guessed-argument reason) — issue #65's recipe names "Route53Profiles/
	// RecoveryControl if proposed" and is silent on RecoveryReadiness for
	// exactly that reason.

	serverAssigned("aws_route53_health_check",
		"Route 53 assigns the health check's own id (a UUID) at create time; nothing in configuration reconstructs it.",
		"ID", "id"),

	serverAssigned("aws_route53profiles_association",
		"Route 53 Profiles assigns the association's own id (rpa-id-…) at create time; the name, resource_id and profile_id arguments describe what is being associated but do not identify the association itself.",
		"ID", "id"),
	serverAssigned("aws_route53profiles_profile",
		"Route 53 Profiles assigns the profile's own id (rp-…) at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),

	serverAssigned("aws_route53recoverycontrolconfig_cluster",
		"Route53 Recovery Control mints the cluster's own ARN at create time; the name argument is client-chosen but the provider's documented import id is the ARN.",
		"ARN", "arn", "id"),
	serverAssigned("aws_route53recoverycontrolconfig_control_panel",
		"Route53 Recovery Control mints the control panel's own ARN at create time, embedding the cluster_arn argument's cluster but not reducible to it; the provider's documented import id is the panel's own ARN.",
		"ARN", "arn", "id"),
	serverAssigned("aws_route53recoverycontrolconfig_safety_rule",
		"Route53 Recovery Control mints the safety rule's own ARN at create time; the rule's several *_rule_config blocks describe its condition but do not identify it.",
		"ARN", "arn", "id"),

	serverAssigned("aws_route53_resolver_endpoint",
		"Route53Resolver assigns the endpoint's own id (rslvr-in-…/rslvr-out-…) at create time; direction and the security_group_ids/ip_address arguments configure it but do not identify it.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_firewall_domain_list",
		"Route53Resolver assigns the domain list's own id (rslvr-fdl-…) at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_firewall_rule_group",
		"Route53Resolver assigns the rule group's own id (rslvr-frg-…) at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_firewall_rule_group_association",
		"Route53Resolver assigns the association's own id (rslvr-frgassoc-…) at create time; the firewall_rule_group_id and vpc_id arguments name what is being associated but do not identify the association itself.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_query_log_config",
		"Route53Resolver assigns the query log config's own id (rqlc-…) at create time; the name and destination_arn arguments configure it but do not identify it.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_rule",
		"Route53Resolver assigns the rule's own id (rslvr-rr-…) at create time; the provider's identity schema requires exactly that id, and none of domain_name, rule_type or target_ip reconstructs it.",
		"ID", "id"),
	// aws_route53_resolver_rule_association is the one row-gen proposal in
	// this family whose own registry evidence and live/survey-full.json's
	// mechanical classifier both land on "list + content match" rather than
	// a flat marker: it is untaggable (no tags argument in the provider's
	// schema), but the provider ships a native list resource for it (rare
	// among the types in this batch) whose results carry resolver_rule_id
	// and vpc_id — the same two required arguments the association's create
	// call takes — so listing and matching by that content, the same
	// mechanism aws_eip above uses with its tofu-slot tag standing in for
	// content instead, recovers a specific instance without needing a tag
	// at all. Confirmed against the provider's own Identity Schema (single
	// required attribute: id, unrelated to resolver_rule_id/vpc_id
	// verbatim, ruling out a parent-derived composite the way
	// aws_route53_zone_association below is one).
	serverAssigned("aws_route53_resolver_rule_association",
		"Route53Resolver assigns the association's own id (rslvr-rrassoc-…) at create time; untaggable, but the provider's native list resource returns resolver_rule_id and vpc_id per association, the same two required arguments the association's create call takes, so list plus content match (as aws_eip above uses tags for) recovers a specific instance.",
		"ID", "id"),

	serverAssigned("aws_cloudfront_anycast_ip_list",
		"CloudFront assigns the anycast IP list's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_connection_function",
		"CloudFront assigns the connection function's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_connection_group",
		"CloudFront assigns the connection group's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	// aws_cloudfront_origin_access_control: row-gen proposed server-assigned
	// via the registry's opaque "Id" — right about the identity (confirmed
	// against the provider's own Import section: the bare OAC id, unrelated
	// to the required "name" argument), but the flat serverAssigned()
	// template undersells it exactly the way it undersold aws_sqs_queue in
	// the messaging batch. This type is untaggable (no tags argument in the
	// provider's schema; CloudFront has no per-OAC tagging API at all), so a
	// tag-filtered marker list cannot find one. live/SURVEY.md already
	// worked this out by hand, before row-gen existed: it carries this exact
	// type as "list + content match", the same category
	// aws_route53_resolver_rule_association above earns from its own native
	// list resource — list every OAC in the account and match by content,
	// here the required "name" argument, which AWS enforces unique per
	// account, standing in for the tag a tag-filtered marker would otherwise
	// use. That prior classification is honored here, not second-guessed.
	// It is untaggable and one of live/survey.json's curated 68 rows, which
	// is exactly the shape the messaging batch's aws_sns_topic_subscription
	// hit and deferred rather than ratified — but issue #54 has since landed
	// (tools/survey-gen/untaggable_render.go): live/LIMITATIONS.md's
	// "Untaggable types cannot be removed by the sweep" entry now derives
	// its roster from live/survey-full.json's taggability signal across the
	// *entire* registry-backed roster, not from the curated 68 intersected
	// with the admission table the messaging batch's deferral was written
	// against, so admitting an untaggable curated-68 type is exactly the
	// same doc-mechanical fixup ratifying any other untaggable type already
	// needs — not the out-of-scope hand-edit it used to obligate. See
	// live/e2e/estates/route53-cloudfront/README.md for the full writeup.
	serverAssigned("aws_cloudfront_origin_access_control",
		"CloudFront assigns the origin access control's own id at create time; the name argument is client-chosen (and AWS-enforced unique per account, which is what the list-plus-content match reads) but is not the import identity.",
		"ID", "id"),
	// aws_cloudfront_distribution: precedented already in live/SURVEY.md
	// (path "marker", status "blocked-emulator") before this batch or
	// row-gen existed — row-gen itself declines to propose a pastable row
	// here (evidence-only: live/import-grammar.json's own heuristic
	// mis-read the Identity Schema's required "id" attribute as an
	// "argument-composed" id, which it is not — id is computed, not
	// settable). Confirmed directly against the provider's Identity Schema
	// (required: id) and Import section: the distribution's own id
	// (E74FTE3EXAMPLE-shaped), unrelated to any configuration argument.
	// Taggable and listable (live/survey-full.json), so the marker path
	// this row-gen-shaped entry follows the same shape as
	// aws_route53_zone's own pre-registry row above. Still blocked-emulator
	// per SURVEY.md (floci serves no usable CloudFront distribution
	// lifecycle, choudoufu#26) — not evidence against the identity, the
	// same standing the messaging batch's CloudWatch rows and this batch's
	// own aws_sqs_queue-style entries already established.
	serverAssigned("aws_cloudfront_distribution",
		"CloudFront assigns the distribution's own id at create time; the provider's identity schema requires exactly that id, and no configuration argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_distribution_tenant",
		"CloudFront assigns the distribution tenant's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_multitenant_distribution",
		"CloudFront assigns the multi-tenant distribution's own id at create time, the same shape as aws_cloudfront_distribution above; no configuration argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_trust_store",
		"CloudFront assigns the trust store's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_vpc_origin",
		"CloudFront assigns the VPC origin's own id at create time; the vpc_origin_endpoint_config block configures it but does not identify it.",
		"ID", "id"),

	// aws_cloudfront_function: row-gen itself declines to propose a
	// pastable row (evidence-only — the provider has not adopted the newer
	// Identity Schema plugin feature for this type in v6.58.0, so
	// live/survey-full.json shows no identity schema at all), but
	// live/import-grammar.json's docs-derived evidence and
	// live/registry.json's own AWS::CloudFront::Function row (primaryIdentifier
	// ["Name"], in createOnlyProperties and not in readOnlyProperties) agree
	// with each other and with the provider's documented import command
	// (terraform import aws_cloudfront_function.test my_test_function):
	// client-named, the "name" argument verbatim. The provider's own
	// Attribute Reference does not state that id equals name, so id is not
	// claimed as an identity source here — the same standard of care
	// aws_route's synthesized id gets.
	TypeIdentity{
		Type:          "aws_cloudfront_function",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_cloudfront_key_value_store: row-gen proposed this correctly the
	// first time (client-named via the provider's own identity schema,
	// required: name), confirmed against the provider's documented import
	// command (terraform import aws_cloudfront_key_value_store.example
	// example_store).
	TypeIdentity{
		Type:          "aws_cloudfront_key_value_store",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},

	// aws_cloudfront_realtime_log_config: row-gen proposed server-assigned
	// via the registry's "Arn" (this type's own primaryIdentifier) — right
	// that the ARN is the identity, but the flat serverAssigned() template
	// undersells it the same way it undersold aws_sns_topic and
	// aws_sqs_queue above: this is the account-derived shape, not an opaque
	// ID. The provider's own Identity Schema requires exactly one field,
	// arn, and its documented import command (terraform import
	// aws_cloudfront_realtime_log_config.example
	// arn:aws:cloudfront::111122223333:realtime-log-config/ExampleNameForRealtimeLogConfig)
	// confirms the value is built from the required "name" argument plus
	// the account of the cloud the run is against — no region segment,
	// because CloudFront is a global service, unlike aws_sns_topic's and
	// aws_sqs_queue's regional ARNs/URLs above.
	TypeIdentity{
		Type: "aws_cloudfront_realtime_log_config",
		Components: []Component{
			inAttr("arn", sep("arn:aws:cloudfront::")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":realtime-log-config/")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:cloudfront::ACCOUNT:realtime-log-config/NAME",
		IdentityAttrs: []string{"arn", "id"},
	},

	// aws_route53_hosted_zone_dnssec: row-gen classified this evidence-only
	// (its "hosted_zone_id" argument name was guessed, not backed by a
	// provider identity schema). Independent verification against the
	// provider's Argument Reference (required: hosted_zone_id, the type's
	// only argument beside the optional signing_status) and Import section
	// confirms it is a named-singleton-child of the zone, the same shape as
	// aws_s3_bucket_policy: at most one per hosted zone, and the import id
	// is not a second, separately-minted token the way
	// aws_route53_resolver_dnssec_config's is (rejected above) — it is
	// literally the parent zone's own id, verbatim (terraform import
	// aws_route53_hosted_zone_dnssec.example Z1D633PJN98FT9). Concrete
	// whenever the zone is, through the aws_route53_zone marker.
	TypeIdentity{
		Type:          "aws_route53_hosted_zone_dnssec",
		Components:    []Component{attr("hosted_zone_id")},
		ImportSyntax:  "HOSTEDZONEID",
		IdentityAttrs: nil,
	},
	// aws_route53_key_signing_key: row-gen classified this needs-hand-separator
	// (registry primaryIdentifier ["HostedZoneId", "Name"], composite, no
	// separator in any schema). Independent verification against the
	// provider's Argument Reference (required: hosted_zone_id,
	// key_management_service_arn, name) and Import section supplies the
	// separator row-gen would not guess: a comma, between the parent zone's
	// own id and the key-signing key's own "name" argument (terraform
	// import aws_route53_key_signing_key.example Z1D633PJN98FT9,example) —
	// the Import section's own prose describes the second half as a "KMS
	// Key identifier", but the worked example and the registry's
	// primaryIdentifier both agree it is the "name" argument, not the
	// separate key_management_service_arn argument, so the prose is read as
	// imprecise rather than followed literally. The same
	// hand-separator-from-docs move live/e2e/estates/route53-cloudfront's
	// README makes explicit for aws_route53_zone_association and
	// aws_route53_resolver_firewall_rule below, all three precedented by
	// aws_route53_record's own ZONEID_NAME_TYPE composite above.
	TypeIdentity{
		Type: "aws_route53_key_signing_key",
		Components: []Component{
			attr("hosted_zone_id"),
			sep(","),
			attr("name"),
		},
		ImportSyntax:  "HOSTEDZONEID,NAME",
		IdentityAttrs: nil,
	},
	// aws_route53_zone_association: row-gen classified this evidence-only
	// (folded as a property-child of AWS::Route53::HostedZone). The
	// provider's own Identity Schema settles it directly: required
	// zone_id and vpc_id, both already-admitted-or-configured (zone_id
	// through the aws_route53_zone marker, vpc_id through the
	// already-admitted aws_vpc marker), joined by a colon in the documented
	// import command (terraform import aws_route53_zone_association.example
	// Z123456ABCDEFG:vpc-12345678). A composite of two required identity
	// attributes is exactly what [SynthesizeTypeIdentity] refuses to
	// self-admit (composites need a separator no schema carries), which is
	// why this needs a hand row despite having a full Identity Schema —
	// the same reason aws_route53_record above does.
	TypeIdentity{
		Type: "aws_route53_zone_association",
		Components: []Component{
			attr("zone_id"),
			sep(":"),
			attr("vpc_id"),
		},
		ImportSyntax:  "ZONEID:VPCID",
		IdentityAttrs: nil,
	},
	// aws_route53_resolver_firewall_rule: row-gen folded this into
	// AWS::Route53Resolver::FirewallRuleGroup's own registry entry and
	// proposed parent-derived admission "once [the rule group] is ratified"
	// — ratified above, in this same batch. Independent verification
	// against the provider's Argument Reference and Import section
	// confirms the composite: required firewall_rule_group_id, joined by a
	// colon to firewall_domain_list_id for a standard rule (terraform
	// import aws_route53_resolver_firewall_rule.example
	// rslvr-frg-0123456789abcdef:rslvr-fdl-0123456789abcdef) — both already
	// admitted above, in this batch, as their own marker rows. The
	// provider's docs also describe a second, "DNS Firewall Advanced" rule
	// shape whose second half is a firewall_threat_protection_id the
	// provider itself computes from a dns_threat_protection argument rather
	// than a value configuration supplies directly; this entry does not
	// attempt that shape. It does not need to: attr's own contract (read
	// the first of these arguments the configuration sets; fail naming all
	// of them if none is) means an advanced rule, which sets
	// dns_threat_protection instead of firewall_domain_list_id, simply
	// fails to resolve rather than resolving to a wrong or guessed string —
	// the same honest half-coverage aws_route53_record's own set_identifier
	// caveat above accepts rather than guessing around.
	TypeIdentity{
		Type: "aws_route53_resolver_firewall_rule",
		Components: []Component{
			attr("firewall_rule_group_id"),
			sep(":"),
			attr("firewall_domain_list_id"),
		},
		ImportSyntax:  "FIREWALLRULEGROUPID:FIREWALLDOMAINLISTID",
		IdentityAttrs: nil,
	},
	// aws_cloudfront_monitoring_subscription: row-gen classified this
	// evidence-only (its "distribution_id" argument name was guessed, not
	// backed by a provider identity schema). Independent verification
	// against the provider's Argument Reference (required: distribution_id,
	// monitoring_subscription) and Attribute Reference — "id: the ID …,
	// which corresponds to the distribution_id" — confirms the same
	// named-singleton-child shape as aws_route53_hosted_zone_dnssec above:
	// at most one per distribution, and the import id is literally the
	// parent's own id, verbatim (terraform import
	// aws_cloudfront_monitoring_subscription.example E3QYSUHO4VYRGB).
	// Concrete whenever aws_cloudfront_distribution above is, through its
	// own marker.
	TypeIdentity{
		Type:          "aws_cloudfront_monitoring_subscription",
		Components:    []Component{attr("distribution_id")},
		ImportSyntax:  "DISTRIBUTIONID",
		IdentityAttrs: []string{"id"},
	},
)

func init() { registerCohortTable(identityTableRoute53Cloudfront) }
