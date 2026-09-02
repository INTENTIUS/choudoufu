// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The s3 cohort's slice of internal/live/stamp's three pinned test
// collections. The cohort's first six types (aws_s3_bucket and the
// lifecycle/policy/public-access/encryption/versioning sub-resources) predate
// the per-cohort split and are still pinned in stamp_test.go's own lists;
// this file carries what the demand-head batch added on 2026-08-15.
//
// Both are untaggable, read from live/survey-full.json's signals.taggable
// rather than judged here: aws_s3_bucket_replication_configuration and
// aws_s3_bucket_object_lock_configuration both record
// "taggable": false, and neither doc page's Argument Reference names a tags
// argument. Their marker therefore lives on the parent bucket, the same way
// every other S3 sub-resource in this cohort works.
var taggableS3 []string

var untaggableS3 = []string{
	// Demand-head batch (2026-08-15): the two most-declared unadmitted types
	// in live/corpus-refusals.json's ladder.unadmitted_demand - 16 and 11 of
	// the 145 published estates respectively. Both classifier-derived, not
	// hand-shaped: aws_s3_bucket_replication_configuration by the existing
	// single-argument grammar rule, aws_s3_bucket_object_lock_configuration
	// by importprecedence.go's tryDocumentedShorterForm, which its own
	// two-form Import section needed building.
	"aws_s3_bucket_replication_configuration",
	"aws_s3_bucket_object_lock_configuration",
	// account-public-access-block batch: an account-level singleton (one
	// per AWS account, not per bucket). Its Argument Reference names
	// account_id and four block_public_* booleans, no tags block at all.
	"aws_s3_account_public_access_block",
}

func init() {
	registerCohortStamp(taggableS3, untaggableS3, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Caricature schemas, the same trade every unit test in this
			// package makes. The arguments named are the ones the identity
			// row reads (bucket) plus enough of each type's own required
			// arguments to be recognisable; no tags argument, because
			// neither type has one.
			"aws_s3_bucket_replication_configuration": untaggedSchema("id", "bucket", "role"),
			"aws_s3_bucket_object_lock_configuration": untaggedSchema("id", "bucket"),
			"aws_s3_account_public_access_block":      untaggedSchema("id", "account_id", "block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"),
		})
	})
}
