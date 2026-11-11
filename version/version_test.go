// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package version

import (
	"regexp"
	"strings"
	"testing"
)

// Smoke test to validate that the version file can be read correctly and all exported
// variables include the expected information.
func TestVersion(t *testing.T) {
	if match, _ := regexp.MatchString("[^\\d+\\.]", Version); match != false {
		t.Fatalf("Version should contain only the main version")
	}

	if match, _ := regexp.MatchString("[^a-z\\d]", Prerelease); match != false {
		t.Fatalf("Prerelease should contain only letters and numbers")
	}

	if SemVer.Prerelease() != "" {
		t.Fatalf("SemVer should not include prerelease information")
	}

	if !strings.Contains(String(), Prerelease) {
		t.Fatalf("Full version string should include prerelease information")
	}
}

// TestIsReleaseBuild: a test binary is built without the release linker
// flag, so it is not a release build, and the test hook switches the answer
// both ways and restores it. Nothing else moves: Prerelease is what init
// computed from VERSION and the dev flag.
func TestIsReleaseBuild(t *testing.T) {
	if IsReleaseBuild() {
		t.Fatalf("a test binary reads as a release build (dev = %q)", dev)
	}
	prerelease := Prerelease

	restore := SetReleaseBuildForTesting(true)
	if !IsReleaseBuild() {
		t.Error("SetReleaseBuildForTesting(true) did not make IsReleaseBuild true")
	}
	if Prerelease != prerelease {
		t.Errorf("the hook changed Prerelease from %q to %q", prerelease, Prerelease)
	}
	inner := SetReleaseBuildForTesting(false)
	if IsReleaseBuild() {
		t.Error("SetReleaseBuildForTesting(false) did not make IsReleaseBuild false")
	}
	inner()
	if !IsReleaseBuild() {
		t.Error("restoring the inner hook did not bring back the outer setting")
	}
	restore()
	if IsReleaseBuild() {
		t.Error("restore did not return IsReleaseBuild to the build's own answer")
	}
}
