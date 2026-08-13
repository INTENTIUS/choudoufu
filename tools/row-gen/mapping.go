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

// mappingRow is one row of live/mapping.json (issue #43's committed
// artifact): tools/mapping-gen/mapping.go's Row, read back rather than
// imported, since that package is main and unexported.
type mappingRow struct {
	TFType     string  `json:"tf_type"`
	CFNType    *string `json:"cfn_type"`
	Via        string  `json:"via"`
	FoldParent *string `json:"fold_parent"`
}

type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

// loadMapping reads live/mapping.json and returns every row whose via is
// name, alias or fold - the "mapped set" this tool classifies. via:none rows
// carry no CFN evidence at all and are silently excluded, sorted by tf_type
// for deterministic output.
func loadMapping(path string) ([]mappingRow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art mappingArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make([]mappingRow, 0, len(art.Rows))
	for _, r := range art.Rows {
		switch r.Via {
		case "name", "alias", "fold":
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TFType < out[j].TFType })
	return out, nil
}
