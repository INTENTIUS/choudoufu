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
	got := idParts(section, sepPtr(":"), "f1288a1f:c1d4e5f6",
		[]string{"instance_id", "name"}, []string{"arn", "queue_id"})
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
	got := idParts(section, sepPtr("/"), "example/production",
		[]string{"function_name", "name", "function_version"}, []string{"arn", "invoke_arn"})
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
	got := idParts(section, sepPtr("/"), "a1b2c3d4/example/REGIONAL",
		[]string{"name", "scope", "default_action"}, []string{"id", "arn", "capacity"})
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
	if got := idParts(section, sepPtr(":"), "a:b:c", []string{"instance_id"}, []string{"queue_id"}); got != nil {
		t.Errorf("idParts = %v, want nil (two named segments cannot account for a three-segment example)", got)
	}
}

// TestIDParts_NoSeparator: with no resolved separator there is no arity
// check to pass, so no attribution is claimed.
func TestIDParts_NoSeparator(t *testing.T) {
	section := "## Import\n\nusing the `instance_id` and `queue_id` separated by a colon (`:`).\n"
	if got := idParts(section, nil, "a:b", []string{"instance_id"}, []string{"queue_id"}); got != nil {
		t.Errorf("idParts = %v, want nil", got)
	}
}

// TestIDParts_SingleTokenProse: a doc that names only one argument ("using
// `analyzer_name`") has no per-segment story to tell.
func TestIDParts_SingleTokenProse(t *testing.T) {
	section := "## Import\n\nimport Access Analyzer Analyzers using the `analyzer_name`. For example:\n"
	if got := idParts(section, sepPtr(":"), "a:b", []string{"analyzer_name"}, nil); got != nil {
		t.Errorf("idParts = %v, want nil", got)
	}
}
