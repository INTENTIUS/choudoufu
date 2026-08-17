// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"strings"
)

// This file resolves the import sentences the backtick-token reader cannot:
// docs that name the one argument the ID is, in plain words or through an
// alias (issue #132's plain-prose family). Real shapes, from the pinned
// cache:
//
//	"import CodeCommit repository using repository name."           -> repository_name
//	"import `aws_appsync_api_cache` using the AppSync API ID."      -> api_id
//	"import Lightsail Instances using their name."                  -> name
//	"import `aws_lightsail_disk` using the name attribute."         -> name
//	"import SageMaker AI Hubs using the `name`."                    -> hub_name
//	"import a subnet group using its `name`."                       -> name
//	"import Backup Framework using the `id` which corresponds to
//	 the name of the Backup Framework."                             -> name
//
// Everything here is anchored to the doc's own Argument Reference: a phrase
// resolves only when it lands on a real configuration argument by
// normalized-exact match (or, for a backticked alias like `name`, by a
// unique suffix of a Required argument's own name). A phrase that names an
// exported attribute instead ("using the queue `url`") is refused outright,
// the same way idParts attributes a segment to the server - and a phrase
// that enumerates several parts ("X and Y", "separated by") is the
// composite family's business, never resolved here.

// proseUsingWord anchors the phrase scan: the sentence's own "using ",
// searched backward from each "For example" anchor.
const proseUsingWord = "using "

// usingPhrases returns the prose between the last "using" and each "For
// example" anchor - the span where these docs state what the import ID is.
// A window that contains no "using" contributes nothing.
func usingPhrases(section string) []string {
	var out []string
	lower := strings.ToLower(section)
	idx := 0
	for {
		i := strings.Index(lower[idx:], "for example")
		if i == -1 {
			return out
		}
		anchor := idx + i
		winStart := anchor - 250
		if winStart < 0 {
			winStart = 0
		}
		window := section[winStart:anchor]
		if u := strings.LastIndex(strings.ToLower(window), proseUsingWord); u != -1 {
			phrase := strings.TrimSpace(window[u+len(proseUsingWord):])
			phrase = strings.TrimRight(phrase, " .:,")
			if phrase != "" {
				out = append(out, phrase)
			}
		}
		idx = anchor + len("for example")
	}
}

// backtickTokenRe matches one backtick-quoted token anywhere in a phrase -
// unlike usingBacktickRe it does not require the token to sit immediately
// after "using"/an article, which is what lets "using the channel group's
// `name`" and "using its `name`" resolve at all.
var backtickTokenRe = regexp.MustCompile("`([^`\n]+)`")

// parenRe matches a parenthesized aside inside a phrase ("the catalog ID
// (usually AWS account ID)"), which is commentary, not part of the name.
var parenRe = regexp.MustCompile(`\([^)]*\)`)

// proseMetaWords are the doc's metalanguage for "this name is an argument/
// attribute", never part of the name itself: "using the name attribute"
// names the argument `name`.
var proseMetaWords = map[string]bool{"attribute": true, "attributes": true, "argument": true, "arguments": true}

// proseArticles are the leading words a plain part's own-noun check drops:
// grammar, not name.
var proseArticles = map[string]bool{"the": true, "a": true, "an": true, "its": true, "their": true}

// proseNamedArgument resolves the Import section's own "using ..." sentences
// to the single configuration argument the documented ID is, when the
// prose names exactly one and the Argument Reference confirms it. The
// caller gates it on Composed still being unresolved: it is a last-resort
// reader for docs whose sentence the structured signals (Identity Schema,
// separated-by clause, format token, direct backtick token) all missed.
func proseNamedArgument(section string, args []ArgumentRefEntry, attrNames []string) (string, bool) {
	argNames := make([]string, len(args))
	var requiredNames []string
	for i, a := range args {
		argNames[i] = a.Name
		if a.Required {
			requiredNames = append(requiredNames, a.Name)
		}
	}
	for _, phrase := range usingPhrases(section) {
		if arg, ok := resolveProsePhrase(phrase, argNames, requiredNames, attrNames); ok {
			return arg, true
		}
	}
	return "", false
}

// resolveProsePhrase resolves one phrase, failing closed on anything that is
// not a single, argument-confirmed name.
func resolveProsePhrase(phrase string, argNames, requiredNames, attrNames []string) (string, bool) {
	// "the `id` which corresponds to the name of the Backup Framework": the
	// sentence itself redirects from the opaque `id` to what it really is.
	if i := strings.Index(strings.ToLower(phrase), "corresponds to"); i != -1 {
		phrase = phrase[i+len("corresponds to"):]
	}
	lower := strings.ToLower(phrase)
	if strings.Contains(lower, " and ") || strings.Contains(lower, "separated by") || strings.Contains(phrase, ",") {
		return "", false // an enumeration or a multi-part statement; the composite signals own those
	}
	// "the name of the Backup Framework": the trailer restates the resource,
	// not the name.
	if i := strings.Index(lower, " of "); i != -1 {
		phrase = phrase[:i]
	}

	tokens := backtickTokenRe.FindAllStringSubmatch(phrase, -1)
	if len(tokens) > 1 {
		return "", false
	}
	if len(tokens) == 1 {
		return resolveBacktickAlias(tokens[0][1], argNames, requiredNames, attrNames)
	}
	return resolvePlainWords(phrase, requiredNames, attrNames)
}

// resolveBacktickAlias resolves a single backticked token the direct reader
// (singleArgumentToken) did not: an exact argument the regex's article rule
// missed ("using its `name`"), or a doc alias that is the unique suffix of
// one Required argument's own name (SageMaker's "using the `name`" against
// image_name). A token the Attribute Reference exports is refused: the doc
// is naming a server value ("using the `id`"), not configuration.
func resolveBacktickAlias(token string, argNames, requiredNames, attrNames []string) (string, bool) {
	n := normalize(token)
	for _, a := range argNames {
		if normalize(a) == n {
			return a, true
		}
	}
	for _, a := range attrNames {
		if normalize(a) == n {
			return "", false // an exported attribute; the ID is a server value
		}
	}
	if len(n) < 4 {
		return "", false // "id"/"arn" would suffix-match nearly anything
	}
	var match string
	for _, a := range requiredNames {
		if strings.HasSuffix(normalize(a), n) {
			if match != "" {
				return "", false // ambiguous: two Required arguments share the suffix
			}
			match = a
		}
	}
	return match, match != ""
}

// plainEnumSplitRe splits a plain-word enumeration on its commas and
// "and"s: "the endpoint ID, target subnet ID, and destination CIDR block".
var plainEnumSplitRe = regexp.MustCompile(`,\s*(?:and\s+)?|\s+and\s+`)

// plainEnumIDParts reads the enumeration idiom the backtick sources cannot:
// an Import sentence naming every segment in plain words ("using the
// primary GuardDuty detector ID and IPSet ID"). Each part resolves the same
// way resolvePlainWords does - longest word-suffix against Required
// arguments, exact against exported attributes - plus one source only
// plain prose carries: a part naming the resource's own noun with "ID"
// ("IPSet ID" on aws_guardduty_ipset's own page) is the resource's own
// server-minted identifier, idPartSourceOwnID. Parts nothing resolves stay
// "unknown"; an enumeration that is all unknown proves nothing and
// produces nothing. The caller holds the arity gate.
func plainEnumIDParts(section, tfType string, args []ArgumentRefEntry, attrNames []string) []IDPart {
	var requiredNames []string
	for _, a := range args {
		if a.Required {
			requiredNames = append(requiredNames, a.Name)
		}
	}
	for _, phrase := range usingPhrases(section) {
		// Only the part list before any "separated by" clause names
		// segments; the clause names the join character.
		if i := strings.Index(strings.ToLower(phrase), "separated by"); i != -1 {
			phrase = strings.TrimSpace(phrase[:i])
		}
		if strings.Contains(phrase, "`") {
			continue // a backticked phrase belongs to the token sources
		}
		raw := plainEnumSplitRe.Split(phrase, -1)
		var parts []string
		for _, p := range raw {
			p = strings.TrimSpace(p)
			if p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) < 2 {
			continue
		}
		out := make([]IDPart, len(parts))
		informative := false
		for i, p := range parts {
			out[i], _ = attributePlainPart(p, tfType, requiredNames, attrNames)
			if out[i].Source != idPartSourceUnknown {
				informative = true
			}
		}
		if informative {
			return out
		}
	}
	return nil
}

// plainEnumComposedArguments reads the same plain-word enumeration
// plainEnumIDParts does ("using the listener arn and certificate arn,
// separated by an underscore") but answers classifyGrammar's question
// instead of idParts': not what each segment is, but whether the WHOLE
// documented ID reconstructs from configuration. It succeeds only when
// every enumerated segment attributes to a Required argument - a mix of
// argument- and server-sourced segments (aws_guardduty_ipset's own "the
// primary GuardDuty detector ID and IPSet ID", where IPSet ID is the
// resource's own server-minted id) is exactly the shape idParts' weaker,
// per-segment attribution exists to carry instead of collapsing into a
// wrong Composed=true. Returns the resolved argument names in the doc's
// own left-to-right order, nil when no phrase resolves completely.
func plainEnumComposedArguments(section, tfType string, args []ArgumentRefEntry, attrNames []string) []string {
	var requiredNames []string
	for _, a := range args {
		if a.Required {
			requiredNames = append(requiredNames, a.Name)
		}
	}
	for _, phrase := range usingPhrases(section) {
		if i := strings.Index(strings.ToLower(phrase), "separated by"); i != -1 {
			phrase = strings.TrimSpace(phrase[:i])
		}
		if strings.Contains(phrase, "`") {
			continue // a backticked phrase belongs to the token sources
		}
		raw := plainEnumSplitRe.Split(phrase, -1)
		var parts []string
		for _, p := range raw {
			p = strings.TrimSpace(p)
			if p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) < 2 {
			continue
		}
		names := make([]string, 0, len(parts))
		complete := true
		for _, p := range parts {
			_, arg := attributePlainPart(p, tfType, requiredNames, attrNames)
			if arg == "" {
				complete = false
				break
			}
			names = append(names, arg)
		}
		if complete {
			return names
		}
	}
	return nil
}

// attributePlainPart attributes one plain-word part, and - only when it
// resolves to idPartSourceArgument - the Required argument name it
// resolved to (empty for every other source, including when nothing
// matches at all).
func attributePlainPart(part, tfType string, requiredNames, attrNames []string) (IDPart, string) {
	words := plainContentWords(part)
	for k := len(words); k >= 1; k-- {
		cand := normalize(strings.Join(words[len(words)-k:], ""))
		if len(cand) < 3 {
			continue
		}
		for _, a := range requiredNames {
			if plainNameMatches(cand, a) {
				return IDPart{Token: part, Source: idPartSourceArgument}, a
			}
		}
		for _, a := range attrNames {
			if plainNameMatches(cand, a) {
				return IDPart{Token: part, Source: idPartSourceAttribute}, ""
			}
		}
	}
	if namesOwnIdentifier(words, tfType) {
		return IDPart{Token: part, Source: idPartSourceOwnID}, ""
	}
	return IDPart{Token: part, Source: idPartSourceUnknown}, ""
}

// plainNameMatches reports whether a normalized word-run from the prose names
// the argument arg.
//
// Exact equality is the base case and was the whole rule. It misses the two
// ways documentation prose routinely differs from an argument name, both of
// them properties of English rather than of any one type:
//
//   - the prose adds a generic head noun the argument leaves off. "the user
//     name" for `user`, "the analysis ID" for `analysis`. The sentence needs
//     a noun; the schema name is the noun already.
//   - the prose pluralizes where the argument does not, or the reverse.
//     "group names" for `groups`.
//
// Both are applied to the CANDIDATE only, never to the argument, and only
// after exact equality has failed. The argument name is the ground truth and
// is never rewritten to meet the prose halfway.
//
// The bar for widening this at all: a wrong marker outranks a missing one, so
// a looser match that attributes a segment to the wrong argument is worse
// than no attribution. Neither rule can attribute a segment to an argument
// that does not exist, and the caller still requires every part to resolve
// and every resolved name to be distinct before it uses any of them.
func plainNameMatches(cand, arg string) bool {
	want := normalize(arg)
	if want == "" || cand == "" {
		return false
	}
	for _, form := range []string{cand, trimGenericHead(cand)} {
		if form == "" {
			continue
		}
		if form == want || singularEqual(form, want) {
			return true
		}
	}
	return false
}

// genericHeads are the nouns documentation appends to a schema name to make a
// sentence read. They are only ever REMOVED from the end of a candidate, and
// only when something is left, so "the name" and "the id" cannot collapse to
// nothing and match everything.
var genericHeads = []string{"identifiers", "identifier", "names", "name", "ids", "id", "arns", "arn", "numbers", "number", "values", "value"}

func trimGenericHead(cand string) string {
	for _, head := range genericHeads {
		if len(cand) > len(head) && strings.HasSuffix(cand, head) {
			return cand[:len(cand)-len(head)]
		}
	}
	return ""
}

// singularEqual reports whether two normalized names differ only by a
// trailing "s" or "es". It is deliberately this crude: an English pluralizer
// would introduce its own misfires, and the only forms observed in the
// provider's own import prose are the regular ones.
func singularEqual(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	switch b {
	case a + "s", a + "es":
		return true
	}
	return false
}

// namesOwnIdentifier reports whether a run of words names the resource's
// own identifier: strip leading articles and the trailing "id", and match
// the remainder against the tail of the type's own name - "IPSet ID" ->
// "ipset" == aws_guardduty_ipset's last token,
// "PublishingDestinationID" -> aws_guardduty_publishing_destination's
// last two, "the alias ID" -> aws_bedrockagent_agent_alias's last one.
//
// A resource does not configure its own ID, so a phrase that passes here
// is naming a server-provided value. soleid.go reads the same test over
// an exported attribute's description rather than over import prose.
func namesOwnIdentifier(words []string, tfType string) bool {
	for len(words) > 0 && proseArticles[strings.ToLower(words[0])] {
		words = words[1:]
	}
	base := strings.TrimSuffix(normalize(strings.Join(words, "")), "id")
	if base == "" {
		return false
	}
	typeTokens := strings.Split(strings.TrimPrefix(tfType, "aws_"), "_")
	tail := ""
	for k := 1; k <= len(typeTokens); k++ {
		tail = normalize(typeTokens[len(typeTokens)-k]) + tail
		if tail == base {
			return true
		}
		if len(tail) > len(base) {
			break
		}
	}
	return false
}

// plainContentWords splits a part into its content words: parenthesized
// asides removed, the doc's meta words dropped, articles and qualifiers
// left in place (the suffix scan skips them naturally).
func plainContentWords(part string) []string {
	part = parenRe.ReplaceAllString(part, " ")
	var words []string
	for _, w := range strings.Fields(part) {
		if !proseMetaWords[strings.ToLower(w)] {
			words = append(words, w)
		}
	}
	return words
}

// resolvePlainWords resolves a phrase with no backticks at all ("repository
// name", "the AppSync API ID", "their name", "the name attribute"): the
// longest word-suffix of the phrase whose normalized concatenation exactly
// matches one REQUIRED Argument Reference name wins. Required-only is the
// discipline that keeps three real misfires out: "using the region"
// (aws_cloudwatch_otel_enrichment - `region` is the provider-wide Optional
// meta-argument every v6 page lists, not this resource's identity) and
// "using the provisioned product ID" (aws_servicecatalog_provisioned_product
// - the phrase names the resource's own server-minted ID, and the Optional
// `product_id` argument its tail happens to spell is a different thing).
// An identity a doc states in loose prose is only trusted when it lands on
// an argument the resource cannot be created without. A suffix that matches
// an Attribute Reference name and no argument refuses the phrase - "using
// the queue url" names a server value.
func resolvePlainWords(phrase string, requiredNames, attrNames []string) (string, bool) {
	// A separator character inside a plain phrase means the sentence names
	// a multi-part ID without an "and" ("using the owner directory
	// ID/shared directory ID", aws_directory_service_shared_directory) -
	// the composite family's business, and reading only its tail would
	// claim one part as the whole.
	if strings.ContainsAny(phrase, "/:,|") {
		return "", false
	}
	// "the Amazon Resource Name (ARN)": the spelled-out ARN idiom. Its
	// trailing word is "name", which would suffix-match a resource's own
	// `name` argument (aws_imagebuilder_component), so it is refused before
	// any matching - an ARN is only ever a server value here.
	if strings.Contains(normalize(phrase), "amazonresourcename") {
		return "", false
	}
	// A parenthesized aside that names an exported attribute ("(ARN)")
	// says what the whole phrase is - a server value - even when the prose
	// around it spells something else.
	for _, paren := range parenRe.FindAllString(phrase, -1) {
		inner := normalize(paren)
		for _, a := range attrNames {
			if normalize(a) == inner {
				return "", false
			}
		}
	}
	phrase = parenRe.ReplaceAllString(phrase, " ")
	rawWords := strings.Fields(phrase)
	var words []string
	for _, w := range rawWords {
		if !proseMetaWords[strings.ToLower(w)] {
			words = append(words, w)
		}
	}
	for k := len(words); k >= 1; k-- {
		cand := normalize(strings.Join(words[len(words)-k:], ""))
		if len(cand) < 3 {
			continue
		}
		for _, a := range requiredNames {
			if normalize(a) == cand {
				return a, true
			}
		}
		for _, a := range attrNames {
			if normalize(a) == cand {
				return "", false // names an exported attribute, not configuration
			}
		}
	}
	return "", false
}
