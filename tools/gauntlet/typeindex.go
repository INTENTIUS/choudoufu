// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TypeIndexPath is issue #435's per-estate exercised-type index, read here
// as input rather than written: tools/estate-types owns generating it.
const TypeIndexPath = "live/estate-types.json"

// TypeIndex maps an estate name to the set of resource types its crossing
// exercises, per live/estate-types.json.
type TypeIndex map[string]map[string]bool

// LoadTypeIndex reads live/estate-types.json. A missing file is an empty
// index (every --types filter then matches nothing, which cmdNext reports
// rather than silently ignoring), so this never panics on a checkout that
// predates #435.
func LoadTypeIndex(root string) (TypeIndex, error) {
	b, err := os.ReadFile(filepath.Join(root, TypeIndexPath))
	if os.IsNotExist(err) {
		return TypeIndex{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Estates []struct {
			Name  string   `json:"name"`
			Types []string `json:"types"`
		} `json:"estates"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	idx := TypeIndex{}
	for _, e := range doc.Estates {
		set := make(map[string]bool, len(e.Types))
		for _, t := range e.Types {
			set[t] = true
		}
		idx[e.Name] = set
	}
	return idx, nil
}

// ParseTypes splits a comma-separated --types value into a sorted, deduped,
// whitespace-trimmed list. An empty or all-blank input yields nil, which
// callers read as "no filter requested".
func ParseTypes(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// FilterByTypes keeps only the units whose estate exercises at least one of
// types, per idx, and returns them in the order they were given.
//
// Stale-pin units (Stage == StageStalePin) are never filtered out here,
// deliberately: staleness is about the emulator pin, not about which
// resource types an estate touches, and a repin still has to queue every
// stale-clear estate regardless of --types (live/GAUNTLET.md, "The stale-pin
// rule stays completely untouched"). --types is an additional filter on top
// of whatever NextUnits already returned, never a replacement for that rule.
//
// An empty types list is "no filter": every unit passes through unchanged.
func FilterByTypes(units []Unit, idx TypeIndex, types []string) []Unit {
	if len(types) == 0 {
		return units
	}
	want := make(map[string]bool, len(types))
	for _, t := range types {
		want[t] = true
	}
	var out []Unit
	for _, u := range units {
		if u.Stage == StageStalePin {
			out = append(out, u)
			continue
		}
		exercised := idx[u.Estate]
		matched := false
		for t := range want {
			if exercised[t] {
				matched = true
				break
			}
		}
		if matched {
			out = append(out, u)
		}
	}
	return out
}
