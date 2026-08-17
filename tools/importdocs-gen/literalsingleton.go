// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"strings"
)

// This file answers cloudsingleton.go's question for its remaining case: a
// documented import ID that is, in its entirety, a FIXED STRING - the same
// word for every account, every region, every run. "In Terraform v1.5.0 and
// later, use an `import` block to import IAM Account Password Policy using
// the word `iam-account-password-policy`" (aws_iam_account_password_policy)
// is not composed of any configuration argument (ComposedOfArguments stays
// null - the word is nowhere in the Argument Reference) and is not a cloud
// property either (soleIDCloudValue's vocabulary is "region" and
// "account-id", neither of which this word is). Until this field nothing in
// the artifact said the ID was a constant at all: soleIDPart's own
// quotesTheExampleItself refuses to attribute the token to the server
// PRECISELY because it recognises this shape, but a refusal records nothing
// positive - see soleid.go's own comment on that function. This field is
// the positive statement quotesTheExampleItself declines to make.
//
// THE SIGNAL IS THE WORD "WORD", NOT THE TOKEN MATCHING THE EXAMPLE. Every
// other "using the ID ..." phrasing in the 6.59.0 cache also has its
// backtick token equal the documented example - that is simply what a
// worked example is - and most of those IDs are per-instance, not constant:
// aws_athena_named_query documents "using the query ID", example
// "0123456789", a stand-in for the query's own minted ID, not a literal
// every account shares. Matching on token-equals-example alone would call
// that a constant identity and misidentify every account's query as the
// same object - "the worst available failure" the identity docs describe.
// The doc author's own word choice is what actually distinguishes the two:
// "the word `X`" names X as a piece of vocabulary, spelled out because
// there is nothing else to name; "the ID" (or "the query ID", "the account
// ID") names a slot whose value happens, in the worked example, to look
// like a literal. Grepped over the full 1699-page 6.59.0 cache, "using the
// word" and "using the ID `" (a backtick immediately following, the same
// shape) are disjoint: three pages carry the former, none carries the
// latter.
//
// THE CROSS-CHECK. literalWordRe alone would be sufficient - three pages,
// zero false positives found - but soleIDLiteralValue still requires the
// backtick token to normalize equal to the doc's own worked example before
// reporting it, the same discipline quotesTheExampleItself already applies.
// A future page that phrases "using the word `X`" while its own example
// shows a DIFFERENT string would be a doc inconsistency this function
// refuses rather than resolves by guessing which side is right.
//
// THE THREE, verified against the pinned 6.59.0 cache:
//
//	aws_iam_account_password_policy    "iam-account-password-policy"
//	aws_sesv2_account_vdm_attributes   "ses-account-vdm-attributes"
//	aws_spot_datafeed_subscription     "spot-datafeed-subscription"

// literalWordRe matches the doc's own statement that the import ID is a
// fixed word: "using the word `<token>`". Anchored on "the word" rather
// than on any backtick-after-"using" shape - see this file's own doc
// comment for why that word is the discriminant and not the backticks.
var literalWordRe = regexp.MustCompile("(?i)using\\s+the\\s+word\\s+`([^`\n]+)`")

// soleIDLiteralValue reports the fixed literal string a documented import
// ID IS, in its entirety, when the Import section itself says so with "the
// word `...`" and the doc's own worked example carries the same string -
// or "" for every other page, which is nearly all of them.
func soleIDLiteralValue(section, idExample string) string {
	m := literalWordRe.FindStringSubmatch(section)
	if m == nil {
		return ""
	}
	token := strings.TrimSpace(m[1])
	if idExample == "" || normalize(token) != normalize(idExample) {
		return ""
	}
	return idExample
}
