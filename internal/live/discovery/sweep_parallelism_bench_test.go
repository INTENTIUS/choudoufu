// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// This is issue #605's measurement: what does making the estate-wide sweep
// concurrent cost and buy?
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestSweepParallelismAgainstFloci -v -timeout 30m
//	SWEEP_PAR_SCALE=4 TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestSweepParallelismAgainstFloci -v -timeout 40m
//
// It is [runSweepSplitBenchmark]'s Request shape - the production one
// internal/command/live_plan.go's statelessDiscoverOne builds, TaggingSweep
// included, so the native leg it measures is the ~500-call one issue #605 is
// about and not a synthetic full-table sweep - repeated against ONE estate in
// ONE container at several [Request.SweepParallelism] settings.
//
// Repeating rather than running a process per setting is the point.
// Discover is a read: it mutates nothing, so the estate the second pass sees
// is the estate the first pass saw, and every setting is measured on the same
// fixture, the same emulator pin and the same machine within seconds of the
// others. A warm-up pass runs first and is discarded, and parallelism 1 is
// measured twice - first and last - so run-to-run drift is visible in the
// output rather than assumed away.
//
// # Read the call counts, not the wall clock
//
// The wall-clock win here will look small, and that is a property of the
// emulator rather than of the change. floci answers over a loopback socket;
// real AWS answered #578's measurement at 0.367s per call. Overlapping
// calls that take microseconds saves microseconds. API CALL COUNTS are the
// primary figure and they must be IDENTICAL at every setting - if they move,
// the change is doing more than overlapping. Throttle counts are reported
// per setting for the same reason: floci does not throttle (#567), so a zero
// here is a fact about floci and never a licence to raise the default.
//
// # Measured (2026-08-30, floci pin sha256:c55d74e1, darwin/arm64)
//
// Two scales, each one process, one container, one apply, five Discover
// passes after a discarded warm-up:
//
//	scale  instances  par  calls  pages  throttle  discover   vs par=1
//	1      79         1    558    0      0         433.6ms    1.00x
//	1      79         2    558    0      0         266.4ms    1.63x
//	1      79         10   558    0      0         188.9ms    2.30x
//	1      79         20   558    0      0         154.9ms    2.80x
//	1      79         1    558    0      0         357.4ms    (drift check)
//	4      301        1    591    0      0         419.4ms    1.00x
//	4      301        2    591    0      0         286.7ms    1.46x
//	4      301        10   591    0      0         219.2ms    1.91x
//	4      301        20   591    0      0         173.1ms    2.42x
//	4      301        1    591    0      0         355.7ms    (drift check)
//
// 558 at scale 1 is #578's own figure for this estate, unchanged - which is
// the point. Calls are byte-for-byte identical per API action at every
// setting, at both scales; so are the sweep's scan-row order, the diagnostic
// sequence, and the bound/unclaimed/covered counts. Throttling never fired,
// which says nothing except that floci does not throttle.
//
// The two parallelism-1 rows differ by ~18% (433.6ms and 357.4ms; 419.4ms and
// 355.7ms), so treat the speedup column as approximate. Against the LOWER
// sequential figure, parallelism 10 is 1.89x and 1.62x rather than 2.30x and
// 1.91x.
//
// # Why 2-3x here and not 10x
//
// Roughly half of this run's discover time is work that does not overlap: the
// config-driven scan, the one tagging GetResources call, and the consuming
// loop's own per-object bookkeeping, which is deliberately sequential. Over
// loopback that half is comparable to the call time, so the ceiling is about
// 2x whatever the parallelism. Against real AWS the same calls cost 0.367s
// each (#578) and the bookkeeping is unchanged, so the non-overlappable share
// is a fraction of a percent and the ceiling is the parallelism itself. The
// figure this benchmark establishes is therefore the CALL PARITY and the
// determinism; the wall-clock column is context, and the real-AWS projection
// (521 native calls x 0.39s = 203s sequential, ~20s at 10) stays a projection
// until somebody runs it against an account.
func TestSweepParallelismAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/sweep-parallelism")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	scale := sweepParScale(t)
	rows := runSweepParallelismBenchmark(t, scale)
	for _, r := range rows {
		t.Logf("%s", r)
	}
	for _, line := range sweepParDeltaLines(rows) {
		t.Logf("  %s", line)
	}
}

func sweepParScale(t *testing.T) int {
	t.Helper()
	v := os.Getenv("SWEEP_PAR_SCALE")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("SWEEP_PAR_SCALE=%q is not a positive integer", v)
	}
	return n
}

// sweepParSettings is the parallelism ladder, with 1 at both ends so that
// drift between the first and last measurement is reported rather than
// hidden.
var sweepParSettings = []int{1, 2, 10, 20, 1}

type sweepParRow struct {
	Label       string
	Parallelism int
	Scale       int
	Instances   int

	Elapsed  time.Duration
	Calls    int
	Pages    int
	Throttle int

	ByAPI map[string]int

	SweepCovered int
	Scans        int
	Bound        int
	Unclaimed    int

	// ScanOrder is the sha256-free but complete evidence that concurrency
	// changed no ORDER: the type names of the sweep's scan rows, joined, in
	// the order the run filed them. Every setting must produce the same
	// string.
	ScanOrder string

	// Diags is the run's whole diagnostic sequence, rendered and normalized.
	// Same rule.
	Diags string
}

// requestIDPattern is why comparing rendered diagnostics needs a normalizer
// rather than a byte compare, and it is worth writing down because the first
// run of this benchmark reported every setting's diagnostics as different -
// INCLUDING two parallelism-1 runs against each other, which is what gave it
// away.
//
// A provider list failure carries the AWS SDK's own error text, and that text
// embeds the service's per-request RequestID: a fresh UUID on every call, at
// every setting, concurrent or not. Two identical sequential runs differ in
// exactly those bytes and in nothing else. Normalizing them out is what makes
// the comparison a check on ORDER and CONTENT, which is what issue #605's
// determinism constraint is about, rather than a check on whether the
// emulator issued the same UUIDs twice, which it never will.
var requestIDPattern = regexp.MustCompile(`RequestID: [0-9a-fA-F-]+`)

func normalizeDiagText(s string) string {
	return requestIDPattern.ReplaceAllString(s, "RequestID: <normalized>")
}

func (r sweepParRow) String() string {
	return fmt.Sprintf(
		"SWEEP PARALLELISM: %-8s parallelism=%-3d scale=%d instances=%d calls=%d pages=%d throttle=%d "+
			"discover=%s sweep_covered=%d scans=%d bound=%d unclaimed=%d [emulator=floci]",
		r.Label, r.Parallelism, r.Scale, r.Instances, r.Calls, r.Pages, r.Throttle,
		r.Elapsed, r.SweepCovered, r.Scans, r.Bound, r.Unclaimed,
	)
}

// sweepParDeltaLines is the comparison the issue accepts on, computed rather
// than eyeballed: every setting against the first parallelism-1 row.
func sweepParDeltaLines(rows []sweepParRow) []string {
	if len(rows) == 0 {
		return nil
	}
	var base *sweepParRow
	for i := range rows {
		if rows[i].Parallelism == 1 {
			base = &rows[i]
			break
		}
	}
	if base == nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		speedup := 0.0
		if r.Elapsed > 0 {
			speedup = float64(base.Elapsed) / float64(r.Elapsed)
		}
		out = append(out, fmt.Sprintf(
			"%-8s par=%-3d calls %+d (%d vs %d) throttle %+d wall %s vs %s (%.2fx) scan_order_same=%v diags_same=%v",
			r.Label, r.Parallelism,
			r.Calls-base.Calls, r.Calls, base.Calls,
			r.Throttle-base.Throttle,
			r.Elapsed, base.Elapsed, speedup,
			r.ScanOrder == base.ScanOrder, r.Diags == base.Diags,
		))
	}
	return out
}

func runSweepParallelismBenchmark(t *testing.T, scale int) []sweepParRow {
	t.Helper()

	root := flocitest.RepoRoot(t)
	dir := t.TempDir()
	prefix := fmt.Sprintf("sp%d", os.Getpid()%100000)

	genCmd := exec.Command("go", "run", "./tools/terralith-gen", //nolint:gosec // fixed binary and args, test-only
		"-scale", strconv.Itoa(scale), "-prefix", prefix, "-out", dir)
	genCmd.Dir = root
	genOut, err := genCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run ./tools/terralith-gen -scale %d: %v\n%s", scale, err, genOut)
	}
	t.Logf("terralith-gen: %s", strings.TrimSpace(string(genOut)))

	flociPort := flocitest.StartFloci(t, "cdf-sweeppar")
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(flociPort))

	t.Setenv("AWS_ENDPOINT_URL", proxy.Endpoint())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)
	cfg := loadModuleConfig(t, dir)

	schema := provider.GetProviderSchema(context.Background())
	if schema.Diagnostics.HasErrors() {
		t.Fatalf("reading the AWS provider schema: %s", schema.Diagnostics.Err())
	}
	resolveResult, resolveDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{Schemas: schema.ResourceTypes})
	if resolveDiags.HasErrors() {
		t.Logf("identity resolution diagnostics (%d), not fatal to this benchmark:\n%s", len(resolveDiags), renderDiags(resolveDiags))
	}
	resolutions := resolveResult.All()

	roster, rosterErr := registry.Embedded()
	if rosterErr != nil {
		t.Fatalf("loading the embedded roster: %v", rosterErr)
	}
	ccCfg := cloudcontrol.Config{Endpoint: proxy.Endpoint(), Region: awsRegion}

	one := func(label string, parallelism int) sweepParRow {
		proxy.Reset()
		start := time.Now()
		res, diags := Discover(context.Background(), Request{
			Estate:           "sweep-par-cohort",
			Config:           cfg,
			Resolutions:      resolutions,
			Provider:         provider,
			Region:           awsRegion,
			CollectUnclaimed: true,
			Sweep:            true,
			Roster:           roster,
			CloudControl:     cloudcontrol.New(ccCfg),
			Tagging:          cloudcontrol.NewTagging(ccCfg),
			TaggingSweep:     true,
			SweepParallelism: parallelism,
		})
		elapsed := time.Since(start)

		if len(res.sweepPrefetchWasted) != 0 {
			t.Errorf("%s: the sweep prefetched %v and never used them, so this run made list calls the sequential loop would not have", label, res.sweepPrefetchWasted)
		}
		if res.sweepPrefetchMismatched != 0 {
			t.Errorf("%s: %d prefetched answers were fetched with a configuration the scan disagreed with", label, res.sweepPrefetchMismatched)
		}

		byAPI := proxy.Counts()
		return sweepParRow{
			Label:        label,
			Parallelism:  parallelism,
			Scale:        scale,
			Instances:    len(resolutions),
			Elapsed:      elapsed,
			Calls:        sumCounts(byAPI),
			Pages:        proxy.PaginationTotal(),
			Throttle:     proxy.ThrottleTotal(),
			ByAPI:        byAPI,
			SweepCovered: len(res.SweepCovered),
			Scans:        len(res.Scans),
			Bound:        len(res.Bindings),
			Unclaimed:    len(res.Unclaimed),
			ScanOrder:    strings.Join(sweptScanOrder(res), ","),
			Diags:        normalizeDiagText(renderDiags(diags)),
		}
	}

	// Discarded: the first pass pays whatever the provider process and the
	// emulator have left cold, and attributing that to parallelism 1 would
	// flatter every setting after it.
	warm := one("warmup", 1)
	t.Logf("(discarded) %s", warm)

	rows := make([]sweepParRow, 0, len(sweepParSettings))
	for i, par := range sweepParSettings {
		rows = append(rows, one(fmt.Sprintf("run%d", i+1), par))
	}

	// The acceptance assertions, computed from the rows just measured
	// rather than read off the log by a human.
	base := rows[0]
	for _, r := range rows[1:] {
		if r.Calls != base.Calls {
			t.Errorf("parallelism %d made %d API calls, parallelism %d made %d - concurrency must overlap the waiting and nothing else", r.Parallelism, r.Calls, base.Parallelism, base.Calls)
		}
		for _, action := range sortedKeys(r.ByAPI, base.ByAPI) {
			if r.ByAPI[action] != base.ByAPI[action] {
				t.Errorf("parallelism %d called %s %d times, parallelism %d called it %d times", r.Parallelism, action, r.ByAPI[action], base.Parallelism, base.ByAPI[action])
			}
		}
		if r.ScanOrder != base.ScanOrder {
			t.Errorf("parallelism %d filed its sweep scan rows in a different order than parallelism %d:\n%s\n%s", r.Parallelism, base.Parallelism, r.ScanOrder, base.ScanOrder)
		}
		if r.Diags != base.Diags {
			t.Errorf("parallelism %d produced a different diagnostic sequence than parallelism %d:\n--- %d ---\n%s\n--- %d ---\n%s", r.Parallelism, base.Parallelism, r.Parallelism, r.Diags, base.Parallelism, base.Diags)
		}
		if r.Bound != base.Bound || r.Unclaimed != base.Unclaimed || r.SweepCovered != base.SweepCovered || r.Scans != base.Scans {
			t.Errorf("parallelism %d found a different estate than parallelism %d: bound %d/%d unclaimed %d/%d covered %d/%d scans %d/%d",
				r.Parallelism, base.Parallelism, r.Bound, base.Bound, r.Unclaimed, base.Unclaimed, r.SweepCovered, base.SweepCovered, r.Scans, base.Scans)
		}
	}

	return rows
}

func sortedKeys(a, b map[string]int) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
