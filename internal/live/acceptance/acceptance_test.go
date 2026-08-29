// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"fmt"
	"path/filepath"
	"testing"
)

// The ungated half: the pure helpers the live tier leans on, so a parse
// regression is caught by `go test ./...` rather than only by a docker run.

func TestFailedAddresses(t *testing.T) {
	out := `
╷
│ Error: creating Lambda Function (tofu-lambda-cohort-fn): operation error

│   with aws_lambda_function.app,
│   on lambda.tf line 12, in resource "aws_lambda_function" "app":
╵
╷
│ Error: creating ECR Repository

│   with aws_ecr_repository.app,
│   on ecr.tf line 3:
│   with aws_ecr_repository.app,
╵
`
	got := failedAddresses(out)
	want := []string{"aws_lambda_function.app", "aws_ecr_repository.app"}
	if len(got) != len(want) {
		t.Fatalf("failedAddresses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("failedAddresses[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFirstErrorLine(t *testing.T) {
	if got := firstErrorLine("ok\n│ Error: boom happened\nmore", nil); got != "Error: boom happened" {
		t.Errorf("firstErrorLine = %q", got)
	}
}

func TestArtifactRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cohort-acceptance.json")

	art := buildArtifact("img@sha256:abc", "hashicorp/aws 6.58.0", "deadbeef", "2026-08-28T00:00:00Z", []CohortResult{
		{Name: "s3", Status: "pass", Phase: PhasePass, Resources: 6},
		{Name: "lambda", Status: "fail", Phase: PhaseApply, Resources: 9, FailedResources: []string{"aws_lambda_function.app"}},
	})
	if art.Totals.Cohorts != 2 || art.Totals.Pass != 1 || art.Totals.Fail != 1 {
		t.Fatalf("totals = %+v", art.Totals)
	}
	if art.Cohorts[0].Name != "lambda" {
		t.Fatalf("artifact not sorted by name: %q first", art.Cohorts[0].Name)
	}
	for _, r := range art.Cohorts {
		if r.LastRun == nil || r.LastRun.Commit != "deadbeef" || r.LastRun.Emulator != "img@sha256:abc" || r.LastRun.Date != "2026-08-28T00:00:00Z" {
			t.Fatalf("%s: last_run not stamped from this run's own provenance: %+v", r.Name, r.LastRun)
		}
	}

	if err := writeArtifact(path, art); err != nil {
		t.Fatal(err)
	}
	back, ok, err := readArtifact(path)
	if err != nil || !ok {
		t.Fatalf("readArtifact: ok=%t err=%v", ok, err)
	}
	if back.Totals != art.Totals || len(back.Cohorts) != 2 {
		t.Fatalf("round trip lost data: %+v", back)
	}
	for _, r := range back.Cohorts {
		if r.LastRun == nil || r.LastRun.Commit != "deadbeef" {
			t.Fatalf("%s: last_run did not survive the round trip: %+v", r.Name, r.LastRun)
		}
	}

	if _, ok, err := readArtifact(filepath.Join(dir, "absent.json")); err != nil || ok {
		t.Fatalf("a missing artifact must read as (not present, no error), got ok=%t err=%v", ok, err)
	}
}

// TestIsStale guards the per-row staleness primitive a future "cohort next"
// would key off of - the same comparison tools/gauntlet.IsStale makes for
// live/gauntlet.json's estate rows, proven load-bearing there by #414.
func TestIsStale(t *testing.T) {
	cases := []struct {
		name string
		r    CohortResult
		cur  string
		want bool
	}{
		{"never run", CohortResult{Name: "s3"}, "img@sha256:current", false},
		{"matches the current pin", CohortResult{Name: "s3", LastRun: &LastRun{Emulator: "img@sha256:current"}}, "img@sha256:current", false},
		{"measured against a superseded pin", CohortResult{Name: "s3", LastRun: &LastRun{Emulator: "img@sha256:old"}}, "img@sha256:current", true},
		{"unrecorded provenance reads as stale, never as current", CohortResult{Name: "s3", LastRun: &LastRun{Emulator: ""}}, "img@sha256:current", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsStale(c.r, c.cur); got != c.want {
				t.Errorf("IsStale(%+v, %q) = %t, want %t", c.r, c.cur, got, c.want)
			}
		})
	}
}

// TestStillInFlightSummarizesATimedOutApply pins the timeout detail: the
// four cohorts #149 could not attribute all looked like this, and the
// artifact must say what was hanging rather than "signal: killed".
func TestStillInFlightSummarizesATimedOutApply(t *testing.T) {
	out := "aws_msk_cluster.app: Still creating... [07m40s elapsed]\n" +
		"aws_msk_serverless_cluster.app: Still creating... [07m40s elapsed]\n" +
		"aws_msk_cluster.app: Still creating... [07m50s elapsed]\n" +
		"aws_msk_serverless_cluster.app: Still creating... [07m50s elapsed]\n"
	got := firstErrorLine(out, fmt.Errorf("signal: killed"))
	want := "deadline: still in flight: aws_msk_cluster.app at 07m50s, aws_msk_serverless_cluster.app at 07m50s"
	if got != want {
		t.Errorf("firstErrorLine = %q, want %q", got, want)
	}
	// An output with a real Error: line keeps the existing behavior.
	if got := firstErrorLine("x: Still creating... [01m00s elapsed]\n│ Error: boom\n", nil); got != "Error: boom" {
		t.Errorf("firstErrorLine with an Error line = %q", got)
	}
}
