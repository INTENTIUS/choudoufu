// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestPlanInstancesInHonoursTheTargetScope is GitHub issue #1258's first
// leg at this package's own level: a block the scope drops is neither
// planned nor present in the result, a block it keeps is both, and a nil
// scope is [PlanInstances] exactly.
//
// The count is the assertion that matters. Absence from the result could
// be a provider declining; zero PlanResourceChange calls for the excluded
// block is the pass never asking.
func TestPlanInstancesInHonoursTheTargetScope(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-computed")

	keep := func(names ...string) identity.Scope {
		in := map[string]bool{}
		for _, n := range names {
			in[n] = true
		}
		return func(addr addrs.ConfigResource) bool { return in[addr.String()] }
	}

	for _, tc := range []struct {
		name  string
		scope identity.Scope
		want  []string
		calls int
	}{
		// stub_cert.dynamic_count is never planned: its count reads a
		// managed attribute. So the whole configuration is three calls.
		{name: "nil scope plans every block", scope: nil,
			want: []string{"stub_cert.cert", "stub_cert.repeated[0]", "stub_cert.repeated[1]"}, calls: 3},
		{name: "a scope keeping the counted block", scope: keep("stub_cert.repeated"),
			want: []string{"stub_cert.repeated[0]", "stub_cert.repeated[1]"}, calls: 2},
		{name: "a scope keeping the bare block", scope: keep("stub_cert.cert"),
			want: []string{"stub_cert.cert"}, calls: 1},
		{name: "a scope keeping nothing plannable", scope: keep("stub_cert.dynamic_count"),
			want: nil, calls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := derivingStub()
			got, diags := PlanInstancesIn(context.Background(), cfg, anyProvider(stub), tc.scope)
			if diags.HasErrors() {
				t.Fatalf("PlanInstancesIn: %s", diags.Err())
			}
			keys := keysOf(got)
			sort.Strings(keys)
			if len(keys) != len(tc.want) {
				t.Fatalf("planned %v, want %v", keys, tc.want)
			}
			for i := range tc.want {
				if keys[i] != tc.want[i] {
					t.Errorf("planned %v, want %v", keys, tc.want)
					break
				}
			}
			if stub.calls != tc.calls {
				t.Errorf("PlanResourceChange was called %d time(s), want %d", stub.calls, tc.calls)
			}
		})
	}
}
