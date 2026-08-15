// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// terraformBin is the stock binary that applies each cohort. Stock on
// purpose: the apply half of the round trip is "a user's existing tooling
// created these resources, tags included", and the claim under test is that
// choudoufu can then rebuild the estate from the markers alone.
const terraformBin = "terraform"

// providerPin is what every cohort's versions.tf pins; recorded in the
// artifact so a future provider bump cannot silently describe a different
// run.
const providerPin = "hashicorp/aws 6.59.0"

// Per-command deadlines. The apply deadline is generous because a cohort
// creates up to ~40 resources; the known pathological shape is a provider
// availability waiter polling a status floci never reports (the API Gateway
// REST API waiter, see internal/live/identity/table_generated.go's
// aws_api_gateway_rest_api note), which is exactly what the deadline is
// for: a timed-out phase is recorded as such, not hung forever.
const (
	initTimeout  = 3 * time.Minute
	applyTimeout = 8 * time.Minute
	planTimeout  = 5 * time.Minute
)

// TestCohortAcceptance is issue #108 criterion 2: apply each cohort against
// floci with stock terraform, delete the state, rebuild the plan from
// ownership markers with choudoufu live-plan, and assert it is empty.
//
// Two outputs. Every cohort's verdict is logged and - when
// TF_FLOCI_ACCEPTANCE_ARTIFACT=1 and no -run filter narrowed the set -
// written to live/cohort-acceptance.json. And the committed artifact is a
// ratchet: a cohort it records as passing FAILS this test if it stops
// passing, while a cohort recorded as failing is reported without failing
// the run, because its fixture is the known debt criterion 1 works off.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/acceptance -run TestCohortAcceptance -v -timeout 6h
func TestCohortAcceptance(t *testing.T) {
	flocitest.Gate(t, "cohort acceptance")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	flociPort := flocitest.StartFloci(t, "cdf-accept")
	t.Setenv("AWS_ENDPOINT_URL", flocitest.Endpoint(flociPort))
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	flocitest.PluginCacheDir(t)
	tofuBin := flocitest.BuildTofu(t)

	var results []CohortResult
	cohorts := cohortFixtures(t)
	for _, dir := range cohorts {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			res := runCohort(t, dir, tofuBin)
			results = append(results, res)
			t.Logf("%s: %s (phase %s)%s", name, res.Status, res.Phase, timedOutSuffix(res))
		})
	}

	artifactPath := filepath.Join(flocitest.RepoRoot(t), filepath.FromSlash(artifactRel))
	enforceRatchet(t, artifactPath, results, len(results) == len(cohorts))

	if os.Getenv("TF_FLOCI_ACCEPTANCE_ARTIFACT") == "" {
		t.Logf("TF_FLOCI_ACCEPTANCE_ARTIFACT not set; %s left untouched", artifactRel)
		return
	}
	// len(results), not a counter bumped at subtest entry: a verdict lands
	// in results only when runCohort returned one, so a -run filter AND a
	// harness t.Fatalf mid-cohort both leave the count short and refuse the
	// write. The counter this replaced was incremented before runCohort and
	// would have recorded a shrunk artifact as complete.
	if len(results) != len(cohorts) {
		t.Fatalf("only %d of %d cohorts produced a verdict (a -run filter, or a cohort aborted mid-run); refusing to write a partial %s", len(results), len(cohorts), artifactRel)
	}
	art := buildArtifact(flocitest.Image(), providerPin, results)
	if err := writeArtifact(artifactPath, art); err != nil {
		t.Fatalf("writing %s: %v", artifactRel, err)
	}
	t.Logf("wrote %s: %d pass, %d fail of %d cohorts", artifactRel, art.Totals.Pass, art.Totals.Fail, art.Totals.Cohorts)
}

// cohortFixtures is CohortDirs minus the directories with no .tf files
// (live/e2e/estates/example holds only a README).
func cohortFixtures(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range flocitest.CohortDirs(t) {
		matches, err := filepath.Glob(filepath.Join(dir, "*.tf"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) > 0 {
			out = append(out, dir)
		}
	}
	return out
}

// runCohort takes one cohort through the whole round trip and returns its
// verdict. It never calls t.Fatal for a fixture's own failure - the verdict
// is the datum - only for harness breakage.
func runCohort(t *testing.T, src, tofuBin string) CohortResult {
	t.Helper()

	name := filepath.Base(src)
	res := CohortResult{Name: name, Status: "fail", Resources: countResources(t, src)}
	dir := flocitest.CopyFixtureDir(t, src)
	tag := estateTag(t, src)

	if out, err, timedOut := runInit(t, dir, terraformBin); err != nil {
		res.Phase, res.Detail, res.TimedOut = PhaseInit, firstErrorLine(out, err), timedOut
		t.Logf("%s init:\n%s", name, out)
		return res
	}
	if out, err, timedOut := runTimed(t, dir, applyTimeout, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color"); err != nil {
		res.Phase, res.Detail, res.TimedOut = PhaseApply, firstErrorLine(out, err), timedOut
		res.FailedResources = failedAddresses(out)
		t.Logf("%s apply:\n%s", name, out)
		return res
	}
	if out, err, timedOut := runInit(t, dir, tofuBin); err != nil {
		res.Phase, res.Detail, res.TimedOut = PhaseInit, firstErrorLine(out, err), timedOut
		t.Logf("%s tofu init:\n%s", name, out)
		return res
	}

	for _, f := range []string{"terraform.tfstate", "terraform.tfstate.backup"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("%s: removing %s: %v", name, f, err)
		}
	}

	out, err, timedOut := runTimed(t, dir, planTimeout, tofuBin, "live-plan", "-estate="+tag, "-input=false", "-no-color")
	if err != nil {
		res.Phase, res.Detail, res.TimedOut = PhaseReplan, firstErrorLine(out, err), timedOut
		t.Logf("%s live-plan:\n%s", name, out)
		return res
	}
	if add, change, destroy, ok := flocitest.PlanSummary(out); ok && add+change+destroy > 0 {
		res.Phase = PhaseEmpty
		res.Detail = strings.TrimSpace(planSummaryOf(out))
		res.FailedResources = flocitest.ChangedResources(out)
		t.Logf("%s live-plan (not empty):\n%s", name, out)
		return res
	}
	if !strings.Contains(out, "No changes.") {
		// No summary line and no "No changes." - an output shape this
		// harness does not recognize is a failure to assert, not a pass.
		res.Phase = PhaseEmpty
		res.Detail = "live-plan output carries neither a plan summary nor \"No changes.\""
		t.Logf("%s live-plan (unrecognized):\n%s", name, out)
		return res
	}

	res.Status, res.Phase = "pass", PhasePass
	return res
}

// enforceRatchet fails the test for any cohort the committed artifact
// records as passing that did not pass this run. complete says every cohort
// produced a verdict; only then is a recorded-pass cohort with no verdict
// an error, since under a -run filter absence just means filtered out.
func enforceRatchet(t *testing.T, artifactPath string, results []CohortResult, complete bool) {
	t.Helper()

	committed, ok, err := readArtifact(artifactPath)
	if err != nil {
		t.Fatalf("reading the committed artifact: %v", err)
	}
	if !ok {
		t.Logf("no committed %s yet; every verdict is new", artifactRel)
		return
	}
	current := map[string]CohortResult{}
	for _, r := range results {
		current[r.Name] = r
	}
	// Iterate the COMMITTED passing set, not the current results: a
	// recorded-pass cohort whose fixture was deleted or renamed produces no
	// current result at all, and the first version of this loop - which
	// walked results - would have said nothing about the easiest way for a
	// pass to stop happening.
	for _, c := range committed.Cohorts {
		if c.Status != "pass" {
			continue
		}
		r, ok := current[c.Name]
		switch {
		case !ok && complete:
			t.Errorf("%s: recorded as passing in %s and produced no verdict this run - fixture deleted or renamed", c.Name, artifactRel)
		case !ok:
			// A -run filter left it out; nothing to say.
		case r.Status != "pass":
			t.Errorf("%s: recorded as passing in %s and now fails at phase %s: %s", c.Name, artifactRel, r.Phase, r.Detail)
		}
	}
}

// runInit runs an init under the shared plugin cache's cross-process lock,
// the same discipline flocitest.Run applies: the cache is not safe for
// concurrent writers, and this tier's inits run through runTimed's own exec
// plumbing rather than through Run.
func runInit(t *testing.T, dir, bin string) (string, error, bool) {
	t.Helper()

	defer flocitest.InitLock(t)()
	return runTimed(t, dir, initTimeout, bin, "init", "-input=false", "-no-color")
}

// runTimed runs one command under a deadline, returning its combined output.
func runTimed(t *testing.T, dir string, timeout time.Duration, name string, args ...string) (string, error, bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed binaries, test-only
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err, ctx.Err() != nil
}

// estateTag reads the cohort's estate_tag local, the value live-plan's
// -estate flag must name for markers to match.
var estateTagLine = regexp.MustCompile(`estate_tag\s*=\s*"([^"]+)"`)

func estateTag(t *testing.T, dir string) string {
	t.Helper()

	for _, rel := range []string{"locals.tf", filepath.Join("wrapped", "locals.tf")} {
		data, err := os.ReadFile(filepath.Join(dir, rel)) //nolint:gosec // fixture paths
		if err != nil {
			continue
		}
		if m := estateTagLine.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	t.Fatalf("%s: no estate_tag local found; the cohort is not in estate-gen's shape", dir)
	return ""
}

// countResources counts the resource blocks the cohort declares.
var resourceBlockLine = regexp.MustCompile(`(?m)^resource "`)

func countResources(t *testing.T, dir string) int {
	t.Helper()

	n := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".tf") {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // fixture paths
		if err != nil {
			return err
		}
		n += len(resourceBlockLine.FindAllIndex(data, -1))
		return nil
	})
	if err != nil {
		t.Fatalf("counting resources in %s: %v", dir, err)
	}
	return n
}

// failedAddresses pulls the resource addresses out of terraform's error
// blocks: the "  with aws_x.y," line each carries. -no-color drops the
// ANSI colors but keeps the box-drawing gutter (│), so the pattern admits
// it. The address is captured as everything up to the trailing comma
// rather than a hand-listed character class, because an instance key can
// legally hold "/", ":" or a space (an SSM path, a CIDR), and the first
// class silently dropped those addresses.
var withAddressLine = regexp.MustCompile(`(?m)^[│\s]*with (\S.*?),\s*$`)

func failedAddresses(out string) []string {
	seen := map[string]bool{}
	var addrs []string
	for _, m := range withAddressLine.FindAllStringSubmatch(out, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			addrs = append(addrs, m[1])
		}
	}
	return addrs
}

// planSummaryOf returns the renderer's one-line plan total, for Detail.
var planLine = regexp.MustCompile(`Plan: .*`)

func planSummaryOf(out string) string {
	return planLine.FindString(out)
}

// firstErrorLine is a one-line Detail for the artifact: the first "Error:"
// line of the output, or - when the deadline killed the run before any
// error was printed - the resources still in flight at the kill, or the
// exec error itself as the last resort. The box-drawing gutter -no-color
// keeps (│) is trimmed along with the whitespace.
//
// The in-flight summary exists because "signal: killed" attributes
// nothing: #149's re-measure left four cohorts unattributable until each
// was re-run verbosely by hand, and every one turned out to be a create
// hanging at the deadline - exactly what the partial output already said.
func firstErrorLine(out string, err error) string {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(line, "│╷╵ \t")
		trimmed = strings.TrimSpace(trimmed)
		if strings.HasPrefix(trimmed, "Error:") {
			return trimmed
		}
	}
	if inFlight := stillInFlight(out); inFlight != "" {
		return inFlight
	}
	if err != nil {
		return err.Error()
	}
	return "no Error: line in the output"
}

// stillCreatingLine matches terraform's progress lines, e.g.
// "aws_msk_cluster.app: Still creating... [07m50s elapsed]".
var stillCreatingLine = regexp.MustCompile(`(\S+): Still (?:creating|destroying|reading)\.\.\. \[(\d+m\d+s) elapsed\]`)

// stillInFlight summarizes the resources whose last progress line never
// resolved, with the elapsed time each was last seen at.
func stillInFlight(out string) string {
	last := map[string]string{}
	var order []string
	for _, m := range stillCreatingLine.FindAllStringSubmatch(out, -1) {
		if _, seen := last[m[1]]; !seen {
			order = append(order, m[1])
		}
		last[m[1]] = m[2]
	}
	if len(order) == 0 {
		return ""
	}
	parts := make([]string, 0, len(order))
	for _, addr := range order {
		parts = append(parts, addr+" at "+last[addr])
	}
	return "deadline: still in flight: " + strings.Join(parts, ", ")
}

func timedOutSuffix(res CohortResult) string {
	if res.TimedOut {
		return " (timed out)"
	}
	return ""
}
