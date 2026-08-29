// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseTypes: comma-separated, trimmed, deduped, sorted; blank input is
// "no filter" (nil).
func TestParseTypes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"aws_instance", []string{"aws_instance"}},
		{"aws_instance,aws_s3_bucket", []string{"aws_instance", "aws_s3_bucket"}},
		{" aws_s3_bucket , aws_instance ,aws_instance", []string{"aws_instance", "aws_s3_bucket"}},
	}
	for _, c := range cases {
		got := ParseTypes(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseTypes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestFilterByTypesSelectsExactSubset is the unit's own accept criterion: a
// small fixture type-index and a --types filter must return exactly the
// right estate subset - no more, no fewer - and stale-pin units must pass
// through untouched regardless of whether their estate exercises a
// requested type, because staleness is about the emulator pin, not about
// resource types (live/GAUNTLET.md: "The stale-pin rule stays completely
// untouched").
func TestFilterByTypesSelectsExactSubset(t *testing.T) {
	idx := TypeIndex{
		"e-lambda":  {"aws_lambda_function": true, "aws_iam_role": true},
		"e-s3":      {"aws_s3_bucket": true},
		"e-both":    {"aws_lambda_function": true, "aws_s3_bucket": true},
		"e-neither": {"aws_vpc": true},
		// e-unindexed intentionally has no entry in idx at all: an estate the
		// type index has never seen must be excluded, not treated as a
		// wildcard match.
	}
	units := []Unit{
		{ID: "e-lambda/day2_rename", Estate: "e-lambda", Stage: "day2_rename"},
		{ID: "e-s3/test_plan", Estate: "e-s3", Stage: "test_plan"},
		{ID: "e-both/migrate", Estate: "e-both", Stage: "migrate"},
		{ID: "e-neither/cold_deploy", Estate: "e-neither", Stage: "cold_deploy"},
		{ID: "e-unindexed/cold_deploy", Estate: "e-unindexed", Stage: "cold_deploy"},
		{ID: "e-neither/" + StageStalePin, Estate: "e-neither", Stage: StageStalePin},
		{ID: "e-unindexed/" + StageStalePin, Estate: "e-unindexed", Stage: StageStalePin},
	}

	got := FilterByTypes(units, idx, []string{"aws_lambda_function"})
	var ids []string
	for _, u := range got {
		ids = append(ids, u.ID)
	}
	want := []string{
		"e-lambda/day2_rename",
		"e-both/migrate",
		"e-neither/" + StageStalePin,   // stale-pin: always kept, no type match needed
		"e-unindexed/" + StageStalePin, // stale-pin: always kept, even with no index entry
	}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("FilterByTypes ids = %v, want %v", ids, want)
	}

	// A multi-type filter is OR, not AND: an estate matching either
	// requested type is kept.
	got = FilterByTypes(units, idx, []string{"aws_lambda_function", "aws_s3_bucket"})
	ids = nil
	for _, u := range got {
		ids = append(ids, u.ID)
	}
	want = []string{
		"e-lambda/day2_rename",
		"e-s3/test_plan",
		"e-both/migrate",
		"e-neither/" + StageStalePin,
		"e-unindexed/" + StageStalePin,
	}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("FilterByTypes (OR) ids = %v, want %v", ids, want)
	}

	// No types requested: every unit passes through, order preserved.
	got = FilterByTypes(units, idx, nil)
	if !reflect.DeepEqual(got, units) {
		t.Errorf("FilterByTypes with no types filter must be a no-op")
	}

	// A type nothing exercises: only stale-pin units survive.
	got = FilterByTypes(units, idx, []string{"aws_nonexistent_type"})
	ids = nil
	for _, u := range got {
		ids = append(ids, u.ID)
	}
	want = []string{"e-neither/" + StageStalePin, "e-unindexed/" + StageStalePin}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("FilterByTypes (no match) ids = %v, want %v", ids, want)
	}
}

// TestLoadTypeIndexReadsCommittedArtifact: the real live/estate-types.json,
// read through the loader used at CLI time, must contain at least the
// well-known estates and types this repository's fixtures always carry so a
// silent schema drift is caught here rather than only at `next -types` call
// time.
func TestLoadTypeIndexReadsCommittedArtifact(t *testing.T) {
	root := testRoot(t)
	idx, err := LoadTypeIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) == 0 {
		t.Fatal("LoadTypeIndex returned an empty index from the committed artifact")
	}
	types, ok := idx["corpus-dynamodb-table-basic"]
	if !ok {
		t.Fatal("corpus-dynamodb-table-basic missing from the loaded type index")
	}
	if !types["aws_dynamodb_table"] {
		t.Errorf("corpus-dynamodb-table-basic's type set does not contain aws_dynamodb_table: %v", types)
	}
}

// TestLoadTypeIndexMissingFileIsEmpty: a checkout that predates #435 (or a
// test fixture root without the artifact) must not error; it reads as no
// index, so every --types filter reports no match rather than crashing.
func TestLoadTypeIndexMissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	idx, err := LoadTypeIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 0 {
		t.Errorf("expected an empty index for a root with no %s, got %v", TypeIndexPath, idx)
	}
}

// TestLoadTypeIndexShape confirms the loader reads the exact fields it needs
// from a minimal fixture and ignores the rest of the schema, so a change to
// live/estate-types.json's other fields (totals, sources, ...) cannot
// silently break --types.
func TestLoadTypeIndexShape(t *testing.T) {
	dir := t.TempDir()
	fixture := `{
  "schema": 1,
  "generated_by": "test",
  "totals": {"estates": 2, "distinct_types": 2, "types_in_no_cohort": 0},
  "estates": [
    {"name": "fixture-a", "types": ["aws_instance"], "count": 1, "sources": ["config"]},
    {"name": "fixture-b", "types": ["aws_s3_bucket", "aws_instance"], "count": 2, "sources": ["config"]}
  ]
}`
	if err := os.MkdirAll(filepath.Join(dir, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, TypeIndexPath), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadTypeIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := TypeIndex{
		"fixture-a": {"aws_instance": true},
		"fixture-b": {"aws_s3_bucket": true, "aws_instance": true},
	}
	if !reflect.DeepEqual(idx, want) {
		t.Errorf("LoadTypeIndex = %v, want %v", idx, want)
	}
}

// TestCmdNextTypesFlagIsDocumented is a light guard that the usage string and
// the flag package both know about -types, so the CLI surface this unit adds
// cannot silently regress to only existing in code comments.
func TestCmdNextTypesFlagIsDocumented(t *testing.T) {
	root := testRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "tools", "gauntlet", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `-types T1,T2,...`) {
		t.Error("usage() no longer documents -types")
	}
}
