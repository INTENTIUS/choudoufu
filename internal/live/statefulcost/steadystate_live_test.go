// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefulcost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestSteadyStateCostAgainstFloci is the apples-to-apples one, and the
// difference from TestStatefulCostAgainstFloci beside it is the whole point.
//
// That test's live column is built by ADOPTION: stock applies the estate,
// choudoufu is pointed at the resulting state file with `live-import
// -approve`, and the plans are taken immediately afterwards. live-import
// stamps markers and records nothing - "0 newly recorded" - so every plan in
// that column runs with an empty record store. It is a real measurement of a
// real moment, and it is not the moment an operator spends their life in.
//
// This one measures the estate both tools have been running for a while. Each
// side applies the estate ITSELF, with the artifact its own apply leaves
// behind - a state file for stock, a record store for choudoufu - and then
// plans it. Neither side is handed the other's work, and no adoption step
// appears anywhere in the numbers.
//
// Seconds and API calls are both reported per run, unaveraged, because they
// answer different questions and the fast one is not always the cheap one.
//
//	STEADY_SCALE=136 TF_FLOCI_TEST=1 go test ./internal/live/statefulcost/ \
//	  -run TestSteadyStateCostAgainstFloci -v -timeout 600m
//
//	STEADY_SCALE   terralith-gen -scale (default 1; 136 is 10,069 resources)
func TestSteadyStateCostAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "steadystate")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")

	scale := 1
	if v := os.Getenv("STEADY_SCALE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("STEADY_SCALE=%q is not a positive integer", v)
		}
		scale = n
	}

	root := flocitest.RepoRoot(t)
	choudoufuBin := flocitest.BuildTofu(t)
	flocitest.PluginCacheDir(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	// One emulator each, for the reason the neighbouring test already
	// records: a live plan sweeps the account, so any object belonging to
	// the other column's estate would be listed by it and would inflate the
	// number under test. Stock does not sweep, but giving it its own
	// emulator too keeps the two accounts identical in every other respect.
	portStock := flocitest.StartFloci(t, "cdf-steady-stock")
	proxyStock := flocitest.NewCountingProxy(t, flocitest.Endpoint(portStock))
	portLive := flocitest.StartFloci(t, "cdf-steady-live")
	proxyLive := flocitest.NewCountingProxy(t, flocitest.Endpoint(portLive))
	t.Logf("scale %d; stock emulator %s; live emulator %s", scale, flocitest.Endpoint(portStock), flocitest.Endpoint(portLive))

	var cols []*column

	// ── stock Terraform, holding the state file its own apply wrote ──────
	stockDir := filepath.Join(t.TempDir(), "stock")
	generate(t, root, stockDir, scale, "sta")
	envStock := awsEnv(proxyStock.Endpoint())
	mustRun(t, stockDir, envStock, terraformBin, "init", "-input=false", "-no-color")
	start := time.Now()
	if out, err := run(t, stockDir, envStock, terraformBin, "apply", "-input=false", "-auto-approve", "-no-color"); err != nil {
		t.Fatalf("stock apply: %v\n%s", err, tailOf(out, 40))
	} else {
		t.Logf("stock-terraform: apply %s (%s)", time.Since(start).Round(time.Second), applyLine(out))
	}
	cols = append(cols, timePlans(t, &column{
		Label: "stock-terraform", Bin: terraformBin, Dir: stockDir,
		Endpoint: proxyStock.Endpoint(),
		Args:     []string{"plan", "-input=false", "-no-color"},
	}, proxyStock))

	// ── choudoufu, holding the record store its own apply wrote ──────────
	liveDir := filepath.Join(t.TempDir(), "live")
	generate(t, root, liveDir, scale, "stb")
	addLiveBlock(t, liveDir, "steady-stb")
	envLive := awsEnv(proxyLive.Endpoint())
	mustRun(t, liveDir, envLive, choudoufuBin, "init", "-input=false", "-no-color")
	start = time.Now()
	if out, err := run(t, liveDir, envLive, choudoufuBin, "apply", "-input=false", "-auto-approve", "-no-color"); err != nil {
		t.Fatalf("choudoufu apply: %v\n%s", err, tailOf(out, 60))
	} else {
		t.Logf("choudoufu-live: apply %s (%s)", time.Since(start).Round(time.Second), applyLine(out))
	}
	// The record store must actually exist, or this column is the adoption
	// column under a different name and the whole comparison is void.
	records := filepath.Join(liveDir, ".tofu-records")
	if fi, err := os.Stat(records); err != nil || !fi.IsDir() {
		t.Fatalf("no record store at %s after choudoufu's own apply: %v - without it this column measures the adoption path, not the steady state", records, err)
	}
	// The state cache is the whole point of the comparison, so this reports
	// whether it exists rather than assuming it does. #685 writes it to
	// choudoufu-cache.tfstate under the data dir by default; if it is absent
	// after choudoufu's own apply, every plan below is a cache MISS and the
	// numbers are the rebuild-from-live path, not the steady state.
	cachePath := filepath.Join(liveDir, ".terraform", "choudoufu-cache.tfstate")
	if fi, err := os.Stat(cachePath); err != nil {
		t.Errorf("no state cache at %s after choudoufu's own apply (%v) - every plan below is a cache miss", cachePath, err)
	} else {
		t.Logf("state cache present: %s, %d bytes", cachePath, fi.Size())
	}

	cols = append(cols, timePlans(t, &column{
		Label: "choudoufu-live", Bin: choudoufuBin, Dir: liveDir,
		Endpoint: proxyLive.Endpoint(),
		Args:     []string{"plan", "-input=false", "-no-color"},
	}, proxyLive))

	// The control that says whether the cache is doing anything at all. Same
	// estate, same account, same plan, with persistence disabled. If this
	// column matches the one above call for call, the cache is present and
	// buying nothing, which is a finding about the cache rather than about
	// the cost of live mode.
	if os.Getenv("STEADY_CACHE_CONTROL") != "0" {
		offEnv := append(awsEnv(proxyLive.Endpoint()), "CHOUDOUFU_STATE_CACHE=off")
		cols = append(cols, timePlansEnv(t, &column{
			Label: "choudoufu-live-cache-off", Bin: choudoufuBin, Dir: liveDir,
			Endpoint: proxyLive.Endpoint(),
			Args:     []string{"plan", "-input=false", "-no-color"},
		}, proxyLive, offEnv))
	}

	report(t, scale, cols)
}

// timePlansEnv is timePlans with the environment overridden, so a column can
// differ from its neighbour in exactly one variable. timePlans builds its own
// env from the column's endpoint and has no seam for this.
func timePlansEnv(t *testing.T, c *column, proxy *flocitest.CountingProxy, env []string) *column {
	t.Helper()
	for i := 0; i < repeats; i++ {
		before := proxy.Total()
		start := time.Now()
		out, err := run(t, c.Dir, env, c.Bin, c.Args...)
		elapsed := time.Since(start)
		delta := proxy.Total() - before
		verdict := "empty"
		switch {
		case err != nil:
			verdict = "ERROR"
		case !strings.Contains(out, "No changes."):
			verdict = "NOT EMPTY"
		}
		if verdict != "empty" {
			t.Errorf("%s run %d: verdict %s - not comparable with the other columns\n%s", c.Label, i+1, verdict, tailOf(out, 30))
		}
		c.Seconds = append(c.Seconds, elapsed.Seconds())
		c.Calls = append(c.Calls, delta)
		c.Verdicts = append(c.Verdicts, verdict)
		t.Logf("%s run %d: %.2fs, %d API calls (%s)", c.Label, i+1, elapsed.Seconds(), delta, verdict)
	}
	return c
}
