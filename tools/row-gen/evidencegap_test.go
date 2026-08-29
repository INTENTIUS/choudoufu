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

// TestEvidenceSchemaGapArtifactMatchesCommitted is the drift guard: the
// committed remainder ledger has to be what a fresh run produces, matching
// TestSourcesArtifactMatchesCommitted's own pattern - a change to the
// roster, the survey pin, or tools/row-gen/rejected.json shows up as a test
// failure rather than a silently stale file.
func TestEvidenceSchemaGapArtifactMatchesCommitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatalf("loadProposals: %v", err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("loadSurvey: %v", err)
	}
	rejected, err := loadRejectedTypes(root)
	if err != nil {
		t.Fatalf("loadRejectedTypes: %v", err)
	}

	fresh := buildEvidenceSchemaGapArtifact(proposals, survey, rejected)
	fresh.GeneratedBy = "tools/row-gen -evidence-gap"

	data, err := os.ReadFile(filepath.Join(root, evidenceSchemaGapArtifactPath))
	if err != nil {
		t.Fatalf("reading %s: %s", evidenceSchemaGapArtifactPath, err)
	}
	var committed evidenceSchemaGapArtifact
	if err := json.Unmarshal(data, &committed); err != nil {
		t.Fatalf("decoding %s: %s", evidenceSchemaGapArtifactPath, err)
	}

	freshJSON, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the fresh artifact: %v", err)
	}
	committedJSON, err := json.MarshalIndent(committed, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the committed artifact: %v", err)
	}
	if string(freshJSON) != string(committedJSON) {
		t.Errorf("%s has drifted - regenerate with `go run ./tools/row-gen -evidence-gap`.\ncommitted: %d with-schema, %d no-schema\nfresh:     %d with-schema, %d no-schema",
			evidenceSchemaGapArtifactPath,
			committed.TotalWithSchemaNotCovered, committed.TotalNoSchema,
			fresh.TotalWithSchemaNotCovered, fresh.TotalNoSchema)
	}
}

// TestEvidenceSchemaGapFamiliesPartitionCleanly holds two invariants a hand
// or generator bug could silently break: every family actually carries the
// members its own Count claims, and every member type genuinely belongs to
// exactly the family it is filed under (no type appears twice across
// either artifact half).
func TestEvidenceSchemaGapFamiliesPartitionCleanly(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatalf("loadProposals: %v", err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("loadSurvey: %v", err)
	}
	rejected, err := loadRejectedTypes(root)
	if err != nil {
		t.Fatalf("loadRejectedTypes: %v", err)
	}
	art := buildEvidenceSchemaGapArtifact(proposals, survey, rejected)

	seen := map[string]string{} // type -> which half/family it was already seen in
	check := func(half string, families []evidenceGapFamily) {
		for _, f := range families {
			if f.Count != len(f.Members) {
				t.Errorf("%s family %q: Count=%d but %d Members", half, f.Family, f.Count, len(f.Members))
			}
			if f.Note == "" {
				t.Errorf("%s family %q has no note - every family must name what source would close the gap", half, f.Family)
			}
			for _, m := range f.Members {
				if prior, dup := seen[m]; dup {
					t.Errorf("%s: %s appears in family %q and also in %s", half, m, f.Family, prior)
				}
				seen[m] = half + "/" + f.Family
			}
		}
	}
	check("evidence_only_schema", art.EvidenceOnlySchemaFamilies)
	check("no_identity_schema", art.NoIdentitySchemaFamilies)
}
