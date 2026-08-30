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
// scales, then scan every file terralith-gen wrote - recursively, since
// issue #574 added modules/team_pod/*.tf beneath the output root - for the
// choudoufu-only sidecar filename, the two marker tag keys, the
// record_store block keyword, and a top-level live block. None of them may
// appear anywhere - this generator's whole point is a stranger's stock
// Terraform, the population #546's migration measurement needs to start
// from.
func TestNoChoudoufuConstructLeaks(t *testing.T) {
	for _, scale := range []int{1, 3} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "terralith")
			e := buildEstate(scale, "tl")
			if err := e.write(out); err != nil {
				t.Fatal(err)
			}

			var files []string
			if err := filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() {
					files = append(files, path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(files) == 0 {
				t.Fatal("generated no files at all - the scan below would vacuously pass")
			}
			for _, path := range files {
				name := filepath.Base(path)
				if name == configs.LiveSidecarFilename {
					t.Errorf("generated output contains %s, the choudoufu live-sidecar filename - #564 forbids it", name)
				}
				data, err := os.ReadFile(path) //nolint:gosec // fixed test-generated path
				if err != nil {
					t.Fatal(err)
				}
				content := string(data)
				rel, _ := filepath.Rel(out, path)
				for _, s := range forbiddenSubstrings {
					if strings.Contains(content, s) {
						t.Errorf("%s contains forbidden substring %q", rel, s)
					}
				}
				if liveBlockPattern.MatchString(content) {
					t.Errorf("%s contains a top-level \"live\" block", rel)
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

// countForEachModulePattern matches a `count =` or `for_each =` (as an
// argument, not an identifier substring - regex.go anchors so
// "for_each_ish" or "discount = " cannot false-positive) or a `module "..."`
// block header, anchored to a line start the same way liveBlockPattern is.
// This mirrors #574's own diagnostic method verbatim - the issue's report
// found the defect with `grep -rn 'for_each\|count =' tools/terralith-gen/*.go`
// and found nothing; this is that same grep run the other direction, against
// the generator's OUTPUT rather than its source, so a future regression that
// silently stops emitting one of the three shapes is caught mechanically
// rather than only by re-reading a generated estate by eye.
var (
	countArgPattern    = regexp.MustCompile(`(?m)^\s*count\s*=`)
	forEachArgPattern  = regexp.MustCompile(`(?m)^\s*for_each\s*=`)
	moduleBlockPattern = regexp.MustCompile(`(?m)^\s*module\s+"`)
)

// TestExpansionIsPresent is issue #574's own acceptance bullet, checked
// mechanically: "generated output contains count and for_each at both root
// and module-nested level." Before #574, none of these three patterns
// matched anywhere in generated output - see TestNoChoudoufuConstructLeaks's
// sibling check above for the equivalent "must never appear" direction; this
// is "must appear, and at both root and module-nested level."
func TestExpansionIsPresent(t *testing.T) {
	for _, scale := range []int{1, 4} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "terralith")
			e := buildEstate(scale, "tl")
			if err := e.write(out); err != nil {
				t.Fatal(err)
			}

			rootFiles := []string{"iam.tf", "dns.tf", "pods.tf"}
			var rootContent strings.Builder
			for _, f := range rootFiles {
				data, err := os.ReadFile(filepath.Join(out, f)) //nolint:gosec // fixed test-generated path
				if err != nil {
					t.Fatal(err)
				}
				rootContent.Write(data)
			}
			root := rootContent.String()
			if !countArgPattern.MatchString(root) {
				t.Error("no root-level `count =` argument found anywhere in iam.tf/dns.tf/pods.tf - the identity layer's count-expanded share is missing")
			}
			if !forEachArgPattern.MatchString(root) {
				t.Error("no root-level `for_each =` argument found anywhere in iam.tf/dns.tf/pods.tf - the DNS for_each share is missing")
			}
			if !moduleBlockPattern.MatchString(root) {
				t.Error(`no root-level module "..." block found in pods.tf - the module-nested share is missing`)
			}

			modData, err := os.ReadFile(filepath.Join(out, "modules", "team_pod", "main.tf")) //nolint:gosec // fixed test-generated path
			if err != nil {
				t.Fatalf("modules/team_pod/main.tf: %v", err)
			}
			if !countArgPattern.MatchString(string(modData)) {
				t.Error("no `count =` argument found inside modules/team_pod/main.tf - #574's \"module-nested count instance\", the hardest shape, is missing")
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
