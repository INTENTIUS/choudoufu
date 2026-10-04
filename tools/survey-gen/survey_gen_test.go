// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestSurveyJSONMatchesProviderSchemas regenerates live/survey.json
// from the pinned provider's live schemas and diffs it against the
// committed artifact. A mismatch means either the committed file is stale
// (rerun `go run ./tools/survey-gen` and review the diff) or the provider
// release changed under the pin.
//
// It is gated with the floci tier because it needs a terraform binary and a
// provider download, but it starts no emulator: a schema read needs no
// cloud (the same note TestTaggableSetAgainstRealSchemas carries).
//
//	TF_FLOCI_TEST=1 go test ./tools/survey-gen/ -run TestSurveyJSONMatchesProviderSchemas -v
func TestSurveyJSONMatchesProviderSchemas(t *testing.T) {
	flocitest.Gate(t, "survey generation")
	flocitest.RequireBinary(t, defaultInitBin)

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	roster, err := readRoster(filepath.Join(root, rosterRel))
	if err != nil {
		t.Fatalf("reading the roster from %s: %v", rosterRel, err)
	}

	schemas, importable, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring the provider schemas: %v", err)
	}

	got, err := buildSurvey(schemas, roster, importable).marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated survey: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading the committed %s: %v", surveyJSONRel, err)
	}

	// Accepted is a human ratification stamp the generator only writes
	// with -accept (issue #37, increment 1); buildSurvey itself never sets
	// it, so strip it from the committed file before comparing - this test
	// is about whether the rows are stale, not whether someone accepted
	// them.
	var wantS Survey
	if err := json.Unmarshal(want, &wantS); err != nil {
		t.Fatalf("decoding the committed %s: %v", surveyJSONRel, err)
	}
	wantS.Accepted = ""
	wantCanonical, err := wantS.marshal()
	if err != nil {
		t.Fatalf("re-marshaling the committed %s: %v", surveyJSONRel, err)
	}
	if bytes.Equal(got, wantCanonical) {
		return
	}

	// Name the rows that moved rather than dumping two blobs.
	var gotS Survey
	if err := json.Unmarshal(got, &gotS); err != nil {
		t.Fatal(err)
	}
	wantRows := map[string]string{}
	for _, r := range wantS.Types {
		wantRows[r.Type] = rowJSON(t, r)
	}
	for _, g := range gotS.Types {
		w, ok := wantRows[g.Type]
		if !ok {
			t.Errorf("%s: regenerated but absent from the committed file", g.Type)
			continue
		}
		delete(wantRows, g.Type)
		if gj := rowJSON(t, g); gj != w {
			t.Errorf("%s drifted:\n  committed:   %s\n  regenerated: %s", g.Type, w, gj)
		}
	}
	for typeName := range wantRows {
		t.Errorf("%s: committed but no longer regenerated", typeName)
	}
	if gotS.Counts != wantS.Counts {
		t.Errorf("counts drifted: committed %+v, regenerated %+v", wantS.Counts, gotS.Counts)
	}
	t.Errorf("%s is stale; rerun `go run ./tools/survey-gen` and review the diff", surveyJSONRel)
}

// TestSurveyFullJSONMatchesProviderSchemas is
// TestSurveyJSONMatchesProviderSchemas's sibling for live/survey-full.json.
//
// It exists because that test alone left a hole: it regenerates and diffs
// only the curated 68-type live/survey.json, so nothing here ever ran a
// live regeneration of the 1699-type -all roster and compared it byte for
// byte against what is committed. drift_test.go's
// TestSurveyArtifactsMatchTheirExpectations covers survey-full.json too,
// but only against a hand-maintained counts table in the same file -
// an expectation kept in step by hand alongside the artifact, not derived
// from a fresh run - so a type whose row moved without a regeneration
// (aws_ecs_capacity_provider, after #150 gave it region/account-id-bearing
// IdentityAttrs) passed that
// gate every time, exactly the #169 shape both tests exist to catch. This
// one would have failed on the spot.
//
//	TF_FLOCI_TEST=1 go test ./tools/survey-gen/ -run TestSurveyFullJSONMatchesProviderSchemas -v
func TestSurveyFullJSONMatchesProviderSchemas(t *testing.T) {
	flocitest.Gate(t, "survey generation")
	flocitest.RequireBinary(t, defaultInitBin)

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	schemas, importable, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring the provider schemas: %v", err)
	}

	full := buildSurvey(schemas, allResourceTypeNames(schemas), importable)
	full.GeneratedBy = "tools/survey-gen (go run ./tools/survey-gen -all)"
	got, err := full.marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated %s: %v", surveyFullJSONRel, err)
	}
	want, err := os.ReadFile(filepath.Join(root, surveyFullJSONRel))
	if err != nil {
		t.Fatalf("reading the committed %s: %v", surveyFullJSONRel, err)
	}

	// Accepted is a human ratification stamp the generator only writes
	// with -accept; buildSurvey never sets it, so strip it from the
	// committed file before comparing - this test is about whether the
	// rows are stale, not whether someone accepted them.
	var wantS Survey
	if err := json.Unmarshal(want, &wantS); err != nil {
		t.Fatalf("decoding the committed %s: %v", surveyFullJSONRel, err)
	}
	wantS.Accepted = ""
	wantCanonical, err := wantS.marshal()
	if err != nil {
		t.Fatalf("re-marshaling the committed %s: %v", surveyFullJSONRel, err)
	}
	if bytes.Equal(got, wantCanonical) {
		return
	}

	// Name the rows that moved rather than dumping two 1699-row blobs.
	var gotS Survey
	if err := json.Unmarshal(got, &gotS); err != nil {
		t.Fatal(err)
	}
	wantRows := map[string]string{}
	for _, r := range wantS.Types {
		wantRows[r.Type] = rowJSON(t, r)
	}
	for _, g := range gotS.Types {
		w, ok := wantRows[g.Type]
		if !ok {
			t.Errorf("%s: regenerated but absent from the committed file", g.Type)
			continue
		}
		delete(wantRows, g.Type)
		if gj := rowJSON(t, g); gj != w {
			t.Errorf("%s drifted:\n  committed:   %s\n  regenerated: %s", g.Type, w, gj)
		}
	}
	for typeName := range wantRows {
		t.Errorf("%s: committed but no longer regenerated", typeName)
	}
	if gotS.Counts != wantS.Counts {
		t.Errorf("counts drifted: committed %+v, regenerated %+v", wantS.Counts, gotS.Counts)
	}
	t.Errorf("%s is stale; rerun `go run ./tools/survey-gen -all` and review the diff", surveyFullJSONRel)
}

// rowJSON renders one row canonically for comparison and for failure
// messages (Row carries a pointer field, so == would compare addresses).
func rowJSON(t *testing.T, r Row) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshaling a row: %v", err)
	}
	return string(b)
}

// testLogWriter routes the generator's progress lines through t.Logf.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", bytes.TrimRight(p, "\n"))
	return len(p), nil
}
