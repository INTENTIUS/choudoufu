// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefulcost

import (
	"fmt"
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

const (
	awsRegion = "us-east-1"

	// terraformBin and tofuBin are the two stock binaries this measurement
	// compares against, both taken from PATH. Terraform is the baseline
	// #588 states its claim against; OpenTofu is the fork's own upstream,
	// and without it a difference between Terraform and choudoufu cannot be
	// attributed to the fork rather than to the Terraform/OpenTofu split.
	terraformBin = "terraform"
	tofuBin      = "tofu"

	// repeats is how many times each column's plan runs. Every value is
	// reported; nothing is averaged and nothing is discarded, which is the
	// same discipline live/live-cert/terralith-scale.sh's timed_plans uses.
	repeats = 3
)

// scaleFromEnv reads STATEFUL_COST_SCALE, defaulting to 1. Scale 4 is
// measurable for the three columns with no live block; the fourth column
// is blocked at scale 4 by issue #580 (a false count-index refusal on the
// module-nested pod), which is itself a finding this test records rather
// than works around.
func scaleFromEnv(t *testing.T) int {
	t.Helper()
	v := os.Getenv("STATEFUL_COST_SCALE")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("STATEFUL_COST_SCALE=%q is not a positive integer", v)
	}
	return n
}

// column is one measured plan configuration: which binary, which directory,
// which emulator endpoint, and whether a live block is present.
type column struct {
	Label     string
	Bin       string
	Dir       string
	Endpoint  string
	Args      []string
	Seconds   []float64
	Calls     []int
	Verdicts  []string
	ByAPILast map[string]int
}

// TestStatefulCostAgainstFloci is the measurement.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/statefulcost/ -run TestStatefulCostAgainstFloci -v -timeout 40m
//	STATEFUL_COST_SCALE=4 TF_FLOCI_TEST=1 go test ./internal/live/statefulcost/ -run TestStatefulCostAgainstFloci -v -timeout 60m
//
// It asserts nothing about the numbers. It asserts only that every plan it
// timed actually ran and actually proposed no changes, because a plan that
// errored or that proposed work is not the same operation as the ones it is
// being compared against, and reporting its seconds beside theirs would be
// the comparison this file exists to make honest.
func TestStatefulCostAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "statefulcost")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, tofuBin)
	flocitest.RequireBinary(t, "go")

	scale := scaleFromEnv(t)
	root := flocitest.RepoRoot(t)
	choudoufuBin := flocitest.BuildTofu(t)
	flocitest.PluginCacheDir(t)

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	runLive := os.Getenv("STATEFUL_COST_LIVE") != "0"

	// Two emulators, not one. The migrated column's whole cost is an
	// estate-wide discovery sweep, so any object in the account that
	// belongs to another column's estate would be listed by it and would
	// inflate exactly the number under test. The stateful columns cannot
	// contaminate each other the same way - a stateful plan reads each
	// instance by the ID its state file holds and issues no estate-wide
	// list - so they share one emulator.
	portStateful := flocitest.StartFloci(t, "cdf-sfcost-a")
	proxyStateful := flocitest.NewCountingProxy(t, flocitest.Endpoint(portStateful))
	t.Logf("stateful emulator %s via proxy %s", flocitest.Endpoint(portStateful), proxyStateful.Endpoint())

	var cols []*column

	// ── the three stateful columns ──────────────────────────────────────
	stateful := []struct {
		label  string
		bin    string
		prefix string
	}{
		{"stock-terraform-stateful", terraformBin, "sca"},
		{"stock-tofu-stateful", tofuBin, "scb"},
		{"choudoufu-stateful", choudoufuBin, "scc"},
	}
	for _, s := range stateful {
		dir := filepath.Join(t.TempDir(), s.label)
		generate(t, root, dir, scale, s.prefix)
		env := awsEnv(proxyStateful.Endpoint())

		mustRun(t, dir, env, s.bin, "init", "-input=false", "-no-color")
		applyStart := time.Now()
		out, err := run(t, dir, env, s.bin, "apply", "-input=false", "-auto-approve", "-no-color")
		if err != nil {
			t.Fatalf("%s apply in %s: %v\n%s", s.label, dir, err, tailOf(out, 40))
		}
		t.Logf("%s: apply %s (%s)", s.label, time.Since(applyStart).Round(time.Millisecond), applyLine(out))

		cols = append(cols, timePlans(t, &column{
			Label: s.label, Bin: s.bin, Dir: dir,
			Endpoint: proxyStateful.Endpoint(),
			Args:     []string{"plan", "-input=false", "-no-color"},
		}, proxyStateful))
	}

	// ── the migrated, live-block column ─────────────────────────────────
	if runLive {
		portLive := flocitest.StartFloci(t, "cdf-sfcost-b")
		proxyLive := flocitest.NewCountingProxy(t, flocitest.Endpoint(portLive))
		env := awsEnv(proxyLive.Endpoint())

		prefix := "scd"
		estate := "sfcost-" + prefix

		// The stock half of the migration: stock terraform applies the
		// unmodified generator output and keeps its state file, exactly
		// as live/live-cert/terralith-scale.sh's cold_deploy does. The
		// live column then adopts THAT estate rather than one choudoufu
		// created, which is the migration the product actually claims.
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

		for _, form := range []struct {
			label string
			args  []string
		}{
			{"choudoufu-live-plan", []string{"live-plan", "-no-color"}},
			{"choudoufu-live-block-plan", []string{"plan", "-input=false", "-no-color"}},
		} {
			cols = append(cols, timePlans(t, &column{
				Label: form.label, Bin: choudoufuBin, Dir: adoptedDir,
				Endpoint: proxyLive.Endpoint(), Args: form.args,
			}, proxyLive))
		}
	}

	report(t, scale, cols)
}

// timePlans runs one column's plan `repeats` times, recording wall clock,
// the proxy's call-count delta across each run, and the plan's own verdict.
func timePlans(t *testing.T, c *column, proxy *flocitest.CountingProxy) *column {
	t.Helper()
	for i := 0; i < repeats; i++ {
		before := proxy.Total()
		beforeByAPI := copyCounts(proxy.Counts())
		start := time.Now()
		out, err := run(t, c.Dir, awsEnv(c.Endpoint), c.Bin, c.Args...)
		elapsed := time.Since(start)
		delta := proxy.Total() - before

		verdict := "empty"
		switch {
		case err != nil:
			verdict = "ERROR"
		case !strings.Contains(out, "No changes."):
			verdict = "NOT-EMPTY"
		}
		if verdict != "empty" {
			t.Errorf("%s run %d: verdict %s - its seconds are not comparable with the other columns'\n%s",
				c.Label, i+1, verdict, tailOf(out, 40))
		}

		c.Seconds = append(c.Seconds, elapsed.Seconds())
		c.Calls = append(c.Calls, delta)
		c.Verdicts = append(c.Verdicts, verdict)
		c.ByAPILast = diffCounts(beforeByAPI, proxy.Counts())
		t.Logf("%s run %d: %.2fs, %d API calls (%s)", c.Label, i+1, elapsed.Seconds(), delta, verdict)
	}
	return c
}

func report(t *testing.T, scale int, cols []*column) {
	t.Helper()
	t.Logf("")
	t.Logf("STATEFUL-COST REPORT scale=%d emulator=%s", scale, flocitest.Image())
	t.Logf("%-28s %-34s %-24s %s", "column", "seconds (3 runs)", "API calls (3 runs)", "verdicts")
	for _, c := range cols {
		t.Logf("%-28s %-34s %-24s %s", c.Label, joinFloats(c.Seconds), joinInts(c.Calls), strings.Join(c.Verdicts, ","))
	}
	for _, c := range cols {
		t.Logf("")
		t.Logf("%s: API calls by action, third run", c.Label)
		keys := make([]string, 0, len(c.ByAPILast))
		for k := range c.ByAPILast {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Logf("  %-46s %d", k, c.ByAPILast[k])
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

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
	t.Logf("terralith-gen(%s): %s", prefix, strings.TrimSpace(string(out)))
}

// addLiveBlock puts a live block inside the generated versions.tf's
// terraform block. The anchor is the generator's own required_version line;
// a missing anchor fails loudly rather than producing a directory that is
// silently still stateful, which would make the fourth column measure the
// third one over again.
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
	updated := strings.Replace(string(data), anchor, block, 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
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
	out, err := run(t, dir, env, bin, args...)
	if err != nil {
		t.Fatalf("%s %s in %s: %v\n%s", bin, strings.Join(args, " "), dir, err, tailOf(out, 40))
	}
}

func tailOf(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func applyLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Apply complete!") {
			return strings.TrimSpace(l)
		}
	}
	return "(no Apply complete line)"
}

func stampLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "newly stamped") || strings.Contains(l, "eligible for stamping") {
			return strings.TrimSpace(l)
		}
	}
	return "(no stamp summary line)"
}

func copyCounts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func diffCounts(before, after map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range after {
		if d := v - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

func joinFloats(vs []float64) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, fmt.Sprintf("%.2f", v))
	}
	return strings.Join(parts, " ")
}

func joinInts(vs []int) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, strconv.Itoa(v))
	}
	return strings.Join(parts, " ")
}
