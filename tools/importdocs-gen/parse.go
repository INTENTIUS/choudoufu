// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/uniquename"
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

// subHeadingRe matches the first sub-heading of any depth ("### Alias",
// "#### Cache Behavior Arguments", "##### Forwarded Values Arguments") -
// the boundary that separates a section's own top-level bullets from a
// nested block's. Depth matters: aws_cloudfront_distribution nests its
// block arguments under #### and ##### with no ### at all, and a boundary
// that only knew "### " leaked more than a hundred nested names (a fake
// top-level `id` among them) into the pool every match here reads.
var subHeadingRe = regexp.MustCompile(`(?m)^#{3,6} `)

// topLevelSpan cuts rest down to the section's own top-level content: up
// to the next "## " heading and before the first sub-heading of any depth.
func topLevelSpan(rest string) string {
	if end := strings.Index(rest, "\n## "); end != -1 {
		rest = rest[:end]
	}
	if loc := subHeadingRe.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return rest
}

// argumentReferenceNames returns the resource's top-level configuration
// argument names: the bullets under "## Argument Reference", stopping at
// the first sub-heading (a nested block's own arguments, like
// route53_record's "### Alias", are not top-level import-ID material).
func argumentReferenceNames(doc string) []string {
	loc := argHeadingRe.FindStringIndex(doc)
	if loc == nil {
		return nil
	}
	return dedupe(bulletNameRe.FindAllStringSubmatch(topLevelSpan(doc[loc[1]:]), -1), 1)
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

// identitySchemaOptional returns the provider's own "Identity Schema"
// Optional attribute names - identitySchemaRequired's sibling, over the same
// "### Identity Schema" block's "#### Optional" list. Unlike
// identitySchemaRequired this carries no separate "ok" return: an absent
// block or an absent Optional list are both simply "nothing here", since
// nothing downstream needs to tell them apart the way Composed's
// true/false/nil three-way split needs identitySchemaRequired's own ok.
func identitySchemaOptional(section string) []string {
	idx := strings.Index(section, "### Identity Schema")
	if idx == -1 {
		return nil
	}
	rest := section[idx:]
	optIdx := strings.Index(rest, "#### Optional")
	if optIdx == -1 {
		return nil
	}
	rest = rest[optIdx:]
	end := len(rest)
	if i := strings.Index(rest, "\n### "); i != -1 && i < end {
		end = i
	}
	if i := strings.Index(rest, "\n## "); i != -1 && i < end {
		end = i
	}
	return dedupe(bulletNameRe.FindAllStringSubmatch(rest[:end], -1), 1)
}

// attrHeadingRe matches the doc's top-level attribute-listing heading, the
// Attribute Reference sibling of argHeadingRe: most resources use
// "## Attribute Reference", older docs pluralize it "## Attributes
// Reference".
var attrHeadingRe = regexp.MustCompile(`(?m)^## Attributes? Reference\s*$`)

// attributeReferenceNames returns the resource's top-level exported
// attribute names: the bullets under "## Attribute Reference", with the same
// boundary rule argumentReferenceNames holds itself to (stop at the next
// "## " heading and at the first "### " sub-heading - a nested block's own
// attributes are never import-ID material). These are the names the doc
// itself declares server-provided ("In addition to all arguments above, the
// following attributes are exported"), which is what lets an Import
// section's own prose token be attributed to the server rather than to
// configuration - see idParts.
func attributeReferenceNames(doc string) []string {
	loc := attrHeadingRe.FindStringIndex(doc)
	if loc == nil {
		return nil
	}
	return dedupe(bulletNameRe.FindAllStringSubmatch(topLevelSpan(doc[loc[1]:]), -1), 1)
}

// forceNewPhraseRe matches either spelling the provider's docs use for a
// ForceNew argument's own parenthetical marker: the literal SDK term
// "ForceNew" itself (a handful of resources spell it this way verbatim) or
// the far more common human-readable "Forces new resource"/"Forces new
// resources".
var forceNewPhraseRe = regexp.MustCompile(`(?i)forcenew|forces new resources?`)

// argEntryRe matches one Argument Reference bullet's backtick-quoted name
// and, when present, its full parenthetical - allowing for the doc's
// inconsistent "- (X)", ": (X)" or bare " (X)" separator between the name
// and the paren, and the occasional doubled space. The parenthetical itself
// is optional: a bullet with no leading marker at all (rare) still yields a
// name, just with Required/ForceNew left at their zero value rather than
// guessed.
var argEntryRe = regexp.MustCompile("(?m)^[*-]\\s+`([A-Za-z0-9_]+)`(?:\\s*[-:]?\\s*\\(([^)]*)\\))?")

// ArgumentRefEntry is one Argument Reference bullet: a top-level
// configuration argument's name, whether the doc marks it (Required) as
// opposed to (Optional) - the doc's own binary, not inferred - and whether
// its parenthetical carries a ForceNew/"Forces new resource" annotation.
type ArgumentRefEntry struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	ForceNew bool   `json:"force_new,omitempty"`

	// ServerAssignedIfAbsent is true when the bullet's own prose - not just
	// its parenthetical - states that the provider (or Terraform itself)
	// assigns this argument a value when the configuration omits it: "If
	// omitted, Terraform will assign a random, unique name", "Generated by
	// Terraform if not provided", "If missing, will generate a random,
	// unique id". See serverAssignedIfAbsent for the two-part test (an
	// omission clause and a generation clause, both required) that keeps
	// this off a bullet that merely names an unrelated fallback ("Will
	// manage current user's account by default if omitted" has the
	// omission clause with no generation clause; "If omitted, AWS assigns
	// the `Default` opt-out list" names a literal fallback, not a freshly
	// generated value, and is scraped separately by OmittedFallbacks
	// instead). Always false for a Required argument: omission is not a
	// state a Required argument's own value can be in.
	ServerAssignedIfAbsent bool `json:"server_assigned_if_absent,omitempty"`

	// DeclaredUnique is true when the bullet's own prose states that the
	// value the configuration supplies for this argument is unique across
	// the account and region a run is pointed at: "Unique name used to
	// identify the cache policy", "This name must be unique within your AWS
	// account".
	//
	// It is the provider-documentation half of the two-source evidence
	// GitHub issue #272 requires before a live object may be bound by
	// matching this argument against a listing, and it is deliberately only
	// half. The other half is the CloudFormation registry schema's own
	// description of the same property, which registry-gen records as
	// UniqueNameProperty. Neither source is acted on alone: at hashicorp/aws
	// 6.59.0 this flag is true on the `name` argument of 111 resource types
	// and the registry agrees about 9 of them.
	//
	// The judgement is [uniquename.Asserted]'s, shared verbatim with
	// registry-gen so the two texts are read by one rule rather than two
	// that could drift. Its doc comment carries every phrasing the rule
	// deliberately refuses.
	DeclaredUnique bool `json:"declared_unique,omitempty"`

	// CloudDefault is set when the bullet's own prose states that omitting
	// this argument selects a property of the cloud the run is pointed at
	// rather than a value the configuration could name: "account-id" for
	// "If omitted, this defaults to the AWS Account ID", "region" for
	// "Defaults to the Region set in the provider configuration". It is the
	// missing half of a cloud-derived identity component - the account is
	// what the object is named under when the argument is absent, and the
	// argument is what it is named under when the argument is present - and
	// tools/row-gen's mergeCloudDefault reads it to fill in
	// identity.Component.Attrs on a component that already carries
	// identity.Component.Cloud.
	//
	// The values are deliberately identity.CloudAccountID's and
	// identity.CloudRegion's own strings, so the merge is a lookup rather
	// than a translation table this file and that one both have to agree
	// about. Always empty for a Required argument: a required argument has
	// no omitted case to default from. See cloudDefault for the test.
	CloudDefault string `json:"cloud_default,omitempty"`
}

// accountDefaultRe matches the doc's own statement that omitting an argument
// selects the caller's own AWS account ID, in the three ways the provider's
// docs phrase it across v6.59.0: "If omitted, this defaults to the AWS
// Account ID", "Defaults to the account ID the AWS provider is currently
// connected to", "If none is supplied, the AWS account ID is used by
// default". The default clause and the account clause have to be in the same
// phrase, which is what keeps this off a bullet that merely describes an
// account-valued argument with no default at all (aws_codeartifact_*'s
// domain_owner, "The account number of the AWS account that owns the
// domain", is such a bullet, and it is correctly not a cloud default: the
// docs do not say what omitting it does).
// The optional "automatically determined" is the S3 Control family's
// phrasing of the same fact - "Defaults to automatically determined account
// ID of the Terraform AWS provider" - and it went unread, so every row whose
// account segment is documented that way emitted an argument component with
// no cloud fallback and refused "Identity argument not set" on any
// configuration that omitted the argument. It is admitted as a fixed adjectival
// phrase rather than a bounded gap on purpose: a `[^.]{0,N}` wildcard here
// would also swallow "defaults to the account id of the bucket owner", which
// is a different account and would put the wrong one in a marker.
var accountDefaultRe = regexp.MustCompile(`(?i)(defaults?\s+to\s+(the\s+)?(automatically\s+determined\s+)?(aws\s+)?account\s*id|(aws\s+)?account\s*id\s+is\s+used\s+by\s+default)`)

// regionDefaultRe is accountDefaultRe for the region: the phrase has to name
// the provider, which is what separates the AWS provider's per-resource
// region override ("Defaults to the Region set in the provider
// configuration", on 1488 of the 1699 v6.59.0 resource docs, plus four
// wordings of the same sentence) from an unrelated region-valued default
// that happens to start the same way ("Defaults to the region's default
// VPC", which names no provider before the sentence ends).
var regionDefaultRe = regexp.MustCompile(`(?i)defaults?\s+to\s+the\s+region\b[^.]{0,60}\bprovider\b`)

// cloudDefault reports which cloud property an Argument Reference bullet's
// full text documents as the value the provider uses when the configuration
// omits the argument, or "" for the overwhelming majority of bullets that
// document no such thing.
func cloudDefault(body string) string {
	switch {
	case accountDefaultRe.MatchString(body):
		return "account-id"
	case regionDefaultRe.MatchString(body):
		return "region"
	default:
		return ""
	}
}

// serverAssignedOmitRe matches the doc's own statement that an argument's
// bullet is talking about the omitted-value case, in the handful of ways the
// provider's docs phrase it: "if omitted", "if not specified/provided/set",
// "if missing".
var serverAssignedOmitRe = regexp.MustCompile(`(?i)\bif\s+(omitted|missing|not\s+(specified|provided|set))\b`)

// serverAssignedGenRe matches the doc's own statement that the omitted
// value gets a freshly generated one, as opposed to falling back to an
// existing or literal default: "will assign a random ...", "will
// generate ...", "Generated by Terraform ...", "generated automatically",
// "autogenerate a name". Deliberately narrower than the bare words
// "generate"/"random" alone - "a random 30-minute window" (a scheduling
// default, not an identity) and "the generated map" (an unrelated nested
// concept) both contain "generat.." or "random" without this shape, and
// neither should count.
var serverAssignedGenRe = regexp.MustCompile(`(?i)(assign\w*\s+a\s+random|will\s+generate|generat\w*\s+(?:by|automatically)|auto-?generat\w*)`)

// serverAssignedIfAbsent reports whether an Argument Reference bullet's full
// text (parenthetical and prose both - see argumentReferenceBodies) states
// the auto-generation convention documented across dozens of AWS resource
// types: both an omission clause and a generation clause have to be present,
// since either alone is common prose that means something else (see the two
// regexes' own doc comments for the false positives this rules out).
func serverAssignedIfAbsent(body string) bool {
	return serverAssignedOmitRe.MatchString(body) && serverAssignedGenRe.MatchString(body)
}

// argumentReferenceBodies returns each top-level Argument Reference bullet's
// full text, name to body, keyed the same way argumentReferenceEntries keys
// its own return - first occurrence wins on a duplicate name. Unlike
// argEntryRe (which stops at the bullet's own parenthetical), this follows a
// bullet across its continuation lines - the common shape where the
// auto-generation sentence is its own indented line right after the
// parenthetical, not part of it (aws_iam_role_policy's `name` bullet is the
// canonical example: "(Optional) The name of the role policy." on one line,
// "If omitted, Terraform will assign a random, unique name." indented on the
// next) - up to the next top-level bullet or a blank line, whichever comes
// first.
func argumentReferenceBodies(doc string) map[string]string {
	loc := argHeadingRe.FindStringIndex(doc)
	if loc == nil {
		return nil
	}
	rest := topLevelSpan(doc[loc[1]:])
	bodies := map[string]string{}
	var name string
	var buf []string
	flush := func() {
		if name != "" {
			if _, exists := bodies[name]; !exists {
				bodies[name] = strings.Join(buf, " ")
			}
		}
		name, buf = "", nil
	}
	for _, l := range strings.Split(rest, "\n") {
		if m := bulletNameRe.FindStringSubmatch(l); m != nil {
			flush()
			name = m[1]
			buf = append(buf, strings.TrimSpace(l))
			continue
		}
		trimmed := strings.TrimSpace(l)
		if name == "" {
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		buf = append(buf, trimmed)
	}
	flush()
	return bodies
}

// argumentReferenceEntries returns the resource's top-level configuration
// arguments with their Required/ForceNew/ServerAssignedIfAbsent status: the
// widened sibling of argumentReferenceNames, over the same "## Argument
// Reference" bullet scan and the same boundary rule (stopping at the first
// "### " sub-heading, since a nested block's own arguments are never
// import-ID material) - kept as a separate function rather than folded into
// argumentReferenceNames so every existing caller of that function keeps
// its original, narrower return shape untouched.
func argumentReferenceEntries(doc string) []ArgumentRefEntry {
	loc := argHeadingRe.FindStringIndex(doc)
	if loc == nil {
		return nil
	}
	rest := topLevelSpan(doc[loc[1]:])
	bodies := argumentReferenceBodies(doc)
	seen := map[string]bool{}
	var out []ArgumentRefEntry
	for _, m := range argEntryRe.FindAllStringSubmatch(rest, -1) {
		name, paren := m[1], m[2]
		if seen[name] {
			continue
		}
		seen[name] = true
		required := strings.HasPrefix(strings.TrimSpace(paren), "Required")
		entry := ArgumentRefEntry{
			Name:                   name,
			Required:               required,
			ForceNew:               forceNewPhraseRe.MatchString(paren),
			ServerAssignedIfAbsent: !required && serverAssignedIfAbsent(bodies[name]),
			DeclaredUnique:         uniquename.Asserted(bodies[name]),
		}
		if !required {
			entry.CloudDefault = cloudDefault(bodies[name])
		}
		out = append(out, entry)
	}
	return out
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

// phraseArgRe is snakeArgRe's widening for the same clause: a backtick-quoted
// token whose words are separated by spaces rather than underscores. The
// AWS provider docs mix the two inside one sentence - IPAM's allocation page
// reads "using the allocation `id` and `pool id`, separated by `_`" - and
// snakeArgRe drops the spaced half silently, which leaves the clause with
// one token where the ID has two segments, so idParts' arity gate refuses
// the whole attribution and the page contributes nothing.
//
// It is deliberately a FALLBACK rather than a replacement (see
// separatedByPhraseTokens): the strict form's own results, and
// enumeratedTokens after it, are tried first and unchanged, so a page that
// resolves today resolves identically.
//
// Measured reach at v6.59.0: one page of 1693, aws_vpc_ipam_pool_cidr_
// allocation. That is small and it is the honest number - the value is that
// a real extraction hole is closed rather than a row hand-written around it,
// and the next release's pages get it for free.
var phraseArgRe = regexp.MustCompile("`([a-z][a-z0-9_]*(?: [a-z][a-z0-9_]*)*)`")

// separatedByPhraseTokens re-reads the "separated by" clause with
// phraseArgRe. It returns nothing unless the widened read finds at least
// two tokens, so a caller can try it after the strict sources without ever
// narrowing what they found.
func separatedByPhraseTokens(section string) []string {
	loc := separatedByRe.FindStringSubmatchIndex(section)
	if loc == nil {
		return nil
	}
	if !isCandidateSeparator(section[loc[2]:loc[3]]) {
		return nil
	}
	windowStart := loc[0] - 400
	if windowStart < 0 {
		windowStart = 0
	}
	window := section[windowStart:loc[0]]
	u := strings.LastIndex(strings.ToLower(window), "using")
	if u == -1 {
		return nil
	}
	tokens := dedupe(phraseArgRe.FindAllStringSubmatch(window[u:], -1), 1)
	if len(tokens) < 2 {
		return nil
	}
	return tokens
}

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
	if len(out) > 0 {
		return out
	}
	// Suffix fallback, only when equality and prefix found nothing: a
	// format-string segment that spells a longer noun whose tail is the
	// argument's own name - aws_api_gateway_usage_plan_key's
	// "USAGE-PLAN-KEY-ID" against the `key_id` argument. The argument must
	// be the segment's suffix (never the reverse: a short segment
	// swallowing a long name is coincidence), and suffixes under four
	// normalized characters are excluded for the same reason the prefix
	// check excludes short segments - "id" ends nearly every identifier.
	for _, p := range pool {
		np := normalize(p)
		if len(np) >= 4 && len(ns) > len(np) && strings.HasSuffix(ns, np) {
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

	// ArgumentsInOrder is Arguments' own left-to-right doc order, recovered
	// from a signal where the doc itself states the order: the format-token
	// signal ("using `REST-API-ID/RESPONSE-TYPE`") when every segment
	// fuzzy-matches exactly one Argument Reference name; a separated-by
	// clause whose backticked tokens all matched arguments ("using the
	// `server_id` and `user_name` separated by `/`"); a two-token
	// enumeration of Required arguments ("using `app_id` and
	// `branch_name`", buildRow); or the identity-block example whose
	// values, joined in block order, reproduce the documented ID
	// (identityBlockOrder, buildRow). Unlike Arguments (always alphabetized
	// below), this preserves the order the doc's own prose states the
	// segments in, independent of whatever the literal example value's own
	// segments look like. Empty whenever no source states the order
	// unambiguously.
	ArgumentsInOrder []string
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

	sep, rawClauseArgs, sepOK := separatedByClause(section)
	clauseArgs := matchNames(rawClauseArgs, argNames)
	segs, fmtSep, fmtOK := formatToken(section)

	if result.Composed == nil || *result.Composed {
		switch {
		case len(clauseArgs) > 0:
			t := true
			result.Composed = &t
			result.Arguments = unionStrings(result.Arguments, clauseArgs)
			// The clause names its tokens in the doc's own left-to-right
			// order, and when every one of them matched a real argument
			// (none dropped by the filter above), that order is the ID's
			// order - aws_transfer_user's "using the `server_id` and
			// `user_name` separated by `/`". A partial match (Connect's
			// "`instance_id` and `queue_id`", where queue_id is no
			// argument) proves nothing about the whole ID and sets no
			// order.
			if len(clauseArgs) >= 2 && len(clauseArgs) == len(rawClauseArgs) &&
				len(dedupeStrings(clauseArgs)) == len(clauseArgs) && result.ArgumentsInOrder == nil {
				result.ArgumentsInOrder = append([]string(nil), clauseArgs...)
			}
		case fmtOK && len(segs) >= 2:
			t := true
			result.Composed = &t
			var fuzzy []string
			ordered := make([]string, 0, len(segs))
			orderedOK := true
			for _, s := range segs {
				m := fuzzySegmentMatches(s, argNames)
				fuzzy = append(fuzzy, m...)
				if len(m) == 1 {
					ordered = append(ordered, m[0])
				} else {
					orderedOK = false
				}
			}
			result.Arguments = unionStrings(result.Arguments, fuzzy)
			if orderedOK && len(dedupeStrings(ordered)) == len(ordered) {
				result.ArgumentsInOrder = ordered
			}
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

	// A separator the doc STATES is evidence on its own, and is recorded
	// whether or not the arguments resolved (issue #135).
	//
	// These are two different facts, and folding them together lost the
	// stronger one. aws_bedrockagent_agent_alias's page says "using the
	// alias ID and the agent ID separated by `,`" - it names the separator
	// in backticks and names the parts in plain prose, so no argument
	// matched, Composed stayed unresolved, and the sentence was thrown
	// away. The doc could hardly be more explicit about the character.
	//
	// The inferred separator stays gated. fmtSep comes from splitting a
	// format token, which is a guess about where the boundaries are, and a
	// guess about an ID nothing else could resolve is what the old comment
	// was right to refuse.
	switch {
	case sepOK:
		result.Separator = &sep
	case fmtOK && result.Composed != nil && *result.Composed:
		s := fmtSep
		result.Separator = &s
	}

	sort.Strings(result.Arguments)
	result.Arguments = dedupeStrings(result.Arguments)
	return result
}

// usingEnumRe matches the two-token enumeration idiom, with an optional
// parenthesized aside after the first token: "using `app_id` and
// `branch_name`", "using the `CATALOG-ID` (AWS account ID if not custom)
// and `NAME`".
var usingEnumRe = regexp.MustCompile("(?i)using\\s+(?:the\\s+)?`([^`\n]+)`\\s*(?:\\([^)]*\\))?,?\\s+and\\s+(?:the\\s+)?`([^`\n]+)`")

// enumeratedTokens returns the backticked segment names an Import
// sentence's "X and Y" enumeration lists, in the doc's own order - the
// idiom docs use when they name every segment but state no separator
// (aws_amplify_branch's "using `app_id` and `branch_name`", whose "/" only
// ever appears in the example value). Tokens that are themselves
// multi-segment format strings are formatToken's business and produce
// nothing here.
func enumeratedTokens(section string) []string {
	m := usingEnumRe.FindStringSubmatch(section)
	if m == nil {
		return nil
	}
	for _, tok := range m[1:] {
		if _, _, ok := splitSegments(tok); ok {
			return nil
		}
	}
	return []string{m[1], m[2]}
}

// enumeratedRequiredArguments resolves an enumeration to argument names:
// every token must land on a REQUIRED Argument Reference entry by
// normalized-exact match. Required-only is the same discipline
// resolvePlainWords holds and for the same reason: an Optional argument in
// a composite is usually a server-defaulted value the ratified rows model
// as a slot, not a component (aws_glue_connection's catalog_id, which
// defaults to the account ID). Returns the arguments in the enumeration's
// own order.
func enumeratedRequiredArguments(section string, args []ArgumentRefEntry) ([]string, bool) {
	tokens := enumeratedTokens(section)
	if len(tokens) < 2 {
		return nil, false
	}
	var required []string
	for _, a := range args {
		if a.Required {
			required = append(required, a.Name)
		}
	}
	out := make([]string, len(tokens))
	for i, tok := range tokens {
		matched := matchNames([]string{tok}, required)
		if len(matched) != 1 {
			return nil, false
		}
		// matchNames returns the token's spelling; recover the argument's.
		n := normalize(tok)
		for _, r := range required {
			if normalize(r) == n {
				out[i] = r
				break
			}
		}
	}
	if len(dedupeStrings(out)) != len(out) {
		return nil, false
	}
	return out, true
}

// identityBlockPairRe matches one attribute assignment inside an import
// block's identity sub-block (four-space indent, per blockImportIDRe's
// boundary; the key is occasionally quoted, as aws_sns_topic's
// `"arn" = "..."` is).
var identityBlockPairRe = regexp.MustCompile(`(?m)^    "?([A-Za-z0-9_]+)"?\s*=\s*"([^"]*)"\s*$`)

// identityBlockOrder recovers a composite's argument order from the doc's
// own two example forms shown side by side: the Terraform 1.12+ identity
// block names each attribute with a literal value, and the legacy plain-id
// example joins those same literal values. When the identity block's
// values, joined in the block's own order by the resolved separator,
// reproduce the legacy `id = "..."` string exactly, that order is proven
// by value equality rather than guessed - aws_route53_record's
//
//	identity = { zone_id = "Z4KAPRWWNC7JR"  name = "dev.example.com"  type = "NS" }
//	id = "Z4KAPRWWNC7JR_dev.example.com_NS"
//
// where no segment of the opaque example values echoes its argument's name,
// so every token-matching pass necessarily declines.
func identityBlockOrder(section, sep string, argNames []string) ([]string, bool) {
	idx := strings.Index(section, "identity = {")
	if idx == -1 {
		return nil, false
	}
	rest := section[idx:]
	if end := strings.Index(rest, "}"); end != -1 {
		rest = rest[:end]
	}
	pairs := identityBlockPairRe.FindAllStringSubmatch(rest, -1)
	if len(pairs) < 2 {
		return nil, false
	}
	blockID, ok := "", false
	if m := blockImportIDRe.FindStringSubmatch(section); m != nil {
		blockID, ok = m[1], true
	}
	if !ok {
		return nil, false
	}
	keys := make([]string, len(pairs))
	values := make([]string, len(pairs))
	for i, p := range pairs {
		keys[i] = p[1]
		values[i] = p[2]
	}
	if strings.Join(values, sep) != blockID {
		return nil, false
	}
	if len(matchNames(keys, argNames)) != len(keys) {
		return nil, false // an identity attribute that is no argument is not a component order
	}
	return keys, true
}

// IDPart is one segment of the documented import ID as the Import section's
// own prose names it, attributed back to the section of the doc that owns
// the name (issue #132's per-segment source attribution: the exit for the
// Connect-family shape, where "using the `instance_id` and `queue_id`
// separated by a colon" names two segments of which only one is a
// configuration argument - the flat Arguments list cannot say which kind
// the other one is, and that distinction is the whole question of whether
// the ID is reconstructible from configuration).
type IDPart struct {
	// Token is the doc's own spelling of the segment name, verbatim
	// ("instance_id", "queue_id", "ID", "alias").
	Token string `json:"token"`

	// Source is where the doc itself defines that name:
	//   - "argument": a top-level Argument Reference entry - configuration
	//     supplies this segment;
	//   - "attribute": a top-level Attribute Reference entry and NOT an
	//     argument - the server supplies this segment, the doc says so
	//     itself;
	//   - "unknown": neither section defines the token (aws_lambda_alias's
	//     "alias", which is prose shorthand for the `name` argument) - no
	//     claim either way.
	Source string `json:"source"`
}

// IDPart.Source's values. "own-id" is the plain-prose sibling of
// "attribute": the part names the resource's own identifier on the
// resource's own page ("IPSet ID" on aws_guardduty_ipset's page, "the
// alias ID" on aws_bedrockagent_agent_alias's) - a server-minted value by
// construction, since a resource does not configure its own ID.
const (
	idPartSourceArgument  = "argument"
	idPartSourceAttribute = "attribute"
	idPartSourceOwnID     = "own-id"
	idPartSourceUnknown   = "unknown"
)

// idParts extracts the documented import ID's per-segment source
// attribution. The segment names come from the Import section's own prose,
// strongest signal first: a multi-segment format token ("using
// `ID/Name/Scope`", "using the `server_id/agreement_id`"), else the
// backtick-quoted names a "separated by (`:`)" sentence lists ("using the
// `instance_id` and `queue_id` separated by a colon (`:`)"). Nothing is
// returned unless the named segments account for every segment of the
// documented example under the resolved separator - the same arity
// discipline issue #39's aws_route trap demands: a partial name list is not
// a statement about the whole ID.
//
// sep is the row's final Separator (after buildRow's own fallbacks), not
// classifyGrammar's intermediate one, so a separator recovered from the
// example string itself still gates the arity here.
func idParts(section, tfType string, sep *string, example string, args []ArgumentRefEntry, attrNames []string) []IDPart {
	if sep == nil || example == "" {
		return nil
	}
	argNames := make([]string, len(args))
	for i, a := range args {
		argNames[i] = a.Name
	}
	segCount := len(strings.Split(example, *sep))

	tokens, _, ok := formatToken(section)
	if !ok {
		if _, clause, clauseOK := separatedByClause(section); clauseOK && len(clause) >= 2 {
			tokens = clause
		} else {
			tokens = enumeratedTokens(section)
		}
	}
	if len(tokens) < 2 {
		// The strict backticked sources found nothing usable. Before
		// falling through to plain prose, re-read the "separated by"
		// clause allowing a backticked token to carry spaces - see
		// phraseArgRe for why one exists and how far it reaches.
		tokens = separatedByPhraseTokens(section)
	}
	if len(tokens) < 2 {
		// No backticked token source names the segments; the plain-prose
		// enumeration ("using the primary GuardDuty detector ID and IPSet
		// ID") is the next one that can.
		if parts := plainEnumIDParts(section, tfType, args, attrNames); len(parts) >= 2 && len(parts) == segCount {
			return parts
		}
		// Last, the possessive-of enumeration ("the `id` of the Cognito
		// User Pool, and the `id` of the Cognito User Pool Client"), which
		// neither family above can read: every segment IS backticked, so
		// the plain-word reader skips the phrase, and the tokens are the
		// same generic property word in each, so no token source can tell
		// them apart. See ofPhraseIDParts.
		parts := ofPhraseIDParts(section, tfType, args, attrNames)
		if len(parts) < 2 || len(parts) != segCount {
			return nil
		}
		return parts
	}
	if segCount != len(tokens) {
		return nil // the named segments do not account for the whole example
	}
	argSet := map[string]bool{}
	for _, a := range argNames {
		argSet[normalize(a)] = true
	}
	attrs := map[string]bool{}
	for _, a := range attrNames {
		attrs[normalize(a)] = true
	}
	out := make([]IDPart, len(tokens))
	for i, t := range tokens {
		n := normalize(t)
		switch {
		case argSet[n]:
			out[i] = IDPart{Token: t, Source: idPartSourceArgument}
		case attrs[n]:
			out[i] = IDPart{Token: t, Source: idPartSourceAttribute}
		default:
			out[i] = IDPart{Token: t, Source: idPartSourceUnknown}
		}
	}
	return out
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

// subsetOf reports whether every string in a is also in b.
func subsetOf(a, b []string) bool {
	set := map[string]bool{}
	for _, s := range b {
		set[s] = true
	}
	for _, s := range a {
		if !set[s] {
			return false
		}
	}
	return true
}

// sameSet reports whether a and b contain exactly the same strings,
// ignoring order.
func sameSet(a, b []string) bool {
	return len(a) == len(b) && subsetOf(a, b) && subsetOf(b, a)
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

// omittedFallbackSentence is the one documented shape omittedFallbacks
// reads: "(if you omit `event_bus_name`, the `default` event bus will be
// used)". The argument and the substituted literal are both the doc's own
// backticked words; everything between the literal and "will be used" is
// the service's prose name for the thing being defaulted and is not
// captured.
var omittedFallbackSentence = regexp.MustCompile("\\(if you omit `([a-z0-9_]+)`, the `([^`]+)`[^)]*will be used\\)")

// omittedFallbacks scrapes the Import section for the documented
// omitted-argument fallback sentence, keeping only matches whose argument
// is one the composite's own Arguments list already names - a fallback for
// an argument the ID does not compose is a sentence about something else.
// Returns nil when the section states none, so the artifact field is
// omitted rather than empty.
func omittedFallbacks(section string, args []string) map[string]string {
	named := map[string]bool{}
	for _, a := range args {
		named[a] = true
	}
	var out map[string]string
	for _, m := range omittedFallbackSentence.FindAllStringSubmatch(section, -1) {
		if !named[m[1]] {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[m[1]] = m[2]
	}
	return out
}
