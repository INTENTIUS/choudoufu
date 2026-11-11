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

// TestExperimentalRuntimeToggleInReleaseBuild started as #2336's release
// gate: a release build on a 1.14.0-dev VERSION reads as IsDev, so the
// opt-ins are gated on the release linker flag instead. #2256 U2 (#2337)
// then opened TOFU_X_EXPERIMENTAL_RUNTIME in every build on purpose: it is
// the toggle. So a release build takes the new runtime for plan and apply
// when that variable is set, and not when only TOFU_X_EXPERIMENTAL_RUNTIME_NEXT
// is (that one is the command layer's and stays closed in releases; see
// internal/command's TestNewRuntimeEnabledClosedInReleaseBuild).
func TestExperimentalRuntimeToggleInReleaseBuild(t *testing.T) {
	if version.Prerelease != "dev" {
		t.Fatalf("this test is about a VERSION with a -dev prerelease (1.14.0-dev); Prerelease is %q, so it would prove nothing", version.Prerelease)
	}
	if experimentalRuntimeForcedForTesting {
		t.Fatal("the new-runtime test hook is on, and it bypasses every gate")
	}

	for set, want := range map[string]bool{
		"TOFU_X_EXPERIMENTAL_RUNTIME":      true,
		"TOFU_X_EXPERIMENTAL_RUNTIME_NEXT": false,
	} {
		t.Run("release build/"+set, func(t *testing.T) {
			for _, v := range releaseGateVariables {
				t.Setenv(v, "")
			}
			t.Setenv(set, "1")
			t.Cleanup(version.SetReleaseBuildForTesting(true))
			if got := experimentalRuntimeEnabled(); got != want {
				t.Errorf("a release build on VERSION %s with %s set: experimentalRuntimeEnabled() = %v, want %v", version.String(), set, got, want)
			}
		})
	}
	t.Run("release build/nothing set", func(t *testing.T) {
		for _, v := range releaseGateVariables {
			t.Setenv(v, "")
		}
		t.Cleanup(version.SetReleaseBuildForTesting(true))
		if experimentalRuntimeEnabled() {
			t.Error("a release build with no variable set takes the new runtime")
		}
	})
}
