// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// identityGoldenRoots mirrors internal/live/check's own identityGoldenRoots
// (identitygolden_test.go): the two trees TestIdentityGolden sweeps. Kept in
// sync by hand rather than imported, the same reason importGrammarRow and
// the rest of this package's mirror types are re-declared instead of
// imported - this file only ever reads fixture source text, never any of
// check's resolution logic.
var identityGoldenRoots = []string{"internal/live", "live"}

// resourceDeclRe matches one HCL resource block's own type token:
// `resource "aws_foo" "name" {`. The type is capture group 1.
var resourceDeclRe = regexp.MustCompile(`(?m)^\s*resource\s+"([A-Za-z0-9_]+)"\s+"`)

// goldenExercisedTypes is schemafirst.go's own safety net: every
// provider-local resource type this repository's own fixtures declare,
// under either tree internal/live/check's identity golden sweeps AND
// internal/live/flocitest.FixtureDirs' estate/cohort trees (a subtree of
// "live") - the two independent, committed populations that hold a
// dropped table row to account, both analyzed with NO provider schemas.
//
// It used to parse internal/live/check/testdata/identity-golden.txt's own
// rendered addresses instead of scanning source, which is narrower than it
// looks: that file lists only the instances that currently RESOLVE, so a
// type declared in a fixture but already failing to resolve for some other,
// unrelated reason left no address for that parse to find - exactly the gap
// that let aws_api_gateway_integration_response and
// aws_api_gateway_method_response through once, tripping
// internal/live/identity's TestTableCoversFixtureTypes and
// internal/live/lint's TestAdmissionTableCoversEstate, both of which scan
// resource declarations directly rather than resolved output. Scanning
// source is the union of what every consumer of this table could ever need:
// a type with no declaration anywhere in these trees cannot be exercised by
// any of them, resolved or not.
//
// Dropping a row from the emitted identity table makes
// [identity.SynthesizeTypeIdentity] the type's only remaining resolution
// path, and that function refuses outright the moment it is handed no
// schemas (synthesize.go's own noSchemasRefusal). Every schema-less
// consumer of this table - the golden, the two coverage tests above,
// admission itself - shares that same fallback, so a candidate declared
// anywhere here is held back the same way regardless of which consumer
// would have noticed first.
func goldenExercisedTypes(root string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, rel := range identityGoldenRoots {
		base := filepath.Join(root, rel)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
			}
			if d.IsDir() {
				if d.Name() == ".terraform" {
					return fs.SkipDir
				}
				return nil
			}
			if !hasHCLExt(path) {
				return nil
			}
			data, err := os.ReadFile(path) //nolint:gosec // walking a fixed tree inside the checkout
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			for _, m := range resourceDeclRe.FindAllSubmatch(data, -1) {
				out[string(m[1])] = true
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", base, err)
		}
	}
	return out, nil
}

// hasHCLExt is the same file filter identitygolden_test.go's own sweep uses,
// re-declared here for the reason this file's own doc comment gives - a .tf
// or .tofu file, JSON forms included. (No fixture in either tree uses .tofu
// or .tf.json today, but the loader accepts both, and a filter narrower than
// what the loader reads is exactly the trap live-markers.md warns against.)
func hasHCLExt(path string) bool {
	return strings.HasSuffix(path, ".tf") ||
		strings.HasSuffix(path, ".tf.json") ||
		strings.HasSuffix(path, ".tofu") ||
		strings.HasSuffix(path, ".tofu.json")
}
