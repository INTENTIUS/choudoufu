// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// mapping-gen generates live/mapping.json, the TF-to-CFN type mapping
// artifact everything in #40 flows through (issue #43).
//
// It joins two committed rosters - live/survey-full.json's 1,691 Terraform
// AWS resource types (issue #41) and live/registry.json's 1,653
// CloudFormation Registry types (issue #42) - against the curated overlay
// at tools/mapping-gen/overlay.json, and writes one row per TF type:
//
//	{"tf_type": "aws_s3_bucket", "cfn_type": "AWS::S3::Bucket", "via": "name", "fold_parent": null, "note": null}
//
// via is name (the heuristic in heuristic.go derived the CFN type from the
// TF type's own name), alias (the overlay asserts the pair by hand), fold
// (the TF type is a property-child of a CFN parent - Terraform decomposes
// finer than CloudFormation here - fold_parent carries the parent's CFN
// type and cfn_type stays null), or none (no CFN counterpart; note says why
// when the overlay knows).
//
// Every input is a committed file - no provider, no network, no zip - so
// this tool needs nothing beyond the checkout:
//
//	go run ./tools/mapping-gen
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Path literals, centralized on purpose (see tools/survey-gen/main.go's
// const block for why): the artifacts this tool reads and writes, relative
// to the repository root.
const (
	// tfRosterRel is the TF-side roster: issue #41's whole-provider survey.
	tfRosterRel = "live/survey-full.json"

	// cfnRosterRel is the CFN-side roster: issue #42's registry artifact.
	cfnRosterRel = "live/registry.json"

	// curatedMDRel is the hand-curated 68-type table the pin test measures
	// against; mapping-gen itself only reads its type-name column.
	curatedMDRel = "live/SURVEY.md"

	// overlayJSONRel is the curated overlay this tool joins the two rosters
	// against: aliases the name heuristic cannot derive, folds (TF
	// sub-resources a CFN parent absorbs), and nones (TF types with no CFN
	// counterpart at all, with a reason when known).
	overlayJSONRel = "tools/mapping-gen/overlay.json"

	// mappingJSONRel is where the generated artifact is committed.
	mappingJSONRel = "live/mapping.json"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick survey-gen's and registry-gen's repoRoot use, so the tool runs
// from any directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/mapping-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mapping-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		return fmt.Errorf("reading the TF roster from %s: %w", tfRosterRel, err)
	}

	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		return fmt.Errorf("reading the CFN roster from %s: %w", cfnRosterRel, err)
	}

	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		return fmt.Errorf("reading the overlay from %s: %w", overlayJSONRel, err)
	}

	mapping, err := buildMapping(tfTypes, cfnTypes, overlay)
	if err != nil {
		return err
	}

	data, err := mapping.marshal()
	if err != nil {
		return err
	}

	out := filepath.Join(root, mappingJSONRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "mapping-gen: wrote %s (%d types: %d mapped, %d fold, %d none)\n",
		mappingJSONRel, mapping.Counts.Types, mapping.Counts.Mapped, mapping.Counts.Fold, mapping.Counts.None)
	return nil
}
