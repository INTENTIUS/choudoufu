// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesStreaming is the streaming cohort's slice of [admittedTypesV0]:
// the types the streaming ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesStreaming = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): fifth batch, streaming and
	// ---- app integration (MQ, MSK plus its KafkaConnect service-alias,
	// ---- AppFlow, one AppSync type, EventBridge Pipes, and Scheduler's
	// ---- schedule group). Same tools/row-gen pipeline as the batches
	// ---- above, cross-checked against the AWS provider's documented
	// ---- Argument/Attribute/Import sections and, where the pinned
	// ---- v6.59.0 release ships one, its own ResourceIdentitySchema
	// ---- (live/survey-full.json), not accepted on the registry's
	// ---- classification alone. Six of row-gen's proposals in this
	// ---- batch's scope are rejected on independent verification
	// ---- (aws_appsync_api, aws_appsync_api_cache, aws_appsync_api_key,
	// ---- aws_appsync_domain_name_api_association, aws_appsync_function,
	// ---- aws_scheduler_schedule) — see internal/live/identity/table.go
	// ---- for the per-type evidence and live/e2e/estates/streaming/README.md
	// ---- for the full account, including why SWF (registry-absent; a
	// ---- prior family sweep found zero AWS::SWF::* types anywhere in
	// ---- live/registry.json) never entered scope at all. Cohort estate:
	// ---- live/e2e/estates/streaming.
	"aws_mq_broker":                       {},
	"aws_mq_configuration":                {},
	"aws_msk_cluster":                     {},
	"aws_msk_configuration":               {},
	"aws_msk_serverless_cluster":          {},
	"aws_mskconnect_connector":            {},
	"aws_mskconnect_custom_plugin":        {},
	"aws_mskconnect_worker_configuration": {},
	"aws_appflow_connector_profile":       {},
	"aws_appflow_flow":                    {},
	"aws_appsync_graphql_api":             {},
	"aws_pipes_pipe":                      {},
	"aws_scheduler_schedule_group":        {},
}

func init() { registerCohortAdmitted(admittedTypesStreaming) }
