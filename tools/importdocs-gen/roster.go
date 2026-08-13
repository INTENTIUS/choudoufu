// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// surveyFullArtifact is the slice of live/survey-full.json this tool reads:
// just the type-name roster (issue #41's whole-provider survey), sorted so
// -limit's "first N" is deterministic.
type surveyFullArtifact struct {
	Types []struct {
		Type string `json:"type"`
	} `json:"types"`
}

// loadRoster reads live/survey-full.json's whole-provider type roster.
// import-grammar.json sweeps every TF type the provider ships, not just
// the mapped set live/mapping.json narrows to - the same reasoning issue
// #41 gives survey-full.json itself: which types are admitted or mapped is
// downstream curation, but "which types exist and what their docs say" is
// not.
func loadRoster(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art surveyFullArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	names := make([]string, 0, len(art.Types))
	for _, t := range art.Types {
		names = append(names, t.Type)
	}
	sort.Strings(names)
	return names, nil
}
