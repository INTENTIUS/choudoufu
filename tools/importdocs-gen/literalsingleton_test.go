// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestSoleIDLiteralValueAcceptsTheThree pins the accepting half against the
// three "using the word `...`" pages the 6.59.0 cache actually carries,
// each section excerpt trimmed to the sentence that matters.
func TestSoleIDLiteralValueAcceptsTheThree(t *testing.T) {
	for _, tc := range []struct {
		tfType, section, example string
	}{
		{
			"aws_iam_account_password_policy",
			"## Import\n\nuse an `import` block to import IAM Account Password Policy using the word `iam-account-password-policy`. For example:",
			"iam-account-password-policy",
		},
		{
			"aws_sesv2_account_vdm_attributes",
			"## Import\n\nuse an `import` block to import SESv2 (Simple Email V2) Account VDM Attributes using the word `ses-account-vdm-attributes`. For example:",
			"ses-account-vdm-attributes",
		},
		{
			"aws_spot_datafeed_subscription",
			"## Import\n\nuse an `import` block to import a Spot Datafeed Subscription using the word `spot-datafeed-subscription`. For example:",
			"spot-datafeed-subscription",
		},
	} {
		if got := soleIDLiteralValue(tc.section, tc.example); got != tc.example {
			t.Errorf("%s: soleIDLiteralValue(...) = %q, want %q", tc.tfType, got, tc.example)
		}
	}
}

// TestSoleIDLiteralValueRefusesEverythingElse is the defeating half. The
// first two are the real trap this field's own doc comment names: an
// "using the ID ..." phrasing whose worked example happens to look like a
// literal but is a per-instance value the doc calls an "ID", never a
// "word" - matching on token-equals-example alone would misidentify both.
// The rest are drift and absence cases: a "the word" phrase whose token
// does not match the doc's own worked example (a doc inconsistency this
// field must refuse rather than resolve by guessing), and a page with no
// "using the word" phrasing at all.
func TestSoleIDLiteralValueRefusesEverythingElse(t *testing.T) {
	for _, tc := range []struct {
		name, section, example string
	}{
		{
			"athena named query - 'the query ID', digits that look constant but are per-instance",
			"## Import\n\nimport Athena Named Query using the query ID. For example:",
			"0123456789",
		},
		{
			"cloudtrail delegated admin - 'the delegate account `id`', another account's own id",
			"## Import\n\nimport using the delegate account `id`. For example:",
			"12345678901",
		},
		{
			"drift between the prose token and the doc's own example",
			"## Import\n\nimport using the word `spot-datafeed-subscription`. For example:",
			"some-other-value",
		},
		{
			"no 'using the word' phrasing at all",
			"## Import\n\nimport Foos using the ID. For example:",
			"foo-12345",
		},
		{
			"no import section text",
			"",
			"iam-account-password-policy",
		},
		{
			"no worked example to cross-check against",
			"## Import\n\nimport using the word `iam-account-password-policy`. For example:",
			"",
		},
	} {
		if got := soleIDLiteralValue(tc.section, tc.example); got != "" {
			t.Errorf("%s: soleIDLiteralValue(...) = %q, want %q", tc.name, got, "")
		}
	}
}
