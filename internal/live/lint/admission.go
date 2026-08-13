// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
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

	// ---- Registry-ratified (#40, #44, #65): fifth batch, EC2 networking
	// ---- beyond the core (VPC endpoints, Transit Gateway, VPN, Client VPN,
	// ---- IPAM, prefix lists, VPC peering, DHCP options, network ACLs, flow
	// ---- logs, NAT gateway; issue #65's own next-batch suggestion). Same
	// ---- tools/row-gen pipeline and verification standard as the batches
	// ---- above, cross-checked against the AWS provider's documented import
	// ---- behaviour (its Argument Reference, Attribute Reference and Import
	// ---- section, fetched from the provider's own website/docs/r/ source at
	// ---- the pinned v6.58.0 tag) and against live/import-grammar.json's
	// ---- scraped evidence directly — see internal/live/identity/table.go
	// ---- for the per-type evidence and for the rows this batch rejected.
	// ---- Cohort estate: live/e2e/estates/ec2-networking.
	//
	// aws_nat_gateway is this batch's headline type: the repo's
	// long-standing canonical unadmitted-type example
	// (live/e2e/limits/unadmitted-type, live/LIMITATIONS.md) swaps to
	// aws_cloudwatch_event_rule in the same change — see that fixture's own
	// comment for why.
	"aws_vpc_endpoint":                                 {},
	"aws_vpc_endpoint_service":                         {},
	"aws_vpc_endpoint_policy":                          {},
	"aws_vpc_endpoint_private_dns":                     {},
	"aws_vpc_endpoint_route_table_association":         {},
	"aws_vpc_endpoint_subnet_association":              {},
	"aws_vpc_endpoint_security_group_association":      {},
	"aws_ec2_transit_gateway":                          {},
	"aws_ec2_transit_gateway_connect":                  {},
	"aws_ec2_transit_gateway_connect_peer":             {},
	"aws_ec2_transit_gateway_metering_policy":          {},
	"aws_ec2_transit_gateway_metering_policy_entry":    {},
	"aws_ec2_transit_gateway_multicast_domain":         {},
	"aws_ec2_transit_gateway_peering_attachment":       {},
	"aws_ec2_transit_gateway_policy_table":             {},
	"aws_ec2_transit_gateway_policy_table_association": {},
	"aws_ec2_transit_gateway_route":                    {},
	"aws_ec2_transit_gateway_route_table":              {},
	"aws_ec2_transit_gateway_route_table_association":  {},
	"aws_ec2_transit_gateway_route_table_propagation":  {},
	"aws_ec2_transit_gateway_vpc_attachment":           {},
	"aws_customer_gateway":                             {},
	"aws_vpn_connection":                               {},
	"aws_vpn_gateway":                                  {},
	"aws_ec2_client_vpn_endpoint":                      {},
	"aws_ec2_client_vpn_route":                         {},
	"aws_vpc_ipam":                                     {},
	"aws_vpc_ipam_pool":                                {},
	"aws_vpc_ipam_pool_cidr":                           {},
	"aws_vpc_ipam_resource_discovery":                  {},
	"aws_vpc_ipam_resource_discovery_association":      {},
	"aws_vpc_ipam_scope":                               {},
	"aws_ec2_managed_prefix_list":                      {},
	"aws_ec2_managed_prefix_list_entry":                {},
	"aws_vpc_peering_connection":                       {},
	"aws_vpc_dhcp_options":                             {},
	"aws_vpc_dhcp_options_association":                 {},
	"aws_network_acl":                                  {},
	"aws_network_acl_rule":                             {},
	"aws_flow_log":                                     {},
	"aws_nat_gateway":                                  {},
	"aws_nat_gateway_eip_association":                  {},
	// ---- Fold-children (issue #68): declared property-children of an
	// ---- admitted parent, admitted the same way live/mapping.json's other
	// ---- ~170 "fold" rows will be as this path picks them up in future
	// ---- batches. See internal/live/identity/table.go's own "Fold-children
	// ---- (issue #68)" section comment for the per-type evidence and the
	// ---- two sub-shapes (API Gateway's four duplicate an already-admitted
	// ---- parent's own composite identity; the APS three key on a single
	// ---- parent argument, the same named-singleton-child shape
	// ---- aws_s3_bucket_policy and aws_sns_topic_policy already ratify).
	// ---- Cohort estate: live/e2e/estates/apigateway (the API Gateway four)
	// ---- and live/e2e/estates/aps (the APS three plus their two new
	// ---- parents, aws_prometheus_workspace and aws_prometheus_scraper,
	// ---- neither previously admitted).
	"aws_api_gateway_integration":                  {},
	"aws_api_gateway_integration_response":         {},
	"aws_api_gateway_method_response":              {},
	"aws_api_gateway_method_settings":              {},
	"aws_prometheus_workspace":                     {},
	"aws_prometheus_scraper":                       {},
	"aws_prometheus_alert_manager_definition":      {},
	"aws_prometheus_query_logging_configuration":   {},
	"aws_prometheus_scraper_logging_configuration": {},

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

	// ---- Registry-ratified (#40, #44, #65): sixth batch, security and
	// ---- secrets (Secrets Manager, KMS remainder, SSM remainder, ACM-PCA,
	// ---- GuardDuty, Macie2, SecurityHub, Inspector2, WAFv2 — issue #65's
	// ---- ratification campaign). Same tools/row-gen pipeline and
	// ---- verification standard as the batches above, cross-checked against
	// ---- the AWS provider's documented import behaviour, live/survey-full.json's
	// ---- taggability signal (the real provider schema, not merely the
	// ---- CloudFormation Registry's own tagging claim — SecurityHub's legacy
	// ---- v1 types are where those two disagree, the "newer-API-generation
	// ---- false friend" the ram-servicecatalog sweep flagged) and a live
	// ---- floci probe. See internal/live/identity/table.go for the per-type
	// ---- evidence, the rejected proposals, and the credential-adjacent
	// ---- exclusions this batch calls out explicitly (extending
	// ---- opsExcluded's reasoning to aws_kms_grant and
	// ---- aws_kms_custom_key_store without touching that hand table, since
	// ---- both already resolve to "moves to Ops" on ordinary
	// ---- recoverability grounds). Cohort estate: live/e2e/estates/security.
	"aws_secretsmanager_secret":                        {},
	"aws_secretsmanager_secret_policy":                 {},
	"aws_secretsmanager_secret_rotation":               {},
	"aws_kms_external_key":                             {},
	"aws_kms_replica_key":                              {},
	"aws_ssm_association":                              {},
	"aws_ssm_maintenance_window":                       {},
	"aws_ssm_patch_baseline":                           {},
	"aws_ssm_patch_group":                              {},
	"aws_ssm_resource_data_sync":                       {},
	"aws_ssm_service_setting":                          {},
	"aws_acmpca_certificate_authority":                 {},
	"aws_acmpca_certificate_authority_certificate":     {},
	"aws_acmpca_policy":                                {},
	"aws_guardduty_detector":                           {},
	"aws_guardduty_filter":                             {},
	"aws_guardduty_ipset":                              {},
	"aws_guardduty_threatintelset":                     {},
	"aws_guardduty_malware_protection_plan":            {},
	"aws_guardduty_member":                             {},
	"aws_guardduty_publishing_destination":             {},
	"aws_guardduty_organization_admin_account":         {},
	"aws_guardduty_organization_configuration":         {},
	"aws_macie2_custom_data_identifier":                {},
	"aws_macie2_findings_filter":                       {},
	"aws_macie2_classification_job":                    {},
	"aws_macie2_member":                                {},
	"aws_macie2_organization_admin_account":            {},
	"aws_securityhub_account_v2":                       {},
	"aws_securityhub_aggregator_v2":                    {},
	"aws_securityhub_automation_rule":                  {},
	"aws_securityhub_automation_rule_v2":               {},
	"aws_securityhub_configuration_policy_association": {},
	"aws_securityhub_connector_v2":                     {},
	"aws_securityhub_organization_admin_account":       {},
	"aws_securityhub_standards_control":                {},
	"aws_securityhub_standards_control_association":    {},
	"aws_securityhub_member":                           {},
	"aws_inspector2_filter":                            {},
	"aws_inspector2_delegated_admin_account":           {},
	"aws_inspector2_member_association":                {},
	"aws_wafv2_ip_set":                                 {},
	"aws_wafv2_regex_pattern_set":                      {},
	"aws_wafv2_rule_group":                             {},
	"aws_wafv2_web_acl":                                {},
	"aws_wafv2_web_acl_rule":                           {},

	// ---- Registry-ratified (#40, #44, #65): seventh batch, developer tools
	// ---- (CodeArtifact, CodeBuild, CodeCommit, CodeConnections and its
	// ---- CodeStarConnections predecessor, CodeStarNotifications,
	// ---- CodeDeploy, CodePipeline, and the ECR-public leftover from the
	// ---- IAM/ECR batch's own ECR section). Same tools/row-gen pipeline as
	// ---- the batches above, cross-checked against the AWS provider's
	// ---- documented Argument/Attribute/Import sections fetched from the
	// ---- pinned v6.58.0 tag directly — several of these types' row-gen
	// ---- classification does not survive that check, including three
	// ---- CodeBuild types and CodeCommit whose CFN Registry ships every
	// ---- handler false, a corroboration gap row-gen's own schema-only
	// ---- evidence cannot see. See internal/live/identity/table.go for the
	// ---- per-type evidence, the one rejection, and the CodeGuru pair this
	// ---- batch left outside issue #65's named scope. Cohort estate:
	// ---- live/e2e/estates/devtools.
	"aws_codeartifact_domain":                        {},
	"aws_codeartifact_domain_permissions_policy":     {},
	"aws_codeartifact_repository":                    {},
	"aws_codeartifact_repository_permissions_policy": {},
	"aws_codebuild_fleet":                            {},
	"aws_codebuild_project":                          {},
	"aws_codebuild_report_group":                     {},
	"aws_codebuild_webhook":                          {},
	"aws_codecommit_repository":                      {},
	"aws_codeconnections_connection":                 {},
	"aws_codestarconnections_connection":             {},
	"aws_codestarnotifications_notification_rule":    {},
	"aws_codedeploy_app":                             {},
	"aws_codedeploy_deployment_config":               {},
	"aws_codedeploy_deployment_group":                {},
	"aws_codepipeline":                               {},
	"aws_codepipeline_custom_action_type":            {},
	"aws_codepipeline_webhook":                       {},
	"aws_ecrpublic_repository":                       {},
	"aws_ecrpublic_repository_policy":                {},

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
	// ---- Registry-ratified (#40, #44, #65): sixth batch, advanced
	// ---- networking (Network Firewall, NetworkManager/Cloud WAN, VPC
	// ---- Lattice, Global Accelerator, Route53 Recovery Readiness). Same
	// ---- tools/row-gen pipeline as the batches above, cross-checked
	// ---- against the AWS provider's documented Argument/Attribute/Import
	// ---- sections and, where the doc text alone left the schema argument
	// ---- names or the exact import-ID mechanics ambiguous, the pinned
	// ---- provider's own resource source (internal/service/... on the
	// ---- hashicorp/terraform-provider-aws repository). VPC Lattice is
	// ---- the notable catch: row-gen's flat serverAssigned() template
	// ---- read the CFN registry's primaryIdentifier field name ("Arn")
	// ---- for eleven of its fourteen types and proposed ARN-based
	// ---- identities for all of them, but the provider's own documented
	// ---- Import sections disagree for nine of the eleven — VPC Lattice
	// ---- imports almost its whole family by the short, provider-minted
	// ---- id (svc-…, sn-…, tg-…, rgw-…, rcfg-…, dv-…, snra-…, rft-…), not
	// ---- the arn attribute the same resources also export. See
	// ---- internal/live/identity/table.go for the per-type evidence, the
	// ---- NetworkManager composite identities resolved by hand past
	// ---- row-gen's own "needs hand separator" refusal, and the deferred
	// ---- App Mesh (deprecated service) and Cloud WAN (not a distinct CFN
	// ---- service; folded into NetworkManager's CoreNetwork family)
	// ---- scope notes. Cohort estate:
	// ---- live/e2e/estates/networking-advanced.
	"aws_networkfirewall_firewall":                              {},
	"aws_networkfirewall_firewall_policy":                       {},
	"aws_networkfirewall_logging_configuration":                 {},
	"aws_networkfirewall_rule_group":                            {},
	"aws_networkfirewall_tls_inspection_configuration":          {},
	"aws_networkfirewall_vpc_endpoint_association":              {},
	"aws_networkmanager_connect_attachment":                     {},
	"aws_networkmanager_connect_peer":                           {},
	"aws_networkmanager_core_network":                           {},
	"aws_networkmanager_customer_gateway_association":           {},
	"aws_networkmanager_device":                                 {},
	"aws_networkmanager_dx_gateway_attachment":                  {},
	"aws_networkmanager_global_network":                         {},
	"aws_networkmanager_link":                                   {},
	"aws_networkmanager_link_association":                       {},
	"aws_networkmanager_prefix_list_association":                {},
	"aws_networkmanager_site":                                   {},
	"aws_networkmanager_site_to_site_vpn_attachment":            {},
	"aws_networkmanager_transit_gateway_peering":                {},
	"aws_networkmanager_transit_gateway_registration":           {},
	"aws_networkmanager_transit_gateway_route_table_attachment": {},
	"aws_networkmanager_vpc_attachment":                         {},
	"aws_globalaccelerator_accelerator":                         {},
	"aws_globalaccelerator_cross_account_attachment":            {},
	"aws_globalaccelerator_endpoint_group":                      {},
	"aws_globalaccelerator_listener":                            {},
	"aws_vpclattice_access_log_subscription":                    {},
	"aws_vpclattice_auth_policy":                                {},
	"aws_vpclattice_domain_verification":                        {},
	"aws_vpclattice_listener":                                   {},
	"aws_vpclattice_listener_rule":                              {},
	"aws_vpclattice_resource_configuration":                     {},
	"aws_vpclattice_resource_gateway":                           {},
	"aws_vpclattice_resource_policy":                            {},
	"aws_vpclattice_service":                                    {},
	"aws_vpclattice_service_network":                            {},
	"aws_vpclattice_service_network_resource_association":       {},
	"aws_vpclattice_service_network_service_association":        {},
	"aws_vpclattice_service_network_vpc_association":            {},
	"aws_vpclattice_target_group":                               {},
	"aws_route53recoveryreadiness_cell":                         {},
	"aws_route53recoveryreadiness_readiness_check":              {},
	"aws_route53recoveryreadiness_recovery_group":               {},
	"aws_route53recoveryreadiness_resource_set":                 {},

	// ---- Registry-ratified (#40, #44, #65): fifth batch, identity
	// ---- (Cognito, IAM leftovers, SSO Admin; issue #65's ratification
	// ---- campaign). Same tools/row-gen pipeline and verification standard
	// ---- as the batches above, cross-checked against the AWS provider's
	// ---- documented import behaviour (its own Argument/Attribute/Import
	// ---- sections, fetched from the pinned v6.59.0 tag) rather than
	// ---- accepted on row-gen's classification alone — several rows below
	// ---- correct a row-gen "needs hand separator" or "evidence-only"
	// ---- verdict, the same way the route53-cloudfront and RDS batches
	// ---- did. Two row-gen proposals this batch does not re-litigate,
	// ---- aws_iam_saml_provider and aws_iam_virtual_mfa_device, were
	// ---- already rejected by the IAM/ECR batch above on ARN-embedding
	// ---- grounds; aws_iam_access_key is excluded the same way that
	// ---- batch excluded it, per SURVEY.md's standing credential rule. See
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- for every row this batch rejected or deferred. Cohort estate:
	// ---- live/e2e/estates/identity.
	"aws_cognito_identity_pool":                        {},
	"aws_cognito_identity_pool_provider_principal_tag": {},
	"aws_cognito_identity_pool_roles_attachment":       {},
	"aws_cognito_identity_provider":                    {},
	"aws_cognito_resource_server":                      {},
	"aws_cognito_user":                                 {},
	"aws_cognito_user_group":                           {},
	"aws_cognito_user_in_group":                        {},
	"aws_cognito_user_pool":                            {},
	"aws_cognito_user_pool_domain":                     {},
	"aws_iam_group_policy":                             {},
	"aws_iam_group_policy_attachment":                  {},
	"aws_iam_openid_connect_provider":                  {},
	"aws_iam_policy":                                   {},
	"aws_iam_server_certificate":                       {},
	"aws_iam_user_policy":                              {},
	"aws_iam_user_policy_attachment":                   {},
	"aws_ssoadmin_account_assignment":                  {},
	"aws_ssoadmin_application":                         {},
	"aws_ssoadmin_application_assignment":              {},
	"aws_ssoadmin_instance_access_control_attributes":  {},
	"aws_ssoadmin_permission_set":                      {},
	// ---- Registry-ratified (#40, #44, #65): fifth batch, observability and
	// ---- eventing remainder (CloudWatch, Logs, EventBridge/Events, Step
	// ---- Functions, X-Ray, Grafana, RUM, Synthetics; issue #65's
	// ---- ratification campaign). Same tools/row-gen pipeline as the earlier
	// ---- batches, cross-checked against live/import-grammar.json (the
	// ---- provider's own documented Import sections, fetched at the pinned
	// ---- v6.59.0 tag) and, for several corrections, against the provider's
	// ---- Argument Reference directly — see internal/live/identity/table.go
	// ---- for the per-type evidence and for the rows this batch rejected or
	// ---- left out of scope. Amazon Managed Prometheus (AWS::APS::*) is
	// ---- deliberately untouched here: it is issue #68's concurrent batch,
	// ---- and admitting any of its nine types from this batch too would be a
	// ---- straight collision on both this table and DefaultTable. Amazon
	// ---- Application Signals has no CloudFormation resource type in
	// ---- live/mapping.json's roster at all, so there is no row-gen evidence
	// ---- for it to ratify or reject. Cohort estate:
	// ---- live/e2e/estates/observability.
	"aws_cloudwatch_alarm_mute_rule":          {},
	"aws_cloudwatch_contributor_insight_rule": {},
	"aws_cloudwatch_otel_enrichment":          {},
	"aws_cloudwatch_log_account_policy":       {},
	"aws_cloudwatch_log_anomaly_detector":     {},
	"aws_cloudwatch_log_delivery":             {},
	"aws_cloudwatch_log_delivery_destination": {},
	"aws_cloudwatch_log_delivery_source":      {},
	"aws_cloudwatch_log_destination":          {},
	"aws_cloudwatch_log_metric_filter":        {},
	"aws_cloudwatch_log_resource_policy":      {},
	"aws_cloudwatch_log_stream":               {},
	"aws_cloudwatch_log_subscription_filter":  {},
	"aws_cloudwatch_log_transformer":          {},
	"aws_cloudwatch_query_definition":         {},
	"aws_cloudwatch_event_api_destination":    {},
	"aws_cloudwatch_event_archive":            {},
	"aws_cloudwatch_event_bus":                {},
	"aws_cloudwatch_event_connection":         {},
	"aws_cloudwatch_event_endpoint":           {},
	"aws_cloudwatch_event_permission":         {},
	"aws_sfn_activity":                        {},
	"aws_xray_group":                          {},
	"aws_xray_resource_policy":                {},
	"aws_xray_sampling_rule":                  {},
	"aws_grafana_workspace":                   {},
	"aws_rum_app_monitor":                     {},
	"aws_synthetics_canary":                   {},
	"aws_synthetics_group":                    {},
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
	// ---- Registry-ratified (#40, #44, #65): governance batch (Config
	// ---- remainder, Control Tower, License Manager, Organizations,
	// ---- Resource Explorer, Resource Groups, Service Catalog remainder
	// ---- plus AppRegistry, Audit Manager; issue #65's own next-batch
	// ---- list). Same tools/row-gen pipeline as the batches above,
	// ---- cross-checked against the AWS provider's documented
	// ---- Argument/Attribute/Import sections and against
	// ---- live/survey-full.json's real-schema taggable/list_resource
	// ---- signals, not accepted on the registry's classification alone -
	// ---- the second check caught five of row-gen's clean-looking
	// ---- proposals (aws_licensemanager_grant, the whole License Manager
	// ---- service; aws_organizations_organization; aws_servicecatalog_
	// ---- service_action and aws_servicecatalog_tag_option) whose
	// ---- server-assigned identity is real but whose live/survey-full.json
	// ---- signals are untaggable with no native list resource, which
	// ---- leaves none of this package's four admission paths
	// ---- (internal/live/doc.go) able to recover an existing instance - a
	// ---- clean import grammar is not the same claim as a working
	// ---- admission path. Three of row-gen's needs-hand-separator
	// ---- proposals in this batch's scope are corrected rather than
	// ---- deferred, the same way the GuardDuty filter and WAFv2 web ACL
	// ---- rule corrections read in earlier batches: aws_controltower_
	// ---- control and aws_servicecatalog_portfolio_share both have an
	// ---- unambiguous, documented separator and component arguments the
	// ---- registry's own composite primaryIdentifier either names outright
	// ---- or undercounts by one field; aws_servicecatalog_tag_option_
	// ---- resource_association's own composite is equally clean but stays
	// ---- unratified anyway because its own tag_option_id half names a
	// ---- type this batch just rejected. Two more siblings
	// ---- (aws_servicecatalog_principal_portfolio_association and
	// ---- aws_servicecatalog_product_portfolio_association) are unratified
	// ---- for a different reason: both documented import IDs require
	// ---- accept_language, an optional, defaulted argument this table's
	// ---- Component vocabulary has no way to supply when it is omitted
	// ---- from configuration - the same literal-fallback gap the messaging
	// ---- batch's aws_cloudwatch_event_rule left unratified.
	// ---- aws_config_configuration_recorder and aws_config_delivery_channel
	// ---- are registry-laggard (row-gen's own evidence-only classification
	// ---- undersells a real, clean name-based import this batch confirmed
	// ---- against the provider's docs) but out of this batch's mandate,
	// ---- which named only Config's clean proposals; left for a future
	// ---- batch alongside aws_config_aggregate_authorization and the three
	// ---- OrganizationConfigRule aliases (also confirmed importable, also
	// ---- out of this batch's named scope). aws_servicecatalog_constraint
	// ---- stays reasoned-none, untouched. No AWS::WellArchitected::* rows
	// ---- appeared in this cycle's row-gen pool at all. The four
	// ---- Organizations types that are ratified (accounts, OUs, policies
	// ---- and the resource policy singleton) go in on clean identity
	// ---- evidence alone - but this batch's cohort estate does not
	// ---- exercise any Organizations create against the pinned floci
	// ---- image; see live/e2e/estates/governance/README.md for why.
	// ---- Cohort estate: live/e2e/estates/governance.
	"aws_config_config_rule":                                    {},
	"aws_config_configuration_aggregator":                       {},
	"aws_config_conformance_pack":                               {},
	"aws_config_organization_conformance_pack":                  {},
	"aws_config_remediation_configuration":                      {},
	"aws_controltower_baseline":                                 {},
	"aws_controltower_control":                                  {},
	"aws_controltower_landing_zone":                             {},
	"aws_organizations_account":                                 {},
	"aws_organizations_organizational_unit":                     {},
	"aws_organizations_policy":                                  {},
	"aws_organizations_resource_policy":                         {},
	"aws_resourceexplorer2_index":                               {},
	"aws_resourceexplorer2_view":                                {},
	"aws_resourcegroups_group":                                  {},
	"aws_servicecatalog_portfolio":                              {},
	"aws_servicecatalog_portfolio_share":                        {},
	"aws_servicecatalog_product":                                {},
	"aws_servicecatalog_provisioned_product":                    {},
	"aws_servicecatalogappregistry_application":                 {},
	"aws_servicecatalogappregistry_attribute_group":             {},
	"aws_servicecatalogappregistry_attribute_group_association": {},
	"aws_auditmanager_assessment":                               {},
	"aws_auditmanager_framework":                                {},

	// ---- Registry-ratified (#40, #44, #65): media services
	// ---- (MediaLive's Multiplex pair, MediaPackage v1 and v2, IVS, and
	// ---- IVSChat). Same tools/row-gen pipeline as the batches above,
	// ---- cross-checked against the AWS provider's documented
	// ---- Argument/Attribute/Import sections at the pinned v6.58.0 tag
	// ---- (live/import-grammar.json), not accepted on the registry's
	// ---- classification alone. Four of row-gen's proposals in this
	// ---- batch's scope are deferred rather than ratified — MediaLive's
	// ---- Channel, Input and InputSecurityGroup and MediaConvert's Queue
	// ---- all map to a CloudFormation Registry entry whose handlers block
	// ---- is create/read/update/delete/list **all false** (three of them
	// ---- "some registry-laggard" MediaLive rows a prior sweep already
	// ---- flagged), the same "supplies no real evidence, whatever its
	// ---- primaryIdentifier claims" standard the streaming batch's
	// ---- aws_appsync_api_cache/aws_appsync_api_key rejections set — see
	// ---- internal/live/identity/table.go's own comment for the four
	// ---- deferred rows' evidence (including a hand-verified correction
	// ---- for the Queue, left unratified anyway for consistency) and
	// ---- live/e2e/estates/media/README.md for the full account. MediaStore
	// ---- (both its Container and the Container's policy) is deliberately
	// ---- absent for a different reason: AWS discontinued the service
	// ---- November 13, 2025 (already past), and the pinned provider's own
	// ---- docs carry a deprecation notice on both types — moved to
	// ---- live/residue.go's DeprecatedServices instead of ratified; see
	// ---- the README's "MediaStore: deprecated-service, not ratified"
	// ---- section for the evidence. MediaTailor and MediaConnect never
	// ---- entered scope at all: the pinned v6.58.0 AWS provider ships no
	// ---- aws_mediatailor_*/aws_mediaconnect_* resources whatsoever,
	// ---- despite both services being fully modeled in the CloudFormation
	// ---- Registry. Cohort estate: live/e2e/estates/media.
	"aws_medialive_multiplex":           {},
	"aws_medialive_multiplex_program":   {},
	"aws_media_package_channel":         {},
	"aws_media_packagev2_channel_group": {},
	"aws_ivs_channel":                   {},
	"aws_ivs_playback_key_pair":         {},
	"aws_ivs_recording_configuration":   {},
	"aws_ivschat_logging_configuration": {},
	"aws_ivschat_room":                  {},

	// ---- Registry-ratified (#40, #44, #65): sixth batch, data movement and
	// ---- transfer (Transfer Family's server/user/workflow/connector core,
	// ---- DataSync's agents/locations/tasks in full, DMS including its
	// ---- three registry-laggard replication types, AppIntegrations). Same
	// ---- tools/row-gen pipeline as the batches above, cross-checked
	// ---- against the AWS provider's documented Argument/Attribute/Import
	// ---- sections fetched from the pinned v6.58.0 tag directly. Transfer
	// ---- Server's row-gen proposal (ARN) does not survive that check — the
	// ---- documented import and "id" attribute are both the ServerID — and
	// ---- Transfer User's registry-says-ARN evidence is corrected to its
	// ---- real composite (server_id/user_name). DMS's Certificate and the
	// ---- two Endpoint types (including the S3 endpoint alias the sweep
	// ---- recorded against the same CFN type) correct the same way, to
	// ---- their documented client-named identifiers. DMS's replication
	// ---- instance, subnet group and task are registry-laggard (the CFN
	// ---- Registry ships every handler false for all three), but their
	// ---- provider-documented import commands are clean, unambiguous
	// ---- client-named identifiers untouched by that gap, the same
	// ---- registry-disagrees-but-the-provider-is-clear shape the devtools
	// ---- batch's CodeBuild/CodeCommit corrections established. Two of
	// ---- DataSync's FSx-backed locations (ONTAP, OpenZFS) correct
	// ---- row-gen's plain-ARN ImportSyntax to the provider's documented
	// ---- compound "DataSync-ARN#FSx-ARN" grammar; the identity itself
	// ---- stays server-assigned. Storage Gateway is registry-absent beyond
	// ---- a single TapePool type and is skipped entirely, and MGN/DRS have
	// ---- no CFN Registry footprint at all — row-gen proposes nothing for
	// ---- either. See internal/live/identity/table.go for the per-type
	// ---- evidence and the six Transfer Family types (certificate,
	// ---- profile, web_app, web_app_customization, agreement, ssh_key) left
	// ---- outside this batch's named scope. Cohort estate:
	// ---- live/e2e/estates/data-movement.
	"aws_transfer_server":                           {},
	"aws_transfer_user":                             {},
	"aws_transfer_workflow":                         {},
	"aws_transfer_connector":                        {},
	"aws_datasync_agent":                            {},
	"aws_datasync_task":                             {},
	"aws_datasync_location_s3":                      {},
	"aws_datasync_location_efs":                     {},
	"aws_datasync_location_nfs":                     {},
	"aws_datasync_location_smb":                     {},
	"aws_datasync_location_hdfs":                    {},
	"aws_datasync_location_object_storage":          {},
	"aws_datasync_location_azure_blob":              {},
	"aws_datasync_location_fsx_lustre_file_system":  {},
	"aws_datasync_location_fsx_ontap_file_system":   {},
	"aws_datasync_location_fsx_openzfs_file_system": {},
	"aws_datasync_location_fsx_windows_file_system": {},
	"aws_dms_certificate":                           {},
	"aws_dms_endpoint":                              {},
	"aws_dms_s3_endpoint":                           {},
	"aws_dms_event_subscription":                    {},
	"aws_dms_replication_config":                    {},
	"aws_dms_replication_instance":                  {},
	"aws_dms_replication_subnet_group":              {},
	"aws_dms_replication_task":                      {},
	"aws_appintegrations_data_integration":          {},
	"aws_appintegrations_event_integration":         {},
	// ---- Registry-ratified (#40, #44, #65): sixth batch, databases beyond
	// ---- RDS/DynamoDB/ElastiCache (issue #65's own recipe: Redshift,
	// ---- OpenSearch/OpenSearchServerless, Neptune, DocDB, Timestream, QLDB,
	// ---- MemoryDB, Cassandra/Keyspaces). Same tools/row-gen pipeline as the
	// ---- batches above, cross-checked against live/import-grammar.json's
	// ---- scraped Import sections (the pinned v6.58.0 provider docs
	// ---- fetched directly) rather than accepted on the CFN registry's
	// ---- classification alone — several of these rows correct a row-gen
	// ---- "evidence-only" demotion the same way earlier batches corrected
	// ---- aws_sns_topic_policy and aws_qldb_ledger and aws_memorydb_subnet_group
	// ---- do here. Per-service scope is deliberately narrow, matching issue
	// ---- #65's own sub-lists rather than every row-gen proposal in each
	// ---- service: see internal/live/identity/table.go for the per-type
	// ---- evidence, the rejection, and the out-of-scope proposals this batch
	// ---- left for later. Cohort estate: live/e2e/estates/databases.
	"aws_redshift_cluster":                      {},
	"aws_redshift_parameter_group":              {},
	"aws_redshift_subnet_group":                 {},
	"aws_redshift_snapshot_schedule":            {},
	"aws_redshiftserverless_namespace":          {},
	"aws_redshiftserverless_workgroup":          {},
	"aws_opensearch_domain":                     {},
	"aws_elasticsearch_domain":                  {},
	"aws_opensearchserverless_collection":       {},
	"aws_opensearchserverless_collection_group": {},
	"aws_opensearchserverless_access_policy":    {},
	"aws_opensearchserverless_lifecycle_policy": {},
	"aws_opensearchserverless_security_policy":  {},
	"aws_neptune_cluster_parameter_group":       {},
	"aws_neptune_parameter_group":               {},
	"aws_neptune_subnet_group":                  {},
	"aws_docdb_event_subscription":              {},
	"aws_docdbelastic_cluster":                  {},
	"aws_timestreamwrite_database":              {},
	"aws_timestreamwrite_table":                 {},
	"aws_timestreaminfluxdb_db_cluster":         {},
	"aws_timestreaminfluxdb_db_instance":        {},
	"aws_timestreamquery_scheduled_query":       {},
	"aws_qldb_ledger":                           {},
	"aws_memorydb_acl":                          {},
	"aws_memorydb_cluster":                      {},
	"aws_memorydb_multi_region_cluster":         {},
	"aws_memorydb_parameter_group":              {},
	"aws_memorydb_user":                         {},
	"aws_memorydb_subnet_group":                 {},
	"aws_keyspaces_keyspace":                    {},
	"aws_keyspaces_table":                       {},

	// ---- Registry-ratified (#40, #44, #65): SageMaker batch (domains,
	// ---- user profiles, models, endpoints and their configs, notebook
	// ---- instances, feature groups, model package groups, pipelines,
	// ---- spaces and apps, plus the surrounding algorithm/hub/image/
	// ---- workteam/monitoring family; issue #65's ratification campaign).
	// ---- Same tools/row-gen pipeline as the batches above, cross-checked
	// ---- against the AWS provider's own website/docs/r/ source (fetched
	// ---- from GitHub at the pinned v6.58.0 tag) rather than accepted on
	// ---- row-gen's classification alone: most of this batch's rows
	// ---- correct a registry-laggard "evidence-only" or GUESSED-argument
	// ---- verdict once the real Argument/Attribute Reference and, for
	// ---- several types, a genuine Terraform 1.12+ Identity Schema are
	// ---- read directly. Two of row-gen's 29 SageMaker proposals are
	// ---- rejected (aws_sagemaker_device: an Optional argument nested in
	// ---- a block, not a clean top-level identity component;
	// ---- aws_sagemaker_image_version: its documented composite embeds a
	// ---- server-assigned version number with no corresponding
	// ---- configuration argument at all) — see
	// ---- internal/live/identity/table.go for the full per-type evidence
	// ---- and live/e2e/estates/sagemaker/README.md for the account.
	// ---- Cohort estate: live/e2e/estates/sagemaker.
	"aws_sagemaker_algorithm":                                 {},
	"aws_sagemaker_app":                                       {},
	"aws_sagemaker_app_image_config":                          {},
	"aws_sagemaker_code_repository":                           {},
	"aws_sagemaker_data_quality_job_definition":               {},
	"aws_sagemaker_device_fleet":                              {},
	"aws_sagemaker_domain":                                    {},
	"aws_sagemaker_endpoint":                                  {},
	"aws_sagemaker_endpoint_configuration":                    {},
	"aws_sagemaker_feature_group":                             {},
	"aws_sagemaker_hub":                                       {},
	"aws_sagemaker_image":                                     {},
	"aws_sagemaker_mlflow_app":                                {},
	"aws_sagemaker_mlflow_tracking_server":                    {},
	"aws_sagemaker_model":                                     {},
	"aws_sagemaker_model_card":                                {},
	"aws_sagemaker_model_package_group":                       {},
	"aws_sagemaker_model_package_group_policy":                {},
	"aws_sagemaker_monitoring_schedule":                       {},
	"aws_sagemaker_notebook_instance":                         {},
	"aws_sagemaker_notebook_instance_lifecycle_configuration": {},
	"aws_sagemaker_pipeline":                                  {},
	"aws_sagemaker_project":                                   {},
	"aws_sagemaker_space":                                     {},
	"aws_sagemaker_studio_lifecycle_config":                   {},
	"aws_sagemaker_user_profile":                              {},
	"aws_sagemaker_workteam":                                  {},
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
