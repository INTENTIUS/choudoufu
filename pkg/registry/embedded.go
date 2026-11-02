// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import (
	_ "embed"
	"fmt"
	"sync"
)

// The two artifacts, embedded so a shipped binary can build the roster
// without a checkout. They are byte-for-byte copies of live/mapping.json and
// live/registry.json (go:embed cannot reach outside the package directory);
// TestEmbeddedArtifactsMatchLive holds the copies to the originals, so a
// regeneration of the live/ artifacts fails the suite until these are
// re-copied.
var (
	//go:embed mapping.json
	embeddedMappingJSON []byte

	//go:embed registry.json
	embeddedRegistryJSON []byte
)

var (
	embeddedOnce   sync.Once
	embeddedRoster *Roster
	embeddedErr    error
)

// Embedded returns the roster built from the artifacts compiled into the
// binary, parsed once. This is what production commands use ([Load] and
// [Parse] serve tests and tools that point at a checkout's live/ copies).
func Embedded() (*Roster, error) {
	embeddedOnce.Do(func() {
		embeddedRoster, embeddedErr = Parse(embeddedMappingJSON, embeddedRegistryJSON)
		if embeddedErr != nil {
			embeddedErr = fmt.Errorf("registry: parsing the embedded artifacts: %w", embeddedErr)
		}
	})
	return embeddedRoster, embeddedErr
}
