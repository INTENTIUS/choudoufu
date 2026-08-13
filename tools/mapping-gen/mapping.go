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

// The via vocabulary: nothing outside these five tokens appears in a row's
// via column. viaServiceAlias is issue #43's follow-up heuristic v2
// (servicealias.go): a service-scoped resource-name match the plain name
// heuristic's single service+resource candidate cannot reach.
const (
	viaName         = "name"
	viaAlias        = "alias"
	viaFold         = "fold"
	viaNone         = "none"
	viaServiceAlias = "service-alias"
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

	// Counts are the roster-wide headline totals: mapped (via name, alias,
	// or service-alias) is the number issue #40's 60-75% band gets
	// replaced with.
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

// NeedsAlias is one TF type the service-alias heuristic (servicealias.go)
// matched against more than one CFN type within its aliased service(s): an
// ambiguous hit, deliberately never turned into a row (per the heuristic's
// own safety rule - see servicealias.go's package comment), but worth
// surfacing so a human can turn one of the candidates into an explicit
// overlay alias. main.go prints these to stderr; nothing reads them back.
type NeedsAlias struct {
	TFType     string
	Candidates []string // sorted CFN types, len >= 2
}

// buildMapping joins the TF roster against the CFN roster: the overlay's
// aliases, folds and nones win outright (curated, exact, and validated
// against the current CFN roster), the name heuristic tries next for
// anything the overlay does not cover, the service-alias heuristic tries
// next for anything still unclaimed, and anything still unclaimed after
// that is via:none with a generic note.
func buildMapping(tfTypes, cfnTypes []string, ov Overlay) (Mapping, []NeedsAlias, error) {
	index, err := buildNameIndex(cfnTypes)
	if err != nil {
		return Mapping{}, nil, err
	}

	cfnSet := make(map[string]bool, len(cfnTypes))
	for _, t := range cfnTypes {
		cfnSet[t] = true
	}

	sorted := append([]string(nil), tfTypes...)
	sort.Strings(sorted)

	svcCache := serviceIndexCache{}
	var needsAlias []NeedsAlias

	m := Mapping{GeneratedBy: "tools/mapping-gen (go run ./tools/mapping-gen)"}
	for _, tf := range sorted {
		row, ambiguous, err := classifyRow(tf, index, cfnSet, ov, cfnTypes, svcCache)
		if err != nil {
			return Mapping{}, nil, err
		}
		if len(ambiguous) > 0 {
			needsAlias = append(needsAlias, NeedsAlias{TFType: tf, Candidates: ambiguous})
		}
		switch row.Via {
		case viaName, viaAlias, viaServiceAlias:
			m.Counts.Mapped++
		case viaFold:
			m.Counts.Fold++
		case viaNone:
			m.Counts.None++
		}
		m.Counts.Types++
		m.Rows = append(m.Rows, row)
	}
	return m, needsAlias, nil
}

// classifyRow decides one TF type's row, in priority order: a curated
// overlay entry (alias, fold, or none) wins outright over both heuristics,
// since curation exists precisely to override or fill in what a heuristic
// gets wrong or cannot reach; the plain name heuristic wins next over the
// service-alias heuristic, since an unscoped exact match needs no service
// hint at all. When the service-alias heuristic finds more than one CFN
// type within its aliased service, the row still falls through to via:none
// (an ambiguous guess is not a mapping), but the candidates come back as
// this call's second return value for buildMapping to collect.
func classifyRow(tf string, index map[string]string, cfnSet map[string]bool, ov Overlay, cfnTypes []string, svcCache serviceIndexCache) (Row, []string, error) {
	row := Row{TFType: tf}

	if cfn, ok := ov.Aliases[tf]; ok {
		if !cfnSet[cfn] {
			return Row{}, nil, fmt.Errorf("overlay alias %s -> %s: %s is not in the current CFN roster (stale overlay entry)", tf, cfn, cfn)
		}
		row.Via = viaAlias
		row.CFNType = &cfn
		return row, nil, nil
	}
	if parent, ok := ov.Folds[tf]; ok {
		if !cfnSet[parent] {
			return Row{}, nil, fmt.Errorf("overlay fold %s -> %s: %s is not in the current CFN roster (stale overlay entry)", tf, parent, parent)
		}
		row.Via = viaFold
		row.FoldParent = &parent
		return row, nil, nil
	}
	if note, ok := ov.Nones[tf]; ok {
		row.Via = viaNone
		row.Note = &note
		return row, nil, nil
	}
	if cfn, ok := index[tf]; ok {
		row.Via = viaName
		row.CFNType = &cfn
		return row, nil, nil
	}

	unexplainedNote := "no CFN counterpart found by name or curated overlay"
	if hits := serviceAliasCandidates(tf, cfnTypes, ov.ServiceAliases, svcCache); len(hits) > 0 {
		candidates := make([]string, 0, len(hits))
		for c := range hits {
			candidates = append(candidates, c)
		}
		sort.Strings(candidates)
		if len(candidates) == 1 {
			cfn := candidates[0]
			row.Via = viaServiceAlias
			row.CFNType = &cfn
			return row, nil, nil
		}
		row.Via = viaNone
		row.Note = &unexplainedNote
		return row, candidates, nil
	}

	row.Via = viaNone
	row.Note = &unexplainedNote
	return row, nil, nil
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
