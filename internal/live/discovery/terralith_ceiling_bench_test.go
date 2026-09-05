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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// This is issue #565's benchmark (child C of epic #546, "establish floci's
// ceiling"): the same Discover+BuildFrom pipeline scale_bench_test.go
// measures for a single client-named type (aws_s3_bucket, issue #64), run
// instead against tools/terralith-gen's output (issue #564) - few TYPES,
// many INSTANCES, deliberate duplication, and (unlike scale_bench_test.go's
// estate) genuinely stock-Terraform-shaped: no tofu-estate/tofu-address
// marker anywhere. That absence is deliberate, not an oversight - it is
// what makes this the estate-wide, CollectUnclaimed=true scan every real
// adoption has to pay (doc.go: CollectUnclaimed "switches the scan from
// the server-side [tag] filter to listing everything"), rather than the
// narrow, already-tagged scan scale_bench_test.go's single-cohort fixture
// exercises.
//
// # What this measures, and what it deliberately does not
//
// This runs identity.Resolve + Discover + projection.BuildFrom - the
// stateless-discovery pipeline, with the state file deleted after apply so
// nothing here leans on it (mirrors scale_bench_test.go's
// runScaleBenchmark). It does NOT run live-import
// (internal/command/live_import.go), which reads an existing tfstate as its
// migration bridge and is issue #566's own subject ("the migration
// measurement: stock state -> choudoufu at scale"). This file answers a
// narrower, prior question: at what estate size does floci itself - not
// choudoufu's code - start dominating the very same read pattern
// live-import will also have to pay for every server-assigned
// (needs-discovery) type.
//
// # Measured scaling (2026-08-30, floci pin sha256:c55d74e1, darwin/arm64)
//
// Six tiers, each its own `go test` process (TERRALITH_CEILING_SCALE=N),
// spanning a 73x resource-count range:
//
//	scale  resources  calls  pagination  throttle   apply       discover    build      harness_rss  floci_rss  materialized
//	1      55         77     0           0          31.7s       54.1ms      158.1ms    227MB        245MB      28
//	4      205        257    0           0          78.0s       82.1ms      453.3ms    225MB        304MB      109
//	10     505        617    0           0          189.9s      98.5ms      765.5ms    226MB        255MB      271
//	20     1005       1217   0           0          373.5s      155.3ms     1420.3ms   243MB        276MB      541
//	40     2005       2417   0           0          743.7s      323.0ms     3270.1ms   267MB        291MB      1081
//	80     4005       4817   0           0          1499.3s     451.6ms     5298.5ms   288MB        298MB      2161
//
// (harness_rss/floci_rss are peak values sampled during the Discover+Build
// window only - see PeakHarnessRSSKB/PeakFlociRSSKB's own doc comments for
// what each process is.)
//
// Per-unit rates make the shape legible: apply cost is flat at
// ~0.37s/resource from scale=4 onward (0.577, 0.380, 0.376, 0.372, 0.371,
// 0.374 s/resource) - floci's own per-create latency, linear with no sign
// of degradation even with 4005 objects in one account. discover cost per
// API call FALLS as N grows (0.70, 0.32, 0.16, 0.13, 0.13, 0.09 ms/call) -
// the opposite of a dominance signal. build cost per materialized instance
// is flat after the small-N startup effect (5.65, 4.16, 2.82, 2.63, 3.02,
// 2.45 ms/instance). Peak process memory - both choudoufu's own harness and
// floci's container - stayed within a ~225-300MB band across the whole
// range, not proportional to resource count.
//
// pagination_total reads zero at EVERY tier, including scale=80's 480
// aws_iam_policy instances (4.8x real AWS's documented 100-item default
// page size for IAM ListPolicies) and 80 aws_ecs_task_definition instances
// in one aws_ecs_task_definition ListTaskDefinitions call. Confirmed by a
// direct API probe outside choudoufu entirely (no terraform, no provider,
// plain `aws` CLI against a fresh floci container): 150 IAM policies and
// 120 ECS task definitions, `--max-items`/`--max-results` given explicitly,
// both come back in one response with IsTruncated/nextToken unset. This is
// an emulator gap, not an artifact of these tiers being too small - see
// lex00/floci#185. throttle_total is also zero at every tier, including the
// 4817-call scale=80 run; floci applies no rate limiting in this range.
//
// # Stale as of #574, re-verified only at scale=1 (issue #708)
//
// terralith-gen has emitted a module-nested bucket ("modules/team_pod",
// issue #574) since 2026-08-31, after the table above was recorded. That
// changed the resource count AT EVERY TIER - the table's own scale/resource
// pairing no longer holds for the current generator:
//
//	scale  table's resources (stale)  current resources (go run ./tools/terralith-gen -scale N)
//	1      55                         79
//	4      205                        301
//	10     505                        745
//	20     1005                       1485
//	40     2005                       2965
//	80     4005                       5925
//
// #708 fixed this file's own fixture loader (discovery_test.go's
// loadConfig), which refused the module call outright and so could not run
// this benchmark AT ALL against the current generator - not a resource-count
// drift, a hard failure at every tier. Re-verified directly against floci
// post-fix, scale=1 only (a maintainer scope note during #708 bounded that
// worker's own testing to this one tier, to keep per-worker cost down across
// a parallel batch):
//
//	scale=1 resources=79 types=13(needs-discovery=6) apply=31.656379583s discover=56.42925ms build=91.390542ms api_calls_total=113 pagination_total=0 throttle_total=0 peak_harness_rss_kb=234416 peak_floci_rss_kb=258764 materialized=44 bound=0 unbound=15 unclaimed=20 problems=11 [emulator=floci]
//
// resources and api_calls_total both grew (55->79, 77->113) by roughly the
// same ~1.44x the module bucket adds at every tier, matching the table
// above; apply, discover and build all stayed within the same order of
// magnitude as the old scale=1 row, so nothing in this one tier suggests the
// module shape is itself expensive. Scales 4/10/20/40/80 have NOT been
// re-measured against the current generator - #838 tracks re-running all
// six tiers and replacing this table for real, per this section's own
// convention (date, floci pin, platform, per-tier table, per-unit-rate
// commentary, restated ceiling). Until #838 lands, "The stated ceiling"
// below is evidence from the stale, smaller-resource-count generator, not a
// claim re-established against today's terralith-gen output.
//
// # The stated ceiling
//
// No wall was found, in the measured range (55-4005 resources / 77-4817 API
// calls), in any of the metrics that reflect choudoufu's OWN code: API call
// count, discovery time, build/materialization time, and peak process
// memory all stay flat or improve as the estate grows. Floci-backed
// measurements of THOSE components are trustworthy at least through
// ~4000 resources / ~4800 API calls (this run's own top tier) - the epic's
// "roughly N resources" framing does not apply to them because no ceiling
// showed up to look for. See the section above: this paragraph's numbers
// predate #574's module expansion and are unverified past scale=1 against
// the current generator.
//
// Two components of #546's central claim are a DIFFERENT kind of ceiling,
// though, and it has nothing to do with resource count: list pagination
// volume and throttling cannot be measured against floci AT ANY SCALE,
// because the emulator does not implement the AWS behavior being measured
// (confirmed above and in lex00/floci#185) - not "the estate needs to be
// bigger" but "no floci-backed N will ever produce a nonzero answer here".
// Those two components have to come from real AWS (#546E/#567) regardless
// of how large a floci-backed estate grows.
//
// The practical limit on iterating further at THIS tier is apply/teardown
// wall-clock time, not measurement validity: scale=80 alone cost ~25
// minutes just to stand up (linear at ~0.37s/resource), which is what
// stopped this benchmark's own climb, not a signal that anything above it
// would behave differently.
//
// See TestTerralithCeilingAgainstFloci's doc comment for how to reproduce
// any tier.
//
// # Teardown
//
// Each scale tier's own `go test` process starts a fresh floci container
// (flocitest.StartFloci) and tears it down - "docker rm -f", the entire
// synthetic account gone - via that process's own t.Cleanup when the
// process exits, before the next (larger) tier is ever started by hand.
// That satisfies #546's "teardown is exercised deliberately at each tier
// before growing to the next" without a separate terraform destroy: the
// state file was already deleted (like scale_bench_test.go) to exercise
// the no-state discovery path, so terraform itself has nothing left to
// destroy with - discarding the whole ephemeral account is the equivalent
// operation. Verified after this file's own six-tier run: no
// "cdf-ceiling-*" container remained (`docker ps -a`).
const terralithCeilingEstate = "ceiling-cohort"

// terralithCeilingScale reads TERRALITH_CEILING_SCALE, defaulting to 1 -
// the smallest tier issue #564 already proved applies and destroys
// cleanly, so a bare run of this test never jumps ahead of what is known
// to work.
func terralithCeilingScale(t *testing.T) int {
	t.Helper()
	v := os.Getenv("TERRALITH_CEILING_SCALE")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("TERRALITH_CEILING_SCALE=%q is not a positive integer", v)
	}
	return n
}

// TestTerralithCeilingAgainstFloci is issue #565's own measurement.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestTerralithCeilingAgainstFloci -v -timeout 20m
//	TERRALITH_CEILING_SCALE=10 TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestTerralithCeilingAgainstFloci -v -timeout 20m
//
// A tier above ~40 (2005 resources) needs a longer -timeout: apply alone
// took ~25 minutes at scale=80 (4005 resources) on the machine that
// recorded this file's own "Measured scaling" table above.
//
// Never asserts a pass/fail threshold on the measured numbers - unlike
// scale_bench_test.go's ratchet, there is no committed budget here to
// compare against, because the deliverable is the ceiling itself: a
// statement backed by evidence at named tiers, not a number a future run
// could regress. See this file's own package doc comment ("Measured
// scaling" and "The stated ceiling") for what six such runs found.
func TestTerralithCeilingAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/terralith-ceiling")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	scale := terralithCeilingScale(t)
	report := runTerralithCeilingBenchmark(t, scale)
	t.Logf("%s", report)
	logTerralithByAPI(t, report)
	logTerralithScans(t, report)
}

// TestLoadConfigHandlesCurrentTerralithGenOutput is issue #708's guard
// against a repeat of its own defect: TestTerralithCeilingAgainstFloci's
// fixture loader (discovery_test.go's loadConfig) fell behind
// tools/terralith-gen's own module-nested bucket (issue #574) with nothing
// noticing until someone ran the floci-gated benchmark by hand - a plain
// `go test ./...` never touches it, because TestTerralithCeilingAgainstFloci
// itself skips unless TF_ACC or TF_FLOCI_TEST is set (flocitest.Gate).
//
// This test generates the CURRENT terralith-gen output - no docker, no
// terraform apply, no floci, so it runs in an ordinary `go test` and cannot
// rot silently the same way - and loads it through the exact same
// loadConfig the benchmark calls, asserting the module call actually
// resolved (a child config for "team_pod" exists and at least one
// resolution comes back) rather than only that loadConfig returned without
// a fatal.
func TestLoadConfigHandlesCurrentTerralithGenOutput(t *testing.T) {
	root := flocitest.RepoRoot(t)
	dir := t.TempDir()

	genCmd := exec.Command("go", "run", "./tools/terralith-gen", //nolint:gosec // fixed binary and args, test-only
		"-scale", "1", "-prefix", "lc", "-out", dir)
	genCmd.Dir = root
	if out, err := genCmd.CombinedOutput(); err != nil {
		t.Fatalf("go run ./tools/terralith-gen -scale 1: %v\n%s", err, out)
	}

	cfg := loadConfig(t, dir)
	if cfg == nil {
		t.Fatal("loadConfig returned a nil config for terralith-gen's current output")
	}
	if _, ok := cfg.Children["team_pod"]; !ok {
		t.Fatal(`loaded config has no "team_pod" child module - terralith-gen's own module-nested bucket (issue #574) is missing from the loaded tree, so this fixture is no longer exercising the module-call shape it exists to guard`)
	}

	res, diags := identity.Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Logf("identity resolution diagnostics (%d) - not fatal to this guard, which only asserts loadConfig itself resolves the module call, not that every instance resolves cleanly:\n%s", len(diags), renderDiags(diags))
	}
	if len(res.All()) == 0 {
		t.Fatal("identity.Resolve produced zero resolutions from terralith-gen's current output loaded through loadConfig")
	}
}

// terralithCeilingReport is one benchmark run's measurements, structured so
// that "we made few calls but streamed a lot" and "we were throttled" read
// as different fields rather than one wall-clock (issue #565's explicit
// ask).
type terralithCeilingReport struct {
	Scale          int
	TotalResources int
	Types          int // distinct declared resource types
	NeedsDiscovery int // of Types, how many are server-assigned (genuinely list-based)

	ApplyElapsed    time.Duration
	DiscoverElapsed time.Duration
	BuildElapsed    time.Duration

	CallsTotal      int
	CallsByAPI      map[string]int
	PaginationTotal int
	PaginationByAPI map[string]int
	ThrottleTotal   int
	ThrottleByAPI   map[string]int

	// PeakHarnessRSSKB is this test process's own peak resident set size in
	// KB, sampled every 100ms during the Discover+BuildFrom window only
	// (the phase under test) via `ps -o rss=`. This is choudoufu's own
	// process, running in-process discovery/projection code - it excludes
	// the AWS provider subprocess (a separate PID, launched by
	// launchAWSProvider) and floci itself.
	PeakHarnessRSSKB int64

	// PeakFlociRSSKB is the floci container's own peak memory in KB over
	// the same window, sampled via `docker stats --no-stream`. This is the
	// emulator's memory, not choudoufu's - reported separately so a memory
	// ceiling can be attributed to the right side.
	PeakFlociRSSKB int64

	Materialized int
	Bound        int
	Unbound      int
	Unclaimed    int
	Problems     int

	Scans []scanSummary
}

type scanSummary struct {
	TypeName  string
	NeedsDisc bool
	Declared  int
	Listed    int
	Bound     int
	Unclaimed int
	Sweep     bool
}

func (r terralithCeilingReport) String() string {
	return fmt.Sprintf(
		"TERRALITH CEILING BENCHMARK: scale=%d resources=%d types=%d(needs-discovery=%d) "+
			"apply=%s discover=%s build=%s "+
			"api_calls_total=%d pagination_total=%d throttle_total=%d "+
			"peak_harness_rss_kb=%d peak_floci_rss_kb=%d "+
			"materialized=%d bound=%d unbound=%d unclaimed=%d problems=%d [emulator=floci]",
		r.Scale, r.TotalResources, r.Types, r.NeedsDiscovery,
		r.ApplyElapsed, r.DiscoverElapsed, r.BuildElapsed,
		r.CallsTotal, r.PaginationTotal, r.ThrottleTotal,
		r.PeakHarnessRSSKB, r.PeakFlociRSSKB,
		r.Materialized, r.Bound, r.Unbound, r.Unclaimed, r.Problems,
	)
}

func logTerralithByAPI(t *testing.T, report terralithCeilingReport) {
	t.Helper()
	keys := make([]string, 0, len(report.CallsByAPI))
	for k := range report.CallsByAPI {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  %-40s calls=%-6d pagination=%-6d throttle=%d",
			k, report.CallsByAPI[k], report.PaginationByAPI[k], report.ThrottleByAPI[k])
	}
}

func logTerralithScans(t *testing.T, report terralithCeilingReport) {
	t.Helper()
	for _, s := range report.Scans {
		disc := ""
		if s.NeedsDisc {
			disc = " needs-discovery"
		}
		sweep := ""
		if s.Sweep {
			sweep = " SWEEP"
		}
		t.Logf("  scan %-30s declared=%-4d listed=%-4d bound=%-4d unclaimed=%-4d%s%s",
			s.TypeName, s.Declared, s.Listed, s.Bound, s.Unclaimed, disc, sweep)
	}
}

// runTerralithCeilingBenchmark generates a scale-N terralith
// (tools/terralith-gen), applies it with stock terraform, deletes the
// state file (mirrors scale_bench_test.go's runScaleBenchmark: discovery
// must recover the estate with no state to lean on), and runs
// identity.Resolve + Discover(CollectUnclaimed=true) + projection.BuildFrom
// - counting every AWS API call through the flocitest.CountingProxy
// (issue #64), separated into pagination and throttle counts (issue #565),
// with the test harness's own and floci's own peak memory sampled across
// the Discover+BuildFrom window.
func runTerralithCeilingBenchmark(t *testing.T, scale int) terralithCeilingReport {
	t.Helper()

	root := flocitest.RepoRoot(t)
	dir := t.TempDir()
	prefix := fmt.Sprintf("cb%d", os.Getpid()%100000)

	genCmd := exec.Command("go", "run", "./tools/terralith-gen", //nolint:gosec // fixed binary and args, test-only
		"-scale", strconv.Itoa(scale), "-prefix", prefix, "-out", dir)
	genCmd.Dir = root
	genOut, err := genCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run ./tools/terralith-gen -scale %d: %v\n%s", scale, err, genOut)
	}
	t.Logf("terralith-gen: %s", strings.TrimSpace(string(genOut)))

	flociPort := flocitest.StartFloci(t, "cdf-ceiling")
	flociName := findFlociContainer(t, flociPort)
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(flociPort))

	t.Setenv("AWS_ENDPOINT_URL", proxy.Endpoint())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")

	applyStart := time.Now()
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	applyElapsed := time.Since(applyStart)

	// The benchmark is about discovery/projection's read cost, not the
	// apply that manufactured the estate.
	proxy.Reset()

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)
	cfg := loadConfig(t, dir)

	// Unlike scale_bench_test.go's single-type (aws_s3_bucket) estate, a
	// terralith declares cross-resource references in plain config (a DNS
	// record's name built from its zone's own name argument -
	// aws_route53_record.rec_N.name reads aws_route53_zone.main.name) -
	// exactly the shape a real terralith has and estate-gen's one-block-
	// per-type fixtures never do. identity.Resolve needs the provider's own
	// schemas to confirm such a reference names a real attribute at all
	// (see identity.Context.Schemas's doc comment); without them, every
	// such reference refuses outright. This benchmark is about floci's
	// cost, not resolution correctness, so a residual refusal after
	// schemas are supplied is logged and excluded from the resolutions
	// Discover sees - not fatal, and not silently hidden either.
	schema := provider.GetProviderSchema(context.Background())
	if schema.Diagnostics.HasErrors() {
		t.Fatalf("reading the AWS provider schema: %s", schema.Diagnostics.Err())
	}
	resolveResult, resolveDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{Schemas: schema.ResourceTypes})
	if resolveDiags.HasErrors() {
		t.Logf("identity resolution diagnostics (%d), not fatal to this benchmark - excluded instances are simply absent from what Discover/BuildFrom below sees:\n%s",
			len(resolveDiags), renderDiags(resolveDiags))
	}
	resolutions := resolveResult.All()

	declaredTypes := map[string]int{}        // type -> instance count
	needsDiscoveryTypes := map[string]bool{} // type -> at least one NEEDS_DISCOVERY instance
	for _, r := range resolutions {
		typeName := r.Addr.Resource.Resource.Type
		declaredTypes[typeName]++
		if r.Class == identity.ClassNeedsDiscovery {
			needsDiscoveryTypes[typeName] = true
		}
	}

	stopMem := startMemorySampler(t, flociName)

	discoverStart := time.Now()
	res, diags := Discover(context.Background(), Request{
		Estate:           terralithCeilingEstate,
		Config:           cfg,
		Resolutions:      resolutions,
		Provider:         provider,
		Region:           awsRegion,
		CollectUnclaimed: true,
	})
	discoverElapsed := time.Since(discoverStart)
	if diags.HasErrors() {
		// A stock-shaped, unmarked estate legitimately produces
		// diagnostics (declared-but-unbound instances, orphan/collision
		// edge cases) - this benchmark is measuring floci's cost under the
		// real CollectUnclaimed scan, not asserting a clean migration
		// outcome (that correctness question is issue #566's). Logged, not
		// fatal, so the timing/call-count measurement below still runs.
		t.Logf("Discover diagnostics (%d), not fatal to this benchmark:\n%s", len(diags), renderDiags(diags))
	}

	provs := projection.SingleProvider(addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
	}, provider)
	buildStart := time.Now()
	proj, projDiags := projection.BuildFrom(context.Background(), cfg, res.Resolutions, provs)
	buildElapsed := time.Since(buildStart)
	if projDiags.HasErrors() {
		t.Logf("BuildFrom diagnostics (%d), not fatal to this benchmark:\n%s", len(projDiags), renderDiags(projDiags))
	}

	peakHarnessKB, peakFlociKB := stopMem()

	scans := make([]scanSummary, 0, len(res.Scans))
	for _, s := range res.Scans {
		scans = append(scans, scanSummary{
			TypeName:  s.TypeName,
			NeedsDisc: needsDiscoveryTypes[s.TypeName],
			Declared:  s.Declared,
			Listed:    s.Listed,
			Bound:     s.Bound,
			Unclaimed: s.Unclaimed,
			Sweep:     s.Sweep,
		})
	}
	sort.Slice(scans, func(i, j int) bool { return scans[i].TypeName < scans[j].TypeName })

	return terralithCeilingReport{
		Scale:            scale,
		TotalResources:   len(resolutions),
		Types:            len(declaredTypes),
		NeedsDiscovery:   len(needsDiscoveryTypes),
		ApplyElapsed:     applyElapsed,
		DiscoverElapsed:  discoverElapsed,
		BuildElapsed:     buildElapsed,
		CallsTotal:       proxy.Total(),
		CallsByAPI:       proxy.Counts(),
		PaginationTotal:  proxy.PaginationTotal(),
		PaginationByAPI:  proxy.PaginationCounts(),
		ThrottleTotal:    proxy.ThrottleTotal(),
		ThrottleByAPI:    proxy.ThrottleCounts(),
		PeakHarnessRSSKB: peakHarnessKB,
		PeakFlociRSSKB:   peakFlociKB,
		Materialized:     len(proj.Materialized),
		Bound:            len(res.Bindings),
		Unbound:          len(res.Unbound),
		Unclaimed:        len(res.Unclaimed),
		Problems:         len(res.Problems),
		Scans:            scans,
	}
}

// findFlociContainer resolves the container name flocitest.StartFloci
// picked, by asking docker which container publishes hostPort - the port
// is the only handle StartFloci's return value gives a caller, and
// startMemorySampler needs the name for `docker stats`.
func findFlociContainer(t *testing.T, hostPort string) string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--filter", "publish="+hostPort, "--format", "{{.Names}}").CombinedOutput()
	if err != nil {
		t.Logf("finding floci's container name (memory sampling will be skipped): %v\n%s", err, out)
		return ""
	}
	name := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return name
}

// startMemorySampler starts a background goroutine sampling this process's
// own RSS (`ps -o rss= -p <pid>`) and, when flociContainer is non-empty,
// floci's container memory (`docker stats --no-stream`) every 100ms. The
// returned stop function halts sampling and returns the two peaks in KB.
// Best-effort throughout: a sampling failure (ps/docker missing mid-run,
// container already gone) is logged once and that series just stops
// contributing, rather than failing the whole benchmark over an
// informational number.
func startMemorySampler(t *testing.T, flociContainer string) (stop func() (peakHarnessKB, peakFlociKB int64)) {
	t.Helper()

	pid := os.Getpid()
	var harnessPeak, flociPeak int64
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if kb, ok := sampleRSSKB(pid); ok {
					casMax(&harnessPeak, kb)
				}
				if flociContainer != "" {
					if kb, ok := sampleDockerRSSKB(flociContainer); ok {
						casMax(&flociPeak, kb)
					}
				}
			}
		}
	}()

	return func() (int64, int64) {
		close(done)
		wg.Wait()
		return atomic.LoadInt64(&harnessPeak), atomic.LoadInt64(&flociPeak)
	}
}

func casMax(addr *int64, v int64) {
	for {
		cur := atomic.LoadInt64(addr)
		if v <= cur {
			return
		}
		if atomic.CompareAndSwapInt64(addr, cur, v) {
			return
		}
	}
}

// sampleRSSKB reads pid's resident set size in KB via `ps -o rss=`, which
// reports KB on both Linux and macOS (unlike syscall.Rusage.Maxrss, whose
// unit differs between the two - bytes on Darwin, KB on Linux - `ps`'s own
// output does not).
func sampleRSSKB(pid int) (int64, bool) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, false
	}
	return kb, true
}

// sampleDockerRSSKB reads container's current memory usage via
// `docker stats --no-stream`, parsing the "used" half of a MemUsage field
// shaped like "123.4MiB / 1.943GiB" into KB.
func sampleDockerRSSKB(container string) (int64, bool) {
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", container).Output()
	if err != nil {
		return 0, false
	}
	field := strings.TrimSpace(strings.SplitN(string(out), "/", 2)[0])
	kb, ok := parseMemToKB(field)
	return kb, ok
}

// parseMemToKB parses a docker-stats-shaped size like "123.4MiB" or
// "1.943GiB" into KB.
func parseMemToKB(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		toKB   float64
	}{
		{"GiB", 1024 * 1024},
		{"MiB", 1024},
		{"KiB", 1},
		{"GB", 1000 * 1000},
		{"MB", 1000},
		{"kB", 1},
		{"B", 1.0 / 1024},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			numStr := strings.TrimSuffix(s, u.suffix)
			var f float64
			if _, err := fmt.Sscanf(numStr, "%f", &f); err != nil {
				return 0, false
			}
			return int64(f * u.toKB), true
		}
	}
	return 0, false
}
