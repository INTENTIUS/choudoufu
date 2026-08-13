// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"encoding/json"
	"reflect"
	"testing"
)

func admissions(r DerivabilityReport) map[string]Admission {
	out := map[string]Admission{}
	for _, c := range r.Candidates {
		out[c.Type] = c.Admits
	}
	return out
}

// With no configuration, the report says what each candidate is waiting
// for. That is the survey generator's case: a provider release, no estate.
func TestReportWithoutAConfiguration(t *testing.T) {
	r := Report(cohortSchemas(), nil)

	want := map[string]Admission{
		"aws_ssm_parameter": AdmitSchema,
		"aws_s3_bucket":     AdmitNeedsConfigSignal,
		"aws_fake_queue":    AdmitNeedsConfigSignal,
		"aws_vpc":           AdmitNeedsConfigSignal,
	}
	if got := admissions(r); !reflect.DeepEqual(got, want) {
		t.Errorf("the report says %v, want %v", got, want)
	}

	// A type whose identity attribute is not an argument at all is not a
	// candidate under any evidence, so it gets no row rather than a "no".
	if _, ok := r.Admits("aws_odd_thing"); ok {
		t.Error("a computed-only identity attribute produced a candidate row")
	}

	// The row names exactly what a configuration would have to set.
	bucket, ok := r.Admits("aws_s3_bucket")
	if !ok {
		t.Fatal("aws_s3_bucket has no row")
	}
	if !reflect.DeepEqual(bucket.IdentityAttrs, []string{"bucket"}) {
		t.Errorf("the row asks for %v, want [bucket]", bucket.IdentityAttrs)
	}
	if !bucket.InTable {
		t.Error("aws_s3_bucket is in the hand table and the row should say so")
	}

	// Nothing is admitted that a hand table does not already carry, because
	// nothing has read a configuration yet. That is the honest starting
	// point the survey records.
	wantCounts := AdmissionCounts{Schema: 1, NeedsConfigSignal: 3, NewToTable: 0}
	if r.Counts != wantCounts {
		t.Errorf("counts are %+v, want %+v", r.Counts, wantCounts)
	}
}

// With a configuration, the cohort's rows say what it answered, and the
// counts split the batch nobody has to write out of the rest.
func TestReportWithAConfiguration(t *testing.T) {
	signal := scanFixture(t, "naming-signal-named")
	r := Report(cohortSchemas(), signal)

	want := map[string]Admission{
		"aws_ssm_parameter": AdmitSchema,
		"aws_s3_bucket":     AdmitConfigSignal,
		"aws_fake_queue":    AdmitConfigSignal,
		// No block names a VPC, and the configuration saying so is an
		// answer rather than a silence.
		"aws_vpc": AdmitConfigDeclined,
	}
	if got := admissions(r); !reflect.DeepEqual(got, want) {
		t.Errorf("the report says %v, want %v", got, want)
	}

	wantCounts := AdmissionCounts{
		Schema:            1,
		ConfigSignal:      2,
		NeedsConfigSignal: 0,
		ConfigDeclined:    1,
		// aws_fake_queue alone: the other two admitted types are already in
		// the hand table.
		NewToTable: 1,
	}
	if r.Counts != wantCounts {
		t.Errorf("counts are %+v, want %+v", r.Counts, wantCounts)
	}
}

// A configuration that half-answers is reported as declining, not as
// undecided: "some instances name themselves" is a fact about this
// configuration, and a later batch reading the report must not mistake it
// for "nobody has looked yet".
func TestReportWithADecliningConfiguration(t *testing.T) {
	r := Report(cohortSchemas(), scanFixture(t, "naming-signal"))

	if got := admissions(r)["aws_s3_bucket"]; got != AdmitConfigDeclined {
		t.Errorf("one named bucket out of three is reported as %q, want %q", got, AdmitConfigDeclined)
	}
	// The queue is not in this configuration at all, so nothing here
	// answers for it.
	if got := admissions(r)["aws_fake_queue"]; got != AdmitNeedsConfigSignal {
		t.Errorf("a type the configuration does not declare is reported as %q, want %q", got, AdmitNeedsConfigSignal)
	}
}

// The report is what another program reads, so its shape is pinned: sorted
// rows, snake_case fields, no timestamps, and the same bytes for the same
// schemas.
func TestReportMarshalsStably(t *testing.T) {
	schemas := cohortSchemas()

	first, err := json.Marshal(Report(schemas, nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(Report(schemas, nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("two reports over one provider differ:\n%s\n%s", first, second)
	}

	var decoded struct {
		Candidates []struct {
			Type          string   `json:"type"`
			Admits        string   `json:"admits"`
			IdentityAttrs []string `json:"identity_attrs"`
			InTable       bool     `json:"in_table"`
		} `json:"candidates"`
		Counts struct {
			Schema            int `json:"schema"`
			NeedsConfigSignal int `json:"needs_config_signal"`
			NewToTable        int `json:"new_to_table"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Candidates) != 4 {
		t.Fatalf("decoded %d candidates, want 4: %s", len(decoded.Candidates), first)
	}
	for i := 1; i < len(decoded.Candidates); i++ {
		if decoded.Candidates[i-1].Type >= decoded.Candidates[i].Type {
			t.Errorf("candidates are not sorted: %s", first)
			break
		}
	}
	if decoded.Counts.Schema != 1 || decoded.Counts.NeedsConfigSignal != 3 {
		t.Errorf("counts did not survive the round trip: %s", first)
	}
}

// Admits is a lookup over a sorted slice, so it has to answer for the first
// and last rows too.
func TestReportAdmitsLookup(t *testing.T) {
	r := Report(cohortSchemas(), nil)
	for _, c := range r.Candidates {
		got, ok := r.Admits(c.Type)
		if !ok || got.Type != c.Type {
			t.Errorf("looking up %s found %v (%v)", c.Type, got, ok)
		}
	}
	if _, ok := r.Admits("aws_nothing_at_all"); ok {
		t.Error("a type with no row was found anyway")
	}
}
