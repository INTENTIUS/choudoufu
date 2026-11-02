// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import "testing"

// runConformance is staterecordtest.RunConformance, the [Store] contract
// suite. The suite lives in its own package so that another module can
// hold its stores to it (GitHub issue #2281), and a test in this package
// cannot import that package, which imports this one. So
// conformance_hook_test.go, an external test compiled into the same test
// binary, sets it here before any test runs.
var runConformance func(t *testing.T, newStore func(t *testing.T) Store)

// SetConformanceSuiteForTest is how conformance_hook_test.go hands the
// suite over.
func SetConformanceSuiteForTest(suite func(t *testing.T, newStore func(t *testing.T) Store)) {
	runConformance = suite
}

// equalStrings compares two string slices for equality including order,
// treating nil and empty as equal. The suite keeps its own copy.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
