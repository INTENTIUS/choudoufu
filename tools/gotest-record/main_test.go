// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestALogShowsAPassOnlyWhenTheTestRanAndPassed: a gated test that skipped
// prints ok for its package all the same, which is why a maintainer log is
// read for the test's own --- PASS line and never for the package's.
func TestALogShowsAPassOnlyWhenTheTestRanAndPassed(t *testing.T) {
	p := proof{Test: "TestKubernetesStoreConformance/PutIfAbsent", Package: "./pkg/staterecord", Gate: "CHOUDOUFU_K8S_RECORD_KUBECONFIG"}
	const ok = "ok  \tgithub.com/intentius/choudoufu/pkg/staterecord\t12.0s\n"
	for _, tc := range []struct {
		name, log string
		pass      bool
	}{
		{"passed", "=== RUN   TestKubernetesStoreConformance\n    --- PASS: TestKubernetesStoreConformance/PutIfAbsent (0.10s)\n--- PASS: TestKubernetesStoreConformance (1.00s)\n" + ok, true},
		{"skipped", "--- SKIP: TestKubernetesStoreConformance (0.00s)\n" + ok, false},
		{"subtest failed", "    --- FAIL: TestKubernetesStoreConformance/PutIfAbsent (0.10s)\n--- PASS: TestKubernetesStoreConformance (1.00s)\n" + ok, false},
		{"another subtest passed", "    --- PASS: TestKubernetesStoreConformance/List (0.10s)\n--- PASS: TestKubernetesStoreConformance (1.00s)\n" + ok, false},
		{"package not ok", "    --- PASS: TestKubernetesStoreConformance/PutIfAbsent (0.10s)\n--- PASS: TestKubernetesStoreConformance (1.00s)\nFAIL\n", false},
		{"a longer name's pass", "--- PASS: TestKubernetesStoreConformanceExtra (1.00s)\n" + ok, false},
	} {
		err := testPassed(tc.log, p)
		if tc.pass && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.pass && err == nil {
			t.Errorf("%s: read as a pass", tc.name)
		}
	}
	if packageOK("ok  \tgithub.com/intentius/choudoufu/pkg/staterecordx\t1s\n", "./pkg/staterecord") {
		t.Error("a sibling package's ok line read as this package's")
	}
	if !packageOK("fast\tFast test tier\t2026-10-27T06:30:13.5Z ok  \tgithub.com/intentius/choudoufu/pkg/staterecord\t1.802s\n", "./pkg/staterecord") {
		t.Error("a GitHub Actions log line did not read as the package's ok")
	}
}
