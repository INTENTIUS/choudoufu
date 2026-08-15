// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"strings"
)

// This file is issue #135. live/import-grammar.json recorded a separator on
// 205 of 1,600 rows, and for 23 already-admitted types the separator was
// sitting unscraped in the row's own import_id_example.
//
// # The issue's proposed disambiguator is not available here
//
// It suggested resolving the ambiguity with identity_schema_required's
// arity: "when exactly one candidate splits the example into
// len(identity_schema_required) segments, that candidate is the separator".
// That signal is carried on 441 rows overall, but on these 23 it is empty
// for 22 of them - every row below has arity 0 except
// aws_sagemaker_user_profile, whose example is an ARN that must not be split
// at all. So arity cannot decide these, and a pass built on it would have
// resolved one row and looked like it worked.
//
// # What the examples actually offer
//
// Almost all of them contain exactly ONE candidate separator character. The
// existing splitSegments declines them for a different reason: segmentRe
// requires each segment to start with a letter, and these IDs are full of
// hex and account numbers - '00b00fd5aecc0ab60a708659477e9617:MyFilter',
// '123456789012'. The character was never ambiguous; the segment shape was
// unrecognised.
//
// So this asks a narrower question than splitSegments does. Not "which split
// produces plausible segments", but "is there exactly one candidate
// character in this example at all". One is evidence. Two is not, and
// declines - which is why the ARN, and
// 'us-west-2_abc123|https://example.com' where the ':' of a URL scheme
// competes with the real '|', are both left alone rather than guessed at.

// exampleSeparatorCandidates is the closed set this pass considers,
// deliberately excluding "_" from candidateSeparators.
//
// Underscore is the one character that appears inside single, opaque AWS
// identifiers as often as it appears between two of them: a Cognito user
// pool ID is spelled us-west-2_abc123 and is ONE value. splitSegments
// guards this with isShoutingToken, which works on a prose format token
// ("ROUTETABLEID_DESTINATION") and does not transfer to a real ID example,
// where nothing distinguishes 'tgw-rtb-12345678_tgw-attach-87654321' - two
// IDs joined - from a single pool ID that happens to contain one.
//
// Five of the 23 rows are underscore-joined and stay unresolved for exactly
// that reason. They are the rows the issue's arity signal would have
// settled, and it is absent for all of them; resolving them needs evidence
// this artifact does not yet carry, not a looser rule here.
var exampleSeparatorCandidates = []string{"/", ":", ",", "|"}

// arnSafeSeparators are the candidates that can still be a join when the
// example is an ARN, ':' and '/' being the ARN's own delimiters.
var arnSafeSeparators = []string{",", "|"}

// separatorFromExample reports the single candidate separator character that
// occurs in a documented import-ID example, when exactly one does.
//
// Empty segments are tolerated rather than rejected, because one is
// documented: aws_api_gateway_base_path_mapping's page shows 'example.com/'
// and says in prose it is "an empty base_path or, in other words, a root
// path". A rule that required non-empty segments would refuse the one
// example whose shape the doc goes out of its way to explain.
func separatorFromExample(example string) (string, bool) {
	example = strings.TrimSpace(example)
	if example == "" {
		return "", false
	}

	// An ARN is one opaque identifier, and ':' and '/' are its own internal
	// structure: arn:aws:sns:us-west-2:123456789012:my-topic is a single
	// value, not six configuration arguments joined by colons.
	//
	// This was measured, not foreseen. Without it, 76 rows whose example is
	// a bare ARN were given a separator, including
	// aws_sns_topic_policy and aws_secretsmanager_secret_policy - types
	// whose import ID is exactly one ARN. The earlier ARN case that DID
	// decline, aws_sagemaker_user_profile, only declined because its ARN
	// happened to contain a '/' as well, so the protection looked like it
	// worked and did not exist.
	//
	// A join can still follow an ARN: aws_datazone_user_profile's example
	// is 'arn:aws:iam::123456789012:user/example,dzd_54nakfrg9k6suo,IA'.
	// So the ARN's own two characters drop out and the rest still count.
	candidates := exampleSeparatorCandidates
	if strings.HasPrefix(example, "arn:") {
		candidates = arnSafeSeparators
	}

	var found string
	for _, c := range candidates {
		if !strings.Contains(example, c) {
			continue
		}
		if found != "" {
			return "", false // more than one candidate: this example does not decide
		}
		found = c
	}
	if found == "" {
		return "", false
	}
	// A single occurrence at the very start would leave an empty first
	// segment and no second one to speak of; that is not a join.
	if strings.HasPrefix(example, found) {
		return "", false
	}
	return found, true
}

// namedSeparatorRe reads a separator the doc names in words, and only where
// the sentence is announcing one. Issue #135.
//
// The context requirement is the whole of this pattern's safety, and it was
// added after the loose version wrote a wrong answer. Matching the bare word
// "pipes" anywhere in the section gave aws_pipes_pipe the separator "|",
// because its page says "import pipes using the `name`" - the service's own
// noun. A rule that reads a word without reading what the sentence is doing
// with it will keep finding separators in resource names.
//
// Two announcing shapes appear in the provider's docs:
//
//	"... separated by an underscore"        (also joined by, delimited by)
//	"... the Route Table, an underscore, and the destination"
//
// The second is the one the transit gateway pages use, and it is why this is
// not simply anchored on "separated by".
var namedSeparatorRe = regexp.MustCompile(
	`(?i)(?:(?:separated|joined|delimited)\s+by\s+(?:an?|the)?\s*|,\s*(?:an?|the)\s+)` +
		`(underscores?|colons?|commas?|(?:forward\s+)?slashes?|pipes?|vertical\s+bars?)\b`)

// namedSeparatorChar maps the matched word onto its character.
func namedSeparatorChar(word string) (string, bool) {
	w := strings.ToLower(strings.Join(strings.Fields(word), " "))
	w = strings.TrimSuffix(w, "s")
	switch w {
	case "underscore":
		return "_", true
	case "colon":
		return ":", true
	case "comma":
		return ",", true
	case "slash", "forward slash", "slashe", "forward slashe":
		return "/", true
	case "pipe":
		return "|", true
	case "vertical bar":
		return "|", true
	}
	return "", false
}

// placeholderTokenRe matches a format token written as angle-bracketed
// placeholders joined by a separator: "`<cidr>_<ipam-pool-id>`". The
// existing splitSegments cannot read these, because segmentRe requires a
// segment to start with a letter and "<cidr>" starts with "<".
var placeholderTokenRe = regexp.MustCompile("`(<[^`>]+>(?:[^`<>]<[^`>]+>)+)`")

// separatorFromProse reads a separator the doc states in words or shows in
// an angle-bracketed format token, in that order.
//
// Both are statements about the ID's grammar rather than observations of one
// value, so both outrank separatorFromExample.
func separatorFromProse(section string) (string, bool) {
	lower := strings.ToLower(section)

	// A named separator, taking the earliest announcing sentence so a
	// later aside cannot outrank the one describing the ID.
	if m := namedSeparatorRe.FindStringSubmatch(lower); m != nil {
		if char, ok := namedSeparatorChar(m[1]); ok {
			return char, true
		}
	}

	// A placeholder format token: every join character between the
	// bracketed groups must be the same one, or the token says nothing
	// unambiguous.
	if m := placeholderTokenRe.FindStringSubmatch(section); m != nil {
		var seps []string
		depth := 0
		for _, r := range m[1] {
			switch {
			case r == '<':
				depth++
			case r == '>':
				depth--
			case depth == 0:
				seps = append(seps, string(r))
			}
		}
		if len(seps) > 0 {
			first := seps[0]
			if !isCandidateSeparator(first) {
				return "", false
			}
			for _, s := range seps {
				if s != first {
					return "", false
				}
			}
			return first, true
		}
	}
	return "", false
}
