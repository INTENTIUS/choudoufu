// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// registry-gen generates live/registry.json, the machine-derived roster of
// per-type signals from the CloudFormation Registry schema bundle (issue
// #42, the registry side of #40).
//
// The bundle is a single "latest" artifact with no version in its path,
// republished constantly by AWS, so there is nothing to pin by version. It
// is downloaded and cached the way survey-gen caches the pinned provider
// plugin - once acquired, a repeat run reads the local copy rather than
// hitting the network again - and pinned by content instead: a digest over
// the *extracted schemas*, sorted typeName -> sha256(schema) and hashed in
// order, ported from chant's lexicons/aws/src/spec/pin.ts (#1390, #1473).
// AWS repackaging the zip changes its bytes without changing a schema, so
// the pin never hashes the archive itself.
//
// Enforcement is softened the way chant #1473 learned to: a change to the
// *resource set* (a type added or removed) refuses generation outright,
// because that always changes what the artifact promises; a change confined
// to schema bytes, with the type set unchanged, only warns, because AWS
// ships several distinct digests a day with an unchanged type count and a
// hard pin on that is noise pretending to be signal.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/registry-gen
//
// It needs network for the first download (or a warm cache). -zip points it
// at an existing copy of CloudformationSchema.zip instead, seeding the
// cache from it - useful offline and in tests that gate on a pre-populated
// cache rather than ever downloading.
//
// To accept an upstream move (see the refusal's own instructions for the
// exact edit), set CHOUDOUFU_ACCEPT_REGISTRY_SPEC=1 and rerun; the tool
// prints the pin block to paste into pinned_spec.go and pinned-types.json.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// registryJSONRel is where the generated artifact is committed, relative to
// the repository root.
const registryJSONRel = "live/registry.json"

// repoRoot resolves the checkout's root from this file's own location, the
// same trick survey-gen's repoRoot uses, so the tool runs from any
// directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/registry-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	zipOverride := flag.String("zip", "",
		"use this CloudFormation Registry schema zip instead of the cache/download; also seeds the cache with it")
	flag.Parse()

	if err := run(*zipOverride); err != nil {
		fmt.Fprintf(os.Stderr, "registry-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(zipOverride string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	cachePath, err := defaultZipPath()
	if err != nil {
		return fmt.Errorf("resolving the registry zip cache path: %w", err)
	}

	zipData, err := acquireZip(context.Background(), cachePath, zipOverride, os.Stderr)
	if err != nil {
		return err
	}

	schemas, err := extractSchemas(zipData)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "registry-gen: extracted %d schemas\n", len(schemas))

	accept := os.Getenv(AcceptEnv) != ""
	if err := AssertPinned(schemas, PinnedTypeNames, PinnedSpec, accept, func(m string) {
		fmt.Fprintln(os.Stderr, m)
	}); err != nil {
		return err
	}

	art, err := buildRegistry(PinnedSpec, schemas)
	if err != nil {
		return fmt.Errorf("parsing the registry schemas: %w", err)
	}

	data, err := art.marshal()
	if err != nil {
		return err
	}

	out := filepath.Join(root, registryJSONRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr,
		"registry-gen: wrote %s (%d types: %d taggable, %d list-free, %d list-with-required-input, %d no-list-handler [%d with no handlers at all], %d with read)\n",
		registryJSONRel, art.Counts.Types, art.Counts.Taggable, art.Counts.ListFree, art.Counts.ListWithRequiredInput,
		art.Counts.NoListHandler, art.Counts.NoHandlersAtAll, art.Counts.WithRead)
	return nil
}
