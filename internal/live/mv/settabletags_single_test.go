// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/markers/markerstest"
)

// TestSettableTagsIsMarkersTaggable is the guard for the divergence described
// in [markerstest]. live-mv rewrites tofu-address on an object that already
// exists, which is the worst of the three: on a type whose tags argument the
// provider documents as immutable, the rewrite forces a replacement.
func TestSettableTagsIsMarkersTaggable(t *testing.T) {
	refused := markerstest.VocabularyRefusedBlock()
	free := markerstest.FreeFormTagsBlock()

	if markers.Taggable(refused) {
		t.Fatalf("markerstest.VocabularyRefusedBlock is no longer refused by markers.Taggable; the fixture needs a new refusal case")
	}
	if !markers.Taggable(free) {
		t.Fatalf("markerstest.FreeFormTagsBlock is no longer admitted by markers.Taggable; the fixture is broken")
	}

	if settableTags(refused) {
		t.Errorf("mv.settableTags admits a tags map whose keys the provider documents as its own namespace; markers.Taggable refuses it, and this package rewrites a marker on a live object")
	}
	if !settableTags(free) {
		t.Errorf("mv.settableTags refuses a free-form tags map that markers.Taggable admits")
	}
}
