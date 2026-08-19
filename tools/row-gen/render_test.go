// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
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

// TestPastableSnippetsLoadAsRatifiedRows is the round-trip test on the
// "every pastable row" side, restated for the paste target that actually
// exists. It replaces a test that only asked whether the rendered snippet
// parsed as Go, which stopped meaning anything once issue #263 moved the
// corpus to tools/row-gen/ratified.json: a block can be perfectly good Go
// and still be unpastable, because the file it goes into is JSON.
//
// The assertion is deliberately stronger than the one it replaces. Every
// pastable proposal over the whole mapped set is rendered, the blocks are
// assembled into one object exactly as a ratifier pasting them all in would
// leave the file, and the result goes through loadRatified itself - the
// production loader, with DisallowUnknownFields and the key/Type agreement
// check - rather than through a parser standing in for it. Then each loaded
// row is required to equal the row renderRatifiedEntry claimed to be
// writing, so a field that does not survive the JSON round trip fails here
// instead of silently landing a weaker row than the block displayed.
//
// What it deliberately does NOT prove, because both sides of its comparison
// come from proposedRatifiedRow: that the row is the RIGHT row. Mutating
// that function to emit a wrong value passes here and is caught elsewhere -
// by TestLambdaGoldenFile against a committed golden, and by
// clientnamedcloud_test.go against live/import-grammar.json. This test's
// external referent is loadRatified, so its subject is the serialization,
// not the content. Both mutations were run.
func TestPastableSnippetsLoadAsRatifiedRows(t *testing.T) {
	proposals := loadAllForTest(t)

	want := map[string]identity.TypeIdentity{}
	var members []string
	for _, p := range proposals {
		if !pastableBucket(p.Bucket) {
			continue
		}
		entry, err := renderRatifiedEntry(p)
		if err != nil {
			t.Fatalf("renderRatifiedEntry(%s): %v", p.TFType, err)
		}
		// renderRatifiedEntry ends every block with the comma a paste
		// beside an existing member needs; the last member of the object
		// assembled here must not have one.
		members = append(members, strings.TrimSuffix(strings.TrimRight(entry, "\n"), ","))
		want[p.TFType] = proposedRatifiedRow(p)
	}
	if len(members) == 0 {
		t.Fatal("no pastable rows found in the mapped set; nothing to round-trip")
	}

	path := filepath.Join(t.TempDir(), "ratified.json")
	if err := os.WriteFile(path, []byte("{\n"+strings.Join(members, ",\n")+"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadRatified(path)
	if err != nil {
		t.Fatalf("the %d rendered blocks do not load as %s: %v", len(members), ratifiedJSONRel, err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d rows from %d rendered blocks", len(got), len(members))
	}
	for tf, w := range want {
		if !reflect.DeepEqual(got[tf], w) {
			t.Errorf("%s does not survive the paste round trip:\n rendered from: %#v\n loaded back as: %#v", tf, w, got[tf])
		}
	}
}
