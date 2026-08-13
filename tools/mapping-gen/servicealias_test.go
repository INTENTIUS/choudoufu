// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestServiceResourceIndexScopedToOneService is the unit-level version of
// this file's own safety claim: the index for one service never contains a
// candidate string built from a different service's CFN type, even when
// that other service produces the identical resource segment (Bucket exists
// under both S3 and S3Outposts here).
func TestServiceResourceIndexScopedToOneService(t *testing.T) {
	roster := staticRoster{"AWS::S3::Bucket", "AWS::S3Outposts::Bucket", "AWS::EC2::VPCPeeringConnection"}
	idx := serviceResourceIndex(roster, "S3")

	if got := idx["bucket"]; !got["AWS::S3::Bucket"] || len(got) != 1 {
		t.Errorf(`idx["bucket"] = %v, want exactly {"AWS::S3::Bucket": true}`, got)
	}
	if _, ok := idx["vpc_peering_connection"]; ok {
		t.Error(`idx["vpc_peering_connection"] present in the S3 index; EC2's type must not leak in`)
	}
}

// TestServiceAliasCandidatesCutPoints exercises the cut-point search
// directly: aws_vpc_peering_connection needs cut=0 (EC2's own resource
// segment repeats "VPC"), aws_cloudwatch_log_stream needs cut=1 (stripping
// only "cloudwatch_", keeping "log_stream" - Logs' own type names all keep
// "Log" as a literal word), and aws_ssoadmin_permission_set needs cut=1 for
// a single-token prefix (stripping "ssoadmin_" entirely).
func TestServiceAliasCandidatesCutPoints(t *testing.T) {
	roster := staticRoster{
		"AWS::EC2::VPCPeeringConnection",
		"AWS::Logs::LogStream",
		"AWS::SSO::PermissionSet",
	}
	aliases := map[string][]string{
		"vpc":            {"EC2"},
		"cloudwatch_log": {"Logs"},
		"ssoadmin":       {"SSO"},
	}
	cache := serviceIndexCache{}

	cases := []struct {
		tf   string
		want string
	}{
		{"aws_vpc_peering_connection", "AWS::EC2::VPCPeeringConnection"},
		{"aws_cloudwatch_log_stream", "AWS::Logs::LogStream"},
		{"aws_ssoadmin_permission_set", "AWS::SSO::PermissionSet"},
	}
	for _, c := range cases {
		hits := serviceAliasCandidates(c.tf, roster, aliases, cache)
		if len(hits) != 1 || !hits[c.want] {
			t.Errorf("serviceAliasCandidates(%q) = %v, want exactly {%q}", c.tf, hits, c.want)
		}
	}
}

// TestServiceAliasCandidatesNeverCrossesService is the direct false-positive
// guard at the unit level: aws_amplify_webhook's own prefix ("amplify") has
// no service_aliases entry at all, so no CFN type - not even a coincidental
// resource-name match to a service the table never named for this prefix -
// can ever come back, no matter what the CFN roster contains.
func TestServiceAliasCandidatesNeverCrossesService(t *testing.T) {
	roster := staticRoster{"AWS::CodePipeline::Webhook", "AWS::Cassandra::Type"}
	aliases := map[string][]string{
		"vpc": {"EC2"}, // present, but does not match "amplify" or "appsync"
	}
	cache := serviceIndexCache{}

	for _, tf := range []string{"aws_amplify_webhook", "aws_appsync_type"} {
		if hits := serviceAliasCandidates(tf, roster, aliases, cache); len(hits) != 0 {
			t.Errorf("serviceAliasCandidates(%q) = %v, want no hits (no service_aliases entry matches this prefix)", tf, hits)
		}
	}
}

// TestServiceAliasCandidatesAmbiguousWithinService is the ambiguity path:
// two CFN types in the one aliased service both produce the same candidate
// string, so the caller must see both back rather than either being picked.
func TestServiceAliasCandidatesAmbiguousWithinService(t *testing.T) {
	roster := staticRoster{"AWS::S3::Bucket", "AWS::S3Outposts::Bucket"}
	aliases := map[string][]string{"s3control": {"S3", "S3Outposts"}}
	cache := serviceIndexCache{}

	hits := serviceAliasCandidates("aws_s3control_bucket", roster, aliases, cache)
	if len(hits) != 2 || !hits["AWS::S3::Bucket"] || !hits["AWS::S3Outposts::Bucket"] {
		t.Errorf("serviceAliasCandidates(aws_s3control_bucket) = %v, want both AWS::S3::Bucket and AWS::S3Outposts::Bucket", hits)
	}
}

// TestTokenPrefixEqual is a small direct test of the token-exact prefix
// match: "s3" must never match a type whose first token is "s3control" -
// the property that keeps a short prefix from swallowing an unrelated
// longer one.
func TestTokenPrefixEqual(t *testing.T) {
	cases := []struct {
		tokens, prefix []string
		want           bool
	}{
		{[]string{"s3control", "bucket"}, []string{"s3"}, false},
		{[]string{"s3", "bucket"}, []string{"s3"}, true},
		{[]string{"route53", "resolver", "endpoint"}, []string{"route53", "resolver"}, true},
		{[]string{"route53", "zone"}, []string{"route53", "resolver"}, false},
		{[]string{"vpc"}, []string{"vpc", "endpoint"}, false}, // prefix longer than tokens
	}
	for _, c := range cases {
		if got := tokenPrefixEqual(c.tokens, c.prefix); got != c.want {
			t.Errorf("tokenPrefixEqual(%v, %v) = %v, want %v", c.tokens, c.prefix, got, c.want)
		}
	}
}
