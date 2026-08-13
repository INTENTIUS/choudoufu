// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package main (former2.go): the second of issue #52's two authoritative
// sources, iann0036/former2 - a community-maintained CloudFormation
// generator that, per resource type it supports, already carries both the
// CFN type it emits and the Terraform type the same live object would
// import as. That per-resource pairing is exactly a mapping-gen row, from
// someone else's independent read of both APIs.
//
// former2's per-resource database is not the single js/mappings.js file
// issue #52 names - that file (checked directly against the pinned commit)
// is render-time helper code with no CFN/TF pairs in it at all. The pairs
// live one JS object literal per resource, spread across the ~150 files
// under js/services/*.js, each pushed onto former2's own tracked_resources
// array in the shape:
//
//	tracked_resources.push({
//	    ...
//	    'type': 'AWS::EC2::Instance',
//	    'terraformType': 'aws_instance',
//	    ...
//	});
//
// extractFormer2Rows reads that shape tolerantly rather than evaluating the
// JavaScript: former2 is community data (issue #52's own framing - "a wrong
// entry maps a wrong resource"), and a regex over a well-worn literal
// pattern that degrades to "found nothing" on a shape it does not recognize
// is a safer failure mode for an unreviewed upstream than a JS engine
// embedding would be.
package main

import (
	"regexp"
	"sort"
)

// former2PushRE finds each tracked_resources.push({ call's opening brace;
// everything up to the next such call (or EOF) is that push's own object
// literal, searched independently for its 'type' and 'terraformType'
// fields.
var former2PushRE = regexp.MustCompile(`tracked_resources\.push\(\{`)

// former2TypeRE and former2TFTypeRE pull the two fields' string literals.
// Conservative on purpose: a push whose 'type' or 'terraformType' is built
// from a variable or template literal rather than a plain quoted string (a
// shape not seen in the pinned commit, but not a promise upstream makes to
// keep) simply yields no match rather than a wrong one.
var (
	former2TypeRE   = regexp.MustCompile(`'type'\s*:\s*'([^']+)'`)
	former2TFTypeRE = regexp.MustCompile(`'terraformType'\s*:\s*'([^']+)'`)
)

// Former2Row is one raw {CFN type, TF type} pair extracted from former2's
// per-service source, before any filtering against the live rosters.
type Former2Row struct {
	CFNType string `json:"cfn_type"`
	TFType  string `json:"tf_type"`
}

// extractFormer2Rows scans the concatenated text of former2's
// js/services/*.js files (concatenation order does not matter - every push
// is self-contained) for tracked_resources.push({...}) object literals
// carrying both a 'type' and a 'terraformType' field, and returns one row
// per such literal. A push with a 'terraformType' but no 'type' in the same
// literal (a resource former2 tracks for Terraform output but does not
// itself map to a CloudFormation type - not this join's concern) is
// skipped, not an error.
func extractFormer2Rows(text string) []Former2Row {
	bounds := former2PushRE.FindAllStringIndex(text, -1)
	var rows []Former2Row
	for i, b := range bounds {
		end := len(text)
		if i+1 < len(bounds) {
			end = bounds[i+1][0]
		}
		chunk := text[b[0]:end]
		tm := former2TypeRE.FindStringSubmatch(chunk)
		fm := former2TFTypeRE.FindStringSubmatch(chunk)
		if tm == nil || fm == nil {
			continue
		}
		rows = append(rows, Former2Row{CFNType: tm[1], TFType: fm[1]})
	}
	return rows
}

// Former2DropReason names why a raw extracted row never becomes a
// candidate mapping.
type Former2DropReason string

const (
	// DropCFNUnknown: the CFN type is not in the current registry roster
	// at all.
	DropCFNUnknown Former2DropReason = "cfn_type not in live/registry.json"
	// DropCFNNoPrimaryIdentifier: the CFN type exists but the registry
	// carries no primaryIdentifier for it - issue #52's own eyeball-first
	// guard, since a type with no primaryIdentifier is not admittable
	// through the registry-backed path regardless of what former2 says.
	DropCFNNoPrimaryIdentifier Former2DropReason = "cfn_type has no primary_identifier in live/registry.json"
	// DropTFUnknown: the TF type is not in the current provider roster.
	DropTFUnknown Former2DropReason = "tf_type not in live/survey-full.json"
	// DropSelfContradiction: former2's own source disagrees with itself -
	// the same TF type pushed with more than one distinct CFN type across
	// different resources/files. Neither answer is trustworthy without a
	// human picking one, so both are dropped rather than either winning by
	// map-iteration accident.
	DropSelfContradiction Former2DropReason = "former2 itself pairs this tf_type with more than one distinct cfn_type"
)

// Former2Drop is one raw row excluded from the usable set, with why.
type Former2Drop struct {
	Row    Former2Row
	Reason Former2DropReason
}

// filterFormer2Rows validates every raw extracted row against the two live
// rosters (issue #52: "any former2 row whose CFN type lacks a
// primaryIdentifier in live/registry.json or whose TF type is not in
// live/survey-full.json gets dropped with a count reported") and then
// against former2's own internal consistency, returning the usable
// TFType->CFNType map plus every drop, in the order the checks ran.
func filterFormer2Rows(rows []Former2Row, cfnWithPrimaryID map[string]bool, cfnKnown map[string]bool, tfKnown map[string]bool) (usable map[string]string, drops []Former2Drop) {
	var survivors []Former2Row
	for _, r := range rows {
		switch {
		case !cfnKnown[r.CFNType]:
			drops = append(drops, Former2Drop{r, DropCFNUnknown})
		case !cfnWithPrimaryID[r.CFNType]:
			drops = append(drops, Former2Drop{r, DropCFNNoPrimaryIdentifier})
		case !tfKnown[r.TFType]:
			drops = append(drops, Former2Drop{r, DropTFUnknown})
		default:
			survivors = append(survivors, r)
		}
	}

	byTF := map[string]map[string]bool{}
	for _, r := range survivors {
		if byTF[r.TFType] == nil {
			byTF[r.TFType] = map[string]bool{}
		}
		byTF[r.TFType][r.CFNType] = true
	}

	usable = map[string]string{}
	for _, r := range survivors {
		if len(byTF[r.TFType]) > 1 {
			drops = append(drops, Former2Drop{r, DropSelfContradiction})
			continue
		}
		usable[r.TFType] = r.CFNType
	}

	sort.Slice(drops, func(i, j int) bool {
		if drops[i].Row.TFType != drops[j].Row.TFType {
			return drops[i].Row.TFType < drops[j].Row.TFType
		}
		return drops[i].Row.CFNType < drops[j].Row.CFNType
	})
	return usable, drops
}

// Former2Contradiction is one TF type former2 pairs with a CFN type that
// disagrees with what the rest of this tool's heuristics already mapped it
// to - issue #52's "rows where it CONTRADICTS an existing mapped row"
// case. Neither side is preferred silently; see
// TestFormer2ContradictionsAcknowledged in mapping_gen_test.go.
type Former2Contradiction struct {
	TFType     string `json:"tf_type"`
	MappedCFN  string `json:"mapped_cfn_type"`
	MappedVia  string `json:"mapped_via"`
	Former2CFN string `json:"former2_cfn_type"`
}

// former2Contradictions finds every usable former2 row whose CFN type
// disagrees with a row this tool's own heuristics (name, alias,
// service-alias, fold) already resolved that TF type to.
func former2Contradictions(usable map[string]string, existing map[string]Row) []Former2Contradiction {
	out := []Former2Contradiction{}
	for tf, f2cfn := range usable {
		row, ok := existing[tf]
		if !ok {
			continue
		}
		var mappedCFN string
		switch row.Via {
		case viaName, viaAlias, viaServiceAlias:
			if row.CFNType != nil {
				mappedCFN = *row.CFNType
			}
		case viaFold:
			if row.FoldParent != nil {
				mappedCFN = *row.FoldParent
			}
		default:
			continue
		}
		if mappedCFN != "" && mappedCFN != f2cfn {
			out = append(out, Former2Contradiction{TFType: tf, MappedCFN: mappedCFN, MappedVia: row.Via, Former2CFN: f2cfn})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TFType < out[j].TFType })
	return out
}
