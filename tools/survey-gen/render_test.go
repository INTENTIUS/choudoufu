// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSurveyMDRenderedSpans holds SURVEY.md's four rendered spans - the
// raw-signals sentence, the Summary path-count table, the Provider-wide
// paragraph (issue #679), and the Status table's wired-count cell (issue
// #54) - byte-for-byte to what the renderer produces from the committed
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

	// wired-count is inline (a single Markdown table cell), so it is
	// checked with the inline-marker reader rather than spanContent.
	if got, err := spanContentInline(surveyMDRel, md, spanWiredCount); err != nil {
		t.Errorf("%v", err)
	} else if want := renderWiredCount(); got != want {
		t.Errorf("%s's %q span is stale; run `go run ./tools/survey-gen -render` and commit the result.\n--- committed ---\n%s--- rendered ---\n%s",
			surveyMDRel, spanWiredCount, got, want)
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
