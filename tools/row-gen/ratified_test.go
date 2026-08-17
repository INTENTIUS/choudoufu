// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// loadEmittedTableForTest is the rows -emit would write, the population
// buildConvergence measures against. It is loadEmittedTable's test spelling.
func loadEmittedTableForTest(t *testing.T, proposals []proposal) map[string]identity.TypeIdentity {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := loadEmittedTable(root, proposals)
	if err != nil {
		t.Fatalf("loadEmittedTable: %v", err)
	}
	return rows
}

// loadRatifiedForTest reads tools/row-gen/ratified.json the same way a caller
// of loadRatified would.
func loadRatifiedForTest(t *testing.T) map[string]identity.TypeIdentity {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	rows, err := loadRatified(filepath.Join(root, ratifiedJSONRel))
	if err != nil {
		t.Fatalf("loadRatified: %v", err)
	}
	return rows
}

// TestRatifiedMirrorsEveryTypeIdentityField is the guard that keeps the
// migration lossless as the struct moves.
//
// [ratifiedRow] and [ratifiedComponent] are hand-written mirrors, and a
// hand-written mirror's failure mode is silence: add a field to
// [identity.TypeIdentity], forget the mirror, and every row round-trips
// "successfully" with the new field dropped. emit.go's renderStruct is
// reflection-driven for exactly this reason and says so in its own doc
// comment; this test buys the mirror the same property.
//
// It checks names in both directions, so a rename fails too, and it requires
// every mirror field to carry a json tag, because an untagged field encodes
// under its Go name and would not survive a later rename either.
func TestRatifiedMirrorsEveryTypeIdentityField(t *testing.T) {
	for _, pair := range mirroredStructs() {
		live, stored := pair.Live, pair.Stored

		liveNames := exportedFieldNames(live)
		storedNames := exportedFieldNames(stored)

		for name := range liveNames {
			if !storedNames[name] {
				t.Errorf("%s has field %s and its stored mirror %s does not.\n"+
					"A field with no mirror is dropped silently by toRatified and lost from every row in %s. Add it to the mirror, to toRatified and to fromRatified.",
					live, name, stored, ratifiedJSONRel)
			}
		}
		for name := range storedNames {
			if !liveNames[name] {
				t.Errorf("stored mirror %s has field %s, which %s does not.\n"+
					"fromRatified cannot put it anywhere, so it is data nothing reads.", stored, name, live)
			}
		}

		for i := 0; i < stored.NumField(); i++ {
			f := stored.Field(i)
			if f.PkgPath != "" {
				continue
			}
			if f.Tag.Get("json") == "" {
				t.Errorf("%s.%s carries no json tag; it would encode under its Go name and a rename would silently change the on-disk spelling", stored, f.Name)
			}
		}
	}
}

func exportedFieldNames(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		out[f.Name] = true
	}
	return out
}

// TestRatifiedRoundTripsEveryField is the losslessness proof over the whole
// struct rather than over the rows that happen to exist today.
//
// Three of [identity.TypeIdentity]'s fields - IdentityObjectOnly, Synthesized
// and Admits - are zero in every one of the 892 committed rows, because only
// [identity.SynthesizeTypeIdentity] sets them. A round-trip proof over the
// committed corpus alone would therefore say nothing about them, and would
// keep saying nothing after somebody dropped them from the mirror. So this
// test builds a row with every exported field non-zero, asserts by reflection
// that it really did, and then round-trips it through the same renderer and
// loader the file uses.
//
// It also pins the nil/empty slice distinction in both directions, which is
// the one shape a plain `omitempty` would collapse and which five committed
// rows depend on.
func TestRatifiedRoundTripsEveryField(t *testing.T) {
	full := identity.TypeIdentity{
		Type:           "aws_zzz_every_field",
		ServerAssigned: true,
		Reason:         "every field set, so that dropping one from the mirror fails here",
		// RecordBacked is set here and nowhere else in this test.
		// loadRatified refuses a stored RecordBacked row outright - such a row
		// is derived, not ratified - so the field cannot travel through the
		// file leg below, and proving it round-trips through the conversion
		// pair is the strongest statement available about it.
		RecordBacked: true,
		Components: []identity.Component{
			{
				Literal:                "/",
				Attrs:                  []string{"a", "b"},
				Default:                "d",
				ServerAssignedIfAbsent: true,
				Cloud:                  identity.CloudAccountID,
				IdentityAttr:           "*",
				SoleElement:            true,
			},
			{Attrs: []string{}}, // empty, non-nil
		},
		ImportSyntax:       "A/B",
		IdentityAttrs:      []string{"arn"},
		IdentityObjectOnly: true,
		Synthesized:        true,
		Admits:             identity.AdmitSchema,
	}
	assertEveryExportedFieldNonZero(t, reflect.ValueOf(full), "TypeIdentity")
	assertEveryExportedFieldNonZero(t, reflect.ValueOf(full.Components[0]), "Component")

	// Leg one: the conversion pair, over every field including RecordBacked.
	if got := fromRatified(toRatified(full)); !reflect.DeepEqual(got, full) {
		t.Errorf("toRatified/fromRatified changed a row in which every field is set\n got: %#v\nwant: %#v", got, full)
	}

	// Leg two: the file. Same row minus RecordBacked, which the ledger refuses.
	viaFile := full
	viaFile.RecordBacked = false

	cases := map[string]identity.TypeIdentity{
		"every storable field set": viaFile,
		"empty non-nil attrs":      {Type: "aws_zzz_empty", IdentityAttrs: []string{}},
		"nil attrs":                {Type: "aws_zzz_nil"},
		"empty non-nil comps":      {Type: "aws_zzz_nocomps", Components: []identity.Component{}},
		"zero row but the type":    {Type: "aws_zzz_bare"},
	}

	in := make(map[string]identity.TypeIdentity, len(cases))
	for _, row := range cases {
		in[row.Type] = row
	}
	data, err := renderRatified(in)
	if err != nil {
		t.Fatalf("renderRatified: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ratified.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := loadRatified(path)
	if err != nil {
		t.Fatalf("loadRatified: %v", err)
	}

	for name, want := range cases {
		got, ok := out[want.Type]
		if !ok {
			t.Errorf("%s: %s did not survive the round trip at all", name, want.Type)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: round trip changed the row\n got: %#v\nwant: %#v", name, got, want)
		}
		// DeepEqual treats a nil and an empty slice as different, which is
		// what makes it the right comparison here; assert the distinction
		// explicitly too so a future DeepEqual replacement cannot lose it.
		if (got.IdentityAttrs == nil) != (want.IdentityAttrs == nil) {
			t.Errorf("%s: IdentityAttrs nil-ness changed: got nil=%v, want nil=%v", name, got.IdentityAttrs == nil, want.IdentityAttrs == nil)
		}
		if (got.Components == nil) != (want.Components == nil) {
			t.Errorf("%s: Components nil-ness changed: got nil=%v, want nil=%v", name, got.Components == nil, want.Components == nil)
		}
	}
}

// assertEveryExportedFieldNonZero keeps the fixture above honest. Without it
// the fixture goes stale the moment a field is added: the round trip would
// still pass over a field nobody set.
func assertEveryExportedFieldNonZero(t *testing.T, v reflect.Value, what string) {
	t.Helper()
	ty := v.Type()
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if f.PkgPath != "" {
			continue
		}
		if v.Field(i).IsZero() {
			t.Errorf("the round-trip fixture leaves %s.%s at its zero value, so this test proves nothing about that field.\n"+
				"Set it to something distinctive - that is the whole job of the fixture.", what, f.Name)
		}
	}
}

// TestRatifiedRoundTripsEveryCommittedRow is the migration's own evidence:
// tools/row-gen/ratified.json holds exactly the non-RecordBacked half of
// [identity.DefaultTable], row for row and field for field.
//
// The RecordBacked rows are deliberately absent - [recordBackedTypes] derives
// them from live/logical-schemas.json and nothing about one is ratified - so
// the expected key set is computed by that same rule rather than asserted as a
// number.
func TestRatifiedRoundTripsEveryCommittedRow(t *testing.T) {
	stored := loadRatifiedForTest(t)
	want := ratifiedRowsOf(identity.DefaultTable)

	if len(stored) != len(want) {
		t.Errorf("%s holds %d rows, the non-RecordBacked half of DefaultTable has %d", ratifiedJSONRel, len(stored), len(want))
	}
	for _, tf := range sortedRatifiedKeys(want) {
		got, ok := stored[tf]
		if !ok {
			t.Errorf("%s: in DefaultTable and absent from %s", tf, ratifiedJSONRel)
			continue
		}
		if !reflect.DeepEqual(got, want[tf]) {
			t.Errorf("%s: the stored row differs from the committed table's\n stored: %#v\ntable:  %#v", tf, got, want[tf])
		}
	}
	for _, tf := range sortedRatifiedKeys(stored) {
		if _, ok := want[tf]; !ok {
			t.Errorf("%s: in %s and not in DefaultTable's non-RecordBacked half", tf, ratifiedJSONRel)
		}
	}
}

// TestEmitDoesNotReadTheTableItWrites is issue #263's cure stated as a
// property, and it is the one assertion in this package that the
// pre-#263 generator could not have passed.
//
// It empties [identity.DefaultTable] outright - the exact accident that
// motivated the issue, where a mutation retracted 217 rows, survived
// reverting the mutation, and re-emitted the smaller table twice more
// while exiting 0 - and requires -emit to produce all four files
// byte-identically anyway. Measured against the generator as it stood at
// ceb0795d66, emptying the table and re-emitting yielded a 14-row table
// (recordBackedTypes' derived set) that every fixed-point and convergence
// check in this repository accepted.
//
// Emptying rather than deleting a few rows is deliberate. A handful of
// deletions leaves the read paths that still consult DefaultTable mostly
// working, so a partial revert can pass; nothing survives an empty map by
// accident.
//
// The gate is deliberately excluded here, because it is the one thing that
// SHOULD still read the committed table: runEmit compares
// identity.AdmittedTypes() against what it is about to write, and over an
// emptied table that comparison correctly finds nothing retracted. This test
// drives buildEmitFiles, which is everything downstream of that comparison.
func TestEmitDoesNotReadTheTableItWrites(t *testing.T) {
	ratified := loadRatifiedForTest(t)
	proposals := loadAllForTest(t)
	annotations := loadAnnotationsForTest(t)
	grammar := loadImportGrammarForTest(t)
	survey := loadSurveyForTest(t)
	logical := loadLogicalSchemasForTest(t)

	before, _, _, err := buildEmitFiles(ratified, proposals, annotations, grammar, survey, logical)
	if err != nil {
		t.Fatalf("buildEmitFiles over the committed tree: %v", err)
	}

	saved := identity.DefaultTable
	identity.DefaultTable = map[string]identity.TypeIdentity{}
	defer func() { identity.DefaultTable = saved }()

	after, part, _, err := buildEmitFiles(ratified, proposals, annotations, grammar, survey, logical)
	if err != nil {
		t.Fatalf("buildEmitFiles over an EMPTIED identity.DefaultTable: %v\n"+
			"-emit still depends on the table it writes; the corpus is supposed to be %s", err, ratifiedJSONRel)
	}

	if got := len(part.Generated) + len(part.Override); got != len(saved) {
		t.Errorf("emitting from an emptied table produced %d rows, want %d - the missing rows are the ones "+
			"still being read out of %s instead of %s", got, len(saved), identityTableRel, ratifiedJSONRel)
	}
	for _, rel := range emitFileOrder {
		if !bytes.Equal(before[rel], after[rel]) {
			t.Errorf("%s differs when identity.DefaultTable is emptied (%d bytes with the table, %d without): "+
				"some read still reaches the generator's own previous output, which is exactly what issue #263 is",
				rel, len(before[rel]), len(after[rel]))
		}
	}
}

// TestRatifiedRendersTheCommittedIdentityTable is the point of the exercise,
// stated as a byte comparison.
//
// It runs -emit's whole row assembly with the STORED corpus in place of
// [identity.DefaultTable] and requires the rendered
// internal/live/identity/table_generated.go to equal the committed file
// exactly. A field that did not survive the round trip changes a rendered
// literal, so this fails on anything TestRatifiedRoundTripsEveryCommittedRow
// could conceivably miss, and it fails with a diffable artifact rather than a
// struct dump.
//
// It is not the same assertion as TestEmitFilesMatchCommitted, which renders
// its expected value from DefaultTable and so moves both sides of its
// comparison together (that test's own doc comment says so). Here the input is
// a file on disk that no generator writes.
func TestRatifiedRendersTheCommittedIdentityTable(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	ratified := loadRatifiedForTest(t)
	recordBacked, err := recordBackedRows(ratified, loadLogicalSchemasForTest(t))
	if err != nil {
		t.Fatalf("recordBackedRows: %v", err)
	}
	grammar := loadImportGrammarForTest(t)
	survey := loadSurveyForTest(t)
	vetoed := setOf(markerlessRoster(ratified, survey, loadAllForTest(t), grammar))

	rows, types := emittedRows(ratified, recordBacked, grammar, survey, vetoed)
	src, err := renderIdentityFile(types, rows)
	if err != nil {
		t.Fatalf("renderIdentityFile: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(root, identityTableRel))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src, committed) {
		t.Errorf("rendering %s from %s does not reproduce the committed file byte for byte (%d bytes rendered, %d committed).\n"+
			"That is the migration being lossy: some field of some row does not survive the JSON round trip. Do not re-render the JSON to make this quiet - find the field.",
			identityTableRel, ratifiedJSONRel, len(src), len(committed))
	}
}

// TestRatifiedJSONIsCanonical holds the committed file at renderRatified's own
// spelling, so a hand edit lands in the form the loader round-trips rather
// than in a second one that happens to parse.
func TestRatifiedJSONIsCanonical(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderRatified(loadRatifiedForTest(t))
	if err != nil {
		t.Fatalf("renderRatified: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(root, ratifiedJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rendered, committed) {
		t.Errorf("%s is not in canonical form: re-encoding what it loads produces %d bytes against %d committed.\n"+
			"Key order, indentation or a `null` where a key should be absent - reformat it rather than teaching the loader a second spelling.",
			ratifiedJSONRel, len(rendered), len(committed))
	}
}

// TestRatifiedLoaderRefusesTheTwoWaysAFileCanLie is the loader's non-vacuity
// control. Both refusals are about a wrong marker rather than a missing one: a
// row filed under the wrong key would render another type's identity under
// this type's name, and a RecordBacked row here would put a derived row in a
// ratification ledger where a later edit could contradict its derivation.
func TestRatifiedLoaderRefusesTheTwoWaysAFileCanLie(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			name: "key and type disagree",
			body: `{"aws_a": {"type": "aws_b"}}`,
			want: "must name the same type",
		},
		{
			name: "record-backed row",
			body: `{"aws_a": {"type": "aws_a", "record_backed": true}}`,
			want: "never ratified",
		},
		{
			name: "unknown field",
			body: `{"aws_a": {"type": "aws_a", "compnents": []}}`,
			want: "unknown field",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ratified.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadRatified(path)
			if err == nil {
				t.Fatalf("loadRatified accepted %s", tc.body)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal does not mention %q: %v", tc.want, err)
			}
		})
	}
}
