// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// TestRecordTripsAttributeThroughTheAccessorLayer is the half of the
// instrument that only this package can prove: every record read in a plan
// goes through a [RecordStore] accessor, so a counter that stopped at
// `RecordStore.getRaw` would report one site for the whole run and name
// nothing. Via must be the accessor and Site must be the code that wanted
// the record.
func TestRecordTripsAttributeThroughTheAccessorLayer(t *testing.T) {
	ctx := context.Background()
	counting := staterecord.NewCountingStore(localHintStore(t), nil)
	store := NewRecordEnvelopeStore(counting, RecordKeyPrefix("trip-estate"))
	addr := locatedTestAddr(t, "aws_globalaccelerator_listener", "svc")

	if _, _, _, _, err := store.GetIdentity(ctx, addr); err != nil {
		t.Fatalf("GetIdentity: %v", err)
	}
	if _, _, _, _, err := store.GetResidue(ctx, addr); err != nil {
		t.Fatalf("GetResidue: %v", err)
	}
	if _, _, _, err := store.getProvisioned(ctx, addr); err != nil {
		t.Fatalf("getProvisioned: %v", err)
	}

	trips := counting.Trips()
	if len(trips) != 3 {
		t.Fatalf("got %d trips, want 3: %+v", len(trips), trips)
	}
	wantVia := []string{
		"live/projection.(*RecordStore).GetIdentity",
		"live/projection.(*RecordStore).GetResidue",
		"live/projection.(*RecordStore).getProvisioned",
	}
	for i, tr := range trips {
		if tr.Via != wantVia[i] {
			t.Errorf("trip %d Via = %q, want %q", i, tr.Via, wantVia[i])
		}
		if !strings.Contains(tr.Site, "TestRecordTripsAttributeThroughTheAccessorLayer") {
			t.Errorf("trip %d Site = %q, want it to name this test rather than the accessor", i, tr.Site)
		}
		if strings.Contains(tr.Site, "(*RecordStore)") {
			t.Errorf("trip %d Site = %q attributes the trip to the accessor layer", i, tr.Site)
		}
	}

	// Three accessors, one key: this is the shape the whole reduction turns
	// on, and it has to be visible in the counts rather than inferred.
	got := staterecord.Summarize(trips)
	if got.DistinctKeys != 1 {
		t.Errorf("DistinctKeys = %d, want 1 - three accessors read one envelope", got.DistinctKeys)
	}
	if got.RepeatTrips != 2 {
		t.Errorf("RepeatTrips = %d, want 2", got.RepeatTrips)
	}
}

// TestWrapForTripLogIsOffByDefault pins the flag-off property: an ordinary
// run wraps nothing, so the instrument cannot change what it measures.
func TestWrapForTripLogIsOffByDefault(t *testing.T) {
	t.Setenv(RecordTripLogEnvVar, "")
	inner := localHintStore(t)
	got, err := wrapForTripLog(inner)
	if err != nil {
		t.Fatalf("wrapForTripLog: %v", err)
	}
	if got != inner {
		t.Errorf("wrapForTripLog returned %T with the variable unset, want the store untouched", got)
	}
}
