// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// loadAnnotationsForTest reads tools/row-gen/annotations.json the same way
// runConvergence and runEmit do.
func loadAnnotationsForTest(t *testing.T) map[string]annotation {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		t.Fatalf("loadAnnotations: %v", err)
	}
	return annotations
}

// loadSurveyForTest reads live/survey-full.json the same way runEmit does,
// for mergeIdentityAttrs' own evidence.
func loadSurveyForTest(t *testing.T) map[string]surveyEntry {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("loadSurvey: %v", err)
	}
	return survey
}

// loadLogicalSchemasForTest reads live/logical-schemas.json the same way
// runEmit does, for recordBackedRows' own evidence.
func loadLogicalSchemasForTest(t *testing.T) logicalSchemas {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	logical, err := loadLogicalSchemas(filepath.Join(root, logicalSchemasJSONRel))
	if err != nil {
		t.Fatalf("loadLogicalSchemas: %v", err)
	}
	return logical
}

// loadImportGrammarForTest reads live/import-grammar.json the same way
// runEmit does, for mergeServerAssigned's own evidence.
func loadImportGrammarForTest(t *testing.T) map[string]importGrammarRow {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		t.Fatalf("loadImportGrammar: %v", err)
	}
	return grammar
}

// TestEmitFilesMatchCommitted regenerates the two files -emit owns and
// requires them to match what is committed byte-for-byte. Regenerate with:
//
//	go run ./tools/row-gen -emit
//
// # What this catches, and what it does not (issue #138)
//
// It catches a rendering change: emit.go's own output shape, a field added
// to identity.TypeIdentity that renderStruct now emits, an edit to a
// generated file that does not survive the round trip (reordering,
// formatting, a comment).
//
// It also, since issue #263's cure, catches a semantic hand-edit of the
// generated tables - and for most of this test's life it did NOT, which is
// worth stating because two earlier versions of this comment got the claim
// wrong in both directions. buildEmitFiles used to render its expected value
// FROM identity.DefaultTable, compiled from table_generated.go, which is the
// file under test: a hand edit moved both sides of the comparison
// identically, and changing one row's Components to name an argument no
// provider schema has left this test green. buildEmitFiles now renders from
// tools/row-gen/ratified.json, which is external to every file it compares,
// so that edit fails here.
//
// The other tests that catch it, all of which failed on that same edit even
// then:
//
//   - TestNoRatifiedRowNamesAnUnknownArgument (sources_test.go) - checks
//     every ratified row's arguments against the provider schema and the
//     scraped docs, which are external to the table.
//   - TestConvergenceArtifactMatchesCommitted (convergence_test.go)
//   - TestSourcesArtifactMatchesCommitted (sources_test.go)
//
// The general question worth asking of any drift test here: what external
// source does it consult? One whose expected value derives from the thing it
// checks is measuring agreement with itself. The len(identity.DefaultTable)
// comparison below is now such a source rather than a tautology, for the
// same reason: it holds ratified.json's emitted key set against the
// committed table's.
func TestEmitFilesMatchCommitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals := loadAllForTest(t)
	annotations := loadAnnotationsForTest(t)
	grammar := loadImportGrammarForTest(t)

	files, identityPart, lintPart, err := buildEmitFiles(loadRatifiedForTest(t), proposals, annotations, grammar, loadSurveyForTest(t), loadLogicalSchemasForTest(t))
	if err != nil {
		t.Fatalf("buildEmitFiles: %v", err)
	}

	if got, want := len(identityPart.Generated)+len(identityPart.Override), len(identity.DefaultTable); got != want {
		t.Errorf("identity partition covers %d types, DefaultTable has %d", got, want)
	}
	if got, want := len(lintPart.Generated)+len(lintPart.Override), len(identityPart.Generated)+len(identityPart.Override)-recordBackedCount(); got != want {
		t.Errorf("lint partition covers %d types, want %d (identity partition minus RecordBacked types)", got, want)
	}

	for _, rel := range emitFileOrder {
		committed, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading committed %s: %v", rel, err)
		}
		if !bytes.Equal(files[rel], committed) {
			t.Errorf("%s has drifted from a fresh `go run ./tools/row-gen -emit` - regenerate and commit it", rel)
		}
	}
}

// recordBackedCount is the number of [identity.DefaultTable] entries with
// RecordBacked set - admittedTypesV0's own partition total is always the
// identity partition total minus this many, see splitNonRecordBacked.
func recordBackedCount() int {
	n := 0
	for _, ti := range identity.DefaultTable {
		if ti.RecordBacked {
			n++
		}
	}
	return n
}

// TestEmitPartitionsDisjointAndComplete pins the two invariants
// buildEmitFiles' output depends on: every admitted type lands in exactly
// one of the identity table's two partitions, and admittedTypesV0's own
// partition is exactly the identity partition's RecordBacked types dropped
// - never a type added or lost.
func TestEmitPartitionsDisjointAndComplete(t *testing.T) {
	proposals := loadAllForTest(t)
	annotations := loadAnnotationsForTest(t)

	grammar := loadImportGrammarForTest(t)
	_, identityPart, lintPart, err := buildEmitFiles(loadRatifiedForTest(t), proposals, annotations, grammar, loadSurveyForTest(t), loadLogicalSchemasForTest(t))
	if err != nil {
		t.Fatalf("buildEmitFiles: %v", err)
	}

	seen := make(map[string]string, len(identityPart.Generated)+len(identityPart.Override))
	for _, tf := range identityPart.Generated {
		seen[tf] = "generated"
	}
	for _, tf := range identityPart.Override {
		if which, dup := seen[tf]; dup {
			t.Errorf("%s: in both the generated and override identity partitions (also %s)", tf, which)
		}
		seen[tf] = "override"
	}
	for tf := range identity.DefaultTable {
		if _, ok := seen[tf]; !ok {
			t.Errorf("%s: in DefaultTable but in neither identity partition", tf)
		}
	}

	lintSeen := make(map[string]bool, len(lintPart.Generated)+len(lintPart.Override))
	for _, tf := range lintPart.Generated {
		lintSeen[tf] = true
	}
	for _, tf := range lintPart.Override {
		if lintSeen[tf] {
			t.Errorf("%s: in both the generated and override lint partitions", tf)
		}
		lintSeen[tf] = true
	}
	for tf, which := range seen {
		wantInLint := !identity.DefaultTable[tf].RecordBacked
		_, gotInLint := lintSeen[tf]
		if gotInLint != wantInLint {
			t.Errorf("%s: identity partition=%s, RecordBacked=%v, but lint partition membership=%v (want %v)",
				tf, which, identity.DefaultTable[tf].RecordBacked, gotInLint, wantInLint)
		}
		delete(lintSeen, tf)
	}
	for tf := range lintSeen {
		t.Errorf("%s: in a lint partition but not in either identity partition at all", tf)
	}
}

// TestEmitGateRefusesUnruledMismatch pins issue #132's gate from the
// failure side: an admitted type that the fresh classifier does not
// reproduce and that carries no ruling in tools/row-gen/annotations.json
// must make buildEmitFiles refuse outright, naming the type - never
// silently render a table row that is indistinguishable from a row nobody
// has looked at. The test takes the real proposals and the real ledger,
// deletes one unreproduced type's ruling, and requires the error to name
// exactly that type. (Written before the gate existed and verified to fail
// against that state: buildEmitFiles then returned files and a nil error.)
func TestEmitGateRefusesUnruledMismatch(t *testing.T) {
	proposals := loadAllForTest(t)
	annotations := loadAnnotationsForTest(t)

	// Find an unreproduced admitted type from the same comparison the gate
	// itself runs. Every one of them must be ruled for -emit to work at
	// all, so the first is as good as any.
	emittedTable := loadEmittedTableForTest(t, proposals)
	art := buildConvergence(emittedTable, proposals, annotations)
	matched := make(map[string]bool, len(art.Types))
	for _, row := range art.Types {
		matched[row.TFType] = row.Matched
	}
	// A RecordBacked row is exempt from the gate by derivation, so it is
	// never a candidate victim - deleting its (now non-existent) ruling
	// would prove nothing about the gate.
	recordBacked, err := recordBackedRows(loadRatifiedForTest(t), loadLogicalSchemasForTest(t))
	if err != nil {
		t.Fatalf("recordBackedRows: %v", err)
	}
	victim := ""
	for _, tf := range sortedRatifiedKeys(emittedTable) {
		if !matched[tf] && !recordBacked[tf] {
			victim = tf
			break
		}
	}
	if victim == "" {
		t.Skip("every admitted type is reproduced by the classifier; the gate has nothing left to guard")
	}

	broken := make(map[string]annotation, len(annotations))
	for tf, a := range annotations {
		broken[tf] = a
	}
	delete(broken, victim)

	grammar := loadImportGrammarForTest(t)
	files, _, _, err := buildEmitFiles(loadRatifiedForTest(t), proposals, broken, grammar, loadSurveyForTest(t), loadLogicalSchemasForTest(t))
	if err == nil {
		t.Fatalf("buildEmitFiles accepted an unreproduced, unruled type (%s): the gate is not firing", victim)
	}
	if files != nil {
		t.Errorf("buildEmitFiles returned files alongside the gate error; a refused emit must not hand back content to write")
	}
	if !strings.Contains(err.Error(), victim) {
		t.Errorf("gate error does not name the unruled type %s: %v", victim, err)
	}
}

// TestEmitRendersValidGo guards the renderer itself, independent of what is
// currently committed: every one of the two files buildEmitFiles produces
// must parse as a single, syntactically valid Go source file.
func TestEmitRendersValidGo(t *testing.T) {
	proposals := loadAllForTest(t)
	annotations := loadAnnotationsForTest(t)

	grammar := loadImportGrammarForTest(t)
	files, _, _, err := buildEmitFiles(loadRatifiedForTest(t), proposals, annotations, grammar, loadSurveyForTest(t), loadLogicalSchemasForTest(t))
	if err != nil {
		t.Fatalf("buildEmitFiles: %v", err)
	}
	for _, rel := range emitFileOrder {
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, rel, files[rel], parser.DeclarationErrors); err != nil {
			t.Errorf("%s: does not parse as Go source: %v", rel, err)
		}
	}
}
