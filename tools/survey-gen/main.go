// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// survey-gen generates stateless/survey.json, the machine-derived companion
// to live/SURVEY.md's hand-written per-type table (issue #25,
// increments 1 and 2).
//
// It boots the pinned AWS provider release the same way the gated test tier
// does - a temp working directory requiring the provider, `terraform init`
// to download it, go-plugin to launch the binary - and reads one
// GetProviderSchema response. That response carries all three raw signals
// the survey records per type (a top-level tags argument, a native list
// resource, a resource identity schema) plus the identity attribute
// composition, and the classifier in classify.go turns those into an
// admission path per the rules in SURVEY.md's Method section.
//
// The roster itself stays human: the 68 types are read out of SURVEY.md's
// own table, because which types make up "the top set" is curation, not
// schema. Everything else in survey.json is derived.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/survey-gen
//
// It needs network for the provider download (or a warm
// TF_PLUGIN_CACHE_DIR) and a terraform binary on PATH (-init-bin overrides).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Path literals and pins, centralized on purpose: a later approved phase
// renames stateless/ to live/ and the module path to
// github.com/intentius/choudoufu, and this block is the one place in this
// tool that rename touches.
const (
	// surveyJSONRel is where the generated artifact is committed, relative
	// to the repository root.
	surveyJSONRel = "stateless/survey.json"

	// surveyMDRel is the hand-written survey whose per-type table names the
	// roster this tool derives signals and paths for.
	surveyMDRel = "live/SURVEY.md"

	// providerSource and providerVersion pin the provider release surveyed,
	// the same release the estate fixture pins
	// (live/e2e/estate/versions.tf).
	providerSource  = "hashicorp/aws"
	providerVersion = "6.58.0"

	// defaultInitBin downloads the provider. Stock terraform, the same
	// binary the gated test tier drives; -init-bin swaps it for choudoufu
	// or tofu, which resolve the same release via registry.opentofu.org.
	defaultInitBin = "terraform"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick flocitest.RepoRoot uses, so the tool runs from any directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/survey-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	initBin := flag.String("init-bin", defaultInitBin,
		"binary that downloads the pinned provider (terraform, tofu or choudoufu)")
	flag.Parse()

	if err := run(*initBin); err != nil {
		fmt.Fprintf(os.Stderr, "survey-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(initBin string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	roster, err := readRoster(filepath.Join(root, surveyMDRel))
	if err != nil {
		return fmt.Errorf("reading the roster from %s: %w", surveyMDRel, err)
	}
	fmt.Fprintf(os.Stderr, "survey-gen: %d types in %s's table\n", len(roster), surveyMDRel)

	workdir, err := os.MkdirTemp("", "survey-gen-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	schemas, err := acquireSchemas(initBin, workdir, os.Stderr)
	if err != nil {
		return err
	}

	survey := buildSurvey(schemas, rosterTypes(roster))
	data, err := survey.marshal()
	if err != nil {
		return err
	}

	out := filepath.Join(root, surveyJSONRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "survey-gen: wrote %s (%d types: %d taggable, %d with list resources, %d with identity schemas)\n",
		surveyJSONRel, len(survey.Types), survey.Counts.Taggable, survey.Counts.ListResource, survey.Counts.IdentitySchema)
	return nil
}
