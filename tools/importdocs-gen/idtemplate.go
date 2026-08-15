// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"strings"
)

// This file is issue #172's extraction half: when a doc's Import section
// shows a full ARN (or https:// URL) as the import ID, the whole string is a
// TEMPLATE, not an opaque value - `arn:aws:sns:us-west-2:123456789012:
// my-topic` decomposes into a scheme/partition/service literal, a region
// slot, an account slot, and a resource tail whose segments come from
// configuration arguments. The flat ComposedOfArguments/Arguments fields
// cannot say any of that (they only ever answer "is the ID argument-
// composed", not "which cloud value fills position 4"), so this parse
// records the per-segment structure as its own artifact field,
// Row.IDTemplate.
//
// Extraction only states what the example shows. Segment attribution is
// fail-closed: a tail segment whose value carries no doc signal linking it
// to an argument is recorded as Unattributed with the raw token, never
// guessed - aws_sns_topic's "my-topic" stays unattributed because the doc's
// own Argument Reference marks `name` Optional (the topic can be
// name_prefix-generated or provider-named), so nothing Required corroborates
// the tail. Each attributed segment names the signal that linked it
// (AttributedBy), because the consumer's confidence bar depends on it - see
// tools/row-gen/importprecedence.go's tryAssembledTemplate for the two-tier
// rule and the measured counterexample (aws_ram_permission) behind it.

// TemplateSegment is one piece of the documented import-ID template, in
// order. Exactly one of Literal, Cloud, Argument or Unattributed is set.
type TemplateSegment struct {
	// Literal is a fixed fragment the template states verbatim, delimiters
	// included ("arn:aws:sns:", ":registry/", "/").
	Literal string `json:"literal,omitempty"`

	// Cloud names a slot filled by the cloud the run points at, in
	// internal/live/identity.CloudValue's own vocabulary: "region" or
	// "account-id". Positional in the ARN grammar (region at position 4,
	// account at position 5), so derivable without guessing.
	Cloud string `json:"cloud,omitempty"`

	// Argument names the configuration argument this tail segment reads,
	// with AttributedBy naming the doc signal that linked them.
	Argument     string `json:"argument,omitempty"`
	AttributedBy string `json:"attributed_by,omitempty"`

	// Unattributed carries the raw example token for a tail segment no doc
	// signal links to an argument - the honest "the extraction cannot say"
	// answer, recorded so the gap is visible rather than silently dropped.
	Unattributed string `json:"unattributed,omitempty"`
}

// IDTemplate is the parsed template: Kind "arn" or "url", Segments in
// left-to-right order. Concatenating the segments' example-time values
// reproduces the documented example string.
type IDTemplate struct {
	Kind     string            `json:"kind"`
	Segments []TemplateSegment `json:"segments"`
}

// AttributedBy values, strongest first. The first two are statements the
// segment's OWN TEXT makes ("domain-id" spells domain_id; "report-group-
// name" is the report group's `name`; "example-repo" abbreviates
// `repository`) - the doc wrote the argument into the placeholder. The last
// two are contextual: the segment is a stand-in for the resource itself
// (every non-filler word is the type's own noun) resolved against the one
// Required name argument, or the segment's value equals the Example Usage
// section's own literal for exactly one argument. tools/row-gen holds
// contextual attributions to a lower confidence tier - see its
// tryAssembledTemplate.
const (
	attrByPlaceholderName   = "placeholder-names-argument"
	attrByPlaceholderAbbrev = "placeholder-abbreviates-argument"
	attrBySelfPlaceholder   = "self-placeholder-required-name"
	attrByExampleValue      = "example-usage-value"
)

// templateFiller is the generic placeholder vocabulary the provider's docs
// pad example values with ("example-webhook", "my-topic", "test-permission",
// "ExampleNameForRealtimeLogConfig"). These words carry no attribution
// signal of their own and are stripped before matching.
var templateFiller = map[string]bool{
	"example": true, "my": true, "sample": true, "test": true,
	"for": true, "a": true, "an": true, "the": true,
}

var (
	arnPartitionRe = regexp.MustCompile(`^aws[a-z-]*$`)
	arnServiceRe   = regexp.MustCompile(`^[a-z0-9-]+$`)
	arnRegionRe    = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d+$`)
	arnAccountRe   = regexp.MustCompile(`^\d{9,12}$`)
)

// idTemplate parses the Import section's example IDs into one agreed
// template, or nil when the examples are not ARN/URL-formed (the ordinary
// short-ID case), disagree with each other structurally, or the string
// cannot be decomposed without guessing. Attribution merges across all the
// section's examples: the identity-block form and the legacy plain-id form
// often carry different example values for the same position
// (aws_codeartifact_domain: "example" vs "tf-acc-test-8593714120730241305"),
// and a signal from either attributes the position, with a disagreement
// between them failing closed to Unattributed.
func idTemplate(section, tfType string, args []ArgumentRefEntry, exampleArgs []ExampleArgument) *IDTemplate {
	cands := importIDCandidates(section)
	if len(cands) == 0 {
		return nil
	}
	if strings.HasPrefix(cands[0], "arn:") {
		return arnTemplate(cands, tfType, args, exampleArgs)
	}
	if strings.HasPrefix(cands[0], "https://") {
		return urlTemplate(cands, tfType, args, exampleArgs)
	}
	return nil
}

// identityBlockValueRe matches the single attribute pair inside an import
// block's identity sub-block, the same shape identityBlockPairRe reads.
var identityBlockValueRe = identityBlockPairRe

// importIDCandidates collects every literal example ID the section shows:
// the legacy console commands, the plain `id = "..."` import-block lines,
// and - new here - a 1.12+ identity block whose single attribute value IS
// the whole ID (`identity = { "arn" = "arn:aws:sns:..." }`). Deduplicated,
// document order.
func importIDCandidates(section string) []string {
	var out []string
	for _, m := range consoleImportRe.FindAllStringSubmatch(section, -1) {
		out = append(out, strings.Trim(m[1], `"`))
	}
	for _, m := range blockImportIDRe.FindAllStringSubmatch(section, -1) {
		out = append(out, m[1])
	}
	for idx := 0; ; {
		i := strings.Index(section[idx:], "identity = {")
		if i == -1 {
			break
		}
		i += idx
		rest := section[i:]
		if end := strings.Index(rest, "}"); end != -1 {
			rest = rest[:end]
		}
		if pairs := identityBlockValueRe.FindAllStringSubmatch(rest, -1); len(pairs) == 1 {
			out = append(out, pairs[0][2])
		}
		idx = i + 1
	}
	seen := map[string]bool{}
	var dedup []string
	for _, c := range out {
		if c != "" && !seen[c] {
			seen[c] = true
			dedup = append(dedup, c)
		}
	}
	return dedup
}

// parsedARN is one candidate example decomposed at the ARN grammar's own
// positions.
type parsedARN struct {
	Partition string
	Service   string
	Region    string // "" for a global-service ARN (aws_cloudfront_*)
	Account   string
	Head      string // the resource-type token ("registry", "report-group"), "" when the tail has none (aws_sns_topic)
	HeadDelim string // the delimiter after Head: "/" or ":"
	Tail      []string
}

// parseARNExample decomposes one example at the ARN grammar's colon
// positions, requiring each fixed-position slot to actually look like what
// the position says it is - a partition, a region (or empty), a numeric
// account. Anything else is not this shape and returns ok=false rather than
// a partial parse.
func parseARNExample(example, tfType string) (parsedARN, bool) {
	parts := strings.SplitN(example, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return parsedARN{}, false
	}
	p := parsedARN{Partition: parts[1], Service: parts[2], Region: parts[3], Account: parts[4]}
	if !arnPartitionRe.MatchString(p.Partition) || !arnServiceRe.MatchString(p.Service) {
		return parsedARN{}, false
	}
	if p.Region != "" && !arnRegionRe.MatchString(p.Region) {
		return parsedARN{}, false
	}
	if !arnAccountRe.MatchString(p.Account) {
		return parsedARN{}, false
	}
	resource := parts[5]
	if resource == "" {
		return parsedARN{}, false
	}
	// The resource part's own leading token is a fixed type marker exactly
	// when it names the TF type's own trailing nouns ("registry" on
	// aws_glue_registry, "report-group" on aws_codebuild_report_group,
	// "deliverystream" on aws_kinesis_firehose_delivery_stream). A head
	// that names something else (aws_codeartifact_domain_permissions_policy's
	// "domain", which is the PARENT's marker) is left in the tail, where
	// attribution decides - fail-closed, not a second guess.
	if i := strings.IndexAny(resource, "/:"); i > 0 && namesOwnTypeTail(resource[:i], tfType) {
		p.Head = resource[:i]
		p.HeadDelim = string(resource[i])
		resource = resource[i+1:]
	}
	if resource == "" {
		return parsedARN{}, false
	}
	p.Tail = strings.Split(resource, "/")
	for _, seg := range p.Tail {
		if seg == "" {
			return parsedARN{}, false
		}
	}
	return p, true
}

// arnTemplate builds the template all ARN-formed candidates agree on. The
// fixed skeleton (partition, service, region presence, head, arity) must be
// identical across candidates; the per-position attribution may come from
// any candidate's value.
func arnTemplate(cands []string, tfType string, args []ArgumentRefEntry, exampleArgs []ExampleArgument) *IDTemplate {
	var parsed []parsedARN
	for _, c := range cands {
		if !strings.HasPrefix(c, "arn:") {
			continue
		}
		p, ok := parseARNExample(c, tfType)
		if !ok {
			return nil
		}
		parsed = append(parsed, p)
	}
	if len(parsed) == 0 {
		return nil
	}
	first := parsed[0]
	for _, p := range parsed[1:] {
		if p.Partition != first.Partition || p.Service != first.Service ||
			(p.Region == "") != (first.Region == "") ||
			p.Head != first.Head || p.HeadDelim != first.HeadDelim ||
			len(p.Tail) != len(first.Tail) {
			return nil
		}
	}

	t := &IDTemplate{Kind: "arn"}
	lead := "arn:" + first.Partition + ":" + first.Service + ":"
	if first.Region == "" {
		t.Segments = append(t.Segments, TemplateSegment{Literal: lead + ":"})
	} else {
		t.Segments = append(t.Segments,
			TemplateSegment{Literal: lead},
			TemplateSegment{Cloud: "region"},
			TemplateSegment{Literal: ":"})
	}
	t.Segments = append(t.Segments, TemplateSegment{Cloud: "account-id"})
	tailLead := ":"
	if first.Head != "" {
		tailLead = ":" + first.Head + first.HeadDelim
	}
	t.Segments = append(t.Segments, TemplateSegment{Literal: tailLead})

	values := make([][]string, len(parsed))
	for i, p := range parsed {
		values[i] = p.Tail
	}
	t.Segments = append(t.Segments, attributeTail(values, tfType, args, exampleArgs)...)
	return t
}

// urlTemplate is the https:// sibling, deliberately narrower: the host is a
// literal (no rule this parse trusts extracts a region or account slot from
// a URL - aws_sqs_queue's documented example uses the legacy region-less
// queue.amazonaws.com host and a non-numeric account, so both slots would be
// guesses), and the path segments go through the same tail attribution. The
// template still records what the example shows; whether any consumer can
// act on a slotless URL template is the consumer's call.
func urlTemplate(cands []string, tfType string, args []ArgumentRefEntry, exampleArgs []ExampleArgument) *IDTemplate {
	type parsedURL struct {
		Host string
		Path []string
	}
	var parsed []parsedURL
	for _, c := range cands {
		if !strings.HasPrefix(c, "https://") {
			continue
		}
		rest := strings.TrimPrefix(c, "https://")
		i := strings.Index(rest, "/")
		if i <= 0 || !strings.Contains(rest[:i], ".") {
			return nil
		}
		p := parsedURL{Host: rest[:i], Path: strings.Split(rest[i+1:], "/")}
		if len(p.Path) == 0 {
			return nil
		}
		for _, seg := range p.Path {
			if seg == "" {
				return nil
			}
		}
		parsed = append(parsed, p)
	}
	if len(parsed) == 0 {
		return nil
	}
	first := parsed[0]
	for _, p := range parsed[1:] {
		if p.Host != first.Host || len(p.Path) != len(first.Path) {
			return nil
		}
	}
	t := &IDTemplate{Kind: "url"}
	t.Segments = append(t.Segments, TemplateSegment{Literal: "https://" + first.Host + "/"})
	values := make([][]string, len(parsed))
	for i, p := range parsed {
		values[i] = p.Path
	}
	t.Segments = append(t.Segments, attributeTail(values, tfType, args, exampleArgs)...)
	return t
}

// attributeTail attributes each tail position across every candidate's
// values, emitting Argument or Unattributed segments joined by "/" literals.
// Bijective: an argument attributes at most one position (left to right),
// and two candidates attributing the same position to different arguments
// fail that position closed.
func attributeTail(values [][]string, tfType string, args []ArgumentRefEntry, exampleArgs []ExampleArgument) []TemplateSegment {
	var out []TemplateSegment
	taken := map[string]bool{}
	arity := len(values[0])
	for pos := 0; pos < arity; pos++ {
		if pos > 0 {
			out = append(out, TemplateSegment{Literal: "/"})
		}
		arg, by, conflict := "", "", false
		for _, v := range values {
			a, b, ok := attributeSegment(v[pos], tfType, args, exampleArgs, taken)
			if !ok {
				continue
			}
			if arg != "" && arg != a {
				conflict = true
			}
			arg, by = a, b
		}
		if arg == "" || conflict {
			out = append(out, TemplateSegment{Unattributed: values[0][pos]})
			continue
		}
		taken[arg] = true
		out = append(out, TemplateSegment{Argument: arg, AttributedBy: by})
	}
	return out
}

// attributeSegment links one tail segment's example value to a
// configuration argument, or refuses. The signals, tried strongest first:
//
//  1. The segment's words spell a documented argument's full name
//     ("domain-id" -> domain_id, "user-profile-name" -> user_profile_name).
//     Required or Optional: a full-name spelling is the doc naming the
//     argument outright.
//  2. After stripping generic filler and the type's own nouns, the words
//     spell a Required argument ("report-group-name" -> name;
//     "ExampleNameForRealtimeLogConfig" -> name; "profile-name" ->
//     user_profile_name via the same own-noun stripping on the argument
//     side). Required-only: a partial spelling needs the stronger pool.
//  3. A single leftover word (>= 4 chars) is the unambiguous prefix of
//     exactly one Required argument ("example-repo" -> repository).
//  4. Nothing is left after stripping and at least one own-type noun was
//     present - the value is a stand-in for the resource itself
//     ("example-webhook", "example-delivery-stream") - resolved against the
//     one Required argument whose own name strips to "name". A type with no
//     such Required argument refuses: aws_sns_topic's Optional `name` is
//     exactly the case this must not guess.
//  5. The segment's value string-equals the doc's own Example Usage literal
//     for exactly one top-level argument (aws_glue_registry's
//     registry_name = "example" against tail "example").
//
// Signals 1-3 are statements the segment's own text makes; 4-5 are
// contextual. AttributedBy records which fired.
func attributeSegment(value, tfType string, args []ArgumentRefEntry, exampleArgs []ExampleArgument, taken map[string]bool) (arg, by string, ok bool) {
	nouns := ownTypeNouns(tfType)
	words, wordsOK := placeholderWords(value)
	if wordsOK {
		full := strings.Join(words, "")
		if a, ok := uniqueMatch(args, taken, false, func(name string) bool {
			return templAlnum(name) == full
		}); ok {
			return a, attrByPlaceholderName, true
		}

		var remaining []string
		hadNoun := false
		for _, w := range words {
			if nouns[w] {
				hadNoun = true
				continue
			}
			if templateFiller[w] {
				continue
			}
			remaining = append(remaining, w)
		}
		rjoin := strings.Join(remaining, "")
		if len(remaining) > 0 {
			if a, ok := uniqueMatch(args, taken, true, func(name string) bool {
				return templAlnum(name) == rjoin || templArgToken(name) == rjoin || stripNounWords(name, nouns) == rjoin
			}); ok {
				return a, attrByPlaceholderName, true
			}
			if len(remaining) == 1 && len(rjoin) >= 4 {
				if a, ok := uniqueMatch(args, taken, true, func(name string) bool {
					n := templAlnum(name)
					return n != rjoin && strings.HasPrefix(n, rjoin)
				}); ok {
					return a, attrByPlaceholderAbbrev, true
				}
			}
		} else if hadNoun {
			if a, ok := uniqueMatch(args, taken, true, func(name string) bool {
				return stripNounWords(name, nouns) == "name"
			}); ok {
				return a, attrBySelfPlaceholder, true
			}
		}
	}

	if len(value) >= 3 {
		documented := map[string]bool{}
		for _, a := range args {
			documented[a.Name] = true
		}
		match := ""
		for _, ea := range exampleArgs {
			if len(ea.Path) != 1 || !ea.IsString || ea.Value != value {
				continue
			}
			name := ea.Path[0]
			if !documented[name] || taken[name] {
				continue
			}
			if match != "" && match != name {
				return "", "", false // two arguments carry this same literal
			}
			match = name
		}
		if match != "" {
			return match, attrByExampleValue, true
		}
	}
	return "", "", false
}

// uniqueMatch returns the one argument satisfying pred, respecting the
// bijection (taken) and, when requiredOnly, the Required pool. Zero or
// several matches refuse.
func uniqueMatch(args []ArgumentRefEntry, taken map[string]bool, requiredOnly bool, pred func(string) bool) (string, bool) {
	match := ""
	for _, a := range args {
		if requiredOnly && !a.Required {
			continue
		}
		if taken[a.Name] || !pred(a.Name) {
			continue
		}
		if match != "" {
			return "", false
		}
		match = a.Name
	}
	return match, match != ""
}

// placeholderWords splits a placeholder value into its lowercase words, on
// hyphen/underscore/dot and camelCase boundaries. ok is false when any part
// is not purely alphabetic - a value carrying digits is an opaque
// identifier, not a placeholder phrase, and word rules must not touch it.
func placeholderWords(value string) ([]string, bool) {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	runes := []rune(value)
	for i, r := range runes {
		switch {
		case r == '-' || r == '_' || r == '.':
			flush()
		case r >= 'A' && r <= 'Z':
			// A camel boundary: an upper after a lower always splits; an
			// upper followed by a lower splits an upper run (SAMLAdfs ->
			// SAML, Adfs).
			if len(cur) > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				if prev >= 'a' && prev <= 'z' || (prev >= 'A' && prev <= 'Z' && nextLower) {
					flush()
				}
			}
			cur = append(cur, r)
		case r >= 'a' && r <= 'z':
			cur = append(cur, r)
		default:
			return nil, false
		}
	}
	flush()
	if len(words) == 0 {
		return nil, false
	}
	return words, true
}

// ownTypeNouns is the TF type's own vocabulary: its underscore tokens minus
// the "aws" prefix.
func ownTypeNouns(tfType string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.Split(strings.TrimPrefix(tfType, "aws_"), "_") {
		if t != "" {
			out[t] = true
		}
	}
	return out
}

// namesOwnTypeTail reports whether head spells a contiguous SUFFIX of the
// type's own tokens ("report-group" against aws_codebuild_report_group,
// "deliverystream" against aws_kinesis_firehose_delivery_stream). Suffix
// only: a mid-token match ("domain" against
// aws_codeartifact_domain_permissions_policy) is the parent's marker, not
// this type's, and stays in the tail.
func namesOwnTypeTail(head, tfType string) bool {
	h := templAlnum(head)
	if h == "" {
		return false
	}
	tokens := strings.Split(strings.TrimPrefix(tfType, "aws_"), "_")
	tail := ""
	for k := 1; k <= len(tokens); k++ {
		tail = templAlnum(tokens[len(tokens)-k]) + tail
		if tail == h {
			return true
		}
		if len(tail) > len(h) {
			return false
		}
	}
	return false
}

// stripNounWords joins an argument name's underscore words minus the type's
// own nouns - the argument-side mirror of the segment-side stripping
// (user_profile_name on aws_sagemaker_user_profile strips to "name").
func stripNounWords(argName string, nouns map[string]bool) string {
	var out []string
	for _, w := range strings.Split(argName, "_") {
		if !nouns[w] {
			out = append(out, w)
		}
	}
	return strings.Join(out, "")
}

// templAlnum and templArgToken mirror tools/row-gen's alnum/argToken
// helpers for this package.
func templAlnum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func templArgToken(name string) string {
	name = strings.TrimSuffix(name, "_id")
	name = strings.TrimSuffix(name, "_arn")
	return templAlnum(name)
}
