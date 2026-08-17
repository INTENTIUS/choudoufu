// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// registryEntry is the slice of live/registry.json's per-type shape
// (tools/registry-gen/parse.go's Entry) that classification actually reads:
// the primary identifier, the three property partitions, and the handlers
// section's list-ability. A schema change to the rest of that artifact
// (tagging detail, additional_identifiers) does not ripple into this tool.
type registryEntry struct {
	TypeName             string   `json:"type_name"`
	PrimaryIdentifier    []string `json:"primary_identifier"`
	ReadOnlyProperties   []string `json:"read_only_properties"`
	CreateOnlyProperties []string `json:"create_only_properties"`
	Handlers             struct {
		List              bool     `json:"list"`
		ListRequiredInput []string `json:"list_required_input"`
	} `json:"handlers"`

	// UniqueNameProperty is the path, inside this type's CloudFormation
	// schema, of a Name property whose own description asserts the name is
	// unique within the account and region - tools/registry-gen computes it
	// with internal/live/uniquename.Asserted, and it is empty for the great
	// majority of types, whose schema makes no such claim. It is the
	// REGISTRY half of the two-source uniqueness evidence uniquename.go
	// crosses; the provider half is live/import-grammar.json's
	// declared_unique, and neither alone admits anything.
	UniqueNameProperty string `json:"unique_name_property"`
}

// readOnlySet and createOnlySet are the two property partitions classify.go
// tests membership against, as sets rather than slices.
func (e registryEntry) readOnlySet() map[string]bool   { return toSet(e.ReadOnlyProperties) }
func (e registryEntry) createOnlySet() map[string]bool { return toSet(e.CreateOnlyProperties) }

func toSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// registryArtifact is the slice of live/registry.json this tool decodes.
type registryArtifact struct {
	Types []registryEntry `json:"types"`
}

// loadRegistry reads live/registry.json (issue #42's committed artifact) and
// indexes it by CFN type name.
func loadRegistry(path string) (map[string]registryEntry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art registryArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make(map[string]registryEntry, len(art.Types))
	for _, e := range art.Types {
		out[e.TypeName] = e
	}
	return out, nil
}
