// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"testing"
)

// The Import sections below are quoted from the pinned doc cache
// (hashicorp/aws 6.59.0) rather than paraphrased, because the whole reading
// turns on the exact phrasing.
const (
	cognitoUserPoolClientImport = "## Import\n\n" +
		"In Terraform v1.5.0 and later, use an [`import` block](https://developer.hashicorp.com/terraform/language/import) " +
		"to import Cognito User Pool Clients using the `id` of the Cognito User Pool, and the `id` of the " +
		"Cognito User Pool Client. For example:\n\n" +
		"```terraform\nimport {\n  to = aws_cognito_user_pool_client.client\n  " +
		"id = \"us-west-2_abc123/3ho4ek12345678909nh3fmhpko\"\n}\n```\n"

	cognitoManagedUserPoolClientImport = "## Import\n\n" +
		"In Terraform v1.5.0 and later, use an [`import` block](https://developer.hashicorp.com/terraform/language/import) " +
		"to import Cognito User Pool Clients using the `id` of the Cognito User Pool and the `id` of the " +
		"Cognito User Pool Client. For example:\n\n" +
		"```terraform\nimport {\n  to = aws_cognito_managed_user_pool_client.example\n  " +
		"id = \"us-west-2_abc123/3ho4ek12345678909nh3fmhpko\"\n}\n```\n"
)

// TestOfPhraseIDParts_CognitoUserPoolClient is the shape this reader exists
// for, and it asserts the RENDERED segment names by value rather than the
// bare fact that a grammar appeared. The two segments are spelled the same
// in the prose - both `id` - so a reading that got the qualification wrong
// would still produce two parts and still satisfy the arity gate; only the
// values say whether it read the sentence or guessed at it.
func TestOfPhraseIDParts_CognitoUserPoolClient(t *testing.T) {
	got := idParts(cognitoUserPoolClientImport, "aws_cognito_user_pool_client", sepPtr("/"),
		"us-west-2_abc123/3ho4ek12345678909nh3fmhpko",
		req("name", "user_pool_id"), []string{"client_secret", "id"})
	want := []IDPart{
		{Token: "user_pool_id", Source: idPartSourceArgument},
		{Token: "id", Source: idPartSourceAttribute},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %#v, want %#v", got, want)
	}
}

// TestOfPhraseIDParts_QualifierWordInTheTypeName is the sibling page whose
// prose calls the object a "Cognito User Pool Client" while its type name
// is aws_cognito_managed_user_pool_client. The self-reference test has to
// see through the qualifier word or the segment the resource mints for
// itself resolves to nothing and the whole page is refused.
func TestOfPhraseIDParts_QualifierWordInTheTypeName(t *testing.T) {
	got := idParts(cognitoManagedUserPoolClientImport, "aws_cognito_managed_user_pool_client", sepPtr("/"),
		"us-west-2_abc123/3ho4ek12345678909nh3fmhpko",
		req("user_pool_id", "name_pattern", "name_prefix"), []string{"client_secret", "id", "name"})
	want := []IDPart{
		{Token: "user_pool_id", Source: idPartSourceArgument},
		{Token: "id", Source: idPartSourceAttribute},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %#v, want %#v", got, want)
	}
}

// TestOwnerNamesThisType_ParentIsNotTheResource is the distinction the
// whole reading turns on, isolated: on aws_cognito_user_pool_client's page,
// "the Cognito User Pool" is the PARENT and "the Cognito User Pool Client"
// is this resource. A prefix match would call both self-references, put the
// resource's own `id` in the parent's position, and compose an import
// string with the leaf twice.
func TestOwnerNamesThisType_ParentIsNotTheResource(t *testing.T) {
	cases := []struct {
		owner  string
		tfType string
		want   bool
	}{
		{"the Cognito User Pool Client", "aws_cognito_user_pool_client", true},
		{"the Cognito User Pool", "aws_cognito_user_pool_client", false},
		{"the Cognito User Pool Client", "aws_cognito_managed_user_pool_client", true},
		{"the Cognito User Pool", "aws_cognito_managed_user_pool_client", false},
		{"the listener", "aws_vpclattice_listener", true},
		{"the VPC Lattice service", "aws_vpclattice_listener", false},
		// Every prose word must be one of the type's own tokens.
		{"the Cognito User Group", "aws_cognito_user_pool_client", false},
		{"", "aws_cognito_user_pool_client", false},
	}
	for _, tc := range cases {
		if got := ownerNamesThisType(plainContentWords(tc.owner), tc.tfType); got != tc.want {
			t.Errorf("ownerNamesThisType(%q, %q) = %v, want %v", tc.owner, tc.tfType, got, tc.want)
		}
	}
}

// TestOfPhraseIDParts_DistributedOwnerRefused is the shape that looks like
// this one and is not: aws_s3control_multi_region_access_point's "the
// `account_id` and `name` of the Multi-Region Access Point" hangs ONE owner
// off both segments, so re-reading each part in the schema's order would
// turn `account_id` into an unqualified token and `name` into
// "multiregionaccesspointname". The existing backtick token source reads
// that page correctly; this reader must not touch it.
func TestOfPhraseIDParts_DistributedOwnerRefused(t *testing.T) {
	section := "## Import\n\nimport Multi-Region Access Points using the `account_id` and `name` of the " +
		"Multi-Region Access Point. For example:\n"
	if got := ofPhraseIDParts(section, "aws_s3control_multi_region_access_point", req("account_id", "name"), []string{"alias"}); got != nil {
		t.Errorf("ofPhraseIDParts = %#v, want nil (the owner is shared, not per-segment)", got)
	}
}

// TestOfPhraseIDParts_TrailingClauseRefused is aws_vpclattice_listener's
// page: two of-phrases, but the second carries "combined with a `/`
// character" - a separator clause this reader does not know, spelled
// differently from the "separated by" one it does. A part with an
// unaccounted-for tail is refused whole rather than trimmed on a guess.
func TestOfPhraseIDParts_TrailingClauseRefused(t *testing.T) {
	section := "## Import\n\nimport VPC Lattice Listener using the `listener_id` of the listener and the `id` " +
		"of the VPC Lattice service combined with a `/` character. For example:\n"
	if got := ofPhraseIDParts(section, "aws_vpclattice_listener", req("service_identifier"),
		[]string{"arn", "listener_id"}); got != nil {
		t.Errorf("ofPhraseIDParts = %#v, want nil", got)
	}
}

// TestOfPhraseIDParts_UnresolvableSegmentRefusesThePhrase: this reader
// emits schema NAMES, not prose, so a segment that lands on no argument and
// no attribute has no honest token to carry. Refusing the whole phrase is
// the only alternative to inventing one.
func TestOfPhraseIDParts_UnresolvableSegmentRefusesThePhrase(t *testing.T) {
	section := "## Import\n\nimport things using the `id` of the Frobnicator, and the `id` of the Widget. " +
		"For example:\n"
	if got := ofPhraseIDParts(section, "aws_foo_widget", req("name"), []string{"id"}); got != nil {
		t.Errorf("ofPhraseIDParts = %#v, want nil (the Frobnicator segment resolves to nothing)", got)
	}
}

// TestOfPhraseIDParts_DuplicateNamesRefused: two segments reducing to one
// name means the reading lost the difference between them, and a consumer
// matching both against the same attribute would compose a string with one
// segment repeated and another missing.
func TestOfPhraseIDParts_DuplicateNamesRefused(t *testing.T) {
	section := "## Import\n\nimport things using the `id` of the Widget and the `id` of the Widget. For example:\n"
	if got := ofPhraseIDParts(section, "aws_foo_widget", req("name"), []string{"id"}); got != nil {
		t.Errorf("ofPhraseIDParts = %#v, want nil", got)
	}
}

// TestOfPhraseIDParts_ArityGate: the caller's gate applies to this family
// too - names that do not account for every segment of the documented
// example are not a grammar for it.
func TestOfPhraseIDParts_ArityGate(t *testing.T) {
	if got := idParts(cognitoUserPoolClientImport, "aws_cognito_user_pool_client", sepPtr("/"),
		"a/b/c", req("name", "user_pool_id"), []string{"client_secret", "id"}); got != nil {
		t.Errorf("idParts = %#v, want nil (two named segments cannot account for a three-segment example)", got)
	}
}

// TestOfPhraseIDParts_NeverPreemptsTheReadersAboveIt pins the ordering
// contract: this is the last reader consulted, and a page the backtick
// token sources or the plain-word enumeration already resolve must come out
// of idParts unchanged. Mutation-checked by construction - the subject is
// the sibling Cognito page whose prose names its segments outright, and
// whose row this change must not move.
func TestOfPhraseIDParts_NeverPreemptsTheReadersAboveIt(t *testing.T) {
	section := "## Import\n\nIn Terraform v1.5.0 and later, use an [`import` block](https://x) to import Cognito " +
		"User Pool UI Customizations using the `user_pool_id` and `client_id` separated by `,`. For example:\n"
	got := idParts(section, "aws_cognito_user_pool_ui_customization", sepPtr(","),
		"us-west-2_ZCTarbt5C,12bu4fuk3mlgqa2rtrujgp6egq",
		req("user_pool_id", "client_id"), []string{"creation_date", "css_version"})
	want := []IDPart{
		{Token: "user_pool_id", Source: idPartSourceArgument},
		{Token: "client_id", Source: idPartSourceArgument},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idParts = %#v, want %#v", got, want)
	}
	if of := ofPhraseIDParts(section, "aws_cognito_user_pool_ui_customization",
		req("user_pool_id", "client_id"), []string{"creation_date", "css_version"}); of != nil {
		t.Errorf("ofPhraseIDParts = %#v, want nil on a page with no of-phrase at all", of)
	}
}
