// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package harness

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func testRepo(t *testing.T) *Repo {
	t.Helper()
	r, err := Open(".")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r
}

// TestBurndownRegistryIsWellFormed runs the registry's own shape checks over
// the shipped entries. Everything ValidateBurndown rejects is a way an entry
// could look complete and prove nothing.
func TestBurndownRegistryIsWellFormed(t *testing.T) {
	for _, err := range ValidateBurndown(Burndown()) {
		t.Error(err)
	}
}

// TestAssumptionsRegistryIsWellFormed is the same for the other half.
func TestAssumptionsRegistryIsWellFormed(t *testing.T) {
	for _, err := range ValidateAssumptions(Assumptions()) {
		t.Error(err)
	}
}

// TestBurndownBoundsHold is the ratchet sweep: every quantity measured, every
// denominator floor checked before its bound, nothing quoted from a recorded
// constant.
//
// A measurement below its bound is not a failure - a drop is the whole point -
// but it is logged, because a bound that has stopped bounding anything is how
// tools/row-gen's annotation ledger drifted twice in two days without anybody
// noticing.
func TestBurndownBoundsHold(t *testing.T) {
	repo := testRepo(t)
	for _, res := range MeasureAll(repo, Burndown()) {
		if res.Err != nil {
			t.Errorf("%s could not be measured, which is not the same as passing: %v", res.Entry.ID, res.Err)
			continue
		}
		if res.Breach != nil {
			t.Error(res.Breach)
			continue
		}
		if res.Slack > 0 {
			t.Logf("%s: %d %s, %d below its bound of %d - lower the bound to the measurement",
				res.Entry.ID, res.Reading.Value, res.Entry.Unit, res.Slack, res.Entry.Bound)
		}
	}
}

// TestAssumptionsHold runs every seeded claim's check.
func TestAssumptionsHold(t *testing.T) {
	repo := testRepo(t)
	for _, a := range Assumptions() {
		detail, err := a.Check(repo)
		if err != nil {
			t.Errorf("%s no longer holds: %v\nClaim: %s\nIf this stops being true: %s",
				a.ID, err, a.Claim, a.Consequence)
			continue
		}
		t.Logf("%s: %s", a.ID, detail)
	}
}

// TestHarnessDocIsCurrent is the drift half: the committed document's
// generated spans must be what a fresh render produces.
//
// It renders in process rather than shelling out to the generator, so the
// whole check costs a JSON parse and no build.
func TestHarnessDocIsCurrent(t *testing.T) {
	repo := testRepo(t)
	committed, err := os.ReadFile(repo.Path(DocRel))
	if err != nil {
		t.Fatalf("reading %s: %v", DocRel, err)
	}
	fresh, err := Render(repo, string(committed))
	if err != nil {
		t.Fatalf("rendering %s: %v", DocRel, err)
	}
	if fresh == string(committed) {
		return
	}
	m := Markers()
	for _, span := range []string{SpanBurndown, SpanAssumptions} {
		got, err := m.Content(DocRel, string(committed), span)
		if err != nil {
			t.Fatalf("reading the %s span: %v", span, err)
		}
		want, err := m.Content(DocRel, fresh, span)
		if err != nil {
			t.Fatalf("reading the fresh %s span: %v", span, err)
		}
		if got != want {
			t.Errorf("%s's %q span is stale. Run `just harness`.\nFirst difference:\n%s",
				DocRel, span, firstDiff(got, want))
		}
	}
}

func firstDiff(got, want string) string {
	g := strings.Split(got, "\n")
	w := strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		gl, wl := "", ""
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "  committed: " + gl + "\n  fresh:     " + wl
		}
	}
	return "(no line differs; the difference is trailing whitespace)"
}

var digitRun = regexp.MustCompile(`[0-9]+`)

// TestHandWrittenProseCarriesNoFigures holds the document to its own promise.
//
// The whole point of rendering the registries is that no figure in this file
// is typed by hand, so a figure outside a generated span is one nothing
// recomputes - which is precisely the class of number that has been wrong in
// this repository every time somebody went looking. Issue references are
// allowed because they are names, not measurements.
func TestHandWrittenProseCarriesNoFigures(t *testing.T) {
	repo := testRepo(t)
	raw, err := os.ReadFile(repo.Path(DocRel))
	if err != nil {
		t.Fatalf("reading %s: %v", DocRel, err)
	}
	md := string(raw)
	m := Markers()
	for _, span := range []string{SpanBurndown, SpanAssumptions} {
		body, err := m.Content(DocRel, md, span)
		if err != nil {
			t.Fatalf("reading the %s span: %v", span, err)
		}
		md = strings.Replace(md, body, "\n", 1)
	}
	for i, line := range strings.Split(md, "\n") {
		for _, loc := range digitRun.FindAllStringIndex(line, -1) {
			if loc[0] > 0 && line[loc[0]-1] == '#' {
				continue // an issue reference
			}
			t.Errorf("%s:%d: hand-written figure %q outside a generated span: %q\n"+
				"Every number in this document comes from a measurement function. If this one should "+
				"too, add it to a registry entry; if it is genuinely prose, rewrite it in words",
				DocRel, i+1, line[loc[0]:loc[1]], strings.TrimSpace(line))
		}
	}
}

// TestShapeChecksRejectAnEntryThatProvesNothing is the adversarial half:
// each gate is shown to bite, on an entry built to defeat it.
//
// A validator nobody has tried to get past is a validator that might match
// nothing. Every case here is a mistake somebody could plausibly make in a
// struct literal, and the point is that "I forgot" and "there is genuinely
// none" must not look the same.
func TestShapeChecksRejectAnEntryThatProvesNothing(t *testing.T) {
	base := func() Entry {
		return Entry{
			ID:            "z-synthetic",
			Claim:         "a claim",
			Unit:          "things",
			Direction:     AtMost,
			Measured:      "roster A",
			Against:       "roster B",
			AgainstWhy:    "B is written by something else",
			Instrument:    "an in-process map",
			BlindSpots:    []string{"everything else"},
			NoDenominator: "nothing to shrink",
			Tracker:       "#0",
			Measure:       func(*Repo) (Reading, error) { return Reading{}, nil },
		}
	}
	cases := []struct {
		name    string
		mutate  func(*Entry)
		wantSub string
	}{
		{"no denominator and no note", func(e *Entry) { e.NoDenominator = "" }, "neither a Denominator nor a NoDenominator"},
		{"both a denominator and a note", func(e *Entry) {
			e.Denominator = &Denominator{Name: "roster B", Floor: 1, Why: "why", Measure: func(*Repo) (int, error) { return 1, nil }}
		}, "both a Denominator and a NoDenominator"},
		{"held against itself", func(e *Entry) { e.Against = e.Measured }, "agrees with itself"},
		{"no external source at all", func(e *Entry) { e.Against = "" }, "Against is empty"},
		{"no blind spots recorded", func(e *Entry) { e.BlindSpots = nil }, "no BlindSpots"},
		{"a recorded number instead of a measurement", func(e *Entry) { e.Measure = nil }, "no Measure function"},
		{"a denominator with a floor of zero", func(e *Entry) {
			e.NoDenominator = ""
			e.Denominator = &Denominator{Name: "roster B", Floor: 0, Why: "why", Measure: func(*Repo) (int, error) { return 1, nil }}
		}, "floor of zero pins nothing"},
		{"a denominator that is the thing measured", func(e *Entry) {
			e.NoDenominator = ""
			e.Denominator = &Denominator{Name: "roster A", Floor: 1, Why: "why", Measure: func(*Repo) (int, error) { return 1, nil }}
		}, "cannot bound it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base()
			tc.mutate(&e)
			errs := ValidateBurndown([]Entry{e})
			var joined []string
			for _, err := range errs {
				joined = append(joined, err.Error())
			}
			all := strings.Join(joined, "\n")
			if !strings.Contains(all, tc.wantSub) {
				t.Errorf("ValidateBurndown accepted an entry it should reject, or rejected it for the wrong reason.\nwant a message containing %q\ngot:\n%s",
					tc.wantSub, all)
			}
		})
	}
}

// TestDenominatorFloorStopsTheMeasurement proves the floor is checked before
// the bound and not merely alongside it.
//
// The failure this prevents is subtle: a roster that shrank makes the
// numerator agree with a smaller bound, so a harness that reported the bound
// as held would be reporting progress caused by deletion. Below the floor the
// measurement is not taken at all.
func TestDenominatorFloorStopsTheMeasurement(t *testing.T) {
	measured := false
	e := Entry{
		ID: "z-floor", Claim: "c", Unit: "u", Direction: AtMost, Bound: 0,
		Measured: "A", Against: "B", AgainstWhy: "w", Instrument: "i",
		BlindSpots: []string{"b"}, Tracker: "#0",
		Denominator: &Denominator{
			Name: "B", Floor: 100, Why: "a shrink would be the cheaper edit",
			Measure: func(*Repo) (int, error) { return 99, nil },
		},
		Measure: func(*Repo) (Reading, error) {
			measured = true
			return Reading{Value: 0}, nil
		},
	}
	res := MeasureAll(testRepo(t), []Entry{e})[0]
	if res.Breach == nil {
		t.Fatal("a denominator below its floor did not breach")
	}
	if !strings.Contains(res.Breach.Error(), "below the floor") {
		t.Errorf("the breach does not name the floor: %v", res.Breach)
	}
	if measured {
		t.Error("the bound was checked against a roster below its floor; a count against a roster that " +
			"shrank is a different measurement, not a smaller number")
	}
}

// TestEveryBurndownEntryIsInTheDocument stops an entry being added to the
// registry while the reader-facing half quietly stays behind.
func TestEveryBurndownEntryIsInTheDocument(t *testing.T) {
	repo := testRepo(t)
	raw, err := os.ReadFile(repo.Path(DocRel))
	if err != nil {
		t.Fatalf("reading %s: %v", DocRel, err)
	}
	md := string(raw)
	for _, e := range Burndown() {
		if !strings.Contains(md, "### `"+e.ID+"`") {
			t.Errorf("%s has no section for burndown entry %q; run `just harness`", DocRel, e.ID)
		}
	}
	for _, a := range Assumptions() {
		if !strings.Contains(md, "### `"+a.ID+"`") {
			t.Errorf("%s has no section for assumption %q; run `just harness`", DocRel, a.ID)
		}
	}
}
