// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"io"
	"os"
	"testing"
)

// smokeEnv gates every test in this file: they shell out to `go run
// ./tools/<name>` against the real checkout (network for the first
// provider/registry/docs fetch, several minutes of wall time even warm),
// so they're opt-in rather than part of the default `go test ./...` run -
// same shape as tools/registry-gen's own realZipOrSkip gate, but env-based
// since these smoke tests need network access registry-gen's cache-file
// gate doesn't require.
const smokeEnv = "ADMISSION_PIPELINE_SMOKE"

func smokeOrSkip(t *testing.T) string {
	t.Helper()
	if os.Getenv(smokeEnv) == "" {
		t.Skipf("set %s=1 to run admission-pipeline's subprocess smoke tests (network + several minutes)", smokeEnv)
	}
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	return root
}

// TestSmokeDetect_RealEndpoints runs Detect against the real
// registry.terraform.io and CloudFormation Registry endpoints, read-only -
// no artifact is written. This is the same call `go run
// ./tools/admission-pipeline` makes for its DETECT stage.
func TestSmokeDetect_RealEndpoints(t *testing.T) {
	root := smokeOrSkip(t)

	report, err := Detect(context.Background(), root)
	if err != nil {
		t.Fatalf("Detect against the real endpoints: %v", err)
	}
	t.Logf("%s", report.String())
}

// TestSmokeRegenerateAndVerify runs the full REGENERATE chain (every
// *-gen tool as a subprocess) against the real checkout, then VERIFY. It
// mutates live/*.json and live/SURVEY.md in place - the same files a real
// pipeline run would - so it only makes sense against a disposable
// checkout; guarded by smokeEnv for exactly that reason, on top of the
// network and multi-minute runtime.
func TestSmokeRegenerateAndVerify(t *testing.T) {
	root := smokeOrSkip(t)

	if dirty, err := gitDirty(root); err != nil {
		t.Fatalf("gitDirty: %v", err)
	} else if dirty {
		t.Fatal("refusing to run TestSmokeRegenerateAndVerify against a dirty working tree - it writes live/*.json and live/SURVEY.md in place")
	}

	regen, err := Regenerate(root, false, io.Discard)
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if regen.ProposalPath == "" {
		t.Error("Regenerate did not record a row-gen proposal path")
	}

	if err := Verify(root, io.Discard); err != nil {
		t.Fatalf("Verify after Regenerate: %v", err)
	}
}

// TestSmokePropose runs PROPOSE (issue #65) as a subprocess against the real
// checkout, the same call the pipeline's own run() makes right after
// REGENERATE. Shares TestSmokeRegenerateAndVerify's dirty-tree guard and
// network/runtime cost, so it is gated the same way.
func TestSmokePropose(t *testing.T) {
	root := smokeOrSkip(t)

	if dirty, err := gitDirty(root); err != nil {
		t.Fatalf("gitDirty: %v", err)
	} else if dirty {
		t.Fatal("refusing to run TestSmokePropose against a dirty working tree")
	}

	propose, err := Propose(root, io.Discard)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if propose.Path == "" {
		t.Error("Propose did not record a captured report path")
	}
	if propose.Summary == "" {
		t.Error("Propose did not capture a summary line")
	}
}
