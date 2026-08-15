// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/terminal"
)

// These tests are issue #161. The report was already honest about unset
// input variables, in a trailer after the verdict, the findings and two
// other paragraphs. Someone assessing compatibility reads the headline and
// stops, so the headline is what has to be right - and it was wrong in the
// direction that makes this fork look worse than it is, for the audiences
// most worth winning, since anyone whose variables come from a pipeline
// runs with none of them set.

func renderLiveCheck(t *testing.T, rep LiveCheckReport) string {
	t.Helper()
	streams, done := terminal.StreamsForTesting(t)
	NewLiveCheck(NewView(streams)).Report(rep)
	return done(t).Stdout()
}

func finding(title string, sites, unsetSites int, refs ...string) LiveCheckFinding {
	return LiveCheckFinding{
		Title:         title,
		Layer:         "identity",
		SiteCount:     sites,
		UnsetVarSites: unsetSites,
		UnsetVarRefs:  refs,
	}
}

// TestVerdictIsInconclusiveWhenEveryRefusalDependsOnAnUnsetVariable is the
// issue's headline case. Every refusal would go away with values supplied,
// so "cannot move under live resource markers yet" is a claim about the
// missing tfvars rather than about the configuration.
func TestVerdictIsInconclusiveWhenEveryRefusalDependsOnAnUnsetVariable(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 2, Instances: 4,
		Findings:                  []LiveCheckFinding{finding("Non-static identity argument", 2, 2, "account_id")},
		UnsetVariables:            []string{"account_id"},
		VariableDependentFindings: 1,
		FullyVariableDependent:    1,
	})

	if !strings.Contains(out, "inconclusive") {
		t.Errorf("the verdict does not say inconclusive, though every refusal depends on an unset "+
			"variable:\n%s", out)
	}
	if strings.Contains(out, "cannot move under live resource markers yet") {
		t.Errorf("the verdict still reads as a refusal of the configuration:\n%s", out)
	}
	if !strings.Contains(out, "var.account_id") {
		t.Errorf("the verdict does not name the variable to supply:\n%s", out)
	}
}

// TestVerdictStaysBlockedWhenARefusalIsReal is the other half, and the one
// that keeps the first from being a way to look good. One genuine refusal
// means the configuration genuinely cannot move, whatever the variables do.
func TestVerdictStaysBlockedWhenARefusalIsReal(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 3, Instances: 1,
		Findings: []LiveCheckFinding{
			finding("Non-static identity argument", 2, 2, "account_id"),
			finding("Identity derived from an impure function", 1, 0),
		},
		UnsetVariables:            []string{"account_id"},
		VariableDependentFindings: 1,
		FullyVariableDependent:    1,
	})

	if strings.Contains(out, "inconclusive") {
		t.Errorf("a configuration with a real refusal was reported as inconclusive:\n%s", out)
	}
	if !strings.Contains(out, "cannot move under live resource markers yet") {
		t.Errorf("the blocked verdict is missing:\n%s", out)
	}
	if !strings.Contains(out, "1 of those refusal(s) depend entirely on an input variable") {
		t.Errorf("the verdict does not say how many refusals may not be real:\n%s", out)
	}
}

// TestOnlyAffectedRefusalsAreMarked keeps the caveat worth reading. Marking
// every refusal when any variable is unset is the same failure as the
// trailer, with more words.
func TestOnlyAffectedRefusalsAreMarked(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 3, Instances: 1,
		Findings: []LiveCheckFinding{
			finding("Non-static identity argument", 2, 2, "account_id"),
			finding("Identity derived from an impure function", 1, 0),
		},
		UnsetVariables:            []string{"account_id"},
		VariableDependentFindings: 1,
		FullyVariableDependent:    1,
	})

	impure := out[strings.Index(out, "Identity derived from an impure function"):]
	if strings.Contains(impure, "May not be real") {
		t.Errorf("the impure-function refusal is marked as variable-dependent; uuid() refuses whatever "+
			"the variables are:\n%s", impure)
	}
	nonstatic := out[strings.Index(out, "Non-static identity argument"):strings.Index(out, "Identity derived from an impure function")]
	if !strings.Contains(nonstatic, "May not be real") {
		t.Errorf("the variable-dependent refusal is not marked:\n%s", nonstatic)
	}
}

// TestPartiallyAffectedRefusalReportsTheSiteCount: a refusal firing in six
// places, two of which read an unset variable, is real in four. The mark has
// to say which, or it overstates the doubt the way the old trailer
// overstated the refusals.
func TestPartiallyAffectedRefusalReportsTheSiteCount(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 6, Instances: 1,
		Findings:                  []LiveCheckFinding{finding("Non-static identity argument", 6, 2, "account_id")},
		UnsetVariables:            []string{"account_id"},
		VariableDependentFindings: 1,
	})

	if !strings.Contains(out, "2 of these site(s) read var.account_id") {
		t.Errorf("a partially-affected refusal does not report how many sites are affected:\n%s", out)
	}
	if strings.Contains(out, "May not be real: every site") {
		t.Errorf("a partially-affected refusal is marked as wholly variable-dependent:\n%s", out)
	}
}

// TestTrailerSaysNoRefusalIsAffectedWhenNoneIs. Unset variables with no
// refusal reading one is a real and common state - the variables are unused
// by anything identity-bearing - and saying "some refusals above may be an
// artifact of that" there is simply false.
func TestTrailerSaysNoRefusalIsAffectedWhenNoneIs(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 1, Instances: 1,
		Findings:       []LiveCheckFinding{finding("Identity derived from an impure function", 1, 0)},
		UnsetVariables: []string{"unused_thing"},
	})

	if !strings.Contains(out, "No refusal above reads one") {
		t.Errorf("the trailer does not say that no refusal is affected:\n%s", out)
	}
	if strings.Contains(out, "may be an artifact") {
		t.Errorf("the trailer still casts doubt on refusals that read no unset variable:\n%s", out)
	}
}

// TestVerdictLeadsWithTheOneEditWhenOnlyTheBackendBlocks is #175's case:
// 11 of 145 real published estates are in exactly this state, and the
// headline is where their reader stops.
func TestVerdictLeadsWithTheOneEditWhenOnlyTheBackendBlocks(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 1, Instances: 4,
		Findings:          []LiveCheckFinding{finding("State backends are not available under live resource markers", 1, 0)},
		OnlyBackendBlocks: true,
	})

	if !strings.Contains(out, "one edit from moving") {
		t.Errorf("the one-edit verdict is missing:\n%s", out)
	}
	if strings.Contains(out, "cannot move under live resource markers yet") {
		t.Errorf("the generic blocked headline still leads a one-edit configuration:\n%s", out)
	}
}

// TestVerdictDoesNotClaimOneEditWhenAnythingElseBlocks keeps the first
// honest: a second refusal of any kind means the edit is not the whole
// distance, and claiming otherwise is the inconclusive-verdict failure
// mode with a different sentence.
func TestVerdictDoesNotClaimOneEditWhenAnythingElseBlocks(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 2, Instances: 1,
		Findings: []LiveCheckFinding{
			finding("State backends are not available under live resource markers", 1, 0),
			finding("Non-static identity argument", 1, 0),
		},
	})

	if strings.Contains(out, "one edit from moving") {
		t.Errorf("a configuration with a non-backend refusal was reported one edit away:\n%s", out)
	}
	if !strings.Contains(out, "cannot move under live resource markers yet") {
		t.Errorf("the blocked verdict is missing:\n%s", out)
	}
}

// TestOneEditYieldsToTheVariableCaveat: a backend refusal never reads a
// variable, but the flag is computed elsewhere - if both ever arrive set,
// the variable caveat must win, because "one edit" would then be a claim
// the evidence does not support.
func TestOneEditYieldsToTheVariableCaveat(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 2, Instances: 1,
		Findings: []LiveCheckFinding{
			finding("State backends are not available under live resource markers", 2, 2, "region"),
		},
		UnsetVariables:            []string{"region"},
		VariableDependentFindings: 1,
		FullyVariableDependent:    1,
		OnlyBackendBlocks:         true,
	})

	if strings.Contains(out, "one edit from moving") {
		t.Errorf("the one-edit claim overrode the unset-variable caveat:\n%s", out)
	}
}
