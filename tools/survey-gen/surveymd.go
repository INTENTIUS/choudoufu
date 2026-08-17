// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"strings"
)

// HandRow is one row of SURVEY.md's hand-written per-type table.
type HandRow struct {
	// Type, Path and Status are the table's fixed-vocabulary columns.
	Type, Path, Status string

	// Identity is the prose identity column, kept for messages.
	Identity string

	// SchemaTier is true when the row's Source column carries the `schema`
	// derivation tier (the provider ships an identity schema for the type)
	// and false for the `docs` tier (it does not). This is the one raw
	// signal the hand table records per row.
	SchemaTier bool
}

// readRoster parses SURVEY.md's per-type table. The table is the only place
// in the file where a row's first cell is a resource type name, which is
// what keeps this parser from reading the file's other tables: the summary
// and status tables never open with an `aws_` cell.
func readRoster(path string) ([]HandRow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}

	validPaths := map[string]bool{
		pathClientNamed:          true,
		pathMarker:               true,
		pathParentDerived:        true,
		pathEnumerableUnbindable: true,
		pathAccountDerived:       true,
		pathUniqueName:           true,
		pathOps:                  true,
	}

	var rows []HandRow
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "| aws_") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) != 5 {
			return nil, fmt.Errorf("per-type table row with %d cells instead of 5: %s", len(cells), trimmed)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		row := HandRow{
			Type:     cells[0],
			Path:     cells[1],
			Status:   cells[2],
			Identity: cells[3],
		}
		if !validPaths[row.Path] {
			return nil, fmt.Errorf("%s carries a Path outside the fixed vocabulary: %q", row.Type, row.Path)
		}
		// The Source column is "provenance; tier".
		switch tier := cells[4][strings.LastIndex(cells[4], ";")+1:]; strings.TrimSpace(tier) {
		case "schema":
			row.SchemaTier = true
		case "docs":
			row.SchemaTier = false
		default:
			return nil, fmt.Errorf("%s carries a Source without a schema/docs tier: %q", row.Type, cells[4])
		}
		if seen[row.Type] {
			return nil, fmt.Errorf("%s appears twice in the per-type table", row.Type)
		}
		seen[row.Type] = true
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no per-type rows found in %s", path)
	}
	return rows, nil
}

// rosterTypes is just the type names of the hand table's rows, in table
// order.
func rosterTypes(rows []HandRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Type)
	}
	return out
}
