// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// This is issue #64's guided axis on top of scale_bench_test.go's cold-plan
// benchmark: the same estate, planned cold and then Request.Guided, with
// every AWS API call counted through the same flocitest.CountingProxy, so
// the guided leg's savings claim is a measurement of this checkout against
// real floci rather than a description of the code.
//
// # What this measures, and what it deliberately does not
//
// scale_bench_test.go's own N-scaling curve (17*N+3 calls for an
// aws_s3_bucket estate) is dominated by projection.BuildFrom's per-instance
// ImportResourceState + ReadResource cost, not by discovery.Discover at
// all - see that file's doc comment, "Both steps matter, not just Discover".
// Request.Guided only touches Discover's estate-wide sweep (see
// guided.go and discovery.go's Request.Guided doc comment), so it has
// nothing to say about that O(17N) term: a client-named type like
// aws_s3_bucket never enters Discover's list/bind machinery, guided or not,
// and this benchmark's own measurement below confirms that directly (the
// bucket count never appears in either call count - both totals are the
// sweep's alone).
//
// What guided mode changes, and what this benchmark isolates instead, is
// the estate-wide sweep's PER-RUN TYPE UNIVERSE: today, every admitted type
// the configuration does not declare costs one List call on every plan,
// regardless of whether this estate has ever held a resource of that type.
// That cost is O(admission table) - a constant against N, but not against
// the number of types a real deployment's sweep has to consider - and
// guided mode narrows it to O(types this estate has actual evidence for),
// falling back to the unnarrowed, O(admission table) universe whenever the
// hint cannot be trusted or a verification pass is asked for. The measured
// numbers below are exactly that comparison, not a description of an
// unmeasured claim: sweeping N_types admitted-but-never-used types costs
// N_types calls cold and 0 calls guided (routine), while a type the estate
// actually still holds costs the same one call either way - guided moves
// the sweep's cost from being proportional to the admission table's size
// toward being proportional to the estate's own footprint (its "delta" from
// empty), never below what verification needs to stay honest.
const guidedBenchEstate = "guided-cohort"

// guidedBenchSize is deliberately small and unrelated to
// scale_bench_test.go's benchmarkEstateSize: this benchmark is measuring the
// sweep's per-type constant, not the config-driven scan's per-instance cost,
// so a handful of buckets is enough to prove the config-driven scan is
// untouched (see assertBucketsCostNothing) without paying N=200's apply
// time twice more (cold, then guided) on every run of this benchmark.
const guidedBenchSize = 5

// guidedBenchSweepTypes is the sweep's type universe for this benchmark,
// overriding the admission-table default exactly the way
// scale_bench_test.go's own SweepTypes pin does, and for the same reason:
// a handful of named types keeps this benchmark's result attributable to
// the mechanism under test rather than to floci's coverage gaps in types
// this issue has no stake in. aws_sns_topic is the one this run plants a
// real, undeclared, estate-tagged resource of (see
// runGuidedSweepBenchmark); aws_kms_key and aws_route53_zone are two
// admitted, real-floci-listable types (both declared and exercised by the
// package's own estateName fixture, see fakeCloud's unfilter map) that this
// benchmark's estate never creates any of, so they stay absent from every
// hint this benchmark writes and prove the "always full for absent types"
// half of the design.
var guidedBenchSweepTypes = []string{"aws_sns_topic", "aws_kms_key", "aws_route53_zone"}

// guidedSNSSnippet is a hand-written resource block for the one
// undeclared-but-owned resource this benchmark needs and tools/estate-gen
// cannot produce today (-count only ever emits one type). It mirrors
// live/e2e/estates/messaging/messaging.tf's own aws_sns_topic block: a name
// and this estate's ownership tags, nothing else required. Planted
// alongside the generated bucket estate before apply, then removed from the
// directory (not destroyed) before Discover ever loads the configuration,
// which is exactly the "deleted block" shape TestSweepFindsDeletedBlock
// exercises against the fake cloud - here against a real, live resource.
const guidedSNSSnippet = `
resource "aws_sns_topic" "leftover" {
  name = "guided-bench-leftover"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_sns_topic.leftover"
  }
}
`

// TestGuidedSweepAgainstFloci measures issue #64's guided-discovery claim
// against real floci: cold vs guided(routine) vs guided(verify) API call
// counts for the estate-wide sweep, over an estate this benchmark controls
// completely. See runGuidedSweepBenchmark for the mechanics and the package
// doc comment above for what the three numbers mean.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestGuidedSweepAgainstFloci -v
//
// The measured numbers this test's own run produced are recorded in
// live/plan-budget.json's "guided" section - see recordGuidedMeasurement -
// which is this benchmark's committed artifact the same way
// live/plan-budget.json's top-level fields are scale_bench_test.go's.
func TestGuidedSweepAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/scale-guided")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	report := runGuidedSweepBenchmark(t)
	t.Logf("%s", report)
	recordGuidedMeasurement(t, report)

	if report.GuidedRoutineCalls >= report.ColdCalls {
		t.Errorf("guided(routine) issued %d calls, want fewer than cold's %d: guided mode found no savings to measure",
			report.GuidedRoutineCalls, report.ColdCalls)
	}
	if report.GuidedVerifyCalls != report.ColdCalls {
		t.Errorf("guided(verify) issued %d calls, want exactly cold's %d: a verification pass must match full enumeration",
			report.GuidedVerifyCalls, report.ColdCalls)
	}
}

// guidedSweepReport is one guided-sweep benchmark run's three measurements.
type guidedSweepReport struct {
	SweepTypes         []string `json:"sweep_types"`
	ColdCalls          int      `json:"cold_calls"`
	GuidedRoutineCalls int      `json:"guided_routine_calls"`
	GuidedVerifyCalls  int      `json:"guided_verify_calls"`
	SkippedByRoutine   []string `json:"skipped_by_routine"`
}

func (r guidedSweepReport) String() string {
	return fmt.Sprintf(
		"GUIDED SWEEP BENCHMARK: sweep_types=%v cold=%d guided_routine=%d (skipped %v) guided_verify=%d",
		r.SweepTypes, r.ColdCalls, r.GuidedRoutineCalls, r.SkippedByRoutine, r.GuidedVerifyCalls,
	)
}

// runGuidedSweepBenchmark builds one small estate (guidedBenchSize declared
// aws_s3_bucket instances, plus one hand-planted, undeclared aws_sns_topic
// carrying this estate's markers), applies it once, and then runs
// discovery's estate-wide sweep over guidedBenchSweepTypes three times
// against that one live estate - cold, guided(routine), guided(verify) -
// counting every AWS API call each pass issues through the counting proxy.
//
// The sweep is deliberately isolated from the config-driven scan: nothing
// this benchmark declares is a needs-discovery type (aws_s3_bucket is
// client-named, per scale_bench_test.go's own doc comment), so
// decl.typeNames() is empty and every counted call belongs to the sweep
// alone. assertBucketsCostNothing checks that assumption rather than
// asserting it away.
func runGuidedSweepBenchmark(t *testing.T) guidedSweepReport {
	t.Helper()

	root := flocitest.RepoRoot(t)
	dir := t.TempDir()

	genCmd := exec.Command("go", "run", "./tools/estate-gen", //nolint:gosec // fixed binary and args, test-only
		"-cohort", "guided", "-types", benchType, "-count", strconv.Itoa(guidedBenchSize), "-out", dir)
	genCmd.Dir = root
	if out, err := genCmd.CombinedOutput(); err != nil {
		t.Fatalf("go run ./tools/estate-gen -cohort guided -count %d: %v\n%s", guidedBenchSize, err, out)
	}

	snsPath := filepath.Join(dir, "leftover.tf")
	if err := os.WriteFile(snsPath, []byte(guidedSNSSnippet), 0o644); err != nil { //nolint:gosec // test fixture, not a secret
		t.Fatalf("writing %s: %v", snsPath, err)
	}

	flociPort := flocitest.StartFloci(t, "cdf-guided-bench")
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(flociPort))

	t.Setenv("AWS_ENDPOINT_URL", proxy.Endpoint())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	proxy.Reset()

	// The hint: a real one, persisted to a real record store through
	// projection.Manager exactly as writeGuidedHintFixture is for the
	// equivalence tests, recording both types this estate now actually
	// holds. aws_kms_key and aws_route53_zone are never mentioned - this
	// estate never created any - so both stay absent from the hint and, per
	// the design, always fully swept.
	hintStore := newGuidedHintStore(t)
	writeGuidedHintFixtureFor(t, hintStore, guidedBenchEstate, time.Now(), benchType, "aws_sns_topic")

	// The undeclared-but-owned resource stays live; only its configuration
	// block goes away, so the next config load declares nothing of it -
	// TestSweepFindsDeletedBlock's shape, against a real provider.
	if err := os.Remove(snsPath); err != nil {
		t.Fatalf("removing %s: %v", snsPath, err)
	}

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

	baseReq := Request{
		Estate:      guidedBenchEstate,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    provider,
		Region:      awsRegion,
		Sweep:       true,
		SweepTypes:  guidedBenchSweepTypes,
	}

	proxy.Reset()
	coldRes, diags := Discover(context.Background(), baseReq)
	assertNoErrors(t, diags)
	assertBucketsCostNothing(t, coldRes)
	coldCalls := proxy.Total()

	proxy.Reset()
	routineReq := baseReq
	routineReq.Guided = true
	routineReq.HintStore = hintStore
	routineRes, diags := Discover(context.Background(), routineReq)
	assertNoErrors(t, diags)
	if !routineRes.Guided {
		t.Fatalf("guided(routine) pass fell back: %s", routineRes.GuidedFallback)
	}
	routineCalls := proxy.Total()

	proxy.Reset()
	verifyReq := routineReq
	verifyReq.GuidedVerify = true
	verifyRes, diags := Discover(context.Background(), verifyReq)
	assertNoErrors(t, diags)
	if !verifyRes.Guided {
		t.Fatalf("guided(verify) pass fell back: %s", verifyRes.GuidedFallback)
	}
	verifyCalls := proxy.Total()

	skipped := append([]string(nil), routineRes.GuidedSweepSkipped...)
	sort.Strings(skipped)

	return guidedSweepReport{
		SweepTypes:         guidedBenchSweepTypes,
		ColdCalls:          coldCalls,
		GuidedRoutineCalls: routineCalls,
		GuidedVerifyCalls:  verifyCalls,
		SkippedByRoutine:   skipped,
	}
}

// assertBucketsCostNothing pins this benchmark's own isolation claim: the
// guidedBenchSize declared aws_s3_bucket instances never reach Discover's
// list/bind machinery (they are client-named, resolved before Discover ever
// runs), so nothing they cost is folded into the sweep numbers this
// benchmark reports.
func assertBucketsCostNothing(t *testing.T, res *Result) {
	t.Helper()
	if len(res.Bindings) != 0 {
		t.Errorf("bound %d instances, want 0 (aws_s3_bucket is client-named and should never enter Discover's bind machinery)", len(res.Bindings))
	}
	if _, ok := res.ScanFor(benchType); ok {
		t.Errorf("%s was scanned by Discover; the benchmark's guidance numbers assume it never is", benchType)
	}
}

// recordGuidedMeasurement writes report into live/plan-budget.json's
// "guided" section, alongside scale_bench_test.go's own cold-plan fields, so
// the O(delta)-vs-O(admission-table) claim in this file's doc comment is
// backed by a committed, regenerable measurement rather than an assertion.
// Unlike checkAgainstBudget, this is not a ratchet with a tolerance -
// TestGuidedSweepAgainstFloci's own two assertions (guided(routine) <
// cold, guided(verify) == cold) are the pass/fail gate; this function only
// keeps the artifact current with whatever this run measured.
func recordGuidedMeasurement(t *testing.T, report guidedSweepReport) {
	t.Helper()
	path := planBudgetPath(t)

	data, err := os.ReadFile(path) //nolint:gosec // fixed path inside the checkout
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}

	guided := map[string]any{
		"estate_size":          guidedBenchSize,
		"resource_type":        benchType,
		"sweep_types":          report.SweepTypes,
		"cold_calls":           report.ColdCalls,
		"guided_routine_calls": report.GuidedRoutineCalls,
		"guided_verify_calls":  report.GuidedVerifyCalls,
		"skipped_by_routine":   report.SkippedByRoutine,
		"measured_at":          time.Now().UTC().Format("2006-01-02"),
		"note": "Issue #64's guided-discovery axis, from TestGuidedSweepAgainstFloci " +
			"(go test -run TestGuidedSweepAgainstFloci, TF_FLOCI_TEST=1). cold_calls is the estate-wide " +
			"sweep's API call count over sweep_types with Request.Guided false; guided_routine_calls is the same " +
			"sweep with Request.Guided true and a fresh snapshot hint (aws_sns_topic present, aws_kms_key and " +
			"aws_route53_zone absent); guided_verify_calls adds Request.GuidedVerify, which must match cold_calls " +
			"exactly. The config-driven scan costs nothing in any of the three numbers -- aws_s3_bucket is " +
			"client-named and never reaches Discover's list/bind machinery, see assertBucketsCostNothing -- so " +
			"every call counted here belongs to the sweep alone. This is not a ratchet: TestGuidedSweepAgainstFloci's " +
			"own assertions (guided_routine_calls < cold_calls, guided_verify_calls == cold_calls) are the gate; " +
			"this section only keeps the artifact's numbers current with the most recent measurement.",
	}
	guidedJSON, err := marshalNoHTMLEscape(guided, "")
	if err != nil {
		t.Fatalf("encoding the guided measurement: %v", err)
	}
	raw["guided"] = guidedJSON

	out, err := marshalNoHTMLEscape(raw, "  ")
	if err != nil {
		t.Fatalf("encoding %s: %v", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil { //nolint:gosec // committed artifact, not a secret
		t.Fatalf("writing %s: %v", path, err)
	}
}

// marshalNoHTMLEscape is json.MarshalIndent without the standard library's
// default HTML-safe escaping of &, < and > (json.Encoder.SetEscapeHTML),
// which would otherwise turn plan-budget.json's own "budget <
// ceiling"-shaped prose into unreadable < / & escapes on every
// regeneration. indent == "" produces compact output (used for the nested
// "guided" object before it is re-embedded as json.RawMessage); a non-empty
// indent produces the same pretty-printed, trailing-newline-terminated shape
// checkAgainstBudget's own artifact already has.
func marshalNoHTMLEscape(v any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder.Encode always appends a trailing newline; that matches
	// the artifact's existing shape for the indented, top-level call and is
	// harmless (trimmed by the caller) for the compact, nested one.
	if indent == "" {
		return bytes.TrimRight(buf.Bytes(), "\n"), nil
	}
	return buf.Bytes(), nil
}
