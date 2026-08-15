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
	"reflect"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// fakeAllSchema builds a minimal resource schema good enough for classify:
// a required "name" argument, and a top-level settable tags map when
// tagged. Not the identity package's own fakeProviderSchemas (that helper
// is unexported in a different package) - just enough shape for this file's
// roster-selection tests, which do not exercise the classifier's path
// judgment, only which types make it into a survey at all.
func fakeAllSchema(tagged bool) providers.Schema {
	attrs := map[string]*configschema.Attribute{
		"name": {Type: cty.String, Required: true},
	}
	if tagged {
		attrs["tags"] = &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	}
	return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
}

// TestAllResourceTypeNames checks the -all roster is every ResourceTypes
// key the provider's schema carries, nothing more and nothing less.
func TestAllResourceTypeNames(t *testing.T) {
	schemas := providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"aws_a": fakeAllSchema(false),
			"aws_b": fakeAllSchema(false),
			"aws_c": fakeAllSchema(false),
		},
	}
	got := allResourceTypeNames(schemas)
	sort.Strings(got)
	want := []string{"aws_a", "aws_b", "aws_c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("allResourceTypeNames = %v, want %v", got, want)
	}
}

// TestBuildSurveyAllRosterSupersedesCurated is issue #41's own framing of
// the delta: buildSurvey already classifies provider-wide once given every
// type name instead of the curated roster, so a type outside SURVEY.md's
// table must appear in the -all roster's survey, and every type both
// rosters share must classify identically either way - -all changes which
// rows exist, not what an existing row says.
func TestBuildSurveyAllRosterSupersedesCurated(t *testing.T) {
	schemas := providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"aws_curated_one": fakeAllSchema(true),
			"aws_curated_two": fakeAllSchema(false),
			"aws_extra_only":  fakeAllSchema(true),
		},
	}
	curatedRoster := []string{"aws_curated_one", "aws_curated_two"}

	curated := buildSurvey(schemas, curatedRoster, testServiceOf)
	if curated.Counts.Types != 2 {
		t.Fatalf("curated survey has %d types, want 2", curated.Counts.Types)
	}
	for _, r := range curated.Types {
		if r.Type == "aws_extra_only" {
			t.Errorf("curated survey (no -all) must not include aws_extra_only")
		}
	}

	full := buildSurvey(schemas, allResourceTypeNames(schemas), testServiceOf)
	if full.Counts.Types != 3 {
		t.Fatalf("full survey has %d types, want 3", full.Counts.Types)
	}
	fullRows := map[string]Row{}
	for _, r := range full.Types {
		fullRows[r.Type] = r
	}
	if _, ok := fullRows["aws_extra_only"]; !ok {
		t.Errorf("full survey is missing aws_extra_only, the type outside the curated roster")
	}

	for _, cr := range curated.Types {
		fr, ok := fullRows[cr.Type]
		if !ok {
			t.Fatalf("full survey is missing curated type %s", cr.Type)
		}
		if !reflect.DeepEqual(cr, fr) {
			t.Errorf("%s classifies differently under -all:\n  curated: %+v\n  full:    %+v", cr.Type, cr, fr)
		}
	}
}

// TestWriteSurveysCuratedBytesIndependentOfAll pins the issue's acceptance
// bar: live/survey.json's bytes never depend on whether -all was passed.
// -all only adds live/survey-full.json alongside it.
func TestWriteSurveysCuratedBytesIndependentOfAll(t *testing.T) {
	schemas := providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"aws_curated_one": fakeAllSchema(true),
			"aws_curated_two": fakeAllSchema(false),
			"aws_extra_only":  fakeAllSchema(true),
		},
	}
	roster := []HandRow{{Type: "aws_curated_one"}, {Type: "aws_curated_two"}}

	rootWithout, rootWith := t.TempDir(), t.TempDir()
	for _, root := range []string{rootWithout, rootWith} {
		if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var log bytes.Buffer
	if err := writeSurveys(rootWithout, schemas, roster, false, false, "", &log); err != nil {
		t.Fatalf("writeSurveys without -all: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootWithout, surveyFullJSONRel)); !os.IsNotExist(err) {
		t.Errorf("writeSurveys without -all must not write %s (stat err: %v)", surveyFullJSONRel, err)
	}

	if err := writeSurveys(rootWith, schemas, roster, true, false, "", &log); err != nil {
		t.Fatalf("writeSurveys with -all: %v", err)
	}

	gotWithout, err := os.ReadFile(filepath.Join(rootWithout, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading %s (without -all): %v", surveyJSONRel, err)
	}
	gotWith, err := os.ReadFile(filepath.Join(rootWith, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading %s (with -all): %v", surveyJSONRel, err)
	}
	if !bytes.Equal(gotWithout, gotWith) {
		t.Errorf("%s depends on -all:\n--- without -all ---\n%s--- with -all ---\n%s", surveyJSONRel, gotWithout, gotWith)
	}

	fullData, err := os.ReadFile(filepath.Join(rootWith, surveyFullJSONRel))
	if err != nil {
		t.Fatalf("reading %s: %v", surveyFullJSONRel, err)
	}
	var full Survey
	if err := json.Unmarshal(fullData, &full); err != nil {
		t.Fatalf("decoding %s: %v", surveyFullJSONRel, err)
	}
	if full.Counts.Types != 3 {
		t.Errorf("%s has %d types, want 3 (the provider's whole roster)", surveyFullJSONRel, full.Counts.Types)
	}
	const wantGeneratedBy = "tools/survey-gen (go run ./tools/survey-gen -all)"
	if full.GeneratedBy != wantGeneratedBy {
		t.Errorf("%s's generated_by = %q, want %q", surveyFullJSONRel, full.GeneratedBy, wantGeneratedBy)
	}
}

// TestWriteSurveysAcceptStampsHeader is issue #37 increment 1's own bar:
// the accepted field is written only when a human passes -accept, both
// artifacts get the same date, and leaving -accept off leaves the field
// out of the header rather than guessing a date forward.
func TestWriteSurveysAcceptStampsHeader(t *testing.T) {
	schemas := providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"aws_curated_one": fakeAllSchema(true),
		},
	}
	roster := []HandRow{{Type: "aws_curated_one"}}

	rootAccepted, rootUnaccepted := t.TempDir(), t.TempDir()
	for _, root := range []string{rootAccepted, rootUnaccepted} {
		if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var log bytes.Buffer
	const today = "2026-08-12"
	if err := writeSurveys(rootAccepted, schemas, roster, true, true, today, &log); err != nil {
		t.Fatalf("writeSurveys with -accept: %v", err)
	}
	if err := writeSurveys(rootUnaccepted, schemas, roster, true, false, "", &log); err != nil {
		t.Fatalf("writeSurveys without -accept: %v", err)
	}

	for _, tc := range []struct {
		root, rel string
		wantDate  string
	}{
		{rootAccepted, surveyJSONRel, today},
		{rootAccepted, surveyFullJSONRel, today},
		{rootUnaccepted, surveyJSONRel, ""},
		{rootUnaccepted, surveyFullJSONRel, ""},
	} {
		data, err := os.ReadFile(filepath.Join(tc.root, tc.rel))
		if err != nil {
			t.Fatalf("reading %s: %v", tc.rel, err)
		}
		var s Survey
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("decoding %s: %v", tc.rel, err)
		}
		if s.Accepted != tc.wantDate {
			t.Errorf("%s (accept=%v): accepted = %q, want %q", tc.rel, tc.wantDate != "", s.Accepted, tc.wantDate)
		}
		if tc.wantDate == "" && bytes.Contains(data, []byte(`"accepted"`)) {
			t.Errorf("%s: an unaccepted run must omit the accepted field entirely, not write it empty", tc.rel)
		}
	}
}
