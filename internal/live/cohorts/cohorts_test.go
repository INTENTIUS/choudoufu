// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cohorts

import (
	"sort"
	"strings"
	"testing"
)

// cohortCount pins the roster's size. The 31 cohorts that rendered
// configuration under live/e2e/estates before issue #699 deleted the
// committed trees; live/e2e/estates/example held only a README and rendered
// nothing, so it is not one of them. Adding a cohort moves this number and
// the acceptance artifact's own cohort count together.
const cohortCount = 31

func TestRosterIsWellFormed(t *testing.T) {
	if len(all) != cohortCount {
		t.Errorf("roster holds %d cohorts, want %d", len(all), cohortCount)
	}
	if !sort.SliceIsSorted(all, func(i, j int) bool { return all[i].Name < all[j].Name }) {
		t.Error("the roster is not sorted by name")
	}
	seenName := map[string]bool{}
	for _, c := range all {
		if c.Name == "" {
			t.Fatal("a cohort has no name")
		}
		if seenName[c.Name] {
			t.Errorf("%s: duplicate cohort", c.Name)
		}
		seenName[c.Name] = true
		if len(c.Types) == 0 {
			t.Errorf("%s: empty roster; a cohort with no -types roster renders nothing", c.Name)
		}
		if !sort.StringsAreSorted(c.Types) {
			t.Errorf("%s: Types is not sorted", c.Name)
		}
		if !sort.StringsAreSorted(c.Supporting) {
			t.Errorf("%s: Supporting is not sorted", c.Name)
		}
		inRoster := map[string]bool{}
		for _, typ := range c.Types {
			if inRoster[typ] {
				t.Errorf("%s: %s listed twice in Types", c.Name, typ)
			}
			inRoster[typ] = true
			if !strings.HasPrefix(typ, "aws_") {
				t.Errorf("%s: %s is not a provider-local AWS type", c.Name, typ)
			}
		}
		seenSup := map[string]bool{}
		for _, typ := range c.Supporting {
			if inRoster[typ] {
				// The two lists partition what the rendered tree declares.
				// A type in both would double-count in DeclaredTypes and
				// hide a roster edit behind a supporting-pass measurement.
				t.Errorf("%s: %s is in both Types and Supporting", c.Name, typ)
			}
			if seenSup[typ] {
				t.Errorf("%s: %s listed twice in Supporting", c.Name, typ)
			}
			seenSup[typ] = true
		}
	}
}

// TestFixtureTypesIsTheUnion checks the accessor the ungated union pins in
// internal/live/lint and internal/live/identity now read, rather than
// trusting it because it is short.
func TestFixtureTypesIsTheUnion(t *testing.T) {
	got := FixtureTypes()
	if !sort.StringsAreSorted(got) {
		t.Error("FixtureTypes is not sorted")
	}
	want := map[string]bool{}
	for _, c := range all {
		for _, typ := range c.Types {
			want[typ] = true
		}
		for _, typ := range c.Supporting {
			want[typ] = true
		}
	}
	if len(got) != len(want) {
		t.Errorf("FixtureTypes returned %d types, want the union's %d", len(got), len(want))
	}
	for _, typ := range got {
		if !want[typ] {
			t.Errorf("FixtureTypes returned %s, which no cohort declares", typ)
		}
		if len(CohortsDeclaring(typ)) == 0 {
			t.Errorf("CohortsDeclaring(%s) is empty for a type FixtureTypes returned", typ)
		}
	}
}
