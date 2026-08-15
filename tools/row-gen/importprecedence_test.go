// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestDocPartPlaceholder pins issue #176's placeholder normalization: the
// Import section's own segment names, from literal placeholder tokens
// (byte-identical to the pre-#176 derivation) through the prose phrases the
// old derivation refused wholesale.
func TestDocPartPlaceholder(t *testing.T) {
	cases := []struct {
		token string
		want  string
		ok    bool
	}{
		// Literal placeholder names pass through as plain uppercasing.
		{"instance_id", "INSTANCE_ID", true},
		{"ID", "ID", true},
		{"PublishingDestinationID", "PUBLISHINGDESTINATIONID", true},
		// Prose phrases: articles are grammar, not name.
		{"the configuration profile ID", "CONFIGURATIONPROFILEID", true},
		{"application ID", "APPLICATIONID", true},
		{"deployment number", "DEPLOYMENTNUMBER", true},
		// The format-metalanguage prefix ends at its "of".
		{"a comma separated value of DomainIdentifier", "DOMAINIDENTIFIER", true},
		// A parenthesized aside is commentary.
		{"the catalog ID (usually AWS account ID)", "CATALOGID", true},
		// Content words that merely look meta-ish stay content.
		{"partition values", "PARTITIONVALUES", true},
		// Not a name at all: fails closed, templated fallback stands.
		{"the id of the project it's under", "", false},
	}
	for _, c := range cases {
		got, ok := docPartPlaceholder(c.token)
		if got != c.want || ok != c.ok {
			t.Errorf("docPartPlaceholder(%q) = %q, %v; want %q, %v", c.token, got, ok, c.want, c.ok)
		}
	}
}

// TestDeriveDocImportSyntax_AppConfigShape is issue #176's first measured
// misfire: the registry's primaryIdentifier names, "-"-joined in registry
// order, contradicted the doc's own colon-joined reverse order. The
// derivation must follow the scraped segment order and separator.
func TestDeriveDocImportSyntax_AppConfigShape(t *testing.T) {
	sep := ":"
	g := importGrammarRow{
		TFType:          "aws_appconfig_configuration_profile",
		ImportIDExample: "71abcde:11xxxxx",
		Separator:       &sep,
		IDParts: []idPart{
			{Token: "the configuration profile ID", Source: idPartSourceAttribute},
			{Token: "application ID", Source: "argument"},
		},
	}
	p := proposal{
		TFType:            "aws_appconfig_configuration_profile",
		Bucket:            bucketServerAssigned,
		PrimaryIdentifier: []string{"ApplicationId", "ConfigurationProfileId"},
	}
	deriveDocImportSyntax(&p, g)
	if p.DerivedImportSyntax != "CONFIGURATIONPROFILEID:APPLICATIONID" {
		t.Errorf("DerivedImportSyntax = %q, want %q", p.DerivedImportSyntax, "CONFIGURATIONPROFILEID:APPLICATIONID")
	}
}

// TestDeriveDocImportSyntax_Refusals: no scraped segment evidence, an
// unresolvable segment, or a syntax another rule already derived all leave
// the proposal untouched - the templated fallback (or the earlier rule's
// answer) stands.
func TestDeriveDocImportSyntax_Refusals(t *testing.T) {
	sep := ":"
	bare := proposal{TFType: "aws_x", Bucket: bucketServerAssigned}
	deriveDocImportSyntax(&bare, importGrammarRow{})
	if bare.DerivedImportSyntax != "" {
		t.Errorf("no evidence: DerivedImportSyntax = %q, want empty", bare.DerivedImportSyntax)
	}

	unresolvable := proposal{TFType: "aws_x", Bucket: bucketServerAssigned}
	deriveDocImportSyntax(&unresolvable, importGrammarRow{
		Separator: &sep,
		IDParts: []idPart{
			{Token: "glossary id", Source: "argument"},
			{Token: "the id of the project it's under", Source: "unknown"},
		},
	})
	if unresolvable.DerivedImportSyntax != "" {
		t.Errorf("unresolvable segment: DerivedImportSyntax = %q, want empty", unresolvable.DerivedImportSyntax)
	}

	derived := proposal{TFType: "aws_x", Bucket: bucketServerAssigned, DerivedImportSyntax: "ARN"}
	deriveDocImportSyntax(&derived, importGrammarRow{
		Separator: &sep,
		IDParts:   []idPart{{Token: "a_id", Source: "argument"}, {Token: "b_id", Source: "argument"}},
	})
	if derived.DerivedImportSyntax != "ARN" {
		t.Errorf("already derived: DerivedImportSyntax = %q, want ARN kept", derived.DerivedImportSyntax)
	}
}

// TestImportEvidenceContradiction pins issue #176's R3 corroboration clause
// against the real shapes it was measured on: the two counterexamples must
// be contradicted, and every ratified instance's evidence shape must stay
// uncontradicted.
func TestImportEvidenceContradiction(t *testing.T) {
	// aws_route53_resolver_config: the Import prose names the documented ID
	// as the resource's own identifier.
	resolver := importGrammarRow{
		ImportIDExample: "rslvr-rc-715aa20c73a23da7",
		EvidenceExcerpt: "## Import\n\nimport Route 53 Resolver configs using the Route 53 Resolver config ID. For example:\n\n```console\n% terraform import aws_route53_resolver_config.example rslvr-rc-715aa20c73a23da7\n```",
	}
	if r := importEvidenceContradiction("resource_id", "aws_route53_resolver_config", resolver); r == "" {
		t.Error("resolver config: want a contradiction (the prose names the resource's own ID), got none")
	}

	// aws_route53_hosted_zone_dnssec: near-identical prose, but "Hosted Zone
	// identifier" names the PARENT zone (the argument's referent), not the
	// dnssec resource's own noun tail - ratified client-named by
	// hosted_zone_id, and the clause must not contradict it.
	dnssec := importGrammarRow{
		ImportIDExample: "Z1D633PJN98FT9",
		EvidenceExcerpt: "## Import\n\nimport `aws_route53_hosted_zone_dnssec` resources using the Route 53 Hosted Zone identifier. For example:\n\n```console\n% terraform import aws_route53_hosted_zone_dnssec.example Z1D633PJN98FT9\n```",
	}
	if r := importEvidenceContradiction("hosted_zone_id", "aws_route53_hosted_zone_dnssec", dnssec); r != "" {
		t.Errorf("hosted zone dnssec: want no contradiction, got %q", r)
	}

	// aws_opensearch_application: the doc's own Example Usage sets the
	// claimed `name` to a word-placeholder while the documented ID is an
	// opaque identifier.
	opensearch := importGrammarRow{
		ImportIDExample:  "app-1234567890abcdef0",
		EvidenceExcerpt:  "## Import\n\nimport OpenSearch applications using the `id`. For example:",
		ExampleArguments: []exampleArgument{{Path: []string{"name"}, Value: "my-opensearch-app", IsString: true}},
	}
	if r := importEvidenceContradiction("name", "aws_opensearch_application", opensearch); r == "" {
		t.Error("opensearch application: want a contradiction (usage word-placeholder vs opaque ID), got none")
	}

	// aws_sesv2_contact_list: the doc configures the argument with the very
	// value it imports by - consistency proven.
	sesv2 := importGrammarRow{
		ImportIDExample:  "example",
		ExampleArguments: []exampleArgument{{Path: []string{"contact_list_name"}, Value: "example", IsString: true}},
	}
	if r := importEvidenceContradiction("contact_list_name", "aws_sesv2_contact_list", sesv2); r != "" {
		t.Errorf("sesv2 contact list: want no contradiction, got %q", r)
	}

	// aws_api_gateway_domain_name: usage literal and documented ID are both
	// word-placeholder-shaped domain values - no shape clash.
	apigw := importGrammarRow{
		ImportIDExample:  "dev.example.com",
		ExampleArguments: []exampleArgument{{Path: []string{"domain_name"}, Value: "api.example.com", IsString: true}},
	}
	if r := importEvidenceContradiction("domain_name", "aws_api_gateway_domain_name", apigw); r != "" {
		t.Errorf("api gateway domain name: want no contradiction, got %q", r)
	}

	// aws_msk_scram_secret_association: the documented ID template's own
	// per-segment attribution names the claimed argument; a claim the
	// template attributes to another argument is contradicted.
	msk := importGrammarRow{
		ImportIDExample: "arn:aws:kafka:us-west-2:123456789012:cluster/example/279c0212-3",
		IDTemplate: &idTemplate{Kind: "arn", Segments: []idTemplateSegment{
			{Literal: "arn:aws:kafka:"}, {Cloud: "region"}, {Literal: ":"}, {Cloud: "account-id"},
			{Literal: ":"}, {Argument: "cluster_arn", AttributedBy: attrByPlaceholderName},
			{Literal: "/"}, {Unattributed: "example"},
		}},
	}
	if r := importEvidenceContradiction("cluster_arn", "aws_msk_scram_secret_association", msk); r != "" {
		t.Errorf("msk scram: want no contradiction (template attributes the argument), got %q", r)
	}
	if r := importEvidenceContradiction("secret_arn_list", "aws_msk_scram_secret_association", msk); r == "" {
		t.Error("msk scram with a different claimed argument: want a contradiction (template attributes another argument), got none")
	}

	// An Identity Schema that names only other attributes is a direct
	// contradiction; one naming the argument is consistency.
	schema := importGrammarRow{IdentitySchemaRequired: []string{"arn"}}
	if r := importEvidenceContradiction("name", "aws_x", schema); r == "" {
		t.Error("identity schema naming only arn: want a contradiction for claimed name, got none")
	}
	if r := importEvidenceContradiction("arn", "aws_x", schema); r != "" {
		t.Errorf("identity schema naming the claimed argument: want no contradiction, got %q", r)
	}
}

// TestTryArgumentReferenceConfirmedGuess_Corroboration: the full R3 path.
// A contradicted row stays evidence-only with the contradiction on record;
// an uncontradicted row promotes exactly as before issue #176.
func TestTryArgumentReferenceConfirmedGuess_Corroboration(t *testing.T) {
	contradicted := proposal{
		TFType:    "aws_route53_resolver_config",
		Bucket:    bucketEvidenceOnly,
		ArgName:   "resource_id",
		ArgSource: argSourceGuessed,
	}
	g := importGrammarRow{
		ImportIDExample:   "rslvr-rc-715aa20c73a23da7",
		EvidenceExcerpt:   "import Route 53 Resolver configs using the Route 53 Resolver config ID. For example:",
		ArgumentReference: []argumentRefEntry{{Name: "resource_id", Required: true}},
	}
	if tryArgumentReferenceConfirmedGuess(&contradicted, g) {
		t.Fatal("contradicted row promoted; want refusal")
	}
	if contradicted.Bucket != bucketEvidenceOnly {
		t.Errorf("bucket = %s, want evidence-only kept", contradicted.Bucket)
	}
	if len(contradicted.Notes) == 0 {
		t.Error("want the contradiction recorded as a note")
	}

	clean := proposal{
		TFType:    "aws_sesv2_contact_list",
		Bucket:    bucketEvidenceOnly,
		ArgName:   "contact_list_name",
		ArgSource: argSourceGuessed,
	}
	gClean := importGrammarRow{
		ImportIDExample:   "example",
		ArgumentReference: []argumentRefEntry{{Name: "contact_list_name", Required: true}},
		ExampleArguments:  []exampleArgument{{Path: []string{"contact_list_name"}, Value: "example", IsString: true}},
	}
	if !tryArgumentReferenceConfirmedGuess(&clean, gClean) {
		t.Fatal("uncontradicted row refused; want promotion")
	}
	if clean.Bucket != bucketClientNamed || clean.ArgName != "contact_list_name" {
		t.Errorf("promoted row = %s/%s, want client-named/contact_list_name", clean.Bucket, clean.ArgName)
	}
}

// TestValueNamesRequiredArgument pins the shapes that rewrote rule 3's
// matching (issue #132). Every case is a real page: the three the old
// substring containment got right must keep resolving, and the five it got
// wrong - an argument's token floating inside the resource's own id prefix
// or a placeholder describing the resource's own identifier - must refuse.
func TestValueNamesRequiredArgument(t *testing.T) {
	cases := []struct {
		name     string
		example  string
		tfType   string
		required []string
		want     string
		wantOK   bool
	}{
		{"id value whose prefix is the argument's token, aws_vpc_dhcp_options_association",
			"vpc-0f001273ec18911b1", "aws_vpc_dhcp_options_association", []string{"vpc_id", "dhcp_options_id"}, "vpc_id", true},
		{"placeholder describing the argument, aws_codebuild_fleet",
			"fleet-name", "aws_codebuild_fleet", []string{"name"}, "name", true},
		{"placeholder describing the argument, aws_codebuild_project",
			"project-name", "aws_codebuild_project", []string{"name"}, "name", true},
		{"the resource's own id prefix embedding a token, aws_vpc_endpoint",
			"vpce-3ecf2a57", "aws_vpc_endpoint", []string{"vpc_id", "service_name"}, "", false},
		{"a token inside a longer own prefix, aws_ec2_local_gateway_route_table_vpc_association",
			"lgw-vpc-assoc-1234567890abcdef", "aws_ec2_local_gateway_route_table_vpc_association", []string{"local_gateway_route_table_id", "vpc_id"}, "", false},
		{"a token inside a longer own prefix, aws_vpc_encryption_control",
			"vpcec-12345678901234567", "aws_vpc_encryption_control", []string{"vpc_id"}, "", false},
		{"a token inside a longer own prefix, aws_vpc_ipam_resource_discovery_association",
			"ipam-res-disco-assoc-0178368ad2146a492", "aws_vpc_ipam_resource_discovery_association", []string{"ipam_id", "ipam_resource_discovery_id"}, "", false},
		{"placeholder describing the resource's own identifier, aws_mailmanager_rule_set",
			"rule-set-id", "aws_mailmanager_rule_set", []string{"rule_set_name"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := valueNamesRequiredArgument(tc.example, tc.tfType, tc.required)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("valueNamesRequiredArgument(%q) = (%q, %v), want (%q, %v)", tc.example, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestLooksOpaque_JoinedARNsAreNotOpaque pins the aws_controltower_control
// and aws_ssoadmin_application_assignment shape: an ARN's own grammar has
// "/" and ":" inside it, but never "," or "|", so an arn:-prefixed example
// carrying either is a joined value and must not be claimed as one opaque
// arn attribute.
func TestLooksOpaque_JoinedARNsAreNotOpaque(t *testing.T) {
	cases := []struct {
		example string
		want    bool
	}{
		{"arn:aws:networkmanager::123456789012:device/global-network-01/device-07f6", true},
		{"arn:aws:organizations::123456789101:ou/o-x/ou-y,arn:aws:controltower:us-east-1::control/WTDSMKDKDNLE", false},
		{"arn:aws:sso::123456789012:application/id-12345678,abcd1234,USER", false},
		{"d-1234567890", true},
		{"aabbccddee/example-stage", false},
	}
	for _, tc := range cases {
		if got := looksOpaque(tc.example); got != tc.want {
			t.Errorf("looksOpaque(%q) = %v, want %v", tc.example, got, tc.want)
		}
	}
}

// TestIdentitySchemaOrder is issue #134's guard: the third fallback in
// tryGrammarComposite, which takes the provider's own Identity Schema as the
// segment order when the value-token bijection is ambiguous.
//
// The refusals matter more than the acceptances here. Arity alone fits a lot
// of things, and the whole reason this rule is safe is that it also demands
// the example corroborate the order.
func TestIdentitySchemaOrder(t *testing.T) {
	sep := func(s string) *string { return &s }
	cases := []struct {
		name    string
		row     importGrammarRow
		want    []string
		wantOK  bool
		because string
	}{
		{
			name: "ambiguous by value token, settled by the schema's order",
			row: importGrammarRow{
				ImportIDExample:        "role_of_mypolicy_name:mypolicy_name",
				Separator:              sep(":"),
				Arguments:              []string{"name", "role"},
				IdentitySchemaRequired: []string{"role", "name"},
			},
			want:    []string{"role", "name"},
			wantOK:  true,
			because: "both segments contain \"name\", so the bijection declines; the schema says role first and the first segment says \"role\"",
		},
		{
			name: "arity fits but the order is contradicted",
			row: importGrammarRow{
				// The documented example leads with a principal id while the
				// schema leads with instance_arn - aws_ssoadmin_account_assignment.
				ImportIDExample:        "principal-1234,GROUP,target-1,AWS_ACCOUNT,permission-set-arn,instance-arn",
				Separator:              sep(","),
				Arguments:              []string{"instance_arn", "permission_set_arn", "principal_id", "principal_type", "target_id", "target_type"},
				IdentitySchemaRequired: []string{"instance_arn", "permission_set_arn", "principal_id", "principal_type", "target_id", "target_type"},
			},
			wantOK:  false,
			because: "segment 0 names principal, not instance - the order fits by length and by nothing else",
		},
		{
			name: "nothing in the example corroborates the order",
			row: importGrammarRow{
				// aws_opensearchserverless_access_policy: real values that
				// echo neither argument name.
				ImportIDExample:        "example/data",
				Separator:              sep("/"),
				Arguments:              []string{"name", "type"},
				IdentitySchemaRequired: []string{"name", "type"},
			},
			wantOK:  false,
			because: "an uncorroborated order is a coincidence of length, even when it happens to be right",
		},
		{
			name: "the schema describes a different identity",
			row: importGrammarRow{
				ImportIDExample:        "cluster-1:addon-1",
				Separator:              sep(":"),
				Arguments:              []string{"cluster_name", "addon_name"},
				IdentitySchemaRequired: []string{"cluster_name", "region"},
			},
			wantOK:  false,
			because: "the schema's set is not the argument set, so it is not a reordering of this identity",
		},
		{
			name: "arity mismatch",
			row: importGrammarRow{
				ImportIDExample:        "a:b:c",
				Separator:              sep(":"),
				Arguments:              []string{"cluster_name", "addon_name"},
				IdentitySchemaRequired: []string{"cluster_name", "addon_name"},
			},
			wantOK:  false,
			because: "three segments against two attributes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := identitySchemaOrder(tc.row)
			if ok != tc.wantOK {
				t.Fatalf("identitySchemaOrder ok = %v, want %v (%s)", ok, tc.wantOK, tc.because)
			}
			if !tc.wantOK {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("order = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("order = %v, want %v", got, tc.want)
					break
				}
			}
		})
	}
}
