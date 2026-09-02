// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"path/filepath"

	"github.com/spf13/afero"
)

// overlayFS builds the filesystem [LoadOverlay] hands the configuration
// parser: the real one, with the overlay's files laid over it.
//
// nil for an empty overlay, and configs.NewParser reads nil as afero.OsFs -
// so [Load] goes through exactly the code path it always did, with no
// filesystem wrapper in it at all. That is deliberate rather than tidy: the
// published-form numbers this repository's whole burndown is computed from
// run through Load, and the one thing this change must not do is move them.
//
// The layering is afero's CopyOnWriteFs over the OS, with a MemMapFs holding
// the overlay. Two properties are load-bearing and both are pinned by
// TestOverlayFS:
//
//   - A directory listing is the UNION of the two layers, deduplicated, so a
//     file the overlay adds (the live sidecar) appears to the loader's own
//     directory scan and a file it replaces appears once, not twice.
//   - A path in neither layer still returns a not-exist error, so the
//     parser's own "does this directory hold configuration" tests behave.
func overlayFS(overlay map[string][]byte) afero.Fs {
	if len(overlay) == 0 {
		return nil
	}
	layer := afero.NewMemMapFs()
	for path, src := range overlay {
		// MkdirAll on the parent first: MemMapFs will invent the parent
		// entry for a written file, but a ReadDir of that parent has to
		// find a real directory node to merge against the OS layer's.
		_ = layer.MkdirAll(filepath.Dir(path), 0o755)
		_ = afero.WriteFile(layer, path, src, 0o644)
	}
	return afero.NewCopyOnWriteFs(afero.NewOsFs(), layer)
}
