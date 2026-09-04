// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"encoding/json"
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

// TestVerdictNamesTheBackendEditWhenItIsAllThatRemains is #175's case,
// revised by #215 after #214 demoted state-backend from a fatal finding to
// a warning: the rule can no longer block a configuration at all, so this
// is now the clean-but-one-warning estate rather than the one-edit-away
// blocked one. live/e2e/limits/backend-block is exactly this shape - a real
// live-check run against it is Blocked: false with a single state-backend
// warning, which is what this constructs directly.
func TestVerdictNamesTheBackendEditWhenItIsAllThatRemains(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: false, Instances: 4,
		Warnings:           []LiveCheckCount{{Label: "State backends are not available under live resource markers", Count: 1}},
		OnlyBackendRemains: true,
	})

	if !strings.Contains(out, "already moves under live resource markers") {
		t.Errorf("the backend-remains verdict is missing:\n%s", out)
	}
	if strings.Contains(out, "Nothing in . is refused") {
		t.Errorf("the generic clean headline still leads a backend-only estate:\n%s", out)
	}
	if !strings.Contains(out, "recommended edit") {
		t.Errorf("the verdict does not say deleting the block is still recommended:\n%s", out)
	}
}

// TestVerdictDoesNotClaimBackendRemainsWhenAnotherWarningJoinsIt keeps the
// first honest: a second warning of any kind means the backend block is not
// the only remaining item, so the generic clean verdict is the accurate
// one.
func TestVerdictDoesNotClaimBackendRemainsWhenAnotherWarningJoinsIt(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: false, Instances: 1,
		Warnings: []LiveCheckCount{
			{Label: "State backends are not available under live resource markers", Count: 1},
			{Label: "Some other non-fatal diagnostic", Count: 1},
		},
	})

	if strings.Contains(out, "already moves under live resource markers") {
		t.Errorf("a configuration with a non-backend warning was reported as only-backend-remains:\n%s", out)
	}
	if !strings.Contains(out, "Nothing in . is refused") {
		t.Errorf("the generic clean verdict is missing:\n%s", out)
	}
}

// TestBlockedVerdictNeverClaimsBackendRemains guards the view's own
// invariant directly rather than trusting the command package to uphold it:
// state-backend can no longer reach Findings, so a real Analyze() output can
// never set both Blocked and OnlyBackendRemains, but the view must not rely
// on that alone. A blocked configuration has to read as blocked, never as a
// clean "already moves" claim that contradicts the refusal printed right
// below it.
func TestBlockedVerdictNeverClaimsBackendRemains(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 1, Instances: 1,
		Findings:           []LiveCheckFinding{finding("Non-static identity argument", 1, 0)},
		OnlyBackendRemains: true,
	})

	if strings.Contains(out, "already moves under live resource markers") {
		t.Errorf("a blocked configuration claimed to already move under live resource markers:\n%s", out)
	}
	if !strings.Contains(out, "cannot move under live resource markers yet") {
		t.Errorf("the blocked verdict is missing:\n%s", out)
	}
}

// The stage lists next. All three are printed from three slices, and one of
// them can go empty: GitHub issue #261 asked whether the discovery stage
// still belongs in Unchecked, and was closed partly on the argument that
// emptying that list would make this report overstate the run. Part of that
// argument was this renderer, which printed "Not checked: %s." with no guard
// at all - correct only for as long as the list stayed non-empty, and
// asserted on by nothing.

// TestNotCheckedNamesTheStagesWhenThereAreAny is the ordinary case, pinned so
// that the empty-list branch below cannot be satisfied by dropping the line
// altogether.
func TestNotCheckedNamesTheStagesWhenThereAreAny(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Instances: 1,
		Checked:   []string{"lint", "identity"},
		Partial:   []string{"projection"},
		Unchecked: []string{"discovery"},
	})

	if !strings.Contains(out, "Not checked: discovery.") {
		t.Errorf("the report does not name the stage nobody looked at:\n%s", out)
	}
	if !strings.Contains(out, "Partly checked: projection.") {
		t.Errorf("the report does not name the partly checked stage:\n%s", out)
	}
	if !strings.Contains(out, "not a promise that an apply succeeds") {
		t.Errorf("the caveat is missing:\n%s", out)
	}
}

// TestNotCheckedLineIsNotPrintedWhenNothingIsUnchecked is the branch that had
// no reader. An empty list rendered "Not checked: ." - a sentence naming no
// stage, which reads as a formatting bug and tells a user nothing.
//
// The caveat has to survive it. A run with no wholly-unchecked stage still
// makes no cloud calls and still has partly-checked stages whose remaining
// refusals need one, so "a clean result is not a promise that an apply
// succeeds" is true either way and is the part that matters.
func TestNotCheckedLineIsNotPrintedWhenNothingIsUnchecked(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Instances: 1,
		Checked:   []string{"lint", "identity", "dataread", "stamp", "discovery"},
		Partial:   []string{"projection"},
		Unchecked: nil,
	})

	if strings.Contains(out, "Not checked:") {
		t.Errorf("the report still prints a \"Not checked\" line with nothing to name:\n%s", out)
	}
	if !strings.Contains(out, "not a promise that an apply succeeds") {
		t.Errorf("the caveat went away with the stage list; it is true whether or not a stage is wholly unchecked:\n%s", out)
	}
	if !strings.Contains(out, "makes no cloud calls") {
		t.Errorf("the report no longer says it made no cloud calls:\n%s", out)
	}
	if !strings.Contains(out, "Partly checked: projection.") {
		t.Errorf("the partly checked stage went missing:\n%s", out)
	}
}

// TestSourcelessSiteRendersAsNothingRatherThanABlankLine covers the shape
// every projection finding has today. internal/live/projection raises both of
// the refusals internal/live/check computes offline with tfdiags.Sourceless,
// so the site carries no file, no line and no address, and the example loop
// printed four spaces and a newline for it.
func TestSourcelessSiteRendersAsNothingRatherThanABlankLine(t *testing.T) {
	out := renderLiveCheck(t, LiveCheckReport{
		Dir: ".", Blocked: true, Sites: 1, Instances: 1,
		Findings: []LiveCheckFinding{{
			Title:     "Empty import identity",
			Layer:     "projection",
			SiteCount: 1,
			Examples:  []LiveCheckSite{{}},
			Remedy:    "A resource resolved to an import identity with no content, which no provider can import.",
			DocsRef:   `live/LIMITATIONS.md, "Empty import identity"`,
		}},
		Checked:   []string{"lint"},
		Partial:   []string{"projection"},
		Unchecked: []string{"discovery"},
	})

	for i, line := range strings.Split(out, "\n") {
		if line != "" && strings.TrimSpace(line) == "" {
			t.Errorf("line %d is whitespace only (%q); a site with no position and no address must print nothing:\n%s", i+1, line, out)
		}
	}
	if !strings.Contains(out, "Empty import identity  (1 site(s), projection)") {
		t.Errorf("the finding's own heading is missing, so the site count is no longer reported:\n%s", out)
	}
	if !strings.Contains(out, "which no provider can import") {
		t.Errorf("the remedy is what carries the content for a sourceless finding, and it is missing:\n%s", out)
	}
}

// GitHub issue #790: "choudoufu live-check -json" prints the declared
// roster instead of the prose above. These tests exercise [LiveCheckJSON]
// directly, the same way the tests above exercise [LiveCheckHuman] - the
// analysis package (internal/live/check) decides what is true, this
// package only decides how it reads, in either format.

func renderLiveCheckJSON(t *testing.T, rep LiveCheckReport) string {
	t.Helper()
	streams, done := terminal.StreamsForTesting(t)
	NewLiveCheckJSON(NewView(streams)).Report(rep)
	return done(t).Stdout()
}

func decodeLiveCheckDocument(t *testing.T, out string) liveCheckDocument {
	t.Helper()
	var doc liveCheckDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %s\n%s", err, out)
	}
	return doc
}

// TestJSONReportsTheDeclaredRoster is #790's own shape: every instance's
// address, type and rung, and a refused one's rule and reason - the same
// fields the issue's Ask names verbatim.
func TestJSONReportsTheDeclaredRoster(t *testing.T) {
	out := renderLiveCheckJSON(t, LiveCheckReport{
		Dir: ".", Blocked: true, Instances: 1, Sites: 1,
		InstanceRoster: []LiveCheckInstance{
			{Address: "aws_s3_bucket.data", Type: "aws_s3_bucket", Rung: "tag-governable"},
			{Address: "aws_iam_role_policy.inline", Type: "aws_iam_role_policy", Rung: "declaration-carried",
				Refused: true, Rule: "Non-static identity argument", Reason: "reads a value this run cannot prove statically"},
		},
		References: []LiveCheckReference{
			{From: "data.aws_vpc.network", Estate: "network", Address: "aws_vpc.main", ReadBy: []string{"aws_subnet.app"}},
		},
		Checked:   []string{"lint", "identity"},
		Unchecked: []string{"discovery"},
	})

	doc := decodeLiveCheckDocument(t, out)
	if len(doc.Instances) != 2 {
		t.Fatalf("got %d instances, want 2: %+v", len(doc.Instances), doc.Instances)
	}
	resolved, refused := doc.Instances[0], doc.Instances[1]
	if resolved.Address != "aws_s3_bucket.data" || resolved.Rung != "tag-governable" || resolved.Refused {
		t.Errorf("resolved instance = %+v, want the tag-governable bucket, unrefused", resolved)
	}
	if !refused.Refused || refused.Rule != "Non-static identity argument" || refused.Reason == "" {
		t.Errorf("refused instance = %+v, want Refused with a rule and a reason", refused)
	}

	if len(doc.References) != 1 {
		t.Fatalf("got %d references, want 1: %+v", len(doc.References), doc.References)
	}
	ref := doc.References[0]
	if ref.From != "data.aws_vpc.network" || ref.Estate != "network" || ref.Address != "aws_vpc.main" {
		t.Errorf("reference = %+v, want the network/aws_vpc.main edge", ref)
	}
	if len(ref.ReadBy) != 1 || ref.ReadBy[0] != "aws_subnet.app" {
		t.Errorf("reference.ReadBy = %v, want [\"aws_subnet.app\"]", ref.ReadBy)
	}
}

// TestJSONExitCodeMatchesBlocked: #790 asks for "the verdict and exit
// code" in the document, and it has to be the same verdict Run() itself
// acts on (report.Blocked()), not a second copy that could drift from it.
func TestJSONExitCodeMatchesBlocked(t *testing.T) {
	blocked := decodeLiveCheckDocument(t, renderLiveCheckJSON(t, LiveCheckReport{Dir: ".", Blocked: true}))
	if !blocked.Blocked || blocked.ExitCode != 1 {
		t.Errorf("blocked report: Blocked=%v ExitCode=%d, want true/1", blocked.Blocked, blocked.ExitCode)
	}

	clean := decodeLiveCheckDocument(t, renderLiveCheckJSON(t, LiveCheckReport{Dir: ".", Blocked: false}))
	if clean.Blocked || clean.ExitCode != 0 {
		t.Errorf("clean report: Blocked=%v ExitCode=%d, want false/0", clean.Blocked, clean.ExitCode)
	}
}

// TestJSONInstancesAndReferencesAreNeverNull: encoding/json renders a nil
// slice as `null`, and a roster reading "null" rather than "[]" for a
// clean, reference-free directory would make a caller like behold check for
// a case that is not actually different from zero entries.
func TestJSONInstancesAndReferencesAreNeverNull(t *testing.T) {
	out := renderLiveCheckJSON(t, LiveCheckReport{Dir: ".", Blocked: false})

	if strings.Contains(out, `"instances": null`) {
		t.Errorf("instances rendered as null instead of []:\n%s", out)
	}
	if strings.Contains(out, `"references": null`) {
		t.Errorf("references rendered as null instead of []:\n%s", out)
	}
	doc := decodeLiveCheckDocument(t, out)
	if doc.Instances == nil || doc.References == nil {
		t.Errorf("decoded document has a nil slice: instances=%v references=%v", doc.Instances, doc.References)
	}
}

// TestJSONEstateIsOmittedWhenUnset: most directories live-check runs
// against declare no live block at all (it runs with no live block by
// design), and the document must not print an empty estate name as though
// one were declared.
func TestJSONEstateIsOmittedWhenUnset(t *testing.T) {
	out := renderLiveCheckJSON(t, LiveCheckReport{Dir: ".", Blocked: false})
	if strings.Contains(out, `"estate"`) {
		t.Errorf("estate key printed with no live block present:\n%s", out)
	}
}

// TestJSONCarriesTheSameCheckedStagesAsText is #790's "Done when": "the
// text and JSON agree on every count." Checked/Partial/Unchecked are
// exactly the strings [LiveCheckHuman.Report] prints, including Partial's
// own embedded refusal counts, because [liveCheckDocument] reuses the same
// []string fields rather than re-deriving them - this test is what would
// catch the two falling out of sync if that stopped being true.
func TestJSONCarriesTheSameCheckedStagesAsText(t *testing.T) {
	rep := LiveCheckReport{
		Dir: ".", Blocked: false, Instances: 3,
		Checked:   []string{"lint", "identity", "dataread", "stamp"},
		Partial:   []string{"projection (2 of 27 refusals; the rest need a cloud)"},
		Unchecked: []string{"discovery"},
	}

	text := renderLiveCheck(t, rep)
	doc := decodeLiveCheckDocument(t, renderLiveCheckJSON(t, rep))

	for _, stage := range doc.Checked {
		if !strings.Contains(text, stage) {
			t.Errorf("JSON names checked stage %q, which the text report does not mention:\n%s", stage, text)
		}
	}
	for _, stage := range doc.Partial {
		if !strings.Contains(text, stage) {
			t.Errorf("JSON names partial stage %q verbatim, which the text report does not carry:\n%s", stage, text)
		}
	}
	for _, stage := range doc.Unchecked {
		if !strings.Contains(text, stage) {
			t.Errorf("JSON names unchecked stage %q, which the text report does not mention:\n%s", stage, text)
		}
	}
}
