// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package recordtrips

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

const (
	awsRegion    = "us-east-1"
	terraformBin = "terraform"

	// repeats is how many plans are measured. Every value is reported and
	// nothing is averaged, the same discipline internal/live/statefulcost
	// uses: a mean hides the first run paying for something the later two
	// do not.
	repeats = 3
)

// TestRecordTripsAgainstFloci is the measurement.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/recordtrips/ -run TestRecordTripsAgainstFloci -v -timeout 30m
//
// It asserts that every plan it measured was empty, because a plan that
// proposed work is not the operation whose cost is being reported, and its
// numbers beside the others' would be the comparison this file exists to
// make honest. It asserts nothing about the counts themselves — those are
// the finding.
func TestRecordTripsAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "record-store round trips")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	scale := scaleFromEnv(t)
	root := flocitest.RepoRoot(t)
	choudoufuBin := flocitest.BuildTofu(t)
	flocitest.PluginCacheDir(t)

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	port := flocitest.StartFloci(t, "cdf-rtrips")
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(port))
	env := awsEnv(proxy.Endpoint())
	t.Logf("emulator %s via proxy %s", flocitest.Endpoint(port), proxy.Endpoint())

	prefix := "rtp"
	estate := "rtrips-" + prefix

	// The stock half of the migration: stock terraform applies the
	// generator's output and keeps its state file. The live directory then
	// adopts THAT estate, which is the migration the promise is about.
	coldDir := filepath.Join(t.TempDir(), "cold")
	generate(t, root, coldDir, scale, prefix)
	mustRun(t, coldDir, env, terraformBin, "init", "-input=false", "-no-color")
	if out, err := run(t, coldDir, env, terraformBin, "apply", "-input=false", "-auto-approve", "-no-color"); err != nil {
		t.Fatalf("cold apply: %v\n%s", err, tailOf(out, 40))
	}

	adoptedDir := filepath.Join(t.TempDir(), "adopted")
	generate(t, root, adoptedDir, scale, prefix)
	addLiveBlock(t, adoptedDir, estate)
	mustRun(t, adoptedDir, env, choudoufuBin, "init", "-input=false", "-no-color")

	importOut, err := run(t, adoptedDir, env, choudoufuBin,
		"live-import", "-state="+filepath.Join(coldDir, "terraform.tfstate"), "-estate="+estate, "-approve")
	if err != nil {
		t.Fatalf("live-import -approve: %v\n%s", err, tailOf(importOut, 60))
	}
	t.Logf("live-import: %s", stampLine(importOut))

	instances := countRecords(t, adoptedDir, estate)
	t.Logf("estate %s: %d instances hold a record after migration", estate, instances)

	outDir := reportDir(t)
	t.Logf("plan output and trip logs: %s", outDir)

	type measured struct {
		calls   int
		trips   []staterecord.Trip
		seconds float64
	}
	var runs []measured

	for i := 1; i <= repeats; i++ {
		logPath := filepath.Join(outDir, fmt.Sprintf("trips_%d.tsv", i))
		// Fresh file per run: appended to by the process, so a leftover
		// from an earlier `go test` in the same output directory would
		// silently double every count.
		if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clearing %s: %v", logPath, err)
		}

		planEnv := append(awsEnv(proxy.Endpoint()), projection.RecordTripLogEnvVar+"="+logPath)
		before := proxy.Total()
		start := time.Now()
		out, planErr := run(t, adoptedDir, planEnv, choudoufuBin, "plan", "-input=false", "-no-color")
		elapsed := time.Since(start)
		calls := proxy.Total() - before

		planPath := filepath.Join(outDir, fmt.Sprintf("plan_%d.out", i))
		if err := os.WriteFile(planPath, []byte(out), 0o600); err != nil {
			t.Fatalf("writing %s: %v", planPath, err)
		}

		switch {
		case planErr != nil:
			t.Errorf("plan run %d failed: %v; full output at %s", i, planErr, planPath)
		case !strings.Contains(out, "No changes."):
			t.Errorf("plan run %d is not empty (%s); its cost is not the cost under measurement. Full output at %s",
				i, planSummary(out), planPath)
		}

		data, err := os.ReadFile(logPath) //nolint:gosec // a path this test just named
		if err != nil {
			t.Fatalf("reading the trip log %s: %v (the plan wrote none, so the run measured nothing)", logPath, err)
		}
		trips, err := staterecord.ParseTripLog(data)
		if err != nil {
			t.Fatalf("parsing %s: %v", logPath, err)
		}
		if len(trips) == 0 {
			t.Fatalf("the trip log %s is empty: this run measured nothing, which is not the same finding as zero trips", logPath)
		}

		runs = append(runs, measured{calls: calls, trips: trips, seconds: elapsed.Seconds()})
		t.Logf("run %d: aws-calls %d, record-trips %d (%.2fs)", i, calls, len(trips), elapsed.Seconds())
	}

	// ── the report ──────────────────────────────────────────────────────
	t.Logf("")
	t.Logf("RECORD-TRIP REPORT scale=%d instances=%d emulator=%s", scale, instances, flocitest.Image())
	for i, r := range runs {
		t.Logf("run %d: aws-calls %d, record-trips %d (%.2fs)", i+1, r.calls, len(r.trips), r.seconds)
	}

	last := runs[len(runs)-1]
	sum := staterecord.Summarize(last.trips)
	t.Logf("")
	t.Logf("last run: %d trips over %d distinct keys; %d re-read a key an earlier trip had already read",
		sum.Total, sum.DistinctKeys, sum.RepeatTrips)
	t.Logf("trips per instance: %.2f", float64(sum.Total)/float64(max(instances, 1)))

	t.Logf("")
	t.Logf("by site (the code that wanted the record):")
	for _, line := range staterecord.SortedByCount(sum.BySite) {
		t.Logf("  %s", line)
	}
	t.Logf("")
	t.Logf("by accessor:")
	for _, line := range staterecord.SortedByCount(sum.ByVia) {
		t.Logf("  %s", line)
	}
	t.Logf("")
	t.Logf("by store method:")
	for _, line := range staterecord.SortedByCount(sum.ByMethod) {
		t.Logf("  %s", line)
	}

	// How often one key is read, and by how many different sites: the shape
	// that decides whether a cache is the right fix or the wrong one.
	perKey := map[string]int{}
	sitesPerKey := map[string]map[string]bool{}
	for _, tr := range last.trips {
		perKey[tr.Key]++
		if sitesPerKey[tr.Key] == nil {
			sitesPerKey[tr.Key] = map[string]bool{}
		}
		sitesPerKey[tr.Key][tr.Site] = true
	}
	hist := map[int]int{}
	for _, n := range perKey {
		hist[n]++
	}
	t.Logf("")
	t.Logf("reads per key:")
	for _, line := range staterecord.SortedByCount(intKeyed(hist)) {
		t.Logf("  keys read this many times: %s", line)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func intKeyed(in map[int]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[strconv.Itoa(k)] = v
	}
	return out
}

// countRecords counts the record files the migration wrote, which is the
// instance population every per-instance figure in the report divides by.
// Read off the store's own directory rather than off live-import's summary
// line, so the denominator is what actually exists.
func countRecords(t *testing.T, dir, estate string) int {
	t.Helper()
	rootDir := filepath.Join(dir, ".tofu-records", projection.RecordKeyPrefix(estate))
	n := 0
	err := filepath.Walk(rootDir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", rootDir, err)
	}
	return n
}

func scaleFromEnv(t *testing.T) int {
	t.Helper()
	v := os.Getenv("RECORD_TRIPS_SCALE")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("RECORD_TRIPS_SCALE=%q is not a positive integer", v)
	}
	return n
}

// reportDir is where the plan outputs and trip logs are kept, so a run's
// numbers can be re-derived and its plans diffed by value against another
// run's. RECORD_TRIPS_OUT names it.
func reportDir(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("RECORD_TRIPS_OUT"); v != "" {
		if err := os.MkdirAll(v, 0o755); err != nil {
			t.Fatalf("creating %s: %v", v, err)
		}
		return v
	}
	dir, err := os.MkdirTemp("", "recordtrips-")
	if err != nil {
		t.Fatalf("creating an output directory: %v", err)
	}
	return dir
}

// ── estate setup, the same shape internal/live/statefulcost uses ────────

func generate(t *testing.T, root, dir string, scale int, prefix string) {
	t.Helper()
	cmd := exec.Command("go", "run", "./tools/terralith-gen", //nolint:gosec // fixed binary and args, test-only
		"-scale", strconv.Itoa(scale), "-prefix", prefix, "-out", dir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PWD="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terralith-gen -scale %d -prefix %s: %v\n%s", scale, prefix, err, out)
	}
	useFlociProviderBlock(t, dir)
}

// flociProviderBlock replaces terralith-gen's own provider block for the
// reason internal/live/statefulcost gives: the generator sets
// skip_requesting_account_id, which is right for output that must stand
// alone and wrong for any run resolving an ECS identity, because an ECS ARN
// carries the account id (issue #572).
const flociProviderBlock = `provider "aws" {
  region                      = "` + awsRegion + `"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
`

func useFlociProviderBlock(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "versions.tf")
	data, err := os.ReadFile(path) //nolint:gosec // a path this test just generated
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	const anchor = "provider \"aws\" {"
	idx := strings.Index(string(data), anchor)
	if idx < 0 {
		t.Fatalf("%s has no %q block; terralith-gen's versions.tf template changed", path, anchor)
	}
	if err := os.WriteFile(path, []byte(string(data[:idx])+flociProviderBlock), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func addLiveBlock(t *testing.T, dir, estate string) {
	t.Helper()
	path := filepath.Join(dir, "versions.tf")
	data, err := os.ReadFile(path) //nolint:gosec // a path this test just generated
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	const anchor = `required_version = ">= 1.5.0"`
	if !strings.Contains(string(data), anchor) {
		t.Fatalf("%s does not contain the expected anchor %q; terralith-gen's versions.tf template changed", path, anchor)
	}
	block := anchor + "\n\n  live {\n    estate = \"" + estate + "\"\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n  }"
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), anchor, block, 1)), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func awsEnv(endpoint string) []string {
	return append(os.Environ(), "AWS_ENDPOINT_URL="+endpoint)
}

func run(t *testing.T, dir string, env []string, bin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // fixed binaries, test-only
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustRun(t *testing.T, dir string, env []string, bin string, args ...string) {
	t.Helper()
	if out, err := run(t, dir, env, bin, args...); err != nil {
		t.Fatalf("%s %s in %s: %v\n%s", bin, strings.Join(args, " "), dir, err, tailOf(out, 40))
	}
}

func planSummary(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Plan: ") {
			return strings.ReplaceAll(l, " ", "_")
		}
	}
	return "NOT-EMPTY-no-plan-line"
}

func stampLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "newly stamped") || strings.Contains(l, "eligible for stamping") {
			return strings.TrimSpace(l)
		}
	}
	return "(no stamp summary line)"
}

func tailOf(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
