// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"testing"
)

// TestClassifyGrammar_ClauseOrder is the aws_transfer_user shape: a
// separated-by clause naming every argument in backticks. The clause's own
// left-to-right order is the ID's order, so ArgumentsInOrder carries it -
// this is what lets tryGrammarComposite's order fallback assemble the
// composite when the example's opaque values ("s-12345678/test-username")
// defeat the value-token bijection.
func TestClassifyGrammar_ClauseOrder(t *testing.T) {
	section := "## Import\n\nimport Transfer Users using the `server_id` and `user_name` separated by `/`. For example:\n\n```console\n% terraform import aws_transfer_user.bar s-12345678/test-username\n```\n"
	got := classifyGrammar(section, []string{"role", "server_id", "user_name"})
	if got.Composed == nil || !*got.Composed {
		t.Fatalf("Composed = %v, want true", got.Composed)
	}
	if want := []string{"server_id", "user_name"}; !reflect.DeepEqual(got.ArgumentsInOrder, want) {
		t.Errorf("ArgumentsInOrder = %v, want %v", got.ArgumentsInOrder, want)
	}
}

// TestClassifyGrammar_PartialClauseSetsNoOrder is the Connect guard: a
// clause whose second token (queue_id) is no argument proves nothing about
// the whole ID, so no order is claimed even though one token matched.
func TestClassifyGrammar_PartialClauseSetsNoOrder(t *testing.T) {
	section := "## Import\n\nimport Amazon Connect Queues using the `instance_id` and `queue_id` separated by a colon (`:`). For example:\n\n```console\n% terraform import aws_connect_queue.example a:b\n```\n"
	got := classifyGrammar(section, []string{"instance_id", "name"})
	if got.ArgumentsInOrder != nil {
		t.Errorf("ArgumentsInOrder = %v, want none (queue_id is no argument; the order of the whole ID is unproven)", got.ArgumentsInOrder)
	}
}

// TestEnumeratedRequiredArguments_AmplifyBranchShape: "using `app_id` and
// `branch_name`" names every segment in order but states no separator.
func TestEnumeratedRequiredArguments_AmplifyBranchShape(t *testing.T) {
	section := "## Import\n\nimport Amplify branch using `app_id` and `branch_name`. For example:\n\n```console\n% terraform import aws_amplify_branch.master d2ypk4k47z8u6/master\n```\n"
	got, ok := enumeratedRequiredArguments(section, req("app_id", "branch_name"))
	if !ok || !reflect.DeepEqual(got, []string{"app_id", "branch_name"}) {
		t.Errorf("enumeratedRequiredArguments = (%v, %v), want ([app_id branch_name], true)", got, ok)
	}
}

// TestEnumeratedRequiredArguments_OptionalRefuses is the aws_glue_connection
// guard: `CATALOG-ID` matches the Optional catalog_id argument, which
// defaults to the account ID server-side - the ratified rows model that as
// a slot, not a component, so an enumeration reaching an Optional argument
// is refused.
func TestEnumeratedRequiredArguments_OptionalRefuses(t *testing.T) {
	section := "## Import\n\nimport Glue Connections using the `CATALOG-ID` (AWS account ID if not custom) and `NAME`. For example:\n\n```console\n% terraform import aws_glue_connection.MyConnection 123456789012:MyConnection\n```\n"
	args := append(opt("catalog_id"), ArgumentRefEntry{Name: "name", Required: true})
	if got, ok := enumeratedRequiredArguments(section, args); ok {
		t.Errorf("enumeratedRequiredArguments = (%v, true), want a refusal (catalog_id is Optional)", got)
	}
}

// TestIdentityBlockOrder_Route53RecordShape: the doc's identity-block
// values, joined in the block's own order by the separator, reproduce the
// legacy id example exactly - order proven by value equality, which is the
// only signal aws_route53_record's opaque segments ("Z4KAPRWWNC7JR", "dev",
// "NS") leave standing.
func TestIdentityBlockOrder_Route53RecordShape(t *testing.T) {
	section := "## Import\n\n```terraform\nimport {\n  to = aws_route53_record.example\n  identity = {\n    zone_id = \"Z4KAPRWWNC7JR\"\n    name    = \"dev.example.com\"\n    type    = \"NS\"\n  }\n}\n```\n\n```terraform\nimport {\n  to = aws_route53_record.example\n  id = \"Z4KAPRWWNC7JR_dev.example.com_NS\"\n}\n```\n"
	got, ok := identityBlockOrder(section, "_", []string{"name", "type", "zone_id", "ttl"})
	if !ok || !reflect.DeepEqual(got, []string{"zone_id", "name", "type"}) {
		t.Errorf("identityBlockOrder = (%v, %v), want ([zone_id name type], true)", got, ok)
	}
}

// TestIdentityBlockOrder_ValueMismatchRefuses: block values that do not
// reproduce the id example under the separator prove nothing.
func TestIdentityBlockOrder_ValueMismatchRefuses(t *testing.T) {
	section := "## Import\n\n```terraform\nimport {\n  to = aws_x.example\n  identity = {\n    a = \"one\"\n    b = \"two\"\n  }\n}\n```\n\n```terraform\nimport {\n  to = aws_x.example\n  id = \"two_one\"\n}\n```\n"
	if got, ok := identityBlockOrder(section, "_", []string{"a", "b"}); ok {
		t.Errorf("identityBlockOrder = (%v, true), want a refusal (joined values do not reproduce the id)", got)
	}
}

// TestFuzzySegmentMatches_SuffixFallback is the
// aws_api_gateway_usage_plan_key shape: "USAGE-PLAN-KEY-ID" carries the
// `key_id` argument as its tail, reachable only when equality and prefix
// find nothing.
func TestFuzzySegmentMatches_SuffixFallback(t *testing.T) {
	got := fuzzySegmentMatches("USAGE-PLAN-KEY-ID", []string{"key_id", "usage_plan_id"})
	if !reflect.DeepEqual(got, []string{"key_id"}) {
		t.Errorf("fuzzySegmentMatches = %v, want [key_id]", got)
	}
	// The fallback never lets a short suffix like "id" match.
	if got := fuzzySegmentMatches("SOME-THING", []string{"id"}); got != nil {
		t.Errorf("fuzzySegmentMatches short-suffix = %v, want none", got)
	}
}
