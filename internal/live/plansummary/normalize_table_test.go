// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plansummary

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func raw(t testing.TB, v any) json.RawMessage {
	t.Helper()
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func change(t testing.TB, addr string, actions []string, before, after map[string]any) ResourceChange {
	t.Helper()
	rc := ResourceChange{Address: addr, Change: Change{Actions: actions}}
	if before != nil {
		rc.Change.Before = raw(t, before)
	}
	if after != nil {
		rc.Change.After = raw(t, after)
	}
	return rc
}

var (
	create  = []string{"create"}
	update  = []string{"update"}
	replace = []string{"delete", "create"}
	destroy = []string{"delete"}
)

func root(dir, estate string, changes ...ResourceChange) SetRoot {
	return SetRoot{Root: dir, Estate: estate, Status: "planned", Plan: &Plan{FormatVersion: "1.2", ResourceChanges: changes}}
}

// tableRow is one pair of roots and whether the summary must put them in
// one group.
type tableRow struct {
	name  string
	a, b  SetRoot
	group bool
}

// vectorFile is chant's shared table of grouping vectors, vendored
// byte-for-byte (INTENTIUS/choudoufu#1855, INTENTIUS/chant#3188). CI checks
// the copy against chant main; edit it in chant, never here.
const vectorFile = "testdata/normalization-vectors.json"

type vectorDoc struct {
	Format    string `json:"format"`
	RootPairs []struct {
		Name  string  `json:"name"`
		Group bool    `json:"group"`
		A     SetRoot `json:"a"`
		B     SetRoot `json:"b"`
	} `json:"rootPairs"`
	InstancePairs []struct {
		Name  string         `json:"name"`
		Group bool           `json:"group"`
		A     ResourceChange `json:"a"`
		B     ResourceChange `json:"b"`
	} `json:"instancePairs"`
	OverEagerFalselyGroups []string `json:"overEagerFalselyGroups"`
}

func loadVectors(t testing.TB) vectorDoc {
	t.Helper()
	data, err := os.ReadFile(vectorFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc vectorDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s: %v", vectorFile, err)
	}
	if doc.Format != "plan-summary-normalization-vectors/1" {
		t.Fatalf("%s: format %q, want plan-summary-normalization-vectors/1", vectorFile, doc.Format)
	}
	if len(doc.RootPairs) == 0 || len(doc.InstancePairs) == 0 {
		t.Fatalf("%s: no rows (%d root pairs, %d instance pairs)", vectorFile, len(doc.RootPairs), len(doc.InstancePairs))
	}
	return doc
}

func normalizationTable(t testing.TB) []tableRow {
	var rows []tableRow
	for _, r := range loadVectors(t).RootPairs {
		rows = append(rows, tableRow{name: r.Name, a: r.A, b: r.B, group: r.Group})
	}
	return rows
}

// grouper says whether two roots land in one group.
type grouper func(a, b SetRoot) bool

func realGrouper(a, b SetRoot) bool {
	s := Summarize(Input{Set: &SetDocument{Roots: []SetRoot{a, b}}})
	return len(s.Failed) == 0 && len(s.Groups) == 1
}

// naiveGrouper is grouping by a hash of the raw change set: equal bytes or
// nothing.
func naiveGrouper(a, b SetRoot) bool {
	ra, _ := json.Marshal(a.Plan.ResourceChanges)
	rb, _ := json.Marshal(b.Plan.ResourceChanges)
	return string(ra) == string(rb)
}

// overEagerGrouper is the normalizer this package must not be: it blanks
// every string value before grouping, which groups everything that differs
// only in a string.
func overEagerGrouper(a, b SetRoot) bool {
	return realGrouper(blankStrings(a), blankStrings(b))
}

func blankStrings(r SetRoot) SetRoot {
	var blank func(v any) any
	blank = func(v any) any {
		switch t := v.(type) {
		case string:
			return "x"
		case []any:
			for i := range t {
				t[i] = blank(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = blank(t[k])
			}
		}
		return v
	}
	plan := *r.Plan
	plan.ResourceChanges = nil
	for _, rc := range r.Plan.ResourceChanges {
		for _, side := range []*json.RawMessage{&rc.Change.Before, &rc.Change.After} {
			if v := decode(*side); v != nil {
				*side, _ = json.Marshal(blank(v))
			}
		}
		plan.ResourceChanges = append(plan.ResourceChanges, rc)
	}
	r.Plan = &plan
	return r
}

func checkTable(t *testing.T, g grouper) {
	for _, row := range normalizationTable(t) {
		if got := g(row.a, row.b); got != row.group {
			t.Errorf("%s: grouped=%v, want %v", row.name, got, row.group)
		}
	}
}

// TestNormalizationTable is #1753's normalization table against the real
// summary.
func TestNormalizationTable(t *testing.T) {
	checkTable(t, realGrouper)
}

// TestNaiveGroupingFailsEveryMustGroupRow keeps the table's red-first proof:
// every must-group row is a pair a raw hash keeps apart, so none of them
// passes by accident of identical input.
func TestNaiveGroupingFailsEveryMustGroupRow(t *testing.T) {
	for _, row := range normalizationTable(t) {
		if row.group && naiveGrouper(row.a, row.b) {
			t.Errorf("%s: a raw hash already groups this pair, so the row proves nothing about normalization", row.name)
		}
	}
}

// TestOverEagerNormalizerFailsMustNotRows is the other direction: a
// normalizer that strips every string falsely groups the must-not rows
// whose difference is a string. If this stops failing them, the table no
// longer guards against blanket stripping.
func TestOverEagerNormalizerFailsMustNotRows(t *testing.T) {
	falselyGrouped := map[string]bool{}
	for _, row := range normalizationTable(t) {
		if !row.group && overEagerGrouper(row.a, row.b) {
			falselyGrouped[row.name] = true
		}
	}
	want := map[string]bool{}
	for _, name := range loadVectors(t).OverEagerFalselyGroups {
		want[name] = true
	}
	if !reflect.DeepEqual(falselyGrouped, want) {
		t.Errorf("an over-eager normalizer falsely grouped %v, want %v", falselyGrouped, want)
	}
}

// TestPlanInstanceTable is the same rule over one plan's for_each/count
// expansion, where an instance's own key is its name. The pairs are the
// shared file's instancePairs.
func TestPlanInstanceTable(t *testing.T) {
	for _, tc := range loadVectors(t).InstancePairs {
		s := Summarize(Input{Plan: &Plan{ResourceChanges: []ResourceChange{tc.A, tc.B}}})
		if got := len(s.Groups) == 1; got != tc.Group {
			t.Errorf("%s: grouped=%v, want %v (%d groups)", tc.Name, got, tc.Group, len(s.Groups))
		}
	}
}

// TestSharedVectorFileIsWellFormed holds the vendored file to its own
// contract: row names are unique, and every name in overEagerFalselyGroups
// is a root pair.
func TestSharedVectorFileIsWellFormed(t *testing.T) {
	doc := loadVectors(t)
	roots := map[string]bool{}
	for _, r := range doc.RootPairs {
		if roots[r.Name] {
			t.Errorf("root pair %q appears twice", r.Name)
		}
		roots[r.Name] = true
	}
	inst := map[string]bool{}
	for _, r := range doc.InstancePairs {
		if inst[r.Name] {
			t.Errorf("instance pair %q appears twice", r.Name)
		}
		inst[r.Name] = true
	}
	for _, name := range doc.OverEagerFalselyGroups {
		if !roots[name] {
			t.Errorf("overEagerFalselyGroups names %q, which is not a root pair", name)
		}
	}
}
