// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import "strings"

// admittedTypesV0 is the stateless v0 admission table: the provider-local
// resource type names that may appear in a configuration planned without
// authoritative state.
//
// A type belongs here only if its identity is recoverable from the live system
// with no memory, by one of the four admission paths described in the
// internal/stateless package documentation. The v0 contents are deliberately
// small: exactly the types used by the estate fixture (stateless/e2e/estate), including the ones
// that are there only to support a coverage row rather than to be one.
//
// The table is hardcoded and grows in two steps, neither of which is a v0
// concern:
//
//   - The provider survey in the design doc (AWS provider, 2026-08: 65 of the
//     top 68 types admitted) is the source for the next batch. Adding a type
//     means naming which of the four admission paths recovers its identity.
//   - Once the provider identity schemas from opentofu#2854 are plumbed
//     through (P1.2), most of this table becomes derivable rather than
//     asserted: a type whose identity schema is fully client-assigned or fully
//     parent-derived admits itself. The hardcoded list is what stands in until
//     then, and should shrink as that lands, not grow forever.
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

	// Parent-derived: identity is a composite key over already-admitted
	// parents.
	"aws_route":                      {},
	"aws_route_table_association":    {},
	"aws_iam_role_policy_attachment": {},
	// aws_route53_record (#19's second slice): the survey classes it
	// client-named, and its name and type are, but the third component of
	// its import identity (ZONEID_NAME_TYPE) is the parent zone's
	// server-assigned Z-ID, so the fork wires it as a composite through the
	// aws_route53_zone marker — flag F5 in stateless/SURVEY.md, resolved by
	// #20 wiring the zone. Verified against the provider's identity schema
	// (required import attributes: name, type, zone_id) and against floci.
	"aws_route53_record": {},
	// #21's parent-derived slice: the attachment's identity is the target
	// group's live ARN joined with the target and the port. Untaggable, so
	// it is not swept for removal — see stateless/LIMITATIONS.md.
	"aws_lb_target_group_attachment": {},

	// Client-assigned identity: the name is already in the configuration.
	"aws_s3_bucket":            {},
	"aws_s3_bucket_policy":     {},
	"aws_iam_role":             {},
	"aws_cloudwatch_log_group": {},
	// aws_ssm_parameter: the receipt demo (PE.3, stateless/RECEIPTS.md). Its
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
	// what the estate is written around. See internal/stateless/identity's
	// CloudContext. aws_sqs_queue is the same shape and is not here: floci
	// reports a queue URL the AWS provider's own importer will not parse
	// (choudoufu#26).
	"aws_sns_topic": {},

	// List plus content match, as a fungible set bound by tofu-slot marker.
	"aws_eip": {},
}

// admitted reports whether the given provider-local resource type is in the v0
// admission table.
func admitted(resourceType string) bool {
	_, ok := admittedTypesV0[resourceType]
	return ok
}

// logicalTypePrefixes are the provider-local type prefixes whose resources
// exist only inside the state file. Their value is generated once and then
// remembered; the record of them IS the store that stateless mode removes, so
// there is nothing to recover them from and no version of them that works
// without authoritative state. See stateless/LIMITATIONS.md.
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
