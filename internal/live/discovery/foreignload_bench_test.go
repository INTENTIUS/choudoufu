// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// This file is issue #1032 unit 3's measurement: claim 14 - "a plan costs
// its estate, not its account" - at terralith scale, on the emulator.
//
// The question it answers is narrow and it is not the one
// slicing_bench_test.go asks. #584 measured how one estate's plan splits
// into legs as THAT ESTATE grows. Here the estate under measurement is held
// still and the ACCOUNT AROUND IT grows: a second, foreign estate of up to
// 3,705 resources is applied into the same emulator account under a
// different tofu-estate marker, and the same measurement is taken again.
// The sweep column is expected not to move, the read pass is expected to
// track the owned population and nothing else, and the Cloud Control column
// is reported whatever it turns out to be, because claim 14's own text names
// the account-wide list as the one thing it does not cover.
//
// FOREIGN IS A DIFFERENT ESTATE, NOT AN UNMARKED ONE. That is what
// live/smoke/scenarios/plan-cost-tracks-the-estate.sh does with its eight
// foreign resources (`live { estate = "demo-data" }`, applied with the
// binary under test), and matching it is what makes this the same claim at a
// different scale rather than a different claim. Unmarked, stock-applied
// resources are a weaker fixture for this question: the tag sweep's
// server-side filter excludes an absent marker for free, while a marker
// naming another estate has to be fetched and rejected.
//
//	# the three rows of the foreign-load table, one row per invocation
//	FOREIGN_SCALE=0  OWNED_SCALE=1  TF_FLOCI_TEST=1 env -u PWD go test ./internal/live/discovery/ -run TestForeignLoadAgainstFloci -v -timeout 60m
//	FOREIGN_SCALE=1  OWNED_SCALE=1  TF_FLOCI_TEST=1 env -u PWD go test ./internal/live/discovery/ -run TestForeignLoadAgainstFloci -v -timeout 60m
//	FOREIGN_SCALE=50 OWNED_SCALE=1  TF_FLOCI_TEST=1 env -u PWD go test ./internal/live/discovery/ -run TestForeignLoadAgainstFloci -v -timeout 180m
//	# the inverse: a small foreign estate beside a large owned one
//	FOREIGN_SCALE=1  OWNED_SCALE=50 TF_FLOCI_TEST=1 env -u PWD go test ./internal/live/discovery/ -run TestForeignLoadAgainstFloci -v -timeout 180m
//	# the control: the same row with the sweep's estate filter removed
//	FOREIGN_BREAK=1 FOREIGN_SCALE=1 OWNED_SCALE=1 TF_FLOCI_TEST=1 env -u PWD go test ...
//
// Not a ratchet. Nothing here asserts a threshold on a call count; the
// deliverable is the recorded row. What it does assert is that every plan it
// counted actually ran and actually proposed nothing, for the reason
// statefulcost_live_test.go gives about its own columns: a plan that errored
// or that proposed work is not the operation the other rows are, and
// reporting its calls beside theirs would be the comparison this file exists
// to make honest.

// foreignRow is one row of the foreign-load table.
type foreignRow struct {
	Commit   string `json:"commit"`
	Emulator string `json:"emulator"`
	Break    bool   `json:"break"`

	OwnedScale       int `json:"owned_scale"`
	OwnedResources   int `json:"owned_resources"`
	ForeignScale     int `json:"foreign_scale"`
	ForeignResources int `json:"foreign_resources"`

	ForeignApplySeconds float64 `json:"foreign_apply_seconds"`
	OwnedApplySeconds   float64 `json:"owned_apply_seconds"`

	// The operator-visible number: what `choudoufu plan` actually costs on
	// this account, counted at the proxy, with the record store the apply
	// left behind. This is the column comparable with what-you-pay.md's
	// 157 calls for the 79-instance adopted plan.
	PlanCalls    int            `json:"plan_calls"`
	PlanSeconds  float64        `json:"plan_seconds"`
	PlanVerdict  string         `json:"plan_verdict"`
	PlanByAPI    map[string]int `json:"plan_by_api"`
	PlanCloudCtl int            `json:"plan_cloud_control_calls"`

	// The leg split, taken in process the way plan-cost.md's own table is,
	// with no record store: sweep = Discover, read pass = BuildFrom.
	Sweep       int `json:"sweep_calls"`
	ReadPass    int `json:"read_pass_calls"`
	Total       int `json:"total_calls"`
	TaggingLeg  int `json:"tagging_leg_calls"`
	NativeSweep int `json:"native_sweep_calls"`

	// CloudControlList is the account-wide list claim 14 names as its own
	// exception: CloudApiService.ListResources carries no server-side tag
	// filter at all, so it enumerates the account rather than the estate.
	// CloudControlGet is the per-candidate refinement that list can force.
	CloudControlList int `json:"cloud_control_list_calls"`
	CloudControlGet  int `json:"cloud_control_get_calls"`

	Materialized int `json:"materialized"`
	Bound        int `json:"bound"`
	Unclaimed    int `json:"unclaimed"`

	DiscoverByAPI map[string]int `json:"discover_by_api"`
	BuildByAPI    map[string]int `json:"build_by_api"`
}

// cloudControlTargets are the X-Amz-Target values [flocitest.CountingProxy]
// records for the two Cloud Control operations discovery uses. The proxy
// names a JSON-RPC request by its target header verbatim
// (flocitest.actionOf), so these are the exact keys its count maps carry.
const (
	ccListTarget = "CloudApiService.ListResources"
	ccGetTarget  = "CloudApiService.GetResource"
)

func TestForeignLoadAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/foreign-load")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "go")

	ownedScale := envIntAtLeast(t, "OWNED_SCALE", 1, 1)
	foreignScale := envIntAtLeast(t, "FOREIGN_SCALE", 0, 0)
	breaking := os.Getenv("FOREIGN_BREAK") == "1"

	root := flocitest.RepoRoot(t)
	work := t.TempDir()
	choudoufuBin := flocitest.BuildTofu(t)
	flocitest.PluginCacheDir(t)

	port := startFlociForForeignLoad(t)
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(port))
	t.Setenv("AWS_ENDPOINT_URL", proxy.Endpoint())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	t.Logf("emulator %s via counting proxy %s", flocitest.Endpoint(port), proxy.Endpoint())

	row := &foreignRow{
		Commit:           flocitest.HeadCommit(t),
		Emulator:         flocitest.Image(),
		Break:            breaking,
		OwnedScale:       ownedScale,
		OwnedResources:   terralithResources(ownedScale),
		ForeignScale:     foreignScale,
		ForeignResources: terralithResources(foreignScale),
	}

	// ---- 1. the foreign estate, if this row has one. A different
	// tofu-estate marker on every taggable object, exactly as
	// plan-cost-tracks-the-estate.sh's demo-data estate carries one.
	if foreignScale > 0 {
		dir := filepath.Join(work, "foreign")
		generateTerralith(t, root, dir, foreignScale, "fg")
		useFlociProvider(t, dir)
		addLiveBlockTo(t, dir, "foreign-load-fg")
		flocitest.Run(t, dir, choudoufuBin, "init", "-input=false", "-no-color")
		start := time.Now()
		out, err := runIn(dir, choudoufuBin, "apply", "-auto-approve", "-input=false", "-no-color")
		row.ForeignApplySeconds = time.Since(start).Seconds()
		if err != nil {
			t.Fatalf("applying the foreign estate at -scale %d: %v\n%s", foreignScale, err, lastLines(out, 40))
		}
		t.Logf("FOREIGN APPLY scale=%d resources=%d seconds=%.1f %s",
			foreignScale, row.ForeignResources, row.ForeignApplySeconds, applySummary(out))
	} else {
		t.Logf("FOREIGN APPLY scale=0 resources=0 (the empty-account row)")
	}

	// ---- 2. the owned estate, beside it, in the same account.
	ownedDir := filepath.Join(work, "owned")
	const ownedEstate = "foreign-load-ow"
	generateTerralith(t, root, ownedDir, ownedScale, "ow")
	useFlociProvider(t, ownedDir)
	addLiveBlockTo(t, ownedDir, ownedEstate)
	flocitest.Run(t, ownedDir, choudoufuBin, "init", "-input=false", "-no-color")
	start := time.Now()
	out, err := runIn(ownedDir, choudoufuBin, "apply", "-auto-approve", "-input=false", "-no-color")
	row.OwnedApplySeconds = time.Since(start).Seconds()
	if err != nil {
		t.Fatalf("applying the owned estate at -scale %d: %v\n%s", ownedScale, err, lastLines(out, 40))
	}
	t.Logf("OWNED APPLY scale=%d resources=%d seconds=%.1f %s",
		ownedScale, row.OwnedResources, row.OwnedApplySeconds, applySummary(out))

	// ---- 3. the operator-visible plan, counted at the proxy.
	proxy.Reset()
	planStart := time.Now()
	planArgs := []string{"plan", "-input=false", "-no-color"}
	if breaking {
		// The control. -adoption-only is the account-wide question, and the
		// thing it changes is exactly the one under test: scanType's
		// collectUnclaimed branch drops the server-side estate filter and
		// widens every list to ScopeAll, "because a server-side estate
		// filter would hide" an unclaimed resource. If the cost does not
		// climb with the foreign population here, then the scoped column
		// was flat for some reason other than the scoping.
		planArgs = append(planArgs, "-adoption-only")
	}
	planOut, planErr := runIn(ownedDir, choudoufuBin, planArgs...)
	row.PlanSeconds = time.Since(planStart).Seconds()
	row.PlanCalls = proxy.Total()
	row.PlanByAPI = copyCountMap(proxy.Counts())
	row.PlanCloudCtl = row.PlanByAPI[ccListTarget] + row.PlanByAPI[ccGetTarget]
	switch {
	case planErr != nil:
		row.PlanVerdict = "ERROR"
	case strings.Contains(planOut, "No changes."):
		row.PlanVerdict = "empty"
	default:
		row.PlanVerdict = "NOT EMPTY"
	}
	if row.PlanVerdict != "empty" {
		// Not fatal under the control: -adoption-only's whole purpose is to
		// offer unclaimed resources for adoption, so a non-empty plan there
		// is the mode working, not the fixture broken.
		if breaking {
			t.Logf("plan -adoption-only verdict %s (expected under the control)", row.PlanVerdict)
		} else {
			t.Errorf("the owned estate's plan came back %s, so its call count is not comparable with the other rows':\n%s",
				row.PlanVerdict, lastLines(planOut, 60))
		}
	}
	t.Logf("PLAN calls=%d seconds=%.1f verdict=%s cloud_control=%d",
		row.PlanCalls, row.PlanSeconds, row.PlanVerdict, row.PlanCloudCtl)

	// ---- 4. the leg split, in process, no record store: sweep against
	// read pass, the shape plan-cost.md's own table reports.
	split := measureLegsWith(t, ownedDir, ownedEstate, proxy, breaking)
	row.Sweep = split.DiscoverCalls
	row.ReadPass = split.BuildCalls
	row.Total = split.DiscoverCalls + split.BuildCalls
	row.TaggingLeg = split.TaggingLegCalls
	row.NativeSweep = split.NativeSweepCalls
	row.Materialized = split.Materialized
	row.Bound = split.Bound
	row.Unclaimed = split.Unclaimed
	row.DiscoverByAPI = split.DiscoverByAPI
	row.BuildByAPI = split.BuildByAPI
	row.CloudControlList = split.DiscoverByAPI[ccListTarget] + split.BuildByAPI[ccListTarget]
	row.CloudControlGet = split.DiscoverByAPI[ccGetTarget] + split.BuildByAPI[ccGetTarget]

	reportForeignRow(t, row)
}

// reportForeignRow prints the row in the shape the deliverable table wants,
// then the per-action breakdowns behind every column in it, so a reader can
// check any cell against the actions that produced it rather than taking
// the sum on trust.
func reportForeignRow(t *testing.T, row *foreignRow) {
	t.Helper()

	t.Logf("")
	t.Logf("FOREIGN-LOAD ROW break=%v emulator=%s commit=%s", row.Break, row.Emulator, row.Commit)
	t.Logf("  owned            scale=%d resources=%d apply=%.1fs", row.OwnedScale, row.OwnedResources, row.OwnedApplySeconds)
	t.Logf("  foreign          scale=%d resources=%d apply=%.1fs", row.ForeignScale, row.ForeignResources, row.ForeignApplySeconds)
	t.Logf("  plan (operator)  calls=%d seconds=%.1f verdict=%s", row.PlanCalls, row.PlanSeconds, row.PlanVerdict)
	t.Logf("  sweep            %d  (tagging leg %d, native sweep %d)", row.Sweep, row.TaggingLeg, row.NativeSweep)
	t.Logf("  read pass        %d", row.ReadPass)
	t.Logf("  cloud control    list=%d get=%d", row.CloudControlList, row.CloudControlGet)
	t.Logf("  total            %d", row.Total)
	t.Logf("  materialized=%d bound=%d unclaimed=%d", row.Materialized, row.Bound, row.Unclaimed)

	logCounts(t, "plan, by action", row.PlanByAPI)
	logCounts(t, "sweep (Discover), by action", row.DiscoverByAPI)
	logCounts(t, "read pass (BuildFrom), by action", row.BuildByAPI)

	if out := os.Getenv("FOREIGN_LOAD_OUT"); out != "" {
		data, err := json.MarshalIndent(row, "", "  ")
		if err != nil {
			t.Fatalf("encoding the foreign-load row: %v", err)
		}
		if err := os.WriteFile(out, append(data, '\n'), 0o600); err != nil {
			t.Fatalf("writing %s: %v", out, err)
		}
		t.Logf("row written to %s", out)
	}
}

func logCounts(t *testing.T, label string, m map[string]int) {
	t.Helper()

	t.Logf("")
	t.Logf("  %s:", label)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		t.Logf("    %-46s %d", k, m[k])
	}
}

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// terralithResources is tools/terralith-gen's own composition formula,
// 74N + 5, so a row can name the population it stood up without parsing the
// generator's report line. Scale 0 means "no estate at all", which the
// generator itself refuses and this row spells as zero resources.
func terralithResources(scale int) int {
	if scale <= 0 {
		return 0
	}
	return 74*scale + 5
}

func generateTerralith(t *testing.T, root, dir string, scale int, prefix string) {
	t.Helper()

	cmd := exec.Command("go", "run", "./tools/terralith-gen", //nolint:gosec // fixed binary and args, test-only
		"-scale", strconv.Itoa(scale), "-prefix", prefix, "-out", dir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PWD="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terralith-gen -scale %d -prefix %s: %v\n%s", scale, prefix, err, out)
	}
	t.Logf("terralith-gen(%s): %s", prefix, strings.TrimSpace(string(out)))
}

// foreignFlociProvider is the provider block
// live/live-cert/terralith-scale.sh's provider_block emits for TARGET=floci,
// for the reason statefulcost_live_test.go's own copy gives: the
// generator's block sets skip_requesting_account_id, which is right for a
// generator whose output must stand alone and wrong for any run that
// resolves an ECS identity, because an ECS ARN carries the account id
// (issue #572).
const foreignFlociProvider = `provider "aws" {
  region                      = "` + awsRegion + `"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
`

func useFlociProvider(t *testing.T, dir string) {
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
	if err := os.WriteFile(path, []byte(string(data[:idx])+foreignFlociProvider), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func addLiveBlockTo(t *testing.T, dir, estate string) {
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
	updated := strings.Replace(string(data), anchor, block, 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// startFlociForForeignLoad starts the emulator. FOREIGN_LOAD_PORT pins the
// host port, because a worker running beside others is given a port pair
// rather than allowed to take any free one; with it unset this falls back
// to flocitest's own kernel-assigned free port.
func startFlociForForeignLoad(t *testing.T) string {
	t.Helper()

	pinned := os.Getenv("FOREIGN_LOAD_PORT")
	if pinned == "" {
		return flocitest.StartFloci(t, "cdf-foreignload")
	}
	if _, err := strconv.Atoi(pinned); err != nil {
		t.Fatalf("FOREIGN_LOAD_PORT=%q is not a port number", pinned)
	}
	name := fmt.Sprintf("cdf-foreignload-%d-%s", os.Getpid(), pinned)
	_ = exec.Command("docker", "rm", "-f", name).Run()
	out, err := exec.Command("docker", "run", "-d", "--rm", //nolint:gosec // fixed args, test-only
		"-p", "127.0.0.1:"+pinned+":4566", "--name", name, flocitest.Image()).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run --name %s -p %s: %v\n%s", name, pinned, err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput(); err != nil {
			t.Logf("removing the floci container %s: %v\n%s", name, err, out)
		}
	})
	waitHealthy(t, name, pinned)
	return pinned
}

// waitHealthy blocks until the emulator answers its health endpoint, the
// same wait flocitest.startFlociOnce does for the free-port path.
func waitHealthy(t *testing.T, container, hostPort string) {
	t.Helper()

	health := flocitest.Endpoint(hostPort) + "/_localstack/health"
	deadline := time.Now().Add(3 * time.Minute)
	for {
		resp, err := http.Get(health) //nolint:gosec,noctx // fixed localhost URL, test-only
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", "--tail", "40", container).CombinedOutput()
			t.Fatalf("floci did not become healthy at %s within 3m (last error: %v)\ncontainer logs:\n%s", health, err, logs)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// envIntAtLeast reads an integer environment variable with a floor, which
// envInt cannot do: envInt rejects 0, and 0 is a meaningful value here (the
// empty-account row).
func envIntAtLeast(t *testing.T, name string, def, min int) int {
	t.Helper()

	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min {
		t.Fatalf("%s=%q is not an integer >= %d", name, v, min)
	}
	return n
}

func runIn(dir, bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...) //nolint:gosec // fixed binaries, test-only
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func applySummary(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Apply complete!") {
			return strings.TrimSpace(line)
		}
	}
	return "(no Apply complete! line)"
}

func copyCountMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
