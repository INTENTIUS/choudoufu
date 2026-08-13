// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

func TestImportSection_StopsAtNextLevel2Heading(t *testing.T) {
	doc := "# Resource: aws_foo\n\nBody.\n\n## Argument Reference\n\n* `name` - (Required)\n\n## Import\n\nImport prose.\n\n### Identity Schema\n\n#### Required\n\n* `name`\n\n## Attribute Reference\n\nshould not appear\n"
	section, ok := importSection(doc)
	if !ok {
		t.Fatal("expected an Import section")
	}
	if !contains(section, "Import prose.") || !contains(section, "Identity Schema") {
		t.Errorf("section missing expected content: %q", section)
	}
	if contains(section, "Attribute Reference") {
		t.Errorf("section leaked past the next level-2 heading: %q", section)
	}
}

func TestImportSection_Absent(t *testing.T) {
	doc := "# Resource: aws_foo\n\n## Argument Reference\n\n* `name` - (Required)\n"
	if _, ok := importSection(doc); ok {
		t.Error("expected no Import section")
	}
}

func TestArgumentReferenceNames_StopsAtSubHeading(t *testing.T) {
	doc := "## Argument Reference\n\nThis resource supports the following arguments:\n\n* `zone_id` - (Required) The ID.\n* `name` - (Required) The name.\n* `alias` - (Optional) A block.\n  [Documented below](#alias).\n\n### Alias\n\n* `zone_id` - (Required) different meaning here, must not count.\n* `evaluate_target_health` - (Required)\n\n## Attribute Reference\n"
	got := argumentReferenceNames(doc)
	want := []string{"zone_id", "name", "alias"}
	if !equalStrings(got, want) {
		t.Errorf("argumentReferenceNames = %v, want %v", got, want)
	}
}

func TestIdentitySchemaRequired(t *testing.T) {
	section := "## Import\n\nprose\n\n### Identity Schema\n\n#### Required\n\n* `role` (String) Name of the IAM role.\n* `policy_arn` (String) ARN of the IAM policy.\n\n#### Optional\n\n* `account_id` (String) AWS Account.\n\nmore prose\n"
	got, ok := identitySchemaRequired(section)
	if !ok {
		t.Fatal("expected an Identity Schema block")
	}
	want := []string{"role", "policy_arn"}
	if !equalStrings(got, want) {
		t.Errorf("identitySchemaRequired = %v, want %v", got, want)
	}
}

func TestImportIDExample_ConsoleCommand(t *testing.T) {
	section := "## Import\n\n```console\n% terraform import aws_kms_key.a 1234abcd-12ab-34cd-56ef-1234567890ab\n```\n"
	got, ok := importIDExample(section)
	if !ok || got != "1234abcd-12ab-34cd-56ef-1234567890ab" {
		t.Errorf("importIDExample = %q, %v", got, ok)
	}
}

func TestImportIDExample_BlockOnlyFallback(t *testing.T) {
	section := "## Import\n\n```terraform\nimport {\n  to = aws_kms_key.a\n  id = \"1234abcd-12ab-34cd-56ef-1234567890ab\"\n}\n```\n"
	got, ok := importIDExample(section)
	if !ok || got != "1234abcd-12ab-34cd-56ef-1234567890ab" {
		t.Errorf("importIDExample = %q, %v", got, ok)
	}
}

func TestImportIDExample_IgnoresNestedIdentityBlockID(t *testing.T) {
	// The nested `identity = { id = ... }` block's id must not be picked up
	// as the plain-ID example - only the top-level (two-space indent) id.
	section := "## Import\n\n```terraform\nimport {\n  to = aws_vpc_security_group_ingress_rule.example\n  identity = {\n    id = \"sgr-02108b27edd666983\"\n  }\n}\n```\n\n```terraform\nimport {\n  to = aws_vpc_security_group_ingress_rule.example\n  id = \"sgr-real-example\"\n}\n```\n"
	got, ok := importIDExample(section)
	if !ok || got != "sgr-real-example" {
		t.Errorf("importIDExample = %q, %v, want the top-level id only", got, ok)
	}
}

func TestSplitSegments(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		wantSegs []string
		wantSep  string
		wantOK   bool
	}{
		{"underscore pair", "ROUTETABLEID_DESTINATION", []string{"ROUTETABLEID", "DESTINATION"}, "_", true},
		{"slash pair with hyphens", "REST-API-ID/DEPLOYMENT-ID", []string{"REST-API-ID", "DEPLOYMENT-ID"}, "/", true},
		{"snake segments over slash", "function_name/alias", []string{"function_name", "alias"}, "/", true},
		{"three slash segments", "ID/Name/Scope", []string{"ID", "Name", "Scope"}, "/", true},
		{"single segment, no separator", "id", nil, "", false},
		{"ambiguous: no candidate cleanly splits", "a/b:c", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segs, sep, ok := splitSegments(tt.token)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (segs=%v sep=%q)", ok, tt.wantOK, segs, sep)
			}
			if !ok {
				return
			}
			if sep != tt.wantSep || !equalStrings(segs, tt.wantSegs) {
				t.Errorf("got segs=%v sep=%q, want segs=%v sep=%q", segs, sep, tt.wantSegs, tt.wantSep)
			}
		})
	}
}

func TestSeparatedByClause(t *testing.T) {
	section := "using the role name and policy arn separated by `/`. For example:"
	sep, args, ok := separatedByClause(section)
	if !ok || sep != "/" {
		t.Fatalf("sep = %q, ok = %v, want \"/\"", sep, ok)
	}
	if len(args) != 0 {
		t.Errorf("expected no backtick-quoted argument names in this prose form, got %v", args)
	}
}

func TestSeparatedByClause_WithArgumentNames(t *testing.T) {
	section := "using `target_group_arn`, `target_id`, and optionally `port` and `availability_zone` separated by commas (`,`)."
	sep, args, ok := separatedByClause(section)
	if !ok || sep != "," {
		t.Fatalf("sep = %q, ok = %v, want \",\"", sep, ok)
	}
	want := []string{"target_group_arn", "target_id", "port", "availability_zone"}
	if !equalStrings(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestClassifyGrammar_OpaqueSingleRequired(t *testing.T) {
	section := "## Import\n\n### Identity Schema\n\n#### Required\n\n* `id` - (String) ID of the thing.\n\nusing the `id`.\n"
	argNames := []string{"description", "policy"}
	got := classifyGrammar(section, argNames)
	if got.Composed == nil || *got.Composed {
		t.Fatalf("Composed = %v, want false", got.Composed)
	}
	if got.Separator != nil {
		t.Errorf("Separator = %v, want nil", got.Separator)
	}
}

func TestClassifyGrammar_NoSignalIsUnsure(t *testing.T) {
	section := "## Import\n\nUse `terraform import` to import this using the cluster name.\n"
	got := classifyGrammar(section, []string{"name"})
	if got.Composed != nil {
		t.Errorf("Composed = %v, want nil (no Identity Schema, no format signal)", *got.Composed)
	}
	if got.Separator != nil {
		t.Errorf("Separator = %v, want nil", got.Separator)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
