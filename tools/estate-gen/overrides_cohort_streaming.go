// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesStreaming is the streaming cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesStreaming = map[string]typeOverride{
	// Streaming and app integration batch (issue #65). Five of this
	// cohort's types are server-assigned in internal/live/identity/table.go
	// (no Components at all), so the generic pass's identityArgName never
	// fires for their own "name" argument, which is itself a plain Required
	// string in the real provider schema even though the type's *identity*
	// is server-assigned. Before #136's cohort/type-fix rule, the generic
	// pass's same-name parent search left all five pointing their "name" at
	// whichever other resource in this run happened to render first with
	// its own "name" Required too (aws_appflow_connector_profile,
	// alphabetically first) - the identical failure shape aws_ecs_daemon's
	// own override above already names for the same root cause. That
	// collision no longer happens (gen.go's parentRef never treats a bare
	// "name" argument as a same-named sibling's parent, self-owned or not);
	// each entry below now keeps its own literal name for an independent
	// reason - a length or charset limit, or consistency with its
	// siblings - stated per entry.
	// #175 ratification batch, 2026-08-15: two of the four types this
	// batch added to the cohort carry arguments the provider validates as
	// well-formed ARNs at plan time, a constraint the wire schema does not
	// express (the same class as aws_pipes_pipe's role_arn/source/target
	// below).
	"aws_appsync_domain_name": {
		Reasons: []string{
			`certificate_arn is Required and validated as a well-formed ARN (validate: "certificate_arn" (placeholder) is an invalid ARN: arn: invalid prefix); no admitted ACM type exists in this cohort to reference, so it stays a literal placeholder ARN naming an ACM certificate - the same "no real sibling to reference" shape aws_pipes_pipe's target accepts below`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("certificate_arn", exprTokens(`"arn:aws:acm:us-east-1:123456789012:certificate/12345678-1234-1234-1234-123456789012"`))
		},
	},
	"aws_msk_scram_secret_association": {
		Reasons: []string{
			`cluster_arn and every secret_arn_list member are validated as well-formed ARNs (validate: "cluster_arn" ... is an invalid ARN: arn: invalid prefix / "secret_arn_list.0" (placeholder) is an invalid ARN). cluster_arn is wired to this cohort's own aws_msk_cluster - the association's identity IS that cluster's server-assigned ARN, the identity-bound reference shape the table's IdentityAttrs sanction - and secret_arn_list stays a literal placeholder ARN naming a Secrets Manager secret with the AmazonMSK_ prefix the service requires of associated secrets, since no admitted Secrets Manager type exists in this cohort to reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			clusterExpr := `"arn:aws:kafka:us-east-1:123456789012:cluster/tofu-streaming-cohort/12345678-1234-1234-1234-123456789012-1"`
			if cluster, ok := g.byType["aws_msk_cluster"]; ok {
				clusterExpr = fmt.Sprintf("%s.arn", cluster)
			}
			body.SetAttributeRaw("cluster_arn", exprTokens(clusterExpr))
			body.SetAttributeRaw("secret_arn_list", exprTokens(`["arn:aws:secretsmanager:us-east-1:123456789012:secret:AmazonMSK_tofu-streaming-cohort-AbCdEf"]`))
		},
	},
	"aws_msk_configuration": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (#136's cohort/type-fix rule); kept set to its own literal, matching its siblings. server_properties is Required but the generic placeholder string is not a real Kafka broker properties file, which the provider does not validate at plan time but does need at apply time (confirmed by hand against floci during this batch's verification)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-msk-configuration"`, g.cohort)))
			body.SetAttributeRaw("server_properties", exprTokens(`"auto.create.topics.enable=true"`))
		},
	},
	"aws_mskconnect_connector": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision aws_appsync_graphql_api used to describe (#136's cohort/type-fix rule); kept set to its own literal, matching its siblings. capacity is a required block whose two nested block_types (autoscaling, provisioned_capacity) are both themselves Optional in the schema, but the provider requires exactly one (validate: "Missing required argument" once floci is asked to create it); kafka_cluster.apache_kafka_cluster.bootstrap_servers is Required but the generic placeholder string is not the bootstrap-broker-list format the provider expects, so it is wired to this cohort's own aws_msk_cluster's bootstrap_brokers output; kafka_cluster_client_authentication.authentication_type and kafka_cluster_encryption_in_transit.encryption_type are both Optional in the schema but their empty blocks leave the provider to guess, set here to their documented defaults for clarity; plugin.custom_plugin.arn/revision are Required but the generic placeholder string and zero are neither a real plugin ARN nor a real revision, wired to this cohort's own aws_mskconnect_custom_plugin instead; kafkaconnect_version is Required and the provider validates it against the versions MSK Connect actually supports, not an arbitrary string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-mskconnect-connector"`, g.cohort)))
			body.SetAttributeRaw("kafkaconnect_version", exprTokens(`"2.7.1"`))
			body.SetAttributeRaw("connector_configuration", exprTokens(`{
    "connector.class" = "org.apache.kafka.connect.mirror.MirrorSourceConnector"
    "tasks.max"        = "1"
    "topics"           = "example"
  }`))
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "capacity":
					pc := blk.Body().AppendNewBlock("provisioned_capacity", nil)
					pc.Body().SetAttributeRaw("worker_count", exprTokens(`1`))
				case "kafka_cluster":
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "apache_kafka_cluster" {
							bootstrapExpr := `"placeholder"`
							if cluster, ok := g.byType["aws_msk_cluster"]; ok {
								bootstrapExpr = fmt.Sprintf("%s.bootstrap_brokers", cluster)
							}
							inner.Body().SetAttributeRaw("bootstrap_servers", exprTokens(bootstrapExpr))
						}
					}
				case "kafka_cluster_client_authentication":
					blk.Body().SetAttributeRaw("authentication_type", exprTokens(`"NONE"`))
				case "kafka_cluster_encryption_in_transit":
					blk.Body().SetAttributeRaw("encryption_type", exprTokens(`"PLAINTEXT"`))
				case "plugin":
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "custom_plugin" {
							arnExpr := `"placeholder"`
							revExpr := `0`
							if plugin, ok := g.byType["aws_mskconnect_custom_plugin"]; ok {
								arnExpr = fmt.Sprintf("%s.arn", plugin)
								revExpr = fmt.Sprintf("%s.latest_revision", plugin)
							}
							inner.Body().SetAttributeRaw("arn", exprTokens(arnExpr))
							inner.Body().SetAttributeRaw("revision", exprTokens(revExpr))
						}
					}
				}
			}
		},
	},
	"aws_mskconnect_custom_plugin": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision aws_appsync_graphql_api used to describe (#136's cohort/type-fix rule); kept set to its own literal, matching its siblings. content_type is Required and the provider validates it against a fixed enum (validate: "expected content_type to be one of [JAR ZIP]"), and the generic placeholder string is not a member; location.s3.bucket_arn is Required and validated as a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-mskconnect-custom-plugin"`, g.cohort)))
			body.SetAttributeRaw("content_type", exprTokens(`"JAR"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "location" {
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "s3" {
							inner.Body().SetAttributeRaw("bucket_arn", exprTokens(fmt.Sprintf(
								`"arn:aws:s3:::tofu-%s-cohort-plugins"`, g.cohort)))
						}
					}
				}
			}
		},
	},
	"aws_mq_broker": {
		Reasons: []string{
			`engine_type, engine_version and host_instance_type are all Required strings the provider validates against real ActiveMQ/RabbitMQ values, not an arbitrary placeholder (engine_type: validate "expected engine_type to be one of [ACTIVEMQ RABBITMQ]"); user is Optional-shaped in the schema (a set with no min_items) but the provider requires at least one broker user in practice (found only by exercising a create against floci during this batch's verification, not by validate). engine_type is RABBITMQ rather than the more common ActiveMQ: floci's own AmazonMQ emulation refuses ACTIVEMQ outright ("BadRequestException: Only RABBITMQ EngineType is supported"), confirmed by hand against floci during this batch's verification - both engine types are equally real and valid against the actual AWS API, so this is a floci-emulator accommodation, not a correctness compromise`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine_type", exprTokens(`"RABBITMQ"`))
			body.SetAttributeRaw("engine_version", exprTokens(`"3.13"`))
			body.SetAttributeRaw("host_instance_type", exprTokens(`"mq.t3.micro"`))
			user := body.AppendNewBlock("user", nil)
			user.Body().SetAttributeRaw("username", exprTokens(`"tofuadmin"`))
			user.Body().SetAttributeRaw("password", exprTokens(fmt.Sprintf(`"Tofu%sCohortPw1!"`, g.cohort)))
		},
	},
	"aws_mq_configuration": {
		Reasons: []string{
			`"name" no longer needs a fix for the accidental cross-type collision aws_appsync_graphql_api used to describe (#136's cohort/type-fix rule: this type is client-named in the identity table by a composite, not a single "name" argument, so parentRef's old fallback used to treat any same-named sibling as fair game regardless of admission path); kept set to its own literal, matching its siblings. engine_type and engine_version are Required strings validated against real ActiveMQ/RabbitMQ values, the same shape as aws_mq_broker above; data is Required and must be a well-formed broker configuration document (XML for ActiveMQ), not an arbitrary placeholder string - not caught by validate, found by exercising a create against floci during this batch's verification`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-mq-configuration"`, g.cohort)))
			body.SetAttributeRaw("engine_type", exprTokens(`"ACTIVEMQ"`))
			body.SetAttributeRaw("engine_version", exprTokens(`"5.18"`))
			body.SetAttributeRaw("data", exprTokens(`<<DATA
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<broker xmlns="http://activemq.apache.org/schema/core">
  <plugins>
  </plugins>
</broker>
DATA
`))
		},
	},
	"aws_msk_cluster": {
		Reasons: []string{
			`kafka_version and number_of_broker_nodes are both Required, and the generic pass's zero-value/placeholder defaults (0, "placeholder") are neither a real Kafka version nor a legal broker count (must be at least 1 and a multiple of the number of client_subnets); broker_node_group_info.instance_type is Required and the provider validates it is a real MSK-supported instance type, not an arbitrary string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("kafka_version", exprTokens(`"3.6.0"`))
			body.SetAttributeRaw("number_of_broker_nodes", exprTokens(`1`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "broker_node_group_info" {
					blk.Body().SetAttributeRaw("instance_type", exprTokens(`"kafka.m5.large"`))
				}
			}
		},
	},
	"aws_msk_serverless_cluster": {
		Reasons: []string{
			`client_authentication.sasl.iam.enabled is Required and schema-valid as either bool, but MSK Serverless accepts only IAM-authenticated SASL in practice (the generic pass's zero-value "false" passes validate but the provider refuses it at create - confirmed by hand against floci during this batch's verification); vpc_config.subnet_ids is Required and schema-valid with the generic pass's single-element placeholder list, but MSK Serverless requires subnets in at least two distinct Availability Zones in practice, the same not-caught-by-validate shape`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "client_authentication":
					for _, sasl := range blk.Body().Blocks() {
						if sasl.Type() == "sasl" {
							for _, iam := range sasl.Body().Blocks() {
								if iam.Type() == "iam" {
									iam.Body().SetAttributeRaw("enabled", exprTokens(`true`))
								}
							}
						}
					}
				case "vpc_config":
					blk.Body().SetAttributeRaw("subnet_ids", exprTokens(`["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]`))
				}
			}
		},
	},
	"aws_appflow_connector_profile": {
		Reasons: []string{
			`connection_mode and connector_type are both Required strings validated against fixed enums (validate: "expected connection_mode to be one of [Public Private]", "expected connector_type to be one of [...]"); connector_profile_config.connector_profile_credentials and .connector_profile_properties are both required blocks (min_items 1) but every field inside their per-connector-type oneof sub-blocks is itself optional in the schema, so the generic pass renders both empty - the provider needs one real connector-type sub-block filled in, chosen here as CustomConnector/APIKEY, the connector type needing the fewest required fields of the 24 the enum offers`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("connection_mode", exprTokens(`"Public"`))
			body.SetAttributeRaw("connector_type", exprTokens(`"CustomConnector"`))
			for _, blk := range body.Blocks() {
				if blk.Type() != "connector_profile_config" {
					continue
				}
				for _, inner := range blk.Body().Blocks() {
					switch inner.Type() {
					case "connector_profile_credentials":
						cc := inner.Body().AppendNewBlock("custom_connector", nil)
						cc.Body().SetAttributeRaw("authentication_type", exprTokens(`"APIKEY"`))
						ak := cc.Body().AppendNewBlock("api_key", nil)
						ak.Body().SetAttributeRaw("api_key", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-appflow-api-key"`, g.cohort)))
					case "connector_profile_properties":
						inner.Body().AppendNewBlock("custom_connector", nil)
					}
				}
			}
		},
	},
	"aws_appflow_flow": {
		Reasons: []string{
			`destination_flow_config.connector_type and source_flow_config.connector_type are both Required strings validated against the same fixed enum as aws_appflow_connector_profile above; task.task_type is Required and validated against its own fixed enum (validate: "expected task_type to be one of [...]"); trigger_config.trigger_type is Required and validated against a fixed enum too. Chosen connector type is S3 rather than aws_appflow_connector_profile's CustomConnector: S3 flows need no connector_profile_name at all (Optional in the schema, and AppFlow's own S3 connector is IAM-based, not credential-based), the simplest of the connector types this cohort's sibling resource does not have to match`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "destination_flow_config":
					blk.Body().SetAttributeRaw("connector_type", exprTokens(`"S3"`))
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "destination_connector_properties" {
							s3 := inner.Body().AppendNewBlock("s3", nil)
							s3.Body().SetAttributeRaw("bucket_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-appflow-dest"`, g.cohort)))
						}
					}
				case "source_flow_config":
					blk.Body().SetAttributeRaw("connector_type", exprTokens(`"S3"`))
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "source_connector_properties" {
							s3 := inner.Body().AppendNewBlock("s3", nil)
							s3.Body().SetAttributeRaw("bucket_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-appflow-source"`, g.cohort)))
							s3.Body().SetAttributeRaw("bucket_prefix", exprTokens(`"data"`))
						}
					}
				case "task":
					blk.Body().SetAttributeRaw("task_type", exprTokens(`"Passthrough"`))
				case "trigger_config":
					blk.Body().SetAttributeRaw("trigger_type", exprTokens(`"OnDemand"`))
				}
			}
		},
	},
	"aws_pipes_pipe": {
		Reasons: []string{
			`role_arn, source and target are all Required and validated as well-formed ARNs (validate: "is an invalid ARN"), and source is additionally validated against a pattern requiring either an "smk://" bootstrap string or a real ARN (validate: "expected value of source to match regular expression ..."); the generic placeholder string satisfies none of the three. source is wired to this cohort's own aws_msk_cluster (a Managed Streaming Kafka source pipe reads from), with the source_parameters.managed_streaming_kafka_parameters.topic_name the MSK source type requires in practice (Optional in the schema, not caught by validate); target stays a literal placeholder ARN naming an SQS queue - no admitted type in this cohort is a valid Pipes target, the same "no real sibling to reference" shape aws_rds_integration's own override above accepts for its target_arn`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
			sourceExpr := fmt.Sprintf(`"arn:aws:kafka:us-east-1:000000000000:cluster/tofu-%s-cohort-msk-cluster/placeholder"`, g.cohort)
			if cluster, ok := g.byType["aws_msk_cluster"]; ok {
				sourceExpr = cluster.String() + ".arn"
			}
			body.SetAttributeRaw("source", exprTokens(sourceExpr))
			body.SetAttributeRaw("target", exprTokens(fmt.Sprintf(
				`"arn:aws:sqs:us-east-1:000000000000:tofu-%s-cohort-pipes-target"`, g.cohort)))
			sp := body.AppendNewBlock("source_parameters", nil)
			mskp := sp.Body().AppendNewBlock("managed_streaming_kafka_parameters", nil)
			mskp.Body().SetAttributeRaw("topic_name", exprTokens(`"example"`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesStreaming) }
