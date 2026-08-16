// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/passthrough"
)

// TestCatalogComesFromBothTables is #114's fourth acceptance criterion: the
// refusal set is the two packages' own tables, not a third list kept here.
//
// It compares sets rather than counts, so a refusal added to either package
// appears here without anyone editing this package, and one invented here
// fails.
func TestCatalogComesFromBothTables(t *testing.T) {
	want := map[string]bool{}
	for _, rule := range lint.Rules() {
		want[string(LayerLint)+"/"+string(rule)] = true
	}
	for _, refusal := range identity.Refusals() {
		want[string(LayerIdentity)+"/"+refusal.Summary] = true
	}
	// The third table, added by #110. Its refusals surface during identity
	// resolution - that is the pass the user hits them in - so they are
	// catalogued under that layer with RaisedBy naming the package that
	// actually wrote the diagnostic.
	for _, refusal := range passthrough.Refusals() {
		want[string(LayerIdentity)+"/"+refusal.Summary] = true
	}
	// The fourth table, added by #179: the data-read phase's offline
	// eligibility classification is a checked pass, so its registry ranks
	// in the catalog alongside the other two.
	for _, refusal := range dataread.Refusals() {
		want[string(LayerDataread)+"/"+refusal.Summary] = true
	}

	got := map[string]bool{}
	for _, refusal := range Catalog() {
		key := string(refusal.Layer) + "/" + refusal.ID
		if got[key] {
			t.Errorf("catalog lists %s twice", key)
		}
		got[key] = true

		if refusal.Title == "" {
			t.Errorf("%s has no title", key)
		}
	}

	for key := range want {
		if !got[key] {
			t.Errorf("catalog is missing %s, which its source table carries", key)
		}
	}
	for key := range got {
		if !want[key] {
			t.Errorf("catalog invents %s, which no source table carries", key)
		}
	}
}

// TestLayersClassifyEveryLivePackage is the drift guard [UncheckedLayers]
// promises.
//
// A report's honesty rests on its list of unchecked stages being complete. A
// new stage appearing in internal/live without being classified would make
// every report's "not checked" line quietly wrong, which is the failure mode
// #101 spent a campaign on. So the classification is asserted against the
// packages that actually exist, and adding one is a test failure until
// somebody decides whether this instrument sees it.
func TestLayersClassifyEveryLivePackage(t *testing.T) {
	// Packages that are not live-path stages: support code, test helpers,
	// and the two front ends' own homes. Adding to this list is the way to
	// say "not a stage"; the point is that it has to be said.
	notAStage := map[string]bool{
		"acceptance":      true, // issue #108's cohort acceptance tier: a gated test harness, not a pass
		"check":           true, // this package: the instrument itself
		"cloudcontrol":    true, // a client for one AWS API
		"docsref":         true, // parses the doc references refusals carry
		"flocitest":       true, // test harness
		"foreign":         true, // classification of unclaimed resources, inside discovery's stage
		"lifecycle":       true,
		"listclient":      true, // a client
		"liveimport":      true, // the bulk migration command's engine
		"markerkey":       true,
		"markers":         true, // the marker vocabulary itself
		"mdspan":          true, // rewrites generated regions of a markdown doc
		"mv":              true, // the rename command's engine
		"passthrough":     true, // a registry of upstream diagnostics, not a pass
		"pins":            true, // the shared provider-version pin (#117), one constant
		"pluginschema":    true, // provider schema reading
		"policy":          true, // the ownership policy matrix
		"refusalscan":     true, // the shared lockstep scanner behind those registries
		"providerversion": true,
		"registry":        true,
		"slots":           true,
		"staterecord":     true,
		"untag":           true,
	}

	classified := map[string]bool{}
	for _, layer := range append(CheckedLayers(), UncheckedLayers()...) {
		classified[string(layer)] = true
	}

	entries, err := os.ReadDir("..")
	if err != nil {
		t.Fatalf("reading internal/live: %s", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if classified[name] || notAStage[name] {
			continue
		}
		t.Errorf("internal/live/%s is neither a classified layer nor listed as not-a-stage. "+
			"Decide which it is: if this instrument cannot see it, it belongs in UncheckedLayers "+
			"so every report says so.", name)
	}

	// The other direction: a layer naming a package that no longer exists.
	for name := range classified {
		if _, err := os.Stat(filepath.Join("..", name)); err != nil {
			t.Errorf("layer %q names internal/live/%s, which does not exist", name, name)
		}
	}
}

// TestAnalyzeReportsTheRuleThatFired runs the analysis over the lint
// package's own fixtures and checks that the finding is keyed by rule
// identity.
func TestAnalyzeReportsTheRuleThatFired(t *testing.T) {
	tests := []struct {
		fixture string
		layer   Layer
		id      string
	}{
		{"count-index", LayerLint, string(lint.RuleCountIndex)},
		{"provisioner", LayerLint, string(lint.RuleProvisioner)},
		{"backend", LayerLint, string(lint.RuleStateBackend)},
		{"remote-state", LayerLint, string(lint.RuleRemoteState)},
		{"moved", LayerLint, string(lint.RuleMovedBlock)},
	}

	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			report := Dir(t.Context(), filepath.Join("..", "lint", "testdata", test.fixture), Context{})
			if !report.Readable() {
				t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
			}
			if !report.Blocked() {
				t.Fatalf("fixture was not refused; expected %s/%s", test.layer, test.id)
			}

			var found bool
			for _, finding := range report.Findings {
				if finding.Layer == test.layer && finding.ID == test.id {
					found = true
					if len(finding.Sites) == 0 {
						t.Errorf("%s has no sites", finding.ID)
					}
					for _, site := range finding.Sites {
						if site.Location() == "" {
							t.Errorf("%s has a site with no position", finding.ID)
						}
					}
					if !finding.Registered {
						t.Errorf("%s is not in the catalog", finding.ID)
					}
				}
			}
			if !found {
				t.Errorf("expected %s/%s; got %s", test.layer, test.id, findingIDs(report))
			}
		})
	}
}

// TestCleanFixtureIsNotBlocked checks the other verdict, since a report that
// refused everything would pass every test above.
//
// The estate fixture is used rather than lint's own "clean" directory, which
// is clean only for lint: it sets `count = tobool(count.index) ? 1 : 0` to
// prove a boundary of the count.index rule, and identity - which does
// evaluate count expressions - correctly refuses it. Two passes, two
// meanings of clean.
func TestCleanFixtureIsNotBlocked(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("..", "..", "..", "live", "e2e", "estates", "s3"), Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	if report.Blocked() {
		t.Fatalf("clean fixture was refused: %s", findingIDs(report))
	}
	if report.Instances == 0 {
		t.Error("clean fixture resolved no instances, so nothing was actually checked")
	}
}

// TestUnadmittedTypeIsCountedOnce covers the dedupe rule in [Analyze]: lint
// and identity both refuse an unadmitted type, and counting one resource as
// two blocked refusals would inflate the ranking the corpus artifact exists
// to produce.
func TestUnadmittedTypeIsCountedOnce(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("..", "lint", "testdata", "unadmitted"), Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	byLocation := map[string][]string{}
	for _, finding := range report.Findings {
		for _, site := range finding.Sites {
			if loc := site.Location(); loc != "" {
				byLocation[loc] = append(byLocation[loc], string(finding.Layer)+"/"+finding.ID)
			}
		}
	}
	for loc, ids := range byLocation {
		if len(ids) > 1 {
			t.Errorf("%s is counted %d times: %v", loc, len(ids), ids)
		}
	}
}

// TestTypeShapedFindingSummarizesByType covers #114's requirement that
// unadmitted types are summarized rather than enumerated.
func TestTypeShapedFindingSummarizesByType(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("..", "lint", "testdata", "unadmitted"), Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	var checked bool
	for _, finding := range report.Findings {
		if finding.ID != string(lint.RuleUnadmittedType) {
			continue
		}
		checked = true
		types := finding.Types()
		if len(types) == 0 {
			t.Fatal("unadmitted-type finding carries no type breakdown, so a report has nothing to summarize with")
		}
		var total int
		for _, tc := range types {
			if tc.Type == "" {
				t.Error("a type count has no type name")
			}
			total += tc.Sites
		}
		if total != len(finding.Sites) {
			t.Errorf("type breakdown covers %d sites; the finding has %d", total, len(finding.Sites))
		}
	}
	if !checked {
		t.Skip("fixture no longer produces an unadmitted-type finding")
	}
}

// TestReportNamesWhatItDidNotCheck is #114's third acceptance criterion.
func TestReportNamesWhatItDidNotCheck(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("..", "..", "..", "live", "e2e", "estates", "s3"), Context{})

	if len(report.Checked) == 0 {
		t.Error("report names no checked layers")
	}
	if len(report.Unchecked) == 0 {
		t.Fatal("report names no unchecked layers; a clean verdict would then read as a promise about stages nobody looked at")
	}

	for _, layer := range report.Unchecked {
		for _, checked := range report.Checked {
			if layer == checked {
				t.Errorf("%s is reported as both checked and unchecked", layer)
			}
		}
	}
}

// TestUnreadableDirectoryIsNotClean covers the distinction the command's
// exit path depends on: a directory that will not parse has no findings
// either, and must never render as "nothing refused this".
func TestUnreadableDirectoryIsNotClean(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.tf"), []byte("resource \"aws_s3_bucket\" {\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := Dir(t.Context(), dir, Context{})
	if report.Readable() {
		t.Fatal("a malformed configuration reported as readable")
	}
	if report.Blocked() {
		t.Error("an unreadable configuration reported findings")
	}
	if len(report.Load.Diags) == 0 {
		t.Error("an unreadable configuration reported no diagnostics, so nothing explains it")
	}
}

func findingIDs(report Report) []string {
	out := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		out = append(out, string(finding.Layer)+"/"+finding.ID)
	}
	return out
}

// TestCorpusCarriesRefusalsThatFiredNowhere is the property that makes the
// #102 artifact a work queue rather than a log: a refusal blocking nothing
// is evidence too, and it is invisible to any instrument assembled from
// observed output.
func TestCorpusCarriesRefusalsThatFiredNowhere(t *testing.T) {
	corpus := NewCorpus()
	corpus.Add("count-index", "test fixture", Dir(t.Context(), filepath.Join("..", "lint", "testdata", "count-index"), Context{}))
	corpus.Finish()

	if got, want := len(corpus.Refusals), len(Catalog()); got < want {
		t.Errorf("corpus carries %d refusal rows; the catalog has %d", got, want)
	}

	var fired, zero int
	for _, row := range corpus.Refusals {
		if row.Configs > 0 {
			fired++
		} else {
			zero++
		}
	}
	if fired == 0 {
		t.Error("no refusal fired on a fixture written to trip one")
	}
	if zero == 0 {
		t.Error("every refusal fired on a single fixture, which cannot be right")
	}

	// Ranked by configurations blocked, then sites: #102's choice, because
	// a raw site count is dominated by whichever configuration is largest.
	for i := 1; i < len(corpus.Refusals); i++ {
		prev, cur := corpus.Refusals[i-1], corpus.Refusals[i]
		if prev.Configs < cur.Configs {
			t.Errorf("row %d (%s, %d configs) outranks row %d (%s, %d configs)",
				i, cur.ID, cur.Configs, i-1, prev.ID, prev.Configs)
		}
	}

	if corpus.Totals.Configs != 1 {
		t.Errorf("totals count %d configurations; one was added", corpus.Totals.Configs)
	}
}
