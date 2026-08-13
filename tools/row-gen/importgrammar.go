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

// importGrammarRow is the slice of live/import-grammar.json's per-type shape
// (tools/importdocs-gen, issue #52/#55) this tool reads: whether the
// provider's own Import documentation shows the ID built from configuration
// arguments, the literal example that showed it, and the argument names the
// ID's segments matched against Argument Reference (Arguments) - the source
// resolveArgName consults ahead of the carve seed (issue #52's second half:
// retiring tools/mapping-gen/carve-seed.json in the import-grammar rows it
// makes redundant).
type importGrammarRow struct {
	TFType              string   `json:"tf_type"`
	ImportIDExample     string   `json:"import_id_example"`
	ComposedOfArguments *bool    `json:"composed_of_arguments"`
	Separator           *string  `json:"separator"`
	Arguments           []string `json:"arguments"`
}

type importGrammarArtifact struct {
	Rows []importGrammarRow `json:"rows"`
}

// loadImportGrammar reads live/import-grammar.json and indexes it by TF
// type name. A missing file is not an error: the demotion hook this feeds
// (classifyAll's applyImportGrammarDemotions) is additive evidence, not a
// required input, and every artifact this tool already reads predates
// import-grammar.json's own introduction - see #55's sequencing note that
// this source lands ahead of the orchestrator that would otherwise
// guarantee its presence.
func loadImportGrammar(path string) (map[string]importGrammarRow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]importGrammarRow{}, nil
		}
		return nil, err
	}
	var art importGrammarArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make(map[string]importGrammarRow, len(art.Rows))
	for _, r := range art.Rows {
		out[r.TFType] = r
	}
	return out, nil
}
