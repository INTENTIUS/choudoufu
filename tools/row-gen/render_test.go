// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// loadAllForTest reads the four committed artifacts the same way run() does,
// so tests exercise the production loaders rather than reimplementing them.
func loadAllForTest(t *testing.T) []proposal {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	registry, err := loadRegistry(filepath.Join(root, registryJSONRel))
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	mapping, err := loadMapping(filepath.Join(root, mappingJSONRel))
	if err != nil {
		t.Fatalf("loadMapping: %v", err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("loadSurvey: %v", err)
	}
	carveSeed, err := loadCarveSeed(filepath.Join(root, carveSeedJSONRel))
	if err != nil {
		t.Fatalf("loadCarveSeed: %v", err)
	}
	importGrammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		t.Fatalf("loadImportGrammar: %v", err)
	}
	proposals, err := classifyAll(mapping, registry, survey, carveSeed, importGrammar)
	if err != nil {
		t.Fatalf("classifyAll: %v", err)
	}
	return proposals
}

// TestLambdaGoldenFile is the committed sample the issue asks for: running
// the tool restricted to one service batch (Lambda) must reproduce
// tools/row-gen/testdata/lambda.golden.txt exactly, so a reviewer reading
// that file sees the tool's real, current output shape. Regenerate it with:
//
//	go run ./tools/row-gen -service Lambda > tools/row-gen/testdata/lambda.golden.txt
func TestLambdaGoldenFile(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	proposals := loadAllForTest(t)
	got := renderReport(proposals, "Lambda")

	want, err := os.ReadFile(filepath.Join(root, "tools/row-gen/testdata/lambda.golden.txt"))
	if err != nil {
		t.Fatalf("reading the golden file: %v", err)
	}
	if got != string(want) {
		t.Errorf("Lambda batch drifted from tools/row-gen/testdata/lambda.golden.txt.\nRegenerate with:\n  go run ./tools/row-gen -service Lambda > tools/row-gen/testdata/lambda.golden.txt\n\n--- got ---\n%s", got)
	}
}

// TestPastableSnippetsParseAsGo is the round-trip test the issue asks for on
// the "every pastable row" side: every admittedTypesV0 line and every
// DefaultTable entry this tool renders, over the whole mapped set, has to be
// syntactically valid Go once pasted into the wrapping literal a reviewer
// would paste it into. A snippet that does not parse is not "ready to
// paste" no matter how the evidence reads.
func TestPastableSnippetsParseAsGo(t *testing.T) {
	proposals := loadAllForTest(t)

	var admissionLines, tableEntries string
	n := 0
	for _, p := range proposals {
		switch p.Bucket {
		case bucketServerAssigned:
			admissionLines += renderAdmissionLine(p.TFType)
			tableEntries += renderServerAssignedEntry(p)
			n++
		case bucketClientNamed:
			admissionLines += renderAdmissionLine(p.TFType)
			tableEntries += renderClientNamedEntry(p)
			n++
		}
	}
	if n == 0 {
		t.Fatal("no proposed rows found in the mapped set; nothing to round-trip")
	}

	admissionSrc := fmt.Sprintf("package p\n\nvar _ = map[string]struct{}{\n%s\n}\n", admissionLines)
	if _, err := parser.ParseFile(token.NewFileSet(), "admission.go", admissionSrc, parser.AllErrors); err != nil {
		t.Errorf("rendered admittedTypesV0 lines do not parse as Go: %v\n\n%s", err, admissionSrc)
	}

	tableSrc := fmt.Sprintf("package p\n\nvar _ = []any{\n%s\n}\n", tableEntries)
	if _, err := parser.ParseFile(token.NewFileSet(), "table.go", tableSrc, parser.AllErrors); err != nil {
		t.Errorf("rendered DefaultTable entries do not parse as Go: %v\n\n%s", err, tableSrc)
	}
}
