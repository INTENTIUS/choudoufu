// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package harness holds the two registries this repository's numbers live
// in: what it is driving down (the burndown), and what it believes while it
// does (the assumptions).
//
// It exists because both were prose. Six downward ratchets sat as
// hand-written consts in three unrelated test files, each with its own
// bespoke reader and its own paragraph explaining what it was measured
// against; the load-bearing claims - which refusal IDs are non-blocking,
// which analysis layers a report covers, how many credential exclusions are
// sanctioned - sat in CLAUDE.md and a handoff document with nothing that
// failed when they stopped being true. Three separate incidents on
// 2026-08-16 traced back to that shape: a defect count that did not
// reproduce, a site total measured on a branch and copied into three
// committed files, and a corpus that measures a fraction of 58 entries'
// surface without saying so.
//
// Two properties those incidents demand, and which this package enforces
// rather than documents:
//
// A ratchet must pin its denominator. Every quantity here is a count
// against some roster, and shrinking the roster is always the cheaper way
// to make the count fall. [Denominator] carries the roster and a floor, and
// the floor is checked before the bound is.
//
// A ratchet must not measure itself. A count derived from an artifact and
// compared against that same artifact agrees with itself no matter what it
// says. Every [Entry] names what it measures and what external thing it is
// held against, and the two must differ.
//
// Nothing here runs a generator, launches a provider, or touches the
// network. Every measurement reads a committed artifact or an in-process
// Go roster, so the whole registry sweeps in well under a second and can
// sit in the fast test tier.
package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Direction is which way a burndown quantity is allowed to move.
type Direction string

const (
	// AtMost is a downward ratchet: the measurement may fall freely and
	// may never exceed [Entry.Bound]. Every quantity in the registry today
	// is one of these; the type exists so an upward drive (a coverage
	// count, say) has somewhere to go without being written as a negated
	// ceiling.
	AtMost Direction = "at most"

	// AtLeast is the mirror, for a quantity being driven up.
	AtLeast Direction = "at least"
)

// Reading is one measurement's result.
type Reading struct {
	// Value is the number the bound applies to.
	Value int

	// Population is the members the value counts, when they are
	// enumerable. A failure message quotes a sample of these, which is the
	// difference between "621, up from 620" and knowing which type moved.
	Population []string

	// Note is anything the measurement learned that the value does not
	// carry - a cross-check that agreed, a component count.
	Note string
}

// Denominator is the roster a quantity is counted against, plus the floor
// that stops the roster itself from being the cheaper edit.
//
// live/admission_coverage_test.go's universeFloor is the original: the
// unreached-type count is a difference against the provider's own type
// roster, so deleting rows from that roster lowers it exactly as
// effectively as admitting a type does. Every migrated ratchet needs its
// equivalent or an explicit statement that it has none.
type Denominator struct {
	// Name is the roster, as a reader would go looking for it.
	Name string

	// Floor is the smallest the roster may be. Below it, the measurement
	// is not reported at all: a shrunken roster makes the numerator
	// meaningless rather than merely smaller.
	Floor int

	// Why says what a shrink would look like and why the floor is where it
	// is, so a future reader can tell a deliberate bump from a convenient
	// one.
	Why string

	// Measure reads the roster's size.
	Measure func(*Repo) (int, error)
}

// Entry is one quantity the project is driving somewhere.
//
// The measurement is a function rather than a recorded number on purpose.
// A recorded number and the thing that produces it drift, and this
// repository has three separate incidents where they did.
type Entry struct {
	// ID is stable and is what a failure message, the rendered document
	// and any future tooling agree on.
	ID string

	// Claim is the quantity in one sentence, in the form a reader who has
	// never seen the code can check: "N provider types are in neither the
	// admission table nor a veto ledger".
	Claim string

	// Unit is what the number counts: "provider types", "rulings".
	Unit string

	// Bound is the current ceiling (or floor, for [AtLeast]).
	Bound int

	// Direction is which way the bound holds.
	Direction Direction

	// Measured names the roster or artifact under test - the thing whose
	// contents the number is a property of.
	Measured string

	// Against names the external source of truth the measurement is held
	// to, and it must differ from Measured. A quantity read out of an
	// artifact and checked against that same artifact is self-agreement,
	// and the identityattr test was passing for exactly that reason.
	Against string

	// AgainstWhy says why Against is external to Measured: which different
	// producer it comes from, and what edit to Measured cannot reach it.
	AgainstWhy string

	// Instrument is how the number is obtained, in the terms
	// .claude/skills/measuring-choudoufu asks for: which reader, run
	// where, with what available.
	Instrument string

	// BlindSpots are what Instrument cannot see. tools/refusal-probe's
	// default mode cannot see the stamp layer, any rule that returns false
	// without provider schemas, or a non-AWS estate; a reader who does not
	// know that will read its zeroes as evidence.
	BlindSpots []string

	// Denominator pins the roster, or is nil - in which case
	// NoDenominator must say why there is nothing to game.
	Denominator *Denominator

	// NoDenominator is the explicit statement that this quantity has no
	// roster a shrink could exploit. Exactly one of this and Denominator
	// is set; [ValidateBurndown] enforces that, because "I forgot" and
	// "there is none" look identical in a struct literal otherwise.
	NoDenominator string

	// Tracker is the issue this quantity belongs to, or the reason it has
	// no tracker home.
	Tracker string

	// History is what moved the bound, newest last. This is the one field
	// carrying prose numbers, and they are history rather than assertions:
	// the live number always comes from Measure.
	History []string

	// Measure computes the current value.
	Measure func(*Repo) (Reading, error)
}

// Result is one entry measured.
type Result struct {
	Entry            Entry
	Reading          Reading
	DenominatorValue int

	// Err is set when the measurement could not be taken at all - an
	// artifact that would not parse, a summary that contradicts its own
	// body. It is not a bound failure and must never be rendered as one: a
	// document that printed a zero here would be the blind-scanner failure
	// this repository has already had once.
	Err error

	// Breach is set when the bound does not hold, or when the denominator
	// fell below its floor. Both are failures; they are different failures
	// and the message says which.
	Breach error

	// Slack is Bound-Value for an [AtMost] entry: positive means the
	// constant is stale and should be lowered. Staleness is not a failure -
	// a drop is the point - but it is worth saying out loud, because
	// annotationCountRatchetMax sat two above its own ledger and had
	// stopped bounding anything.
	Slack int
}

// measure runs one entry against repo. Callers go through [MeasureAll], so
// the registry is always swept in one order and nothing can quietly measure
// a subset.
func (e Entry) measure(repo *Repo) Result {
	res := Result{Entry: e, DenominatorValue: -1}

	if e.Denominator != nil {
		n, err := e.Denominator.Measure(repo)
		if err != nil {
			res.Err = fmt.Errorf("%s: reading the denominator %q: %w", e.ID, e.Denominator.Name, err)
			return res
		}
		res.DenominatorValue = n
		if n < e.Denominator.Floor {
			res.Breach = fmt.Errorf(
				"%s: its denominator %q is %d, below the floor of %d. %s "+
					"The bound is not checked: a count against a roster that shrank is not a smaller "+
					"problem, it is a different measurement",
				e.ID, e.Denominator.Name, n, e.Denominator.Floor, e.Denominator.Why)
			return res
		}
	}

	r, err := e.Measure(repo)
	if err != nil {
		res.Err = fmt.Errorf("%s: measuring: %w", e.ID, err)
		return res
	}
	res.Reading = r

	switch e.Direction {
	case AtMost:
		res.Slack = e.Bound - r.Value
		if r.Value > e.Bound {
			res.Breach = fmt.Errorf(
				"%s: %d %s, above the bound of %d. Claim: %s. Measured on %s, held against %s. %s%s",
				e.ID, r.Value, e.Unit, e.Bound, e.Claim, e.Measured, e.Against,
				denominatorPhrase(res), samplePhrase(r.Population))
		}
	case AtLeast:
		res.Slack = r.Value - e.Bound
		if r.Value < e.Bound {
			res.Breach = fmt.Errorf(
				"%s: %d %s, below the bound of %d. Claim: %s. Measured on %s, held against %s. %s%s",
				e.ID, r.Value, e.Unit, e.Bound, e.Claim, e.Measured, e.Against,
				denominatorPhrase(res), samplePhrase(r.Population))
		}
	default:
		res.Err = fmt.Errorf("%s: unknown direction %q", e.ID, e.Direction)
	}
	return res
}

func denominatorPhrase(res Result) string {
	if res.Entry.Denominator == nil {
		return "No denominator: " + res.Entry.NoDenominator + " "
	}
	return fmt.Sprintf("Denominator %q held at %d (floor %d). ",
		res.Entry.Denominator.Name, res.DenominatorValue, res.Entry.Denominator.Floor)
}

func samplePhrase(pop []string) string {
	if len(pop) == 0 {
		return ""
	}
	sample := pop
	suffix := ""
	if len(sample) > 20 {
		sample = sample[:20]
		suffix = fmt.Sprintf(" (first 20 of %d)", len(pop))
	}
	return fmt.Sprintf("Members: %v%s.", sample, suffix)
}

// MeasureAll runs every entry in the registry, in registry order.
func MeasureAll(repo *Repo, entries []Entry) []Result {
	out := make([]Result, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.measure(repo))
	}
	return out
}

// ValidateBurndown checks the registry's own shape. Everything it rejects
// is a way an entry could look complete while proving nothing.
func ValidateBurndown(entries []Entry) []error {
	var errs []error
	seen := map[string]bool{}
	prev := ""
	for i, e := range entries {
		where := fmt.Sprintf("entry %d (%q)", i, e.ID)
		switch {
		case e.ID == "":
			errs = append(errs, fmt.Errorf("%s: no ID", where))
		case seen[e.ID]:
			errs = append(errs, fmt.Errorf("%s: duplicate ID", where))
		case e.ID < prev:
			errs = append(errs, fmt.Errorf("%s: out of order, %q precedes it", where, prev))
		}
		seen[e.ID] = true
		prev = e.ID

		for _, f := range []struct{ name, val string }{
			{"Claim", e.Claim},
			{"Unit", e.Unit},
			{"Measured", e.Measured},
			{"Against", e.Against},
			{"AgainstWhy", e.AgainstWhy},
			{"Instrument", e.Instrument},
			{"Tracker", e.Tracker},
		} {
			if f.val == "" {
				errs = append(errs, fmt.Errorf("%s: %s is empty", where, f.name))
			}
		}
		if e.Measure == nil {
			errs = append(errs, fmt.Errorf("%s: no Measure function; a recorded number is the thing this registry replaces", where))
		}
		if e.Direction != AtMost && e.Direction != AtLeast {
			errs = append(errs, fmt.Errorf("%s: Direction is %q, not %q or %q", where, e.Direction, AtMost, AtLeast))
		}
		if e.Against != "" && e.Against == e.Measured {
			errs = append(errs, fmt.Errorf(
				"%s: Against and Measured are the same thing (%q). A count read out of an artifact and "+
					"checked against that artifact agrees with itself whatever it says", where, e.Measured))
		}
		if len(e.BlindSpots) == 0 {
			errs = append(errs, fmt.Errorf(
				"%s: no BlindSpots. Every instrument here has some; recording none means they were not "+
					"looked for. Say \"none known, and here is why\" if that is the finding", where))
		}
		switch {
		case e.Denominator == nil && e.NoDenominator == "":
			errs = append(errs, fmt.Errorf(
				"%s: neither a Denominator nor a NoDenominator note. This count is against some roster, "+
					"and shrinking that roster is always the cheaper way to make it fall - pin it, or say "+
					"in writing that there is nothing to shrink", where))
		case e.Denominator != nil && e.NoDenominator != "":
			errs = append(errs, fmt.Errorf("%s: both a Denominator and a NoDenominator note; exactly one holds", where))
		case e.Denominator != nil:
			d := e.Denominator
			if d.Name == "" || d.Why == "" || d.Measure == nil {
				errs = append(errs, fmt.Errorf("%s: Denominator needs a Name, a Why and a Measure", where))
			}
			if d.Floor <= 0 {
				errs = append(errs, fmt.Errorf("%s: Denominator floor is %d; a floor of zero pins nothing", where, d.Floor))
			}
			if d.Name == e.Measured {
				errs = append(errs, fmt.Errorf(
					"%s: the denominator is the thing being measured (%q), so it cannot bound it", where, d.Name))
			}
		}
	}
	return errs
}

// Repo is a checkout, plus a one-shot cache for each artifact a measurement
// reads. Several entries read live/rowgen-convergence.json and
// live/survey-full.json; parsing each once keeps the whole sweep well under
// a second.
type Repo struct {
	Root  string
	cache map[string]any
}

// Open finds the checkout root by walking up from dir looking for go.mod.
func Open(dir string) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for cur := abs; ; {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return &Repo{Root: cur, cache: map[string]any{}}, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil, fmt.Errorf("no go.mod at or above %s", abs)
		}
		cur = parent
	}
}

// Path resolves a repo-relative path.
func (r *Repo) Path(rel string) string { return filepath.Join(r.Root, filepath.FromSlash(rel)) }

// SortedKeys is the small helper every measurement's Population needs.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
