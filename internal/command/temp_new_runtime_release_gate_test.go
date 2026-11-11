// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"

	"github.com/intentius/choudoufu/version"
)

// releaseGateVariables are every variable that can switch on a part of
// upstream's new runtime: TOFU_X_EXPERIMENTAL_RUNTIME for plan and apply in
// internal/tofu, TOFU_X_EXPERIMENTAL_RUNTIME_NEXT for this layer (`#4823`).
var releaseGateVariables = []string{"TOFU_X_EXPERIMENTAL_RUNTIME", "TOFU_X_EXPERIMENTAL_RUNTIME_NEXT"}

// TestNewRuntimeEnabledClosedInReleaseBuild is the command-layer half of
// #2336's release gate, red first; internal/tofu's
// TestExperimentalRuntimeClosedInReleaseBuild is the other half. On VERSION
// 1.14.0-dev a release build has Prerelease "dev", so a gate on the version
// string would honour the opt-in in every release. With the release flag
// forced on through the test hook, Meta.NewRuntimeEnabled must be false
// whichever variable is set. The development-build control shows the test
// can fail: there, TOFU_X_EXPERIMENTAL_RUNTIME_NEXT does switch it on.
func TestNewRuntimeEnabledClosedInReleaseBuild(t *testing.T) {
	if version.Prerelease != "dev" {
		t.Fatalf("this test is about a VERSION with a -dev prerelease (1.14.0-dev); Prerelease is %q, so it would prove nothing", version.Prerelease)
	}
	m := &Meta{}

	t.Run("development build control", func(t *testing.T) {
		for _, v := range releaseGateVariables {
			t.Setenv(v, "")
		}
		t.Setenv("TOFU_X_EXPERIMENTAL_RUNTIME_NEXT", "1")
		if !m.NewRuntimeEnabled() {
			t.Fatal("a development build with TOFU_X_EXPERIMENTAL_RUNTIME_NEXT set does not enable the new command layer, so the release rows below cannot fail")
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
			if m.NewRuntimeEnabled() {
				t.Errorf("a release build on VERSION %s with %s set enables the new command layer; it must not", version.String(), set)
			}
		})
	}
}
