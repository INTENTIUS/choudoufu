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
//     Between rules 1 and 2 sits tryAssembledTemplate (issue #172): the
//     scrape's own per-segment ARN/URL template, when fully attributed,
//     resolves the one shape no other bucket can express - components
//     carrying Cloud region/account slots and mid-string literals. See its
//     own doc comment for the two-tier evidence bar.
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
//     Between rules 3 and 4 sits tryDocNamedServerSegment (issue #132): a
//     needs-hand-separator proposal whose doc names every segment of the
//     documented example and attributes one to the Attribute Reference is
//     server-assigned outright, refuting the argument reconstructions rules
//     4 and 5 would otherwise attempt - see its own doc comment.
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
		// No CFNType/PrimaryIdentifier guard here: a fold row (CFNType=="",
		// PrimaryIdentifier==nil - classifyFold never sets either) is not
		// skipped. tryGrammarComposite and tryArgumentReferenceValueMatch
		// read only g and p.Bucket, so they apply to a fold row exactly as
		// well as a mapped one. Every other rule below is reached only
		// behind a p.Bucket==bucketNeedsHandSeparator or
		// p.Bucket==bucketServerAssigned case guard, and classifyFold never
		// produces either bucket (only bucketFoldChild or bucketEvidenceOnly
		// - see its own doc comment), nor can the two PrimaryIdentifier-free
		// rules above promote a fold row into one of those buckets (they
		// only ever set bucketClientNamed or bucketComposite) - so those
		// PrimaryIdentifier-dependent rules stay unreachable for a fold row,
		// not merely coincidentally empty-safe.
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
		case p.Bucket != bucketComposite && tryAssembledTemplate(p, g):
		case p.Bucket == bucketEvidenceOnly && tryArgumentReferenceConfirmedGuess(p, g):
		case p.Bucket != bucketClientNamed && p.Bucket != bucketComposite && tryArgumentReferenceValueMatch(p, g):
		case p.Bucket == bucketNeedsHandSeparator && tryDocNamedServerSegment(p, g):
		case p.Bucket == bucketNeedsHandSeparator && tryArgumentReferenceComposite(p, g):
		case p.Bucket == bucketNeedsHandSeparator && tryRegistryComposite(p, g):
		case p.Bucket == bucketNeedsHandSeparator && tryOpaqueOverride(p, g):
		case p.Bucket == bucketServerAssigned && tryArnVsIDOverride(p, g):
		}
		deriveDocImportSyntax(p, g)
		tryCompoundArnImportSyntax(p, g)
		applyIdentitySchemaAttrsCorrection(p, g)
		// Last, so that whatever the rules above settled on is what gets
		// compared against the provider's own two answers. See
		// crosscheck.go: this is the pass that would have caught
		// aws_ecs_service's phantom service_arn.
		applyIdentitySchemaCrossCheck(p, g)
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
		p.Rule = "import-grammar precedence: composed_of_arguments, multi-argument, order stated by the doc itself (arguments_in_doc_order: a format token, a separated-by clause or enumeration naming every argument, or an identity-block example whose values reproduce the documented ID)"
		return true
	}

	// Third fallback (issue #134): the provider's own Identity Schema is an
	// ORDERED list, present on 441 rows, and it settles the order the two
	// passes above could not.
	//
	// aws_iam_role_policy is the shape. Its example is
	// "role_of_mypolicy_name:mypolicy_name" and its arguments are
	// ["name","role"]; both segments contain the token "name", so the
	// value-token bijection is ambiguous and declines. Its
	// identity_schema_required is ["role","name"] - which is the example's
	// own order, from the provider rather than from a guess.
	//
	// Arity alone must not be enough, and aws_ssoadmin_account_assignment is
	// why: six segments, arity matches, and the schema's order is NOT the
	// example's. So the order has to be corroborated, not merely fitted -
	// see identitySchemaOrderCorroborated.
	if order, ok := identitySchemaOrder(g); ok {
		setClientNamedComposite(p, order, *g.Separator, g)
		p.Rule = "import-grammar precedence: composed_of_arguments, multi-argument, order taken from the provider's own Identity Schema and corroborated against the example's segments"
		return true
	}
	return false
}

// identitySchemaOrder is the third fallback's whole test. It returns the
// argument order the provider's Identity Schema states, and whether that
// order is safe to use.
//
// Three conditions, all required:
//
//  1. The schema's required attributes are the same SET as the grammar row's
//     own argument list. A schema naming something the arguments do not is
//     describing a different identity, not a reordering of this one.
//  2. Their count equals the number of segments the example splits into.
//     Same arity discipline the ArgumentsInOrder branch above holds.
//  3. At least one segment bijects unambiguously to its schema-ordered
//     position by value token. This is the corroboration: an order that fits
//     the arity but matches no segment anywhere is a coincidence of length,
//     and aws_ssoadmin_account_assignment is exactly that - its six
//     identity attributes are a real set in a different order from the
//     documented example.
func identitySchemaOrder(g importGrammarRow) ([]string, bool) {
	req := g.IdentitySchemaRequired
	if len(req) < 2 || g.Separator == nil {
		return nil, false
	}
	if !sameStringSet(req, g.Arguments) {
		return nil, false
	}
	segments := strings.Split(g.ImportIDExample, *g.Separator)
	if len(segments) != len(req) {
		return nil, false
	}
	if !anySegmentCorroboratesOrder(segments, req) {
		return nil, false
	}
	return append([]string(nil), req...), true
}

// anySegmentCorroboratesOrder reports whether at least one (segment,
// argument) pair at the same index shares a name token, and no pair at the
// same index is positively contradicted by matching a DIFFERENT argument
// instead.
//
// The positive half is the corroboration. The negative half is what refuses
// aws_ssoadmin_account_assignment, whose example leads with a principal id
// while its schema leads with instance_arn: that segment matches
// "principal_id"'s token and not "instance_arn"'s, so the order is
// contradicted rather than merely unconfirmed.
func anySegmentCorroboratesOrder(segments, order []string) bool {
	corroborated := false
	for i, seg := range segments {
		lower := strings.ToLower(seg)
		if segmentNamesArgument(lower, order[i]) {
			corroborated = true
			continue
		}
		for j, other := range order {
			if j != i && segmentNamesArgument(lower, other) {
				return false // this segment belongs at another position
			}
		}
	}
	return corroborated
}

// segmentNamesArgument reports whether an example segment's text carries a
// token from the argument's own name - "example-cluster" for "cluster_name",
// "role_of_mypolicy_name" for "role". Tokens shorter than four characters
// are skipped: "id", "arn" and "name" appear in almost every segment and
// would corroborate anything.
func segmentNamesArgument(lowerSegment, argument string) bool {
	for _, token := range strings.Split(argument, "_") {
		if len(token) < 4 {
			continue
		}
		if strings.Contains(lowerSegment, token) {
			return true
		}
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
	p.CompositeDefaults = compositeDefaultsFor(args, g)
	p.ArgSource = argSourceImportGrammar
	p.Rule = "import-grammar precedence: composed_of_arguments, multi-argument, arity confirmed against the example string"
	p.Notes = append(p.Notes, fmt.Sprintf("import docs pin %s joined by %q: %s", quoteList(args), sep, g.ImportIDExample))
}

// tryAssembledTemplate is issue #172's rule: the scrape decomposed the
// documented import ID into an ARN/URL template
// (live/import-grammar.json's import_id_template - a leading scheme
// literal, positional Cloud region/account slots, fixed mid-string
// literals, argument tail segments), and every segment is attributed, so
// the proposal can finally render the assembled-components shape the
// ratified table spells as {Literal: "arn:aws:sns:"}, {Cloud: "region"},
// ..., {Attrs: [...]}. Refuses whenever any segment is Unattributed, the
// template carries no Cloud slot, no argument at all, or the tail does not
// END in an argument - a template whose last segment is opaque
// reconstructs nothing.
//
// The bar then depends on what the rule would overturn - the same
// proportionality tryDocNamedServerSegment and #134's order-corroboration
// already practice:
//
//   - A proposal the registry left unresolved (needs-hand-separator,
//     evidence-only, fold-child), or client-named by a name that is not a
//     documented configuration argument at all (aws_kinesis_firehose_
//     delivery_stream's "arn", the Identity Schema's required attribute
//     taken as if it were an argument): any attribution signal suffices.
//     There is no standing claim to defeat, only a gap to fill.
//   - A rule-1 server-assigned proposal (primaryIdentifier wholly
//     read-only): every tail segment must be attributed by its OWN TEXT
//     (attrByPlaceholderName/attrByPlaceholderAbbrev - "report-group-name"
//     spells `name`, "example-repo" abbreviates `repository`). The
//     contextual signals (a self-placeholder resolved against the one
//     Required name argument, an Example Usage value match) are NOT enough
//     to overturn the registry here, and this was measured, not assumed:
//     letting them fire flips 16 admitted rows the ratifiers adopted
//     server-assigned unchanged - aws_ram_permission (tail
//     "test-permission", a self-placeholder over a Required `name`, ratified
//     ServerAssigned) is evidence-identical to aws_sns_topic (tail
//     "my-topic", ratified as components) on every pinned source: rule-1
//     registry claim, Identity Schema requiring [arn], listable, taggable.
//     No evidence-only rule can split that pair, so the contextual tier
//     stays confined to rows with no server-assigned claim standing.
func tryAssembledTemplate(p *proposal, g importGrammarRow) bool {
	t := g.IDTemplate
	if t == nil || len(t.Segments) == 0 {
		return false
	}
	hasCloud, hasArg, allOwnText := false, false, true
	for _, s := range t.Segments {
		switch {
		case s.Unattributed != "":
			return false
		case s.Cloud != "":
			hasCloud = true
		case s.Argument != "":
			hasArg = true
			if s.AttributedBy != attrByPlaceholderName && s.AttributedBy != attrByPlaceholderAbbrev {
				allOwnText = false
			}
		case s.Literal != "":
		default:
			return false // an empty segment is a malformed template, not evidence
		}
	}
	if !hasCloud || !hasArg || t.Segments[len(t.Segments)-1].Argument == "" {
		return false
	}

	switch p.Bucket {
	case bucketNeedsHandSeparator, bucketEvidenceOnly, bucketFoldChild:
		// No standing claim; any attribution tier fills the gap.
	case bucketClientNamed:
		if argDocumented(p.ArgName, g) {
			return false // a documented-argument client-naming already resolved this row
		}
	case bucketServerAssigned:
		if !allOwnText {
			return false // see the doc comment: the aws_ram_permission counterexample
		}
	default:
		return false
	}

	p.Bucket = bucketAssembled
	p.Assembled = append([]idTemplateSegment(nil), t.Segments...)
	p.CompositeArgs = nil
	p.CompositeSep = ""
	p.ArgName = ""
	p.ArgSource = argSourceImportGrammar
	p.Rule = "import-grammar precedence: the documented import ID is a full " + strings.ToUpper(t.Kind) + " template; scheme literals, Cloud region/account slots and every tail segment attributed per-segment by the scrape (issue #172)"
	p.Notes = append(p.Notes, fmt.Sprintf("import docs template (%s) attributes every segment: %s", t.Kind, describeSegments(t.Segments)))
	return true
}

// argDocumented reports whether name is a documented top-level
// configuration argument on the grammar row's Argument Reference.
func argDocumented(name string, g importGrammarRow) bool {
	for _, a := range g.ArgumentReference {
		if a.Name == name {
			return true
		}
	}
	return false
}

// describeSegments renders a template's segments for a proposal note.
func describeSegments(segs []idTemplateSegment) string {
	var out []string
	for _, s := range segs {
		switch {
		case s.Cloud != "":
			out = append(out, "<"+s.Cloud+">")
		case s.Argument != "":
			out = append(out, fmt.Sprintf("%s (%s)", s.Argument, s.AttributedBy))
		default:
			out = append(out, fmt.Sprintf("%q", s.Literal))
		}
	}
	return strings.Join(out, " + ")
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
		p.CompositeDefaults = compositeDefaultsFor(dc.ArgsInOrder, g)
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
//
// Issue #176's corroboration clause: existing-and-Required is true of many
// arguments that are not the import identity, so the promotion additionally
// requires the row's own Import evidence to be CONSISTENT with the claimed
// argument (importEvidenceContradiction) - the two measured counterexamples
// are aws_route53_resolver_config, whose Import prose names the documented
// ID as "the Route 53 Resolver config ID" (the resource's own server-minted
// identifier, not the Required vpc-reference argument resource_id), and
// aws_opensearch_application, whose documented example is the read-only id
// "app-1234567890abcdef0" while the doc's own Example Usage sets the claimed
// `name` to the word-placeholder "my-opensearch-app". A contradicted row
// stays evidence-only - the honest GUESSED bucket it came from - with a note
// recording what contradicted it.
func tryArgumentReferenceConfirmedGuess(p *proposal, g importGrammarRow) bool {
	if p.ArgSource != argSourceGuessed || p.ArgName == "" {
		return false
	}
	for _, name := range requiredArgumentReferenceNames(g) {
		if name != p.ArgName {
			continue
		}
		if reason := importEvidenceContradiction(p.ArgName, p.TFType, g); reason != "" {
			p.Notes = append(p.Notes, fmt.Sprintf("the guessed argument %q is a Required top-level argument, but the row's own Import evidence contradicts it as the import identity: %s; staying evidence-only (issue #176)", p.ArgName, reason))
			return false
		}
		p.Bucket = bucketClientNamed
		p.ArgSource = argSourceArgumentReference
		p.Rule = "import-grammar precedence: the guessed argument name is confirmed as a Required top-level argument in the provider's own Argument Reference"
		p.Notes = append(p.Notes, fmt.Sprintf("Argument Reference documents %q as Required, confirming the CFN-property-derived guess", p.ArgName))
		return true
	}
	return false
}

// importEvidenceContradiction is issue #176's R3 corroboration clause: it
// answers whether the row's own pinned Import evidence is consistent with
// arg being the type's import identity, returning "" when it is and the
// contradiction's own statement when it is not.
//
// The bar is deliberately consistency, not positive attribution, and that
// was measured rather than assumed: of the rule's seven ratified instances
// (its 7/7 PROPOSE ledger record), five - aws_api_gateway_domain_name
// ("dev.example.com"), aws_cloudfront_monitoring_subscription
// ("E3QYSUHO4VYRGB"), aws_route53_hosted_zone_dnssec ("Z1D633PJN98FT9"),
// aws_sagemaker_feature_group ("feature_group-foo") and
// aws_vpclattice_auth_policy ("abcd-12345678") - have Import sections whose
// example no extracted signal attributes to the ratified argument at all
// (the attribution the ratifiers used lives in prose the scrape does not
// carry, e.g. cloudfront's Attribute Reference sentence "which corresponds
// to the `distribution_id`"). A positive-attribution bar would demote those
// five ratified rows to evidence-only, which the convergence ratchet rightly
// forbids. What the clause therefore checks is that no pinned signal
// attributes the documented ID to something OTHER than the claimed
// argument, with the positive signals short-circuiting first:
//
//   - the provider's own Identity Schema, when present, must name arg among
//     its required attributes (naming only others is a direct contradiction);
//   - the documented ID template's per-segment attribution (issue #172),
//     when it attributes any segment, must attribute one to arg;
//   - the doc's own Example Usage literal for arg: equality with the
//     documented import ID is consistency proven (aws_sesv2_contact_list's
//     "example"); a word-placeholder literal against an opaque documented ID
//     is a shape contradiction (aws_opensearch_application);
//   - the Import prose's own "using ..." sentence, when it names the ID in
//     plain words as the resource's own identifier - the type's own noun
//     tail plus "ID", tools/importdocs-gen/prosename.go's own-id rule - is
//     the doc saying the ID is server-minted (aws_route53_resolver_config).
func importEvidenceContradiction(arg, tfType string, g importGrammarRow) string {
	if len(g.IdentitySchemaRequired) > 0 {
		for _, a := range g.IdentitySchemaRequired {
			if a == arg {
				return ""
			}
		}
		return fmt.Sprintf("the provider's own Identity Schema requires %s", quoteList(g.IdentitySchemaRequired))
	}

	if t := g.IDTemplate; t != nil {
		attributed := 0
		for _, s := range t.Segments {
			if s.Argument == arg {
				return "" // the template's own attribution names the claimed argument
			}
			if s.Argument != "" {
				attributed++
			}
		}
		if attributed > 0 {
			return "the documented ID template attributes its segments to other arguments"
		}
	}

	literals := 0
	allWordShaped := true
	for _, ea := range g.ExampleArguments {
		if len(ea.Path) != 1 || ea.Path[0] != arg || !ea.IsString {
			continue
		}
		if ea.Value == g.ImportIDExample {
			return "" // the doc configures the argument with the very value it imports by
		}
		literals++
		if !wordPlaceholderValue(ea.Value) {
			allWordShaped = false
		}
	}
	if literals > 0 && allWordShaped && g.ImportIDExample != "" && !wordPlaceholderValue(g.ImportIDExample) {
		return fmt.Sprintf("the doc's own Example Usage sets %q to a word-placeholder value, while the documented import ID %q is an opaque identifier", arg, g.ImportIDExample)
	}

	if phrase, ok := importProseOwnIDPhrase(g.EvidenceExcerpt, tfType); ok {
		return fmt.Sprintf("the Import prose names the documented ID as the resource's own identifier (%q)", phrase)
	}
	return ""
}

// wordPlaceholderValue reports whether a doc value is a word-placeholder (a
// phrase of purely alphabetic words - "my-opensearch-app", "NAME OF
// DIRECTORY", "directoryNameExample") as opposed to an opaque identifier
// carrying digits or punctuation ("app-1234567890abcdef0") - the shape
// distinction tools/importdocs-gen/idtemplate.go's placeholderWords draws.
func wordPlaceholderValue(v string) bool {
	words := strings.FieldsFunc(strings.ToLower(v), func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.'
	})
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !isAlphaWord(w) {
			return false
		}
	}
	return true
}

// importForExampleAnchor and importUsingPhrases mirror tools/importdocs-gen/
// prosename.go's usingPhrases for this package: the prose between the last
// "using" and each "For example" anchor is where the Import section states
// what the documented ID is.
const importForExampleAnchor = "for example"

func importUsingPhrases(section string) []string {
	var out []string
	lower := strings.ToLower(section)
	idx := 0
	for {
		i := strings.Index(lower[idx:], importForExampleAnchor)
		if i == -1 {
			return out
		}
		anchor := idx + i
		winStart := anchor - 250
		if winStart < 0 {
			winStart = 0
		}
		window := section[winStart:anchor]
		if u := strings.LastIndex(strings.ToLower(window), "using "); u != -1 {
			phrase := strings.TrimSpace(window[u+len("using "):])
			phrase = strings.TrimRight(phrase, " .:,")
			if phrase != "" {
				out = append(out, phrase)
			}
		}
		idx = anchor + len(importForExampleAnchor)
	}
}

// importProseOwnIDPhrase reports whether any single-part, plain-prose
// "using ..." sentence in the Import section names the documented ID as the
// resource's OWN identifier: its article-stripped words are the type's own
// noun tail plus a trailing id-word ("the Route 53 Resolver config ID" on
// aws_route53_resolver_config's page) - the plain-prose own-id rule
// tools/importdocs-gen/prosename.go's attributePlainPart applies to
// multi-part enumerations, read here for the single-part sentence idParts
// never covers. Backticked, enumerated or separated-by phrases are other
// signals' business and contribute nothing; so does a phrase whose noun
// words are not the type's own tail (aws_route53_hosted_zone_dnssec's "the
// Route 53 Hosted Zone identifier" names the PARENT zone - the argument's
// referent - not the dnssec resource itself, because "hosted zone" is not
// the type's noun tail "...dnssec").
func importProseOwnIDPhrase(section, tfType string) (string, bool) {
	for _, phrase := range importUsingPhrases(section) {
		lower := strings.ToLower(phrase)
		if strings.Contains(phrase, "`") || strings.Contains(phrase, ",") ||
			strings.Contains(lower, " and ") || strings.Contains(lower, "separated by") {
			continue
		}
		words := strings.Fields(docPartParenRe.ReplaceAllString(phrase, " "))
		for len(words) > 0 && docPartArticles[strings.ToLower(words[0])] {
			words = words[1:]
		}
		if len(words) < 2 {
			continue // a bare "the id" claims nothing about whose id it is
		}
		switch strings.ToLower(words[len(words)-1]) {
		case "id", "ids", "identifier":
		default:
			continue
		}
		nounWords := make([]string, 0, len(words)-1)
		for _, w := range words[:len(words)-1] {
			nounWords = append(nounWords, alnum(w))
		}
		if namesOwnType(nounWords, tfType) {
			return phrase, true
		}
	}
	return "", false
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
	match, ok := valueNamesRequiredArgument(g.ImportIDExample, p.TFType, requiredArgumentReferenceNames(g))
	if !ok {
		return false
	}
	p.Bucket = bucketClientNamed
	p.ArgName = match
	p.ArgSource = argSourceArgumentReference
	p.Rule = "import-grammar precedence: the documented example's own text embeds exactly one Required argument's name, confirming it over the registry's own primaryIdentifier claim"
	p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q embeds the Required argument %q; superseding the registry-only classification", g.ImportIDExample, match))
	return true
}

// tryDocNamedServerSegment sits between rules 3 and 4, for a still-needs-
// hand-separator proposal (the registry claims a composite primaryIdentifier
// that is not wholly read-only, so classify.go could settle nothing): when
// the Import section's own prose names every segment of the documented
// example and attributes at least one to the doc's own Attribute Reference
// (importdocs-gen's per-segment attribution, issue #132), the ID carries a
// server-provided value no configuration argument supplies, so no
// argument-reconstruction rule below can ever be right about this type -
// the identity is server-assigned, discovered by listing, exactly what
// WAFv2's "using `ID/Name/Scope`" (ID is the exported attribute) and
// Transfer's "using the `server_id/agreement_id`" (agreement_id exported)
// say outright. Tried before rules 4 and 5 because both try to reconstruct
// a composite from argument names, and a doc-named server segment refutes
// that reconstruction at the source: aws_ssoadmin_permission_set's example
// happens to token-match the registry's two-ARN primaryIdentifier under
// rule 5, but its own doc names the first segment `arn` - the exported
// attribute - so the composite that reconstruction builds is one the
// provider never accepts from configuration alone.
//
// Claims no IdentityAttrs: which exported attribute (if any single one)
// equals the whole joined ID is issue #44's declared non-goal, same as
// every other server-assigned path here. The ImportSyntax placeholder is
// derived afterwards by deriveDocImportSyntax (issue #176), from the same
// prose segment names in their documented order around the pinned
// separator - documentation only, per TypeIdentity's doc comment.
func tryDocNamedServerSegment(p *proposal, g importGrammarRow) bool {
	if !docNamesServerSegment(g) || g.Separator == nil {
		return false
	}
	p.Bucket = bucketServerAssigned
	p.Rule = "import-grammar precedence: the Import section's own prose names a segment that is an exported attribute, not a configuration argument, so the ID is not reconstructible from configuration"
	p.Notes = append(p.Notes, fmt.Sprintf("import docs name the ID's segments and attribute %s to the Attribute Reference, not the Argument Reference; a server-provided segment makes the identity server-assigned despite the registry's composite primaryIdentifier %s", serverSegmentTokens(g), quoteList(p.PrimaryIdentifier)))
	return true
}

// valueNamesRequiredArgument decides whether a single documented example
// value names exactly one Required argument. The earlier form of this
// check was bare substring containment, and it misfired on five real
// pages: "vpce-3ecf2a57" contains "vpc" without being a VPC's id, and
// "lgw-vpc-assoc-…", "vpcec-…", "ipam-res-disco-assoc-…", "rule-set-id"
// each embed an argument's token inside the resource's OWN id prefix. The
// value's own shape is the discriminator, over its hyphen-split words:
//
//   - An all-alphabetic value is a placeholder description, not an id
//     ("fleet-name", "project-name" - the doc describing "the fleet's
//     name"). It resolves by longest word-suffix against the Required
//     arguments, unless its words describe the resource's own identifier
//     ("rule-set-id" on aws_mailmanager_rule_set's page: strip the
//     trailing "id" and what remains is the type's own noun), which is a
//     server value and refuses.
//   - Anything else is a real id value, and the argument's token must
//     equal the value's ENTIRE alphabetic prefix - the id-scheme part
//     before the opaque tail. "vpc-0f001273ec18911b1" has prefix "vpc",
//     exactly vpc_id's token; "lgw-vpc-assoc-…"'s prefix is
//     "lgwvpcassoc", which is nobody's argument, however many argument
//     tokens float inside it.
func valueNamesRequiredArgument(example, tfType string, required []string) (string, bool) {
	words := strings.Split(strings.ToLower(example), "-")
	allAlpha := true
	for _, w := range words {
		if !isAlphaWord(w) {
			allAlpha = false
			break
		}
	}
	if allAlpha {
		if len(words) >= 2 && words[len(words)-1] == "id" && namesOwnType(words[:len(words)-1], tfType) {
			return "", false // the value describes the resource's own identifier
		}
		for k := len(words); k >= 1; k-- {
			cand := strings.Join(words[len(words)-k:], "")
			if len(cand) < 3 {
				continue
			}
			match := ""
			for _, name := range required {
				if alnum(name) == cand || argToken(name) == cand {
					if match != "" {
						return "", false // two Required arguments claim the same words
					}
					match = name
				}
			}
			if match != "" {
				return match, true
			}
		}
		return "", false
	}
	prefix := ""
	for _, w := range words {
		if !isAlphaWord(w) {
			break
		}
		prefix += w
	}
	if len(prefix) < 3 {
		return "", false
	}
	match := ""
	for _, name := range required {
		if argToken(name) == prefix {
			if match != "" {
				return "", false
			}
			match = name
		}
	}
	return match, match != ""
}

// isAlphaWord reports whether w is non-empty and purely letters.
func isAlphaWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// namesOwnType reports whether words concatenate to the tail of the TF
// type's own name - the same own-noun test importdocs-gen's plain-part
// attribution uses ("rule set" against aws_mailmanager_rule_set).
func namesOwnType(words []string, tfType string) bool {
	base := strings.Join(words, "")
	if base == "" {
		return false
	}
	typeTokens := strings.Split(strings.TrimPrefix(tfType, "aws_"), "_")
	tail := ""
	for k := 1; k <= len(typeTokens); k++ {
		tail = typeTokens[len(typeTokens)-k] + tail
		if tail == base {
			return true
		}
		if len(tail) > len(base) {
			break
		}
	}
	return false
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
		p.CompositeDefaults = compositeDefaultsFor(dc.ArgsInOrder, g)
		p.ArgSource = argSourceArgumentReference
		p.Rule = "import-grammar precedence: Argument Reference's own Required arguments, separator and order recovered from the documented example string"
		p.Notes = append(p.Notes, fmt.Sprintf("import docs example %q splits on %q into exactly %d Required Argument Reference names, matched by name: %s", g.ImportIDExample, sep, len(required), quoteList(dc.ArgsInOrder)))
		return true
	}
	return false
}

// deriveDocImportSyntax is issue #176's first fix, cosmetic-only in the same
// sense tryCompoundArnImportSyntax is (ImportSyntax never decides anything;
// Components/ServerAssigned do): a server-assigned proposal whose
// ImportSyntax would otherwise be templated from the registry's own
// primaryIdentifier NAMES - joined with "-" in the registry's order - gets a
// placeholder derived from the scraped evidence instead: the Import
// section's own per-segment names (id_parts, which by the scrape's arity
// gate cover every segment of the documented example), in the documented
// order, around the scraped separator. The five measured misfires this
// retires (issue #176's table) all had a pinned separator and pinned segment
// order the renderer ignored: aws_appconfig_configuration_profile's doc
// states "configuration profile ID and application ID separated by a colon"
// while the template printed APPLICATIONID-CONFIGURATIONPROFILEID - wrong
// order AND separator - and aws_datazone_form_type's doc states three
// comma-separated segments while the registry's two-name primaryIdentifier
// template printed two.
//
// Runs after every switch rule, ahead of tryCompoundArnImportSyntax (a
// pinned per-segment order and separator beats the compound-ARN "#" idiom:
// aws_ecs_task_set's three comma-joined segments embed two ARNs, and the
// doc's own comma order is the truer placeholder), and never overrides an
// ImportSyntax another rule already derived. A row with
// no scraped segment evidence (no id_parts, no separator, or a segment
// whose prose does not reduce to a placeholder) is left exactly as before:
// the registry-name template, honestly flagged TEMPLATED by the renderer.
func deriveDocImportSyntax(p *proposal, g importGrammarRow) {
	if p.Bucket != bucketServerAssigned || p.DerivedImportSyntax != "" {
		return
	}
	if g.Separator == nil || len(g.IDParts) < 2 {
		return
	}
	parts := make([]string, len(g.IDParts))
	for i, part := range g.IDParts {
		placeholder, ok := docPartPlaceholder(part.Token)
		if !ok {
			return // this segment's prose is not a name; the templated fallback stands
		}
		parts[i] = placeholder
	}
	p.DerivedImportSyntax = strings.Join(parts, *g.Separator)
	p.Notes = append(p.Notes, fmt.Sprintf("ImportSyntax placeholder derived from the Import section's own segment names, order and separator: %s", p.DerivedImportSyntax))
}

// docPartArticles are the leading grammar words a prose segment name drops -
// the same set tools/importdocs-gen/prosename.go's proseArticles holds.
var docPartArticles = map[string]bool{"the": true, "a": true, "an": true, "its": true, "their": true}

// docPartParenRe matches a parenthesized aside inside a segment name ("the
// catalog ID (usually AWS account ID)"), commentary rather than name - the
// same rule prosename.go's parenRe applies.
var docPartParenRe = regexp.MustCompile(`\([^)]*\)`)

// docPartPlaceholder reduces one id_parts token - the Import section's own
// spelling of a segment, which ranges from a literal placeholder name
// ("instance_id", "ID") to a prose phrase ("the configuration profile ID",
// "a comma separated value of DomainIdentifier") - to the uppercase
// placeholder text an ImportSyntax string uses. Three rules, all of them
// prose grammar rather than anything type-specific:
//
//   - a parenthesized aside is commentary, not name;
//   - a format-metalanguage prefix ends at its last "of" ("a comma separated
//     value of DomainIdentifier" names DomainIdentifier);
//   - leading articles are grammar, not name.
//
// What remains must be word-shaped (letters, digits, "_", "-" - the same
// alphabet importdocs-gen's segmentRe accepts); anything else ("it's") is
// not a placeholder and fails closed, leaving the caller's templated
// fallback in place. A token that already is a placeholder name passes
// through as its plain uppercasing, byte-identical to the previous
// derivation for every non-phrasal row.
func docPartPlaceholder(token string) (string, bool) {
	token = docPartParenRe.ReplaceAllString(token, " ")
	words := strings.Fields(token)
	for i := len(words) - 1; i >= 0; i-- {
		if strings.EqualFold(words[i], "of") {
			words = words[i+1:]
			break
		}
	}
	var out []string
	for _, w := range words {
		if docPartArticles[strings.ToLower(w)] {
			continue
		}
		for _, r := range w {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			default:
				return "", false
			}
		}
		out = append(out, strings.ToUpper(w))
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, ""), true
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
		// An ARN's own grammar has "/" and ":" inside it, but never "," or
		// "|" - an arn:-prefixed example carrying either is a JOINED value
		// (aws_controltower_control's "OU-ARN,CONTROL-ARN",
		// aws_ssoadmin_application_assignment's "APP-ARN,id,USER"), and
		// claiming it as one opaque arn attribute would assert an identity
		// the type does not have.
		return !strings.ContainsAny(example, ",|")
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

// compositeDefaultsFor is the slice of the grammar row's omitted_fallbacks
// that names one of this composite's own arguments; nil when there are
// none, so proposals without a documented fallback stay byte-identical to
// what they were before the field existed.
func compositeDefaultsFor(args []string, g importGrammarRow) map[string]string {
	var out map[string]string
	for _, a := range args {
		if v, ok := g.OmittedFallbacks[a]; ok {
			if out == nil {
				out = map[string]string{}
			}
			out[a] = v
		}
	}
	return out
}
