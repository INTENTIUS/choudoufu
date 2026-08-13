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
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// limitationsMDRel (residue_render.go) is the doc whose mechanical claims
// this file holds to the committed survey artifact and the admission
// table. Prose reasoning stays hand-written; the numbers, rosters, and
// example types below are derivable, so drift in them is a test failure,
// not an editing chore. The untaggable entry's own derivation moved to a
// rendered span (issue #54, untaggable_render.go); TestLimitationsMDResidueRosterSpans
// in residue_test.go holds it, not this file.

// TestLimitationsDocAgainstSurvey is the LIMITATIONS.md sibling of
// TestSurveyJSONAgainstHandTable: ungated, no provider, two committed files
// and the admission table. It pins the doc's unadmitted-type entry — the
// "N of M top types admitted" headline, the excluded-by-rule roster, and
// the example type's continued unadmittedness.
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
	// live/e2e/limits/unadmitted-type/ must move to another type
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
		t.Errorf("unadmitted-type example %s is now in the admission table; swap the entry and the fixture at live/e2e/limits/unadmitted-type/ to a still-unadmitted type", example)
	}
}

// TestReadmeUpstreamVersion holds the README's upstream-version story to
// the shape the "stop hardcoding the upstream OpenTofu version" change
// chose: the README names no literal version (pkg.go.dev renders the
// README frozen at each tag, so a literal drifts) and instead points at
// version/VERSION, which resolves within the same tagged tree. The test
// pins both halves: the pointer is present, and no stale bold literal
// has crept back in.
func TestReadmeUpstreamVersion(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md")) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	if !strings.Contains(string(readme), "version/VERSION") {
		t.Errorf("README.md no longer points at version/VERSION for the upstream OpenTofu version")
	}
	if hardcoded := regexp.MustCompile(`\*\*OpenTofu \d`); hardcoded.Match(readme) {
		t.Errorf("README.md hardcodes an upstream OpenTofu version again; point at version/VERSION instead")
	}
}

// contractMDXRel and its two rendered spans (contract_render.go) replaced
// this file's former TestContractEnumerationMatchesAdmissionTable: the
// Contract section's count and enumeration are generated now, so
// TestContractMDXRenderedSpans in contract_render_test.go holds them to
// identity.AdmittedTypes by the same render/drift pattern this file's
// TestLimitationsDocAgainstSurvey uses for LIMITATIONS.md's hand-written
// claims (issue #54).
