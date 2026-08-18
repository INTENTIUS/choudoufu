// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/markers/markerstest"
)

// TestTaggableIsMarkersTaggable is the guard for the divergence described in
// [markerstest]. live-import writes the marker that adopts a live object, so
// admitting a tag map the provider owns the key space of writes a marker the
// API rejects or rewrites - and reports success either way.
func TestTaggableIsMarkersTaggable(t *testing.T) {
	refused := markerstest.VocabularyRefusedBlock()
	free := markerstest.FreeFormTagsBlock()

	if markers.Taggable(refused) {
		t.Fatalf("markerstest.VocabularyRefusedBlock is no longer refused by markers.Taggable; the fixture needs a new refusal case")
	}
	if !markers.Taggable(free) {
		t.Fatalf("markerstest.FreeFormTagsBlock is no longer admitted by markers.Taggable; the fixture is broken")
	}

	if taggable(refused) {
		t.Errorf("liveimport.taggable admits a tags map whose keys the provider documents as its own namespace; markers.Taggable refuses it, and this package writes")
	}
	if !taggable(free) {
		t.Errorf("liveimport.taggable refuses a free-form tags map that markers.Taggable admits")
	}
}
