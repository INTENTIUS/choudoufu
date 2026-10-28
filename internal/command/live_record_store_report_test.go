// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"testing"

	"github.com/intentius/choudoufu/internal/command/views"
)

// GitHub issue #2293's contract on live-check -json: a live root with no
// estate and no record_store gets both from its backend block, with no cloud
// call. One fixture per backend that has a record store counterpart.
func TestLiveCheckJSON_RecordStoreDerivedFromBackend(t *testing.T) {
	for _, tc := range []struct {
		dir        string
		kind       string
		bucket     string
		declaredAt string
	}{
		{"testdata/live-record-store-s3", "s3", "acme-tfstate", "main.tf:2"},
		{"testdata/live-record-store-gcs", "gcs", "acme-state", "main.tf:2"},
		{"testdata/live-record-store-azurerm", "azurerm", "acmestate/tfstate", "main.tf:2"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			c, done := newLiveCheckCommand(t)
			c.Run([]string{"-json", tc.dir})
			out := done(t)

			var doc struct {
				Estate       string `json:"estate"`
				EstateSource string `json:"estate_source"`
				RecordStore  struct {
					Kind       string `json:"kind"`
					Bucket     string `json:"bucket"`
					Prefix     string `json:"prefix"`
					Source     string `json:"source"`
					DeclaredAt string `json:"declared_at"`
				} `json:"record_store"`
			}
			if err := json.Unmarshal([]byte(out.Stdout()), &doc); err != nil {
				t.Fatalf("stdout is not valid JSON: %s\nstdout:\n%s\nstderr:\n%s", err, out.Stdout(), out.Stderr())
			}
			if doc.Estate != "prod-network" || doc.EstateSource != liveEstateSourceBackendKey {
				t.Errorf("estate = %q from %q, want prod-network from the backend key", doc.Estate, doc.EstateSource)
			}
			rs := doc.RecordStore
			if rs.Kind != tc.kind || rs.Bucket != tc.bucket || rs.Source != liveStoreSourceBackend {
				t.Errorf("record_store = %+v, want %s %q from the backend", rs, tc.kind, tc.bucket)
			}
			if rs.Prefix != "tofu-records/prod-network/" {
				t.Errorf("prefix = %q, want tofu-records/prod-network/", rs.Prefix)
			}
			if rs.DeclaredAt != tc.declaredAt {
				t.Errorf("declared_at = %q, want %q", rs.DeclaredAt, tc.declaredAt)
			}
		})
	}
}

func TestLiveSetupLine(t *testing.T) {
	got := liveSetupLine(liveSetupStoreFixture())
	want := `Records: s3 bucket "acme-tfstate" under tofu-records/prod-network/ (derived from backend "s3" at backend.tf:2); estate "prod-network" (from the backend key)`
	if got != want {
		t.Errorf("line:\n got %s\nwant %s", got, want)
	}
}

func liveSetupStoreFixture() views.LiveSetup {
	return views.LiveSetup{
		Estate:       "prod-network",
		EstateSource: liveEstateSourceBackendKey,
		RecordStore: views.LiveSetupStore{
			Kind:       "s3",
			Bucket:     "acme-tfstate",
			Prefix:     "tofu-records/prod-network/",
			Source:     liveStoreSourceBackend,
			DeclaredAt: "backend.tf:2",
		},
	}
}
