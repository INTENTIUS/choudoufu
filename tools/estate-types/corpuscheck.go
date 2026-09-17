// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// corpusModuleDir returns the .corpus module directory one estateSpec
// depends on, or "" if it depends on nothing under .corpus at all.
//
// Every "corpus-*" spec traces to a real directory fetched by
// "just corpus-fetch" - most of them directly through their first
// [estateSpec.ConfigDirs] entry (".corpus/alb/examples/complete-alb" ->
// ".corpus/alb"), and corpus-sumaform-aws through sumaform.go's
// prepareSumaformModules instead, since its ConfigDirs is deliberately nil
// (see that spec's own Note). Every non-"corpus-*" spec (terralith-scale,
// reference-*) generates or hand-writes its configuration and needs nothing
// fetched, so it is not a corpus estate at all.
func corpusModuleDir(spec estateSpec) string {
	if !strings.HasPrefix(spec.Name, "corpus-") {
		return ""
	}
	if spec.Name == "corpus-sumaform-aws" {
		return ".corpus/sumaform"
	}
	if len(spec.ConfigDirs) == 0 {
		return ""
	}
	const prefix = ".corpus/"
	rel := spec.ConfigDirs[0]
	if !strings.HasPrefix(rel, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(rel, prefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return prefix + rest
}

// checkCorpusPopulated refuses to scan at all when .corpus is absent, or
// present but empty, rather than let every corpus-* [scanEstate] call
// silently see a missing directory, record a Note, and return zero types.
//
// That silent path is issue #1183: with no .corpus, the generator used to
// exit 0 having collapsed the board from 177 distinct types to 37, because
// nothing forced the many-missing-directories case to be louder than a
// single missing one. This runs once, before any estate is scanned, and
// names the fix ("just corpus-fetch") rather than leaving a contributor to
// diagnose 25 identical "no such file or directory" Notes by hand.
//
// A *partially* populated .corpus (some modules fetched, others not) is
// deliberately not caught here - that failure is per-estate and already
// visible per-estate in Notes; TestCorpusEstatesUseAllConfigDirs in
// main_test.go is the guard for it in the committed artifact.
func checkCorpusPopulated(root string) error {
	expected := map[string]bool{}
	for _, spec := range estateSpecs {
		if dir := corpusModuleDir(spec); dir != "" {
			expected[dir] = true
		}
	}
	if len(expected) == 0 {
		return nil
	}

	populated := 0
	for dir := range expected {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err == nil && info.IsDir() {
			populated++
		}
	}

	if populated == 0 {
		return fmt.Errorf(
			"expected %d corpus estate(s) under .corpus, found 0 populated; run \"just corpus-fetch\" before \"go run ./tools/estate-types\"",
			len(expected),
		)
	}
	return nil
}
