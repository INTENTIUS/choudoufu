// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

	// ArgumentReference, ArgumentsInOrder, IdentitySchemaRequired and
	// IdentitySchemaOptional are the widened scrape's own evidence (issue:
	// the decisive identity evidence for a scrape-gap type sits in the
	// Argument Reference or the Identity Schema, not the Import section's
	// prose alone) - see tools/importdocs-gen/artifact.go's Row for what
	// each one means; the shapes here are the same, just re-declared for
	// this package's own JSON decode the way the five fields above already
	// are.
	ArgumentReference      []argumentRefEntry `json:"argument_reference"`
	ArgumentsInOrder       []string           `json:"arguments_in_doc_order"`
	IdentitySchemaRequired []string           `json:"identity_schema_required"`
	IdentitySchemaOptional []string           `json:"identity_schema_optional"`

	// IDParts is the documented import ID's per-segment source attribution
	// (issue #132): the segment names the Import section's own prose gives,
	// each attributed to the doc section that defines it ("argument",
	// "attribute" or "unknown"), present only when the named segments
	// account for every segment of the documented example - see
	// tools/importdocs-gen/parse.go's idParts.
	IDParts []idPart `json:"id_parts"`
}

// idPart mirrors tools/importdocs-gen/parse.go's IDPart: one prose-named
// segment of the documented import ID and which doc section defines it.
type idPart struct {
	Token  string `json:"token"`
	Source string `json:"source"`
}

// idPartSourceAttribute is the Source value naming a segment the doc's own
// Attribute Reference exports - a server-provided value no configuration
// argument supplies. The other two values ("argument", "unknown") are only
// ever tested for indirectly, so they carry no constant here.
const idPartSourceAttribute = "attribute"

// docNamesServerSegment reports whether the grammar row's per-segment
// attribution names at least one segment as the doc's own Attribute
// Reference export. When it does, the doc itself is saying the import ID
// carries a server-provided value, so the ID is not reconstructible from
// configuration - the fact that separates aws_connect_queue
// ("instance_id:queue_id", queue_id exported) from the structurally
// identical-looking aws_lambda_alias ("function_name/alias", alias being
// prose shorthand for the `name` argument, attributed "unknown"). A
// non-empty IDParts already guarantees the named segments cover the whole
// documented example (the scrape's own arity gate), so no further arity
// check is needed here.
func docNamesServerSegment(g importGrammarRow) bool {
	for _, part := range g.IDParts {
		if part.Source == idPartSourceAttribute {
			return true
		}
	}
	return false
}

// serverSegmentTokens names the attribute-sourced segments, for the note a
// docNamesServerSegment-based decision leaves behind.
func serverSegmentTokens(g importGrammarRow) string {
	var out []string
	for _, part := range g.IDParts {
		if part.Source == idPartSourceAttribute {
			out = append(out, part.Token)
		}
	}
	return strings.Join(out, ", ")
}

// argumentRefEntry mirrors tools/importdocs-gen/artifact.go's
// ArgumentRefEntry: one Argument Reference bullet's name and its
// Required/ForceNew marking.
type argumentRefEntry struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	ForceNew bool   `json:"force_new"`
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
