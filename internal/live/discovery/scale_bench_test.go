// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
)

// This is issue #64's benchmark half: a synthetic-scale estate (manufactured
// by tools/estate-gen's -count flag), planned against floci with every AWS
// API call counted, so that "how does a plan's cost grow with estate size"
// stops being a question nobody has measured. See ../cloudcontrol/doc.go's
// "Retries" section for the backoff-policy half, and
// live/plan-budget.json + TestPlanCallBudgetAgainstFloci below for the
// committed-artifact ratchet the issue asks for.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestScaleAgainstFloci -v
//	ESTATE_BENCH_N=1000 TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestScaleAgainstFloci -v -timeout 30m
//
// Also runnable as `make bench-estate` (any N via ESTATE_BENCH_N) or
// `just bench-estate`.
//
// # Measured scaling (aws_s3_bucket, this checkout's floci pin, 2026-08-13)
//
// api_calls_total was exactly linear in N with no measurable noise across
// three sizes on the machine that recorded live/plan-budget.json:
//
//	N=20:    343 calls (17.15/instance)
//	N=200:  3403 calls (17.02/instance) - the committed budget's own N
//	N=1000: 17003 calls (17.00/instance)
//
// The fit is calls_total = 17*N + 3: a fixed per-instance cost (S3's own
// aws_s3_bucket Read is chatty - see live/plan-budget.json's calls_by_api
// for the thirteen-ish subresource GETs plus repeated HEADs that make up
// the 17) and a small constant (STS GetCallerIdentity, IAM GetUser, one
// account-resolution call) that does not grow with the estate. Wall-clock
// (informational only - see checkAgainstBudget) tracked the same shape
// against local floci: apply 3.5s at N=200 growing to 21.4s at N=1000;
// plan (Discover+BuildFrom) 3.5-4.1s at N=200 growing to 17.6s at N=1000.
// Nothing in this run exercised cloudcontrol's retry policy - floci never
// throttled - so this benchmark says nothing about behavior during an API
// brownout; that is the backoff policy's own unit tests
// (../cloudcontrol/retry_test.go), not this file's job.
//
// This is one type's curve, not a general law: a type whose Read makes
// fewer or more per-instance calls than aws_s3_bucket's ~17 will scale at
// a different slope, and a sweep of undeclared types (this benchmark
// pins SweepTypes to keep that constant at zero - see the comment on
// SweepTypes below) adds its own N-independent constant on top. What
// generalizes is the shape (linear in N, per-type slope plus a small
// constant), not the number 17.
//
// # Multi-provider scaling (issue #69)
//
// live/plan-budget.json's committed number is a single-provider
// measurement and stays one: TestPlanCallBudgetAgainstFloci calls Discover
// directly, once, exactly as it did before issue #69 - an estate whose
// managed resources sit under one provider configuration is unaffected by
// anything below, and this ratchet does not need regenerating on issue
// #69's account.
//
// An estate whose managed resources span N distinct provider configurations
// (internal/command/live_plan.go's statelessDiscover, looping the sweep
// once per configuration and merging with [Merge]) pays this budget's
// sweep-side cost up to N times over, not once: sweepTypes's per-type list
// calls (the "GET ?bucket-region&max-buckets&x-id"-style estate-wide scan,
// not the per-instance Read this file's own curve is about) run once per
// provider configuration, because each account and region has to be
// searched on its own - a sweep against the wrong account only narrows
// what a run sees, it never mis-reports what exists, but it also never
// substitutes for actually asking the other account. The parent-read leg
// (parent_read.go) does not multiply the same way: Request.ScopeProvider
// keeps each pass processing only its own declared parents, so that leg's
// own O(N) cost (N here being the estate's instance count, not the
// provider count) is paid once per instance total, not once per instance
// per provider.
//
// Nothing here is committed as a second ratchet artifact - the sweep's own
// per-type cost (independent of estate size) is what a multi-provider
// estate multiplies, and this benchmark deliberately measures the
// per-*instance* Read cost instead (SweepTypes pinned to keep the sweep's
// own contribution at zero, see the comment on SweepTypes below), so it has
// nothing to compare a multi-provider run against yet. A future benchmark
// that wants to measure the sweep's own per-provider-configuration cost
// would need to stop pinning SweepTypes down to nothing and count instead.

// benchType is the resource type the scale estate is built from:
// aws_s3_bucket, the simplest type tools/estate-gen's -count validation
// accepts (a single required argument, its own identity; see
// gen.go's renderCounted) that this package's own estate fixture already
// proves discoverable end-to-end (TestDiscoverAgainstFloci covers
// aws_s3_bucket in the ordinary, non-scaled estate).
const benchType = "aws_s3_bucket"

// benchEstate is the tofu-estate value the generated scale estate stamps -
// distinct from every hand-written fixture's own estate tag, so a stray
// leftover from a crashed run is never mistaken for one of them.
const benchEstate = "bench-cohort"

// benchmarkEstateSize is the N the committed budget artifact
// (live/plan-budget.json) was measured at, and the only N
// TestPlanCallBudgetAgainstFloci ratchets against. estateBenchN reads
// ESTATE_BENCH_N to let TestScaleAgainstFloci explore a different N by
// hand without touching this constant or the artifact.
const benchmarkEstateSize = 200

// estateBenchN is the scale estate's size: ESTATE_BENCH_N if set (a
// positive integer), benchmarkEstateSize otherwise. This is what
// TestScaleAgainstFloci sizes its estate to; TestPlanCallBudgetAgainstFloci
// always uses benchmarkEstateSize directly; it does not read this.
func estateBenchN(t *testing.T) int {
	t.Helper()
	v := os.Getenv("ESTATE_BENCH_N")
	if v == "" {
		return benchmarkEstateSize
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		t.Fatalf("ESTATE_BENCH_N=%q is not a positive integer", v)
	}
	return n
}

// TestScaleAgainstFloci manufactures an N-resource estate, applies it,
// then runs the same discovery pass internal/command's live-plan pipeline
// runs (Sweep + CollectUnclaimed, discovery.go's step 5), counting every
// API call the apply's terraform process and the plan's provider
// subprocess together send to floci - through the counting proxy
// (flocitest.CountingProxy) rather than an instrumented RoundTripper,
// because the provider runs in its own subprocess with its own http.Client
// that a RoundTripper installed in this process's Go code would never see.
//
// Wall-clock is logged as informational; the committed gate is
// TestPlanCallBudgetAgainstFloci's call-count ratchet below. A single N is
// not "the" scaling curve - a caller after that runs this test at a few
// values of ESTATE_BENCH_N and compares api_calls_total by hand.
func TestScaleAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/scale")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	n := estateBenchN(t)
	report := runScaleBenchmark(t, n)
	t.Logf("%s", report)
	logByAPI(t, report)

	if n == benchmarkEstateSize {
		checkAgainstBudget(t, report)
	} else {
		t.Logf("ESTATE_BENCH_N=%d overrides the committed budget's N=%d; not ratcheting (see TestPlanCallBudgetAgainstFloci for the fixed-N gate)", n, benchmarkEstateSize)
	}
}

// TestPlanCallBudgetAgainstFloci is the ratchet issue #64 asks for: a
// regeneration of the benchmark at the committed artifact's own N
// (benchmarkEstateSize) must not exceed its recorded call count by more
// than budgetTolerance. It is a separate test (rather than folded silently
// into TestScaleAgainstFloci) so `-run TestPlanCallBudgetAgainstFloci`
// alone is what CI or a pre-merge check would name.
func TestPlanCallBudgetAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/scale")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	report := runScaleBenchmark(t, benchmarkEstateSize)
	t.Logf("%s", report)
	logByAPI(t, report)
	checkAgainstBudget(t, report)
}

// scaleReport is one benchmark run's measurements: what
// TestScaleAgainstFloci logs and what checkAgainstBudget compares to
// live/plan-budget.json.
type scaleReport struct {
	N            int            `json:"n"`
	ApplyElapsed time.Duration  `json:"apply_elapsed_ns"`
	PlanElapsed  time.Duration  `json:"plan_elapsed_ns"`
	CallsTotal   int            `json:"calls_total"`
	CallsByAPI   map[string]int `json:"calls_by_api"`
	Materialized int            `json:"materialized"`
}

// benchProviders satisfies dataread.Providers over the benchmark's one
// launched provider process, the way the command layer's statelessProviders
// does over its plugin cache.
type benchProviders struct {
	provider providers.Interface
}

func (b benchProviders) ConfiguredProvider(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
	return b.provider, nil
}

func (r scaleReport) String() string {
	return "ESTATE SCALE BENCHMARK: N=" + strconv.Itoa(r.N) +
		" apply=" + r.ApplyElapsed.String() +
		" plan(discover+project)=" + r.PlanElapsed.String() +
		" materialized=" + strconv.Itoa(r.Materialized) +
		" api_calls_total=" + strconv.Itoa(r.CallsTotal)
}

// runScaleBenchmark generates an N-instance aws_s3_bucket estate with
// tools/estate-gen's -count flag, applies it with stock terraform, deletes
// the state file (the non-event every other floci live test in this
// package performs - discovery must recover the estate with no state to
// lean on), and runs discovery.Discover plus projection.BuildFrom - steps 5
// and 7 of internal/command/live_plan.go's own pipeline (its doc comment
// numbers them) - with a counting proxy standing in for floci so every
// request either process sends is counted.
//
// Both steps matter, not just Discover: aws_s3_bucket is a client-named
// type (internal/live/identity/table.go: Components: []Component{attr
// ("bucket")}), so its import ID is already known from configuration and
// identity.Resolve hands back a Concrete resolution directly - it is never
// a "needs discovery" instance, never appears in res.Bindings, and
// Discover's own list/bind machinery does not touch it at all. The read
// that actually costs one API call per instance and that a real plan pays
// for every client-named resource in the estate is projection.BuildFrom's
// ImportResourceState + ReadResource per resolution (build.go). A
// benchmark that stopped at Discover would report a flat, N-independent
// call count for this type and call that "the scale story", which would be
// true of Discover alone and false of a plan.
func runScaleBenchmark(t *testing.T, n int) scaleReport {
	t.Helper()

	root := flocitest.RepoRoot(t)
	dir := t.TempDir()

	genCmd := exec.Command("go", "run", "./tools/estate-gen",
		"-cohort", "bench", "-types", benchType, "-count", strconv.Itoa(n), "-out", dir) //nolint:gosec // fixed binary and args, test-only
	genCmd.Dir = root
	if out, err := genCmd.CombinedOutput(); err != nil {
		t.Fatalf("go run ./tools/estate-gen -count %d: %v\n%s", n, err, out)
	}

	flociPort := flocitest.StartFloci(t, "cdf-bench")
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

	// The benchmark is about a plan's read cost, not the apply that
	// manufactured the estate; the apply's own calls are logged (above)
	// but excluded from the counted total below.
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
	resolutions := resolveOrFail(t, cfg).All()

	planStart := time.Now()

	// Issue #179's data-read phase, the pre-resolution step of
	// live_plan.go's pipeline, inside the measured window so that its
	// ReadDataSource calls count into the same plan-call budget as
	// discovery's and projection's reads. The generated estate declares no
	// data sources, so today this is the phase's free path - an offline
	// probe, zero network calls, calls_total unchanged - and the wiring is
	// what makes a future data-source-bearing fixture's reads measured
	// rather than assumed.
	dataAnalysis := dataread.Analyze(context.Background(), cfg, dataread.Options{})
	if _, drDiags := dataread.Read(context.Background(), cfg, dataAnalysis, benchProviders{provider}); drDiags.HasErrors() {
		t.Fatalf("data-read phase failed: %s", drDiags.Err())
	}

	res, diags := Discover(context.Background(), Request{
		Estate:           benchEstate,
		Config:           cfg,
		Resolutions:      resolutions,
		Provider:         provider,
		Region:           awsRegion,
		CollectUnclaimed: true,
		Sweep:            true,
		// SweepTypes is pinned to just benchType rather than left at its
		// default (every admitted type - sweepTypes' doc comment) as a
		// deliberate, documented narrowing: a full default sweep against
		// floci hits a real, pre-existing gap unrelated to this benchmark -
		// at least aws_lambda_capacity_provider's listed objects carry no
		// tags attribute on floci, which sweepTypes' walk turns into a
		// [ProblemNoTags] *error* diagnostic (discovery.go: "everything
		// that could make a plan act on the wrong resource is an error"),
		// so Discover returns with diags.HasErrors() true before this
		// benchmark ever gets to measure anything. Since benchType is
		// already covered by the config-driven scan, sweepTypes' own
		// dedup (decl.types[t] != nil) reduces this to an empty sweep
		// universe regardless - this benchmark therefore measures the
		// config-driven scan's cost at scale, not the sweep-of
		// undeclared-types constant a real multi-type estate would also
		// pay. That constant is real and worth measuring once the floci
		// gap above is fixed or worked around; it is out of this issue's
		// backoff/scale scope to chase here, and is reported as a finding
		// rather than fixed in place.
		SweepTypes: []string{benchType},
	})
	assertNoErrors(t, diags)

	// aws_s3_bucket is client-named (see the comment above), so nothing
	// here is a needs-discovery instance and res.Bindings stays empty; the
	// N instances Discover found nothing to bind still ride through as
	// res.Resolutions, the merged list projection.BuildFrom consumes next.
	if len(res.Bindings) != 0 {
		t.Errorf("bound %d instances, want 0 (aws_s3_bucket is client-named; nothing here should need marker discovery)", len(res.Bindings))
	}

	provs := projection.SingleProvider(addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
	}, provider)
	proj, projDiags := projection.BuildFrom(context.Background(), cfg, res.Resolutions, provs)
	planElapsed := time.Since(planStart)
	assertNoErrors(t, projDiags)

	if len(proj.Materialized) != n {
		t.Errorf("materialized %d instances, want the generated estate's %d", len(proj.Materialized), n)
	}

	return scaleReport{
		N:            n,
		ApplyElapsed: applyElapsed,
		PlanElapsed:  planElapsed,
		CallsTotal:   proxy.Total(),
		CallsByAPI:   proxy.Counts(),
		Materialized: len(proj.Materialized),
	}
}

// logByAPI logs report's per-action call breakdown, sorted, one line per
// action - the detail api_calls_total alone does not show, useful for
// telling "one API is a hot spot" apart from "every call is like this".
func logByAPI(t *testing.T, report scaleReport) {
	t.Helper()
	keys := make([]string, 0, len(report.CallsByAPI))
	for k := range report.CallsByAPI {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  %-50s %d", k, report.CallsByAPI[k])
	}
}

// planBudgetPath is live/plan-budget.json, the committed artifact issue #64
// asks the call-count budget to be recorded as.
func planBudgetPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(flocitest.RepoRoot(t), "live", "plan-budget.json")
}

// planBudget is live/plan-budget.json's shape.
type planBudget struct {
	N               int            `json:"n"`
	ResourceType    string         `json:"resource_type"`
	CallsTotal      int            `json:"calls_total"`
	CallsByAPI      map[string]int `json:"calls_by_api"`
	Tolerance       float64        `json:"tolerance"`
	FloorTolerance  float64        `json:"floor_tolerance"`
	WallClockBucket string         `json:"wall_clock_bucket"`
	Note            string         `json:"note"`
}

// checkAgainstBudget is the ratchet, and it is two-sided (issue #630):
// report.CallsTotal (measured at benchmarkEstateSize) must sit inside the
// band the committed artifact declares - not above CallsTotal by more than
// Tolerance, and not below it by more than FloorTolerance.
//
// The two bounds guard different things, which is why they are two fields
// and not one symmetric tolerance. The ceiling is a cost gate: it protects
// the user from a plan that got more expensive. The floor is an instrument
// gate: it protects the MEASUREMENT from going blind, because a
// one-directional ratchet cannot tell "we got faster" from "we stopped
// measuring something", and this repository has twice shipped green on the
// second - a cohort that passed because its failing resources vanished from
// the fixture (#556), and a board row that read pass from a run that
// measured nothing (#413/#414).
//
// What sizes the floor is the smallest leg this benchmark pays for. At
// N=200 the run's 4408 calls are two legs: projection's per-instance read
// (3400, 17/instance) and the parent-read sweep (1005 - five unscoped
// bucket-child lists, each one ListBuckets plus one subresource GET per
// bucket), plus three fixed calls. The parent-read leg is 22.8% of the
// total, so a floor at 20% is the loosest one that still fails when a whole
// leg silently stops running. Measured, not reasoned: disabling
// parentReadSweep drops the run to 3403, under the 3526 floor.
//
// What the floor deliberately does NOT catch is small drift - the artifact
// carrying 4413 while the code had moved to 4408 is 0.11%, and no
// practical band catches that. Only re-measuring does. The guard against
// THAT is live/flociimage_test.go's TestFlociMeasurementsMatchThePinOrSay
// WhyNot, which forces a re-measurement whenever the emulator pin moves,
// plus the ordinary discipline of re-measuring in the PR that moves the
// number. The floor is the collapse detector, not the staleness detector.
//
// Wall-clock is deliberately not gated in either direction - the artifact's
// WallClockBucket and report.PlanElapsed are both informational only,
// exactly as this package's doc comment promises; floci's own performance on
// the machine running the test, not this package's code, is what a
// wall-clock assertion would actually be grading.
func checkAgainstBudget(t *testing.T, report scaleReport) {
	t.Helper()

	path := planBudgetPath(t)
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		t.Fatalf("reading the committed call-count budget %s: %v", path, err)
	}
	var budget planBudget
	if err := json.Unmarshal(data, &budget); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	if budget.N != report.N {
		t.Fatalf("%s was measured at N=%d, but this run measured N=%d; regenerate the artifact at matching N before trusting this ratchet", path, budget.N, report.N)
	}
	if budget.FloorTolerance <= 0 {
		// Not a warning. An absent or zero floor_tolerance is how the
		// under-run bound would come back off without anyone deciding to
		// remove it, which is the shape of every silently-disabled check
		// this repository has had to find the hard way.
		t.Fatalf("%s declares floor_tolerance=%v; the ratchet's under-run bound is what tells \"we got faster\" apart from \"we stopped measuring something\", and it needs a positive width. Set it rather than dropping it.", path, budget.FloorTolerance)
	}

	ceiling := float64(budget.CallsTotal) * (1 + budget.Tolerance)
	floor := float64(budget.CallsTotal) * (1 - budget.FloorTolerance)
	t.Logf("call-count budget: committed=%d band=[-%.0f%%,+%.0f%%] floor=%.0f ceiling=%.0f measured=%d",
		budget.CallsTotal, budget.FloorTolerance*100, budget.Tolerance*100, floor, ceiling, report.CallsTotal)
	if float64(report.CallsTotal) > ceiling {
		t.Errorf("plan issued %d API calls against a committed budget of %d (+%.0f%% tolerance = %.0f); either a real regression, or %s needs regenerating with today's measurement",
			report.CallsTotal, budget.CallsTotal, budget.Tolerance*100, ceiling, path)
	}
	if float64(report.CallsTotal) < floor {
		t.Errorf("plan issued %d API calls, under a committed budget of %d by more than the -%.0f%% floor (= %.0f). A drop this size is either a real improvement worth recording - re-measure and commit %s in the same change - or a leg of the measurement that stopped running. Check calls_by_api against the committed breakdown to tell which: an improvement moves the calls it set out to move, a blind instrument loses a whole key.",
			report.CallsTotal, budget.CallsTotal, budget.FloorTolerance*100, floor, path)
	}
}
