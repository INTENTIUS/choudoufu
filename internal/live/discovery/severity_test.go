// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// [SeverityForRefusal] is what a report about refusals reads to tell a
// blocker from a note, and live/LIMITATIONS.md rendered every discovery row
// as `error` before it existed. These tests are the reason it cannot answer
// one thing while the diagnostic an operator sees says another.

// TestProblemSummariesAreDistinct: [problemKindForSummary] is
// [problemSummaries] read backwards, and a reverse lookup over a map that is
// not injective would answer at random across parses - Go map order is not
// stable.
func TestProblemSummariesAreDistinct(t *testing.T) {
	seen := make(map[string]ProblemKind, len(problemSummaries))
	for kind, summary := range problemSummaries {
		if other, dup := seen[summary]; dup {
			t.Errorf("%s and %s share the summary %q, so the reverse lookup is not well defined",
				kind, other, summary)
		}
		seen[summary] = kind
	}
}

// TestSeverityForRefusalMatchesTheDiagnostic is the pin that matters: for
// every problem kind, what the document says has to be what the operator
// sees. It goes through problemDiag itself rather than reasoning about it,
// so a future change that gives the diagnostic a severity from somewhere
// else fails here.
func TestSeverityForRefusalMatchesTheDiagnostic(t *testing.T) {
	if len(problemSummaries) == 0 {
		t.Fatal("problemSummaries is empty; this test can see nothing")
	}
	var warnings int
	for kind, summary := range problemSummaries {
		res := &Result{}
		diag := problemDiag(res, Problem{Kind: kind, Detail: "detail."})

		want := tfdiags.Error
		if SeverityForRefusal(summary) == SeverityWarning {
			want = tfdiags.Warning
			warnings++
		}
		if got := diag.Severity(); got != want {
			t.Errorf("%s (%q): diagnostic severity %v, SeverityForRefusal says %v",
				kind, summary, got, SeverityForRefusal(summary))
		}
		if got := diag.Description().Summary; got != summary {
			t.Errorf("%s: diagnostic summary %q, problemSummaries says %q", kind, got, summary)
		}
	}
	if warnings == 0 {
		t.Error("no problem kind is a warning, so this test proved only that everything is an error")
	}
}

// TestSweepGapDiagnosticSeverityMatches: the one diagnostic in this package
// that is not a Problem. It is always a warning - a gap in removal coverage
// is not a wrong plan - and SeverityForRefusal has to say so under the same
// constant the call site uses.
func TestSweepGapDiagnosticSeverityMatches(t *testing.T) {
	if SeverityForRefusal(SummaryIncompleteSweep) != SeverityWarning {
		t.Fatalf("SeverityForRefusal(%q) = %v, want %v",
			SummaryIncompleteSweep, SeverityForRefusal(SummaryIncompleteSweep), SeverityWarning)
	}

	res := &Result{}
	diags := sweepGapDiag(res, SweepGap{
		TypeName: "aws_s3_bucket",
		Reason:   SweepGapListFailed,
		Detail:   "detail.",
	})
	if len(diags) != 1 {
		t.Fatalf("a failed list produced %d diagnostics, want 1", len(diags))
	}
	if got := diags[0].Severity(); got != tfdiags.Warning {
		t.Errorf("sweep gap diagnostic severity %v, want warning", got)
	}
	if got := diags[0].Description().Summary; got != SummaryIncompleteSweep {
		t.Errorf("sweep gap summary %q, want %q", got, SummaryIncompleteSweep)
	}
}

// TestEveryRegisteredRefusalHasAStatedSeverity is the blindness guard.
//
// [SeverityForRefusal] falls through to error for a summary it does not
// recognise, which is right for the caller errors and marker-trouble
// refusals and would silently be wrong for a new warning nobody wired up.
// So every registry entry has to be accounted for here: a problem kind's, or
// the sweep gap's, or on this list of the ones that genuinely stop the run.
// Adding a refusal to the registry breaks this test until someone says which
// it is.
func TestEveryRegisteredRefusalHasAStatedSeverity(t *testing.T) {
	// Refusals that stop the run, stated rather than defaulted: the caller
	// errors (a request missing its estate, configuration or provider) and
	// marker trouble (an address that cannot be carried, two addresses
	// escaping to one value, a resolution naming no declared block). Each
	// was read off its own call site, and every one is built with
	// tfdiags.Error or hcl.DiagError.
	fatal := map[string]bool{
		"Address too long to carry an ownership marker":    true,
		"Invalid estate name":                              true,
		"No configuration to discover against":             true,
		"No provider access":                               true,
		"One marker value for two declared addresses":      true,
		"Resolved resource missing from the configuration": true,
		"Unscoped account reconciliation refused":          true,
		SummaryUnclassifiedProblem:                         true,
	}

	registered := make(map[string]bool)
	for _, r := range Refusals() {
		registered[r.Summary] = true

		switch {
		case summaryIsAProblemKind(r.Summary):
			// Severity comes from ProblemKind.Severity, pinned above.
		case r.Summary == SummaryIncompleteSweep:
			// Pinned above.
		case fatal[r.Summary]:
			if SeverityForRefusal(r.Summary) != SeverityError {
				t.Errorf("%q is listed as fatal but SeverityForRefusal says %v",
					r.Summary, SeverityForRefusal(r.Summary))
			}
		default:
			t.Errorf("registered refusal %q has no stated severity. It is neither a "+
				"ProblemKind's summary nor the sweep gap's, so SeverityForRefusal "+
				"defaults it to error. Say which it is: add it to this test's fatal "+
				"list, or give it a ProblemKind.", r.Summary)
		}
	}

	for summary := range fatal {
		if !registered[summary] {
			t.Errorf("this test lists %q as a fatal refusal, but the registry has no such entry", summary)
		}
	}
}

func summaryIsAProblemKind(summary string) bool {
	_, ok := problemKindForSummary(summary)
	return ok
}
