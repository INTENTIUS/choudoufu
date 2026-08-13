// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The stragglers cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableStragglers = []string{
	// Registry-ratified stragglers batch (#40, #44, issue #65's
	// ratification campaign): reachable types earlier batches left
	// outside their own named scope. Seven of this batch's types are
	// untaggable instead - see below. See
	// live/e2e/estates/stragglers/README.md.
	"aws_transfer_certificate",
	"aws_transfer_profile",
	"aws_transfer_web_app",
	"aws_transfer_agreement",
	"aws_storagegateway_tape_pool",
}

var untaggableStragglers = []string{
	// Registry-ratified stragglers batch (#40, #44, issue #65): seven
	// types with no tags argument at all, confirmed against the pinned
	// v6.58.0 provider docs and the generated live/e2e/estates/stragglers
	// fixture: aws_transfer_web_app_customization (Argument Reference:
	// web_app_id, favicon_file, logo_file, title only) and
	// aws_networkmanager_core_network_policy_attachment (Argument
	// Reference: core_network_id, policy_document only) are both
	// named-singleton-child folds, the same untaggable shape as
	// aws_s3_bucket_policy; the five ECR types are all policy/rule
	// documents or a name-prefix template, the same shape as
	// aws_ecr_repository_policy's own siblings. See
	// live/e2e/estates/stragglers/README.md, "Untaggable types".
	"aws_transfer_web_app_customization",
	"aws_networkmanager_core_network_policy_attachment",
	"aws_ecr_lifecycle_policy",
	"aws_ecr_pull_through_cache_rule",
	"aws_ecr_pull_time_update_exclusion",
	"aws_ecr_repository_creation_template",
	"aws_ecr_repository_policy",
}

func init() {
	registerCohortStamp(taggableStragglers, untaggableStragglers, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified stragglers batch (#40, #44, issue #65). See
			// live/e2e/estates/stragglers/README.md, "Untaggable types" for
			// which of these carry no tags argument at all.
			"aws_transfer_certificate":                          taggedSchema("id", "arn", "certificate_id"),
			"aws_transfer_profile":                              taggedSchema("id", "arn", "profile_id"),
			"aws_transfer_web_app":                              taggedSchema("id", "arn", "web_app_id"),
			"aws_transfer_web_app_customization":                untaggedSchema("id", "web_app_id"),
			"aws_transfer_agreement":                            taggedSchema("id", "arn", "server_id", "agreement_id"),
			"aws_networkmanager_core_network_policy_attachment": untaggedSchema("id", "core_network_id", "state"),
			"aws_storagegateway_tape_pool":                      taggedSchema("id", "arn", "pool_name"),
			"aws_ecr_lifecycle_policy":                          untaggedSchema("id", "repository", "registry_id"),
			"aws_ecr_pull_through_cache_rule":                   untaggedSchema("id", "ecr_repository_prefix"),
			"aws_ecr_pull_time_update_exclusion":                untaggedSchema("id", "principal_arn"),
			"aws_ecr_repository_creation_template":              untaggedSchema("id", "prefix"),
			"aws_ecr_repository_policy":                         untaggedSchema("id", "repository", "registry_id"),
		})
	})
}
