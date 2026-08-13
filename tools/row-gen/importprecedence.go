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
// Five narrow rules, tried in order per proposal; the first that fires
// wins:
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
//     implies two segments.
//  2. tryRegistryComposite: only for a still-needs-hand-separator proposal
//     (the registry's own primaryIdentifier is the arity signal here, not
//     the grammar). Splits the documented example on each of the four
//     characters internal/live/identity/table.go's DefaultTable actually
//     uses, and accepts a separator only when every resulting segment
//     matches exactly one primaryIdentifier property by name - recovering
//     NetworkManager's association quartet's separator and order from
//     evidence rather than guessing it.
//  3. tryOpaqueOverride: still needs-hand-separator, but nothing in rule 2
//     reconstructed the registry's claimed composite from the documented
//     example at all. That is itself evidence the registry's composite
//     primaryIdentifier is not what the provider actually imports by -
//     NetworkManager's device/site/link, whose provider StateContext
//     importer reads a single arn attribute a GlobalNetworkId+DeviceId
//     composite never touches.
//  4. tryArnVsIDOverride: already believed server-assigned, single opaque
//     primaryIdentifier named like an ARN, but the documented example is
//     not one - VPC Lattice's and Transfer Server's shape.
//  5. tryCompoundArnImportSyntax: cosmetic-only, always tried last: a
//     server-assigned proposal whose documented example joins two ARNs
//     (DataSync's FSx-backed locations) gets an ImportSyntax placeholder
//     that says so, instead of the flat single-primaryIdentifier guess.
func applyImportGrammarPrecedence(proposals []proposal, importGrammar map[string]importGrammarRow) {
	for i := range proposals {
		p := &proposals[i]
		if p.CFNType == "" {
			// A fold row carries no registry primaryIdentifier of its own
			// (classifyFold never sets PrimaryIdentifier) - nothing here
			// applies, and rules 2-4 below would be vacuously unreachable
			// anyway since they all gate on a non-empty PrimaryIdentifier.
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
		case p.Bucket == bucketNeedsHandSeparator && tryRegistryComposite(p, g):
		case p.Bucket == bucketNeedsHandSeparator && tryOpaqueOverride(p, g):
		case p.Bucket == bucketServerAssigned && tryArnVsIDOverride(p, g):
		}
		tryCompoundArnImportSyntax(p, g)
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
// directly. Order is instead recovered the same way rule 2
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
	dc, ok := deriveCompositeWithSeparator(g.ImportIDExample, *g.Separator, g.Arguments)
	if !ok {
		return false
	}
	setClientNamedComposite(p, dc.ArgsInOrder, dc.Separator, g)
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

// tryRegistryComposite is rule 2: only reached for a still-needs-hand-
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

// tryOpaqueOverride is rule 3: reached only when rule 2 could not
// reconstruct the registry's own composite primaryIdentifier from the
// documented example under any candidate separator. That failure is itself
// the evidence: the provider's own Import section pins a single value the
// registry's claimed multi-part primaryIdentifier does not actually
// describe - NetworkManager's device/site/link, whose provider
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
	syntax, attr := labelForOpaqueValue(g.ImportIDExample)
	p.Bucket = bucketServerAssigned
	p.DerivedImportSyntax = syntax
	p.DerivedIdentityAttrs = []string{attr}
	p.Rule = "import-grammar precedence: the registry's composite primaryIdentifier does not reconstruct the documented example string under any known separator; the provider's own Import section shows a single opaque value instead"
	p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q is a single value, not the registry's %d-part composite primaryIdentifier %s", g.ImportIDExample, len(p.PrimaryIdentifier), quoteList(p.PrimaryIdentifier)))
	return true
}

// tryArnVsIDOverride is rule 4: the VPC Lattice/Transfer Server shape. The
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

// tryCompoundArnImportSyntax is rule 5, always tried last and cosmetic
// only: a bucketServerAssigned proposal whose documented example joins two
// (or more) ARNs - DataSync's FSx-backed locations, whose provider Import
// section reads "DataSync-ARN#FSx-ARN" - gets an ImportSyntax placeholder
// built from each ARN's own service token, instead of the flat single-
// primaryIdentifier guess renderServerAssignedEntry would otherwise print.
// Never overrides a rule 3/4 result (DerivedImportSyntax already set), and
// never changes the bucket or IdentityAttrs - ImportSyntax is documentation
// only (see TypeIdentity's own doc comment: "Components is what the code
// follows"), so getting this placeholder's wording exactly right is not
// load-bearing the way the other four rules are.
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
