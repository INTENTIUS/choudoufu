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
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/opentofu/opentofu/internal/stateless/identity"
)

// limitationsMDRel is the doc whose mechanical claims this file holds to
// the committed survey artifact and the admission table. Prose reasoning
// stays hand-written; the numbers, rosters, and example types below are
// derivable, so drift in them is a test failure, not an editing chore.
const limitationsMDRel = "stateless/LIMITATIONS.md"

// TestLimitationsDocAgainstSurvey is the LIMITATIONS.md sibling of
// TestSurveyJSONAgainstHandTable: ungated, no provider, two committed files
// and the admission table. It pins the doc's unadmitted-type entry — the
// "N of M top types admitted" headline, the excluded-by-rule roster, and
// the example type's continued unadmittedness — and derives the untaggable
// entry's type list from survey signals intersected with the admission
// table, the derivation stamp's hand-pinned test asserts piecewise.
func TestLimitationsDocAgainstSurvey(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	docBytes, err := os.ReadFile(filepath.Join(root, limitationsMDRel))
	if err != nil {
		t.Fatalf("reading %s: %v", limitationsMDRel, err)
	}
	doc := string(docBytes)

	data, err := os.ReadFile(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading %s (regenerate with `go run ./tools/survey-gen`): %v", surveyJSONRel, err)
	}
	var survey Survey
	if err := json.Unmarshal(data, &survey); err != nil {
		t.Fatalf("decoding %s: %v", surveyJSONRel, err)
	}

	admitted := map[string]bool{}
	for _, typeName := range identity.AdmittedTypes() {
		admitted[typeName] = true
	}
	surveyed := map[string]bool{}
	for _, row := range survey.Types {
		surveyed[row.Type] = true
	}

	// The headline: "65 of 68 top types admitted" is the roster minus the
	// excluded-by-rule set, both of which the generator owns.
	headline := fmt.Sprintf("%d of %d top types admitted", survey.Counts.Types-len(opsExcluded), survey.Counts.Types)
	if !strings.Contains(doc, headline) {
		t.Errorf("%s does not contain the headline %q derived from %s and opsExcluded", limitationsMDRel, headline, surveyJSONRel)
	}

	// The unadmitted-type entry.
	_, entry, found := strings.Cut(doc, "### unadmitted-type")
	if !found {
		t.Fatalf("%s has no `### unadmitted-type` entry", limitationsMDRel)
	}
	if end := strings.Index(entry, "\n### "); end >= 0 {
		entry = entry[:end]
	}

	// Every excluded-by-rule type is named in the entry.
	for typeName := range opsExcluded {
		if !strings.Contains(entry, "`"+typeName+"`") {
			t.Errorf("%s's unadmitted-type entry does not name excluded-by-rule type %s", limitationsMDRel, typeName)
		}
	}

	// The entry's example construct must stay surveyed-but-unadmitted.
	// Issue #20 admits more marker-path types over time; when the example
	// type is wired in, the entry and the fixture at
	// stateless/e2e/limits/unadmitted-type/ must move to another type
	// together.
	construct := regexp.MustCompile("\\*\\*Construct\\.\\*\\* `(aws_[a-z0-9_]+)`").FindStringSubmatch(entry)
	if construct == nil {
		t.Fatalf("%s's unadmitted-type entry has no **Construct.** `aws_...` line", limitationsMDRel)
	}
	example := construct[1]
	if !surveyed[example] {
		t.Errorf("unadmitted-type example %s is not in %s's roster", example, surveyJSONRel)
	}
	if admitted[example] {
		t.Errorf("unadmitted-type example %s is now in the admission table; swap the entry and the fixture at stateless/e2e/limits/unadmitted-type/ to a still-unadmitted type", example)
	}

	// The untaggable entry's roster is a derivation: admitted types whose
	// survey row says no top-level tags argument.
	var derived []string
	for _, row := range survey.Types {
		if admitted[row.Type] && !row.Signals.Taggable {
			derived = append(derived, row.Type)
		}
	}
	sort.Strings(derived)

	const untaggableHeading = "**Untaggable types cannot be removed by the sweep.**"
	_, untaggable, found := strings.Cut(doc, untaggableHeading)
	if !found {
		t.Fatalf("%s has no %q entry", limitationsMDRel, untaggableHeading)
	}
	if end := strings.Index(untaggable, "\n\n"); end >= 0 {
		untaggable = untaggable[:end]
	}
	listed := regexp.MustCompile("`(aws_[a-z0-9_]+)`").FindAllStringSubmatch(untaggable, -1)
	docSet := map[string]bool{}
	for _, m := range listed {
		docSet[m[1]] = true
	}
	for _, typeName := range derived {
		if !docSet[typeName] {
			t.Errorf("%s is untaggable and admitted per %s but missing from the untaggable entry", typeName, surveyJSONRel)
		}
		delete(docSet, typeName)
	}
	for typeName := range docSet {
		t.Errorf("the untaggable entry names %s, which is not untaggable-and-admitted per %s", typeName, surveyJSONRel)
	}
}
