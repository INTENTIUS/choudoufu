// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesObservability is the observability cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesObservability = map[string]typeOverride{
	// Observability and eventing remainder batch (issue #65). Every
	// argument below is Optional in the wire schema (so the generic
	// required-only pass leaves it unset, or leaves a bare "placeholder"
	// that fails an enum/ARN-format/length check the schema itself does not
	// carry), or is a nested block the schema marks optional while the
	// provider requires its contents in practice - the same two failure
	// shapes issue #56 already named for the earlier cohorts above.
	// #175 reversal batch, 2026-08-15. The rule and its target pair by
	// matching seed literals (the assembled-identity pairing shape the
	// 55856b4473 ruling recorded): event_rule's own IdentityAttrs is nil -
	// the id-alias inference issue #44 leaves to a human was not made - so
	// no identity-bound reference from the target's "rule" argument to the
	// rule's "name" attribute is sanctioned, and both sides carry the same
	// literal instead. event_bus_name stays omitted on both on purpose:
	// the row's Component.Default supplies the documented "default" bus,
	// and a committed fixture exercising the fallback is the round-trip
	// proof the vocabulary needs.
	"aws_cloudwatch_event_rule": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (#136's cohort/type-fix rule: a bare "name" argument is never treated as a same-named sibling's parent); kept set to its own literal so the target below can pair with it by matching seed literal. event_pattern carries the doc example's own value by hand because a type override displaces the doc-example seeding, and the provider requires one of event_pattern/schedule_expression (validate: "one of event_pattern,schedule_expression must be specified"), a constraint the wire schema does not express`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-observability-cohort-event-rule"`))
			body.SetAttributeRaw("event_pattern", exprTokens(`"{\"detail-type\":[\"AWS Console Sign In via CloudTrail\"]}"`))
		},
	},
	"aws_cloudwatch_event_target": {
		Reasons: []string{
			`"rule" pairs with aws_cloudwatch_event_rule's name by matching seed literal (see the batch comment above); arn is validated as a well-formed ARN (validate: "arn" (placeholder) is an invalid ARN) and no admitted type in this cohort is a valid EventBridge target, so it stays a literal placeholder ARN naming an SNS topic - the same "no real sibling to reference" shape aws_pipes_pipe's target accepts in the streaming cohort`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule", exprTokens(`"tofu-observability-cohort-event-rule"`))
			body.SetAttributeRaw("arn", exprTokens(`"arn:aws:sns:us-east-1:123456789012:tofu-observability-cohort-event-target"`))
		},
	},
	"aws_cloudwatch_alarm_mute_rule": {
		Reasons: []string{
			`rule is a required argument typed as a nested block with MinItems 0 in the wire schema, so the generic required-only pass never renders one at all - not caught by "terraform validate" (which only checks the arguments a block actually has), only surfaced applying against floci (apply: "missing required field, PutAlarmMuteRuleInput.Rule"). Its own nested schedule block is likewise required in practice.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			rule := body.AppendNewBlock("rule", nil)
			schedule := rule.Body().AppendNewBlock("schedule", nil)
			schedule.Body().SetAttributeRaw("duration", exprTokens(`"PT4H"`))
			schedule.Body().SetAttributeRaw("expression", exprTokens(`"cron(0 2 * * ? *)"`))
		},
	},
	"aws_cloudwatch_contributor_insight_rule": {
		Reasons: []string{
			`rule_definition is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "A string value was provided that is not valid JSON string format"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule_definition", exprTokens(fmt.Sprintf(`jsonencode({
    Schema = {
      Name    = "CloudWatchLogRule"
      Version = 1
    }
    LogGroupNames = ["tofu-%s-cohort-insight-source"]
    LogFormat     = "JSON"
    Contribution = {
      Keys = ["$.ip"]
    }
    AggregateOn = "Count"
  })`, g.cohort)))
		},
	},
	"aws_cloudwatch_event_api_destination": {
		Reasons: []string{
			`connection_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); http_method is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected http_method to be one of [...]"). connection_arn references this same cohort's aws_cloudwatch_event_connection.app.arn - an unknown value at validate time, which the ARN-format check never runs against - rather than a literal placeholder.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if conn, ok := g.byType["aws_cloudwatch_event_connection"]; ok {
				body.SetAttributeRaw("connection_arn", exprTokens(fmt.Sprintf("%s.arn", conn)))
			} else {
				body.SetAttributeRaw("connection_arn", exprTokens(fmt.Sprintf(
					`"arn:aws:events:us-east-1:000000000000:connection/tofu-%s-cohort/00000000-0000-0000-0000-000000000000"`, g.cohort)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"POST"`))
		},
	},
	"aws_cloudwatch_event_archive": {
		Reasons: []string{
			`name is length-limited to 48 characters (validate: "expected length of name to be in the range (1 - 48)"), and this cohort's own name ("observability") makes the generic tofu-<cohort>-cohort-<type> placeholder 51 characters - shortened here to a value that still names the cohort and the type. event_source_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); it references this same cohort's aws_cloudwatch_event_bus.app.arn - an unknown value at validate time, which the ARN-format check never runs against - rather than a literal placeholder.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-obs-event-archive"`))
			if bus, ok := g.byType["aws_cloudwatch_event_bus"]; ok {
				body.SetAttributeRaw("event_source_arn", exprTokens(fmt.Sprintf("%s.arn", bus)))
			} else {
				body.SetAttributeRaw("event_source_arn", exprTokens(fmt.Sprintf(
					`"arn:aws:events:us-east-1:000000000000:event-bus/tofu-%s-cohort-bus"`, g.cohort)))
			}
		},
	},
	"aws_cloudwatch_event_connection": {
		Reasons: []string{
			`authorization_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected authorization_type to be one of [...]"); auth_parameters is a required block, but the provider requires exactly one of its api_key/basic/oauth children set in practice (validate: "Invalid combination of arguments" x3 on an empty auth_parameters), and the chosen child's own key/value pair is itself required.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authorization_type", exprTokens(`"API_KEY"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "auth_parameters" {
					apiKey := blk.Body().AppendNewBlock("api_key", nil)
					apiKey.Body().SetAttributeRaw("key", exprTokens(`"x-api-key"`))
					apiKey.Body().SetAttributeRaw("value", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-api-key-value"`, g.cohort)))
				}
			}
		},
	},
	"aws_cloudwatch_event_endpoint": {
		Reasons: []string{
			`event_bus is a required block appearing exactly twice in the schema (a global endpoint always names a primary and a secondary event bus), and each child's event_bus_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN" x2); the generic pass's placeholder string is neither. The first bus references this same cohort's aws_cloudwatch_event_bus.app.arn; the second is a literal placeholder in a different region, since a global endpoint's two buses are documented as living in different regions. routing_config.failover_config's primary.health_check and secondary.route are both required in practice - not caught by "terraform validate" (which only checks the arguments a block actually has, and the generic pass rendered both primary and secondary as empty blocks), only surfaced applying against floci (apply: "missing required field, CreateEndpointInput.RoutingConfig.FailoverConfig.Primary" and "...Secondary").`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			primary := fmt.Sprintf(`"arn:aws:events:us-west-2:000000000000:event-bus/tofu-%s-cohort-secondary"`, g.cohort)
			firstExpr := primary
			if bus, ok := g.byType["aws_cloudwatch_event_bus"]; ok {
				firstExpr = fmt.Sprintf("%s.arn", bus)
			}
			i := 0
			for _, blk := range body.Blocks() {
				if blk.Type() != "event_bus" {
					continue
				}
				if i == 0 {
					blk.Body().SetAttributeRaw("event_bus_arn", exprTokens(firstExpr))
				} else {
					blk.Body().SetAttributeRaw("event_bus_arn", exprTokens(primary))
				}
				i++
			}
			for _, blk := range body.Blocks() {
				if blk.Type() != "routing_config" {
					continue
				}
				for _, fc := range blk.Body().Blocks() {
					if fc.Type() != "failover_config" {
						continue
					}
					for _, leg := range fc.Body().Blocks() {
						switch leg.Type() {
						case "primary":
							leg.Body().SetAttributeRaw("health_check", exprTokens(
								`"arn:aws:route53:::healthcheck/00000000-0000-0000-0000-000000000000"`))
						case "secondary":
							leg.Body().SetAttributeRaw("route", exprTokens(`"us-west-2"`))
						}
					}
				}
			}
		},
	},
	"aws_cloudwatch_event_permission": {
		Reasons: []string{
			`principal is a required string the schema does not constrain, but the provider validates it is "*" or a 12-digit AWS account ID (validate: "\"principal\" must be * or a 12 digit AWS account ID"); the generic placeholder string is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("principal", exprTokens(`"*"`))
		},
	},
	"aws_cloudwatch_log_account_policy": {
		Reasons: []string{
			`policy_document is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "contains an invalid JSON"); policy_type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected policy_type to be one of [...]"); the generic placeholder string satisfies neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_document", exprTokens(fmt.Sprintf(`jsonencode({
    DestinationArn = "arn:aws:lambda:us-east-1:000000000000:function:tofu-%s-cohort-log-account-policy-target"
    FilterPattern  = ""
    Distribution   = "Random"
  })`, g.cohort)))
			body.SetAttributeRaw("policy_type", exprTokens(`"SUBSCRIPTION_FILTER_POLICY"`))
		},
	},
	"aws_cloudwatch_log_delivery": {
		Reasons: []string{
			`delivery_destination_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); it references this same cohort's aws_cloudwatch_log_delivery_destination.app.arn - an unknown value at validate time, which the ARN-format check never runs against - rather than a literal placeholder. delivery_source_name is likewise pointed at this same cohort's aws_cloudwatch_log_delivery_source.app.name instead of the generic pass's disconnected literal string, so the delivery names a source that actually exists in this estate.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if dest, ok := g.byType["aws_cloudwatch_log_delivery_destination"]; ok {
				body.SetAttributeRaw("delivery_destination_arn", exprTokens(fmt.Sprintf("%s.arn", dest)))
			} else {
				body.SetAttributeRaw("delivery_destination_arn", exprTokens(fmt.Sprintf(
					`"arn:aws:logs:us-east-1:000000000000:delivery-destination:tofu-%s-cohort-log-delivery-destination"`, g.cohort)))
			}
			if src, ok := g.byType["aws_cloudwatch_log_delivery_source"]; ok {
				body.SetAttributeRaw("delivery_source_name", exprTokens(fmt.Sprintf("%s.name", src)))
			}
		},
	},
	"aws_cloudwatch_log_delivery_destination": {
		Reasons: []string{
			`name is length-limited to 60 characters (validate: "Attribute name string length must be between 1 and 60"), and this cohort's own name ("observability") makes the generic tofu-<cohort>-cohort-<type> placeholder 61 characters - shortened here by one character's worth of margin. delivery_destination_configuration is Optional in the schema, but the provider requires it in practice unless delivery_destination_type is XRAY (validate: "delivery_destination_configuration is required when delivery_destination_type is not XRAY") - set to XRAY here rather than inventing a destination_resource_arn this cohort has no real destination for.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-obs-log-delivery-destination"`))
			body.SetAttributeRaw("delivery_destination_type", exprTokens(`"XRAY"`))
		},
	},
	"aws_cloudwatch_log_delivery_source": {
		Reasons: []string{
			`resource_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); the generic placeholder string is not. log_type is paired with resource_arn here as the provider's own documented CloudFront example (ACCESS_LOGS / a CloudFront distribution ARN) rather than left at the generic placeholder, since the two arguments name the same source in practice even though only the ARN shape is checked locally.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("log_type", exprTokens(`"ACCESS_LOGS"`))
			body.SetAttributeRaw("resource_arn", exprTokens(`"arn:aws:cloudfront::000000000000:distribution/EDFDVBD6EXAMPLE"`))
		},
	},
	"aws_cloudwatch_log_destination": {
		Reasons: []string{
			`role_arn and target_arn are both required strings the schema does not constrain, but the provider validates each is a well-formed ARN (validate: "is an invalid ARN" x2); the generic placeholder string is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-log-destination"`, g.cohort)))
			body.SetAttributeRaw("target_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:kinesis:us-east-1:000000000000:stream/tofu-%s-cohort-log-destination-target"`, g.cohort)))
		},
	},
	"aws_cloudwatch_log_resource_policy": {
		Reasons: []string{
			`policy_document is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_document", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "logs.amazonaws.com" }
      Action    = "logs:PutLogEvents"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_cloudwatch_log_subscription_filter": {
		Reasons: []string{
			`destination_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("destination_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:lambda:us-east-1:000000000000:function:tofu-%s-cohort-log-subscription-target"`, g.cohort)))
		},
	},
	"aws_cloudwatch_log_transformer": {
		Reasons: []string{
			`transformer_config is a required argument typed as a list of nested blocks with MinItems 0 in the wire schema, so the generic required-only pass never renders one at all (validate: "Block transformer_config must have a configuration value as the provider has marked it as required"), and the provider requires its first processor to be a parser - parse_json is the simplest one. log_group_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); this cohort admits no aws_cloudwatch_log_group of its own (that type ratifies elsewhere, in the client-named section above), so the ARN is a literal placeholder rather than a cross-reference.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("log_group_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:logs:us-east-1:000000000000:log-group:/tofu-%s-cohort-log-transformer-source"`, g.cohort)))
			tc := body.AppendNewBlock("transformer_config", nil)
			tc.Body().AppendNewBlock("parse_json", nil)
		},
	},
	"aws_grafana_workspace": {
		Reasons: []string{
			`account_access_type, authentication_providers and permission_type are each required strings (or a set of them) the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]" x3); the generic placeholder string matches none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("account_access_type", exprTokens(`"CURRENT_ACCOUNT"`))
			body.SetAttributeRaw("authentication_providers", exprTokens(`["AWS_SSO"]`))
			body.SetAttributeRaw("permission_type", exprTokens(`"SERVICE_MANAGED"`))
		},
	},
	"aws_rum_app_monitor": {
		Reasons: []string{
			`domain and domain_list are both Optional in the schema, so the generic pass sets neither, but the provider requires exactly one of them (validate: "one of domain,domain_list must be specified" x2)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("domain", exprTokens(fmt.Sprintf(`"tofu-%s-cohort.example.com"`, g.cohort)))
		},
	},
	"aws_xray_resource_policy": {
		Reasons: []string{
			`policy_document is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "A string value was provided that is not valid JSON string format"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_document", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "xray.amazonaws.com" }
      Action    = "xray:GetSamplingStatisticSummaries"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_xray_sampling_rule": {
		Reasons: []string{
			`rule_name is length-limited to 32 characters (validate: "expected length of rule_name to be in the range (1 - 32)"), and this cohort's own name ("observability") makes the generic tofu-<cohort>-cohort-<type> placeholder 46 characters - shortened here to a value that still names the cohort and the type. http_method is length-limited to 10 characters and the generic "placeholder" string is 11; priority must be in (1 - 9999) and version must be at least 1, but both are numeric arguments the generic required-only pass zero-values rather than infers a real member for; resource_arn has no local format check but "*" (match any resource) is the provider's own documented value for a rule with no specific resource.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule_name", exprTokens(`"tofu-obs-xray-rule"`))
			body.SetAttributeRaw("service_name", exprTokens(`"tofu-obs-xray-rule"`))
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("resource_arn", exprTokens(`"*"`))
			body.SetAttributeRaw("priority", exprTokens(`1000`))
			body.SetAttributeRaw("version", exprTokens(`1`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesObservability) }
