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

	residue "github.com/intentius/choudoufu/live"
)

// TestLimitationsMDResidueRosterSpans holds live/LIMITATIONS.md's five
// residue-roster spans byte-for-byte to what live/residue.go's accessors
// produce from the committed live/mapping.json and live/registry.json —
// the same drift pattern TestSurveyMDRenderedSpans applies to SURVEY.md,
// applied here to the residue roster (issue #49). Two committed artifacts
// and one doc, no provider, so it is not gated; drift fails here with the
// command that fixes it.
func TestLimitationsMDResidueRosterSpans(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	mdPath := filepath.Join(root, limitationsMDRel)
	mdBytes, err := os.ReadFile(mdPath) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v", limitationsMDRel, err)
	}
	md := string(mdBytes)

	for _, span := range []struct {
		name, want string
	}{
		{spanResidueDeprecated, renderResidueDeprecated()},
		{spanResidueCFNOnly, renderResidueCFNOnly()},
		{spanResidueUnmapped, renderResidueUnmapped()},
		{spanResidueLaggard, renderResidueLaggard()},
		{spanResidueEmulator, renderResidueEmulator()},
	} {
		got, err := spanContent(limitationsMDRel, md, span.name)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if got != span.want {
			t.Errorf("%s's %q span is stale; run `go run ./tools/survey-gen -render` and commit the result.\n--- committed ---\n%s--- rendered ---\n%s",
				limitationsMDRel, span.name, got, span.want)
		}
	}

	// The whole-file check catches what the per-span one cannot: a marker
	// pair going missing or duplicated.
	if out, err := renderResidueSpans(md); err != nil {
		t.Errorf("rendering %s's residue-roster spans: %v", limitationsMDRel, err)
	} else if out != md {
		t.Errorf("%s differs from its rendered form; run `go run ./tools/survey-gen -render` and commit the result", limitationsMDRel)
	}
}

// TestResidueCFNOnlyHasNoTFCounterpart is the doc-side half of the CFN-only
// cohort's computed claim: every curated entry is a real registry type
// (checked already, at package load, by live/residue.go's own init-time
// panic) and, independently here, not referenced by any live/mapping.json
// row as either a cfn_type or a fold_parent. A future mapping-gen run that
// links one of these four to a new TF type should fail this test, not
// silently leave the doc claiming "no TF counterpart" for a type that now
// has one.
func TestResidueCFNOnlyHasNoTFCounterpart(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "live", "mapping.json")) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading live/mapping.json: %v", err)
	}
	var mapping struct {
		Rows []struct {
			CFNType    *string `json:"cfn_type"`
			FoldParent *string `json:"fold_parent"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(data, &mapping); err != nil {
		t.Fatalf("decoding live/mapping.json: %v", err)
	}

	referenced := map[string]bool{}
	for _, r := range mapping.Rows {
		if r.CFNType != nil {
			referenced[*r.CFNType] = true
		}
		if r.FoldParent != nil {
			referenced[*r.FoldParent] = true
		}
	}
	for _, c := range residue.CFNOnlyConstructs {
		if referenced[c.Type] {
			t.Errorf("residue.CFNOnlyConstructs names %s, which live/mapping.json now maps a TF type to; it is no longer CFN-only", c.Type)
		}
	}
}
