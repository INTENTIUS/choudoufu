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
// # Measured scaling (2026-09-05, floci pin sha256:a39185cc, darwin/arm64)
//
// Supersedes the 2026-08-30 table this section used to carry, which
// predated terralith-gen's module-nested bucket (issue #574). #708 fixed
// this file's own fixture loader (discovery_test.go's loadConfig) so the
// benchmark could run against the current generator at all, and this table
// is #838's full six-tier re-measurement against that fixed loader.
//
// Six tiers, each its own `go test` process (TERRALITH_CEILING_SCALE=N),
// spanning a 75x resource-count range:
//
//	scale  resources  calls  pagination  throttle   apply       discover    build      harness_rss  floci_rss  materialized
//	1      79         113    0           0          31.8s       52.9ms      82.6ms     223MB        251MB      44
//	4      301        401    0           0          88.6s       80.4ms      251.5ms    228MB        260MB      173
//	10     745        977    0           0          211.8s      133.6ms     568.3ms    232MB        279MB      431
//	20     1485       1937   0           0          418.3s      216.5ms     1096.7ms   240MB        293MB      861
//	40     2965       3857   0           0          837.8s      569.8ms     3025.2ms   273MB        280MB      1721
//	80     5925       7697   0           0          1720.9s     806.6ms     4863.5ms   334MB        399MB      3441
//
// (harness_rss/floci_rss are peak values sampled during the Discover+Build
// window only - see PeakHarnessRSSKB/PeakFlociRSSKB's own doc comments for
// what each process is.)
//
// Per-unit rates make the shape legible: apply cost is flat at
// ~0.28s/resource from scale=4 onward (0.294, 0.284, 0.282, 0.283, 0.290
// s/resource) - floci's own per-create latency, linear with no sign of
// degradation even with 5925 objects in one account; scale=1's higher
// 0.403s/resource is fixed per-run overhead (container health-check,
// terraform init) amortized over fewer resources, the same shape the prior
// table showed at its own scale=1. discover cost per API call falls as N
// grows (0.469, 0.201, 0.137, 0.112, 0.148, 0.105 ms/call) - still the
// opposite of a dominance signal, though scale=40 breaks the decline by a
// small margin (0.148ms vs scale=20's 0.112ms) before scale=80 falls again
// to 0.105ms; noise at millisecond resolution, not a trend. build cost per
// materialized instance stays in a 1.27-1.88ms band with no growth trend
// (1.877, 1.454, 1.319, 1.274, 1.758, 1.413 ms/instance) - the same
// millisecond-scale noise applies.
//
// Peak process memory is the one line that no longer reads as a flat band.
// choudoufu's own harness grew from 223MB to 334MB (1.50x) and floci's
// container from 251MB to 399MB (1.59x) across the range - real, gradual
// growth, but strongly sub-linear against the 75x growth in resource count
// (79->5925) and 78x growth in materialized instances (44->3441). Neither
// series shows a sharp knee at one tier (floci's own line actually dips to
// 280MB at scale=40 before its largest single-tier jump, to 399MB, lands at
// scale=80), and 334MB/399MB is nowhere near a working ceiling on the
// hardware this ran on - but it is a real change from the pre-#574 table's
// finding of a flat ~225-300MB band, and this table says so rather than
// repeating the old claim unchanged.
//
// pagination_total reads zero at EVERY tier, including scale=80's 800
// aws_iam_policy instances (8x real AWS's documented 100-item default page
// size for IAM ListPolicies) and 80 aws_ecs_task_definition instances
// (unchanged from the pre-#574 generator - the module bucket added IAM and
// DNS resources, not ECS task definitions) in one aws_ecs_task_definition
// ListTaskDefinitions call. Confirmed by a direct API probe outside
// choudoufu entirely (no terraform, no provider, plain `aws` CLI against a
// fresh floci container): 150 IAM policies and 120 ECS task definitions,
// `--max-items`/`--max-results` given explicitly, both come back in one
// response with IsTruncated/nextToken unset. This is an emulator gap, not
// an artifact of these tiers being too small - see lex00/floci#185.
// throttle_total is also zero at every tier, including the 7697-call
// scale=80 run; floci applies no rate limiting in this range.
//
// # The stated ceiling
//
// No wall was found, in the measured range (79-5925 resources / 113-7697
// API calls), in the request/timing metrics that reflect choudoufu's OWN
// code: API call count, apply cost per resource, discover cost per call,
// and build cost per materialized instance all stay flat or improve as the
// estate grows, this run included. Peak process memory is the one metric
// whose character changed from the pre-#574 table: it is no longer flat
// within a fixed band, though it stays strongly sub-linear against the
// resource-count growth (see above), and 334MB/399MB at the top tier is not
// itself a practical ceiling on the hardware this ran on (an ordinary
// developer laptop). Floci-backed measurements of these components are
// trustworthy at least through ~5925 resources / ~7697 API calls (this
// run's own top tier, up from the pre-#574 table's ~4000/~4800) - the
// epic's "roughly N resources" framing still does not apply to API-call
// count or per-unit timing, and now carries an explicit, measured caveat
// for peak memory: it does grow with N, gradually and sub-linearly, not
// sharply.
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
// wall-clock time, not measurement validity: scale=80 alone cost ~28.7
// minutes just to stand up (apply=1720.9s, ~0.29s/resource), which is what
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
