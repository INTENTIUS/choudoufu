// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/substrate"
)

// TestMarkerSurfaceForNamesEverySubstrateSurface: every surface a
// substrate declares has a name in the ownership read, and the name comes
// back as the same surface. A surface with no arm in markerSurfaceFor
// would read as surfaceNone, which checkOwnership admits unchecked - the
// shape of GitHub issue #1108, one layer down (#1118).
func TestMarkerSurfaceForNamesEverySubstrateSurface(t *testing.T) {
	for _, sub := range substrate.All {
		for _, surface := range sub.Surfaces() {
			got := markerSurfaceFor(surface)
			if got == surfaceNone {
				t.Errorf("%s surface %q has no name in markerSurfaceFor, so checkOwnership would admit its types without reading a marker", sub.Name(), surface)
				continue
			}
			if back := got.surface(); back != surface {
				t.Errorf("%s surface %q round-trips to %q", sub.Name(), surface, back)
			}
		}
	}
}
