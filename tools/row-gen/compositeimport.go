// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is GitHub issue #337: the residue #329's fix cannot reach, named
// and classified by a derived rule rather than left to be discovered one
// estate at a time.
//
// # The question, in one sentence
//
// internal/live/identity's record-located mechanism records the applied
// object's `id` and hands that string back to import on the next run, so it
// is only correct for a type whose `id` IS the whole import string - and for
// a markerless type whose documented import is composite, nothing said which
// of the two shapes it was.
//
// #329 answered it for the types the provider's own wire identity schema
// settles: when that schema requires `id` plus at least one other attribute,
// the string is not the whole identity and the record carries the identity
// OBJECT instead. That gate is safe by construction because identity
// attributes are unordered - no separator and no component order is ever
// guessed.
//
// This file is about the types that gate cannot see. Measured at
// f78db6c375, of [identity.MarkerlessTypes]' 140 types 43 document a
// composite import (a non-null separator in live/import-grammar.json) and 27
// of those carry no wire identity schema at all. For those 27 the schema
// says nothing, today's rule records `id`, and whether that is the whole
// string or a fragment was simply unknown.
//
// # What settles it, and why it is sound
//
// The provider's own Attribute Reference bullet for `id`, scraped by
// tools/importdocs-gen/idattribute.go, which reads that sentence for one
// thing only: a stated composite form and the character joining it.
//
// The verdict here is not that sentence alone. It is that sentence AGREEING
// with the Import section's independently-scraped separator - two different
// sections of the page, written for different purposes, scraped by different
// readers, naming the same join character for the same type. That is the
// same standard sourcesAgree applies in markerless.go, and it is what keeps
// this out of the trap #309's own scouting documented: no component ORDER is
// read here, from prose or from anywhere else, because the consumer does not
// need one. "Is `id` the whole import string" is a yes/no question, and a
// yes needs no grammar.
//
// Everything else is UNPROVEN and stays that way. Seven of the 27 carry an
// `id` bullet whose own words describe a single value under a composite
// import ("The EMR Instance ID", "ID of the traffic policy") - the exact
// leaf shape #329 exists to refuse - and thirteen carry no `id` bullet at
// all. This file does not promote either group to a verdict: a description
// that fails to state a composite is weak evidence of a leaf, not proof of
// one, and the roster records the reason rather than inventing a class.
//
// # What this is not
//
// It is a classification and an artifact, not an admission change. Nothing
// here is wired into [identity.LocatedType], and doing so is a separate,
// sequenced step (#309): moving the 22 unproven types from a silent wrong
// record to an honest refusal is the right direction, and it is a support
// change worth landing on its own evidence rather than as a side effect of
// building the evidence.
//
// It names no resource type, here or in anything it reads.

// compositeImportArtifactPath is where the roster is written, relative to
// the repository root.
const compositeImportArtifactPath = "live/composite-import-roster.json"

// The verdicts, and the reasons behind an unproven one.
const (
	// compositeVerdictWhole: the exported `id` is provably the whole
	// documented import string, so recording it round-trips.
	compositeVerdictWhole = "id-is-whole-import-string"

	// compositeVerdictUnproven: nothing available says which shape this
	// type's `id` is.
	compositeVerdictUnproven = "unproven"

	// The three ways a type lands on compositeVerdictUnproven.
	compositeReasonNoIDBullet    = "the page's Attribute Reference documents no `id` attribute, so no source describes what the exported id holds"
	compositeReasonNotComposite  = "the page's Attribute Reference documents `id` without stating a composite form, which is not evidence either way: it is equally the wording used for a bare server-minted leaf under a composite import"
	compositeReasonSeparatorDiff = "the Attribute Reference states a composite `id` whose separator disagrees with the Import section's own, so the two sections of the page describe different strings and neither corroborates the other"
)

// compositeImportArtifact is the whole roster.
type compositeImportArtifact struct {
	GeneratedBy string                 `json:"generated_by"`
	Question    string                 `json:"question"`
	Summary     compositeImportTotals  `json:"summary"`
	Types       []compositeImportEntry `json:"types"`
}

type compositeImportTotals struct {
	// Markerless is len(identity.MarkerlessTypes), the population every
	// other figure here is a subset of.
	Markerless int `json:"markerless_types"`

	// CompositeImport is how many of those document a composite import - a
	// non-null separator in live/import-grammar.json.
	CompositeImport int `json:"markerless_with_composite_import"`

	// WireIdentitySchema is how many of THOSE carry a provider wire
	// identity schema, and so are already answered by #329's gate rather
	// than by this roster. Reported so the two populations add up in view
	// rather than in a reader's head.
	WireIdentitySchema int `json:"of_those_with_a_wire_identity_schema"`

	// Residue is the roster's own population: composite import, no wire
	// identity schema.
	Residue int `json:"residue_with_no_wire_identity_schema"`

	// Whole and Unproven split Residue.
	Whole    int `json:"of_residue_id_proven_whole"`
	Unproven int `json:"of_residue_unproven"`
}

// compositeImportEntry is one type's verdict and the evidence for it.
type compositeImportEntry struct {
	Type string `json:"type"`

	// Verdict is compositeVerdictWhole or compositeVerdictUnproven.
	Verdict string `json:"verdict"`

	// ImportSeparator is live/import-grammar.json's own scraped separator
	// for the documented import string.
	ImportSeparator string `json:"import_separator"`

	// ImportIDExample is the documented example, carried so a reader can
	// see the string the verdict is about.
	ImportIDExample string `json:"import_id_example"`

	// StatedSeparator and IDDescription are the Attribute Reference's own
	// evidence, empty when the page states none.
	StatedSeparator string `json:"id_attribute_stated_separator,omitempty"`
	IDDescription   string `json:"id_attribute_description,omitempty"`

	// Reason is why an unproven type is unproven. Empty on a whole verdict.
	Reason string `json:"reason,omitempty"`
}

// runCompositeImport builds the roster and writes it.
func runCompositeImport(out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		return err
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		return err
	}

	art := buildCompositeImportArtifact(identity.MarkerlessTypes, survey, grammar)
	art.GeneratedBy = "tools/row-gen -composite-import"

	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, compositeImportArtifactPath), data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}

	fmt.Fprintf(errOut, "row-gen: wrote %s\n", compositeImportArtifactPath)
	fmt.Fprintf(out, "markerless=%d composite-import=%d with-wire-identity-schema=%d residue=%d\n",
		art.Summary.Markerless, art.Summary.CompositeImport, art.Summary.WireIdentitySchema, art.Summary.Residue)
	fmt.Fprintf(out, "of the residue: id proven whole=%d, unproven=%d\n", art.Summary.Whole, art.Summary.Unproven)
	return nil
}

// buildCompositeImportArtifact is runCompositeImport without the file
// handling, so a test can read the result.
//
// markerless is [identity.MarkerlessTypes] passed in rather than read here,
// for the reason ratified.go states about every other generator input: a
// function that reads its own package's globals cannot be measured against
// anything else.
func buildCompositeImportArtifact(markerless map[string]struct{}, survey map[string]surveyEntry, grammar map[string]importGrammarRow) compositeImportArtifact {
	art := compositeImportArtifact{
		Question: "for a markerless type with a documented composite import and no wire identity schema, " +
			"is the exported `id` the whole import string (so a located record of it round-trips) or a bare leaf?",
	}
	art.Summary.Markerless = len(markerless)

	names := make([]string, 0, len(markerless))
	for t := range markerless {
		names = append(names, t)
	}
	sort.Strings(names)

	for _, t := range names {
		g, ok := grammar[t]
		if !ok || g.Separator == nil || *g.Separator == "" {
			continue
		}
		art.Summary.CompositeImport++

		// The wire identity schema is what #329's gate reads. A survey
		// entry with an identity block is exactly live/survey-full.json's
		// own identity_schema signal - survey-gen writes the block only
		// when the provider serves a schema - so this asks the same
		// question that gate asks, from the same source, without needing a
		// live provider here.
		if e, known := survey[t]; known && e.Identity != nil {
			art.Summary.WireIdentitySchema++
			continue
		}
		art.Summary.Residue++
		art.Types = append(art.Types, classifyCompositeImport(t, g))
	}
	for _, e := range art.Types {
		if e.Verdict == compositeVerdictWhole {
			art.Summary.Whole++
		} else {
			art.Summary.Unproven++
		}
	}
	return art
}

// classifyCompositeImport is the rule, for one type.
//
// The whole verdict needs both halves: the Attribute Reference states a
// composite `id` AND names the same join character the Import section's own
// scrape found. One half alone is one reading of one sentence.
func classifyCompositeImport(t string, g importGrammarRow) compositeImportEntry {
	e := compositeImportEntry{
		Type:            t,
		Verdict:         compositeVerdictUnproven,
		ImportSeparator: *g.Separator,
		ImportIDExample: g.ImportIDExample,
	}
	switch {
	case g.IDAttribute == nil:
		e.Reason = compositeReasonNoIDBullet
	case g.IDAttribute.StatedSeparator == "":
		e.IDDescription = g.IDAttribute.Description
		e.Reason = compositeReasonNotComposite
	case g.IDAttribute.StatedSeparator == *g.Separator:
		e.Verdict = compositeVerdictWhole
		e.StatedSeparator = g.IDAttribute.StatedSeparator
		e.IDDescription = g.IDAttribute.Description
	default:
		e.StatedSeparator = g.IDAttribute.StatedSeparator
		e.IDDescription = g.IDAttribute.Description
		e.Reason = compositeReasonSeparatorDiff
	}
	return e
}
