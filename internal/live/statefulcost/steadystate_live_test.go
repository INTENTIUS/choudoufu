// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefulcost

import (
	"fmt"
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
	// STEADY_FLAT_PER_TYPE swaps terralith-gen for the tagging-served estate
	// above, on BOTH columns so the comparison still holds. It is the only
	// shape on which the state cache can be asked to do anything (#1085).
	flatPerType := 0
	if v := os.Getenv("STEADY_FLAT_PER_TYPE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("STEADY_FLAT_PER_TYPE=%q is not a positive integer", v)
		}
		flatPerType = n
		t.Logf("tagging-served estate: %d objects per type across 5 types, plus one VPC = %d", n, n*5+1)
	}

	stockDir := filepath.Join(t.TempDir(), "stock")
	if flatPerType > 0 {
		writeFlatServed(t, stockDir, "sta", flatPerType)
	} else {
		generate(t, root, stockDir, scale, "sta")
	}
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
	if flatPerType > 0 {
		writeFlatServed(t, liveDir, "stb", flatPerType)
	} else {
		generate(t, root, liveDir, scale, "stb")
	}
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

	// The columns where the cache is actually allowed to serve. Both gates
	// in live_mode.go require !PlanRefresh: a default plan refreshes every
	// instance by design, so it switches the cache off and never even
	// computes the vouch types. The reads policy defaults to "selective",
	// which is the other gate, so -refresh=false is the whole difference.
	//
	// This is choudoufu's equivalent of stock planning from the state file
	// its own apply wrote: prior state taken from the artifact on disk
	// rather than re-read from the account.
	if os.Getenv("STEADY_REFRESH_FALSE") != "0" {
		cols = append(cols, timePlansEnv(t, &column{
			Label: "choudoufu-live-refresh-false", Bin: choudoufuBin, Dir: liveDir,
			Endpoint: proxyLive.Endpoint(),
			Args:     []string{"plan", "-refresh=false", "-input=false", "-no-color"},
		}, proxyLive, awsEnv(proxyLive.Endpoint())))

		offEnv2 := append(awsEnv(proxyLive.Endpoint()), "CHOUDOUFU_STATE_CACHE=off")
		cols = append(cols, timePlansEnv(t, &column{
			Label: "choudoufu-refresh-false-cache-off", Bin: choudoufuBin, Dir: liveDir,
			Endpoint: proxyLive.Endpoint(),
			Args:     []string{"plan", "-refresh=false", "-input=false", "-no-color"},
		}, proxyLive, offEnv2))
	}

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

// ---------------------------------------------------------------------------
// The tagging-served estate (issues #1085, #1086, #1087)
// ---------------------------------------------------------------------------

// flatServedConfig generates an estate the state cache can actually serve.
//
// Every type here is implemented by the pinned emulator AND outside
// `taggingAPIUnservedServices`, whose only entry is `aws_iam_`. That is the
// whole point: the sweep's one estate-filtered GetResources sights these
// objects, so a cached entry can be vouched for the price of a page rather
// than the price of a read per instance. On the terralith it cannot - 83.7%
// of that estate is identity, which GetResources does not index at all, so
// the cache is structurally unable to help there (#1085).
//
// Five services rather than one, because the sweep is O(types) and a
// single-type estate would measure a shape no operator has. They are chosen
// free at rest and high-quota on real AWS, which is the property the
// terralith gets from being identity-heavy and which a replacement has to
// earn some other way: log groups, queues, topics, tables and security
// groups all cost nothing empty.
//
// %[1]s prefix, %[2]d objects per type.
const flatServedConfig = `# Generated by TestSteadyStateCostAgainstFloci. A tagging-served estate.

resource "aws_cloudwatch_log_group" "g" {
  count             = %[2]d
  name              = format("%[1]s-lg-%%05d", count.index)
  retention_in_days = 1
  tags              = { Scenario = "steady" }
}

resource "aws_sqs_queue" "q" {
  count = %[2]d
  name  = format("%[1]s-q-%%05d", count.index)
  tags  = { Scenario = "steady" }
}

resource "aws_sns_topic" "t" {
  count = %[2]d
  name  = format("%[1]s-t-%%05d", count.index)
  tags  = { Scenario = "steady" }
}

resource "aws_dynamodb_table" "d" {
  count        = %[2]d
  name         = format("%[1]s-d-%%05d", count.index)
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"
  attribute {
    name = "id"
    type = "S"
  }
  tags = { Scenario = "steady" }
}

resource "aws_security_group" "s" {
  count       = %[2]d
  name        = format("%[1]s-sg-%%05d", count.index)
  description = "steady-state scenario"
  vpc_id      = aws_vpc.main.id
  tags        = { Scenario = "steady" }
}

resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  tags       = { Scenario = "steady" }
}
`

func writeFlatServed(t *testing.T, dir, prefix string, perType int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	main := fmt.Sprintf(flatServedConfig, prefix, perType)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o600); err != nil {
		t.Fatalf("writing the flat configuration: %v", err)
	}
	versions := `terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region                      = "` + awsRegion + `"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
`
	if err := os.WriteFile(filepath.Join(dir, "versions.tf"), []byte(versions), 0o600); err != nil {
		t.Fatalf("writing the flat provider wiring: %v", err)
	}
}
