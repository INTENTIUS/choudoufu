// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"regexp"
	"strings"
)

// applyImportGrammarPrecedence is the rowgen-convergence upgrade: the
// ratification batches' dominant correction class is the CFN registry's
// primaryIdentifier disagreeing with the provider's own documented import
// grammar (VPC Lattice's short ids over the registry's opaque Arn - 9 of 14
// types in one batch; NetworkManager's device/site/link ARN imports over a
// composite primaryIdentifier the provider never actually uses; DataSync's
// compound ARN#ARN grammars; Transfer Server's ServerID). This pass makes
// live/import-grammar.json's pinned evidence win that disagreement wherever
// it is unambiguous, instead of leaving every one of these a human
// correction every batch pays for again.
//
// Runs after applyImportGrammarDemotions (main.go's classifyAll), over
// every proposal regardless of the bucket that function or classifyMapped
// left it in: a row already demoted to evidence-only by the older, more
// conservative pass is exactly the shape tryGrammarComposite resolves
// further when the grammar row turns out to carry a usable argument list.
//
// Rules, tried in order per proposal; the first that fires wins:
//
//  1. tryGrammarComposite: live/import-grammar.json itself pins
//     composed_of_arguments, an argument list and (when there is more than
//     one argument) a separator. Authoritative once its own arity checks
//     out against the documented example string - the same discipline
//     issue #39's aws_route trap needs, the other direction: aws_route's
//     grammar row lists four candidate destination arguments (the
//     provider's Argument Reference names all of a route's mutually
//     exclusive destination fields), but the documented example splits into
//     only two segments, so the arity check rejects it and aws_route stays
//     needs-hand-separator. aws_lambda_alias's row is the same trap the
//     other way: one named argument against a separator that plainly
//     implies two segments. When the per-segment value tokens are too
//     opaque to bijection-match (aws_api_gateway_gateway_response's
//     "12345abcde/UNAUTHORIZED" placeholders), this rule falls back to the
//     widened scrape's own ArgumentsInOrder - the same segments, matched to
//     Argument Reference names from the format-string prose instead of the
//     literal example value - see tryGrammarComposite's own doc comment.
//  2. tryArgumentReferenceConfirmedGuess: a proposal classify.go already
//     filed evidence-only because its argument name was GUESSED (the CFN
//     property name, snake-cased, backed by no schema or grammar) - the
//     widened Argument Reference scrape independently confirms that same
//     name is a real, Required top-level argument, so the two sources
//     (CFN's own primaryIdentifier naming, the provider's own docs) agree
//     and the guess is trusted.
//  3. tryArgumentReferenceValueMatch: the documented example is a single,
//     unsplit value (no candidate separator present) whose own text
//     embeds exactly one Required argument's name-token
//     (aws_vpc_dhcp_options_association's "vpc-0f001273ec18911b1" embeds
//     "vpc", the Required vpc_id argument's own token, while
//     dhcp_options_id's own token is absent) - the client-naming argument
//     the doc actually shows, independent of what the registry's own
//     primaryIdentifier claims (which is often a multi-part composite the
//     provider does not really use, or an opaque attribute read-only per
//     CFN but not per the provider). Also catches the codebuild_fleet
//     shape: a server-assigned registry claim (Arn is read-only) whose
//     classic import example nonetheless names a Required argument
//     directly.
//  4. tryArgumentReferenceComposite: the still-needs-hand-separator
//     sibling of rule 3, for a documented example that does split - tries
//     every candidate separator against the Argument Reference's own
//     Required argument names (rather than the registry's
//     primaryIdentifier names, which rule 6 already tried and which do not
//     always match the provider's own TF argument names).
//  5. tryRegistryComposite: only for a still-needs-hand-separator proposal
//     (the registry's own primaryIdentifier is the arity signal here, not
//     the grammar). Splits the documented example on each of the four
//     characters internal/live/identity/table.go's DefaultTable actually
//     uses, and accepts a separator only when every resulting segment
//     matches exactly one primaryIdentifier property by name - recovering
//     NetworkManager's association quartet's separator and order from
//     evidence rather than guessing it.
//  6. tryOpaqueOverride: still needs-hand-separator, but nothing in rules
//     3-5 reconstructed a composite from the documented example at all,
//     AND the example itself looks like a genuinely single opaque value
//     (looksOpaque) rather than one this pass simply failed to take apart -
//     NetworkManager's device/site/link, whose provider StateContext
//     importer reads a single arn attribute a GlobalNetworkId+DeviceId
//     composite never touches.
//  7. tryArnVsIDOverride: already believed server-assigned, single opaque
//     primaryIdentifier named like an ARN, but the documented example is
//     not one - VPC Lattice's and Transfer Server's shape.
//  8. tryCompoundArnImportSyntax: cosmetic-only: a server-assigned proposal
//     whose documented example joins two ARNs (DataSync's FSx-backed
//     locations) gets an ImportSyntax placeholder that says so, instead of
//     the flat single-primaryIdentifier guess.
//  9. applyIdentitySchemaAttrsCorrection: always tried last, and only ever
//     touches a proposal that rule 7 already gave a single-value
//     DerivedIdentityAttrs guess to (never introduces a claim where none
//     existed - see that function's own doc comment for why: this
//     codebase's own established convention pairs most server-assigned
//     types' single documented identity attribute with a hand-confirmed
//     "id" alias, e.g. aws_lb's ["arn", "id"], that this scrape has no way
//     to know about, and issue #44 declares that inference explicitly out
//     of scope. When the widened scrape's own Identity Schema names a
//     DIFFERENT attribute than rule 7's guess (not merely a shorter list),
//     that direct provider evidence corrects the guess -
//     aws_batch_compute_environment's legacy Import section example
//     ("sample") makes rule 7 guess "id"; the Identity Schema correctly
//     requires "arn".
func applyImportGrammarPrecedence(proposals []proposal, importGrammar map[string]importGrammarRow) {
	for i := range proposals {
		p := &proposals[i]
		if p.CFNType == "" {
			// A fold row carries no registry primaryIdentifier of its own
			// (classifyFold never sets PrimaryIdentifier) - nothing here
			// applies, and every rule below that gates on PrimaryIdentifier
			// would be vacuously unreachable anyway.
			continue
		}
		g, ok := importGrammar[p.TFType]
		if !ok {
			continue
		}
		// A proposal already bucketClientNamed already ran resolveArgName,
		// which checks this exact grammar row itself, ranked behind only
		// the provider's own identity schema (classify.go's resolveArgName
		// doc comment) - rule 1 below has nothing to add there and skips
		// it, rather than risk silently downgrading a schema-sourced
		// argument name to a grammar-sourced one that happens to agree.
		switch {
		case p.Bucket != bucketClientNamed && tryGrammarComposite(p, g):
		case p.Bucket == bucketEvidenceOnly && tryArgumentReferenceConfirmedGuess(p, g):
		case p.Bucket != bucketClientNamed && p.Bucket != bucketComposite && tryArgumentReferenceValueMatch(p, g):
		case p.Bucket == bucketNeedsHandSeparator && tryArgumentReferenceComposite(p, g):
		case p.Bucket == bucketNeedsHandSeparator && tryRegistryComposite(p, g):
		case p.Bucket == bucketNeedsHandSeparator && tryOpaqueOverride(p, g):
		case p.Bucket == bucketServerAssigned && tryArnVsIDOverride(p, g):
		}
		tryCompoundArnImportSyntax(p, g)
		applyIdentitySchemaAttrsCorrection(p, g)
	}
}

// candidateSeparators is the join-character search order tryRegistryComposite
// tries when the grammar row itself carries no separator: the four
// internal/live/identity/table.go's DefaultTable doc comment names as its
// own ("/", ":", "_", ","), plus Cloud Control's own "|", ordered so the
// characters least likely to occur inside an embedded ARN or resource path
// (which own plenty of "/" and ":" internally) are tried first.
var candidateSeparators = []string{",", "|", "_", "/", ":"}

// derivedComposite is deriveCompositeWithSeparator's result: the separator
// that produced an exact, unambiguous match, and the TF argument names in
// the documented example string's own left-to-right order.
type derivedComposite struct {
	Separator   string
	ArgsInOrder []string
}

// tryGrammarComposite is rule 1: live/import-grammar.json's own
// composed_of_arguments/arguments/separator fields are the provider's own
// structured answer, built by the same scrape that produced the documented
// example string - but that Arguments array's own order turns out to be
// alphabetical, not the documented string's own left-to-right order
// (confirmed against aws_api_gateway_method: arguments
// ["http_method","resource_id","rest_api_id"] sorted alphabetically, while
// the documented example "12345abcde/67890fghij/GET" is
// rest_api_id/resource_id/http_method - the reverse - and against
// aws_iam_role_policy_attachment the same way), so it is never trusted
// directly. Order is instead recovered the same way rule 5
// (tryRegistryComposite) recovers it for the registry's own composite
// primaryIdentifier: deriveCompositeWithSeparator's token-matching
// bijection against the pinned separator. A single named argument needs no
// order, but is still arity-checked against a pinned separator (a
// separator implies more than one segment, so a lone argument name
// contradicts it - this is what catches aws_lambda_alias's grammar row).
//
// Because deriveCompositeWithSeparator fails closed whenever a segment's
// value carries no recognizable token of its own (aws_api_gateway_method's
// generic "12345abcde"/"67890fghij"/"GET" placeholders, unlike an
// ARN-shaped or prefixed value), this rule resolves fewer types than
// trusting the array's order would have - a deliberate trade: an unresolved
// proposal is a proposal a human still checks by hand, exactly today's
// baseline; a wrongly-ordered one would be a worse regression than that.
func tryGrammarComposite(p *proposal, g importGrammarRow) bool {
	if g.ComposedOfArguments == nil || !*g.ComposedOfArguments || len(g.Arguments) == 0 {
		return false
	}

	if len(g.Arguments) == 1 {
		if g.Separator != nil && len(strings.Split(g.ImportIDExample, *g.Separator)) != 1 {
			return false // arity contradiction: a separator that actually splits, against one named argument
		}
		setClientNamedComposite(p, []string{g.Arguments[0]}, "", g)
		return true
	}

	if g.Separator == nil {
		return false // more than one argument, but no pinned separator to join them with
	}
	if dc, ok := deriveCompositeWithSeparator(g.ImportIDExample, *g.Separator, g.Arguments); ok {
		setClientNamedComposite(p, dc.ArgsInOrder, dc.Separator, g)
		return true
	}
	// The literal example's own segments were too opaque to bijection-match
	// (aws_api_gateway_gateway_response's "12345abcde/UNAUTHORIZED"). The
	// widened scrape's own ArgumentsInOrder recovers the same order a
	// different way: the doc's format-string prose ("using
	// `REST-API-ID/RESPONSE-TYPE`"), matched to Argument Reference names at
	// scrape time rather than against the example value - so this still
	// only fires when that order is the same argument set, split the same
	// number of ways as the example itself splits on the pinned separator
	// (an arity check against the example string, the same discipline rule
	// 1's other branch already holds itself to).
	if len(g.ArgumentsInOrder) == len(g.Arguments) && sameStringSet(g.ArgumentsInOrder, g.Arguments) &&
		len(strings.Split(g.ImportIDExample, *g.Separator)) == len(g.ArgumentsInOrder) {
		setClientNamedComposite(p, g.ArgumentsInOrder, *g.Separator, g)
		p.Rule = "import-grammar precedence: composed_of_arguments, multi-argument, order recovered from the doc's format-string prose (the example's own segments were too opaque to match by value)"
		return true
	}
	return false
}

// sameStringSet reports whether a and b contain exactly the same strings,
// ignoring order and duplicates - the set-equality check tryGrammarComposite
// needs to confirm ArgumentsInOrder is a reordering of Arguments, not a
// different (and hence untrustworthy) argument list.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if !set[x] {
			return false
		}
	}
	return true
}

// setClientNamedComposite finishes a resolved composite: bucketClientNamed
// for a single argument (no separator needed), bucketComposite for two or
// more.
func setClientNamedComposite(p *proposal, args []string, sep string, g importGrammarRow) {
	if len(args) == 1 {
		p.Bucket = bucketClientNamed
		p.ArgName = args[0]
		p.ArgSource = argSourceImportGrammar
		p.Rule = "import-grammar precedence: composed_of_arguments, single argument, arity confirmed against the example string"
		p.Notes = append(p.Notes, fmt.Sprintf("import docs pin a single-argument composed ID (%s), superseding the registry-only classification", g.ImportIDExample))
		return
	}
	p.Bucket = bucketComposite
	p.CompositeArgs = append([]string(nil), args...)
	p.CompositeSep = sep
	p.ArgSource = argSourceImportGrammar
	p.Rule = "import-grammar precedence: composed_of_arguments, multi-argument, arity confirmed against the example string"
	p.Notes = append(p.Notes, fmt.Sprintf("import docs pin %s joined by %q: %s", quoteList(args), sep, g.ImportIDExample))
}

// tryRegistryComposite is rule 5: only reached for a still-needs-hand-
// separator proposal, whose registry primaryIdentifier is the arity signal
// (the grammar row did not resolve it under rule 1 - either because it has
// no composed_of_arguments evidence of its own, or that evidence's arity
// did not check out). Tries each candidateSeparators entry against the
// documented example, and for the first that splits the string into
// exactly len(PrimaryIdentifier) segments, matches each segment back to a
// primaryIdentifier property by name (deriveCompositeWithSeparator's own
// bijection) - recovering both the separator and, critically, the STRING's
// own left-to-right order, which need not agree with the registry's own
// primaryIdentifier order (NetworkManager's link association documents
// global_network_id,link_id,device_id; the registry's own primaryIdentifier
// order is GlobalNetworkId, DeviceId, LinkId - device before link).
func tryRegistryComposite(p *proposal, g importGrammarRow) bool {
	if g.ImportIDExample == "" || len(p.PrimaryIdentifier) < 2 {
		return false
	}
	candidates := make([]string, len(p.PrimaryIdentifier))
	for i, name := range p.PrimaryIdentifier {
		candidates[i] = snakeCase(name)
	}
	for _, sep := range candidateSeparators {
		dc, ok := deriveCompositeWithSeparator(g.ImportIDExample, sep, candidates)
		if !ok {
			continue
		}
		p.Bucket = bucketComposite
		p.CompositeArgs = dc.ArgsInOrder
		p.CompositeSep = dc.Separator
		p.ArgSource = argSourceImportGrammar
		p.Rule = "import-grammar precedence: registry composite primaryIdentifier, separator and order recovered from the documented example string"
		p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q splits on %q into exactly the registry's %d primary-identifier parts, matched by name: %s", g.ImportIDExample, sep, len(p.PrimaryIdentifier), quoteList(dc.ArgsInOrder)))
		return true
	}
	return false
}

// deriveCompositeWithSeparator splits example on sep and requires an exact,
// unambiguous bijection against candidates: every part must contain exactly
// one candidate's argToken as a substring, and every candidate must be
// claimed by exactly one part. Arity mismatch (wrong part count) or any
// ambiguity (a part matching zero or multiple candidates, or a candidate
// matched by more than one part) fails closed - this function only ever
// returns evidence-backed matches, never a best guess.
func deriveCompositeWithSeparator(example, sep string, candidates []string) (derivedComposite, bool) {
	if sep == "" || example == "" || len(candidates) < 2 {
		return derivedComposite{}, false
	}
	parts := strings.Split(example, sep)
	if len(parts) != len(candidates) {
		return derivedComposite{}, false
	}
	tokens := make([]string, len(candidates))
	for i, c := range candidates {
		tokens[i] = argToken(c)
	}
	assignment := make([]int, len(parts))
	used := make([]bool, len(candidates))
	for pi, part := range parts {
		partNorm := alnum(part)
		match := -1
		for ci, tok := range tokens {
			if tok == "" {
				continue
			}
			if strings.Contains(partNorm, tok) {
				if match != -1 {
					return derivedComposite{}, false // ambiguous: this part matches more than one candidate
				}
				match = ci
			}
		}
		if match == -1 || used[match] {
			return derivedComposite{}, false
		}
		used[match] = true
		assignment[pi] = match
	}
	argsInOrder := make([]string, len(parts))
	for pi, ci := range assignment {
		argsInOrder[pi] = candidates[ci]
	}
	return derivedComposite{Separator: sep, ArgsInOrder: argsInOrder}, true
}

// argToken reduces a TF/snake_case argument name to the alphanumeric core a
// provider-minted value usually embeds as a literal prefix or path segment,
// stripping the "_id"/"_arn" typing suffix that never appears in the value
// itself (a device's own value is "device-07f6...", never
// "deviceid-07f6...").
func argToken(name string) string {
	name = strings.TrimSuffix(name, "_id")
	name = strings.TrimSuffix(name, "_arn")
	return alnum(name)
}

// alnum lowercases s and drops every character that is not a letter or
// digit, so hyphen/underscore/colon/slash punctuation differences between an
// argument name and a documented example's own formatting never block a
// match.
func alnum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// requiredArgumentReferenceNames returns the Name of every g.ArgumentReference
// entry marked Required - the candidate pool tryArgumentReferenceConfirmedGuess,
// tryArgumentReferenceValueMatch and tryArgumentReferenceComposite all draw
// from, since an Optional argument (a default-assigned name, a config knob)
// is never what a documented import ID actually identifies a resource by.
func requiredArgumentReferenceNames(g importGrammarRow) []string {
	var out []string
	for _, a := range g.ArgumentReference {
		if a.Required {
			out = append(out, a.Name)
		}
	}
	return out
}

// tryArgumentReferenceConfirmedGuess is rule 2: reached only for a proposal
// classify.go's resolveArgName already filed evidence-only because its
// argument name came from the last-resort GUESSED source (a snake-cased CFN
// property name, backed by neither a provider identity schema nor
// live/import-grammar.json's own composed_of_arguments evidence) - see
// classify.go's argSourceGuessed. The widened Argument Reference scrape is a
// second, independent source: when it also names that exact argument
// Required, the two sources (CFN's own primaryIdentifier property name, the
// provider's own docs) agree, which is enough to trust the guess -
// aws_athena_workgroup, aws_athena_data_catalog, aws_glue_classifier and
// aws_db_proxy's shape: row-gen already guessed the right TF argument name
// from the CFN property, but had no source willing to confirm it until now.
func tryArgumentReferenceConfirmedGuess(p *proposal, g importGrammarRow) bool {
	if p.ArgSource != argSourceGuessed || p.ArgName == "" {
		return false
	}
	for _, name := range requiredArgumentReferenceNames(g) {
		if name == p.ArgName {
			p.Bucket = bucketClientNamed
			p.ArgSource = argSourceArgumentReference
			p.Rule = "import-grammar precedence: the guessed argument name is confirmed as a Required top-level argument in the provider's own Argument Reference"
			p.Notes = append(p.Notes, fmt.Sprintf("Argument Reference documents %q as Required, confirming the CFN-property-derived guess", p.ArgName))
			return true
		}
	}
	return false
}

// tryArgumentReferenceValueMatch is rule 3: the documented example is a
// single, unsplit value (no candidate separator present - candidateSeparators,
// this file's own set) whose own text embeds exactly one Required Argument
// Reference argument's name-token (argToken; the same substring-bijection
// technique deriveCompositeWithSeparator uses per-segment, applied here to
// the one and only segment there is). aws_vpc_dhcp_options_association's
// "vpc-0f001273ec18911b1" embeds the Required vpc_id argument's own token
// "vpc" and nothing of dhcp_options_id's "dhcpoptions" - resolving the real,
// simpler identity a two-part registry primaryIdentifier oversold. Runs
// regardless of the proposal's current bucket (server-assigned, needs-hand-
// separator or evidence-only all reach it) because the signal is the
// documented value's own content, not any prior classification - but never
// when composed_of_arguments already resolved true (rule 1's territory) or
// when g.ArgumentsInOrder shows this same example is actually part of a
// wider composite this function's single-segment reading would misread.
func tryArgumentReferenceValueMatch(p *proposal, g importGrammarRow) bool {
	if g.ImportIDExample == "" {
		return false
	}
	if g.ComposedOfArguments != nil && *g.ComposedOfArguments {
		return false
	}
	if strings.ContainsAny(g.ImportIDExample, ",|_/:") {
		return false // not a single segment; tryArgumentReferenceComposite's territory
	}
	if strings.ContainsAny(g.ImportIDExample, "<>{}") {
		return false // a doc placeholder token ("<id>"), not a real example value
	}
	valueNorm := alnum(g.ImportIDExample)
	var match string
	for _, name := range requiredArgumentReferenceNames(g) {
		tok := argToken(name)
		if len(tok) < 3 || !strings.Contains(valueNorm, tok) {
			continue
		}
		if match != "" {
			return false // ambiguous: more than one Required argument's token appears in the value
		}
		match = name
	}
	if match == "" {
		return false
	}
	p.Bucket = bucketClientNamed
	p.ArgName = match
	p.ArgSource = argSourceArgumentReference
	p.Rule = "import-grammar precedence: the documented example's own text embeds exactly one Required argument's name, confirming it over the registry's own primaryIdentifier claim"
	p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q embeds the Required argument %q; superseding the registry-only classification", g.ImportIDExample, match))
	return true
}

// tryArgumentReferenceComposite is rule 4: the still-needs-hand-separator
// sibling of rule 3, for a documented example that does split into more than
// one segment. Tries every candidateSeparators entry against the Required
// Argument Reference names (rather than the registry's primaryIdentifier
// names, which rule 5/tryRegistryComposite already tried and which do not
// always match the provider's own TF argument spelling - CFN's MemberId
// against the provider's own account_id, for one) via the same
// deriveCompositeWithSeparator bijection rule 5 uses, so it fails closed the
// same way: an example whose segments carry no recognizable argument token
// (a generic placeholder ID, not a name-prefixed or ARN-shaped value) simply
// does not resolve, rather than guessing an order.
func tryArgumentReferenceComposite(p *proposal, g importGrammarRow) bool {
	if g.ImportIDExample == "" {
		return false
	}
	required := requiredArgumentReferenceNames(g)
	if len(required) < 2 {
		return false
	}
	for _, sep := range candidateSeparators {
		dc, ok := deriveCompositeWithSeparator(g.ImportIDExample, sep, required)
		if !ok {
			continue
		}
		p.Bucket = bucketComposite
		p.CompositeArgs = dc.ArgsInOrder
		p.CompositeSep = dc.Separator
		p.ArgSource = argSourceArgumentReference
		p.Rule = "import-grammar precedence: Argument Reference's own Required arguments, separator and order recovered from the documented example string"
		p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q splits on %q into exactly %d Required Argument Reference names, matched by name: %s", g.ImportIDExample, sep, len(required), quoteList(dc.ArgsInOrder)))
		return true
	}
	return false
}

// applyIdentitySchemaAttrsCorrection is rule 9: see this file's own
// package-level doc comment for why it is a correction pass, called
// unconditionally after every switch rule and tryCompoundArnImportSyntax,
// rather than one more switch case - it only ever touches a proposal that
// rule 6 (tryOpaqueOverride) or rule 7 (tryArnVsIDOverride) already gave a
// single-value DerivedIdentityAttrs guess to (len == 1), never introduces a
// claim where none existed (a bucketComposite or still-needs-hand-separator
// proposal is left alone, the same issue #44 non-goal every other rule here
// respects).
//
// Corrects that guess only when the widened scrape's own Identity Schema
// names exactly one Required attribute, it differs from the guess already
// on file, and that name is confirmed absent from Argument Reference (the
// same "not actually a configuration argument" shape rule 1's territory
// would have claimed instead, had it resolved) - aws_batch_compute_environment's
// legacy Import section example ("sample") makes rule 7 guess "id"; the
// Identity Schema correctly requires "arn". A multi-attribute Identity
// Schema is left alone: that is a different shape (a composite identity) a
// single-value guess cannot be corrected into.
func applyIdentitySchemaAttrsCorrection(p *proposal, g importGrammarRow) {
	if p.Bucket != bucketServerAssigned || len(p.DerivedIdentityAttrs) != 1 {
		return
	}
	if len(g.IdentitySchemaRequired) != 1 {
		return
	}
	attr := g.IdentitySchemaRequired[0]
	if attr == p.DerivedIdentityAttrs[0] {
		return
	}
	for _, ref := range g.ArgumentReference {
		if ref.Name == attr {
			return // a configuration argument, not a read-only identity attribute
		}
	}
	p.Notes = append(p.Notes, fmt.Sprintf("Identity Schema requires %q for import, correcting the %q guess derived from the legacy Import section example", attr, p.DerivedIdentityAttrs[0]))
	p.DerivedIdentityAttrs = []string{attr}
	p.DerivedImportSyntax = strings.ToUpper(attr)
	p.Rule = "import-grammar precedence: the provider's own Terraform 1.12+ Identity Schema names a different required import identity attribute than the legacy Import section's example implied"
}

// looksOpaque reports whether g's own evidence actually supports "a single,
// server-assigned value" - the shape tryOpaqueOverride is for - rather than
// merely "nothing above resolved a composite for this proposal." An
// ARN-shaped example is always eligible: an ARN's own internal "/" and ":"
// characters are structural to the ARN itself, not a composite-ID separator
// (NetworkManager's device/site/link). Anything else must contain no
// candidate composite separator at all: a value like
// "aabbccddee/example-stage" or "example.com/base-path" plainly looks
// composite even when nothing in this pass could name its parts, and is left
// needs-hand-separator - an honest unresolved gap - rather than guessed
// opaque.
func looksOpaque(example string) bool {
	if strings.HasPrefix(example, "arn:") {
		return true
	}
	return !strings.ContainsAny(example, ",|_/:")
}

// tryOpaqueOverride is rule 6: reached only when rules 3-5 could not
// reconstruct a composite from the documented example under any candidate
// separator or argument source, and the example itself looksOpaque. That
// combination is itself the evidence: the provider's own Import section pins
// a single value the registry's claimed multi-part primaryIdentifier does
// not actually describe - NetworkManager's device/site/link, whose provider
// StateContext importer reads the type's own arn attribute (confirmed
// against each resource's @Testing(importStateIdAttribute="arn")
// decoration in the AWS provider source) rather than composing
// global_network_id with a server-minted device/site/link id.
func tryOpaqueOverride(p *proposal, g importGrammarRow) bool {
	if g.ImportIDExample == "" {
		return false
	}
	if g.ComposedOfArguments != nil && *g.ComposedOfArguments {
		return false // rule 1's territory; if it did not resolve this already, guessing further here is not warranted
	}
	if !looksOpaque(g.ImportIDExample) {
		return false // the example still looks composite; an honest needs-hand-separator gap, not opacity
	}
	syntax, attr := labelForOpaqueValue(g.ImportIDExample)
	p.Bucket = bucketServerAssigned
	p.DerivedImportSyntax = syntax
	p.DerivedIdentityAttrs = []string{attr}
	p.Rule = "import-grammar precedence: the registry's composite primaryIdentifier does not reconstruct the documented example string under any known separator; the provider's own Import section shows a single opaque value instead"
	p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q is a single value, not the registry's %d-part composite primaryIdentifier %s", g.ImportIDExample, len(p.PrimaryIdentifier), quoteList(p.PrimaryIdentifier)))
	return true
}

// tryArnVsIDOverride is rule 7: the VPC Lattice/Transfer Server shape. The
// proposal is already believed server-assigned (rule 1 in classify.go:
// primaryIdentifier ⊆ readOnlyProperties), the registry's own single
// primaryIdentifier is opaque and named like an ARN, but the provider's
// documented example is not one - the registry is right that no argument
// reconstructs the identity, wrong about which exported attribute it is.
//
// The ImportSyntax placeholder is corrected whenever the example is not an
// ARN, but the IdentityAttrs guess is claimed only when the example is a
// single, unsegmented token. VPC Lattice's own Listener and ListenerRule
// are the reason: their documented examples ("svc-.../listener-...") are
// themselves a two-resource composite the type exports no single flat
// attribute equal to (confirmed against the ratified table.go entry, whose
// own comment says so explicitly) - a "/" in the example is the signal a
// same-shaped Service or TargetGroup id ("svc-...", "tg-...", no
// separator) never carries, so it is what gates the IdentityAttrs claim.
func tryArnVsIDOverride(p *proposal, g importGrammarRow) bool {
	if g.ImportIDExample == "" {
		return false
	}
	if g.ComposedOfArguments != nil && *g.ComposedOfArguments {
		return false
	}
	if len(p.PrimaryIdentifier) != 1 || !strings.Contains(strings.ToLower(p.PrimaryIdentifier[0]), "arn") {
		return false
	}
	if strings.HasPrefix(g.ImportIDExample, "arn:") {
		return false // the registry's ARN claim and the documented example agree; nothing to override
	}
	syntax, attr := labelForOpaqueValue(g.ImportIDExample)
	p.DerivedImportSyntax = syntax
	if !strings.ContainsAny(g.ImportIDExample, "/,|") {
		p.DerivedIdentityAttrs = []string{attr}
	}
	p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q is not an ARN, though the registry names %s; the provider's own short id is the real identity", g.ImportIDExample, p.PrimaryIdentifier[0]))
	return true
}

// labelForOpaqueValue derives a documentation-only ImportSyntax placeholder
// and identity-attribute guess from a single documented example's own
// shape: a full ARN gets "ARN"/"arn", anything else gets the generic
// "ID"/"id" - the same two spellings every ratified server-assigned entry
// in internal/live/identity/table.go actually uses for this shape (VPC
// Lattice's nine corrected types and Transfer Server all resolve to
// "ID"/"id"; NetworkManager's device/site/link resolve to "ARN"/"arn").
func labelForOpaqueValue(example string) (importSyntax, identityAttr string) {
	if strings.HasPrefix(example, "arn:") {
		return "ARN", "arn"
	}
	return "ID", "id"
}

// arnRe matches one ARN's own service segment ("arn:aws:SERVICE:...").
var arnRe = regexp.MustCompile(`arn:aws[a-z0-9-]*:([a-z0-9-]+):`)

// tryCompoundArnImportSyntax is rule 8, tried after every switch rule above
// (and cosmetic only, not "always tried last": rule 9,
// applyIdentitySchemaAttrsCorrection, still runs after it): a
// bucketServerAssigned proposal whose documented example joins two (or
// more) ARNs - DataSync's FSx-backed locations, whose provider Import
// section reads "DataSync-ARN#FSx-ARN" - gets an ImportSyntax placeholder
// built from each ARN's own service token, instead of the flat single-
// primaryIdentifier guess renderServerAssignedEntry would otherwise print.
// Never overrides a rule 6/7 result (DerivedImportSyntax already set), and
// never changes the bucket or IdentityAttrs - ImportSyntax is documentation
// only (see TypeIdentity's own doc comment: "Components is what the code
// follows"), so getting this placeholder's wording exactly right is not
// load-bearing the way the other rules are.
func tryCompoundArnImportSyntax(p *proposal, g importGrammarRow) {
	if p.Bucket != bucketServerAssigned || p.DerivedImportSyntax != "" {
		return
	}
	if g.ImportIDExample == "" {
		return
	}
	matches := arnRe.FindAllStringSubmatch(g.ImportIDExample, -1)
	if len(matches) < 2 {
		return
	}
	parts := make([]string, len(matches))
	for i, m := range matches {
		parts[i] = strings.ToUpper(m[1]) + "-ARN"
	}
	p.DerivedImportSyntax = strings.Join(parts, "#")
	p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q joins %d ARNs; ImportSyntax reflects that compound shape rather than the registry's single-ARN placeholder", g.ImportIDExample, len(matches)))
}
