// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"strings"
)

// This file scrapes one sentence the rest of this tool never read: the
// Attribute Reference's own `id` bullet, asked a single question - does the
// doc state that the exported `id` IS the whole composite import string?
//
// # Why that question, and why it could not be answered before
//
// Every other grammar signal here describes the IMPORT STRING: its
// separator, its segments, which argument each segment comes from. None of
// them says what the provider puts in the `id` ATTRIBUTE after a create,
// and the two are not the same fact. A provider can set `id` to the whole
// composite ("us-west-2_abc/xyz") or to the bare server-minted leaf
// ("xyz") while documenting the same composite import either way.
//
// internal/live/identity's record-located mechanism turns entirely on that
// difference (GitHub issues #329, #337). It records the applied object's
// `id` and hands it back to import on the next run, so a type whose `id` is
// the whole string round-trips and a type whose `id` is a leaf records a
// fragment - a WRONG identity, which no verdict-level check can see. #329
// closed the half of this the provider's own wire identity schema settles.
// For a type with no wire identity schema the schema says nothing, and this
// sentence is the only remaining account of what `id` holds.
//
// # Why this reads only a separator, and refuses everything else
//
// It records a separator the description STATES, and nothing else - no
// component order, no segment names. That is deliberate and it is what
// makes the signal safe to act on: a consumer asking "is `id` the whole
// import string" needs no order, and every attempt to recover order from
// prose in this class has counterexamples (see #309's own scouting -
// registry.json's primary_identifier order is provably not import order,
// and the Cognito import doc names both segments "id"). A description that
// claims a composite without naming its separator therefore yields nothing
// here rather than yielding a partial answer, because the stated separator
// is also the corroboration: it is checked against the Import section's own
// independently-scraped separator by the consumer, and a claim with nothing
// to check is a claim on one reading of one sentence.
//
// Measured at hashicorp/aws 6.59.0 over all 1693 fetched pages: 967 carry an
// `id` bullet, this fires on 116 of them, and on every one of those the
// stated separator either equals the Import section's own scraped separator
// (115) or the Import section scraped no separator at all (1) - zero
// contradictions. That agreement is the evidence the reading is faithful,
// and it is measured across the whole provider rather than over the
// population that motivated it.

// idAttrSeparatorWords maps the separator names the provider's docs spell
// out in words to the character each names. It is deliberately narrower
// than English: only the characters in candidateSeparators appear, so this
// reading can never introduce a join character the rest of the tool refuses
// to report. "hyphen" and "period" are the notable absentees, and dropping
// them costs exactly one page (aws_appsync_function's "Formatted as
// ApiId-FunctionId") - the right cost, since a separator this tool would
// not otherwise emit has no consumer.
var idAttrSeparatorWords = map[string]string{
	"forward slash": "/",
	"slash":         "/",
	"colon":         ":",
	"comma":         ",",
	"underscore":    "_",
	"pipe":          "|",
}

// idAttrSeparatorAlternation is idAttrSeparatorWords' keys as a regexp
// alternation, longest first so "forward slash" wins over "slash".
const idAttrSeparatorAlternation = `forward slash|underscore|slash|colon|comma|pipe`

var (
	// idAttrSeparatedByBacktickRe is the commonest spelling by a wide
	// margin: "...separated by a slash (`/`)." The character is quoted, so
	// nothing is inferred from the word at all.
	idAttrSeparatedByBacktickRe = regexp.MustCompile("(?i)separated by[^`]{0,60}`([^`]{1,3})`")

	// idAttrSeparatedByWordRe is the same sentence with the character left
	// unquoted: "separated by a colon." Reading it needs
	// idAttrSeparatorWords and nothing else.
	idAttrSeparatedByWordRe = regexp.MustCompile(`(?i)(?:separated|delimited|joined|divided)\s+by\s+(?:an?\s+|the\s+)?(` + idAttrSeparatorAlternation + `)s?\b`)

	// idAttrDelimitedWordRe is the adjectival form, in either of the two
	// participles the docs use for it: "A pipe-delimited string combining
	// `configuration_set_name` and `event_destination_name`", "A
	// comma-separated string made up of `ip` and `destination_pool_name`".
	idAttrDelimitedWordRe = regexp.MustCompile(`(?i)\b(` + idAttrSeparatorAlternation + `)[- ](?:delimited|separated)\b`)

	// idAttrFormatTokenRe is the format-string idiom: "API Key ID
	// (Formatted as ApiId:Key)". The token itself carries the separator,
	// so the reading is accepted only when exactly ONE candidate character
	// appears in it - a token joined two different ways states no single
	// separator and is refused rather than guessed at.
	idAttrFormatTokenRe = regexp.MustCompile("(?i)format(?:ted)?\\s+(?:as|:)\\s*`?([A-Za-z0-9_]+(?:[:/,|_.-][A-Za-z0-9_]+)+)`?")

	// idAttrCompositionWordRe is the gate on the last reading below: the
	// description has to CLAIM a composition somewhere before a
	// backtick-quoted token in it is read as spelling one out. Without the
	// gate, any description quoting a value that happens to contain a
	// candidate separator would state a grammar it never claimed.
	idAttrCompositionWordRe = regexp.MustCompile(`(?i)\b(?:combination|combined|combining|composed|composition|concatenat\w*|consists? of|comprising|made up of)\b`)

	// idAttrQuotedTokenRe finds every backtick-quoted token in the
	// description. Which of them (if any) actually SPELLS a composite is
	// splitSegments' question, not this regexp's: an underscore is both a
	// candidate separator and the character every snake_case argument name
	// is full of, so "the combination of `service_name` and `user_name`"
	// reads as an underscore-joined composite to anything that decides the
	// split by scanning for a separator character. splitSegments is the
	// reader the rest of this tool already trusts with exactly that
	// ambiguity.
	idAttrQuotedTokenRe = regexp.MustCompile("`([^`\n]+)`")
)

// IDAttribute is what the Attribute Reference's `id` bullet says. Present
// on a row whenever the page documents an `id` attribute at all - nil means
// the page's Attribute Reference has no `id` bullet, which is a different
// state from a bullet that describes a single value, and a consumer
// deciding what `id` holds needs to tell the two apart.
type IDAttribute struct {
	// StatedSeparator is the join character the description names, when it
	// states a composite form at all. Always one of candidateSeparators.
	// Empty when the description states no composite form - which is NOT
	// evidence that the id is a leaf, only the absence of evidence that it
	// is not: the same wording ("Name of the traffic mirror filter rule")
	// is what a page uses for a bare server-minted leaf and for a value it
	// simply did not describe carefully.
	StatedSeparator string `json:"stated_separator,omitempty"`

	// Reading names which of the spellings matched, so a reviewer can
	// see which rule produced the answer without re-deriving it. Empty
	// alongside an empty StatedSeparator.
	Reading string `json:"reading,omitempty"`

	// Description is the bullet's own text, verbatim - the evidence, kept
	// beside the verdict for the same reason EvidenceExcerpt is, and the
	// only field set when no composite form is stated.
	Description string `json:"description"`
}

// The readings' names, as they appear in IDAttribute.Reading.
const (
	idAttrReadingSeparatedByBacktick = "separated-by-backtick"
	idAttrReadingSeparatedByWord     = "separated-by-word"
	idAttrReadingDelimitedWord       = "delimited-word"
	idAttrReadingFormatToken         = "format-token"
	idAttrReadingCompositeToken      = "composite-token"

	// idAttrReadingCompositeAdjacent is adjacentBacktickSeparator's
	// reading: a chain of two or more backtick-quoted tokens joined, in
	// the prose itself, by one identical candidate-separator character
	// and nothing else - "`disk_name`,`instance_name`". The doc is
	// spelling the id out as a literal template of quoted segments, and
	// the separator is the plain character between them, not a character
	// found inside any one segment (that is splitSegments' question,
	// answered by idAttrReadingCompositeToken above).
	idAttrReadingCompositeAdjacent = "composite-adjacent-backtick"
)

// idAttributeComposite reads the Attribute Reference's `id` bullet: its text
// always, and the composite form it states when it states one. Nil only when
// the page documents no `id` attribute at all.
//
// doc is the whole page. attributeDescriptions is the same reader soleid.go
// already uses for the same bullet, asked a different question.
func idAttributeComposite(doc string) *IDAttribute {
	desc := strings.TrimSpace(attributeDescriptions(doc)[genericIDAttr])
	if desc == "" {
		return nil
	}
	sep, reading, _ := statedIDSeparator(desc)
	return &IDAttribute{StatedSeparator: sep, Reading: reading, Description: desc}
}

// statedIDSeparator applies the readings in descending order of how little
// each infers. The quoted character is read first because it infers
// nothing; the format token is read after it because it is the only one
// that derives the separator from a value rather than from a statement
// about the form; the two composition-word-gated readings run last because
// they are the only ones that need a claim of composition to be safe at
// all - one reading a token's own contents, the other reading the plain
// text between two tokens.
func statedIDSeparator(desc string) (sep, reading string, ok bool) {
	if m := idAttrSeparatedByBacktickRe.FindStringSubmatch(desc); m != nil && isCandidateSeparator(m[1]) {
		return m[1], idAttrReadingSeparatedByBacktick, true
	}
	if m := idAttrSeparatedByWordRe.FindStringSubmatch(desc); m != nil {
		return idAttrSeparatorWords[strings.ToLower(m[1])], idAttrReadingSeparatedByWord, true
	}
	if m := idAttrDelimitedWordRe.FindStringSubmatch(desc); m != nil {
		return idAttrSeparatorWords[strings.ToLower(m[1])], idAttrReadingDelimitedWord, true
	}
	if m := idAttrFormatTokenRe.FindStringSubmatch(desc); m != nil {
		if sep, ok := soleCandidateSeparatorIn(m[1]); ok {
			return sep, idAttrReadingFormatToken, true
		}
	}
	if idAttrCompositionWordRe.MatchString(desc) {
		for _, m := range idAttrQuotedTokenRe.FindAllStringSubmatch(desc, -1) {
			if _, sep, ok := splitSegments(m[1]); ok {
				return sep, idAttrReadingCompositeToken, true
			}
		}
		if sep, ok := adjacentBacktickSeparator(desc); ok {
			return sep, idAttrReadingCompositeAdjacent, true
		}
	}
	return "", "", false
}

// adjacentBacktickSeparator reads the separator from BETWEEN backtick-quoted
// tokens rather than from inside one, for the idiom idAttrQuotedTokenRe's
// per-token reading cannot see: a composite spelled as a literal template of
// quoted segments, "`disk_name`,`instance_name`" or
// "`name`,`domain_name`,`type`,`target`". splitSegments already owns
// everything a single token can state; this owns everything the description
// states between two of them.
//
// It requires EVERY gap between consecutive quoted tokens in desc to reduce,
// once trimmed of whitespace, to the exact same one candidate-separator
// character - not just the gaps inside what a human would call the
// composite list. That is deliberately stricter than "some adjacent pair
// agrees": a description quoting other, unrelated backtick tokens elsewhere
// (an example value, a cross-reference) would otherwise let an unrelated gap
// forge agreement with the real one. Requiring the whole chain to agree
// costs nothing against the measured corpus - every real instance is a
// clean, self-contained list with no other quoted token in the sentence -
// and it means a description this cannot uniquely resolve is refused rather
// than guessed at, same as every other reading here.
func adjacentBacktickSeparator(desc string) (string, bool) {
	idx := idAttrQuotedTokenRe.FindAllStringIndex(desc, -1)
	if len(idx) < 2 {
		return "", false
	}
	var sep string
	for i := 0; i+1 < len(idx); i++ {
		between := strings.TrimSpace(desc[idx[i][1]:idx[i+1][0]])
		if len(between) != 1 || !isCandidateSeparator(between) {
			return "", false
		}
		if sep == "" {
			sep = between
		} else if sep != between {
			return "", false
		}
	}
	return sep, true
}

// soleCandidateSeparatorIn returns the one candidate separator character
// token contains, refusing a token that contains none or more than one
// distinct character.
func soleCandidateSeparatorIn(token string) (string, bool) {
	var found string
	for _, r := range token {
		c := string(r)
		if !isCandidateSeparator(c) {
			continue
		}
		if found != "" && found != c {
			return "", false
		}
		found = c
	}
	return found, found != ""
}
