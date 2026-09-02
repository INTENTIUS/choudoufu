// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// TestOnboardingLadderPinsEachClass pins #175's classification rule with one
// fabricated entry per class, folded through the same [Corpus.Add] path the
// generator uses, so the rule cannot drift from what the artifact carries
// without this failing.
func TestOnboardingLadderPinsEachClass(t *testing.T) {
	loaded := LoadResult{Config: &configs.Config{}}

	lintFinding := func(rule lint.Rule, sites ...Site) Finding {
		return Finding{
			Refusal: Refusal{Layer: LayerLint, ID: string(rule)},
			Sites:   sites,
		}
	}
	site := func(typ string) Site { return Site{File: "main.tf", Line: 1, Type: typ} }

	corpus := NewCorpus()

	// One fabricated entry per class, all under the rate-capable origin.
	// The origin string is the one rateCapableOrigins already licenses;
	// fabricating a new origin here would test an allowlist miss instead.
	const origin = "published deployment"
	if !rateCapableOrigins[origin] {
		t.Fatalf("origin %q is not rate-capable; this test's fixtures are stale", origin)
	}

	corpus.Add("clean", origin, Report{Load: loaded})
	corpus.Add("backend-only", origin, Report{Load: loaded, Findings: []Finding{
		lintFinding(lint.RuleStateBackend, site("")),
	}})
	corpus.Add("admissions-only", origin, Report{Load: loaded, Findings: []Finding{
		lintFinding(lint.RuleStateBackend, site("")),
		lintFinding(lint.RuleUnadmittedType, site("aws_ecs_service"), site("aws_ecs_service"), site("aws_lambda_permission")),
		lintFinding(lint.RuleLogicalResource, site("null_resource")),
	}})
	// #179's rung: the admissions set widened by exactly the eligible-read
	// finding. The phase's own refusals do NOT qualify - the second entry
	// pins that an ineligible data source keeps its estate on
	// language-blocked.
	corpus.Add("data-read-eligible", origin, Report{Load: loaded, Findings: []Finding{
		lintFinding(lint.RuleStateBackend, site("")),
		lintFinding(lint.RuleUnadmittedType, site("aws_ecs_service")),
		{Refusal: Refusal{Layer: LayerDataread, ID: dataread.SummaryEligibleRead}, Sites: []Site{site("")}},
	}})
	corpus.Add("language-blocked", origin, Report{Load: loaded, Findings: []Finding{
		lintFinding(lint.RuleStateBackend, site("")),
		lintFinding(lint.RuleUnadmittedType, site("aws_ecs_service")),
		{Refusal: Refusal{Layer: LayerIdentity, ID: "Unresolvable identity"}, Sites: []Site{site("")}},
	}})
	corpus.Add("data-read-blocked", origin, Report{Load: loaded, Findings: []Finding{
		{Refusal: Refusal{Layer: LayerDataread, ID: dataread.SummaryNotReadable}, Sites: []Site{site("")}},
	}})
	corpus.Add("unreadable", origin, Report{})

	// A ranking-only entry gets no profile, however hard it is blocked: its
	// population measures fixtures, and profiling it would balloon the
	// artifact with rows that measure this repo's own assumptions.
	corpus.Add("fixture", "in-repo fixture", Report{Load: loaded, Findings: []Finding{
		lintFinding(lint.RuleStateBackend, site("")),
	}})

	corpus.Finish()

	wantClass := map[string]OnboardingClass{
		"clean":              OnboardingClean,
		"backend-only":       OnboardingBackendOnly,
		"admissions-only":    OnboardingAdmissionsOnly,
		"data-read-eligible": OnboardingDataReadEligible,
		"data-read-blocked":  OnboardingLanguageBlocked,
		"language-blocked":   OnboardingLanguageBlocked,
		"unreadable":         OnboardingUnreadable,
	}
	for _, entry := range corpus.Entries {
		if entry.Origin != origin {
			if entry.Profile != nil {
				t.Errorf("%s: ranking-only entry carries a profile", entry.Name)
			}
			continue
		}
		if entry.Profile == nil {
			t.Errorf("%s: rate-capable entry has no profile", entry.Name)
			continue
		}
		if got, want := entry.Profile.Class, wantClass[entry.Name]; got != want {
			t.Errorf("%s: class %q, want %q", entry.Name, got, want)
		}
	}

	// The admissions-only entry's unadmitted types come from the
	// unadmitted-type finding's sites, deduplicated and sorted; the
	// logical-resource types are a different remedy (a record_store) and
	// stay out of the list.
	for _, entry := range corpus.Entries {
		if entry.Name != "admissions-only" {
			continue
		}
		want := []string{"aws_ecs_service", "aws_lambda_permission"}
		if !reflect.DeepEqual(entry.Profile.UnadmittedTypes, want) {
			t.Errorf("admissions-only unadmitted types = %v, want %v", entry.Profile.UnadmittedTypes, want)
		}
	}

	if corpus.Ladder == nil {
		t.Fatal("corpus with a rate-capable population carries no ladder")
	}
	if want := []string{origin}; !reflect.DeepEqual(corpus.Ladder.Origins, want) {
		t.Errorf("ladder origins = %v, want %v", corpus.Ladder.Origins, want)
	}
	wantCounts := map[OnboardingClass]int{
		OnboardingClean:            1,
		OnboardingBackendOnly:      1,
		OnboardingAdmissionsOnly:   1,
		OnboardingDataReadEligible: 1,
		OnboardingLanguageBlocked:  2,
		OnboardingUnreadable:       1,
	}
	if len(corpus.Ladder.Classes) != len(OnboardingClasses()) {
		t.Fatalf("ladder carries %d class rows, want %d (zeros included)", len(corpus.Ladder.Classes), len(OnboardingClasses()))
	}
	for _, row := range corpus.Ladder.Classes {
		if row.Configs != wantCounts[row.Class] {
			t.Errorf("class %q counted %d, want %d", row.Class, row.Configs, wantCounts[row.Class])
		}
	}

	// Demand counts entries, not sites: aws_ecs_service appears at five
	// sites across three entries and must read 3.
	wantDemand := []TypeDemand{
		{Type: "aws_ecs_service", Configs: 3},
		{Type: "aws_lambda_permission", Configs: 1},
	}
	if !reflect.DeepEqual(corpus.Ladder.UnadmittedDemand, wantDemand) {
		t.Errorf("demand table = %v, want %v", corpus.Ladder.UnadmittedDemand, wantDemand)
	}
}

// TestOnboardingClassDerivesFromRefusalIDsOnly is the edge the rung table
// does not show: the subset rule, not rung prose, is the classifier. A
// profile with only unadmitted-type (or that plus logical-resource) still
// lands on the rung whose edits fix it.
func TestOnboardingClassDerivesFromRefusalIDsOnly(t *testing.T) {
	cases := []struct {
		ids  []string
		want OnboardingClass
	}{
		{[]string{string(lint.RuleUnadmittedType)}, OnboardingAdmissionsOnly},
		{[]string{string(lint.RuleLogicalResource), string(lint.RuleUnadmittedType)}, OnboardingAdmissionsOnly},
		{[]string{string(lint.RuleProvisioner)}, OnboardingLanguageBlocked},
		{[]string{"Dynamic value in static context"}, OnboardingLanguageBlocked},
		// #179's rung and its boundaries: the eligible-read finding alone,
		// or with any admissions-set finding, lands on the new rung; the
		// data-read pass's own refusals, or any other language finding
		// beside it, keep the estate language-blocked.
		{[]string{dataread.SummaryEligibleRead}, OnboardingDataReadEligible},
		{[]string{string(lint.RuleUnadmittedType), dataread.SummaryEligibleRead}, OnboardingDataReadEligible},
		{[]string{dataread.SummaryNotReadable}, OnboardingLanguageBlocked},
		{[]string{dataread.SummaryEligibleRead, "Dynamic value in static context"}, OnboardingLanguageBlocked},
	}
	for _, tc := range cases {
		if got := ClassifyOnboarding(true, tc.ids); got != tc.want {
			t.Errorf("ClassifyOnboarding(true, %v) = %q, want %q", tc.ids, got, tc.want)
		}
	}
}

// TestBackendOnlyRungCoupledToRuleSeverity is issue #256 item 6.
// OnboardingBackendOnly's switch-case in ClassifyOnboarding can be true only
// when every ID in a report's Findings equals lint.RuleStateBackend's ID -
// but analyze.go routes any SeverityWarning-rule finding (RuleStateBackend
// is one, per #210) to Report.Warnings instead of Report.Findings, and
// ClassifyOnboarding is only ever called with refusalIDs(report.Findings).
// So as long as the rule stays warning-severity, a real backend/cloud-block
// config can never reach Findings with only that ID in it, and the rung is
// structurally dead - a rung every ladder table still prints and can never
// show a nonzero count for.
//
// TestOnboardingLadderPinsEachClass exercises OnboardingBackendOnly by
// constructing a Finding for RuleStateBackend directly, which proves the
// switch statement's own logic but not that a real analysis run can ever
// produce that input - it bypasses analyze.go's severity routing entirely,
// the same synthetic-fixture gap the audit-trap list in CLAUDE.md warns
// about ("a ratchet that measures agreement with itself").
//
// This test goes through the real pipeline (Dir, which runs Load then
// Analyze) on a fixture with nothing wrong except a backend block, and
// couples the two ends directly: RuleStateBackend at warning severity must
// classify OnboardingClean (not OnboardingBackendOnly), and the moment the
// rule's severity is raised to fatal, this test starts failing until it is
// updated to expect OnboardingBackendOnly instead - which is exactly the
// "move together or fail" property #256 asked for, checked against a real
// analysis run rather than a hand-built Findings slice.
func TestBackendOnlyRungCoupledToRuleSeverity(t *testing.T) {
	report := Dir(context.Background(), filepath.Join("testdata", "backend-only-real"), Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %+v", report.Load)
	}

	class := ClassifyOnboarding(report.Readable(), refusalIDs(report.Findings))
	isWarning := lint.RuleStateBackend.Severity() == lint.SeverityWarning

	switch {
	case isWarning && class != OnboardingClean:
		t.Errorf("RuleStateBackend is warning-severity, so a backend-only config's finding never reaches "+
			"Findings and OnboardingBackendOnly is unreachable - want class %q, got %q. If something now "+
			"routes a warning-severity finding into Findings, OnboardingBackendOnly just became reachable "+
			"and every ladder table's rung count needs re-checking.",
			OnboardingClean, class)
	case !isWarning && class != OnboardingBackendOnly:
		t.Errorf("RuleStateBackend is no longer warning-severity, so its finding now reaches Findings and "+
			"a backend-only config should classify %q - got %q instead. This test's own expectation needs "+
			"updating for the new severity, which is the point: the ladder rung and the rule severity must "+
			"move together, not silently drift apart", OnboardingBackendOnly, class)
	}
}

// TestCorpusArtifactLadderIsConsistent recomputes the committed artifact's
// ladder from its own per-entry profiles: profiles present exactly for the
// rate-capable origins, each entry's class re-derivable from its refusal
// IDs, and the summary's counts and demand table equal to a fresh fold over
// the entries. A hand-edit to the artifact, or a regeneration under drifted
// code, fails here instead of shipping a summary its own rows contradict.
func TestCorpusArtifactLadderIsConsistent(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "live", "corpus-refusals.json"))
	if err != nil {
		t.Fatalf("reading the corpus artifact: %s", err)
	}
	var artifact struct {
		Entries []CorpusEntry  `json:"entries"`
		Ladder  *LadderSummary `json:"ladder"`
	}
	if err := json.Unmarshal(src, &artifact); err != nil {
		t.Fatalf("parsing the corpus artifact: %s", err)
	}

	profiled := 0
	byClass := map[OnboardingClass]int{}
	demand := map[string]int{}
	for _, entry := range artifact.Entries {
		if rateCapableOrigins[entry.Origin] != (entry.Profile != nil) {
			t.Errorf("%s (origin %q): profile presence does not match rate capability", entry.Name, entry.Origin)
			continue
		}
		if entry.Profile == nil {
			continue
		}
		profiled++
		var ids []string
		for _, r := range entry.Profile.Refusals {
			ids = append(ids, r.ID)
		}
		if got := ClassifyOnboarding(entry.Loaded, ids); got != entry.Profile.Class {
			t.Errorf("%s: class %q does not re-derive from its refusal IDs (got %q)", entry.Name, entry.Profile.Class, got)
		}
		byClass[entry.Profile.Class]++
		for _, typ := range entry.Profile.UnadmittedTypes {
			demand[typ]++
		}
	}
	if profiled == 0 {
		t.Fatal("the committed artifact profiles no entries; the published-deployment population is gone or the field moved")
	}

	if artifact.Ladder == nil {
		t.Fatal("the committed artifact carries no ladder despite profiled entries")
	}
	for _, row := range artifact.Ladder.Classes {
		if row.Configs != byClass[row.Class] {
			t.Errorf("ladder counts %d %q entries; the profiles sum to %d", row.Configs, row.Class, byClass[row.Class])
		}
	}
	for _, row := range artifact.Ladder.UnadmittedDemand {
		if row.Configs != demand[row.Type] {
			t.Errorf("ladder demands %s in %d configs; the profiles sum to %d", row.Type, row.Configs, demand[row.Type])
		}
	}
	if got, want := len(artifact.Ladder.UnadmittedDemand), len(demand); got != want {
		t.Errorf("ladder demand table has %d rows; the profiles declare %d distinct unadmitted types", got, want)
	}
}
