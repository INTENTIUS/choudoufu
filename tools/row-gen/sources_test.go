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

// TestSourcesArtifactMatchesCommitted is the drift guard: the committed
// report has to be what a fresh run produces, so a change to any of the
// three sources shows up as a failure rather than as a stale file.
func TestSourcesArtifactMatchesCommitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, grammar := loadSourcesForTest(t, root)

	fresh := buildSourcesArtifact(survey, grammar)
	fresh.GeneratedBy = "tools/row-gen -sources"

	data, err := os.ReadFile(filepath.Join(root, sourcesArtifactPath))
	if err != nil {
		t.Fatalf("reading %s: %s", sourcesArtifactPath, err)
	}
	var committed sourcesArtifact
	if err := json.Unmarshal(data, &committed); err != nil {
		t.Fatalf("decoding %s: %s", sourcesArtifactPath, err)
	}

	if committed.Summary != fresh.Summary {
		t.Errorf("%s has drifted - regenerate with `go run ./tools/row-gen -sources`.\ncommitted=%+v\nfresh    =%+v",
			sourcesArtifactPath, committed.Summary, fresh.Summary)
	}
	if len(committed.Conflicts) != len(fresh.Conflicts) {
		t.Errorf("%s lists %d conflicts, a fresh run finds %d", sourcesArtifactPath, len(committed.Conflicts), len(fresh.Conflicts))
	}
}

// TestWireAndDocsAgreeEverywhere pins the finding that reshaped GitHub issue
// #106's own framing.
//
// The issue leads with three sources that might each disagree with the other
// two. Two of them never do: the provider's identity schema and the scraped
// documentation cover 438 types between them and agree on all of them. They
// are the same fact reaching the repository twice, and the scrape is
// faithful.
//
// This is a ratchet, not a decoration. If a scrape change ever introduces a
// wire-vs-docs conflict, that is a scraper bug and this is where it surfaces
// - rather than in a row somebody ratifies six months later.
func TestWireAndDocsAgreeEverywhere(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	art := buildSourcesArtifact(loadSourcesForTest(t, root))

	if art.Summary.Both == 0 {
		t.Fatal("no type is described by both sources, so this asserted nothing")
	}
	for _, c := range art.Conflicts {
		if c.Kind != "wire-vs-docs" {
			continue
		}
		t.Errorf("%s: the provider's identity schema says %v and the scraped documentation says %v.\n"+
			"These have never disagreed. A new disagreement is a scraper bug rather than a judgement call.",
			c.Type, c.Wire, c.Docs)
	}
}

// TestNoRatifiedRowNamesAnUnknownArgument is the aws_ecs_service shape,
// checked against what already shipped rather than against a proposal.
//
// crosscheck.go refuses it in a fresh proposal. This says no ratified row
// carries it today, which is the other half of the claim and the half a
// generator cannot make for itself.
func TestNoRatifiedRowNamesAnUnknownArgument(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	art := buildSourcesArtifact(loadSourcesForTest(t, root))

	if art.Summary.TableRows == 0 {
		t.Fatal("no ratified row had both sources to check against, so this asserted nothing")
	}
	for _, c := range art.Conflicts {
		if c.Kind != "table-vs-schema" {
			continue
		}
		t.Errorf("%s's ratified row builds its identity from %v, which neither the documented Argument Reference nor the Identity Schema knows.\n"+
			"That is what trips FindingArgumentNotInSchema at projection time.", c.Type, c.Table)
	}
}

func loadSourcesForTest(t *testing.T, root string) (map[string]surveyEntry, map[string]importGrammarRow) {
	t.Helper()

	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("loading the survey: %s", err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		t.Fatalf("loading the import grammar: %s", err)
	}
	return survey, grammar
}
