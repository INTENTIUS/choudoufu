// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"strings"
)

// This file scrapes the second evidence source the markerless admission
// veto needs (issue #249): whether a resource's documented import ID is a
// value the server mints, for a type CloudFormation does not model at all
// and whose registry entry therefore states nothing.
//
// idParts already answers that question per segment for a COMPOSITE ID,
// and refuses to answer it for anything shorter: its arity gate wants at
// least two named tokens, so a one-segment ID - "import OpenSearch VPC
// endpoints using the `id`" - produces nothing. That is the whole gap.
// soleIDPart closes it with the same discipline and the same vocabulary,
// over the one segment the prose names.
//
// argumentReferenceNamesAnyDepth is the safety half, and it is the half a
// top-level-only read gets wrong. A segment naming a NESTED block's
// argument (aws_eks_identity_provider_config's
// `oidc.identity_provider_config_name`) is configuration-supplied, and a
// reader that only sees the top-level bullets attributes it to the server
// instead, because the name does turn up in the Attribute Reference. The
// consumer subtracts these before it treats a segment as server-minted.

// argHeadingSpanEnd is the boundary argumentReferenceNamesAnyDepth reads
// to: the next top-level "## " heading. Unlike topLevelSpan it does NOT
// stop at the first sub-heading, because a nested block's own arguments
// are exactly what this function exists to collect.
const argHeadingSpanEnd = "\n## "

// argumentReferenceNamesAnyDepth returns every Argument Reference bullet
// name on the page at any block depth: the top-level arguments
// argumentReferenceNames already returns, plus every nested block's own
// arguments documented under a sub-heading inside the same
// "## Argument Reference" section.
//
// This is deliberately wider than argumentReferenceNames rather than a
// replacement for it. Nothing that builds an import ID should read it -
// a nested argument is not top-level import-ID material, which is the
// rule argumentReferenceNames' own boundary encodes. It is a REFUTATION
// set: a name that appears here is a name configuration can supply, so a
// segment carrying it is not evidence of anything the server minted, at
// any depth.
func argumentReferenceNamesAnyDepth(doc string) []string {
	loc := argHeadingRe.FindStringIndex(doc)
	if loc == nil {
		return nil
	}
	rest := doc[loc[1]:]
	if end := strings.Index(rest, argHeadingSpanEnd); end != -1 {
		rest = rest[:end]
	}
	return dedupe(bulletNameRe.FindAllStringSubmatch(rest, -1), 1)
}

// attributeDescriptionRe matches one Attribute Reference bullet's name and
// the rest of its line - the doc's own sentence about what the exported
// value is. Only the name's own line is taken: every description this
// reads is written on one line in the 6.59.0 cache, and a continuation
// rule that swallowed the following bullets would attribute one
// attribute's words to another.
var attributeDescriptionRe = regexp.MustCompile("(?m)^[*-]\\s+`([A-Za-z0-9_]+)`\\s*[-:]?\\s*(.*)$")

// attributeDescriptions returns each top-level Attribute Reference
// bullet's own description text, keyed by attribute name, over the same
// span attributeReferenceNames reads.
func attributeDescriptions(doc string) map[string]string {
	loc := attrHeadingRe.FindStringIndex(doc)
	if loc == nil {
		return nil
	}
	out := map[string]string{}
	for _, m := range attributeDescriptionRe.FindAllStringSubmatch(topLevelSpan(doc[loc[1]:]), -1) {
		if _, seen := out[m[1]]; !seen {
			out[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return out
}

// genericIDAttr is the one exported attribute name that states nothing by
// itself. Every AWS resource page exports `id`, and the Import section's
// "using the `id`" is the doc naming the import string, not naming what
// the import string holds - the same sentence appears over an account ID
// (aws_backup_global_settings), a Region (aws_bedrock_model_invocation_
// logging_configuration), a configured tag key (aws_ce_cost_allocation_
// tag) and a server-minted endpoint id (aws_opensearch_vpc_endpoint).
// Attributing the bare token to the Attribute Reference on the strength
// of the bullet existing would call all four server-minted, so the token
// is refused as evidence and the bullet's own description is read
// instead - see attributeDescribesOwnIdentifier.
const genericIDAttr = "id"

// cloudValuedDescriptionRe matches an `id` description that names a
// property of the cloud the run is pointed at rather than a property of
// the resource. Both spellings appear as the WHOLE of some descriptions
// ("The AWS Account ID.", "AWS Region in which logging is configured")
// and as a second-sentence correction to a first sentence that reads like
// a resource identifier ("Unique identifier for the account registration.
// Since registration is applied per AWS region, this will be the active
// region name"), so the match is over the whole description and it
// refuses rather than qualifies. An identity built from a cloud property
// is internal/live/identity's Component{Cloud: ...} shape, which resolves
// without discovery and therefore needs no marker - the opposite of what
// this scrape is looking for.
var cloudValuedDescriptionRe = regexp.MustCompile(`(?i)account\s*id|\bregions?\b`)

// ownIdentifierPhraseRe matches the two shapes a doc uses to say an
// exported value is this resource's own identifier: the noun phrase
// before the identifier word ("The SIP rule ID", "The delegation set
// ID"), and the noun phrase after it ("The unique identifier of the
// endpoint", "The ID of the Elastictranscoder pipeline", "Unique
// identifier for the event action"). The submatches are the two candidate
// noun phrases; namesOwnIdentifier decides whether either is this
// resource's own noun.
var ownIdentifierPhraseRe = regexp.MustCompile(`(?i)^(.*?)\b(?:ids?|identifiers?)\b(?:\s+(?:of|for)\s+(.*?))?[.,;]`)

// attributeDescribesOwnIdentifier reports whether the doc's own
// description of an exported attribute says the value is this resource's
// own identifier - "The unique identifier of the endpoint" on
// aws_opensearch_vpc_endpoint's page, whose last name token is
// "endpoint".
//
// This is attributePlainPart's own-id test moved from the import prose to
// the attribute's description, for the one token the import prose cannot
// carry it in (see genericIDAttr). It is a positive statement the doc
// makes, not an absence: a description that names no identifier at all
// ("The name of the project in the space"), or names one belonging to
// something else ("Identifier of the source cluster"), or names a cloud
// property, all fail.
func attributeDescribesOwnIdentifier(desc, tfType string) bool {
	if desc == "" || cloudValuedDescriptionRe.MatchString(desc) {
		return false
	}
	// A description with no terminal punctuation still has one clause;
	// give the regexp a boundary to stop at rather than failing it.
	m := ownIdentifierPhraseRe.FindStringSubmatch(desc + ".")
	if m == nil {
		return false
	}
	for _, candidate := range []string{m[2], m[1]} {
		if candidate == "" {
			continue
		}
		if namesOwnIdentifier(strings.Fields(candidate), tfType) {
			return true
		}
	}
	return false
}

// soleIDPart attributes the ONE segment of a one-segment documented
// import ID, the way idParts attributes each segment of a composite. It
// returns nil for everything it cannot settle, which is the majority: an
// unattributed lone segment states nothing, unlike an unattributed
// segment sitting beside an attributed one.
//
// The gates are idParts' own, read the other way round. A resolved
// separator means the doc has already said the ID has parts, so this is
// not that ID; a documented example carrying a separator character means
// the same thing even when no separator resolved. A prose phrase that
// enumerates ("X and Y", "separated by") belongs to the composite family
// and is skipped here, exactly as resolveProsePhrase skips it.
//
// anyDepthArgs is argumentReferenceNamesAnyDepth's refutation set, and it
// is consulted before any server attribution: a segment naming a
// configuration argument at ANY block depth is configuration-supplied,
// whatever section its name also happens to appear in.
func soleIDPart(section, tfType string, sep *string, example string, args []ArgumentRefEntry, anyDepthArgs, attrNames []string, attrDescs map[string]string) *IDPart {
	if sep != nil || example == "" || strings.ContainsAny(example, soleSegmentDisqualifiers) {
		return nil
	}
	argSet := map[string]bool{}
	for _, a := range args {
		argSet[normalize(a.Name)] = true
	}
	for _, n := range anyDepthArgs {
		argSet[normalize(n)] = true
	}
	var requiredNames []string
	for _, a := range args {
		if a.Required {
			requiredNames = append(requiredNames, a.Name)
		}
	}
	attrSet := map[string]bool{}
	for _, a := range attrNames {
		attrSet[normalize(a)] = true
	}

	for _, phrase := range usingPhrases(section) {
		lower := strings.ToLower(phrase)
		if strings.Contains(lower, " and ") || strings.Contains(lower, "separated by") || strings.Contains(phrase, ",") {
			continue
		}
		stripped := parenRe.ReplaceAllString(phrase, " ")
		ticks := backtickTokenRe.FindAllStringSubmatch(stripped, -1)
		if len(ticks) > 1 {
			continue // several names in one phrase is the composite family's business
		}
		var part IDPart
		if len(ticks) == 1 {
			part = attributeSoleToken(ticks[0][1], tfType, argSet, requiredNames, attrSet, attrDescs,
				phraseQualifiesToken(stripped, ticks[0][0]))
		} else {
			part, _ = attributePlainPart(phrase, tfType, requiredNames, attrNames)
			if argSet[normalize(strings.Join(plainContentWords(part.Token), ""))] {
				part.Source = idPartSourceArgument
			}
		}
		if part.Source == idPartSourceUnknown || quotesTheExampleItself(part.Token, example) {
			return nil
		}
		return &part
	}
	return nil
}

// quotesTheExampleItself reports whether the prose token IS the documented
// example value rather than a name for it - "import IAM Account Password
// Policy using the word `iam-account-password-policy`", whose example is
// that same string. A doc quoting the literal it wants pasted is stating
// that the ID is a constant, which is the opposite of a value the server
// mints per instance: a constant needs no discovery to recover, so it is
// not this scrape's business and must not be attributed to the server.
// Two pages in the 6.59.0 cache take this shape, both singletons.
func quotesTheExampleItself(token, example string) bool {
	return normalize(token) == normalize(example)
}

// phraseQualifiesToken reports whether the "using ..." phrase names
// anything BESIDE the backticked token: "the VPC peering `id`" qualifies
// its token, "the `id`" does not. Articles, the doc's own metalanguage
// and the token itself are not qualifiers; any other word is.
//
// This matters for exactly one token (see genericIDAttr): "using the `id`"
// is the doc declining to say what the ID holds, while "using the VPC
// peering `id`" is the doc saying whose id it is - and on
// aws_vpc_peering_connection_options that is the `vpc_peering_connection_id`
// argument, a value configuration supplies.
func phraseQualifiesToken(phrase, quotedToken string) bool {
	for _, w := range strings.Fields(strings.ReplaceAll(phrase, quotedToken, " ")) {
		lw := strings.ToLower(strings.Trim(w, ".,;:"))
		if lw == "" || proseArticles[lw] || proseMetaWords[lw] {
			continue
		}
		return true
	}
	return false
}

// soleSegmentDisqualifiers are the characters whose presence in the
// documented example means the ID is not one segment, whatever the
// separator scrape settled: candidateSeparators' own set less "_", which
// occurs inside single names far too often to read as a join.
const soleSegmentDisqualifiers = "/:,|"

// constantValueRe matches an exported attribute's description stating
// that the value is always one particular constant - AWS Config's
// retention configuration, "The object is always named **default**". A
// value that is the same on every instance is not a per-instance minted
// identifier: it needs no discovery to recover, because the constant is
// in the documentation. This refuses a segment the doc itself refutes in
// the same sentence it names.
var constantValueRe = regexp.MustCompile(`(?i)\bis\s+always\b|\balways\s+(named|called|set\s+to)\b`)

// describesAConstant reports whether an exported attribute's description
// states the value is a fixed constant rather than a minted one.
func describesAConstant(desc string) bool {
	return desc != "" && constantValueRe.MatchString(desc)
}

// attributeSoleToken attributes a backtick-quoted lone segment name, in
// the one order that keeps configuration ahead of the server: an argument
// at any depth first, then the alias case (a backticked short name that
// is a unique suffix of one Required argument's own name - "using the
// `name`" on a page whose argument is `backup_vault_name`), then the
// generic `id`'s own description, then an exported attribute, then the
// resource's own noun.
//
// qualified is phraseQualifiesToken's answer, and it only ever blocks the
// generic `id`: a qualified `id` is the doc naming somebody else's id.
func attributeSoleToken(token, tfType string, argSet map[string]bool, requiredNames []string, attrSet map[string]bool, attrDescs map[string]string, qualified bool) IDPart {
	n := normalize(token)
	if argSet[n] {
		return IDPart{Token: token, Source: idPartSourceArgument}
	}
	if len(n) >= 3 {
		var hits int
		for _, r := range requiredNames {
			if strings.HasSuffix(normalize(r), n) {
				hits++
			}
		}
		if hits == 1 {
			return IDPart{Token: token, Source: idPartSourceArgument}
		}
	}
	if n == genericIDAttr {
		if !qualified && attributeDescribesOwnIdentifier(attrDescs[genericIDAttr], tfType) {
			return IDPart{Token: token, Source: idPartSourceOwnID}
		}
		return IDPart{Token: token, Source: idPartSourceUnknown}
	}
	if attrSet[n] {
		if describesAConstant(attrDescs[token]) {
			return IDPart{Token: token, Source: idPartSourceUnknown}
		}
		return IDPart{Token: token, Source: idPartSourceAttribute}
	}
	if namesOwnIdentifier(strings.Fields(token), tfType) {
		return IDPart{Token: token, Source: idPartSourceOwnID}
	}
	return IDPart{Token: token, Source: idPartSourceUnknown}
}
