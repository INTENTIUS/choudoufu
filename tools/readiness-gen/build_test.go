// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestPartitionGuard is the Accept criterion: every type from
// live/survey-full.json appears exactly once in live/readiness.json, and
// the tier and status totals both sum to the survey's own type count.
func TestPartitionGuard(t *testing.T) {
	root := testRepoRoot(t)
	survey, err := loadSurvey(root)
	if err != nil {
		t.Fatal(err)
	}
	art := readCommitted(t, root)

	if art.Counts.Types != survey.Counts.Types {
		t.Fatalf("%s counts.types is %d, live/survey-full.json counts.types is %d",
			OutputJSONRel, art.Counts.Types, survey.Counts.Types)
	}
	if len(art.Types) != survey.Counts.Types {
		t.Fatalf("%s lists %d types, live/survey-full.json lists %d", OutputJSONRel, len(art.Types), survey.Counts.Types)
	}

	seen := make(map[string]int, len(art.Types))
	for _, r := range art.Types {
		seen[r.Type]++
	}
	for _, st := range survey.Types {
		switch seen[st.Type] {
		case 0:
			t.Errorf("%s is in live/survey-full.json but missing from %s", st.Type, OutputJSONRel)
		case 1:
			// exactly once, as required
		default:
			t.Errorf("%s appears %d times in %s, want exactly once", st.Type, seen[st.Type], OutputJSONRel)
		}
	}
	surveySet := make(map[string]bool, len(survey.Types))
	for _, st := range survey.Types {
		surveySet[st.Type] = true
	}
	for typeName := range seen {
		if !surveySet[typeName] {
			t.Errorf("%s names %s, which live/survey-full.json's provider roster does not contain", OutputJSONRel, typeName)
		}
	}

	tierSum, statusSum := 0, 0
	for _, n := range art.Counts.Tiers {
		tierSum += n
	}
	for _, n := range art.Counts.Statuses {
		statusSum += n
	}
	if tierSum != art.Counts.Types {
		t.Errorf("tier counts sum to %d, counts.types is %d", tierSum, art.Counts.Types)
	}
	if statusSum != art.Counts.Types {
		t.Errorf("status counts sum to %d, counts.types is %d", statusSum, art.Counts.Types)
	}

	validTiers := map[string]bool{TierMarkerCarried: true, TierDeclarationCarried: true, TierRecordCarried: true, TierExcludedByDesign: true}
	validStatuses := map[string]bool{
		StatusInContract: true, StatusPendingRatification: true, StatusNeedsSeparator: true,
		StatusNeedsEvidence: true, StatusPendingMechanism: true, StatusExcluded: true,
	}
	for _, r := range art.Types {
		if !validTiers[r.Tier] {
			t.Errorf("%s: tier %q is not one of the RFC's four tier names", r.Type, r.Tier)
		}
		if !validStatuses[r.Status] {
			t.Errorf("%s: status %q is not one of the six recognized statuses", r.Type, r.Status)
		}
	}
}

// TestArtifactMatchesCommitted is the drift pattern
// tools/row-gen/buckets_test.go's TestBucketsArtifactMatchesCommitted uses:
// recompute from the same committed inputs and hold the artifact to it.
func TestArtifactMatchesCommitted(t *testing.T) {
	root := testRepoRoot(t)
	want, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	got := readCommitted(t, root)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s is stale; run `go run ./tools/readiness-gen` and commit it", OutputJSONRel)
	}
}

// TestBuildIsDeterministic is the Accept criterion "run the generator twice
// and diff": two in-process Build() calls over the same tree must produce
// byte-identical JSON.
func TestBuildIsDeterministic(t *testing.T) {
	root := testRepoRoot(t)
	a, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	aj, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(aj) != string(bj) {
		t.Errorf("two Build() runs over the same tree produced different output")
	}
}

// readinessRatchetAllowlist names a type live/readiness.json records
// in-contract that a fresh Build() no longer places in-contract, with the
// reason it is allowed to have moved. Empty is the intended state - modeled
// on internal/live/acceptance's committed-artifact ratchet
// (enforceRatchet, "a cohort it records as passing FAILS this test if it
// stops passing") and on live/flociimage_test.go's staleFlociMeasurements
// (a regression is a standing, reviewed decision or it is a defect): an
// entry here is not a parking space, it is a deliberate retraction with its
// reason recorded where the next reader will see it.
var readinessRatchetAllowlist = map[string]string{}

// TestInContractRatchet is the Accept criterion: a type may not leave
// in-contract status without an allowlist entry naming why.
func TestInContractRatchet(t *testing.T) {
	root := testRepoRoot(t)
	committed := readCommitted(t, root)
	fresh, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	freshByType := make(map[string]Row, len(fresh.Types))
	for _, r := range fresh.Types {
		freshByType[r.Type] = r
	}

	seenAllowed := map[string]bool{}
	for _, c := range committed.Types {
		if c.Status != StatusInContract {
			continue
		}
		if reason, allowed := readinessRatchetAllowlist[c.Type]; allowed {
			seenAllowed[c.Type] = true
			t.Logf("%s: allowed off in-contract (%s)", c.Type, reason)
			continue
		}
		f, ok := freshByType[c.Type]
		if !ok {
			t.Errorf("%s: recorded in-contract in %s and produced no verdict from a fresh Build() - "+
				"the type left live/survey-full.json's roster, or add it to readinessRatchetAllowlist with why",
				c.Type, OutputJSONRel)
			continue
		}
		if f.Status != StatusInContract {
			t.Errorf("%s: recorded in-contract in %s and a fresh Build() now says %q. "+
				"Regenerate and commit if this is a real, reviewed change (a retraction, a ruling), "+
				"and add %s to readinessRatchetAllowlist naming why; otherwise this is a regression to fix.",
				c.Type, OutputJSONRel, f.Status, c.Type)
		}
	}

	// Exemption hygiene, the same half TestCIExclusionsAreReal
	// (live/ci_coverage_test.go) and TestFlociImage's own checks hold their
	// allowlists to: an entry naming a type that no longer needs it reads as
	// a live exception to the next person who has to decide whether their
	// own regression is "normal here".
	for typeName, reason := range readinessRatchetAllowlist {
		if !seenAllowed[typeName] {
			t.Errorf("readinessRatchetAllowlist names %s (%q), which the committed artifact does not record "+
				"in-contract (or a fresh Build() agrees with it again); delete the entry", typeName, reason)
		}
	}
}

// testRepoRoot resolves the checkout root the same way repoRoot does,
// wrapped for a test's fatal-on-error convenience.
func testRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// readCommitted reads and decodes the committed live/readiness.json.
func readCommitted(t *testing.T, root string) Artifact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(OutputJSONRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v (run `go run ./tools/readiness-gen` and commit it)", OutputJSONRel, err)
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatalf("decoding %s: %v", OutputJSONRel, err)
	}
	return art
}
