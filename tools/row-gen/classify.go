// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "strings"

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
	// fold (property-child) row. Printed for the record; never pastable.
	bucketEvidenceOnly bucket = "evidence-only"
)

// argSource names where a client-named proposal's TF argument name came
// from, in the issue's stated preference order.
type argSource string

const (
	argSourceIdentitySchema argSource = "provider identity schema (live/survey-full.json)"
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
func classifyMapped(tf, cfn string, e registryEntry, survey map[string]surveyEntry, carveSeed map[string]string) proposal {
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
			arg, src, confident := resolveArgName(tf, id, survey, carveSeed)
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
// a property-child of a CFN parent rather than a type of its own. It is
// always evidence-only - row-gen proposes no pastable row for a fold child,
// only the parent-derived admission note the issue's rule 5 asks for.
func classifyFold(tf, foldParent string, mapped []proposal) proposal {
	p := proposal{
		TFType:     tf,
		Service:    serviceOf(foldParent),
		Bucket:     bucketEvidenceOnly,
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
	if p.ParentKnown && (p.ParentBucket == bucketServerAssigned || p.ParentBucket == bucketClientNamed) {
		p.Notes = append(p.Notes, "proposal: parent-derived admission keyed on "+p.ParentTFType+" once it is ratified ("+string(p.ParentBucket)+")")
	} else if p.ParentKnown {
		p.Notes = append(p.Notes, "parent "+p.ParentTFType+" is not itself proposed ("+string(p.ParentBucket)+"); no parent-derived admission to propose yet")
	} else {
		p.Notes = append(p.Notes, "no mapped TF type resolves to the fold parent "+foldParent+"; nothing to key a parent-derived admission on")
	}
	return p
}

// resolveArgName is the client-named argument-name source order the issue
// states: the provider's own identity schema first, the carve seed second,
// and a snake-cased guess off the CFN property name last - which is never
// confident, so callers must check the third return value before treating
// the row as pastable.
func resolveArgName(tf, cfnProperty string, survey map[string]surveyEntry, carveSeed map[string]string) (string, argSource, bool) {
	if s, ok := survey[tf]; ok {
		if arg, ok := s.identityArg(); ok {
			return arg, argSourceIdentitySchema, true
		}
	}
	if arg, ok := carveSeed[tf]; ok {
		return arg, argSourceCarveSeed, true
	}
	return snakeCase(cfnProperty), argSourceGuessed, false
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
