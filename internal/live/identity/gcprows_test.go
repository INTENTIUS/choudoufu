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

// TestGoogleOptionalPlaceholderRow resolves a row whose id format names an
// optional argument with no default: an instance that writes it is named
// from configuration, and one that leaves it out is refused by name, the
// type keeping its row.
func TestGoogleOptionalPlaceholderRow(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-optional-placeholder", nil)
	result, diags := Resolve(context.Background(), cfg)

	got := resolutionAt(t, result, "google_bigquery_connection.lake")
	if want := "projects/estate-prod/locations/EU/connections/lake"; got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("written location: %s %q, want CONCRETE %q", got.Class, got.ImportID, want)
	}

	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want the one refusal: %v", len(diags), diags.Err())
	}
	if d := diags[0].Description(); !strings.Contains(d.Detail, "google_bigquery_connection.nowhere") || !strings.Contains(d.Detail, `"location"`) {
		t.Errorf("the refusal does not name the instance and its missing location: %s", d.Detail)
	}
}

// TestGoogleLongFormRefused resolves the arguments hashicorp/google
// rewrites before they become part of the id (#2144): an IAM parent the
// updater rebuilds from a long or short name, and an argument of a type
// whose create keeps only the last path segment. A long-form value refuses
// the instance by name; a short one, and a "/" in an argument the provider
// substitutes as written, resolve as before.
func TestGoogleLongFormRefused(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-long-form", nil)
	result, diags := Resolve(context.Background(), cfg)

	concrete := map[string]string{
		"google_secret_manager_secret_iam_member.byname": "projects/estate-prod/secrets/db-pass roles/secretmanager.secretAccessor group:a@example.com",
		"google_securityposture_posture.org":             "organizations/123/locations/global/postures/baseline",
	}
	for addr, id := range concrete {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete || got.ImportID != id {
			t.Errorf("%s = %s %q, want CONCRETE %q", addr, got.Class, got.ImportID, id)
		}
	}

	refused := map[string]string{
		"google_secret_manager_secret_iam_member.byid":         `secret_id to "projects/estate-prod/secrets/db-pass"`,
		"google_secret_manager_secret_iam_member.literal_long": `secret_id to "projects/other/secrets/db-pass"`,
		"google_compute_network_firewall_policy_rule.r":        `firewall_policy to "projects/estate-prod/global/firewallPolicies/pol"`,
		"google_secret_manager_secret_iam_member.minted":       `secret_id from google_recaptcha_enterprise_key.site.id, which is not known until apply`,
	}
	for addr, arg := range refused {
		for _, res := range result.All() {
			if res.Addr.String() == addr {
				t.Errorf("%s resolved to %s %q, want it refused", addr, res.Class, res.ImportID)
			}
		}
		found := false
		for _, d := range diags {
			desc := d.Description()
			if desc.Summary == "Long-form identity argument not composed yet" && strings.Contains(desc.Detail, addr+" sets "+arg) && strings.Contains(desc.Detail, "#2144") {
				found = true
			}
		}
		if !found {
			t.Errorf("no long-form refusal names %s and %s: %v", addr, arg, diags.Err())
		}
	}
	if len(diags) != len(refused) {
		t.Errorf("got %d diagnostics, want the %d refusals: %v", len(diags), len(refused), diags.Err())
	}
}
