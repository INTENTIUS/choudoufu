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
	"github.com/intentius/choudoufu/internal/live/stamp"
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
	// The fifth table, added by #224: internal/live/stamp needs no cloud
	// either, so its registry ranks in the catalog alongside the other four.
	for _, refusal := range stamp.Refusals() {
		want[string(LayerStamp)+"/"+refusal.Summary] = true
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
		"acceptance":   true, // issue #108's cohort acceptance tier: a gated test harness, not a pass
		"check":        true, // this package: the instrument itself
		"cloudcontrol": true, // a client for one AWS API
		"docsref":      true, // parses the doc references refusals carry
		"docrefs":      true, // issue #256 item 8: godoc cross-package citation sweep, not a live pass
		"flocitest":    true, // test harness
		"foreign":      true, // classification of unclaimed resources, inside discovery's stage
		"harness":      true, // the burndown and assumptions registries; measures the instrument, is not part of it
		"lifecycle":    true,
		"listclient":   true, // a client
		"liveimport":   true, // the bulk migration command's engine
		"markerkey":    true,
		"markers":      true, // the marker vocabulary itself
		"marksafe":     true, // issue #240's lockstep scanner over mark-unsafe cty accessors, plus its mark-injection sweep
		"mdspan":       true, // rewrites generated regions of a markdown doc
		"mv":           true, // the rename command's engine
		// A no-classic-Importer provider response classified, and a stub
		// synthesized from a resolved identity in its place - shared by
		// internal/live/projection's pre-walk import and #388's plan-node
		// seam (internal/tofu, which cannot import any other internal/live
		// package). A pure function over a providers.Schema and a
		// diagnostics list; it refuses nothing itself, and every refusal
		// it can still produce is internal/live/projection's own "Resource
		// type has no classic Importer", already classified there.
		"noimporter": true,
		// Computes the source edit that turns a state-backed module into a
		// live one, so the ONBOARDED form of a configuration can be
		// measured (tools/refusal-probe -onboarded). It rewrites text and
		// refuses nothing: everything it produces is fed back through this
		// package's own passes, which is where the verdict comes from.
		"onboard":         true,
		"moved":           true, // the moved-block relation lint and discovery share; a pure function over addresses
		"passthrough":     true, // a registry of upstream diagnostics, not a pass
		"pins":            true, // the shared provider-version pin (#117), one constant
		"pluginschema":    true, // provider schema reading
		"policy":          true, // the ownership policy matrix
		"providerscope":   true, // module-aware provider address resolution (#104); a pure function, not yet wired into any pass
		"refusalscan":     true, // the shared lockstep scanner behind those registries
		"providerversion": true,
		"registry":        true,
		"slots":           true,
		"staterecord":     true,
		// GitHub issue #365's strict-profile vocabulary: a setting type,
		// the valid set, the default, and which settings a build
		// implements. It refuses nothing itself - internal/live/lint does,
		// against this table - the same division internal/live/policy has.
		"strict": true,
		"untag":  true,
		// One predicate over a documentation string, shared by two
		// generators so both read the same rule (#272). It refuses nothing
		// at run time and never will: what it produces is a signal in a
		// generated artifact, and the stage that would act on that signal
		// does not exist yet.
		"uniquename": true,
	}

	classified := map[string]bool{}
	for _, layer := range allClassifiedLayers() {
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

// TestAnalyzeRoutesStateBackendToWarning is GitHub issue #210: RuleStateBackend
// is [lint.SeverityWarning] now, so a configuration whose only issue is a
// backend block is not blocked - the finding lands in Warnings, not Findings,
// registered in the catalog under the same (layer, ID) either channel would
// use. "backend" and "cloud" are lint's own limits fixtures, sharing this
// rule for the two HCL forms that trip it (see live/LIMITATIONS.md).
func TestAnalyzeRoutesStateBackendToWarning(t *testing.T) {
	for _, fixture := range []string{"backend", "cloud"} {
		t.Run(fixture, func(t *testing.T) {
			report := Dir(t.Context(), filepath.Join("..", "lint", "testdata", fixture), Context{})
			if !report.Readable() {
				t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
			}
			if report.Blocked() {
				t.Fatalf("report.Blocked() = true; a warning-severity issue must not block: %v", report.Findings)
			}
			for _, finding := range report.Findings {
				if finding.Layer == LayerLint && finding.ID == string(lint.RuleStateBackend) {
					t.Errorf("state-backend finding present in report.Findings; it must be in Warnings only: %v", finding)
				}
			}

			var found bool
			for _, warning := range report.Warnings {
				if warning.Layer != LayerLint || warning.ID != string(lint.RuleStateBackend) {
					continue
				}
				found = true
				if len(warning.Sites) == 0 {
					t.Errorf("%s has no sites", warning.ID)
				}
				if !warning.Registered {
					t.Errorf("%s is not in the catalog", warning.ID)
				}
			}
			if !found {
				t.Errorf("expected %s/%s in report.Warnings; got %v", LayerLint, lint.RuleStateBackend, report.Warnings)
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
	if len(report.Partial) == 0 {
		t.Fatal("report names no partially checked layers; projection has been one since Analyze began computing its two offline refusals")
	}

	// The three lists must be disjoint, in all three pairings. A stage in
	// two of them makes the caveat unreadable: a reader cannot tell whether
	// "projection" in the not-checked line refers to the whole stage or to
	// the part the partial line already accounted for.
	seen := map[Layer]string{}
	for _, layer := range report.Checked {
		seen[layer] = "checked"
	}
	for _, p := range report.Partial {
		if where, dup := seen[p.Layer]; dup {
			t.Errorf("%s is reported as both %s and partially checked", p.Layer, where)
		}
		seen[p.Layer] = "partially checked"
	}
	for _, layer := range report.Unchecked {
		if where, dup := seen[layer]; dup {
			t.Errorf("%s is reported as both %s and unchecked", layer, where)
		}
	}
}

// TestPartialLayerRefusalsAreRegistered is what keeps [PartiallyCheckedLayers]
// honest against the stage it describes.
//
// The IDs there are string literals, because the stage raises them as
// diagnostic summaries and exports no constant for either. A literal that
// stops matching - a reworded summary in internal/live/projection - would
// leave the report claiming to check a refusal that no longer exists under
// that name, while the finding [Analyze] actually raises reads as
// unregistered. So each one is looked up in that stage's own registry, which
// is the external source: mutating the constant to anything the registry
// does not carry fails here.
func TestPartialLayerRefusalsAreRegistered(t *testing.T) {
	for _, p := range PartiallyCheckedLayers() {
		if len(p.Refusals) == 0 {
			t.Errorf("%s is listed as partially checked but names no refusals it checks", p.Layer)
		}
		if p.Total <= len(p.Refusals) {
			t.Errorf("%s claims %d checked refusals of %d total; a partial layer that checks all of them belongs in CheckedLayers", p.Layer, len(p.Refusals), p.Total)
		}
		for _, id := range p.Refusals {
			refusal, ok := lookup(p.Layer, id)
			if !ok {
				t.Errorf("%s is listed as a checked refusal of the %s layer, but that layer's registry does not carry it", id, p.Layer)
				continue
			}
			if refusal.What == "" {
				t.Errorf("%s resolves in the %s registry with no description", id, p.Layer)
			}
		}
	}
}

// allClassifiedLayers is every layer any of the three lists names. The
// package's completeness guards walk this rather than two of the three
// lists, which is how projection stayed uncovered once already.
func allClassifiedLayers() []Layer {
	out := append([]Layer(nil), CheckedLayers()...)
	for _, p := range PartiallyCheckedLayers() {
		out = append(out, p.Layer)
	}
	return append(out, UncheckedLayers()...)
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
