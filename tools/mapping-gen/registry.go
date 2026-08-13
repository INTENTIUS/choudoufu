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

// CFNRoster is the CFN-side type roster the join reads: everything past
// this point only needs a slice of AWS::Service::Resource names, never
// caring where they came from. That seam is what let this tool's join logic
// (heuristic.go, mapping.go) be built and tested before issue #42 landed,
// against the small fixture at testdata/cfn-types.json - swapping the real
// source in below was the one-line change the seam promised.
type CFNRoster interface {
	// Types returns every CFN resource type name in the roster
	// (AWS::Service::Resource), deduplicated, unsorted.
	Types() ([]string, error)
}

// registryJSONRoster reads the CFN-side roster from live/registry.json,
// tools/registry-gen's committed artifact (issue #42). This is the
// production source: mapping-gen reads it directly, no zip and no network,
// same as it reads live/survey-full.json for the TF side.
type registryJSONRoster struct {
	path string
}

// registryArtifact is the slice of live/registry.json's shape
// (RegistryArtifact in tools/registry-gen/registry.go) this loader actually
// needs - the type names and, for the former2 join's own eyeball-first
// guard (issue #52), whether each carries a primaryIdentifier - so a schema
// change to the rest of that artifact (counts, handlers, tagging, ...) does
// not ripple into this tool.
type registryArtifact struct {
	Types []struct {
		TypeName          string   `json:"type_name"`
		PrimaryIdentifier []string `json:"primary_identifier"`
	} `json:"types"`
}

func (r registryJSONRoster) Types() ([]string, error) {
	art, err := r.load()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(art.Types))
	for _, t := range art.Types {
		out = append(out, t.TypeName)
	}
	return out, nil
}

// WithPrimaryIdentifier returns the set of CFN types that carry a
// primaryIdentifier - used to drop former2 rows naming a CFN type the
// registry-backed admission path could never reach regardless of what
// former2 says (issue #52).
func (r registryJSONRoster) WithPrimaryIdentifier() (map[string]bool, error) {
	art, err := r.load()
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(art.Types))
	for _, t := range art.Types {
		if len(t.PrimaryIdentifier) > 0 {
			out[t.TypeName] = true
		}
	}
	return out, nil
}

func (r registryJSONRoster) load() (registryArtifact, error) {
	data, err := os.ReadFile(r.path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return registryArtifact{}, err
	}
	var art registryArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return registryArtifact{}, fmt.Errorf("decoding %s: %w", r.path, err)
	}
	return art, nil
}

// staticRoster is a fixed in-memory CFN roster: testdata/cfn-types.json's
// small committed fixture, for tests that need a real CFNRoster
// implementation but not live/registry.json's full ~1,650-type artifact.
type staticRoster []string

func (s staticRoster) Types() ([]string, error) { return s, nil }

// loadStaticRoster reads a testdata roster file: a bare JSON array of CFN
// type names, nothing else.
func loadStaticRoster(path string) (staticRoster, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return names, nil
}
