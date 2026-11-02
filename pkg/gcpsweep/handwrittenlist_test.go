// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"testing"
)

// TestHandwrittenTypesListAndBind (#2274): Cloud SQL instances, GKE clusters
// and Assured Workloads workloads list at their written-out collection, read
// their labels where each API keeps them, carry their documented Asset name,
// and that name gives the type's import ID.
func TestHandwrittenTypesListAndBind(t *testing.T) {
	for _, tc := range []struct {
		typeName, path, body, asset, importID string
		parents                               map[string]string
	}{
		{"google_sql_database_instance", "/v1/projects/p1/instances",
			`{"items": [{"name": "db1", "selfLink": "https://sqladmin.googleapis.com/sql/v1beta4/projects/p1/instances/db1",
			  "settings": {"userLabels": {"tofu-estate": "est", "tofu-address": "a1"}}},
			 {"name": "db2", "selfLink": "https://sqladmin.googleapis.com/sql/v1beta4/projects/p1/instances/db2",
			  "settings": {"userLabels": {"tofu-estate": "est-east"}}}]}`,
			"//cloudsql.googleapis.com/projects/p1/instances/db1", "projects/p1/instances/db1", nil},
		{"google_container_cluster", "/v1/projects/p1/locations/-/clusters",
			`{"clusters": [{"name": "c1", "location": "us-central1-a",
			  "resourceLabels": {"tofu-estate": "est", "tofu-address": "a1"}}]}`,
			"//container.googleapis.com/projects/p1/locations/us-central1-a/clusters/c1",
			"projects/p1/locations/us-central1-a/clusters/c1", nil},
		{"google_assured_workloads_workload", "/v1/organizations/123/locations/us-central1/workloads",
			`{"workloads": [{"name": "organizations/123/locations/us-central1/workloads/w1",
			  "labels": {"tofu-estate": "est", "tofu-address": "a1"}}]}`,
			"//assuredworkloads.googleapis.com/organizations/123/locations/us-central1/workloads/w1",
			"organizations/123/locations/us-central1/workloads/w1", map[string]string{"organization": "123"}},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			tl, ok := Covered(tc.typeName)
			if !ok || !tl.Derived {
				t.Fatalf("%s is not covered as a listed type", tc.typeName)
			}
			if tl.Consistency != "" {
				t.Errorf("%s claims consistency; it binds, consistency unmeasured", tc.typeName)
			}
			scope, err := tl.Scope(scopeVarsForTest(tc.parents))
			if err != nil {
				t.Fatal(err)
			}
			_, c := newListFake(t, tc.typeName, tc.path, fakeResponse{200, tc.body})
			got, err := c.ListType(context.Background(), tc.typeName, scope, "est")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Name != tc.asset {
				t.Fatalf("got %+v, want one object named %s", got, tc.asset)
			}
			if got[0].Labels["tofu-address"] != "a1" {
				t.Errorf("labels = %v, want the address label read from where the API keeps it", got[0].Labels)
			}
			if !tl.Names(got[0]) {
				t.Errorf("%s does not read its own listed name %s", tc.typeName, got[0].Name)
			}
			id, ok := HandwrittenImportID(tc.typeName, got[0].Name)
			if !ok || id != tc.importID {
				t.Errorf("import ID = %q, %v, want %q", id, ok, tc.importID)
			}
		})
	}

	if _, ok := HandwrittenImportID("google_sql_database_instance", "//sqladmin.googleapis.com/projects/p1/instances/db1"); ok {
		t.Error("a name with another host than the documented Asset name's is not given an import ID")
	}
	if why := Uncovered("google_dialogflow_cx_intent"); why == "" {
		t.Error("google_dialogflow_cx_intent stays uncovered (ruled on plunge#19)")
	}
}
