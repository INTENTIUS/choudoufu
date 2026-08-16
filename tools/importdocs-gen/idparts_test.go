// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"testing"
)

func TestAttributeReferenceNames_StopsAtSubHeading(t *testing.T) {
	doc := "## Argument Reference\n\n* `instance_id` - (Required) The instance.\n\n## Attribute Reference\n\nIn addition to all arguments above, the following attributes are exported:\n\n* `arn` - The ARN of the queue.\n* `queue_id` - The identifier for the queue.\n* `nested` - A block.\n\n### Nested\n\n* `inner` - must not count.\n\n## Timeouts\n"
	got := attributeReferenceNames(doc)
	want := []string{"arn", "queue_id", "nested"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attributeReferenceNames = %v, want %v", got, want)
	}
}

func TestAttributeReferenceNames_Absent(t *testing.T) {
	doc := "## Argument Reference\n\n* `name` - (Required)\n"
	if got := attributeReferenceNames(doc); got != nil {
		t.Errorf("attributeReferenceNames = %v, want nil", got)
	}
}

func sepPtr(s string) *string { return &s }

// TestIDParts_ConnectQueueShape is the shape issue #132's Connect family
// shares: the Import section's own sentence names both segments in
// backticks, one is a configuration argument, the other is the doc's own
// exported attribute - the per-segment attribution that says outright the
// ID is not reconstructible from configuration.
func TestIDParts_ConnectQueueShape(t *testing.T) {
	section := "## Import\n\nimport Amazon Connect Queues using the `instance_id` and `queue_id` separated by a colon (`:`). For example:\n\n```console\n% terraform import aws_connect_queue.example f1288a1f:c1d4e5f6\n```\n"
	got := idParts(section, "aws_connect_queue", sepPtr(":"), "f1288a1f:c1d4e5f6",
		opt("instance_id", "name"), []string{"arn", "queue_id"})
	want := []IDPart{
		{Token: "instance_id", Source: idPartSourceArgument},
		{Token: "queue_id", Source: idPartSourceAttribute},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %v, want %v", got, want)
	}
}

// TestIDParts_LambdaAliasShape is the counter-shape that reverted the first
// attempt at the Connect fix: one named argument, a two-segment example,
// and the second segment ("alias") is prose shorthand for the `name`
// argument, defined in neither doc section. The attribution must say
// "unknown" - no claim either way - never "attribute".
func TestIDParts_LambdaAliasShape(t *testing.T) {
	section := "## Import\n\nimport Lambda Function Aliases using the `function_name/alias`. For example:\n\n```console\n% terraform import aws_lambda_alias.example example/production\n```\n"
	got := idParts(section, "aws_lambda_alias", sepPtr("/"), "example/production",
		opt("function_name", "name", "function_version"), []string{"arn", "invoke_arn"})
	want := []IDPart{
		{Token: "function_name", Source: idPartSourceArgument},
		{Token: "alias", Source: idPartSourceUnknown},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %v, want %v", got, want)
	}
}

// TestIDParts_FormatTokenCaseInsensitive is the WAFv2 shape: the format
// token spells its segments "ID/Name/Scope" while the doc's sections spell
// them "id", "name", "scope" - attribution matches through normalize, and
// the attribute-sourced leading segment is named as such.
func TestIDParts_FormatTokenCaseInsensitive(t *testing.T) {
	section := "## Import\n\nimport WAFv2 Web ACLs using `ID/Name/Scope`. For example:\n\n```console\n% terraform import aws_wafv2_web_acl.example a1b2c3d4/example/REGIONAL\n```\n"
	got := idParts(section, "aws_wafv2_web_acl", sepPtr("/"), "a1b2c3d4/example/REGIONAL",
		opt("name", "scope", "default_action"), []string{"id", "arn", "capacity"})
	want := []IDPart{
		{Token: "ID", Source: idPartSourceAttribute},
		{Token: "Name", Source: idPartSourceArgument},
		{Token: "Scope", Source: idPartSourceArgument},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %v, want %v", got, want)
	}
}

// TestIDParts_ArityGate: names that do not account for every segment of the
// documented example are not a statement about the whole ID (issue #39's
// aws_route discipline) and produce no attribution at all.
func TestIDParts_ArityGate(t *testing.T) {
	section := "## Import\n\nusing the `instance_id` and `queue_id` separated by a colon (`:`). For example:\n\n```console\n% terraform import aws_foo.example a:b:c\n```\n"
	if got := idParts(section, "aws_foo", sepPtr(":"), "a:b:c", opt("instance_id"), []string{"queue_id"}); got != nil {
		t.Errorf("idParts = %v, want nil (two named segments cannot account for a three-segment example)", got)
	}
}

// TestIDParts_NoSeparator: with no resolved separator there is no arity
// check to pass, so no attribution is claimed.
func TestIDParts_NoSeparator(t *testing.T) {
	section := "## Import\n\nusing the `instance_id` and `queue_id` separated by a colon (`:`).\n"
	if got := idParts(section, "aws_foo", nil, "a:b", opt("instance_id"), []string{"queue_id"}); got != nil {
		t.Errorf("idParts = %v, want nil", got)
	}
}

// TestIDParts_SingleTokenProse: a doc that names only one argument ("using
// `analyzer_name`") has no per-segment story to tell.
func TestIDParts_SingleTokenProse(t *testing.T) {
	section := "## Import\n\nimport Access Analyzer Analyzers using the `analyzer_name`. For example:\n"
	if got := idParts(section, "aws_foo", sepPtr(":"), "a:b", opt("analyzer_name"), nil); got != nil {
		t.Errorf("idParts = %v, want nil", got)
	}
}

// TestIDParts_PlainEnumOwnID is the GuardDuty shape (issue #132): the
// sentence names both segments in plain words, the first resolves to the
// Required detector_id argument, and the second names the resource's own
// noun plus ID on the resource's own page - the server-minted identifier a
// resource cannot configure for itself.
func TestIDParts_PlainEnumOwnID(t *testing.T) {
	section := "## Import\n\nimport GuardDuty IPSet using the primary GuardDuty detector ID and IPSet ID. For example:\n\n```console\n% terraform import aws_guardduty_ipset.MyIPSet 00b00fd5aecc:123456789012\n```\n"
	got := idParts(section, "aws_guardduty_ipset", sepPtr(":"), "00b00fd5aecc:123456789012",
		req("activate", "detector_id", "format", "location", "name"), []string{"arn"})
	want := []IDPart{
		{Token: "the primary GuardDuty detector ID", Source: idPartSourceArgument},
		{Token: "IPSet ID", Source: idPartSourceOwnID},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %v, want %v", got, want)
	}
}

// TestIDParts_PlainEnumLeadingArticle is the aws_bedrockagent_agent_alias
// shape: the own-noun part carries a leading article ("the alias ID"), and
// the phrase ends in a "separated by `,`" clause that names the join
// character, not a segment.
func TestIDParts_PlainEnumLeadingArticle(t *testing.T) {
	section := "## Import\n\nimport Agents for Amazon Bedrock Agent Alias using the alias ID and the agent ID separated by `,`. For example:\n\n```console\n% terraform import aws_bedrockagent_agent_alias.example 66IVY0GUTF,GGRRAED6JP\n```\n"
	got := idParts(section, "aws_bedrockagent_agent_alias", sepPtr(","), "66IVY0GUTF,GGRRAED6JP",
		req("agent_alias_name", "agent_id"), []string{"agent_alias_arn"})
	want := []IDPart{
		{Token: "the alias ID", Source: idPartSourceOwnID},
		{Token: "the agent ID", Source: idPartSourceArgument},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %v, want %v", got, want)
	}
}

// TestIDParts_PlainEnumAllUnknownProvesNothing: an enumeration none of
// whose parts resolves is noise, not attribution.
func TestIDParts_PlainEnumAllUnknownProvesNothing(t *testing.T) {
	section := "## Import\n\nimport things using the frobnicator handle and widget token. For example:\n\n```console\n% terraform import aws_foo.example a:b\n```\n"
	if got := idParts(section, "aws_foo", sepPtr(":"), "a:b", req("name"), []string{"id"}); got != nil {
		t.Errorf("idParts = %v, want nil (no part resolves to anything)", got)
	}
}

// TestIDParts_SpacedBacktickSegmentName is the hole phraseArgRe closes:
// the IPAM allocation page names one segment in snake_case and the other as
// a spaced phrase, both inside backticks, in the same "separated by"
// sentence. snakeArgRe matched only the first, which left the clause with
// one token against a two-segment example, so the arity gate refused the
// whole attribution and the page contributed no id_parts at all - which in
// turn kept the type in row-gen's needs-hand-separator bucket, where no
// argument-reconstruction rule can ever be right about it.
//
// The `id` segment resolves to the doc's own Attribute Reference, which is
// the fact tools/row-gen's tryDocNamedServerSegment needs. "pool id"
// resolves to neither section and must stay "unknown": the resource's own
// argument is ipam_pool_id, and claiming a match on a partial phrase is the
// aws_lambda_alias mistake above in the other direction.
func TestIDParts_SpacedBacktickSegmentName(t *testing.T) {
	section := "## Import\n\nimport IPAM allocations using the allocation `id` and `pool id`, separated by `_`. For example:\n\n```console\n% terraform import aws_vpc_ipam_pool_cidr_allocation.example ipam-pool-alloc-0dc6d1_ipam-pool-07cfb5\n```\n"
	got := idParts(section, "aws_vpc_ipam_pool_cidr_allocation", sepPtr("_"),
		"ipam-pool-alloc-0dc6d1_ipam-pool-07cfb5",
		opt("ipam_pool_id", "cidr", "description", "netmask_length"), []string{"id"})
	want := []IDPart{
		{Token: "id", Source: idPartSourceAttribute},
		{Token: "pool id", Source: idPartSourceUnknown},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %v, want %v", got, want)
	}
}

// TestSeparatedByPhraseTokens_NeverNarrowsTheStrictRead pins the fallback's
// one contract: it is only ever consulted when the strict sources found
// fewer than two tokens, and it refuses rather than returning a shorter
// list. A page whose clause names two snake_case arguments must be
// unaffected, which is what keeps this widening from re-attributing the
// 1692 pages that already resolve.
func TestSeparatedByPhraseTokens_NeverNarrowsTheStrictRead(t *testing.T) {
	section := "## Import\n\nimport them using the `instance_id` and `queue_id` separated by a colon (`:`).\n"
	strict := dedupe(snakeArgRe.FindAllStringSubmatch(section, -1), 1)
	if len(strict) < 2 {
		t.Fatalf("fixture no longer exercises the strict path: %v", strict)
	}
	got := separatedByPhraseTokens(section)
	if len(got) < len(strict) {
		t.Errorf("separatedByPhraseTokens = %v, narrower than the strict read %v", got, strict)
	}

	// No "using" before the clause: nothing to scan, and the fallback must
	// say so rather than reaching backwards into unrelated prose.
	if got := separatedByPhraseTokens("## Import\n\nthe `a b` and `c d`, separated by `_`.\n"); got != nil {
		t.Errorf("separatedByPhraseTokens with no leading \"using\" = %v, want nil", got)
	}
}
