// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/intentius/choudoufu/internal/live/markerkey"
)

// VocabularyRefusal reports why this fork's marker vocabulary cannot survive
// a round trip through a key/value map the provider has documented, or
// refused=false when nothing the provider says stands in its way.
//
// This is the question [Taggable] always claimed to answer and did not. The
// old predicate tested the shape of a "tags" attribute - top-level,
// settable, map(string) - and treated that shape as permission to write a
// marker into it. The shape is not permission. At hashicorp/aws 6.59.0 a
// "tags" map of that shape is free-form and a marker is a legal entry; at
// hashicorp/google 7.44.0 the identically-shaped attribute holds
// resource-manager tag bindings, where a key names a TagKey object that
// already exists and a value names a TagValue. The schema type is the same
// down to the element type. Writing "tofu-estate" into the second kind is
// rejected by the API, and on the several types whose description calls the
// field immutable a later marker rewrite - live-mv, or the stale-address
// repair discovery depends on - forces the resource to be replaced.
// GitHub issue #243.
//
// # Why it reads the description
//
// Because that is the only channel that carries this. configschema.Attribute
// has Type, Required/Optional/Computed, Sensitive, Deprecated and WriteOnly,
// and none of them distinguishes a vocabulary from a free-form map. There is
// no ForceNew or requires-replacement flag on an attribute at plugin
// protocol 5 or 6 - checked, not assumed - so "this field forces
// replacement", which is the fact that makes the wrong write destructive,
// exists in the schema only as English in a Description.
//
// # What it tests
//
// Not "is this attribute documented". A provider that writes a doc string
// for a free-form map must keep it, and "documented, therefore refused"
// would be the inverse of a useful signal - it would refuse tfe's genuinely
// free-form tags map and, the day the AWS provider starts describing its own,
// all 847 of those.
//
// What it tests is whether the marker vocabulary fits inside the vocabulary
// the provider has written down, on the provider's own terms:
//
//   - a marker key is a single unnamespaced word ("tofu-estate"). A provider
//     that documents keys as namespaced - a template or an example with a
//     separator, "tagKeys/{tag_key_id}", "123/environment" - has a key space
//     no marker key inhabits.
//   - a marker value is drawn from letters, digits, space and
//     [markerkey.Extras], and runs to [MaxTagValue] characters. A provider
//     that documents a character class or a length bound the marker cannot
//     satisfy has a value space no marker value inhabits.
//
// Both clauses are about the marker grammar meeting a written rule, so
// neither needs to know which provider it is reading, and a provider nobody
// has thought about yet gets the same treatment as the two that prompted
// this.
//
// The returned reason quotes the provider back, because a refusal that does
// not say why sends the reader looking for a typo in their own file.
func VocabularyRefusal(desc string) (string, bool) {
	if strings.TrimSpace(desc) == "" {
		return "", false
	}
	flat := flattenDescription(desc)
	if reason, ok := namespacedKeySpace(flat); ok {
		return reason, true
	}
	if reason, ok := unspellableMarker(flat); ok {
		return reason, true
	}
	return "", false
}

// flattenDescription collapses the runs of whitespace a provider's Go string
// literal leaves in a description into single spaces, so a clause broken
// across source lines ("Keys\nmust be in the format") reads as one run of
// text. The proximity rules below measure distance in characters and would
// otherwise depend on where the provider's author happened to wrap.
func flattenDescription(desc string) string {
	return strings.Join(strings.Fields(desc), " ")
}

// ---------------------------------------------------------------------------
// Clause one: a namespaced key space.
// ---------------------------------------------------------------------------

// keyWord finds the noun the first clause is about. The word boundary
// matters: "monkey" is not a key, and neither is the "key" inside
// "tagKeys/123", which is part of a form rather than a claim about one.
var keyWord = regexp.MustCompile(`(?i)\bkeys?\b`)

// requirementPhrase is a provider stating that something has to take a
// particular form. Kept to phrasings that constrain rather than describe:
// "must be" constrains, "are used for" does not.
var requirementPhrase = regexp.MustCompile(`(?i)\b(must|has to|have to|need(s)? to|can only|may only|should) (be|match|start|begin|have|use|contain|conform)\b|\bcan be either\b|\b(are|is) in the format\b|\bin the format\b`)

// namespacedForm is a key written as namespace and name, joined by a
// separator: "tagKeys/{tag_key_id}", "tagKeys/123", "123/environment". A
// marker key has no such separator - it is one word - so a key space
// described this way has no room for one.
//
// The braces are allowed inside a segment so a template's placeholder counts
// as a segment rather than breaking the match.
var namespacedForm = regexp.MustCompile(`[A-Za-z0-9_.{}-]+/[A-Za-z0-9_.{}-]+`)

// identifierish is what separates a key form from an English phrase that
// happens to contain a slash. "key/value pairs" and "name/value" are two
// words joined by a slash meaning "or"; they say nothing about a key space,
// and reading them as one refuses maps that work. A form that names an
// actual key carries a placeholder or a numeric identifier, because that is
// what a namespaced key is made of - "tagKeys/{tag_key_id}",
// "tagKeys/123", "123/environment".
var identifierish = regexp.MustCompile(`[0-9{}]`)

// urlPrefix keeps a documentation link from reading as a namespaced key.
// Providers put links in descriptions, and a link is not a claim about a
// key.
var urlPrefix = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://`)

// exampleEntry is a literal map entry written out in a description:
// `"123/environment": "production"`. A provider that shows what a key looks
// like has documented the key space as firmly as one that states a rule, and
// at least one type documents it only this way - no rule anywhere in its
// text, just two example bindings.
var exampleEntry = regexp.MustCompile(`"([^"\n]{1,120})"\s*:\s*"`)

// keyClauseWindow is how far after the word "key" a stated form may sit and
// still be read as a statement about that key. Generous enough to span
// "Keys must be in the format tagKeys/{tag_key_id}, and values are in the
// format tagValues/{tag_value_id}" from its first word, short enough that a
// form belonging to a later, unrelated sentence is not attributed to it.
const keyClauseWindow = 160

// namespacedKeySpace reports whether the provider has described its keys as
// namespaced, either by stating the form or by showing one.
func namespacedKeySpace(flat string) (string, bool) {
	for _, loc := range keyWord.FindAllStringIndex(flat, -1) {
		end := loc[1] + keyClauseWindow
		if end > len(flat) {
			end = len(flat)
		}
		window := flat[loc[0]:end]
		if !requirementPhrase.MatchString(window) {
			continue
		}
		if form, ok := namespaced(window); ok {
			return fmt.Sprintf(
				"the provider describes this map's keys as namespaced (%q, from %q), and an ownership marker key such as %q is a single unnamespaced word",
				form, strings.TrimSpace(window), TagEstate), true
		}
	}
	// A shown key, with no rule stated anywhere. A free-form map's example
	// key is a plain label - "team", "environment" - and matching those
	// would refuse maps that work; only a shown key that is itself
	// namespaced counts.
	for _, m := range exampleEntry.FindAllStringSubmatch(flat, -1) {
		if form, ok := namespaced(m[1]); ok {
			return fmt.Sprintf(
				"the provider's own example key for this map is namespaced (%q), and an ownership marker key such as %q is a single unnamespaced word",
				form, TagEstate), true
		}
	}
	return "", false
}

// namespaced reports whether the text carries a key written as namespace and
// name, and returns it.
func namespaced(s string) (string, bool) {
	for _, loc := range namespacedForm.FindAllStringIndex(s, -1) {
		form := s[loc[0]:loc[1]]
		if !identifierish.MatchString(form) {
			continue
		}
		start := loc[0] - 8
		if start < 0 {
			start = 0
		}
		if urlPrefix.MatchString(s[start:loc[1]]) {
			continue
		}
		return form, true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Clause two: a value space the marker cannot be spelled in.
// ---------------------------------------------------------------------------

// documentedPattern is a character class with an optional repetition bound,
// as a provider writes one when it quotes the service's own validation rule:
// "[\p{Ll}\p{Lo}][\p{Ll}\p{Lo}\p{N}_-]{0,62}". Go's regexp understands the
// same Unicode class syntax, so the provider's pattern is compiled and run
// rather than interpreted by hand.
//
// Whitespace is excluded from a class because a character class does not
// contain any, and a markdown link label does: "[Tag definitions]" compiles
// perfectly well as a class of the letters in "Tag definitions" and means
// nothing of the kind.
var documentedPattern = regexp.MustCompile(`(?:\[(?:[^\[\]\\\s]|\\.)+\](?:\{\d+(?:,\d+)?\})?)+`)

// wholeStringRule distinguishes a pattern that governs an entire key or
// value from one that governs a position in it. "must conform to the
// following PCRE regular expression: [...]{0,62}" bounds the whole string
// and its quantifier says so; "begin and end with an alphanumeric character
// ([a-z0-9A-Z])" bounds one character, and testing a whole marker key
// against it would refuse a vocabulary that in fact admits it. A pattern
// with no quantifier counts only where the provider's words make it
// exhaustive.
var wholeStringRule = regexp.MustCompile(`(?i)\b(only contain|contain only|consist(s|ing)? (only )?of|only be|may only)\b`)

// repetitionBound pulls the upper bound off the last quantifier in a
// pattern, which is where a documented length cap lives.
var repetitionBound = regexp.MustCompile(`\{(\d+)(?:,(\d+))?\}`)

// patternWindow is how far before a pattern the word it governs may sit.
// "Label values must be between 0 and 63 characters long, have a UTF-8
// encoding of maximum 128 bytes, and must conform to the following PCRE
// regular expression: [\p{Ll}\p{Lo}\p{N}_-]{0,63}" is 150 characters from
// "values" to the class.
const patternWindow = 220

// unspellableMarker reports whether a documented pattern excludes the
// characters or the length a marker needs.
//
// The marker value is the demanding half: it carries an escaped resource
// address, whose segment separators are "." and ":" (see [EscapeAddress]),
// and it may run to [MaxTagValue] characters. A vocabulary that admits only
// lowercase letters, digits, "_" and "-" up to 63 characters - which is
// what GCP's label rule says, in the provider's own words - can represent
// neither.
func unspellableMarker(flat string) (string, bool) {
	for _, loc := range documentedPattern.FindAllStringIndex(flat, -1) {
		pattern := flat[loc[0]:loc[1]]
		re, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			// Not a pattern after all; a bracketed aside, or a syntax Go's
			// regexp does not share. Nothing to conclude from it.
			continue
		}
		start := loc[0] - patternWindow
		if start < 0 {
			start = 0
		}
		context := strings.ToLower(flat[start:loc[0]])
		governsKeys := strings.Contains(context, "key")
		governsValues := strings.Contains(context, "value")
		if !governsKeys && !governsValues {
			continue
		}
		if !repetitionBound.MatchString(pattern) && !wholeStringRule.MatchString(context) {
			// A rule about one position, not about the whole string.
			continue
		}
		if governsKeys {
			for _, key := range markerTagKeys() {
				if !re.MatchString(key) {
					return fmt.Sprintf(
						"the provider documents this map's keys as matching %s, which the ownership marker key %q does not",
						pattern, key), true
				}
			}
		}
		if governsValues {
			if bad, ok := unrepresentableRune(re); ok {
				return fmt.Sprintf(
					"the provider documents this map's values as matching %s, which cannot spell %q - a character an escaped resource address uses as a separator",
					pattern, string(bad)), true
			}
			if cap, ok := upperBound(pattern); ok && cap < MaxTagValue {
				return fmt.Sprintf(
					"the provider caps this map's values at %d characters (%s), and one ownership marker value may run to %d",
					cap, pattern, MaxTagValue), true
			}
		}
	}
	return "", false
}

// markerTagKeys is every tag key this fork writes. A surface that cannot
// spell all of them cannot carry a marker, since the address tag and its
// continuations are read together.
func markerTagKeys() []string {
	keys := []string{TagEstate, TagAddress, TagSlot}
	for n := 2; n <= MaxContinuations; n++ {
		keys = append(keys, ContinuationTag(n))
	}
	return keys
}

// unrepresentableRune returns the first character the marker value alphabet
// needs that the documented pattern rejects.
//
// The separators come first deliberately. "." and ":" are what
// [EscapeAddress] writes between an address's segments and around an
// instance key, so a vocabulary missing either cannot round-trip an address
// at all, whatever else it admits - that is a stronger statement than "some
// for_each key would need escaping" and it is the one worth reporting.
func unrepresentableRune(re *regexp.Regexp) (rune, bool) {
	alphabet := []rune(".:" + markerkey.Extras + "abzABZ09 ")
	for _, r := range alphabet {
		if !re.MatchString(string(r)) {
			return r, true
		}
	}
	return 0, false
}

// upperBound reads the length cap off a documented pattern's last
// quantifier. A pattern with no quantifier bounds nothing.
func upperBound(pattern string) (int, bool) {
	ms := repetitionBound.FindAllStringSubmatch(pattern, -1)
	if len(ms) == 0 {
		return 0, false
	}
	last := ms[len(ms)-1]
	text := last[2]
	if text == "" {
		text = last[1]
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}
	// Every class before the last contributes at least one character, which
	// is how "[\p{Ll}\p{Lo}][\p{Ll}\p{Lo}\p{N}_-]{0,62}" comes to 63.
	return n + strings.Count(pattern, "[") - 1, true
}
