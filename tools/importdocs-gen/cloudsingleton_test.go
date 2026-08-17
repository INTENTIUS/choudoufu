// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestSoleIDCloudValueAcceptsEveryPublishedRegion pins the accepting half
// against a source outside this file: AWS's own published region list at the
// 6.59.0 pin, every partition. A geography code this vocabulary is missing
// shows up here as a failure rather than as a type silently staying
// unadmitted.
func TestSoleIDCloudValueAcceptsEveryPublishedRegion(t *testing.T) {
	regions := []string{
		"us-east-1", "us-east-2", "us-west-1", "us-west-2",
		"af-south-1",
		"ap-east-1", "ap-east-2",
		"ap-south-1", "ap-south-2",
		"ap-southeast-1", "ap-southeast-2", "ap-southeast-3",
		"ap-southeast-4", "ap-southeast-5", "ap-southeast-7",
		"ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
		"ca-central-1", "ca-west-1",
		"eu-central-1", "eu-central-2",
		"eu-west-1", "eu-west-2", "eu-west-3",
		"eu-south-1", "eu-south-2", "eu-north-1",
		"il-central-1",
		"me-south-1", "me-central-1",
		"mx-central-1",
		"sa-east-1",
		"cn-north-1", "cn-northwest-1",
		"us-gov-east-1", "us-gov-west-1",
		"us-iso-east-1", "us-iso-west-1", "us-isob-east-1",
	}
	for _, r := range regions {
		if got := soleIDCloudValue(r); got != cloudRegionValue {
			t.Errorf("soleIDCloudValue(%q) = %q, want %q", r, got, cloudRegionValue)
		}
	}
}

// TestSoleIDCloudValueRefusesResourceNames is the defeating half, and every
// entry is a documented import ID from the 6.59.0 cache rather than an
// invented string. The first two are the measured counterexamples
// cloudsingleton.go's doc comment names: both are matched in full by
// idtemplate.go's arnRegionRe or by the single-trailing-digit tightening of
// it, which is why neither expression is what this field uses.
func TestSoleIDCloudValueRefusesResourceNames(t *testing.T) {
	for _, id := range []string{
		"tf-redshift-cluster-12345", // aws_redshift_cluster, matched by arnRegionRe
		"app-instance-profile-1",    // aws_iam_instance_profile, matched by the tightened form
		"123456789012",              // an account ID: deliberately outside this vocabulary
		"0123456789",                // aws_athena_named_query's query ID, spelled in digits
		"arn:aws:sns:us-west-2:1:t", // a region inside an ARN is idtemplate.go's business
		"us-east-1:some-other-part", // a region as one segment of a composite
		"us-east-12",                // two ordinal digits: no such region
		"xx-east-1",                 // not a published geography code
		"us-east",                   // no ordinal
		"",                          // no import example at all
	} {
		if got := soleIDCloudValue(id); got != "" {
			t.Errorf("soleIDCloudValue(%q) = %q, want %q", id, got, "")
		}
	}
}
