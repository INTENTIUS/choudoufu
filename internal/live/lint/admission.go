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
	// aws_iam_group: deferred by this IAM/ECR batch pending #54, which has
	// since generalized live/LIMITATIONS.md's "Untaggable types" derivation
	// past the curated 68 (see internal/live/identity/table.go for the
	// evidence, unchanged since this batch's own deferral note). Ratified
	// here by the ECS/EKS batch (#65), which lands the two #54-unblocked
	// deferrals alongside its own cohort rather than opening a two-type
	// cohort.
	"aws_iam_group": {},
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

	// ---- Registry-ratified (#40, #44): fourth batch, EC2 core (instances,
	// ---- EBS, ENI; issue #65's own next-batch suggestion). Same evidence
	// ---- source and verification standard as the three batches above; see
	// ---- internal/live/identity/table.go for the per-type evidence and for
	// ---- the row-gen proposals this batch rejected or left out of scope.
	// ---- Cohort estate: live/e2e/estates/ec2-core. aws_instance is this
	// ---- batch's headline type: the repo's long-standing canonical
	// ---- unadmitted example (live/e2e/limits/unadmitted-type,
	// ---- live/LIMITATIONS.md) swaps to aws_nat_gateway in the same change —
	// ---- see that fixture's own comment for why.
	"aws_instance":                         {},
	"aws_key_pair":                         {},
	"aws_placement_group":                  {},
	"aws_ec2_fleet":                        {},
	"aws_ec2_capacity_reservation":         {},
	"aws_ec2_host":                         {},
	"aws_network_interface":                {},
	"aws_network_interface_attachment":     {},
	"aws_network_interface_permission":     {},
	"aws_eip_association":                  {},
	"aws_volume_attachment":                {},
	"aws_spot_fleet_request":               {},
	"aws_ebs_snapshot_block_public_access": {},
	// ---- Registry-ratified (#40, #44): fourth batch, DynamoDB periphery
	// ---- and ElastiCache (issue #65). Same tools/row-gen pipeline as the
	// ---- three batches above, cross-checked against the AWS provider's
	// ---- documented import behaviour (its "Import" section, fetched from
	// ---- the provider's own website/docs/r/ source at the pinned v6.58.0
	// ---- tag) and, where row-gen's own registry evidence was too weak to
	// ---- paste, against live/import-grammar.json's scraped import
	// ---- grammar rows — see internal/live/identity/table.go for the
	// ---- per-type evidence and for the rows this batch rejected or
	// ---- deferred. Cohort estate: live/e2e/estates/dynamodb-elasticache.
	//
	// DynamoDB's row-gen section is nearly empty beyond the already-admitted
	// aws_dynamodb_table (6 types total; 4 are property-children folded
	// onto AWS::DynamoDB::Table with no pastable row of their own). Of
	// those, only aws_dynamodb_resource_policy's real shape is simple
	// enough to hand-verify within this batch's discipline; the other
	// three are composite import IDs this batch defers rather than
	// hand-guesses — see internal/live/identity/table.go. DynamoDB has no
	// separate "backup" resource type in the provider at all:
	// aws_dynamodb_table's own point_in_time_recovery block covers that
	// ground, not a standalone resource, so there was nothing here for a
	// backup row to be.
	"aws_dynamodb_global_table":    {},
	"aws_dynamodb_resource_policy": {},

	// ElastiCache: seven of row-gen's nine proposed/correctable types land
	// here — the six client-named singular resources (cluster, replication
	// group, serverless cache, subnet group, user, user group) plus the
	// parameter group, corrected from row-gen's evidence-only demotion.
	// aws_elasticache_global_replication_group is rejected outright (not a
	// row-gen misclassification — the identity genuinely cannot be
	// recovered, see table.go), and
	// aws_elasticache_user_group_association is deferred as a composite
	// this batch does not hand-write.
	"aws_elasticache_cluster":           {},
	"aws_elasticache_parameter_group":   {},
	"aws_elasticache_replication_group": {},
	"aws_elasticache_serverless_cache":  {},
	"aws_elasticache_subnet_group":      {},
	"aws_elasticache_user":              {},
	"aws_elasticache_user_group":        {},
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
	// ---- Registry-ratified (#40, #44): fourth batch, ECS and EKS (issue
	// ---- #65). Same tools/row-gen pipeline and verification standard as
	// ---- the batches above; see internal/live/identity/table.go for the
	// ---- per-type evidence and for the row-gen proposals this batch
	// ---- rejected. Cohort estate: live/e2e/estates/ecs-eks.
	"aws_ecs_cluster_capacity_providers": {},
	"aws_ecs_daemon":                     {},
	"aws_eks_access_entry":               {},
	"aws_eks_access_policy_association":  {},
	"aws_eks_addon":                      {},
	"aws_eks_capability":                 {},
	"aws_eks_cluster":                    {},
	"aws_eks_fargate_profile":            {},
	"aws_eks_node_group":                 {},
	// ---- Registry-ratified (#40, #44): fourth batch, storage (EFS, FSx,
	// ---- Backup, issue #65). Same tools/row-gen pipeline as the three
	// ---- batches above, cross-checked against live/import-grammar.json
	// ---- (the AWS provider's own documented Import sections, fetched at
	// ---- the pinned v6.58.0 tag) and, where that was ambiguous, against
	// ---- the provider's real GetProviderSchema response and its rendered
	// ---- website docs — see internal/live/identity/table.go for the
	// ---- per-type evidence, the three row-gen proposals this batch
	// ---- rejected, and live/e2e/estates/storage/README.md for the floci
	// ---- coverage this batch found (EFS and FSx are unserved by the
	// ---- pinned image). Cohort estate: live/e2e/estates/storage.
	//
	// aws_efs_file_system is issue #47's own worked example: the provider
	// ships it no native list resource in v6.58.0 at all (confirmed
	// against the real GetProviderSchema response's list_resource_schemas,
	// zero EFS/FSx/Backup entries among 183), so its enumeration now comes
	// from the Cloud Control fallback rather than the marker path's usual
	// native list — live/mapping.json maps it to AWS::EFS::FileSystem via
	// "name", and live/registry.json records that CFN type as listable
	// with no required input, so internal/live/registry.Roster's
	// EnumerationSource resolves it and internal/live/discovery/
	// cloudcontrol.go's scanTypeCloudControl is reached whenever a caller
	// supplies Request.CloudControl and Request.Roster. Today's production
	// path does not: internal/command/live_plan.go builds discovery.Request
	// with neither field set, so this fallback is proven at the discovery
	// package's own test tier (cloudcontrol_test.go's fake-server suite,
	// keyed on this exact type) but not yet reachable from a real run —
	// wiring the command layer to it is follow-on work, not this batch's.
	"aws_efs_access_point": {},
	"aws_efs_file_system":  {},

	// The FSx family: four Terraform resource types (lustre, ontap,
	// windows, openzfs) all map to CloudFormation's one generic
	// AWS::FSx::FileSystem via live/mapping.json's "alias" rows, and none
	// of the four has a name argument at all — every one imports by the
	// server-assigned id alone. AWS::FSx::FileSystem is not listable in
	// live/registry.json (handlers.list is false), so unlike
	// aws_efs_file_system above, no enumeration mechanism reaches these
	// four today, Cloud Control included — see
	// live/e2e/estates/storage/README.md.
	"aws_fsx_lustre_file_system":  {},
	"aws_fsx_ontap_file_system":   {},
	"aws_fsx_windows_file_system": {},
	"aws_fsx_openzfs_file_system": {},
	// The child types one level under a file system: a storage virtual
	// machine (ONTAP only), a volume (ONTAP and OpenZFS share
	// AWS::FSx::Volume, same fold as the file systems above), a snapshot
	// (OpenZFS only), and a data repository association (Lustre only).
	// All four import by a server-assigned id none of their create-only
	// arguments reconstructs.
	"aws_fsx_ontap_storage_virtual_machine": {},
	"aws_fsx_ontap_volume":                  {},
	"aws_fsx_openzfs_volume":                {},
	"aws_fsx_openzfs_snapshot":              {},
	"aws_fsx_data_repository_association":   {},
	// The one client-named FSx type this batch found: an S3 access point
	// attachment imports by its own name argument, verbatim.
	"aws_fsx_s3_access_point_attachment": {},

	// Backup. aws_backup_plan is genuinely server-assigned — its id is a
	// system-generated plan identifier distinct from the display name, per
	// the provider's own Attribute Reference ("The id of the backup
	// plan."). The other five ratified here are all client-named via
	// "name", three of them (framework, report plan, logically air-gapped
	// vault) corrections of a row-gen proposal that read the CloudFormation
	// registry's opaque, read-only Arn/Id field as the identity when the
	// provider's own documented Import section says plainly that its id
	// "corresponds to the name" — see table.go for the per-type
	// cross-check and the two rejections (aws_backup_selection,
	// aws_backup_restore_testing_selection) this batch declined to ratify.
	"aws_backup_plan":                       {},
	"aws_backup_vault":                      {},
	"aws_backup_framework":                  {},
	"aws_backup_report_plan":                {},
	"aws_backup_restore_testing_plan":       {},
	"aws_backup_logically_air_gapped_vault": {},
	// ---- Registry-ratified (#40, #44): fourth batch, data plane (Kinesis,
	// ---- KinesisFirehose, Glue, Athena; issue #65's recipe). Same
	// ---- tools/row-gen pipeline as the earlier batches, cross-checked
	// ---- against the AWS provider's documented import behaviour and, for
	// ---- several rows, against the pinned provider's own wire schema (read
	// ---- with `terraform providers schema -json`) rather than accepted on
	// ---- the registry's word alone — see internal/live/identity/table.go
	// ---- for the per-type evidence and for the row-gen proposals this
	// ---- batch corrected, rejected or left out of scope. Cohort estate:
	// ---- live/e2e/estates/data.
	"aws_kinesis_stream":                        {},
	"aws_kinesis_stream_consumer":               {},
	"aws_kinesis_firehose_delivery_stream":      {},
	"aws_glue_catalog_database":                 {},
	"aws_glue_catalog_table":                    {},
	"aws_glue_registry":                         {},
	"aws_glue_job":                              {},
	"aws_glue_crawler":                          {},
	"aws_glue_connection":                       {},
	"aws_glue_classifier":                       {},
	"aws_glue_data_catalog_encryption_settings": {},
	"aws_glue_trigger":                          {},
	"aws_glue_ml_transform":                     {},
	"aws_athena_workgroup":                      {},
	"aws_athena_data_catalog":                   {},
	// ---- Registry-ratified (#40, #44, #65): fourth batch, Route53
	// ---- remainder and CloudFront. Same tools/row-gen pipeline as the
	// ---- earlier batches, cross-checked against the AWS provider's
	// ---- documented import behaviour (its own Argument/Attribute/Import
	// ---- sections, fetched from the pinned v6.58.0 tag) and, where
	// ---- row-gen's registry-only evidence was silent on recoverability,
	// ---- against live/tag-verbs.json (which AWS API each service's
	// ---- generic tagging operation actually covers) and
	// ---- live/survey-full.json's mechanical per-type signals — not
	// ---- accepted on row-gen's classification alone. See
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- for the rejected and deferred proposals. Cohort estate:
	// ---- live/e2e/estates/route53-cloudfront.
	"aws_route53_health_check":                             {},
	"aws_route53_hosted_zone_dnssec":                       {},
	"aws_route53_key_signing_key":                          {},
	"aws_route53_zone_association":                         {},
	"aws_route53profiles_association":                      {},
	"aws_route53profiles_profile":                          {},
	"aws_route53recoverycontrolconfig_cluster":             {},
	"aws_route53recoverycontrolconfig_control_panel":       {},
	"aws_route53recoverycontrolconfig_safety_rule":         {},
	"aws_route53_resolver_endpoint":                        {},
	"aws_route53_resolver_firewall_domain_list":            {},
	"aws_route53_resolver_firewall_rule":                   {},
	"aws_route53_resolver_firewall_rule_group":             {},
	"aws_route53_resolver_firewall_rule_group_association": {},
	"aws_route53_resolver_query_log_config":                {},
	"aws_route53_resolver_rule":                            {},
	"aws_route53_resolver_rule_association":                {},
	"aws_cloudfront_anycast_ip_list":                       {},
	"aws_cloudfront_connection_function":                   {},
	"aws_cloudfront_connection_group":                      {},
	"aws_cloudfront_distribution":                          {},
	"aws_cloudfront_distribution_tenant":                   {},
	"aws_cloudfront_function":                              {},
	"aws_cloudfront_key_value_store":                       {},
	"aws_cloudfront_monitoring_subscription":               {},
	"aws_cloudfront_multitenant_distribution":              {},
	"aws_cloudfront_origin_access_control":                 {},
	"aws_cloudfront_realtime_log_config":                   {},
	"aws_cloudfront_trust_store":                           {},
	"aws_cloudfront_vpc_origin":                            {},

	// ---- Registry-ratified (#40, #44, #65): fifth batch, compute
	// ---- platforms (Batch, EMR remainder, App Runner, Elastic
	// ---- Beanstalk, Amplify, Lightsail). Same tools/row-gen pipeline as
	// ---- the earlier batches, cross-checked against the AWS provider's
	// ---- documented import behaviour (its own Argument/Attribute/Import
	// ---- sections, fetched from the pinned v6.58.0 tag) and, where
	// ---- row-gen's registry-only evidence was silent on recoverability
	// ---- or wrong about the argument, against live/tag-verbs.json,
	// ---- live/survey-full.json's mechanical per-type signals, and
	// ---- live/import-grammar.json's docs-derived evidence — not
	// ---- accepted on row-gen's classification alone. Two reclassified
	// ---- rows (aws_batch_job_definition, aws_amplify_app) and one
	// ---- corrected wrong guess (aws_elastic_beanstalk_environment) are
	// ---- the notable catches; see internal/live/identity/table.go for
	// ---- the per-type evidence and for the rejected and deferred
	// ---- proposals. Cohort estate: live/e2e/estates/compute-platforms.
	"aws_batch_compute_environment":                    {},
	"aws_batch_job_definition":                         {},
	"aws_batch_job_queue":                              {},
	"aws_batch_scheduling_policy":                      {},
	"aws_emr_cluster":                                  {},
	"aws_emr_security_configuration":                   {},
	"aws_emr_studio":                                   {},
	"aws_emrcontainers_virtual_cluster":                {},
	"aws_emrserverless_application":                    {},
	"aws_apprunner_auto_scaling_configuration_version": {},
	"aws_apprunner_observability_configuration":        {},
	"aws_apprunner_service":                            {},
	"aws_apprunner_vpc_connector":                      {},
	"aws_apprunner_vpc_ingress_connection":             {},
	"aws_elastic_beanstalk_application":                {},
	"aws_elastic_beanstalk_environment":                {},
	"aws_amplify_app":                                  {},
	"aws_amplify_branch":                               {},
	"aws_lightsail_bucket":                             {},
	"aws_lightsail_certificate":                        {},
	"aws_lightsail_container_service":                  {},
	"aws_lightsail_database":                           {},
	"aws_lightsail_disk":                               {},
	"aws_lightsail_distribution":                       {},
	"aws_lightsail_instance":                           {},
	"aws_lightsail_lb":                                 {},
	"aws_lightsail_lb_certificate":                     {},
	"aws_lightsail_static_ip":                          {},
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
