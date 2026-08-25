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

// TestArgumentReferenceNames_StopsAtDeeperSubHeading is the
// aws_cloudfront_distribution shape: the doc nests its block arguments
// under "####"/"#####" headings with no "###" at all, so a boundary that
// only knew "### " read more than a hundred nested names - including a
// fake top-level `id` (Viewer mTLS Config's) that the Identity Schema then
// matched as if it were a configuration argument.
func TestArgumentReferenceNames_StopsAtDeeperSubHeading(t *testing.T) {
	doc := "## Argument Reference\n\n* `aliases` (Optional) - CNAMEs.\n* `enabled` (Required) - Whether enabled.\n\n#### Viewer mTLS Config Arguments\n\n* `id` - nested, must not count.\n\n##### Trust Store Config Arguments\n\n* `mode` - nested, must not count.\n\n## Attribute Reference\n"
	got := argumentReferenceNames(doc)
	want := []string{"aliases", "enabled"}
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

func TestOmittedFallbacks(t *testing.T) {
	section := "use an import block to import EventBridge Rules using the `event_bus_name/rule_name` (if you omit `event_bus_name`, the `default` event bus will be used). For example:"
	got := omittedFallbacks(section, []string{"event_bus_name", "name"})
	if len(got) != 1 || got["event_bus_name"] != "default" {
		t.Errorf("omittedFallbacks = %v, want {event_bus_name: default}", got)
	}

	// The argument constraint: a fallback sentence about an argument the
	// composite does not name is not recorded.
	if got := omittedFallbacks(section, []string{"statement_id"}); got != nil {
		t.Errorf("omittedFallbacks recorded %v for a composite that does not name event_bus_name", got)
	}

	// No sentence, no field.
	if got := omittedFallbacks("using `a/b`. For example:", []string{"a", "b"}); got != nil {
		t.Errorf("omittedFallbacks = %v on a section with no fallback sentence", got)
	}
}

// TestArgumentReferenceEntries_ServerAssignedIfAbsent pins #190's
// extraction against the real doc shapes that motivated it (each comment
// below quotes the actual cached provider doc text at 6.59.0) alongside the
// false positives a bare "omitted"/"random"/"generate" keyword match would
// have caught: a fallback to an already-known value rather than a freshly
// generated one, a literal backticked default, and unrelated prose that
// happens to share a keyword but not the omission-plus-generation shape.
func TestArgumentReferenceEntries_ServerAssignedIfAbsent(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want bool
	}{
		{
			// aws_iam_role_policy: the auto-generation sentence is its own
			// continuation line, not part of the parenthetical.
			name: "continuation-line sentence",
			doc: "## Argument Reference\n\n" +
				"* `name` - (Optional) The name of the role policy.\n" +
				"  If omitted, Terraform will assign a random, unique name.\n" +
				"* `policy` - (Required) The inline policy document.\n",
			want: true,
		},
		{
			// aws_lambda_permission: on the bullet's own line.
			name: "same-line sentence",
			doc: "## Argument Reference\n\n" +
				"* `statement_id` - (Optional) Statement identifier. Generated by Terraform if not provided\n" +
				"* `statement_id_prefix` - (Optional) Statement identifier prefix.\n",
			want: true,
		},
		{
			// aws_cloudwatch_event_target.
			name: "if missing, will generate",
			doc: "## Argument Reference\n\n" +
				"* `target_id` - (Optional) The unique target assignment ID. If missing, will generate a random, unique id.\n",
			want: true,
		},
		{
			// aws_autoscaling_group and 11 other v6.59.0 types (see
			// serverAssignedOmitRe's own doc comment): the provider's docs
			// state the same auto-generation-on-omission fact with no
			// conditional at all, just "by default" - this is what let
			// corpus-autoscaling-complete's greenfield stage refuse a
			// brand-new aws_autoscaling_group whose name used the
			// name_prefix convention, before this pattern existed.
			name: "by default, no conditional",
			doc: "## Argument Reference\n\n" +
				"* `name` - (Optional) Name of the Auto Scaling Group. By default generated by Terraform. Conflicts with `name_prefix`.\n",
			want: true,
		},
		{
			// A default that falls back to an existing value rather than
			// generating a new one - the omission clause is present but the
			// generation clause is not, so this must not match.
			name: "omission without generation",
			doc: "## Argument Reference\n\n" +
				"* `account_id` - (Optional) The target account. Will manage current user's account by default if omitted.\n",
			want: false,
		},
		{
			// A literal backticked fallback (OmittedFallbacks' own shape,
			// scraped from the Import section separately) rather than a
			// freshly generated value.
			name: "literal fallback, not generation",
			doc: "## Argument Reference\n\n" +
				"* `opt_out_list_name` - (Optional) Name of the opt-out list. If omitted, AWS assigns the `Default` opt-out list.\n",
			want: false,
		},
		{
			// Unrelated prose sharing the words "omitted" and "generated"
			// without the auto-generation shape - the generation clause's
			// own trigger words never appear right after "generat...".
			name: "unrelated omitted/generated prose",
			doc: "## Argument Reference\n\n" +
				"* `value_key` - (Optional) Values extracted from the source objects. If omitted, original objects in the source list will be put into the values of the generated map.\n",
			want: false,
		},
		{
			// A Required argument never gets the field, however its prose
			// reads - omission is not a state a Required argument can be
			// in.
			name: "required argument excluded regardless of prose",
			doc: "## Argument Reference\n\n" +
				"* `name` - (Required) If omitted, Terraform will assign a random, unique name.\n",
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries := argumentReferenceEntries(tc.doc)
			if len(entries) == 0 {
				t.Fatalf("no entries parsed from %q", tc.doc)
			}
			got := entries[0].ServerAssignedIfAbsent
			if got != tc.want {
				t.Errorf("ServerAssignedIfAbsent = %v, want %v (entry: %+v)", got, tc.want, entries[0])
			}
		})
	}
}

// TestArgumentReferenceEntries_DeclaredUnique pins issue #272's
// provider-documentation source against the real doc shapes that motivated
// it: the two CloudFront types the issue's evidence names as PROVEN unique
// (quoted verbatim from the cached v6.59.0 docs), the one it names as NOT
// proven (the permanent negative case the issue asks for -
// aws_cloudfront_origin_access_control's own wording says "a name", never
// "unique"), and two real "do/does not need to be unique" denials found
// elsewhere in the same cache (aws_cleanrooms_collaboration,
// aws_prometheus_scraper) that a bare keyword match would have gotten
// backwards.
func TestArgumentReferenceEntries_DeclaredUnique(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want bool
	}{
		{
			// aws_cloudfront_cache_policy.html.markdown:49, one of the
			// issue's two worked PROVEN examples.
			name: "cache policy - proven unique",
			doc: "## Argument Reference\n\n" +
				"* `name` - (Required) Unique name used to identify the cache policy.\n",
			want: true,
		},
		{
			// aws_cloudfront_origin_request_policy.html.markdown:45, the
			// issue's second worked PROVEN example.
			name: "origin request policy - proven unique",
			doc: "## Argument Reference\n\n" +
				"* `name` - (Required) Unique name to identify the origin request policy.\n",
			want: true,
		},
		{
			// aws_cloudfront_origin_access_control.html.markdown:33 - the
			// issue's own worked NOT-proven negative case, kept here as the
			// permanent regression the issue text asks for: no "unique"
			// anywhere in the bullet.
			name: "origin access control - not proven (permanent negative case)",
			doc: "## Argument Reference\n\n" +
				"* `name` - (Required) A name that identifies the Origin Access Control.\n",
			want: false,
		},
		{
			// aws_cleanrooms_collaboration.html.markdown:51 - an explicit
			// denial in the same clause as the word "unique", which a bare
			// keyword match would misread as a positive.
			name: "explicit denial - do not need to be unique",
			doc: "## Argument Reference\n\n" +
				"* `name` - (Required) - Name of the collaboration.  Collaboration names do not need to be unique.\n",
			want: false,
		},
		{
			// prometheus_scraper.html.markdown:310 - the "does not" spelling
			// of the same denial, on an Optional argument.
			name: "explicit denial - does not need to be unique",
			doc: "## Argument Reference\n\n" +
				"* `alias` - (Optional) Name to associate with the managed scraper. This is for your use, and does not need to be unique.\n",
			want: false,
		},
		{
			// A negation earlier in the bullet, about something else
			// entirely, must not suppress a real later claim - the
			// clause-boundary rule declaredUniqueNegatedRe enforces.
			name: "unrelated negation does not suppress a later positive claim",
			doc: "## Argument Reference\n\n" +
				"* `name` - (Required) Spaces are not allowed. The name must be unique within the account.\n",
			want: true,
		},
		{
			// No mention of uniqueness at all.
			name: "no uniqueness claim",
			doc: "## Argument Reference\n\n" +
				"* `comment` - (Optional) Description for the cache policy.\n",
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries := argumentReferenceEntries(tc.doc)
			if len(entries) == 0 {
				t.Fatalf("no entries parsed from %q", tc.doc)
			}
			got := entries[0].DeclaredUnique
			if got != tc.want {
				t.Errorf("DeclaredUnique = %v, want %v (entry: %+v)", got, tc.want, entries[0])
			}
		})
	}
}
