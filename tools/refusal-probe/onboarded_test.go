// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/check"
)

// The onboarded-form mode's guards.
//
// The failure this mode could most easily have is not a wrong number, it is a
// number about a different thing than the reader assumes - which is what the
// rest of this tool exists to prevent. Two specific shapes:
//
//   - The published-form numbers moving. Every burndown figure this project
//     quotes is computed from them, and a second column added beside them
//     must not touch them.
//   - The onboarded column silently measuring the published form, because
//     the overlay never reached the loader. That failure prints "onboarding
//     changes nothing", which is a plausible-looking result and would have
//     been believed.

// onboardCorpus lays out a two-entry corpus: one estate a record_store
// admits, one it does not.
func onboardCorpus(t *testing.T) (root, manifest string) {
	t.Helper()
	root = t.TempDir()

	// Admitted by a record store: a null_resource is refused without one.
	writeAt(t, filepath.Join(root, "estates", "effects", "main.tf"),
		"terraform {\n  backend \"s3\" {\n    bucket = \"b\"\n  }\n}\n\nresource \"null_resource\" \"a\" {}\n")

	// Not admitted by anything: count.index in an identity-bearing argument
	// is language wall, and no live block touches it.
	writeAt(t, filepath.Join(root, "estates", "wall", "main.tf"),
		"resource \"aws_s3_bucket\" \"b\" {\n  count  = 4\n  bucket = \"name-${count.index % 2}\"\n}\n")

	manifest = writeManifest(t, root, map[string]any{
		"origin": "in-repo fixture",
		"glob":   "estates/*",
	})
	return root, manifest
}

func writeAt(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sweepBoth(t *testing.T, root, manifest string) (published, onboarded *run) {
	t.Helper()
	base := sweepOptions{manifest: manifest, root: root, allowPartial: true}
	p, err := sweep(base)
	if err != nil {
		t.Fatal(err)
	}
	base.onboarded = true
	o, err := sweep(base)
	if err != nil {
		t.Fatal(err)
	}
	return p, o
}

func entryNamed(t *testing.T, r *run, suffix string) entry {
	t.Helper()
	for _, e := range r.Entries {
		if strings.HasSuffix(e.Name, suffix) {
			return e
		}
	}
	t.Fatalf("no entry ending %q in %d entries", suffix, len(r.Entries))
	return entry{}
}

// TestOnboardedModeLeavesThePublishedFormAlone. Everything above
// entry.Onboarding describes the published form in both modes, and this is
// the assertion that says so field by field rather than by reading the code.
func TestOnboardedModeLeavesThePublishedFormAlone(t *testing.T) {
	root, manifest := onboardCorpus(t)
	published, onboarded := sweepBoth(t, root, manifest)

	if published.Totals != onboarded.Totals {
		// Totals holds the onboarding pointer, so compare the rest field by
		// field via a copy with it cleared.
		p, o := published.Totals, onboarded.Totals
		p.Onboarding, o.Onboarding = nil, nil
		if p != o {
			t.Errorf("totals moved:\n published %+v\n onboarded %+v", p, o)
		}
	}
	if len(published.Entries) != len(onboarded.Entries) {
		t.Fatalf("entry count moved: %d -> %d", len(published.Entries), len(onboarded.Entries))
	}
	for i := range published.Entries {
		p, o := published.Entries[i], onboarded.Entries[i]
		o.Onboarding, o.Onboarded = nil, nil
		if p.Name != o.Name || p.Blocked != o.Blocked || p.Sites != o.Sites ||
			p.Instances != o.Instances || p.Shadowed != o.Shadowed || p.Readable != o.Readable {
			t.Errorf("entry %s moved:\n published %+v\n onboarded %+v", p.Name, p, o)
		}
		if len(p.Refusals) != len(o.Refusals) {
			t.Errorf("entry %s refusal set moved: %v -> %v", p.Name, p.Refusals, o.Refusals)
			continue
		}
		for id, n := range p.Refusals {
			if o.Refusals[id] != n {
				t.Errorf("entry %s refusal %s: %d -> %d", p.Name, id, n, o.Refusals[id])
			}
		}
	}
	if published.OnboardedForm || !onboarded.OnboardedForm {
		t.Errorf("onboarded_form = %v / %v, want false / true", published.OnboardedForm, onboarded.OnboardedForm)
	}
	if published.Totals.Onboarding != nil {
		t.Error("a default sweep wrote onboarding totals")
	}
}

// TestOnboardedModeActuallyReachesTheLoader is the mutation this file exists
// for. If the overlay never reaches configs.Parser - a wrong path key, a
// filesystem that reads through to the base, a plan whose overlay was dropped
// - the onboarded column equals the published column exactly, every delta is
// zero, and the output reads "onboarding changes nothing". That is a
// believable answer, which is what makes it dangerous.
func TestOnboardedModeActuallyReachesTheLoader(t *testing.T) {
	root, manifest := onboardCorpus(t)
	_, onboarded := sweepBoth(t, root, manifest)

	effects := entryNamed(t, onboarded, "effects")
	if effects.Onboarding == nil || effects.Onboarding.Status != "edited" {
		t.Fatalf("onboarding record = %+v, want an edit", effects.Onboarding)
	}
	if effects.Refusals["logical-resource"] == 0 {
		t.Fatal("the published form did not refuse the null_resource, so this test measures nothing")
	}
	if effects.Onboarded == nil {
		t.Fatal("no onboarded result")
	}
	if got := effects.Onboarded.Refusals["logical-resource"]; got != 0 {
		t.Errorf("the record store did not admit the null_resource: %d site(s) remain", got)
	}
	if effects.Onboarded.Blocked {
		t.Errorf("still blocked after onboarding: %v", effects.Onboarded.Refusals)
	}

	// And the estate whose blocker no live block answers must be untouched.
	// A mode that "cleared" everything would pass the assertion above.
	wall := entryNamed(t, onboarded, "wall")
	if wall.Refusals["count-index"] == 0 {
		t.Fatalf("the wall fixture does not refuse count-index (%v), so the negative half of this test measures nothing", wall.Refusals)
	}
	if wall.Onboarded == nil || !wall.Onboarded.Blocked {
		t.Errorf("a count.index identity cleared under onboarding; the edit is doing more than it claims: %+v", wall.Onboarded)
	}
	if wall.Onboarded.Refusals["count-index"] != wall.Refusals["count-index"] {
		t.Errorf("count-index moved under onboarding: %d -> %d",
			wall.Refusals["count-index"], wall.Onboarded.Refusals["count-index"])
	}
}

// TestOnboardingTotalsPartitionTheCorpus. edited + already + unmeasurable has
// to equal the entry count, or the onboarded column is over a population
// nobody named. The summary prints a warning when it does not; this fails the
// build instead.
func TestOnboardingTotalsPartitionTheCorpus(t *testing.T) {
	root, manifest := onboardCorpus(t)
	_, onboarded := sweepBoth(t, root, manifest)
	tt := onboarded.Totals.Onboarding
	if tt == nil {
		t.Fatal("no onboarding totals")
	}
	if sum := tt.Edited + tt.Already + tt.Unmeasurable; sum != onboarded.Totals.Entries {
		t.Errorf("edited %d + already %d + unmeasurable %d = %d, want %d entries",
			tt.Edited, tt.Already, tt.Unmeasurable, sum, onboarded.Totals.Entries)
	}
	if tt.Cleared != 1 {
		t.Errorf("cleared = %d, want 1 (the effects estate)", tt.Cleared)
	}
	if tt.Worse != 0 {
		t.Errorf("worse = %d, want 0", tt.Worse)
	}
}

// TestDiffRefusesToCompareAcrossOnboardingModes. -diff compares the published
// form on both sides, so a mixed pair compares fine and answers a question
// neither file was asked. The other axes are covered in guards_test.go; this
// is the one this change adds.
func TestDiffRefusesToCompareAcrossOnboardingModes(t *testing.T) {
	before, after := full(), full()
	after.OnboardedForm = true
	err := diffPreconditions("before.json", before, "after.json", after)
	if err == nil {
		t.Fatal("compared a published sweep against an onboarded one and said nothing")
	}
	if !strings.Contains(err.Error(), "onboarded form") {
		t.Errorf("refusal does not name the axis: %v", err)
	}
	// And the same pair on one axis must still compare.
	before.OnboardedForm = true
	if err := diffPreconditions("before.json", before, "after.json", after); err != nil {
		t.Errorf("refused two onboarded sweeps: %v", err)
	}
}

// TestRungsReadsTheForkLadder pins that the summary's verdict column is
// check.ClassifyOnboarding and not a second rule beside it. The data-read
// finding is the case that separates them: check.Report.Blocked counts it,
// the ladder does not, and a table built on Blocked put a class no code
// change removes at the top of a work queue.
func TestRungsReadsTheForkLadder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readable bool
		refusals map[string]int
		want     check.OnboardingClass
	}{
		{"clean", true, nil, check.OnboardingClean},
		{"unreadable", false, nil, check.OnboardingUnreadable},
		{"admissions", true, map[string]int{"unadmitted-type": 3}, check.OnboardingAdmissionsOnly},
		{"data read", true, map[string]int{"Resolves at plan time via a data-source read": 9}, check.OnboardingDataReadEligible},
		{"language", true, map[string]int{"count-index": 1}, check.OnboardingLanguageBlocked},
	} {
		if got := rungs(tc.readable, tc.refusals); got != tc.want {
			t.Errorf("%s: rungs = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPopulationsKeepsTheOriginsApart. estate-plan keeps published
// deployments and terraform-aws-modules examples apart because onboarding an
// example onboards nobody's infrastructure, and a total over both answers
// neither question. A table that folded them would look right.
func TestPopulationsKeepsTheOriginsApart(t *testing.T) {
	r := &run{Entries: []entry{
		// cleared, and still blocked
		{Name: "a", Origin: "published deployment", Blocked: true, Onboarded: &formResult{}},
		{Name: "b", Origin: "published deployment", Blocked: true, Onboarded: &formResult{Blocked: true}},
		// cleared, WORSE, and one no edit could be computed for
		{Name: "c", Origin: "terraform-aws-modules", Blocked: true, Onboarded: &formResult{}},
		{Name: "d", Origin: "terraform-aws-modules", Onboarded: &formResult{Blocked: true}},
		{Name: "e", Origin: "terraform-aws-modules", Blocked: true},
	}}
	rows := populations(r)
	if len(rows) != 2 {
		t.Fatalf("%d row(s), want 2", len(rows))
	}
	want := map[string]populationRow{
		"published deployment":  {Origin: "published deployment", Entries: 2, BlockedPublished: 2, BlockedOnboarded: 1, Cleared: 1},
		"terraform-aws-modules": {Origin: "terraform-aws-modules", Entries: 3, Unmeasurable: 1, BlockedPublished: 2, BlockedOnboarded: 1, Cleared: 1, Worse: 1},
	}
	for _, got := range rows {
		if got != want[got.Origin] {
			t.Errorf("%s:\n got  %+v\n want %+v", got.Origin, got, want[got.Origin])
		}
	}
}
