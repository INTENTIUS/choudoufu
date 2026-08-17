// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package uniquename reads one claim out of a piece of API documentation:
// that the name the CLIENT supplies for a resource is unique within the
// account and region the run is pointed at.
//
// # Why a claim about wording is worth a package
//
// internal/live/foreign already matches a live object's content against a
// declaration and deliberately refuses to bind on the result: a content match
// is "surfaced for explicit adoption and never bound automatically", because
// inferring ownership from it "would be exactly the guess the marker spec
// exists to forbid". That is right for content in general. Two objects can
// carry the same CIDR, the same policy document, the same anything, and
// picking one of them is a guess.
//
// A name AWS itself guarantees unique is not that. If the API refuses to
// create a second cache policy called "static-assets", then "the cache policy
// called static-assets" names exactly one object in the account, and reading
// it off a listing is not inference - it is reading the identity the
// configuration already states. The exception is narrow because the guarantee
// is narrow: it holds only where AWS says it holds, and this package's whole
// job is to be strict about what counts as AWS saying so.
//
// # Two sources, never one
//
// A caller must cross this predicate over two independent texts before acting
// on it - the provider's own argument-reference prose for the resource's
// top-level name argument, and the CloudFormation registry schema's
// description of the corresponding Name property. Either alone has been wrong:
// the provider docs assert a unique name for 111 resource types at
// hashicorp/aws 6.59.0, the CFN schemas for 32, and the two agree on 9.
// Crossing them is the same discipline the markerless veto uses when it reads
// a ratified row and a classifier verdict rather than trusting one.
//
// # What is deliberately refused
//
// Every guard here exists because a text in the corpus would otherwise have
// been read as a uniqueness guarantee it does not make:
//
//   - NEGATION. AWS::GameLift::Alias says "Alias names do not need to be
//     unique" and AWS::IVS::PlaybackKeyPair says "The value does not need to
//     be unique". Both contain the word; both assert the opposite. Measured
//     across the pinned doc cache and schema bundle this guard fires ZERO
//     times, because assertRe's four shapes never match a denial in the first
//     place - the sentence is refused one line earlier. It is kept, and that
//     fact is pinned by TestNegationGuardIsUnreachedAtThePin, because the
//     likely next change here is widening assertRe, and the widening that
//     first lets a denial through should fail a test rather than admit a type.
//   - PLATFORM-GENERATED. 54 hashicorp/aws 6.59.0 argument bullets end "If
//     omitted, Terraform will assign a random, unique name." - literally the
//     words "unique name", about a value the client did not write. This is by
//     far the largest false positive in either corpus, and without this guard
//     aws_cloudwatch_log_group, aws_iam_role and 52 others would read as
//     declaring a unique client-supplied name. AWS::ApiGateway::ApiKey and
//     AWS::S3::AccessPoint are the registry's version of the same shape,
//     "generates a unique physical ID".
//   - NARROWER SCOPE. AWS::Route53::KeySigningKey's name "must be unique for
//     each key signing key in the same hosted zone", and
//     AWS::BedrockAgentCore::Policy's "must be unique within the policy
//     engine". A listing is account-and-region wide; a name only unique
//     inside one parent can collide across parents in that listing, and a
//     collision between an object this estate means and one it does not is
//     precisely a wrong bind. Account, region and customer scope are the
//     listing's own scope and are accepted; anything narrower is refused.
//
// Refusing on ambiguity is the standing direction here: a wrong identity
// outranks a missing one, and every guard above turns a maybe into a no.
package uniquename

import (
	"regexp"
	"strings"
)

// assertRe matches a sentence stating that the name is unique. The four
// alternatives are the shapes hashicorp/aws 6.59.0 and the CloudFormation
// registry actually use: "A unique name to identify the cache policy", "The
// name must be unique for response headers policies in this AWS-account",
// "Cluster names must be unique per customer and per region", and
// OpenSearchServerless' bulleted "Unique to your account and AWS Region".
var assertRe = regexp.MustCompile(`(?i)(\bunique\s+name\b|\bnames?\s+(?:is|are|must\s+be)\s+unique\b|\bmust\s+be\s+unique\b|\bunique\s+to\s+your\s+account\b)`)

// negateRe matches a sentence denying uniqueness. It has to be checked
// separately rather than folded into assertRe as a negative lookahead,
// because Go's regexp has no lookahead - and because a denial should suppress
// the sentence outright rather than merely fail to match one phrasing of it.
var negateRe = regexp.MustCompile(`(?i)(\bdo(?:es)?\s+not\s+need\s+to\s+be\s+unique\b|\bneed\s+not\s+be\s+unique\b|\bnot\s+unique\b|\bnot\s+have\s+to\s+be\s+unique\b)`)

// generatedRe matches the uniqueness of a value the platform mints when the
// client omits the name. See the package comment's PLATFORM-GENERATED guard.
var generatedRe = regexp.MustCompile(`(?i)generates?\s+a\s+unique|assign\w*\s+a\s+random,?\s+unique|auto-?generat\w*\s+a\s+unique`)

// scopeRe finds a phrase that scopes the uniqueness claim to some container,
// capturing the container's noun. "within the policy engine", "in the same
// hosted zone", "per customer", "within each Region".
var scopeRe = regexp.MustCompile(`(?i)\b(?:within|per|in\s+the\s+same)\s+(?:the\s+|each\s+|your\s+|this\s+|a\s+)?([a-z][a-z0-9-]*)`)

// listingScopes are the containers a Cloud Control listing already covers, so
// a uniqueness claim scoped to one of them is a claim over the whole
// population a caller would search. "customer" and "account" are AWS's two
// spellings of the same boundary; "aws" catches "within your AWS account",
// where the captured noun is the qualifier.
var listingScopes = map[string]bool{
	"account":  true,
	"accounts": true,
	"customer": true,
	"region":   true,
	"regions":  true,
	"aws":      true,
}

// Asserted reports whether desc states that a client-supplied name is unique
// across the account and region a listing would cover.
//
// The unit of judgement is the sentence, not the description: AWS routinely
// puts the uniqueness claim in one sentence and an unrelated fallback,
// character limit or replacement rule in the next, and a description-wide
// match would let a guard in one sentence suppress a genuine claim in
// another - or, worse, let a genuine-looking phrase in one sentence survive a
// denial in the same one. A description asserts uniqueness when at least one
// of its sentences asserts it and clears every guard on its own.
func Asserted(desc string) bool {
	for _, s := range sentences(desc) {
		if !assertRe.MatchString(s) {
			continue
		}
		if negateRe.MatchString(s) || generatedRe.MatchString(s) {
			continue
		}
		if narrowerThanListing(s) {
			continue
		}
		return true
	}
	return false
}

// narrowerThanListing reports whether the sentence scopes its claim to a
// container smaller than the account and region a listing covers. A sentence
// naming no scope at all is not narrowed - "A unique name to identify the
// cache policy" states the guarantee flatly, and reading silence as a narrow
// scope would refuse the plainest phrasing AWS uses.
func narrowerThanListing(sentence string) bool {
	for _, m := range scopeRe.FindAllStringSubmatch(sentence, -1) {
		if !listingScopes[strings.ToLower(m[1])] {
			return true
		}
	}
	return false
}

// sentences splits desc on the punctuation AWS descriptions actually use to
// separate independent claims. Newlines count: several CFN descriptions run
// two claims across a line break with no full stop between them
// (AWS::CloudFront::ResponseHeadersPolicy is the worked example), and joining
// those into one sentence would let either half's guard suppress the other.
func sentences(desc string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if strings.TrimSpace(cur.String()) != "" {
			out = append(out, cur.String())
		}
		cur.Reset()
	}
	for _, r := range desc {
		switch r {
		case '.', ';', '\n':
			cur.WriteRune(r)
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}
