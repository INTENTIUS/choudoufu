// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package absent

import "testing"

func TestMatches(t *testing.T) {
	for name, tc := range map[string]struct {
		summary, detail string
		want            bool
	}{
		"sdk finder":            {"couldn't find resource", "", true},
		"aws api code":          {"error reading thing", "ResourceNotFoundException: no such policy", true},
		"kubernetes":            {"Unable to fetch service account \"smoke-k8s/app\" from Kubernetes", "serviceaccounts \"app\" not found", true},
		"kubernetes grouped":    {"Cannot import", "deployments.apps \"web\" not found", true},
		"kubernetes in summary": {"configmaps \"app-config\" not found", "", true},
		"english not found":     {"Provider produced invalid response", "the endpoint was not found in the region", false},
		"forbidden":             {"Unauthorized", "serviceaccounts is forbidden: User \"x\" cannot get resource", false},
		"empty":                 {"", "", false},
	} {
		if got := Matches(tc.summary, tc.detail); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", name, got, tc.want)
		}
	}
}
