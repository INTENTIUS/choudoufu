// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"sort"
	"strings"
)

// candidateSeparators is the closed set of join characters this tool will
// ever report. It is not a guess space: it is exactly the set already live
// in internal/live/identity/table.go's DefaultTable ("/", ":", "_", ",")
// plus Cloud Control's own "|" (issue #44's non-goals note names both sets).
// A character outside this set never becomes a Separator value, however
// confidently a doc's prose might use one - conservatism the issue asks for
// applies to the alphabet itself, not just to uncertain cases within it.
var candidateSeparators = []string{"/", ":", ",", "_", "|"}

func isCandidateSeparator(s string) bool {
	for _, c := range candidateSeparators {
		if s == c {
			return true
		}
	}
	return false
}

// segmentRe matches one plausible identifier-shaped segment of a composite
// import ID: a letter start, then letters/digits/underscore/hyphen. It
// deliberately excludes the four structural separator characters (and "|"),
// so a candidate split that leaves one of them stranded inside a "segment"
// fails validation rather than being accepted on a technicality.
var segmentRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_\-]*$`)

// importSection extracts the "## Import" heading's content, from the
// heading itself to the next "## " (level-2) heading or end of file.
// Sub-headings ("### Identity Schema", "#### Required", ...) stay inside -
// only another level-2 heading closes the section.
func importSection(doc string) (string, bool) {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimRight(l, " \t"), "## Import") {
			start = i
			break
		}
	}
	if start == -1 {
		return "", false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n")), true
}

// argHeadingRe matches the doc's top-level argument-listing heading. Most
// resources use "## Argument Reference"; a handful of older docs pluralize
// it "## Arguments Reference".
var argHeadingRe = regexp.MustCompile(`(?m)^## Arguments? Reference\s*$`)

// bulletNameRe matches one Markdown bullet's leading backtick-quoted name:
// "* `role`  (Required) - ..." or "* `zone_id` - (Required) ...". The
// provider's docs are not consistent about the bullet marker itself - most
// use "*", but a real minority (aws_acm_certificate's Identity Schema
// among them) use "-" instead, so both are accepted.
var bulletNameRe = regexp.MustCompile("(?m)^[*-]\\s+`([A-Za-z0-9_]+)`")

// argumentReferenceNames returns the resource's top-level configuration
// argument names: the bullets under "## Argument Reference", stopping at
// the first "### " sub-heading (a nested block's own arguments, like
// route53_record's "### Alias", are not top-level import-ID material).
func argumentReferenceNames(doc string) []string {
	loc := argHeadingRe.FindStringIndex(doc)
	if loc == nil {
		return nil
	}
	rest := doc[loc[1]:]
	if end := strings.Index(rest, "\n## "); end != -1 {
		rest = rest[:end]
	}
	if end := strings.Index(rest, "\n### "); end != -1 {
		rest = rest[:end]
	}
	return dedupe(bulletNameRe.FindAllStringSubmatch(rest, -1), 1)
}

// identitySchemaRequired returns the provider's own "Identity Schema"
// Required attribute names, when the Import section carries one (issue
// #52: the provider's newer structured identity block, present for most
// but not all resources in v6.58.0). ok is false when the section has no
// such block at all, which the caller treats differently from an empty
// Required list.
func identitySchemaRequired(section string) (required []string, ok bool) {
	idx := strings.Index(section, "### Identity Schema")
	if idx == -1 {
		return nil, false
	}
	rest := section[idx:]
	reqIdx := strings.Index(rest, "#### Required")
	if reqIdx == -1 {
		return nil, true // the heading exists but this doc shapes it differently; nothing to read
	}
	rest = rest[reqIdx:]
	end := len(rest)
	if i := strings.Index(rest, "#### Optional"); i != -1 && i < end {
		end = i
	}
	if i := strings.Index(rest, "\n### "); i != -1 && i < end {
		end = i
	}
	if i := strings.Index(rest, "\n## "); i != -1 && i < end {
		end = i
	}
	return dedupe(bulletNameRe.FindAllStringSubmatch(rest[:end], -1), 1), true
}

func dedupe(matches [][]string, group int) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		name := m[group]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// consoleImportRe matches the legacy `terraform import` console line:
// "% terraform import aws_foo.example the-literal-id".
var consoleImportRe = regexp.MustCompile(`(?m)^%\s*terraform import\s+\S+\s+(.+?)\s*$`)

// blockImportIDRe matches a plain `id = "..."` line at an import block's
// top level (two-space HCL indent). It deliberately does not match the
// four-space indent identity blocks use ("    id = ..." inside
// "identity = {"), so an opaque identity's own `id` attribute is never
// mistaken for the legacy plain-ID import example.
var blockImportIDRe = regexp.MustCompile(`(?m)^  id\s*=\s*"([^"]+)"\s*$`)

// importIDExample extracts the doc's example import ID: the first
// `terraform import` console command's literal argument, or - for a doc
// that only shows the newer import-block style - the first plain `id = `
// line at an import block's top level.
func importIDExample(section string) (string, bool) {
	if m := consoleImportRe.FindStringSubmatch(section); m != nil {
		return strings.Trim(m[1], `"`), true
	}
	if m := blockImportIDRe.FindStringSubmatch(section); m != nil {
		return m[1], true
	}
	return "", false
}

// separatedByRe matches the doc's prose statement of its own separator
// character, in either the parenthesized form ("separated by underscores
// (`_`)") or the bare form ("separated by `/`").
var separatedByRe = regexp.MustCompile("(?i)separated by[^`]{0,60}`([^`]{1,3})`")

// usingBacktickRe matches a backtick-quoted token right after "using",
// optionally through an article ("the", "a", "an"), the format-string
// idiom docs use for a literal ID shape: "using `ROUTETABLEID_DESTINATION`",
// "using the `function_name/alias`".
var usingBacktickRe = regexp.MustCompile("(?i)using\\s+(?:(?:the|an?)\\s+)?`([^`\n]+)`")

// splitSegments tries every candidate separator against token, keeping
// only the ones that yield at least two segments, each of which parses as
// a plausible identifier on its own (segmentRe). Ambiguous tokens - zero
// or more than one candidate separator validates - report ok=false: a
// token this tool cannot uniquely resolve is not evidence for a
// separator, whatever it looks like at a glance.
//
// "_" is only tried against an all-uppercase token (isShoutingToken):
// AWS's own docs write multi-part format placeholders in shouting case
// ("ROUTETABLEID_DESTINATION"), but a real snake_case argument name like
// "function_name" also contains exactly one "_" - splitting *that* would
// misread one argument's own name as two. "/", ":", "," and "|" carry no
// such ambiguity: none of them is ever legal inside a single Terraform
// argument identifier, so they are tried against every token.
func splitSegments(token string) (segments []string, sep string, ok bool) {
	shouting := isShoutingToken(token)
	var matches [][]string
	for _, c := range candidateSeparators {
		if c == "_" && !shouting {
			continue
		}
		if !strings.Contains(token, c) {
			continue
		}
		parts := strings.Split(token, c)
		if len(parts) < 2 {
			continue
		}
		valid := true
		for _, p := range parts {
			if !segmentRe.MatchString(p) {
				valid = false
				break
			}
		}
		if valid {
			matches = append(matches, append([]string{c}, parts...))
		}
	}
	if len(matches) != 1 {
		return nil, "", false
	}
	return matches[0][1:], matches[0][0], true
}

// isShoutingToken reports whether token, with its candidate separators
// removed, is entirely uppercase letters and digits - AWS's documentation
// convention for a format placeholder ("ROUTETABLEID_DESTINATION") as
// opposed to a literal lowercase argument name ("function_name").
func isShoutingToken(token string) bool {
	hasLetter := false
	for _, r := range token {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if r >= 'A' && r <= 'Z' {
				hasLetter = true
			}
		case isCandidateSeparator(string(r)), r == '-':
			// join characters and internal hyphens don't affect casing
		default:
			return false
		}
	}
	return hasLetter
}

// formatToken finds the first "using `TOKEN`" span in section whose token
// splits unambiguously into two or more segments (splitSegments), and
// returns those segments and the separator that split them.
func formatToken(section string) (segments []string, sep string, ok bool) {
	for _, m := range usingBacktickRe.FindAllStringSubmatch(section, -1) {
		if segs, s, ok := splitSegments(m[1]); ok {
			return segs, s, true
		}
	}
	return nil, "", false
}

// singleArgumentToken finds the first "using `TOKEN`" span whose token is
// NOT multi-segment (splitSegments finds no unambiguous split - it is one
// word, however it's spelled) and whose normalized form exactly matches
// one real Argument Reference name. This is the single-argument sibling of
// formatToken: a doc with no Identity Schema that names its one argument
// directly ("using `analyzer_name`") still resolves, the same way
// aws_lambda_function's Identity-Schema-backed "function_name" does,
// without guessing at tokens that don't name a real argument at all (a
// generic "using the `id`" never matches, because "id" is never itself a
// configuration argument).
func singleArgumentToken(section string, argNames []string) (string, bool) {
	for _, m := range usingBacktickRe.FindAllStringSubmatch(section, -1) {
		token := m[1]
		if _, _, ok := splitSegments(token); ok {
			continue // multi-segment tokens are formatToken's to handle
		}
		if matched := matchNames([]string{token}, argNames); len(matched) == 1 {
			return matched[0], true
		}
	}
	return "", false
}

// separatedByClause reads the doc's own "separated by ... (`X`)" sentence
// for two things at once: the separator character itself, and - by
// scanning backward from that sentence to its nearest preceding "using"
// for backtick-quoted, snake_case-shaped tokens - any argument names the
// prose names directly (lb_target_group_attachment: "using
// `target_group_arn`, `target_id`, ... separated by commas (`,`)").
// clauseArgs is nil when the sentence names the ID's parts in plain prose
// instead ("the role name and policy arn"), which this function does not
// attempt to parse into argument names - see identitySchemaRequired and
// formatToken for the two sources that do carry that signal reliably.
var snakeArgRe = regexp.MustCompile("`([a-z][a-z0-9_]*)`")

func separatedByClause(section string) (sep string, clauseArgs []string, ok bool) {
	loc := separatedByRe.FindStringSubmatchIndex(section)
	if loc == nil {
		return "", nil, false
	}
	captured := section[loc[2]:loc[3]]
	if !isCandidateSeparator(captured) {
		return "", nil, false
	}

	windowStart := loc[0] - 400
	if windowStart < 0 {
		windowStart = 0
	}
	window := section[windowStart:loc[0]]
	if u := strings.LastIndex(strings.ToLower(window), "using"); u != -1 {
		window = window[u:]
		clauseArgs = dedupe(snakeArgRe.FindAllStringSubmatch(window, -1), 1)
	}
	return captured, clauseArgs, true
}

// normalize collapses a name to lowercase letters and digits only, so
// "route_table_id", "RouteTableId" and "ROUTETABLEID" compare equal - the
// three casings the provider's own docs and identity schema use across
// different sections of the same page.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchNames returns the subset of names whose normalized form exactly
// matches some entry in pool, preserving names' original order.
func matchNames(names, pool []string) []string {
	set := map[string]bool{}
	for _, p := range pool {
		set[normalize(p)] = true
	}
	var out []string
	for _, n := range names {
		if set[normalize(n)] {
			out = append(out, n)
		}
	}
	return out
}

// fuzzySegmentMatches returns every pool name whose normalized form either
// equals the segment's normalized form, or has it as a prefix (or vice
// versa) - the loose match a format-string segment like "DESTINATION"
// needs to reach "destination_cidr_block" in the Argument Reference.
// Prefix rather than arbitrary substring on purpose: "NAME" as a suffix of
// "filename" is coincidence, not the same concept, and a prefix match
// mirrors how these docs actually abbreviate ("destination" is the head of
// "destination_cidr_block", never its tail). Segments under five
// normalized characters are excluded from the prefix check entirely - a
// short segment like "id" would otherwise prefix-match implausibly many
// argument names.
func fuzzySegmentMatches(segment string, pool []string) []string {
	ns := normalize(segment)
	var out []string
	for _, p := range pool {
		np := normalize(p)
		if np == ns || (len(ns) >= 5 && (strings.HasPrefix(np, ns) || strings.HasPrefix(ns, np))) {
			out = append(out, p)
		}
	}
	return out
}

// composedResult is the pre-render outcome of classifying one type's
// import grammar: the three fields the artifact row cares about, plus
// nothing else - render into Row happens in artifact.go.
type composedResult struct {
	Composed  *bool
	Separator *string
	Arguments []string
}

// classifyGrammar turns one doc's Import section, plus its Argument
// Reference names, into a composedResult. It runs the two signal families
// the package doc comment lays out and merges them:
//
//   - the provider's own Identity Schema Required list, matched against
//     Argument Reference names (issue #52's primary source: an argument
//     the provider's schema requires for import, cross-checked against
//     the provider's own configuration argument set rather than trusted
//     on the identity schema's word alone);
//   - the doc's prose/format-string description of the ID shape
//     ("separated by `/`", "using `ROUTETABLEID_DESTINATION`"), which is
//     the only source multi-part identity schemas like aws_route's
//     (Required: route_table_id; the destination is one of three Optional
//     alternatives) actually name a join character in.
//
// Composed is true only when at least one of the two signals affirms it;
// false only when the Identity Schema signal affirmatively says the whole
// required set is opaque (matches nothing in Argument Reference, the
// aws_kms_key shape); anything less - no Identity Schema and no
// resolvable format signal - is nil, per the issue's instruction that a
// wrong answer is worse than an admitted unknown.
func classifyGrammar(section string, argNames []string) composedResult {
	var result composedResult

	required, hasSchema := identitySchemaRequired(section)
	switch {
	case hasSchema && len(required) > 0:
		matched := matchNames(required, argNames)
		switch {
		case len(matched) == len(required):
			t := true
			result.Composed = &t
			result.Arguments = append(result.Arguments, required...)
		case len(matched) == 0:
			f := false
			result.Composed = &f
		default:
			result.Arguments = append(result.Arguments, matched...)
		}
	}

	sep, clauseArgs, sepOK := separatedByClause(section)
	clauseArgs = matchNames(clauseArgs, argNames)
	segs, fmtSep, fmtOK := formatToken(section)

	if result.Composed == nil || *result.Composed {
		switch {
		case len(clauseArgs) > 0:
			t := true
			result.Composed = &t
			result.Arguments = unionStrings(result.Arguments, clauseArgs)
		case fmtOK && len(segs) >= 2:
			t := true
			result.Composed = &t
			var fuzzy []string
			for _, s := range segs {
				fuzzy = append(fuzzy, fuzzySegmentMatches(s, argNames)...)
			}
			result.Arguments = unionStrings(result.Arguments, fuzzy)
		}
	}

	// Last resort: no Identity Schema, and nothing multi-segment - but the
	// doc still names its one argument directly in backticks
	// ("using `analyzer_name`"). Only fires while Composed is still
	// unresolved: it never overrides an Identity-Schema-derived false
	// (aws_kms_key's "using the `id`" does not match any real argument, so
	// this simply finds nothing there).
	if result.Composed == nil {
		if arg, ok := singleArgumentToken(section, argNames); ok {
			t := true
			result.Composed = &t
			result.Arguments = unionStrings(result.Arguments, []string{arg})
		}
	}

	// The separator is only meaningful once the ID is known to be
	// composed; an opaque or unresolved ID reports no separator even if
	// a stray candidate character showed up in its prose.
	if result.Composed != nil && *result.Composed {
		switch {
		case sepOK:
			result.Separator = &sep
		case fmtOK:
			s := fmtSep
			result.Separator = &s
		}
	}

	sort.Strings(result.Arguments)
	result.Arguments = dedupeStrings(result.Arguments)
	return result
}

func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
