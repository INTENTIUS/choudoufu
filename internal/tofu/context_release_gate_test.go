// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"testing"

	"github.com/intentius/choudoufu/version"
)

// releaseGateVariables are every variable that can switch on a part of
// upstream's new runtime: TOFU_X_EXPERIMENTAL_RUNTIME for plan and apply in
// this package, TOFU_X_EXPERIMENTAL_RUNTIME_NEXT for the command layer
// (`#4823`). A release build must ignore both, whichever is set.
var releaseGateVariables = []string{"TOFU_X_EXPERIMENTAL_RUNTIME", "TOFU_X_EXPERIMENTAL_RUNTIME_NEXT"}

// TestExperimentalRuntimeClosedInReleaseBuild is #2336's release gate, red
// first. Releases between upstream tags are built on upstream main, whose
// VERSION reads 1.14.0-dev, so a release build has Prerelease "dev" and
// version.IsDev() is true. Gating on the version string would honour the
// opt-in in every release. With the release flag forced on through the
// test hook, experimentalRuntimeEnabled must be false and plan and apply
// take the old runtime, whichever variable is set.
//
// The development-build control shows the test can fail: there,
// TOFU_X_EXPERIMENTAL_RUNTIME does switch the runtime on.
func TestExperimentalRuntimeClosedInReleaseBuild(t *testing.T) {
	if version.Prerelease != "dev" {
		t.Fatalf("this test is about a VERSION with a -dev prerelease (1.14.0-dev); Prerelease is %q, so it would prove nothing", version.Prerelease)
	}
	if experimentalRuntimeForcedForTesting {
		t.Fatal("the new-runtime test hook is on, and it bypasses every gate")
	}

	t.Run("development build control", func(t *testing.T) {
		for _, v := range releaseGateVariables {
			t.Setenv(v, "")
		}
		t.Setenv("TOFU_X_EXPERIMENTAL_RUNTIME", "1")
		if !experimentalRuntimeEnabled() {
			t.Fatal("a development build with TOFU_X_EXPERIMENTAL_RUNTIME set does not take the new runtime, so the release rows below cannot fail")
		}
	})

	for _, set := range releaseGateVariables {
		t.Run("release build/"+set, func(t *testing.T) {
			for _, v := range releaseGateVariables {
				t.Setenv(v, "")
			}
			t.Setenv(set, "1")
			t.Cleanup(version.SetReleaseBuildForTesting(true))
			if !version.IsDev() {
				t.Fatal("the release hook changed IsDev; this test needs a release build that still reads as -dev")
			}
			if experimentalRuntimeEnabled() {
				t.Errorf("a release build on VERSION %s with %s set takes the new runtime; it must take the old one", version.String(), set)
			}
		})
	}
}
