// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// terralith-gen generates a synthetic terralith: a single-state,
// stock-Terraform-shaped estate whose composition mirrors a real one -
// dominated by IAM identity resources with deliberate copy-paste
// duplication, a small ECS container-service layer carrying the
// deploy-time drift pattern, and a Route 53 DNS record fan-out. See issue
// #564 (child B of the #546 epic) for the shape requirements this
// implements, and issue #574 for the count/for_each/module-nested
// expansion added on top: most of the root module's resources are
// individually named, but a share is `count`-expanded, a share is
// `for_each`-expanded, and one bucket is nested inside a module call
// (still one state - see gen.go's buildEstate doc comment).
//
// It exists because tools/estate-gen is the wrong tool for this shape:
// estate-gen emits one resource block per admitted TYPE, for coverage
// breadth across ~1700 AWS types. A terralith is the opposite: a handful
// of types, many instances, deliberate duplication. estate-gen also always
// writes the sidecar (estate.chdf.hcl) and tofu-estate/tofu-address
// markers that make a cohort a choudoufu live estate - this generator
// must never emit any of that. Its subject is a stranger's stock
// Terraform, which is where a real adoption starts (#546's framing).
//
// Composition is a function of one -scale parameter: "the same estate at
// 4x" is `-scale 4`, not a new artifact. See gen.go's composition doc
// comment for the counts and the reasoning behind them.
//
//	go run ./tools/terralith-gen -scale 1 -out /tmp/terralith-small
//	go run ./tools/terralith-gen -scale 4 -out /tmp/terralith-4x
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	providerSource  = "hashicorp/aws"
	providerVersion = "6.59.0"

	// defaultFmtBin canonicalizes the generated HCL's formatting after
	// this tool writes it. A caller with only tofu or choudoufu on PATH
	// can point this at either; formatting is best-effort and a missing
	// binary is not fatal (see formatWithBinary).
	defaultFmtBin = "terraform"
)

func main() {
	scale := flag.Int("scale", 1, "scale factor; every count in the composition is linear in this, so the composition's proportions hold as it grows (issue #564)")
	out := flag.String("out", "", "output directory (required)")
	prefix := flag.String("prefix", "tl", "short name prefix for every generated resource, so more than one generated terralith can coexist in one account without name collisions")
	fmtBin := flag.String("fmt-bin", defaultFmtBin, "binary used to canonicalize the generated *.tf files' formatting, recursively (terraform, tofu or choudoufu); skipped when not on PATH, but a binary that runs and rejects the generated HCL fails the generation")
	flag.Parse()

	if err := run(*scale, *out, *prefix, *fmtBin); err != nil {
		fmt.Fprintf(os.Stderr, "terralith-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(scale int, out, prefix, fmtBin string) error {
	if scale < 1 {
		return fmt.Errorf("-scale must be >= 1, got %d", scale)
	}
	if out == "" {
		return fmt.Errorf("-out is required")
	}
	if prefix == "" {
		return fmt.Errorf("-prefix must not be empty")
	}

	est := buildEstate(scale, prefix)

	if err := est.write(out); err != nil {
		return err
	}

	if err := formatWithBinary(fmtBin, out); err != nil {
		return err
	}

	c := est.composition
	fmt.Fprintf(os.Stderr,
		"terralith-gen: wrote %s (scale=%d, prefix=%q): %d resources total, %d identity (%.1f%%), %d container, %d dns, %d supporting; %d/%d role+policy blocks measured duplicate (%.1f%%)\n",
		out, scale, prefix, c.totalResources(), c.identityResources, c.identityPercent(),
		c.containerResources, c.dnsResources, c.supportingResources,
		c.duplicateRolePolicyBlocks, c.totalRolePolicyBlocks, c.duplicationPercent())
	return nil
}

// formatWithBinary canonicalizes the HCL under out, recursively, with fmtBin.
//
// Two things that both used to read as "fmt failed" are deliberately not the
// same thing here (issue #578, defect 2):
//
//   - fmtBin is not on PATH. Not an error. Formatting is a convenience, and a
//     caller with none of terraform/tofu/choudoufu installed still gets a
//     correct estate; refusing to generate one would make a cosmetic pass a
//     hard dependency.
//   - fmtBin ran and exited non-zero. A generation failure, returned as one,
//     with its stderr. `terraform fmt` is a parser before it is a formatter:
//     it exits 2 with "Error: Invalid expression" and the offending file and
//     line when it cannot parse what this tool wrote. That is a syntax check
//     over the whole generated estate needing no Docker, no emulator and no
//     network - only the binary already being looked up on this line - and
//     `_ = cmd.Run()` threw it away.
//
// -recursive is load-bearing, not tidiness (issue #578, defect 1): `fmt`
// does not recurse by default, so without it the generated module
// subdirectory (modules/team_pod, added by #574) was the one part of the
// estate that was never formatted and, by the paragraph above, never parsed.
// terraform, tofu and choudoufu all accept the flag.
func formatWithBinary(fmtBin, out string) error {
	if fmtBin == "" {
		return nil
	}
	if _, err := exec.LookPath(fmtBin); err != nil {
		return nil
	}

	cmd := exec.Command(fmtBin, "fmt", "-recursive", out) //nolint:gosec // caller-provided binary name, the same trust boundary as tools/estate-gen's -fmt-bin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("%s fmt -recursive %s exited %d - the generated HCL did not survive its own formatter, which is a generation failure:\n%s",
				fmtBin, out, exit.ExitCode(), strings.TrimRight(stderr.String(), "\n"))
		}
		return fmt.Errorf("running %s fmt -recursive %s: %w", fmtBin, out, err)
	}
	return nil
}
