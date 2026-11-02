// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"strings"
	"testing"
)

// TestGoogleAssetImportIDFullName pins #2145 item 5: the Cloud Asset
// Inventory name of each labelled type whose Asset name is its whole name.
func TestGoogleAssetImportIDFullName(t *testing.T) {
	const apihub = "//apihub.googleapis.com/projects/my-proj/locations/us-central1/apiHubInstances/hub-1"
	id, ok := GoogleAssetImportID("google_apihub_api_hub_instance", apihub)
	if want := "projects/my-proj/locations/us-central1/apiHubInstances/hub-1"; !ok || id != want {
		t.Errorf("apihub: got %q, %v; want %q", id, ok, want)
	}
	for _, bad := range []string{
		"//apihub.googleapis.com/projects/my-proj/apiHubInstances/hub-1",
		"//other.googleapis.com/projects/my-proj/locations/us-central1/apiHubInstances/hub-1",
	} {
		if id, ok := GoogleAssetImportID("google_apihub_api_hub_instance", bad); ok {
			t.Errorf("apihub %s: got %q, want refused", bad, id)
		}
	}

	for typ, why := range map[string]string{
		"google_service_directory_namespace": "a project number is not the project ID",
		"google_dataflow_job":                "schema.ImportStatePassthrough",
	} {
		name := map[string]string{
			"google_service_directory_namespace": "//servicedirectory.googleapis.com/projects/123456789/locations/us-central1/namespaces/ns",
			"google_dataflow_job":                "//dataflow.googleapis.com/projects/my-proj/locations/us-central1/jobs/2026-10-20_01_02_03-1",
		}[typ]
		if id, ok := GoogleAssetImportID(typ, name); ok {
			t.Errorf("%s: got %q, want refused", typ, id)
		}
		if got := GoogleAssetUnknown[typ]; !strings.Contains(got, why) {
			t.Errorf("%s: reason %q, want one naming %q", typ, got, why)
		}
	}
}
