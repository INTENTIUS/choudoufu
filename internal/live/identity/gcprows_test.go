// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"strings"
	"testing"
)

// TestGoogleRowsRenderFromConfiguration resolves one resource of each
// shape [GoogleTable] rows, with no provider schemas, and asserts the
// rendered import ID: the provider block's project, region and zone stand
// in wherever a resource names none of its own, which is the defaulting
// hashicorp/google's ReplaceVars applies to the same id format.
func TestGoogleRowsRenderFromConfiguration(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-identity-rows", nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	want := map[string]string{
		"google_compute_network.vpc":              "projects/estate-prod/global/networks/estate-vpc",
		"google_compute_subnetwork.app":           "projects/estate-prod/regions/europe-west1/subnetworks/app",
		"google_compute_disk.data":                "projects/estate-data/zones/europe-west1-c/disks/data",
		"google_pubsub_topic.events":              "projects/estate-prod/topics/events",
		"google_service_account.runner":           "projects/estate-prod/serviceAccounts/ci-runner@estate-prod.iam.gserviceaccount.com",
		"google_project_iam_member.runner_logs":   "estate-prod roles/logging.logWriter serviceAccount:ci-runner@estate-prod.iam.gserviceaccount.com",
		"google_storage_bucket_iam_member.reader": "b/estate-artifacts roles/storage.objectViewer group:readers@example.com until 2027",
	}
	for addr, id := range want {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete && got.Class != ClassParentDerived {
			t.Errorf("%s = %s (%s), want a resolved identity", addr, got.Class, got.Reason)
			continue
		}
		if got.ImportID != id {
			t.Errorf("%s import ID = %q, want %q", addr, got.ImportID, id)
		}
	}

	minted := resolutionAt(t, result, "google_recaptcha_enterprise_key.site")
	if minted.Class != ClassNeedsDiscovery {
		t.Errorf("a minted identity resolved to %s, want NEEDS_DISCOVERY", minted.Class)
	}

	orphan := resolutionAt(t, result, "google_pubsub_topic.orphan")
	if orphan.Class != ClassNeedsDiscovery {
		t.Fatalf("a topic with no project anywhere = %s, want NEEDS_DISCOVERY", orphan.Class)
	}
	if !strings.Contains(orphan.Reason, "Google Cloud project") {
		t.Errorf("the reason does not name the missing project: %q", orphan.Reason)
	}
}

// TestGoogleProjectFromCloudContext is the rest of the AWS account's path:
// a project the provider block does not state is answered by the caller's
// [CloudContext], as an account ID is, before the instance falls to
// discovery.
func TestGoogleProjectFromCloudContext(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-identity-rows", nil)
	result, diags := ResolveIn(context.Background(), cfg, CloudContext{Project: "from-context"})
	assertNoErrors(t, diags)

	orphan := resolutionAt(t, result, "google_pubsub_topic.orphan")
	if orphan.ImportID != "projects/from-context/topics/orphan" {
		t.Errorf("orphan = %s %q, want the context's project", orphan.Class, orphan.ImportID)
	}
	vpc := resolutionAt(t, result, "google_compute_network.vpc")
	if vpc.ImportID != "projects/estate-prod/global/networks/estate-vpc" {
		t.Errorf("vpc import ID = %q; the provider block's project must win over the context's", vpc.ImportID)
	}
}
