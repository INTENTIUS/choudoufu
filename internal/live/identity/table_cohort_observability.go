// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableObservability is the observability cohort's slice of [DefaultTable]:
// the identity rows the observability ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableObservability = buildTable(
	// ---- Registry-ratified (#40, #44, #65): fifth batch, observability and
	// ---- eventing remainder (issue #65's ratification campaign). Same
	// ---- tools/row-gen pipeline as the earlier batches, cross-checked
	// ---- against live/import-grammar.json (the provider's documented
	// ---- Import sections, fetched at the pinned v6.59.0 tag) and, for
	// ---- several rows, against the provider's Argument Reference directly
	// ---- rather than accepted on the registry's word alone. Cohort estate:
	// ---- live/e2e/estates/observability.
	//
	// Amazon Managed Prometheus (AWS::APS::*, the "AMP" TF prefix
	// aws_prometheus_*) is deliberately out of scope: issue #68's concurrent
	// batch owns it, and this batch's own evidence-gathering pass over
	// row-gen's APS section stops at reading it, to avoid two agents
	// proposing the same admission.go/table.go rows at once. Amazon
	// Application Signals has no CloudFormation resource type in
	// live/mapping.json's roster at all (row-gen's service listing has no
	// ApplicationSignals section), so there is nothing here to ratify or
	// reject for it.
	//
	// CloudWatch: three corrections. row-gen filed
	// aws_cloudwatch_alarm_mute_rule and aws_cloudwatch_contributor_insight_rule
	// evidence-only because the registry's own primaryIdentifier for both is
	// a read-only Arn — but each provider doc disagrees, the same shape as
	// the messaging batch's aws_sns_topic_policy correction: both resources'
	// Argument Reference and Identity Schema require_for_import exactly one
	// argument already in configuration (name, rule_name respectively), and
	// the documented import command uses that value verbatim, not an ARN.
	TypeIdentity{
		Type:          "aws_cloudwatch_alarm_mute_rule",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_contributor_insight_rule",
		Components:    []Component{attr("rule_name")},
		ImportSyntax:  "RULE_NAME",
		IdentityAttrs: []string{"rule_name"},
	},
	// aws_cloudwatch_otel_enrichment: row-gen proposed server-assigned via
	// the registry's AccountId. The provider's own Argument Reference has no
	// required arguments at all (region is its only, optional, argument),
	// and its Import section's example ID is the region string alone
	// ("us-west-2") — a per-region account singleton, the same shape as
	// aws_ecr_registry_policy and aws_api_gateway_account above, neither of
	// which any configuration argument identifies.
	serverAssigned("aws_cloudwatch_otel_enrichment",
		"the OTel enrichment setting is a singleton per AWS region: its identity is the region the run is against, which pre-exists the resource and is never supplied by a configuration argument — the resource has no required arguments at all. Confirmed against the provider's own Import section, whose example ID is the region string alone.",
		"REGION"),

	// Logs. aws_cloudwatch_log_group is already admitted above (client-
	// assigned identity, path 1) and is not repeated here.
	//
	// Four property-children row-gen folds onto an already-ratified Logs
	// parent (aws_cloudwatch_log_data_protection_policy and
	// aws_cloudwatch_log_index_policy onto LogGroup,
	// aws_cloudwatch_log_delivery_destination_policy onto
	// DeliveryDestination, aws_cloudwatch_log_destination_policy onto
	// Destination) are deferred for the same reason the ApiGateway batch's
	// method/response children were: admitting a property-child needs a
	// parent-derived admission mechanism this table does not have yet. Their
	// identity work is not the blocker — only the mechanism is.
	serverAssigned("aws_cloudwatch_log_anomaly_detector",
		"the Logs service assigns the anomaly detector's ARN at create time; no argument reconstructs it. Confirmed against the provider's own Identity Schema (required: arn) — the row-gen proposal here already matched the provider's documented behaviour with no correction needed.",
		"ARN"),
	serverAssigned("aws_cloudwatch_log_delivery",
		"the Logs service assigns the delivery's own opaque id at create time; delivery_source_name and delivery_destination_arn describe what it connects, not what comes back. Confirmed against the provider's own Identity Schema (required: id) and its Import section's example (a short opaque token, not an ARN).",
		"ID"),
	TypeIdentity{
		Type:          "aws_cloudwatch_log_delivery_destination",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_log_delivery_source",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_log_destination",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_log_transformer",
		Components:    []Component{attr("log_group_arn")},
		ImportSyntax:  "LOG_GROUP_ARN",
		IdentityAttrs: []string{"log_group_arn"},
	},
	serverAssigned("aws_cloudwatch_query_definition",
		"the Logs service assigns the query definition's own UUID at create time; the query's name and query_string describe it but do not identify it. Confirmed against the provider's own Identity Schema (required: query_definition_id) — the row-gen proposal here already matched the provider's documented behaviour with no correction needed.",
		"QUERY_DEFINITION_ID"),
	// Three of row-gen's four Logs "needs hand separator" rows resolve
	// cleanly against live/import-grammar.json's scraped Import sections,
	// each confirmed required in the provider's own Argument Reference
	// (issue #65's own note that most needs-hand-separator rows are now
	// resolvable this way): aws_cloudwatch_log_metric_filter and
	// aws_cloudwatch_log_stream both join log_group_name and name with a
	// colon; aws_cloudwatch_log_subscription_filter joins the same two
	// arguments with a pipe instead — confirmed by fetching its docs
	// directly, not assumed from the other two's separator. Neither type's
	// own id attribute is asserted to equal the joined string (unconfirmed
	// against provider source), so IdentityAttrs stays nil, the same
	// caution aws_route's own composite above takes.
	TypeIdentity{
		Type: "aws_cloudwatch_log_metric_filter",
		Components: []Component{
			attr("log_group_name"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "LOG_GROUP_NAME:NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_cloudwatch_log_stream",
		Components: []Component{
			attr("log_group_name"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "LOG_GROUP_NAME:NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_cloudwatch_log_subscription_filter",
		Components: []Component{
			attr("log_group_name"),
			sep("|"),
			attr("name"),
		},
		ImportSyntax:  "LOG_GROUP_NAME|NAME",
		IdentityAttrs: nil,
	},
	// The fourth: aws_cloudwatch_log_account_policy. Its two composed
	// arguments (policy_name, policy_type) are both required in the
	// provider's own Argument Reference, and its Import section's separator
	// is a colon, confirmed the same way as the three above.
	TypeIdentity{
		Type: "aws_cloudwatch_log_account_policy",
		Components: []Component{
			attr("policy_name"),
			sep(":"),
			attr("policy_type"),
		},
		ImportSyntax:  "POLICY_NAME:POLICY_TYPE",
		IdentityAttrs: nil,
	},
	// aws_cloudwatch_log_resource_policy: row-gen proposed client-named via
	// policy_name alone, reading only the registry's primaryIdentifier. The
	// provider's real Argument Reference says both policy_name and
	// resource_arn are individually optional but "exactly one ... must be
	// specified" — an account-scoped policy imports by policy_name verbatim,
	// a resource-scoped one by resource_arn verbatim, never both. This is
	// the same mutually-exclusive-alternatives shape aws_route's own
	// destination component already carries (attr's first-present-wins
	// reads whichever of several differently-named arguments the
	// configuration actually set), not a new mechanism: a config that sets
	// neither fails to resolve rather than guessing, the honest half-
	// coverage aws_route53_record's set_identifier caveat also accepts.
	TypeIdentity{
		Type:          "aws_cloudwatch_log_resource_policy",
		Components:    []Component{attr("policy_name", "resource_arn")},
		ImportSyntax:  "POLICY_NAME | RESOURCE_ARN (exactly one)",
		IdentityAttrs: nil,
	},

	// EventBridge/Events. aws_cloudwatch_event_rule was already rejected by
	// the messaging batch (needs a literal-default component this table
	// does not have, for event_bus_name silently defaulting to "default")
	// and is not repeated here. Its property-child,
	// aws_cloudwatch_event_target, folds onto it and is deferred with the
	// rest of this batch's property-children, for the same missing-
	// mechanism reason.
	TypeIdentity{
		Type:          "aws_cloudwatch_event_api_destination",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_event_archive",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_event_bus",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_event_connection",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_event_endpoint",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_cloudwatch_event_permission and aws_cloudwatch_event_bus_policy
	// are documented synonyms — both map to the one CloudFormation resource
	// AWS::Events::EventBusPolicy, an allowlisted mapping-gen contradiction
	// (tools/mapping-gen/mapping_gen_test.go's Former2Contradictions
	// allowlist: "aws_cloudwatch_event_bus_policy manages a whole EventBridge
	// bus policy document; aws_cloudwatch_event_permission manages one
	// statement in it") — but they do not share a ratification verdict.
	// row-gen filed both needs-hand-separator over the registry's composite
	// [EventBusName, StatementId]. The provider's real Identity Schema
	// disagrees for each, in opposite directions:
	//
	//   - aws_cloudwatch_event_permission's Identity Schema requires only
	//     statement_id (event_bus_name is not part of it at all, only an
	//     optional account_id/region), and statement_id is a required
	//     configuration argument. Single-component, resolves concrete
	//     whenever the resource does. Ratified below, correcting row-gen's
	//     classification the same way aws_xray_sampling_rule below does.
	//   - aws_cloudwatch_event_bus_policy's Identity Schema requires
	//     event_bus_name, but the provider's own Argument Reference marks
	//     event_bus_name (Optional) — "if you omit this, the permissions are
	//     set on the default event bus" — so a real, valid configuration may
	//     have no argument for this component to read at all. This is
	//     exactly live/survey-full.json's own "needs-config-signal"
	//     classification for this type (identity attrs settable but not
	//     required arguments), and the same missing-literal-default gap the
	//     messaging batch already declined for aws_cloudwatch_event_rule.
	//     Rejected, not deferred: the gap is a table mechanism this batch
	//     does not build, not missing evidence.
	TypeIdentity{
		Type:          "aws_cloudwatch_event_permission",
		Components:    []Component{attr("statement_id")},
		ImportSyntax:  "STATEMENT_ID",
		IdentityAttrs: []string{"statement_id"},
	},

	// Step Functions remainder: aws_sfn_activity only, per this batch's own
	// scope (aws_sfn_state_machine is already admitted above;
	// aws_sfn_alias is a state-machine alias, out of this batch's named
	// scope and left for a future one).
	serverAssigned("aws_sfn_activity",
		"Step Functions assigns the activity's own ARN at create time; the name argument is client-chosen but the provider's documented import command uses the ARN, which wraps it in an account and a region the configuration does not carry — the same shape as aws_sfn_state_machine above.",
		"ARN"),

	// X-Ray.
	serverAssigned("aws_xray_group",
		"X-Ray assigns the group's own ARN at create time; group_name is a required argument but the provider's documented import command uses the ARN, not the name. Confirmed against the provider's own Import section.",
		"ARN"),
	TypeIdentity{
		Type:          "aws_xray_resource_policy",
		Components:    []Component{attr("policy_name")},
		ImportSyntax:  "POLICY_NAME",
		IdentityAttrs: []string{"policy_name"},
	},
	// aws_xray_sampling_rule: row-gen filed this evidence-only, reading only
	// the registry's read-only RuleARN. The provider's real Argument
	// Reference requires rule_name (already in configuration) and its
	// Import section's example ID is the rule name verbatim ("example-rule"),
	// the same registry-vs-provider mismatch the CloudWatch corrections
	// above share.
	TypeIdentity{
		Type:          "aws_xray_sampling_rule",
		Components:    []Component{attr("rule_name")},
		ImportSyntax:  "RULE_NAME",
		IdentityAttrs: []string{"rule_name"},
	},

	// Grafana. row-gen's proposal already matches the provider: no
	// argument in aws_grafana_workspace's schema is required (name is
	// optional and is not the identity even when set), and the provider
	// mints an opaque "g-..." workspace id at create time, confirmed
	// against the Import section's example.
	serverAssigned("aws_grafana_workspace",
		"Grafana assigns the workspace's own id (g-...) at create time; every argument, including the optional name, describes the workspace but does not identify it.",
		"ID"),

	// RUM.
	TypeIdentity{
		Type:          "aws_rum_app_monitor",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	// Synthetics. aws_synthetics_group_association folds onto
	// AWS::Synthetics::Group and is deferred with this batch's other
	// property-children.
	TypeIdentity{
		Type:          "aws_synthetics_canary",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_synthetics_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
)

func init() { registerCohortTable(identityTableObservability) }
