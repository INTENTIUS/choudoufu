// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSurveyMDRenderedSpans holds SURVEY.md's four rendered spans - the
// raw-signals sentence, the Summary path-count table, the Provider-wide
// paragraph (issue #679), and the Status vocabulary table (issues #54 and
// #1249) - byte-for-byte to what the renderer produces from the committed
// live/survey.json, live/survey-full.json, the doc's own per-type table,
// and the compiled admission table. No provider, so it is not gated; drift
// between the artifacts and the doc fails here with the command that fixes
// it.
func TestSurveyMDRenderedSpans(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading %s (regenerate with `go run ./tools/survey-gen`): %v", surveyJSONRel, err)
	}
	var survey Survey
	if err := json.Unmarshal(data, &survey); err != nil {
		t.Fatalf("decoding %s: %v", surveyJSONRel, err)
	}

	fullData, err := os.ReadFile(filepath.Join(root, surveyFullJSONRel))
	if err != nil {
		t.Fatalf("reading %s (regenerate with `go run ./tools/survey-gen -all`): %v", surveyFullJSONRel, err)
	}
	var full Survey
	if err := json.Unmarshal(fullData, &full); err != nil {
		t.Fatalf("decoding %s: %v", surveyFullJSONRel, err)
	}

	mdPath := filepath.Join(root, surveyMDRel)
	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("reading %s: %v", surveyMDRel, err)
	}
	md := string(mdBytes)
	rows, err := readRoster(mdPath)
	if err != nil {
		t.Fatalf("parsing %s's per-type table: %v", surveyMDRel, err)
	}

	for _, span := range []struct {
		name, want string
	}{
		{spanRawSignals, renderRawSignals(survey.Counts)},
		{spanSummary, renderSummary(rows)},
		{spanProviderWide, renderProviderWide(full)},
		{spanStatusVocabulary, renderStatusVocabulary(rows)},
	} {
		got, err := spanContent(surveyMDRel, md, span.name)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if got != span.want {
			t.Errorf("%s's %q span is stale; run `go run ./tools/survey-gen -render` and commit the result.\n--- committed ---\n%s--- rendered ---\n%s",
				surveyMDRel, span.name, got, span.want)
		}
	}

	// The whole-file check catches what the per-span one cannot: a marker
	// pair going missing or duplicated.
	if out, err := renderSpans(md, survey, full, rows); err != nil {
		t.Errorf("rendering %s's spans: %v", surveyMDRel, err)
	} else if out != md {
		t.Errorf("%s differs from its rendered form; run `go run ./tools/survey-gen -render` and commit the result", surveyMDRel)
	}

	// Every summary override must still describe a row whose table path
	// differs from its counted path, or it is stale and hides a divergence
	// that no longer exists.
	byType := map[string]HandRow{}
	for _, r := range rows {
		byType[r.Type] = r
	}
	for typeName, o := range summaryOverrides {
		r, ok := byType[typeName]
		switch {
		case !ok:
			t.Errorf("summaryOverrides names %s, which is not in %s's table", typeName, surveyMDRel)
		case r.Path == o.counted:
			t.Errorf("stale summary override for %s: the table already says %q; remove it", typeName, o.counted)
		}
	}
}

// TestReadRosterRefusesAStatusOutsideTheVocabulary is the red arm of the
// Status closure (#1249): a per-type row carrying a token with no
// statusVocabulary entry must stop the render, not be tallied into nothing.
func TestReadRosterRefusesAStatusOutsideTheVocabulary(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(root, surveyMDRel))
	if err != nil {
		t.Fatal(err)
	}
	const real = "| aws_sns_topic_subscription | parent-derived | markerless |"
	if strings.Count(string(md), real) != 1 {
		t.Fatalf("expected exactly one %q to plant into", real)
	}
	planted := filepath.Join(t.TempDir(), "SURVEY.md")
	mutated := strings.Replace(string(md), real, "| aws_sns_topic_subscription | parent-derived | deferred |", 1)
	if err := os.WriteFile(planted, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRoster(planted); err == nil {
		t.Fatal("readRoster accepted a Status outside the vocabulary")
	} else {
		t.Logf("refused as required: %v", err)
	}
	if _, err := readRoster(filepath.Join(root, surveyMDRel)); err != nil {
		t.Fatalf("the committed table fails its own vocabulary: %v", err)
	}
}
