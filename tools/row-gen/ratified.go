// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file gives the ratified rows a home outside the file -emit overwrites.
//
// # Why it exists
//
// -emit copies every non-RecordBacked row verbatim out of the corpus
// [buildEmitFiles] hands it (emit.go's [emittedRows]); the fresh classifier
// contributes no row, ever. That corpus used to be [identity.DefaultTable] -
// -emit's own output from the previous run. Measured at 5502e8a3de: empty
// DefaultTable's literal and run -emit twice and the result is a 14-row table
// - exactly [recordBackedTypes]' derived set - byte-identical across both
// runs, exit 0, converged, 878 AWS rows gone. That 14-row table is as much a
// fixed point of -emit as the 892-row one, so a wrong retraction could not be
// undone by regenerating: reverting the cause and re-emitting re-emitted the
// smaller table, because the smaller table was now the input. Only
// `git checkout --` restored it. That is issue #263.
//
// ratified.json is the cure and -emit now reads it. The ratified corpus lives
// in a file no generator writes, so reverting the cause of a retraction
// restores the rows - they were never the generator's to lose. Three reads
// moved together to make that true: [emittedRows]' corpus, [markerlessRoster]'s
// server-assignment verdict, and [buildConvergence]'s population. Two did not,
// and both are comparisons against what currently ships rather than statements
// about what is ratified: retraction.go's gate and [recordBackedRows]'
// dropped-row check.
//
// The other half of what this buys is the workflow. Adding a row used to mean
// hand-editing a file stamped "Code generated ... DO NOT EDIT" that is this
// generator's own output. It is now an edit to this file followed by a
// regeneration.
//
// # Why a file naming 878 resource types is not the hand-wiring the standing
// rule forbids
//
// The rule the project repeats - "a fix that names a concrete aws_* type is
// the wrong fix" - is about DECISIONS: a lookup, a branch or a comparison
// keyed on one named type, which buys the cohort in front of you and nothing
// in the ~1700-type tail. live/derivation_guard_test.go makes exactly that
// distinction, counting a literal inside a function body separately from one
// in a package-level table, and its own prose calls the table form
// "sanctioned".
//
// These 878 rows are not new decisions. Every one of them was ratified by a
// human already, and today they sit inside internal/live/identity's
// table_generated.go - a file carrying "DO NOT EDIT" whose whole content is
// overwritten on every run. Storing hand-ratified data in a generator's output
// file is what makes the data destructible; moving it to an input changes
// nothing about how many types are described by hand and everything about
// whether that description survives a bad run. The count of hand-described
// types is unchanged by this file to the row: 878 before, 878 after.
//
// The derivation debt is unchanged too, and it stays measured where it already
// is. live/rowgen-convergence.json records which of these rows a fresh
// classify.go run reproduces on its own; shrinking the corrected half is the
// work, and moving the rows does not shrink or grow it by one.
//
// # What is stored, and what is not
//
// The RecordBacked rows are NOT here. Nothing about such a row is ratified -
// it carries no components, no reason string and no identity attributes, only
// its own type name and the flag - and [recordBackedTypes] derives the whole
// set from live/logical-schemas.json. A derived row belongs to its derivation,
// not to a ratification ledger.
//
// The three recomputed fields ARE here, at the values the last -emit left
// them: [identity.Component.ServerAssignedIfAbsent] (#190),
// [identity.Component.Attrs] on a cloud-bearing component (#241) and a
// server-assigned row's [identity.TypeIdentity.IdentityAttrs] (#197). That is
// deliberate and it is what makes this migration lossless rather than
// approximately lossless: emit.go's three merges only ever SET a field, never
// clear one, so re-running them over a stored post-merge value is idempotent
// and the rendered table is byte-identical. Storing the pre-merge values
// instead would require reconstructing them, and there is nothing to
// reconstruct them from. The stickiness that follows - evidence that stops
// saying "server-assigned" does not unset the flag - is the behaviour
// [identity.DefaultTable] already has today for the same reason, so this file
// preserves it rather than introducing it.
const ratifiedJSONRel = "tools/row-gen/ratified.json"

// ratifiedRow is one ratified [identity.TypeIdentity] as stored. It mirrors
// the struct field for field; TestRatifiedMirrorsEveryTypeIdentityField fails
// the build if [identity.TypeIdentity] gains, loses or renames one, because a
// silently-dropped field is a field the table would silently lose.
//
// Slices are pointers on purpose. [identity.TypeIdentity.IdentityAttrs] gives
// nil and empty different meanings - "nothing is known" against "no attribute
// of this type carries its identity" - and five committed rows are empty and
// non-nil today. `omitempty` on a plain slice collapses the two; a nil pointer
// is omitted, and a pointer to an empty slice renders `[]`, so both survive.
type ratifiedRow struct {
	Type               string               `json:"type"`
	ServerAssigned     bool                 `json:"server_assigned,omitempty"`
	Reason             string               `json:"reason,omitempty"`
	RecordBacked       bool                 `json:"record_backed,omitempty"`
	Components         *[]ratifiedComponent `json:"components,omitempty"`
	ImportSyntax       string               `json:"import_syntax,omitempty"`
	IdentityAttrs      *[]string            `json:"identity_attrs,omitempty"`
	IdentityObjectOnly bool                 `json:"identity_object_only,omitempty"`
	Synthesized        bool                 `json:"synthesized,omitempty"`
	Admits             identity.Admission   `json:"admits,omitempty"`
}

// ratifiedComponent mirrors [identity.Component] under the same contract as
// [ratifiedRow].
type ratifiedComponent struct {
	Literal                string              `json:"literal,omitempty"`
	Attrs                  *[]string           `json:"attrs,omitempty"`
	Default                string              `json:"default,omitempty"`
	ServerAssignedIfAbsent bool                `json:"server_assigned_if_absent,omitempty"`
	Cloud                  identity.CloudValue `json:"cloud,omitempty"`
	IdentityAttr           string              `json:"identity_attr,omitempty"`
	SoleElement            bool                `json:"sole_element,omitempty"`
	PerElement             bool                `json:"per_element,omitempty"`
}

// toRatified converts one live row to its stored form.
func toRatified(e identity.TypeIdentity) ratifiedRow {
	r := ratifiedRow{
		Type:               e.Type,
		ServerAssigned:     e.ServerAssigned,
		Reason:             e.Reason,
		RecordBacked:       e.RecordBacked,
		ImportSyntax:       e.ImportSyntax,
		IdentityAttrs:      strsPtr(e.IdentityAttrs),
		IdentityObjectOnly: e.IdentityObjectOnly,
		Synthesized:        e.Synthesized,
		Admits:             e.Admits,
	}
	if e.Components != nil {
		comps := make([]ratifiedComponent, 0, len(e.Components))
		for _, c := range e.Components {
			comps = append(comps, ratifiedComponent{
				Literal:                c.Literal,
				Attrs:                  strsPtr(c.Attrs),
				Default:                c.Default,
				ServerAssignedIfAbsent: c.ServerAssignedIfAbsent,
				Cloud:                  c.Cloud,
				IdentityAttr:           c.IdentityAttr,
				SoleElement:            c.SoleElement,
				PerElement:             c.PerElement,
			})
		}
		r.Components = &comps
	}
	return r
}

// fromRatified converts one stored row back to its live form. It is
// toRatified's exact inverse; TestRatifiedRoundTripsEveryField and
// TestRatifiedRoundTripsEveryCommittedRow are the proof.
func fromRatified(r ratifiedRow) identity.TypeIdentity {
	e := identity.TypeIdentity{
		Type:               r.Type,
		ServerAssigned:     r.ServerAssigned,
		Reason:             r.Reason,
		RecordBacked:       r.RecordBacked,
		ImportSyntax:       r.ImportSyntax,
		IdentityAttrs:      strsValue(r.IdentityAttrs),
		IdentityObjectOnly: r.IdentityObjectOnly,
		Synthesized:        r.Synthesized,
		Admits:             r.Admits,
	}
	if r.Components != nil {
		comps := make([]identity.Component, 0, len(*r.Components))
		for _, c := range *r.Components {
			comps = append(comps, identity.Component{
				Literal:                c.Literal,
				Attrs:                  strsValue(c.Attrs),
				Default:                c.Default,
				ServerAssignedIfAbsent: c.ServerAssignedIfAbsent,
				Cloud:                  c.Cloud,
				IdentityAttr:           c.IdentityAttr,
				SoleElement:            c.SoleElement,
				PerElement:             c.PerElement,
			})
		}
		e.Components = comps
	}
	return e
}

// strsPtr and strsValue carry the nil/empty distinction across the JSON
// boundary in both directions. See [ratifiedRow]'s doc comment.
func strsPtr(s []string) *[]string {
	if s == nil {
		return nil
	}
	return &s
}

func strsValue(p *[]string) []string {
	if p == nil {
		return nil
	}
	if *p == nil {
		// `"identity_attrs": null` decodes to a nil slice behind a non-nil
		// pointer. renderRatified never writes that shape, but a hand edit
		// can, and it should mean the same thing as omitting the key.
		return nil
	}
	return *p
}

// loadRatified reads the ratified corpus, keyed by provider-local type name.
//
// It refuses a row whose map key and own Type field disagree: the key is what
// every caller looks a row up by and Type is what gets rendered into the
// table, so a mismatch would emit a row under one name carrying another's
// identity - a wrong marker, which outranks a missing one.
func loadRatified(path string) (map[string]identity.TypeIdentity, error) {
	data, err := os.ReadFile(path) //nolint:gosec // fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var stored map[string]ratifiedRow
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&stored); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	out := make(map[string]identity.TypeIdentity, len(stored))
	for key, row := range stored {
		if row.Type != key {
			return nil, fmt.Errorf("%s: row keyed %q declares type %q - the key and the row must name the same type", path, key, row.Type)
		}
		if row.RecordBacked {
			return nil, fmt.Errorf("%s: %s is stored as RecordBacked, but a record-backed row is derived from %s and never ratified", path, key, logicalSchemasJSONRel)
		}
		out[key] = fromRatified(row)
	}
	return out, nil
}

// renderRatified is loadRatified's inverse over a whole corpus: the canonical
// bytes for a set of ratified rows. TestRatifiedJSONIsCanonical holds the
// committed file equal to it, so a hand edit lands in the one spelling the
// loader round-trips.
func renderRatified(rows map[string]identity.TypeIdentity) ([]byte, error) {
	stored := make(map[string]ratifiedRow, len(rows))
	for t, e := range rows {
		stored[t] = toRatified(e)
	}
	// encoding/json sorts map keys, so the output is deterministic without a
	// key list of our own.
	out, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// ratifiedRowsOf is the non-RecordBacked half of a live table, which is
// exactly what ratified.json holds. It exists so the migration and the
// round-trip proof select the same set by the same rule rather than by two
// copies of the condition.
func ratifiedRowsOf(table map[string]identity.TypeIdentity) map[string]identity.TypeIdentity {
	out := make(map[string]identity.TypeIdentity, len(table))
	for t, e := range table {
		if e.RecordBacked {
			continue
		}
		out[t] = e
	}
	return out
}

// loadEmittedTable is the read-only half of -emit: the exact rows -emit would
// write, without writing them. It is what the measuring modes (-convergence,
// -propose) compare the fresh classifier against, so that the population they
// measure and the population -emit ships can never be two different sets.
//
// It deliberately uses [recordBackedTypes] rather than [recordBackedRows]:
// the derived set is identical, but recordBackedRows also runs -emit's
// dropped-row refusal, and a mode that writes nothing should not fail on a
// condition only a write can cause.
func loadEmittedTable(root string, proposals []proposal) (map[string]identity.TypeIdentity, error) {
	ratified, err := loadRatified(filepath.Join(root, ratifiedJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ratifiedJSONRel, err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", importGrammarJSONRel, err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", surveyJSONRel, err)
	}
	logical, err := loadLogicalSchemas(filepath.Join(root, logicalSchemasJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", logicalSchemasJSONRel, err)
	}
	vetoed := markerlessRoster(ratified, survey, proposals, grammar)
	rows, _ := emittedRows(ratified, setOf(recordBackedTypes(logical)), grammar, survey, setOf(vetoed))
	return rows, nil
}

// sortedRatifiedKeys is the corpus' key order for any message that lists
// types.
func sortedRatifiedKeys(m map[string]identity.TypeIdentity) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mirroredStructs pairs each live struct with its stored mirror, for the
// field-parity guard. Adding a struct to [ratifiedRow]'s reach means adding it
// here.
func mirroredStructs() []struct{ Live, Stored reflect.Type } {
	return []struct{ Live, Stored reflect.Type }{
		{reflect.TypeOf(identity.TypeIdentity{}), reflect.TypeOf(ratifiedRow{})},
		{reflect.TypeOf(identity.Component{}), reflect.TypeOf(ratifiedComponent{})},
	}
}
