// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import "testing"

// forceExperimentalRuntimeForTesting turns the new runtime on for the rest of
// the test with no environment variable set, and restores the previous value
// when the test ends. Tests using it must not run in parallel.
func forceExperimentalRuntimeForTesting(t *testing.T) {
	t.Helper()
	prev := experimentalRuntimeForcedForTesting
	experimentalRuntimeForcedForTesting = true
	t.Cleanup(func() { experimentalRuntimeForcedForTesting = prev })
}
