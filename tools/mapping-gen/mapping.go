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

// The via vocabulary: nothing outside these six tokens appears in a row's
// via column. viaServiceAlias is issue #43's follow-up heuristic v2
// (servicealias.go): a service-scoped resource-name match the plain name
// heuristic's single service+resource candidate cannot reach; its own
// service_aliases table is now generated from names_data.hcl rather than
// hand-curated (issue #52 - namesdata.go), with the overlay kept only for
// prefixes the source disagrees with or does not cover. viaFormer2 (issue
// #52 - former2.go) is a new, distinct via: a per-resource pairing from
// iann0036/former2's own independent CFN/TF join, used only for a TF type
// none of the other heuristics could resolve at all.
const (
	viaName         = "name"
	viaAlias        = "alias"
	viaFold         = "fold"
	viaNone         = "none"
	viaServiceAlias = "service-alias"
	viaFormer2      = "former2"
)

// unexplainedNoteText is the generic fallback note a TF type carries when it
// falls all the way through to via:none with no curated overlay entry (an
// explicit ov.Nones judgment) behind it - the only via:none shape former2
// is allowed to override, since an explicit hand "none" is a judgment
// (a waiter, a credential, ...) former2's guess does not get to second-guess
// (mapping_gen_test.go's own unexplainedNote constant holds the identical
// string for its assertions; not shared directly, since Go forbids two
// same-named package-level constants even in different files of the same
// package).
const unexplainedNoteText = "no CFN counterpart found by name or curated overlay"

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
	// service-alias, or former2) is the number issue #40's 60-75% band gets
	// replaced with.
	Counts MappingCounts `json:"counts"`

	// Former2Contradictions is issue #52's own header-level conflict
	// report: every TF type former2's per-resource pairing disagrees with
	// a row this tool's other sources already mapped, so a reader of the
	// artifact sees the conflict without cross-referencing former2-rows.json
	// by hand. Never silently resolved - see
	// TestFormer2ContradictionsAcknowledged in mapping_gen_test.go, the
	// test that fails until each entry here is either acknowledged in that
	// test's allowlist or fixed at the source.
	Former2Contradictions []Former2Contradiction `json:"former2_contradictions"`

	// Rows has one entry per TF type, sorted by tf_type.
	Rows []Row `json:"rows"`
}

// MappingCounts are the roster-wide totals.
type MappingCounts struct {
	Types  int `json:"types"`
	Mapped int `json:"mapped"`
	Fold   int `json:"fold"`
	None   int `json:"none"`

	// ByVia is the per-source breakdown of Mapped+Fold (every via except
	// "none"): issue #52's "report ... per-source counts" requirement.
	ByVia map[string]int `json:"by_via"`
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

// Sources bundles buildMapping's inputs beyond the two rosters: the
// hand-curated overlay plus issue #52's two generated/sourced tables, kept
// as a single parameter so a new source does not ripple a new positional
// argument through every call site.
type Sources struct {
	// Overlay is tools/mapping-gen/overlay.json: per-type aliases/folds/
	// nones, plus service_aliases entries kept only where GeneratedServiceAliases
	// disagrees with or does not cover a prefix (issue #52's "overlay as a
	// cache of ratified conflicts").
	Overlay Overlay

	// GeneratedServiceAliases is names_data.hcl's own join (namesdata.go):
	// TF prefix -> CFN service name(s), the base the overlay's own
	// ServiceAliases entries override per prefix.
	GeneratedServiceAliases map[string][]string

	// Former2 is iann0036/former2's per-resource TFType -> CFNType pairing
	// (former2.go), already filtered against both live rosters and
	// former2's own internal consistency (filterFormer2Rows) - every entry
	// here is a candidate via:former2 row.
	Former2 map[string]string
}

// mergedServiceAliases combines the generated table with the overlay's own
// remaining entries, the overlay winning per prefix - the "overlay as a
// cache of ratified conflicts" rule: a prefix the overlay names is either a
// genuine disagreement with the source or a prefix the source does not
// cover at all, and either way the hand entry is the one to trust.
func mergedServiceAliases(generated map[string][]string, overlay map[string][]string) map[string][]string {
	merged := make(map[string][]string, len(generated)+len(overlay))
	for prefix, svcs := range generated {
		merged[prefix] = svcs
	}
	for prefix, svcs := range overlay {
		merged[prefix] = svcs
	}
	return merged
}

// buildMapping joins the TF roster against the CFN roster: the overlay's
// aliases, folds and nones win outright (curated, exact, and validated
// against the current CFN roster), the name heuristic tries next for
// anything the overlay does not cover, the service-alias heuristic (its own
// table merged from names_data.hcl and the overlay's remaining conflicts)
// tries next for anything still unclaimed, former2's per-resource pairing
// tries next for anything STILL unclaimed by any of the above (never
// overriding an explicit overlay judgment - see classifyRow), and anything
// still unclaimed after all four is via:none with a generic note.
//
// former2 rows that instead CONTRADICT an already-mapped row (issue #52)
// never change that row's via/cfn_type - they are reported back as the
// second return value for the caller to surface and gate a test on, never
// silently preferred over the heuristic's own answer.
func buildMapping(tfTypes, cfnTypes []string, src Sources) (Mapping, []NeedsAlias, []Former2Contradiction, error) {
	index, err := buildNameIndex(cfnTypes)
	if err != nil {
		return Mapping{}, nil, nil, err
	}

	cfnSet := make(map[string]bool, len(cfnTypes))
	for _, t := range cfnTypes {
		cfnSet[t] = true
	}

	serviceAliases := mergedServiceAliases(src.GeneratedServiceAliases, src.Overlay.ServiceAliases)

	sorted := append([]string(nil), tfTypes...)
	sort.Strings(sorted)

	svcCache := serviceIndexCache{}
	var needsAlias []NeedsAlias
	rows := make(map[string]Row, len(sorted))

	m := Mapping{GeneratedBy: "tools/mapping-gen (go run ./tools/mapping-gen)", Counts: MappingCounts{ByVia: map[string]int{}}}
	for _, tf := range sorted {
		row, ambiguous, err := classifyRow(tf, index, cfnSet, src.Overlay, cfnTypes, serviceAliases, svcCache)
		if err != nil {
			return Mapping{}, nil, nil, err
		}
		if len(ambiguous) > 0 {
			needsAlias = append(needsAlias, NeedsAlias{TFType: tf, Candidates: ambiguous})
		}
		rows[tf] = row
	}

	// former2 contradictions are measured against the mapping as it stands
	// before former2 fills in anything of its own - a row former2 disagrees
	// with was already mapped by a different source, and applying former2
	// afterward must not make that disagreement invisible.
	contradictions := former2Contradictions(src.Former2, rows)
	m.Former2Contradictions = contradictions

	for _, tf := range sorted {
		row := rows[tf]
		if row.Via == viaNone && row.Note != nil && *row.Note == unexplainedNoteText {
			if cfn, ok := src.Former2[tf]; ok && cfnSet[cfn] {
				cfnCopy := cfn
				row = Row{TFType: tf, Via: viaFormer2, CFNType: &cfnCopy}
			}
		}
		switch row.Via {
		case viaName, viaAlias, viaServiceAlias, viaFormer2:
			m.Counts.Mapped++
		case viaFold:
			m.Counts.Fold++
		case viaNone:
			m.Counts.None++
		}
		if row.Via != viaNone {
			m.Counts.ByVia[row.Via]++
		}
		m.Counts.Types++
		m.Rows = append(m.Rows, row)
	}
	return m, needsAlias, contradictions, nil
}

// classifyRow decides one TF type's row, in priority order: a curated
// overlay entry (alias, fold, or none) wins outright over both heuristics,
// since curation exists precisely to override or fill in what a heuristic
// gets wrong or cannot reach; the plain name heuristic wins next over the
// service-alias heuristic, since an unscoped exact match needs no service
// hint at all. When the service-alias heuristic finds more than one CFN
// type within its aliased service, the row still falls through to via:none
// (an ambiguous guess is not a mapping), but the candidates come back as
// this call's second return value for buildMapping to collect. former2 is
// applied by the caller afterward, only to rows this function leaves at the
// generic unexplained via:none (never an explicit ov.Nones judgment) - see
// buildMapping.
func classifyRow(tf string, index map[string]string, cfnSet map[string]bool, ov Overlay, cfnTypes []string, serviceAliases map[string][]string, svcCache serviceIndexCache) (Row, []string, error) {
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

	unexplainedNote := unexplainedNoteText
	if hits := serviceAliasCandidates(tf, cfnTypes, serviceAliases, svcCache); len(hits) > 0 {
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
