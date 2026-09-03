// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import "testing"

// TestReadsPolicyFor pins issue #732's toggle resolution: the environment
// override wins when it names a real policy, the block's argument stands
// otherwise, and the default is "selective" - the maintainer's ruling that
// live-backend behaviors default on, with "full" as the off switch. The
// garbage-env case is the red-proof: an override that could silently turn
// the pass off (or on) with a typo would be a policy nobody chose.
func TestReadsPolicyFor(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		configured string
		want       string
	}{
		{"default is selective", "", "", "selective"},
		{"block sets full", "", "full", "full"},
		{"block sets selective explicitly", "", "selective", "selective"},
		{"env full overrides block selective", "full", "selective", "full"},
		{"env selective overrides block full", "selective", "full", "selective"},
		{"garbage env falls back to block", "sometimes", "full", "full"},
		{"garbage env falls back to default", "sometimes", "", "selective"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvReads, tc.env)
			if got := readsPolicyFor(tc.configured); got != tc.want {
				t.Errorf("readsPolicyFor(%q) with %s=%q = %q, want %q", tc.configured, EnvReads, tc.env, got, tc.want)
			}
		})
	}
}
