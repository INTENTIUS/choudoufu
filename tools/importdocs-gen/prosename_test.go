// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// section wraps one import sentence in the doc shape usingPhrases scans.
func proseSection(sentence string) string {
	return "## Import\n\nIn Terraform v1.5.0 and later, use an import block to " + sentence + " For example:\n\n```console\n% terraform import aws_x.example value\n```\n"
}

func req(names ...string) []ArgumentRefEntry {
	out := make([]ArgumentRefEntry, len(names))
	for i, n := range names {
		out[i] = ArgumentRefEntry{Name: n, Required: true}
	}
	return out
}

func opt(names ...string) []ArgumentRefEntry {
	out := make([]ArgumentRefEntry, len(names))
	for i, n := range names {
		out[i] = ArgumentRefEntry{Name: n}
	}
	return out
}

// TestProseNamedArgument_Resolves covers the real doc shapes the resolver
// exists for, each quoted from the pinned cache.
func TestProseNamedArgument_Resolves(t *testing.T) {
	cases := []struct {
		name     string
		sentence string
		args     []ArgumentRefEntry
		attrs    []string
		want     string
	}{
		{
			name:     "plain words, aws_codecommit_repository",
			sentence: "import CodeCommit repository using repository name.",
			args:     req("repository_name"),
			attrs:    []string{"repository_id", "arn"},
			want:     "repository_name",
		},
		{
			name:     "suffix of a longer phrase, aws_appsync_api_cache",
			sentence: "import `aws_appsync_api_cache` using the AppSync API ID.",
			args:     req("api_id", "api_caching_behavior", "ttl", "type"),
			attrs:    []string{"id"},
			want:     "api_id",
		},
		{
			name:     "the doc's own meta word, aws_lightsail_disk",
			sentence: "import `aws_lightsail_disk` using the name attribute.",
			args:     req("availability_zone", "name", "size_in_gb"),
			attrs:    []string{"arn", "created_at"},
			want:     "name",
		},
		{
			name:     "backticked alias as a unique Required suffix, aws_sagemaker_image",
			sentence: "import SageMaker AI Code Images using the `name`.",
			args:     append(req("image_name", "role_arn"), opt("display_name")...),
			attrs:    []string{"arn", "id"},
			want:     "image_name",
		},
		{
			name:     "article the regex misses, aws_memorydb_subnet_group",
			sentence: "import a subnet group using its `name`.",
			args:     append(req(), ArgumentRefEntry{Name: "name"}, ArgumentRefEntry{Name: "subnet_ids", Required: true}),
			attrs:    []string{"arn"},
			want:     "name",
		},
		{
			name:     "possessive, aws_media_packagev2_channel_group",
			sentence: "import Elemental MediaPackage Version 2 Channel Group using the channel group's name.",
			args:     req("name"),
			attrs:    []string{"arn", "egress_domain"},
			want:     "name",
		},
		{
			name:     "corresponds-to redirect, aws_backup_framework",
			sentence: "import Backup Framework using the `id` which corresponds to the name of the Backup Framework.",
			args:     req("name", "control"),
			attrs:    []string{"id", "arn", "status"},
			want:     "name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := proseNamedArgument(proseSection(tc.sentence), tc.args, tc.attrs)
			if !ok || got != tc.want {
				t.Errorf("proseNamedArgument = (%q, %v), want (%q, true)", got, ok, tc.want)
			}
		})
	}
}

// TestProseNamedArgument_Refuses pins the misfires the discipline exists
// for - each of these fired in an earlier draft and was wrong, or names a
// server value the resolver must never claim.
func TestProseNamedArgument_Refuses(t *testing.T) {
	cases := []struct {
		name     string
		sentence string
		args     []ArgumentRefEntry
		attrs    []string
	}{
		{
			name:     "an exported attribute, aws_devopsguru_resource_collection",
			sentence: "import DevOps Guru Resource Collection using the `id`.",
			args:     req("type"),
			attrs:    []string{"id"},
		},
		{
			name:     "the provider-wide Optional region, aws_cloudwatch_otel_enrichment",
			sentence: "import CloudWatch OTel Enrichment using the region.",
			args:     opt("region"),
			attrs:    []string{"id"},
		},
		{
			name:     "spelled-out ARN idiom, aws_imagebuilder_component",
			sentence: "import `aws_imagebuilder_components` resources using the Amazon Resource Name (ARN).",
			args:     req("name", "platform", "version"),
			attrs:    []string{"arn", "date_created"},
		},
		{
			name:     "the resource's own ID over an Optional argument, aws_servicecatalog_provisioned_product",
			sentence: "import `aws_servicecatalog_provisioned_product` using the provisioned product ID.",
			args:     opt("product_id", "product_name"),
			attrs:    []string{"id", "arn"},
		},
		{
			name:     "a slash-joined two-part phrase, aws_directory_service_shared_directory",
			sentence: "import Directory Service Shared Directories using the owner directory ID/shared directory ID.",
			args:     req("directory_id", "target"),
			attrs:    []string{"id", "shared_directory_id"},
		},
		{
			name:     "an enumeration, aws_amplify_branch",
			sentence: "import Amplify branch using `app_id` and `branch_name`.",
			args:     req("app_id", "branch_name"),
			attrs:    []string{"arn"},
		},
		{
			name:     "a separated-by statement",
			sentence: "import Transfer Users using the `server_id` and `user_name` separated by `/`.",
			args:     req("server_id", "user_name", "role"),
			attrs:    []string{"arn"},
		},
		{
			name:     "an alias two Required arguments share",
			sentence: "import things using the `name`.",
			args:     req("first_name", "last_name"),
			attrs:    []string{"id"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := proseNamedArgument(proseSection(tc.sentence), tc.args, tc.attrs); ok {
				t.Errorf("proseNamedArgument = (%q, true), want a refusal", got)
			}
		})
	}
}

// TestBuildRow_ProseArgumentSuppressesExampleSeparator is the
// aws_cognito_identity_pool_roles_attachment shape: the prose names the
// whole ID as one argument, so the colon inside the example value is
// internal to that value, and the example-derived separator guess must not
// be recorded - a statement about the grammar outranks an observation of
// one value.
func TestBuildRow_ProseArgumentSuppressesExampleSeparator(t *testing.T) {
	doc := "# Resource\n\n## Argument Reference\n\n* `identity_pool_id` - (Required) The pool.\n* `roles` - (Required) The roles.\n\n## Attribute Reference\n\n* `id` - The ID.\n\n## Import\n\nIn Terraform v1.5.0 and later, use an import block to import Cognito Identity Pool Roles Attachment using the Identity Pool ID. For example:\n\n```console\n% terraform import aws_cognito_identity_pool_roles_attachment.example us-west-2:b64805ad-cb56-40ba\n```\n"
	row, ok := buildRow("aws_cognito_identity_pool_roles_attachment", doc)
	if !ok {
		t.Fatal("buildRow returned no row")
	}
	if row.ComposedOfArguments == nil || !*row.ComposedOfArguments {
		t.Fatalf("ComposedOfArguments = %v, want true", row.ComposedOfArguments)
	}
	if len(row.Arguments) != 1 || row.Arguments[0] != "identity_pool_id" {
		t.Errorf("Arguments = %v, want [identity_pool_id]", row.Arguments)
	}
	if row.Separator != nil {
		t.Errorf("Separator = %q, want nil (the colon is internal to the one value)", *row.Separator)
	}
}
