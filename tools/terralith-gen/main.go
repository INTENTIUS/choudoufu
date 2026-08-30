// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// terralith-gen generates a synthetic terralith: a single-root-module,
// single-state, stock-Terraform-shaped estate whose composition mirrors a
// real one - dominated by IAM identity resources with deliberate
// copy-paste duplication, a small ECS container-service layer carrying the
// deploy-time drift pattern, and a Route 53 DNS record fan-out. See issue
// #564 (child B of the #546 epic) for the shape requirements this
// implements.
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
	"flag"
	"fmt"
	"os"
	"os/exec"
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
	fmtBin := flag.String("fmt-bin", defaultFmtBin, "binary used to canonicalize the generated *.tf files' formatting (terraform, tofu or choudoufu); best-effort, skipped if not found")
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

	if fmtBin != "" {
		if _, err := exec.LookPath(fmtBin); err == nil {
			cmd := exec.Command(fmtBin, "fmt", out) //nolint:gosec // caller-provided binary name, the same trust boundary as tools/estate-gen's -fmt-bin
			_ = cmd.Run()                           // best-effort: formatting failure is not a generation failure
		}
	}

	c := est.composition
	fmt.Fprintf(os.Stderr,
		"terralith-gen: wrote %s (scale=%d, prefix=%q): %d resources total, %d identity (%.1f%%), %d container, %d dns, %d supporting; %d/%d role+policy blocks measured duplicate (%.1f%%)\n",
		out, scale, prefix, c.totalResources(), c.identityResources, c.identityPercent(),
		c.containerResources, c.dnsResources, c.supportingResources,
		c.duplicateRolePolicyBlocks, c.totalRolePolicyBlocks, c.duplicationPercent())
	return nil
}
