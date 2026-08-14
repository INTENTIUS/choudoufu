// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
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

	art := buildArtifact("img@sha256:abc", "hashicorp/aws 6.58.0", []CohortResult{
		{Name: "s3", Status: "pass", Phase: PhasePass, Resources: 6},
		{Name: "lambda", Status: "fail", Phase: PhaseApply, Resources: 9, FailedResources: []string{"aws_lambda_function.app"}},
	})
	if art.Totals.Cohorts != 2 || art.Totals.Pass != 1 || art.Totals.Fail != 1 {
		t.Fatalf("totals = %+v", art.Totals)
	}
	if art.Cohorts[0].Name != "lambda" {
		t.Fatalf("artifact not sorted by name: %q first", art.Cohorts[0].Name)
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

	if _, ok, err := readArtifact(filepath.Join(dir, "absent.json")); err != nil || ok {
		t.Fatalf("a missing artifact must read as (not present, no error), got ok=%t err=%v", ok, err)
	}
}
