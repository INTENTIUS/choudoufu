// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// The via vocabulary, verbatim from issue #43: nothing outside these four
// tokens appears in a row's via column.
const (
	viaName  = "name"
	viaAlias = "alias"
	viaFold  = "fold"
	viaNone  = "none"
)

// Row is one TF type's join result: live/mapping.json has one row per TF
// type in the roster it was generated against, sorted by tf_type.
type Row struct {
	TFType     string  `json:"tf_type"`
	CFNType    *string `json:"cfn_type"`
	Via        string  `json:"via"`
	FoldParent *string `json:"fold_parent"`
	Note       *string `json:"note"`
}

// Mapping is the committed artifact: live/mapping.json.
type Mapping struct {
	// GeneratedBy names the tool so a reader of the JSON alone knows where
	// rows come from and how to refresh them.
	GeneratedBy string `json:"generated_by"`

	// Counts are the roster-wide headline totals: mapped (via name or
	// alias) is the number issue #40's 60-75% band gets replaced with.
	Counts MappingCounts `json:"counts"`

	// Rows has one entry per TF type, sorted by tf_type.
	Rows []Row `json:"rows"`
}

// MappingCounts are the roster-wide totals.
type MappingCounts struct {
	Types  int `json:"types"`
	Mapped int `json:"mapped"`
	Fold   int `json:"fold"`
	None   int `json:"none"`
}

// buildMapping joins the TF roster against the CFN roster: the overlay's
// aliases, folds and nones win outright (curated, exact, and validated
// against the current CFN roster), the name heuristic tries next for
// anything the overlay does not cover, and anything still unclaimed is
// via:none with a generic note.
func buildMapping(tfTypes, cfnTypes []string, ov Overlay) (Mapping, error) {
	index, err := buildNameIndex(cfnTypes)
	if err != nil {
		return Mapping{}, err
	}

	cfnSet := make(map[string]bool, len(cfnTypes))
	for _, t := range cfnTypes {
		cfnSet[t] = true
	}

	sorted := append([]string(nil), tfTypes...)
	sort.Strings(sorted)

	m := Mapping{GeneratedBy: "tools/mapping-gen (go run ./tools/mapping-gen)"}
	for _, tf := range sorted {
		row, err := classifyRow(tf, index, cfnSet, ov)
		if err != nil {
			return Mapping{}, err
		}
		switch row.Via {
		case viaName, viaAlias:
			m.Counts.Mapped++
		case viaFold:
			m.Counts.Fold++
		case viaNone:
			m.Counts.None++
		}
		m.Counts.Types++
		m.Rows = append(m.Rows, row)
	}
	return m, nil
}

// classifyRow decides one TF type's row, in priority order: a curated
// overlay entry (alias, fold, or none) wins outright over the name
// heuristic, since curation exists precisely to override or fill in what
// the heuristic gets wrong or cannot reach.
func classifyRow(tf string, index map[string]string, cfnSet map[string]bool, ov Overlay) (Row, error) {
	row := Row{TFType: tf}

	if cfn, ok := ov.Aliases[tf]; ok {
		if !cfnSet[cfn] {
			return Row{}, fmt.Errorf("overlay alias %s -> %s: %s is not in the current CFN roster (stale overlay entry)", tf, cfn, cfn)
		}
		row.Via = viaAlias
		row.CFNType = &cfn
		return row, nil
	}
	if parent, ok := ov.Folds[tf]; ok {
		if !cfnSet[parent] {
			return Row{}, fmt.Errorf("overlay fold %s -> %s: %s is not in the current CFN roster (stale overlay entry)", tf, parent, parent)
		}
		row.Via = viaFold
		row.FoldParent = &parent
		return row, nil
	}
	if note, ok := ov.Nones[tf]; ok {
		row.Via = viaNone
		row.Note = &note
		return row, nil
	}
	if cfn, ok := index[tf]; ok {
		row.Via = viaName
		row.CFNType = &cfn
		return row, nil
	}
	row.Via = viaNone
	note := "no CFN counterpart found by name or curated overlay"
	row.Note = &note
	return row, nil
}

// marshal renders the mapping deterministically: sorted rows, two-space
// indent, trailing newline, no HTML escaping - the same shape survey.json's
// and registry.json's marshal use.
func (m Mapping) marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
