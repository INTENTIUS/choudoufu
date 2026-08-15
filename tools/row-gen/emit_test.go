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
// It does NOT catch a semantic hand-edit of the generated tables, and an
// earlier version of this comment claimed it did. buildEmitFiles renders its
// expected value FROM identity.DefaultTable, which is compiled from
// table_generated.go, which is the file under test - so a hand edit moves
// both sides of the comparison identically. Measured: changing one row's
// Components to name an argument no provider schema has left this test
// green.
//
// The tests that do catch it, all of which failed on that same edit:
//
//   - TestNoRatifiedRowNamesAnUnknownArgument (sources_test.go) - checks
//     every ratified row's arguments against the provider schema and the
//     scraped docs, which are external to the table.
//   - TestConvergenceArtifactMatchesCommitted (convergence_test.go)
//   - TestSourcesArtifactMatchesCommitted (sources_test.go)
//
// The general question worth asking of any drift test here: what external
// source does it consult? One whose expected value derives from the thing it
// checks is measuring agreement with itself.
func TestEmitFilesMatchCommitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals := loadAllForTest(t)
	annotations := loadAnnotationsForTest(t)

	files, identityPart, lintPart, err := buildEmitFiles(proposals, annotations)
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

	_, identityPart, lintPart, err := buildEmitFiles(proposals, annotations)
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

// TestEmitRendersValidGo guards the renderer itself, independent of what is
// currently committed: every one of the two files buildEmitFiles produces
// must parse as a single, syntactically valid Go source file.
func TestEmitRendersValidGo(t *testing.T) {
	proposals := loadAllForTest(t)
	annotations := loadAnnotationsForTest(t)

	files, _, _, err := buildEmitFiles(proposals, annotations)
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
