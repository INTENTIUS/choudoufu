// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// admittedTypesV0 is the stateless v0 admission table: the provider-local
// resource type names that may appear in a configuration planned without
// authoritative state.
//
// A type belongs here only if its identity is recoverable from the live system
// with no memory, by one of the four admission paths described in the
// internal/live package documentation. The v0 contents are deliberately
// small: exactly the types used by the estate fixture (live/e2e/estate), including the ones
// that are there only to support a coverage row rather than to be one.
//
// The table is hardcoded and grows in two steps, neither of which is a v0
// concern:
//
//   - The provider survey in the design doc (AWS provider, 2026-08: 65 of the
//     top 68 types admitted) is the source for the next batch. Adding a type
//     means naming which of the four admission paths recovers its identity.
//   - The provider identity schemas from opentofu#2854 are plumbed through as
//     of #45: [admitted] falls back to [identity.SynthesizeTypeIdentity] for
//     a type this table does not cover, when the caller has schemas to offer
//     it. A type whose identity schema is fully client-assigned or fully
//     parent-derived admits itself that way and needs no row here at all,
//     which is why this table should shrink as the survey's schema-derivable
//     types are pulled out of it, not grow forever.
//
// Keyed by provider-local type name (the first label of a resource block), not
// by fully-qualified provider address, because that is what a configuration
// author writes and what the error message has to name back to them.
var admittedTypesV0 = map[string]struct{}{
	// Marker path: identity is a server-assigned ID, recovered by a
	// tag-filtered list against the tofu-estate marker.
	"aws_vpc":              {},
	"aws_subnet":           {},
	"aws_security_group":   {},
	"aws_route_table":      {},
	"aws_internet_gateway": {},
	// First slice of the survey's marker cohort (#20). Both are taggable,
	// both have a native list resource in the provider, and both round-trip
	// their tags through that list against the floci emulator, which is what
	// the marker path actually needs — an import ID constructible from
	// configuration is not required here, because discovery finds these by
	// their tags.
	"aws_kms_key":      {},
	"aws_route53_zone": {},
	// The ELBv2 chain, second slice of #20. All three are taggable, all
	// three are listable, and all three import by an ARN that ELBv2 mints —
	// the load balancer's and the target group's names are client-chosen and
	// are not their identities, and a listener has no name at all. The
	// listener sequences after the load balancer because it is created
	// against one.
	"aws_lb":              {},
	"aws_lb_target_group": {},
	"aws_lb_listener":     {},
	// Third slice of the survey's marker cohort (#20), probed end to end
	// against floci through the provider before wiring. All six are
	// taggable, all six are listable through the provider's list protocol
	// — the three EC2 types with a server-side tag filter, the ACM and
	// Step Functions pair by region with client-side filtering — and none
	// has an import ID constructible from configuration: EC2 mints the
	// rule, template and volume IDs, and ACM and Step Functions mint ARNs.
	// The per-rule security group resources are one resource per rule,
	// which is what makes each rule individually ownable. Three of the
	// survey's marker rows were probed and did not make this slice:
	// aws_api_gateway_rest_api creates but floci serves no status for it,
	// so the provider's availability waiter dies (blocked-emulator);
	// aws_nat_gateway imports but the provider reads subnet_id out of the
	// NatGatewayAddresses list, which floci returns empty, so every plan
	// proposes replacement (blocked-emulator); and aws_efs_file_system has
	// no list resource in provider v6.58.0 at all, so marker discovery
	// could never enumerate it.
	"aws_vpc_security_group_ingress_rule": {},
	"aws_vpc_security_group_egress_rule":  {},
	"aws_launch_template":                 {},
	"aws_acm_certificate":                 {},
	"aws_sfn_state_machine":               {},
	"aws_ebs_volume":                      {},

	// Parent-derived: identity is a composite key over already-admitted
	// parents.
	"aws_route":                      {},
	"aws_route_table_association":    {},
	"aws_iam_role_policy_attachment": {},
	// aws_route53_record (#19's second slice): the survey classes it
	// client-named, and its name and type are, but the third component of
	// its import identity (ZONEID_NAME_TYPE) is the parent zone's
	// server-assigned Z-ID, so the fork wires it as a composite through the
	// aws_route53_zone marker — flag F5 in live/SURVEY.md, resolved by
	// #20 wiring the zone. Verified against the provider's identity schema
	// (required import attributes: name, type, zone_id) and against floci.
	"aws_route53_record": {},
	// #21's parent-derived slice: the attachment's identity is the target
	// group's live ARN joined with the target and the port. Untaggable, so
	// it is not swept for removal — see live/LIMITATIONS.md.
	"aws_lb_target_group_attachment": {},

	// Client-assigned identity: the name is already in the configuration.
	"aws_s3_bucket":            {},
	"aws_s3_bucket_policy":     {},
	"aws_iam_role":             {},
	"aws_cloudwatch_log_group": {},
	// aws_ssm_parameter: the receipt demo (PE.3, live/RECEIPTS.md). Its
	// name argument is client-named the same way a bucket's or a role's is,
	// so it admits through path 1 like the rest of this section.
	"aws_ssm_parameter": {},
	// First slice of the survey's client-named cohort (#19): both import by
	// the name argument alone, verified against the provider's documented
	// import grammar and against the floci emulator.
	"aws_dynamodb_table": {},
	"aws_ecs_cluster":    {},
	// Second slice of the client-named cohort (#19): the four S3 bucket
	// children. Each is a named singleton child of its bucket — at most one
	// per bucket, imported by the bucket argument alone, the same shape as
	// aws_s3_bucket_policy — verified against the provider's identity
	// schemas (required import attribute: bucket) and against the floci
	// emulator.
	"aws_s3_bucket_versioning":                           {},
	"aws_s3_bucket_public_access_block":                  {},
	"aws_s3_bucket_server_side_encryption_configuration": {},
	"aws_s3_bucket_lifecycle_configuration":              {},
	// Same slice (#19): an inline role policy imports by ROLENAME:POLICYNAME,
	// both halves client-chosen strings already in configuration (the same
	// concrete-composite shape as aws_iam_role_policy_attachment), and a KMS
	// alias imports by its full alias/... name argument — the alias is the
	// client-named handle on the marker-discovered key. Both verified
	// against the provider's identity schemas and against floci.
	"aws_iam_role_policy": {},
	"aws_kms_alias":       {},
	// Same slice (#19): a metric alarm imports by its alarm_name argument
	// alone, verified against the provider's identity schema (required
	// import attribute: alarm_name) and against floci, whose monitoring
	// surface round-trips the marker tags through create/read/list.
	"aws_cloudwatch_metric_alarm": {},
	// Client-named with an account-derived import identity (the survey's
	// flag F2): the topic's ARN is built from the name in configuration plus
	// the account and region of the cloud the run is against. A run that
	// knows those computes the identity; one that does not finds the topic
	// by its tags like any marker type. Either way the name in the block is
	// what the estate is written around. See internal/live/identity's
	// CloudContext. aws_sqs_queue is the same shape; see the messaging
	// batch below, which ratifies it despite a floci gap (choudoufu#26).
	"aws_sns_topic": {},

	// List plus content match, as a fungible set bound by tofu-slot marker.
	"aws_eip": {},

	// ---- Registry-ratified (#40, #44): identity evidence comes from the
	// ---- CloudFormation Registry (live/registry.json) via
	// ---- tools/row-gen, joined against live/mapping.json, rather than
	// ---- from the provider's own identity schema. Each row below was
	// ---- proposed by row-gen and independently checked against the AWS
	// ---- provider's documented import behaviour before landing here — see
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- for the two row-gen proposals this batch rejected. Cohort
	// ---- estate: live/e2e/estates/lambda (#48's per-cohort mechanism).
	// First Lambda batch (8 row-gen proposals; 1 needs-hand-separator
	// skipped per #44's non-goals, 2 rejected — see table.go).
	"aws_lambda_capacity_provider":    {},
	"aws_lambda_code_signing_config":  {},
	"aws_lambda_event_source_mapping": {},
	"aws_lambda_function":             {},
	"aws_lambda_layer_version":        {},

	// ---- Registry-ratified (#40, #44): second batch, IAM and ECR
	// ---- (issue #26). Same evidence source and verification standard as
	// ---- the first Lambda batch above; see internal/live/identity/table.go
	// ---- for the per-type evidence and for the row-gen proposals this
	// ---- batch rejected or deferred. Cohort estate:
	// ---- live/e2e/estates/iam-ecr.
	// tools/row-gen proposed 13 pastable rows across the two services
	// (plus evidence-only and needs-hand-separator rows this batch never
	// touches); 7 ratified here, 5 rejected, 1 deferred — see table.go.
	// #26's two named types, aws_ecr_repository and aws_iam_user, are both
	// in this batch.
	"aws_ecr_registry_policy":                 {},
	"aws_ecr_registry_scanning_configuration": {},
	"aws_ecr_replication_configuration":       {},
	"aws_ecr_repository":                      {},
	"aws_iam_instance_profile":                {},
	"aws_iam_service_linked_role":             {},
	"aws_iam_user":                            {},
	// ---- Registry-ratified (#40, #44): second batch, messaging (SQS, SNS
	// ---- beyond the already-admitted aws_sns_topic, CloudWatch, and
	// ---- EventBridge/Events). Same tools/row-gen pipeline as the Lambda
	// ---- batch above (9 row-gen proposals in scope; 2 rejected and 1
	// ---- deferred on independent verification — see
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- live/e2e/estates/messaging/README.md for why
	// ---- aws_sns_topic_subscription is deferred rather than landing here
	// ---- despite classifying cleanly). Cohort estate:
	// ---- live/e2e/estates/messaging.
	"aws_cloudwatch_composite_alarm": {},
	"aws_cloudwatch_dashboard":       {},
	"aws_cloudwatch_metric_stream":   {},
	"aws_sns_topic_policy":           {},
	// aws_sqs_queue ratifies on paper: its identity is the same
	// account-derived shape as aws_sns_topic above, so the "aws_sqs_queue
	// is the same shape and is not here" sentence a few lines up is now
	// stale prose, not current fact — it is kept out no longer. What kept
	// it out was never the identity, only a floci gap (choudoufu#26: floci
	// reports a queue's URL as its own endpoint, and the AWS provider's
	// importer parses only the amazonaws.com form). See
	// live/e2e/estates/messaging/README.md for the emulator caveat.
	"aws_sqs_queue":        {},
	"aws_sqs_queue_policy": {},

	// ---- Registry-ratified (#40, #44): fourth batch, RDS (issue #65's
	// ---- ratification campaign). Same tools/row-gen pipeline as the
	// ---- earlier batches (18 row-gen proposals in the RDS service section;
	// ---- 17 ratified, 1 rejected — see internal/live/identity/table.go for
	// ---- the per-type evidence, including five corrections where row-gen's
	// ---- own classification undersold a real, documented import grammar
	// ---- the same way the messaging batch's aws_sns_topic_policy correction
	// ---- did). aws_db_instance keeps SURVEY.md's own recorded wrinkle: the
	// ---- survey filed it under marker (taggable, no identity schema in
	// ---- v6.58.0), but its documented import ID is the client-chosen
	// ---- "identifier" argument, so it wires client-named here rather than
	// ---- through a marker, per live/SURVEY.md's own note that "a wiring
	// ---- batch that reaches RDS should expect to admit it by name." Cohort
	// ---- estate: live/e2e/estates/rds.
	"aws_db_event_subscription":         {},
	"aws_db_instance":                   {},
	"aws_db_instance_role_association":  {},
	"aws_db_option_group":               {},
	"aws_db_parameter_group":            {},
	"aws_db_proxy":                      {},
	"aws_db_proxy_default_target_group": {},
	"aws_db_proxy_endpoint":             {},
	"aws_db_subnet_group":               {},
	"aws_rds_cluster":                   {},
	"aws_rds_cluster_instance":          {},
	"aws_rds_cluster_parameter_group":   {},
	"aws_rds_cluster_role_association":  {},
	"aws_rds_custom_db_engine_version":  {},
	"aws_rds_global_cluster":            {},
	"aws_rds_integration":               {},
	"aws_rds_shard_group":               {},
	// ---- Registry-ratified (#40, #44): fourth batch, API Gateway v1 and v2
	// ---- (issue #65). Same tools/row-gen pipeline as the earlier batches,
	// ---- cross-checked against live/import-grammar.json (the pinned
	// ---- v6.58.0 provider docs) and, for several composites, against the
	// ---- provider's Argument Reference and source directly — row-gen's own
	// ---- "needs hand separator" output only says a primaryIdentifier has
	// ---- more than one part, not whether every part is a configuration
	// ---- argument, and several of API Gateway's are not. See
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- rejections, and live/e2e/estates/apigateway/README.md for the
	// ---- floci verification (including a provider crash reading
	// ---- aws_api_gateway_api_key and the re-confirmed aws_api_gateway_rest_api
	// ---- availability-waiter gap). 25 ApiGateway and 13 ApiGatewayV2 types
	// ---- were in row-gen's scope; 16 and 5 respectively ratify here.
	// ---- Cohort estate: live/e2e/estates/apigateway.
	"aws_api_gateway_account":                        {},
	"aws_api_gateway_api_key":                        {},
	"aws_api_gateway_base_path_mapping":              {},
	"aws_api_gateway_client_certificate":             {},
	"aws_api_gateway_documentation_version":          {},
	"aws_api_gateway_domain_name":                    {},
	"aws_api_gateway_domain_name_access_association": {},
	"aws_api_gateway_gateway_response":               {},
	"aws_api_gateway_method":                         {},
	"aws_api_gateway_model":                          {},
	"aws_api_gateway_rest_api":                       {},
	"aws_api_gateway_rest_api_policy":                {},
	"aws_api_gateway_stage":                          {},
	"aws_api_gateway_usage_plan":                     {},
	"aws_api_gateway_usage_plan_key":                 {},
	"aws_api_gateway_vpc_link":                       {},
	"aws_apigatewayv2_api":                           {},
	"aws_apigatewayv2_domain_name":                   {},
	"aws_apigatewayv2_routing_rule":                  {},
	"aws_apigatewayv2_stage":                         {},
	"aws_apigatewayv2_vpc_link":                      {},
}

// admitted reports whether the given provider-local resource type may appear
// in a stateless configuration: first by the v0 hand table, and - only when
// the caller supplied provider schemas - by whatever
// [identity.SynthesizeTypeIdentity] can derive from those schemas and the
// configuration's own naming signal.
//
// The table lookup runs first and unconditionally, so a type the table
// already covers never depends on schemas being present at all. The
// fallback only ever admits a type the table refuses; it never revokes one
// the table already grants. That asymmetry is the whole point: a caller
// with no schemas gets exactly the table's answer, and a caller with
// schemas gets the table's answer plus whatever the schemas additionally
// justify, never less.
func admitted(resourceType string, schemas map[string]providers.Schema, signal *identity.ConfigSignal) bool {
	if _, ok := admittedTypesV0[resourceType]; ok {
		return true
	}
	if len(schemas) == 0 {
		return false
	}
	_, ok := identity.SynthesizeTypeIdentity(resourceType, schemas, signal)
	return ok
}

// logicalTypePrefixes are the provider-local type prefixes whose resources
// exist only inside the state file. Their value is generated once and then
// remembered; the record of them IS the store that stateless mode removes, so
// there is nothing to recover them from and no version of them that works
// without authoritative state. See live/LIMITATIONS.md.
//
// Checked before the admission table so that a random_id gets the explanation
// for why its whole family is out rather than the generic "not in the v0
// table" message.
var logicalTypePrefixes = []string{
	"random_",
	"tls_",
	"time_",
	"null_",
	"local_",
}

// logicalType reports whether the given provider-local resource type is a
// logical, store-only type, and returns the prefix that matched.
func logicalType(resourceType string) (string, bool) {
	for _, prefix := range logicalTypePrefixes {
		if strings.HasPrefix(resourceType, prefix) {
			return prefix, true
		}
	}
	return "", false
}
