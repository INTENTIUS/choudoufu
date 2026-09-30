// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// directControllerHeld is a call to the AWS tag reader by its package name.
var directControllerHeld = regexp.MustCompile(`\bmarkers\.ControllerHeld\(`)

// TestNoReaderBypassesTheFamilyControllerHeld (GitHub issue #1711): every
// non-test reader of "is this live object controller-held" asks
// [ControllerHeld], so a third family's controller is seen everywhere the
// first two are. The one place markers.ControllerHeld may be called is
// the AWS family's own answer (aws.go); the markers package defines it.
//
// A text scan, so it sees the call spelled with the package name, which
// is how every reader in the repository imports markers. It asserts it
// found aws.go's call, so a scan that walked nothing cannot pass.
func TestNoReaderBypassesTheFamilyControllerHeld(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]bool{
		filepath.Join("internal", "live", "substrate", "aws.go"): true,
	}
	markersDir := filepath.Join("internal", "live", "markers") + string(filepath.Separator)

	sawAllowed := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, markersDir) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !directControllerHeld.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if allowed[rel] {
				sawAllowed = true
				continue
			}
			t.Errorf("%s:%d calls markers.ControllerHeld directly; ask substrate.ControllerHeld(substrate.HoldEvidence{Tags: ...}) so every family's controller is seen:\n\t%s", rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawAllowed {
		t.Error("the scan did not find aws.go's markers.ControllerHeld call; it is not seeing the tree")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}
