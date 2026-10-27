// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/stamp"
)

// TestOfflineStampHonorsADeclaredStore is GitHub issue #2296. live-plan lets
// a writable record store settle the unmarked apply refusal for a type the
// apply can record (#1637). The offline check opens no store, so it takes a
// store the configuration names as writable, declared in the live block or
// derived from the backend block (#2293), and says so in the report. A live
// block that names no store keeps the refusal.
func TestOfflineStampHonorsADeclaredStore(t *testing.T) {
	schemas := stampUnmarkedApplyRecordBackedSchemas()
	dir := filepath.Join("testdata", "stamp-declared-store-2296")

	for _, tc := range []struct {
		name       string
		wantRefuse bool
		wantStore  string
	}{
		{"local", false, "local (declared in the live block)"},
		{"derived", false, "s3 (derived from the backend block)"},
		{"implied", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := Dir(t.Context(), filepath.Join(dir, tc.name), Context{Schemas: schemas})
			if !report.Readable() {
				t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
			}

			var refused bool
			for _, f := range report.Findings {
				if f.Layer == LayerStamp && f.ID == stamp.SummaryUnmarkedApply {
					refused = true
				}
			}
			if refused != tc.wantRefuse {
				t.Errorf("unmarked apply refusal fired = %v, want %v; findings: %v", refused, tc.wantRefuse, findingIDs(report))
			}
			if report.StoreAssumedWritable != tc.wantStore {
				t.Errorf("StoreAssumedWritable = %q, want %q", report.StoreAssumedWritable, tc.wantStore)
			}
			wantRecordOnly := 0
			if tc.wantStore != "" {
				wantRecordOnly = 1
			}
			if report.RecordOnly != wantRecordOnly {
				t.Errorf("RecordOnly = %d, want %d", report.RecordOnly, wantRecordOnly)
			}
		})
	}
}

// TestOfflineStampWithNoLiveBlockIsUnchanged: with no live block there is
// no store at all, and the refusal fires exactly as it did before #2296.
func TestOfflineStampWithNoLiveBlockIsUnchanged(t *testing.T) {
	report := Dir(t.Context(), stampUnmarkedApplyRecordBackedFixture, Context{Schemas: stampUnmarkedApplyRecordBackedSchemas()})
	var refused bool
	for _, f := range report.Findings {
		if f.Layer == LayerStamp && f.ID == stamp.SummaryUnmarkedApply {
			refused = true
		}
	}
	if !refused {
		t.Errorf("the unmarked apply refusal did not fire with no store; findings: %v", findingIDs(report))
	}
	if report.StoreAssumedWritable != "" || report.RecordOnly != 0 {
		t.Errorf("a store was assumed with no live block: %q, %d", report.StoreAssumedWritable, report.RecordOnly)
	}
}
