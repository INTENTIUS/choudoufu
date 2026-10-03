// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package largeset

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/plansummary"
)

// TestLargeSetBaselineAgainstFloci is #1750's baseline: generate the fixture
// at A, apply every estate through live/large-set/apply.sh against a scratch
// floci behind the counting proxy, plan each one at A (the steady-state
// control), regenerate the module at B in place, and plan each one again.
// Every plan's API calls, wall clock and summary go into a gated record
// (record.go).
//
// Dispatch only, by design (#1750: "nothing added to per-PR CI"): it is gated
// on its own variable rather than flocitest.Gate, so the nightly floci tier
// does not pick it up either.
//
//	LARGESET_BASELINE=1 LARGESET_RECORD=$PWD/live/large-set/baseline-n5.json \
//	  env -u PWD go test ./internal/live/largeset/ -run TestLargeSetBaselineAgainstFloci -v -timeout 30m
//
//	LARGESET_ESTATES  N (default 5). Anything above 5 is the maintainer's call
//	                  and also needs LARGESET_MAINTAINER_GO=1.
//	LARGESET_REPEATS  readings per plan (default 3)
//	LARGESET_RECORD   where to write the record; unset, the gate runs and
//	                  nothing is written
//	LARGESET_COMMIT   the commit the record names (default git HEAD)
//	LARGESET_SUMMARY_DOC  where to write the bump's set document (#1753),
//	                  the input the record's summary figures were read from
func TestLargeSetBaselineAgainstFloci(t *testing.T) {
	if os.Getenv("LARGESET_BASELINE") == "" {
		t.Skip("the large-set baseline runs on dispatch only: set LARGESET_BASELINE=1 (needs docker, bash, go and the pinned floci image)")
	}
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "bash")
	flocitest.RequireBinary(t, "go")

	n := envInt(t, "LARGESET_ESTATES", 5)
	if n > 5 && os.Getenv("LARGESET_MAINTAINER_GO") == "" {
		t.Fatalf("LARGESET_ESTATES=%d: anything above N=5 runs only on the maintainer's go (#1750); set LARGESET_MAINTAINER_GO=1 if that go was given", n)
	}
	repeats := envInt(t, "LARGESET_REPEATS", 3)

	port := flocitest.StartFloci(t, "cdfa-largeset")
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(port))
	flocitest.PluginCacheDir(t)
	bin := flocitest.BuildTofu(t)
	t.Setenv("AWS_ENDPOINT_URL", proxy.Endpoint())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	dir := filepath.Join(t.TempDir(), "fixture")
	m, err := Write(dir, Options{Estates: n, ModuleVersion: VersionA})
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", filepath.Join(flocitest.RepoRoot(t), "live", "large-set", "apply.sh"), dir) //nolint:gosec // fixed script path
	cmd.Env = append(os.Environ(), "CHOUDOUFU_BIN="+bin, "LARGESET_ENDPOINT="+proxy.Endpoint())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, _ := cmd.Output()
	verdict := strings.TrimSpace(string(out))
	t.Logf("%s", verdict)
	if !strings.HasPrefix(verdict, "LARGESET-APPLY: green") {
		t.Fatalf("the apply at A did not go green:\n%s\n%s", verdict, stderr.String())
	}

	byDir := map[string]Estate{}
	for _, e := range m.Estates {
		byDir[e.Dir] = e
	}
	rec := BaselineRecord{
		Fixture:   FixtureShape{Estates: n, Source: SourceLocal, Prefix: m.Prefix, From: VersionA, To: VersionB},
		Condition: ConditionLivePlanAfterOwnApply,
		Substrate: "floci",
		Emulator:  flocitest.Image(),
		Commit:    baselineCommit(t),
		Date:      time.Now().UTC().Format(time.RFC3339),
		Repeats:   repeats,
	}
	for _, d := range m.ApplyOrder {
		rec.Estates = append(rec.Estates, EstateBaseline{Estate: byDir[d].Name, Role: byDir[d].Role})
	}

	for i, d := range m.ApplyOrder {
		rec.Estates[i].Steady = measurePlan(t, proxy, bin, filepath.Join(dir, d), repeats)
	}
	if _, err := Write(dir, Options{Estates: n, ModuleVersion: VersionB}); err != nil {
		t.Fatal(err)
	}
	for i, d := range m.ApplyOrder {
		rec.Estates[i].Bump = measurePlan(t, proxy, bin, filepath.Join(dir, d), repeats)
	}

	rec.Summary = summarizeBump(t, bin, dir, m, rec)

	t.Logf("LARGESET BASELINE N=%d repeats=%d emulator=%s", n, repeats, rec.Emulator)
	t.Logf("%-4s %-9s %-12s %-12s %-10s %-10s %s", "est", "role", "steady-calls", "bump-calls", "steady-s", "bump-s", "bump plan")
	for _, e := range rec.Estates {
		t.Logf("%-4s %-9s %-12d %-12d %-10.2f %-10.2f %s", e.Estate, e.Role, e.Steady.Median(), e.Bump.Median(),
			e.Steady.MedianSeconds(), e.Bump.MedianSeconds(), e.Bump.Summaries[0])
	}

	path := os.Getenv("LARGESET_RECORD")
	if path == "" {
		if err := GateBaseline(rec); err != nil {
			t.Fatalf("the measurement does not pass the record's gate: %s", err)
		}
		t.Logf("baseline gated clean; set LARGESET_RECORD=<path> to write it")
		return
	}
	if err := WriteBaseline(path, rec); err != nil {
		t.Fatal(err)
	}
	t.Logf("baseline written: %s", path)
}

// summarizeBump is #1753's reading: after the bump's measured plans, one
// more live-plan per estate with -out, show -json of each, wrapped into the
// set document #1752 emits, and summarized. The figures go into the record;
// its gate checks the outliers against the plans' own totals.
func summarizeBump(t *testing.T, bin, dir string, m Manifest, rec BaselineRecord) *SummaryReading {
	t.Helper()
	byDir := map[string]Estate{}
	for _, e := range m.Estates {
		byDir[e.Dir] = e
	}
	doc := plansummary.SetDocument{}
	nameByDir := map[string]string{}
	for _, d := range m.ApplyOrder {
		root := filepath.Join(dir, d)
		plan := exec.Command(bin, "live-plan", "-no-color", "-input=false", "-out=bump.tfplan") //nolint:gosec // the binary this test built
		plan.Dir = root
		if out, err := plan.CombinedOutput(); err != nil {
			t.Fatalf("live-plan -out in %s failed: %v\n%s", root, err, out)
		}
		show := exec.Command(bin, "show", "-json", "bump.tfplan") //nolint:gosec // the binary this test built
		show.Dir = root
		out, err := show.Output()
		if err != nil {
			t.Fatalf("show -json in %s failed: %v", root, err)
		}
		var p plansummary.Plan
		if err := json.Unmarshal(out, &p); err != nil {
			t.Fatalf("show -json in %s is not a plan: %v", root, err)
		}
		doc.Roots = append(doc.Roots, plansummary.SetRoot{Root: d, Estate: byDir[d].Estate, Status: "planned", Plan: &p})
		nameByDir[d] = byDir[d].Name
	}
	if path := os.Getenv("LARGESET_SUMMARY_DOC"); path != "" {
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil { //nolint:gosec // a fixture document
			t.Fatal(err)
		}
	}

	s := plansummary.Summarize(plansummary.Input{Set: &doc})
	r := &SummaryReading{Outliers: []string{}}
	for i, g := range s.Groups {
		r.Groups = append(r.Groups, len(g.Members))
		if i == 0 {
			continue
		}
		for _, mem := range g.Members {
			r.Outliers = append(r.Outliers, nameByDir[mem])
		}
	}
	text := s.Text()
	r.SummaryLines = strings.Count(text, "\n")
	for _, e := range rec.Estates {
		r.PlanLines += e.Bump.OutputLines
	}
	r.MarkdownChars = len([]rune(s.Markdown(plansummary.GitLabNoteLimit)))
	t.Logf("LARGESET SUMMARY groups=%v outliers=%v summary_lines=%d plan_lines=%d\n%s", r.Groups, r.Outliers, r.SummaryLines, r.PlanLines, text)
	return r
}

// measurePlan runs live-plan in dir repeats times and reads each run's cost
// off the proxy and its verdict off its output.
func measurePlan(t *testing.T, proxy *flocitest.CountingProxy, bin, dir string, repeats int) PlanReading {
	t.Helper()
	var r PlanReading
	for i := 0; i < repeats; i++ {
		before := proxy.Total()
		start := time.Now()
		cmd := exec.Command(bin, "live-plan", "-no-color", "-input=false") //nolint:gosec // the binary this test built
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		secs := time.Since(start).Seconds()
		calls := proxy.Total() - before
		output := string(out)
		if err != nil {
			t.Fatalf("live-plan in %s failed: %v\n%s", dir, err, output)
		}
		summary := ""
		if strings.Contains(output, "\nNo changes.") || strings.HasPrefix(output, "No changes.") {
			summary = "No changes."
		}
		add, change, destroy, ok := flocitest.PlanSummary(output)
		if ok {
			summary = planSummaryText(add, change, destroy)
		}
		r.Calls = append(r.Calls, calls)
		r.Seconds = append(r.Seconds, float64(int(secs*100))/100)
		r.Summaries = append(r.Summaries, summary)
		if i == 0 {
			r.Add, r.Change, r.Destroy = add, change, destroy
			r.Changed = flocitest.ChangedResources(output)
			r.OutputLines = strings.Count(output, "\n")
		}
	}
	return r
}

func planSummaryText(add, change, destroy int) string {
	return "Plan: " + strconv.Itoa(add) + " to add, " + strconv.Itoa(change) + " to change, " + strconv.Itoa(destroy) + " to destroy."
}

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q is not a positive integer", name, v)
	}
	return n
}

// baselineCommit is LARGESET_COMMIT, or HEAD with "+dirty" when the tree has
// changes, so a record taken on an uncommitted tree says so.
func baselineCommit(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("LARGESET_COMMIT"); v != "" {
		return v
	}
	head := flocitest.HeadCommit(t)
	if head == "" {
		return ""
	}
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	cmd.Dir = flocitest.RepoRoot(t)
	if out, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return head + "+dirty"
	}
	return head
}
