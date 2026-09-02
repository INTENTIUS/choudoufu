// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/markers/markerstest"
)

// TestTaggableIsMarkersTaggable is the guard for the divergence described in
// [markerstest]: this package's taggable used to be a four-clause copy of the
// shape test, and stayed four-clause when markers.TagSurface grew a fifth.
//
// It asserts the ANSWER on a block the fifth clause decides, not the shape,
// which is the whole point: a re-inlined copy passes any test that only feeds
// it a plain tags map.
func TestTaggableIsMarkersTaggable(t *testing.T) {
	refused := markerstest.VocabularyRefusedBlock()
	free := markerstest.FreeFormTagsBlock()

	// The fixture has to still be the case it claims to be, or the assertion
	// below is vacuous.
	if markers.Taggable(refused) {
		t.Fatalf("markerstest.VocabularyRefusedBlock is no longer refused by markers.Taggable; the fixture needs a new refusal case")
	}
	if !markers.Taggable(free) {
		t.Fatalf("markerstest.FreeFormTagsBlock is no longer admitted by markers.Taggable; the fixture is broken")
	}

	if taggable(refused) {
		t.Errorf("untag.taggable admits a tags map whose keys the provider documents as its own namespace; markers.Taggable refuses it, and this package writes")
	}
	if !taggable(free) {
		t.Errorf("untag.taggable refuses a free-form tags map that markers.Taggable admits")
	}
}
