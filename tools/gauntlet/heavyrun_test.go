// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// fakeEnv drives localHeavyRunAllowed/refuseLocalHeavyRun against a fixed
// map rather than the real process environment, so this test never reads or
// mutates os.Environ() - it must pass identically whether or not it happens
// to run inside actual CI.
func fakeEnv(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestLocalHeavyRunAllowed(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{
			name: "CI env present (GITHUB_ACTIONS=true)",
			env:  map[string]string{"GITHUB_ACTIONS": "true"},
			want: true,
		},
		{
			name: "CI env present (generic CI=true)",
			env:  map[string]string{"CI": "true"},
			want: true,
		},
		{
			name: "maintainer env var present, no CI",
			env:  map[string]string{"CHOUDOUFU_LOCAL_HEAVY_RUN": "maintainer"},
			want: true,
		},
		{
			name: "neither present",
			env:  map[string]string{},
			want: false,
		},
		{
			name: "env var present but wrong value",
			env:  map[string]string{"CHOUDOUFU_LOCAL_HEAVY_RUN": "yes"},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := localHeavyRunAllowed(fakeEnv(c.env))
			if got != c.want {
				t.Errorf("localHeavyRunAllowed(%v) = %v, want %v", c.env, got, c.want)
			}
		})
	}
}

func TestRefuseLocalHeavyRunNamesTheWorkflowAndDispatchLine(t *testing.T) {
	err := refuseLocalHeavyRun(fakeEnv(map[string]string{}), "live-cert.yml", "gh workflow run live-cert.yml -f estate=reference-ec2-vpc")
	if err == nil {
		t.Fatal("refuseLocalHeavyRun with neither CI nor the maintainer env var set: got nil error, want a refusal")
	}
	msg := err.Error()
	for _, want := range []string{"live-cert.yml", "gh workflow run live-cert.yml -f estate=reference-ec2-vpc", "CHOUDOUFU_LOCAL_HEAVY_RUN"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message missing %q: %q", want, msg)
		}
	}

	if err := refuseLocalHeavyRun(fakeEnv(map[string]string{"GITHUB_ACTIONS": "true"}), "live-cert.yml", "irrelevant"); err != nil {
		t.Errorf("refuseLocalHeavyRun inside CI: got %v, want nil", err)
	}
	if err := refuseLocalHeavyRun(fakeEnv(map[string]string{"CHOUDOUFU_LOCAL_HEAVY_RUN": "maintainer"}), "live-cert.yml", "irrelevant"); err != nil {
		t.Errorf("refuseLocalHeavyRun with the maintainer env var: got %v, want nil", err)
	}
}
