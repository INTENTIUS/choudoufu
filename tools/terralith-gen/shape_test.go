// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// forbiddenSubstrings are choudoufu-only constructs that must never appear
// in generated output - #564's shape requirement 2, "no live block, no
// record_store, no markers, anywhere in the output". A plain substring
// scan, not an HCL parse, is deliberate here: the generator's own source
// (templates.go, gen.go) never has a reason to emit any of these words, so
// a literal match is already a strong, auditable, mechanical signal - the
// brief's own instruction ("check mechanically, not by eye") - and it
// needs no HCL library and no schema to run.
var forbiddenSubstrings = []string{
	"record_store",
	"tofu-estate",
	"tofu-address",
}

// liveBlockPattern catches a top-level "live" block
// (`terraform { live { ... } }`, internal/configs/live.go) by its opening
// line. Anchored to a line start so it cannot false-positive on the word
// "live" inside prose or an unrelated identifier.
var liveBlockPattern = regexp.MustCompile(`(?m)^\s*live\s*\{`)

// TestNoChoudoufuConstructLeaks is #564's third acceptance bullet, checked
// mechanically rather than by eye: build a terralith at a couple of
// scales, then scan every file terralith-gen wrote for the choudoufu-only
// sidecar filename, the two marker tag keys, the record_store block
// keyword, and a top-level live block. None of them may appear anywhere -
// this generator's whole point is a stranger's stock Terraform, the
// population #546's migration measurement needs to start from.
func TestNoChoudoufuConstructLeaks(t *testing.T) {
	for _, scale := range []int{1, 3} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "terralith")
			e := buildEstate(scale, "tl")
			if err := e.write(out); err != nil {
				t.Fatal(err)
			}

			entries, err := os.ReadDir(out)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) == 0 {
				t.Fatal("generated no files at all - the scan below would vacuously pass")
			}
			for _, ent := range entries {
				if ent.Name() == configs.LiveSidecarFilename {
					t.Errorf("generated output contains %s, the choudoufu live-sidecar filename - #564 forbids it", ent.Name())
				}
				data, err := os.ReadFile(filepath.Join(out, ent.Name())) //nolint:gosec // fixed test-generated path
				if err != nil {
					t.Fatal(err)
				}
				content := string(data)
				for _, s := range forbiddenSubstrings {
					if strings.Contains(content, s) {
						t.Errorf("%s contains forbidden substring %q", ent.Name(), s)
					}
				}
				if liveBlockPattern.MatchString(content) {
					t.Errorf("%s contains a top-level \"live\" block", ent.Name())
				}
			}
		})
	}
}

// TestNoChoudoufuConstructLeaksHasTeeth is the control this repo's own
// audit history (CLAUDE.md) asks every emptiness-style check to carry: a
// scan that has never been made to fail is not evidence it can. Feeding it
// content that DOES carry each forbidden construct must fail.
func TestNoChoudoufuConstructLeaksHasTeeth(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"record_store", `record_store "local" { path = ".tofu-records" }`},
		{"tofu-estate", `tags = { "tofu-estate" = "x" }`},
		{"tofu-address", `tags = { "tofu-address" = "aws_iam_role.x" }`},
		{"live-block", "terraform {\n  live {\n    estate = \"x\"\n  }\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hasForbidden := false
			for _, s := range forbiddenSubstrings {
				if strings.Contains(tc.content, s) {
					hasForbidden = true
				}
			}
			if liveBlockPattern.MatchString(tc.content) {
				hasForbidden = true
			}
			if !hasForbidden {
				t.Fatalf("test fixture %q does not actually trip any check - fix the fixture", tc.name)
			}
		})
	}
}

// TestValidateGeneratedTerralith is the syntactic half of #564's first
// acceptance bullet: `terraform validate` passes against the real pinned
// provider release, at a couple of scales. Gated behind flocitest.Gate
// (TF_ACC or TF_FLOCI_TEST) because it downloads a real provider; it does
// NOT touch floci or Docker - see live/e2e/terralith-scale/run.sh for the
// live-apply-and-destroy half of the proof, which does.
func TestValidateGeneratedTerralith(t *testing.T) {
	flocitest.Gate(t, "terralith-gen terraform validate")
	flocitest.RequireBinary(t, "terraform")

	for _, scale := range []int{1, 4} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "terralith")
			e := buildEstate(scale, "tl")
			if err := e.write(out); err != nil {
				t.Fatal(err)
			}

			run := func(args ...string) {
				t.Helper()
				cmd := exec.Command("terraform", args...) //nolint:gosec // fixed binary name, test-only
				cmd.Dir = out
				cmdOut, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("terraform %s: %v\n%s", strings.Join(args, " "), err, cmdOut)
				}
			}
			run("init", "-backend=false", "-input=false", "-no-color")
			run("validate", "-no-color")
		})
	}
}
