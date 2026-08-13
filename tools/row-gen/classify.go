// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"strings"
)

// bucket is the four-way split the issue's acceptance counts are over. Every
// row in the mapped set (live/mapping.json's via:name, via:alias and
// via:fold rows) lands in exactly one.
type bucket string

const (
	// bucketServerAssigned: primaryIdentifier is wholly read-only. A
	// pastable serverAssigned(...) row.
	bucketServerAssigned bucket = "server-assigned"
	// bucketClientNamed: a single create-only, non-read-only primary
	// identifier, and the TF argument that supplies it is known with
	// confidence (identity schema or carve seed). A pastable TypeIdentity
	// row.
	bucketClientNamed bucket = "client-named"
	// bucketNeedsHandSeparator: primaryIdentifier has more than one part.
	// The character that joins them is in no schema; never pastable. This
	// is the #39 trap aws_route lands in.
	bucketNeedsHandSeparator bucket = "needs-hand-separator"
	// bucketEvidenceOnly: everything else - an ambiguous property shape, a
	// client-named candidate whose argument had to be GUESSED, and every
	// fold (property-child) row whose parent is neither proposed nor
	// admitted. Printed for the record; never pastable.
	bucketEvidenceOnly bucket = "evidence-only"
	// bucketFoldChild: a via:fold row (issue #68) whose fold parent is
	// either already admitted (internal/live/identity.AdmittedTypes) or
	// itself proposed server-assigned/client-named in this same run - the
	// fifth admission path #68 built, a declared property-child whose
	// existence and identity are wholly determined by an admitted parent
	// plus its own arguments. Like bucketNeedsHandSeparator, never
	// pastable: the child's own composite Components (the parent's tuple,
	// plus any further argument like status_code the parent alone does not
	// supply) still need a human's separator and shape choice, the way
	// internal/live/identity/table.go's "Fold-children (issue #68)" section
	// comment documents for the first slice.
	bucketFoldChild bucket = "fold-child"
)

// argSource names where a client-named proposal's TF argument name came
// from, in the issue's stated preference order.
type argSource string

const (
	argSourceIdentitySchema argSource = "provider identity schema (live/survey-full.json)"
	argSourceImportGrammar  argSource = "import grammar (live/import-grammar.json)"
	argSourceCarveSeed      argSource = "carve seed (tools/mapping-gen/carve-seed.json)"
	argSourceGuessed        argSource = "GUESSED: snake_cased CFN property name"
)

// proposal is one TF type's classification, with the registry evidence that
// produced it and, when the type is one of the two proposed buckets, the
// pastable Go snippets for admission.go and table.go.
type proposal struct {
	TFType  string
	CFNType string // "" for a fold row with no CFN type of its own
	Service string // the CFN namespace segment this proposal batches under

	Bucket bucket
	Rule   string // the classification rule that fired, in prose

	// Registry evidence, named so the printed block can cite exactly what
	// it read.
	PrimaryIdentifier []string
	ReadOnly          []string
	CreateOnly        []string

	// Enumeration story: list-free, parent-input (with the required
	// inputs), or not listable.
	Enumeration  string
	ParentInputs []string

	// Client-named only.
	ArgName   string
	ArgSource argSource

	// Fold rows only.
	FoldParent   string
	ParentTFType string // the mapped-set TF type sharing FoldParent, if any
	ParentBucket bucket // that type's own bucket, "" if none found
	ParentKnown  bool
	Notes        []string // free-form additional evidence lines
}

// isSubset reports whether every element of a is in b.
func isSubset(a []string, b map[string]bool) bool {
	if len(a) == 0 {
		return false // vacuous truth is not evidence; treated as ambiguous by the caller
	}
	for _, x := range a {
		if !b[x] {
			return false
		}
	}
	return true
}

// enumerationStory derives the list-free / parent-input / not-listable story
// from the registry's handlers section - rule 4 in the issue, folded into
// every proposal's evidence rather than kept as a separate bucket.
func enumerationStory(list bool, requiredInput []string) (string, []string) {
	switch {
	case !list:
		return "not listable -> client-named only", nil
	case len(requiredInput) > 0:
		return "parent-input", requiredInput
	default:
		return "list-free", nil
	}
}

// classifyMapped classifies one via:name/via:alias row (a row with a CFN
// type) against its registry entry. The four rules run in the order the
// issue states them; the first that fires wins.
func classifyMapped(tf, cfn string, e registryEntry, survey map[string]surveyEntry, importGrammar map[string]importGrammarRow, carveSeed map[string]string) proposal {
	p := proposal{
		TFType:            tf,
		CFNType:           cfn,
		Service:           serviceOf(cfn),
		PrimaryIdentifier: e.PrimaryIdentifier,
		ReadOnly:          e.ReadOnlyProperties,
		CreateOnly:        e.CreateOnlyProperties,
	}
	p.Enumeration, p.ParentInputs = enumerationStory(e.Handlers.List, e.Handlers.ListRequiredInput)

	ro := e.readOnlySet()
	co := e.createOnlySet()

	// Rule 1: primaryIdentifier ⊆ readOnlyProperties -> server-assigned.
	// Checked first and regardless of arity: a server-assigned type is
	// discovered by listing (marker path), never by reconstructing an
	// import string from configuration, so a multi-part identifier here
	// is not the #39 composite-separator problem at all.
	if isSubset(e.PrimaryIdentifier, ro) {
		p.Bucket = bucketServerAssigned
		p.Rule = "primaryIdentifier ⊆ readOnlyProperties"
		return p
	}

	// Rule 2: a single primary identifier, create-only and not read-only
	// -> client-named, provided the TF argument name is known with
	// confidence.
	if len(e.PrimaryIdentifier) == 1 {
		id := e.PrimaryIdentifier[0]
		if co[id] && !ro[id] {
			p.Rule = "len(primaryIdentifier)==1, in createOnlyProperties, not in readOnlyProperties"
			arg, src, confident := resolveArgName(tf, id, survey, importGrammar, carveSeed)
			p.ArgName = arg
			p.ArgSource = src
			if confident {
				p.Bucket = bucketClientNamed
			} else {
				p.Bucket = bucketEvidenceOnly
				p.Notes = append(p.Notes, "argument name GUESSED, not backed by a provider identity schema or the carve seed; evidence only, never a pastable row")
			}
			return p
		}
	}

	// Rule 3: more than one primary identifier and not caught by rule 1 ->
	// composite. The import-string separator is in no schema (the four in
	// DefaultTable are "/", ":", "_", ","; Cloud Control's own is "|"), so
	// this never becomes a pastable row. This is where aws_route lands.
	if len(e.PrimaryIdentifier) > 1 {
		p.Bucket = bucketNeedsHandSeparator
		p.Rule = "len(primaryIdentifier) > 1 (composite; the separator is in no schema)"
		return p
	}

	// Neither shape fits: a singleton primary identifier that is neither
	// read-only nor create-only (e.g. a fully mutable property), or no
	// primary identifier recorded at all.
	p.Bucket = bucketEvidenceOnly
	p.Rule = "primaryIdentifier does not fit the server-assigned or client-named shape"
	return p
}

// classifyFold classifies one via:fold row: a TF type mapping-gen decided is
// a property-child of a CFN parent rather than a type of its own.
//
// Issue #68 built the fifth admission path this shape needed - a declared
// property-child whose existence and identity are wholly determined by an
// admitted parent plus its own arguments - so a fold row is no longer
// automatically evidence-only: it is bucketFoldChild (proposable, though
// still never pastable - see that bucket's own doc comment) whenever its
// fold parent already has an admission this run can see, checked two ways,
// weakest last:
//
//  1. admitted[parentTFType] - the fold parent's TF type is already in
//     internal/live/identity.AdmittedTypes(), a fact this run reads
//     straight off the real table rather than from its own registry-only
//     classification of that parent. This is what catches
//     aws_api_gateway_method: row-gen's own classifyMapped rule puts it in
//     bucketNeedsHandSeparator (a composite primaryIdentifier, the same as
//     aws_route), but a human already hand-ratified it into DefaultTable,
//     and the fold rule only cares whether an admission exists to key on,
//     not which bucket produced it.
//  2. Failing that, the same check the pre-#68 rule already made: the fold
//     parent is itself proposed server-assigned or client-named by this
//     same run (mapped), which is not yet an admission but is strong
//     enough evidence to name a future one - the aws_prometheus_workspace
//     shape.
//
// admitted may be nil, which is "run me over evidence alone, the way a
// caller with no built identity table has to" - every admitted-only match
// simply never fires, falling through to the weaker mapped-parent check or
// to evidence-only, never a panic.
func classifyFold(tf, foldParent string, mapped []proposal, admitted map[string]bool) proposal {
	p := proposal{
		TFType:     tf,
		Service:    serviceOf(foldParent),
		Rule:       "via==fold: property-child of " + foldParent,
		FoldParent: foldParent,
	}
	for _, m := range mapped {
		if m.CFNType == foldParent {
			p.ParentTFType = m.TFType
			p.ParentBucket = m.Bucket
			p.ParentKnown = true
			break
		}
	}
	switch {
	case p.ParentKnown && admitted[p.ParentTFType]:
		p.Bucket = bucketFoldChild
		p.Notes = append(p.Notes, "proposal: parent-derived admission (issue #68's fold-child path) keyed on "+p.ParentTFType+", already admitted")
	case p.ParentKnown && (p.ParentBucket == bucketServerAssigned || p.ParentBucket == bucketClientNamed):
		p.Bucket = bucketFoldChild
		p.Notes = append(p.Notes, "proposal: parent-derived admission (issue #68's fold-child path) keyed on "+p.ParentTFType+" once it is ratified ("+string(p.ParentBucket)+")")
	case p.ParentKnown:
		p.Bucket = bucketEvidenceOnly
		p.Notes = append(p.Notes, "parent "+p.ParentTFType+" is neither admitted nor itself proposed ("+string(p.ParentBucket)+"); no parent-derived admission to propose yet")
	default:
		p.Bucket = bucketEvidenceOnly
		p.Notes = append(p.Notes, "no mapped TF type resolves to the fold parent "+foldParent+"; nothing to key a parent-derived admission on")
	}
	return p
}

// resolveArgName is the client-named argument-name source order issue #52's
// second half restates: the provider's own identity schema first, then the
// provider's own Import documentation (live/import-grammar.json,
// tools/importdocs-gen) - trusted only when it names exactly one argument
// and the doc itself says the ID is composed of configuration arguments
// rather than a server-assigned opaque value, the same confidence bar the
// identity schema already clears by construction - then the carve seed,
// and a snake-cased guess off the CFN property name last, which is never
// confident, so callers must check the third return value before treating
// the row as pastable. import-grammar sits ahead of the carve seed because
// it is a second authoritative source (the provider's own docs, not a
// vendored snapshot of a sibling project's table); it sits behind the
// identity schema because a schema's required_for_import is the provider's
// own structured answer, sharper than a docs paragraph parsed at pin time.
func resolveArgName(tf, cfnProperty string, survey map[string]surveyEntry, importGrammar map[string]importGrammarRow, carveSeed map[string]string) (string, argSource, bool) {
	if s, ok := survey[tf]; ok {
		if arg, ok := s.identityArg(); ok {
			return arg, argSourceIdentitySchema, true
		}
	}
	if g, ok := importGrammar[tf]; ok {
		if g.ComposedOfArguments != nil && *g.ComposedOfArguments && len(g.Arguments) == 1 {
			return g.Arguments[0], argSourceImportGrammar, true
		}
	}
	if arg, ok := carveSeed[tf]; ok {
		return arg, argSourceCarveSeed, true
	}
	return snakeCase(cfnProperty), argSourceGuessed, false
}

// applyImportGrammarDemotions is the automated rule-1 check issue #55
// names: a type row-gen proposed server-assigned purely from the CFN
// registry's read-only primaryIdentifier, whose provider Import
// documentation shows an ID actually built from configuration arguments,
// is not server-assigned - CloudFormation and the provider model the same
// AWS API independently, and a composite documented import ID is exactly
// the shape the Lambda pilot found by hand for aws_lambda_alias and
// aws_lambda_layer_version_permission (see
// internal/live/identity/table.go's "Rejected" comment). Those two rows
// were kept out of DefaultTable manually; this is that same check, run
// over every proposal instead of by a human reading two docs.
//
// Only bucketServerAssigned rows are ever touched - a client-named or
// needs-hand-separator proposal has no server-assigned claim to correct,
// and an evidence-only row is already not pastable. Mutates proposals in
// place; importGrammar may be empty (loadImportGrammar's committed-file-
// missing case), in which case this is a no-op.
func applyImportGrammarDemotions(proposals []proposal, importGrammar map[string]importGrammarRow) {
	for i := range proposals {
		p := &proposals[i]
		if p.Bucket != bucketServerAssigned {
			continue
		}
		g, ok := importGrammar[p.TFType]
		if !ok || g.ComposedOfArguments == nil || !*g.ComposedOfArguments {
			continue
		}
		p.Bucket = bucketEvidenceOnly
		p.Notes = append(p.Notes, fmt.Sprintf("import docs show argument-composed ID: %s", g.ImportIDExample))
	}
}

// serviceOf pulls the namespace segment out of a CFN type name
// (AWS::Lambda::Function -> "Lambda"), which is what proposals batch by.
func serviceOf(cfnType string) string {
	parts := strings.Split(cfnType, "::")
	if len(parts) >= 2 {
		return parts[1]
	}
	return cfnType
}
