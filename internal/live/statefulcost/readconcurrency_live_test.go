// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefulcost

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestReadPassConcurrencyAgainstFloci asks one question and reports the
// answer whichever way it comes out: at 745 resources, does choudoufu's
// steady-state plan still put ten provider requests in flight at once, the
// way issue #654's fix made it do at 79?
//
// #654 found a plan running one request wide where stock runs ten, fixed it,
// and re-measured at scale 1 only. Its instrument - a latency-injecting
// proxy that records each request's interval - was built ad hoc and never
// kept, so no run since has been able to look at the shape at any other
// scale. This is that instrument, kept, pointed at both scales.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/statefulcost/ -run TestReadPassConcurrencyAgainstFloci -v -timeout 90m
//	CONC_SCALE=10 TF_FLOCI_TEST=1 go test ./internal/live/statefulcost/ -run TestReadPassConcurrencyAgainstFloci -v -timeout 180m
//
// Env:
//
//	CONC_SCALE       terralith-gen -scale (default 1)
//	CONC_LATENCY_MS  injected per-request latency (default 100, #654's)
//	CONC_REPEATS     timed runs per column (default 3)
//	CONC_READ_PAR    comma-separated TOFU_LIVE_READ_PARALLELISM values to
//	                 sweep for the choudoufu column; "" for just the default
//	CONC_SWEEP_PAR   the same for TOFU_LIVE_SWEEP_PARALLELISM
//	CONC_SERIAL_CONTROL  "0" to skip the deliberately-serialised control
//
// It asserts almost nothing about the numbers, because a wall clock taken
// against an emulator grades the machine. What it does assert is that every
// timed run it reports actually planned and actually proposed nothing - a
// plan that errored or that proposed work is a different operation, and its
// timeline is not comparable with the others'.
//
// The one number it does gate is the instrument's own: the control column,
// which is choudoufu forced to one read at a time, must come back peak 1.
// If it does not, this rig cannot see serialisation and no "peak 10" it
// prints about anything else means anything.
func TestReadPassConcurrencyAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "read-pass-concurrency")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	scale := intFromEnv(t, "CONC_SCALE", 1)
	latency := time.Duration(intFromEnv(t, "CONC_LATENCY_MS", 100)) * time.Millisecond
	runs := intFromEnv(t, "CONC_REPEATS", 3)
	root := flocitest.RepoRoot(t)
	choudoufuBin := flocitest.BuildTofu(t)
	flocitest.PluginCacheDir(t)

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	port := flocitest.StartFloci(t, "cdf-conc")
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(port))
	env := awsEnv(proxy.Endpoint())
	t.Logf("emulator %s (%s) via proxy %s; latency %s; scale %d; %d runs per column",
		flocitest.Endpoint(port), flocitest.Image(), proxy.Endpoint(), latency, scale, runs)

	prefix := fmt.Sprintf("cc%d", scale)
	estate := "conc-" + prefix

	// ── the estate, stood up by stock terraform holding its own state ────
	// The latency is OFF for this: an apply of 745 resources through a
	// 100 ms proxy would cost more than the whole measurement, and nothing
	// about the apply is under test.
	coldDir := filepath.Join(t.TempDir(), "cold")
	generate(t, root, coldDir, scale, prefix)
	mustRun(t, coldDir, env, terraformBin, "init", "-input=false", "-no-color")
	applyStart := time.Now()
	applyOut, err := run(t, coldDir, env, terraformBin, "apply", "-input=false", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("cold apply: %v\n%s", err, tailOf(applyOut, 40))
	}
	t.Logf("cold apply: %s in %s", applyLine(applyOut), time.Since(applyStart).Round(time.Second))

	var cols []*timedColumn

	// ── stock, on its own converged state, before anything is stamped ────
	cols = append(cols, timeWithTimeline(t, proxy, latency, runs, &timedColumn{
		Label: "stock-terraform", Bin: terraformBin, Dir: coldDir,
		Endpoint: proxy.Endpoint(),
		Args:     []string{"plan", "-input=false", "-no-color"},
	}))

	// ── the migration: the same estate, adopted ──────────────────────────
	adoptedDir := filepath.Join(t.TempDir(), "adopted")
	generate(t, root, adoptedDir, scale, prefix)
	addLiveBlock(t, adoptedDir, estate)
	mustRun(t, adoptedDir, env, choudoufuBin, "init", "-input=false", "-no-color")
	importStart := time.Now()
	importOut, err := run(t, adoptedDir, env, choudoufuBin,
		"live-import", "-state="+filepath.Join(coldDir, "terraform.tfstate"), "-estate="+estate, "-approve")
	if err != nil {
		t.Fatalf("live-import -approve: %v\n%s", err, tailOf(importOut, 60))
	}
	t.Logf("live-import -approve: %s in %s", stampLine(importOut), time.Since(importStart).Round(time.Second))

	// ── choudoufu, steady state, at the default parallelism ──────────────
	cols = append(cols, timeWithTimeline(t, proxy, latency, runs, &timedColumn{
		Label: "choudoufu-live", Bin: choudoufuBin, Dir: adoptedDir,
		Endpoint: proxy.Endpoint(),
		Args:     []string{"plan", "-input=false", "-no-color"},
	}))

	// ── the control: the same binary, forced to one read at a time ───────
	// This is the reading that gives every other reading here its meaning.
	// TOFU_LIVE_READ_PARALLELISM=1 is documented to reproduce the
	// sequential loop exactly (projection.DefaultReadParallelism's own
	// comment), so if this column does not come back peak 1 the instrument
	// is not seeing the read pass at all.
	var control *timedColumn
	if os.Getenv("CONC_SERIAL_CONTROL") != "0" {
		control = timeWithTimeline(t, proxy, latency, 1, &timedColumn{
			Label: "choudoufu-live-readpar1-CONTROL", Bin: choudoufuBin, Dir: adoptedDir,
			Endpoint: proxy.Endpoint(),
			Args:     []string{"plan", "-input=false", "-no-color"},
			Extra:    []string{"TOFU_LIVE_READ_PARALLELISM=1"},
		})
		cols = append(cols, control)
	}

	// ── the knob sweeps ──────────────────────────────────────────────────
	for _, v := range listFromEnv("CONC_READ_PAR") {
		cols = append(cols, timeWithTimeline(t, proxy, latency, runs, &timedColumn{
			Label: "choudoufu-live-readpar" + v, Bin: choudoufuBin, Dir: adoptedDir,
			Endpoint: proxy.Endpoint(),
			Args:     []string{"plan", "-input=false", "-no-color"},
			Extra:    []string{"TOFU_LIVE_READ_PARALLELISM=" + v},
		}))
	}
	for _, v := range listFromEnv("CONC_SWEEP_PAR") {
		cols = append(cols, timeWithTimeline(t, proxy, latency, runs, &timedColumn{
			Label: "choudoufu-live-sweeppar" + v, Bin: choudoufuBin, Dir: adoptedDir,
			Endpoint: proxy.Endpoint(),
			Args:     []string{"plan", "-input=false", "-no-color"},
			Extra:    []string{"TOFU_LIVE_SWEEP_PARALLELISM=" + v},
		}))
	}

	reportTimelines(t, scale, latency, cols)

	if control != nil && len(control.Stats) > 0 {
		s := control.Stats[0]
		// Not the peak. A whole-run peak of 1 would require that NOTHING in
		// the process ever overlaps anything - not the two provider
		// configurations, not the estate sweep, not the tag index - and the
		// read pass is only one phase of the run. Measured at scale 1 the
		// serialised column reports peak 2 while being, by every other
		// measure, exactly the serial pass #654 described: 155 of 156
		// adjacent pairs non-overlapping and a mean of 0.97 requests in
		// flight across the whole window.
		//
		// So the control gates on those two. They cannot be reached by a
		// concurrent run: a pass that overlaps its reads cannot leave
		// almost every start-ordered pair disjoint, and it cannot average
		// one request in flight.
		fraction := 0.0
		if s.AdjacentPairs > 0 {
			fraction = float64(s.NonOverlapping) / float64(s.AdjacentPairs)
		}
		if fraction < 0.85 || s.MeanConcurrency() > 1.5 {
			t.Errorf("CONTROL FAILED: choudoufu at TOFU_LIVE_READ_PARALLELISM=1 reports %d of %d adjacent pairs non-overlapping (%.2f) at mean concurrency %.2f. "+
				"A deliberately serialised run must come back near-fully non-overlapping at a mean near one; this one did not, so this rig has not shown it can see serialisation "+
				"and no concurrency reading it gives for any other column is evidence.",
				s.NonOverlapping, s.AdjacentPairs, fraction, s.MeanConcurrency())
		} else {
			t.Logf("CONTROL PASSED: the deliberately serialised column reports %d of %d adjacent pairs non-overlapping (%.2f) at mean concurrency %.2f, peak %d. "+
				"The rig can see serialisation.", s.NonOverlapping, s.AdjacentPairs, fraction, s.MeanConcurrency(), s.Peak)
		}
	}
}

// timedColumn is one measured plan configuration and everything the run
// said about it.
type timedColumn struct {
	Label    string
	Bin      string
	Dir      string
	Endpoint string
	Args     []string
	// Extra is environment appended for this column only (the parallelism
	// knobs).
	Extra []string

	Seconds  []float64
	Calls    []int
	Verdicts []string
	Stats    []flocitest.TimelineStats
	ByAction []map[string]flocitest.TimelineStats
	Counts   []map[string]int
}

// timeWithTimeline runs one column's plan n times with the proxy's latency
// switched on, recording for each run the wall clock, the call count, the
// plan's own verdict, and the request timeline.
func timeWithTimeline(t *testing.T, proxy *flocitest.CountingProxy, latency time.Duration, n int, c *timedColumn) *timedColumn {
	t.Helper()
	proxy.SetLatency(latency)
	defer proxy.SetLatency(0)

	env := awsEnv(c.Endpoint)
	env = append(env, c.Extra...)

	for i := 0; i < n; i++ {
		beforeCalls := proxy.Total()
		beforeByAPI := copyCounts(proxy.Counts())
		mark := proxy.TimelineLen()

		start := time.Now()
		out, err := run(t, c.Dir, env, c.Bin, c.Args...)
		elapsed := time.Since(start)

		spans := proxy.SpansFrom(mark)
		stats := flocitest.Timeline(spans)

		// The verdict is read out of the plan's own output. An exit code
		// would not tell these apart: `plan` exits 0 for a plan that
		// proposes work.
		verdict := "empty"
		switch {
		case err != nil:
			verdict = "ERROR"
		case !strings.Contains(out, "No changes."):
			verdict = planSummary(out)
		}
		if verdict != "empty" {
			path := saveOutput(t, c.Label, i+1, out)
			t.Errorf("%s run %d: verdict %s - its timeline is not comparable with the other columns'; full output at %s\n%s",
				c.Label, i+1, verdict, path, changedResources(out))
		}

		c.Seconds = append(c.Seconds, elapsed.Seconds())
		c.Calls = append(c.Calls, proxy.Total()-beforeCalls)
		c.Verdicts = append(c.Verdicts, verdict)
		c.Stats = append(c.Stats, stats)
		c.ByAction = append(c.ByAction, flocitest.TimelineByAction(spans))
		c.Counts = append(c.Counts, diffCounts(beforeByAPI, proxy.Counts()))

		t.Logf("%-32s run %d: %6.2fs wall, %4d requests, peak %2d in flight, %d of %d adjacent pairs non-overlapping, mean concurrency %.2f (%s)",
			c.Label, i+1, elapsed.Seconds(), stats.Requests, stats.Peak,
			stats.NonOverlapping, stats.AdjacentPairs, stats.MeanConcurrency(), verdict)
	}
	return c
}

func reportTimelines(t *testing.T, scale int, latency time.Duration, cols []*timedColumn) {
	t.Helper()
	t.Logf("")
	t.Logf("READ-PASS CONCURRENCY REPORT scale=%d latency=%s emulator=%s commit=%s",
		scale, latency, flocitest.Image(), flocitest.HeadCommit(t))
	t.Logf("An emulator wall clock grades the machine. The load-bearing columns here are")
	t.Logf("PEAK, NON-OVERLAP and MEAN: they are properties of the caller's shape, not of")
	t.Logf("this host. Seconds are reported only as the ratio between two columns measured")
	t.Logf("minutes apart on one machine.")
	t.Logf("")
	t.Logf("%-32s %-22s %-14s %-6s %-16s %-6s", "column", "seconds", "requests", "peak", "non-overlapping", "mean")
	for _, c := range cols {
		peaks := make([]string, 0, len(c.Stats))
		nonov := make([]string, 0, len(c.Stats))
		means := make([]string, 0, len(c.Stats))
		for _, s := range c.Stats {
			peaks = append(peaks, strconv.Itoa(s.Peak))
			nonov = append(nonov, fmt.Sprintf("%d/%d", s.NonOverlapping, s.AdjacentPairs))
			means = append(means, fmt.Sprintf("%.2f", s.MeanConcurrency()))
		}
		t.Logf("%-32s %-22s %-14s %-6s %-16s %-6s",
			c.Label, joinFloats(c.Seconds), joinInts(c.Calls),
			strings.Join(peaks, ","), strings.Join(nonov, ","), strings.Join(means, ","))
	}

	// Per-action, last run of each column: which calls were the serial ones.
	for _, c := range cols {
		if len(c.ByAction) == 0 {
			continue
		}
		last := c.ByAction[len(c.ByAction)-1]
		type row struct {
			action string
			s      flocitest.TimelineStats
		}
		rows := make([]row, 0, len(last))
		for a, s := range last {
			rows = append(rows, row{a, s})
		}
		// Biggest contributors to serial time first: an action's own Sum is
		// how long the run spent inside it, and its peak says whether that
		// time was spent one at a time.
		sort.Slice(rows, func(i, j int) bool { return rows[i].s.Sum > rows[j].s.Sum })
		t.Logf("")
		t.Logf("%s: per-action timeline, last run (sorted by time spent in the action)", c.Label)
		t.Logf("  %-46s %6s %5s %14s %8s", "action", "calls", "peak", "non-overlapping", "sum")
		shown := 0
		for _, r := range rows {
			if shown >= 20 {
				t.Logf("  ... and %d more actions", len(rows)-shown)
				break
			}
			t.Logf("  %-46s %6d %5d %14s %8s",
				r.action, r.s.Requests, r.s.Peak,
				fmt.Sprintf("%d/%d", r.s.NonOverlapping, r.s.AdjacentPairs),
				r.s.Sum.Round(time.Millisecond))
			shown++
		}
	}
}

// ── env helpers ─────────────────────────────────────────────────────────

func intFromEnv(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		t.Fatalf("%s=%q is not a non-negative integer", name, v)
	}
	return n
}

func listFromEnv(name string) []string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
