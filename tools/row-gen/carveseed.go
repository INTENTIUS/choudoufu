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

// carveSeedEntry is one row of tools/mapping-gen/carve-seed.json (issue
// #43): chant's hand-curated AWS_CARVE_TYPES table, transcribed. identityAttr
// is nil for the entries whose chant row carries none.
//
// Adjudicated under #100: deliberate curation, almost fully retired. This
// source sits last in resolveArgName's precedence, and a full proposal run
// on 2026-08-14 attributed exactly one argument to it (aws_ecs_cluster's
// "name"). The file's own _provenance records the staleness rule: a
// provider release that adds an identity schema for a seeded type retires
// that entry, and when the last one retires this loader goes with it.
type carveSeedEntry struct {
	TFType       string  `json:"tfType"`
	IdentityAttr *string `json:"identityAttr"`
}

type carveSeedArtifact struct {
	Entries []carveSeedEntry `json:"entries"`
}

// loadCarveSeed reads tools/mapping-gen/carve-seed.json and indexes it by TF
// type name, dropping entries whose identityAttr is null - a null entry has
// nothing this tool's client-named rule can use, so it is absent from the
// returned map rather than present with an empty string.
func loadCarveSeed(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art carveSeedArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make(map[string]string, len(art.Entries))
	for _, e := range art.Entries {
		if e.IdentityAttr != nil && *e.IdentityAttr != "" {
			out[e.TFType] = *e.IdentityAttr
		}
	}
	return out, nil
}
