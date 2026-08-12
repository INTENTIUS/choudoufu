// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package live_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The stateless mode's only integration surface is the marker specification
// in live/MARKERS.md: anything that reads the markers this mode writes
// does so from that document, and nothing in this tree may depend on such a
// reader. Naming one in a comment is a citation and is fine; importing one is
// a build dependency and is not, because it would put an unrelated project in
// this fork's go.mod for no reason the spec can justify.
//
// chant is the reader that exists today, so it is the name these tests sweep
// for.

const (
	statelessGo   = "."
	statelessSpec = "../../live"
)

// importLine matches an import path in Go source: the quoted string in an
// import declaration. Deliberately crude - this is a sweep for a name, not a
// parser, and a false positive here is a conversation rather than a bug.
var importLine = regexp.MustCompile(`^\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"\s*$`)

// TestStatelessImportsNoExternalTooling walks every Go file under
// internal/live and fails on any import of an outside reader of the
// marker spec.
func TestStatelessImportsNoExternalTooling(t *testing.T) {
	var offenders []string

	// The Go tree: imports are the coupling that matters, because they are
	// the ones the compiler enforces and the ones that would put chant in
	// this fork's go.mod.
	err := filepath.Walk(statelessGo, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // walking this package's own tree
		if readErr != nil {
			return readErr
		}
		inImports := false
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			switch {
			case trimmed == "import (":
				inImports = true
				continue
			case inImports && trimmed == ")":
				inImports = false
				continue
			}

			var importPath string
			if strings.HasPrefix(trimmed, "import ") {
				if m := importLine.FindStringSubmatch(strings.TrimPrefix(trimmed, "import ")); m != nil {
					importPath = m[1]
				}
			} else if inImports {
				if m := importLine.FindStringSubmatch(trimmed); m != nil {
					importPath = m[1]
				}
			}
			if importPath != "" && strings.Contains(strings.ToLower(importPath), "chant") {
				offenders = append(offenders, fmt.Sprintf("%s:%d: %s", path, i+1, trimmed))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", statelessGo, err)
	}

	if len(offenders) > 0 {
		t.Errorf("internal/live imports chant, a consumer of the marker spec.\n"+
			"live/MARKERS.md is the only integration surface between the two;\n"+
			"anything else this tree needs belongs in this tree or in that spec.\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestSpecDirectoryOnlyCitesExternalTooling checks the other half, where the
// dependency would be a claim rather than an import: the spec directory may
// cite an outside reader, and does, but it must not tell a reader that
// something outside this repository is required for any of this to work. The
// spec is meant to be implementable by anyone who reads it.
func TestSpecDirectoryOnlyCitesExternalTooling(t *testing.T) {
	if _, err := os.Stat(statelessSpec); os.IsNotExist(err) {
		t.Skipf("%s does not exist from this package's directory", statelessSpec)
	}

	// Phrases that would turn a citation into a dependency.
	forbidden := []string{
		"requires chant",
		"depends on chant",
		"install chant",
		"chant is required",
		"provided by chant",
	}

	err := filepath.Walk(statelessSpec, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // walking this fork's own tree
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(data))
		for _, phrase := range forbidden {
			if strings.Contains(lower, phrase) {
				t.Errorf("%s says %q; a reader of the markers is a consumer of the spec, never a prerequisite of it",
					path, phrase)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", statelessSpec, err)
	}
}
