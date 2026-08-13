// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The iot cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableIot = []string{
	// Registry-ratified IoT core batch (#40, #44, issue #65): nine of
	// the batch's eleven types carry a top-level tags argument in the
	// pinned provider's own wire schema. See
	// live/e2e/estates/iot/README.md.
	"aws_iot_authorizer",
	"aws_iot_billing_group",
	"aws_iot_domain_configuration",
	"aws_iot_policy",
	"aws_iot_provisioning_template",
	"aws_iot_role_alias",
	"aws_iot_thing_group",
	"aws_iot_thing_type",
	"aws_iot_topic_rule",
}

var untaggableIot = []string{
	// Registry-ratified IoT core batch (#40, #44, issue #65): two of
	// the batch's eleven types carry no tags argument at all in the
	// pinned provider's own wire schema — aws_iot_thing's Argument
	// Reference names only name, attributes and thing_type_name, and
	// aws_iot_topic_rule_destination's names only enabled and
	// vpc_configuration. See live/e2e/estates/iot/README.md,
	// "Untaggable types".
	"aws_iot_thing",
	"aws_iot_topic_rule_destination",
}

func init() {
	registerCohortStamp(taggableIot, untaggableIot, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified IoT core batch (#40, #44, issue #65).
			// Taggable/untaggable per the real provider's documented Argument
			// Reference for each type: aws_iot_thing (name, attributes,
			// thing_type_name only) and aws_iot_topic_rule_destination
			// (enabled, vpc_configuration only) carry no tags argument at all.
			"aws_iot_authorizer":             taggedSchema("arn", "name"),
			"aws_iot_billing_group":          taggedSchema("id", "arn", "name"),
			"aws_iot_domain_configuration":   taggedSchema("id", "arn", "name"),
			"aws_iot_policy":                 taggedSchema("arn", "name"),
			"aws_iot_provisioning_template":  taggedSchema("arn", "name"),
			"aws_iot_role_alias":             taggedSchema("arn", "alias"),
			"aws_iot_thing":                  untaggedSchema("arn", "name"),
			"aws_iot_thing_group":            taggedSchema("id", "arn", "name"),
			"aws_iot_thing_type":             taggedSchema("arn", "name"),
			"aws_iot_topic_rule":             taggedSchema("arn", "name"),
			"aws_iot_topic_rule_destination": untaggedSchema("arn"),
		})
	})
}
