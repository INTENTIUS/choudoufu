// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Ground-truth tests for idtemplate.go (issue #172) over real provider
// docs committed under testdata/docs (these seven fetched at the pinned
// v6.59.0 release, the same pin live/import-grammar.json carries). Each
// claim below was checked against the committed doc's own Import section
// before being written down.
package main

import (
	"reflect"
	"testing"
)

func templateFor(t *testing.T, tfType, slug string) *IDTemplate {
	t.Helper()
	row := mustRow(t, tfType, slug)
	return row.IDTemplate
}

// lit/cloud/argSeg/unattr keep the expected-segment tables below readable.
func lit(s string) TemplateSegment      { return TemplateSegment{Literal: s} }
func cloudSeg(s string) TemplateSegment { return TemplateSegment{Cloud: s} }
func argSeg(name, by string) TemplateSegment {
	return TemplateSegment{Argument: name, AttributedBy: by}
}
func unattr(s string) TemplateSegment { return TemplateSegment{Unattributed: s} }

func wantSegments(t *testing.T, got *IDTemplate, kind string, want []TemplateSegment) {
	t.Helper()
	if got == nil {
		t.Fatalf("IDTemplate = nil, want kind %q with %d segments", kind, len(want))
	}
	if got.Kind != kind {
		t.Errorf("Kind = %q, want %q", got.Kind, kind)
	}
	if !reflect.DeepEqual(got.Segments, want) {
		t.Errorf("Segments = %+v\nwant       %+v", got.Segments, want)
	}
}

// The doc's placeholder tail spells the argument outright: "report-group-
// name" strips its own-type nouns (report, group) to `name`, a Required
// argument - an own-text attribution, the tier strong enough to overturn a
// rule-1 server-assigned registry claim downstream.
func TestIDTemplate_CodebuildReportGroup_OwnTextTail(t *testing.T) {
	got := templateFor(t, "aws_codebuild_report_group", "codebuild_report_group")
	wantSegments(t, got, "arn", []TemplateSegment{
		lit("arn:aws:codebuild:"), cloudSeg("region"), lit(":"), cloudSeg("account-id"),
		lit(":report-group/"), argSeg("name", attrByPlaceholderName),
	})
}

// Two-argument tail: "example-domain" names `domain` after filler
// stripping; "example-repo" abbreviates `repository` (an unambiguous
// prefix of the one Required argument left). Both own-text.
func TestIDTemplate_CodeartifactRepository_TwoArgumentTail(t *testing.T) {
	got := templateFor(t, "aws_codeartifact_repository", "codeartifact_repository")
	wantSegments(t, got, "arn", []TemplateSegment{
		lit("arn:aws:codeartifact:"), cloudSeg("region"), lit(":"), cloudSeg("account-id"),
		lit(":repository/"), argSeg("domain", attrByPlaceholderName),
		lit("/"), argSeg("repository", attrByPlaceholderAbbrev),
	})
}

// The sagemaker placeholders spell their arguments' full names verbatim
// ("domain-id", "space-name").
func TestIDTemplate_SagemakerSpace_FullNamePlaceholders(t *testing.T) {
	got := templateFor(t, "aws_sagemaker_space", "sagemaker_space")
	wantSegments(t, got, "arn", []TemplateSegment{
		lit("arn:aws:sagemaker:"), cloudSeg("region"), lit(":"), cloudSeg("account-id"),
		lit(":space/"), argSeg("domain_id", attrByPlaceholderName),
		lit("/"), argSeg("space_name", attrByPlaceholderName),
	})
}

// CloudFront ARNs carry no region: position 4 is empty, so the two colons
// merge into the leading literal and no region slot exists. The camelCase
// placeholder "ExampleNameForRealtimeLogConfig" strips filler (example,
// for) and own-type nouns (realtime, log, config) to `name`.
func TestIDTemplate_CloudfrontRealtimeLogConfig_EmptyRegion(t *testing.T) {
	got := templateFor(t, "aws_cloudfront_realtime_log_config", "cloudfront_realtime_log_config")
	wantSegments(t, got, "arn", []TemplateSegment{
		lit("arn:aws:cloudfront::"), cloudSeg("account-id"),
		lit(":realtime-log-config/"), argSeg("name", attrByPlaceholderName),
	})
}

// aws_sns_topic's tail "my-topic" is a self-placeholder (generic word plus
// the type's own noun), and its `name` argument is Optional - nothing
// Required corroborates the tail, so the extraction records the raw token
// rather than guessing. This is issue #172's stated sns refusal.
func TestIDTemplate_SNSTopic_TailStaysUnattributed(t *testing.T) {
	got := templateFor(t, "aws_sns_topic", "sns_topic")
	wantSegments(t, got, "arn", []TemplateSegment{
		lit("arn:aws:sns:"), cloudSeg("region"), lit(":"), cloudSeg("account-id"),
		lit(":"), unattr("my-topic"),
	})
}

// aws_ram_permission's tail "test-permission" is the same self-placeholder
// shape over a Required `name` - the extraction attributes it, but marks
// the signal contextual (attrBySelfPlaceholder), which is what keeps
// row-gen's rule from overturning the rule-1 server-assigned registry
// claim for this row. See tools/row-gen's TestTryAssembledTemplate_
// RulOneServerAssignedNeedsOwnText.
func TestIDTemplate_RAMPermission_SelfPlaceholderIsContextual(t *testing.T) {
	got := templateFor(t, "aws_ram_permission", "ram_permission")
	wantSegments(t, got, "arn", []TemplateSegment{
		lit("arn:aws:ram:"), cloudSeg("region"), lit(":"), cloudSeg("account-id"),
		lit(":permission/"), argSeg("name", attrBySelfPlaceholder),
	})
}

// aws_sqs_queue's documented example is the legacy region-less host with a
// non-numeric account segment: the URL template records the host as a
// literal and both path segments unattributed - the extraction states what
// the example shows, and the endpoint normalisation the ratified row made
// (sqs.REGION.amazonaws.com) stays a recorded ruling, not a parse.
func TestIDTemplate_SQSQueue_LegacyHostStaysLiteral(t *testing.T) {
	got := templateFor(t, "aws_sqs_queue", "sqs_queue")
	wantSegments(t, got, "url", []TemplateSegment{
		lit("https://queue.amazonaws.com/"), unattr("80398EXAMPLE"), lit("/"), unattr("MyQueue"),
	})
}

// Attribution reads every Example Usage fence, not the one fence the seed
// chooses. This page's import example spells the LEAD fence's name while
// the variant preference picks the clean second fence (the lead drops a
// reference), which is aws_kinesisanalyticsv2_application's exact shape:
// its import example says "application/example-sql-application" and only
// the SQL fence spells that name. Coupling attribution to the chosen fence
// turned this segment unattributed the moment the preference moved.
func TestIDTemplate_AttributionSpansAllFences(t *testing.T) {
	doc := "# Resource: aws_thing\n\n## Example Usage\n\n### Lead\n\n```terraform\n" + `
resource "aws_thing" "a" {
  thing_name = "example-sql-application"
  role       = aws_iam_role.example.arn
}
` + "```\n\n### Second\n\n```terraform\n" + `
resource "aws_thing" "b" {
  thing_name = "example-flink-application"
  mode       = "fast"
}
` + "```\n\n## Argument Reference\n\n* `thing_name` - (Required)\n* `role` - (Optional)\n* `mode` - (Optional)\n\n" +
		"## Import\n\n```console\n% terraform import aws_thing.a arn:aws:thing:us-west-2:123456789012:application/example-sql-application\n```\n"

	row, ok := buildRow("aws_thing", doc)
	if !ok {
		t.Fatal("buildRow refused the page")
	}
	// The seed's pick is the clean second fence; the sanity check that this
	// fixture really exercises the decoupling.
	if a := argFor(row.ExampleArguments, "thing_name"); a == nil || a.Value != "example-flink-application" {
		t.Fatalf("chosen fence's thing_name = %+v; the fixture no longer separates the seed's fence from the import example's", a)
	}
	wantSegments(t, row.IDTemplate, "arn", []TemplateSegment{
		lit("arn:aws:thing:"), cloudSeg("region"), lit(":"), cloudSeg("account-id"),
		lit(":"), unattr("application"), lit("/"), argSeg("thing_name", attrByExampleValue),
	})
}

// A short-ID example produces no template at all.
func TestIDTemplate_NilForShortIDExample(t *testing.T) {
	if got := templateFor(t, "aws_ecs_cluster", "ecs_cluster"); got != nil {
		t.Errorf("IDTemplate = %+v, want nil for a short-ID example", got)
	}
}

func TestNamesOwnTypeTail(t *testing.T) {
	cases := []struct {
		head, tfType string
		want         bool
	}{
		{"report-group", "aws_codebuild_report_group", true},
		{"deliverystream", "aws_kinesis_firehose_delivery_stream", true},
		{"user-profile", "aws_sagemaker_user_profile", true},
		{"registry", "aws_glue_registry", true},
		// A mid-token match is the PARENT's marker, not this type's: the
		// permissions-policy child must not consume "domain" as its own.
		{"domain", "aws_codeartifact_domain_permissions_policy", false},
		{"webhook", "aws_codepipeline_webhook", true},
		{"certificate", "aws_acm_certificate", true},
		{"plan", "aws_backup_plan", true},
		{"loadbalancer", "aws_lb", false},
	}
	for _, c := range cases {
		if got := namesOwnTypeTail(c.head, c.tfType); got != c.want {
			t.Errorf("namesOwnTypeTail(%q, %q) = %v, want %v", c.head, c.tfType, got, c.want)
		}
	}
}

func TestPlaceholderWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"my-topic", []string{"my", "topic"}, true},
		{"ExampleNameForRealtimeLogConfig", []string{"example", "name", "for", "realtime", "log", "config"}, true},
		{"user-profile-name", []string{"user", "profile", "name"}, true},
		{"MyQueue", []string{"my", "queue"}, true},
		{"SAMLADFS", []string{"samladfs"}, true},
		{"tf-acc-test-8593714120730241305", nil, false},
		{"80398EXAMPLE", nil, false},
	}
	for _, c := range cases {
		got, ok := placeholderWords(c.in)
		if ok != c.ok || (ok && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("placeholderWords(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
