// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// azurermIDsPinnedVersion is the azurerm version the Azure lane is ruled
// on (#1122, 2026-10-19: "Pins for this lane: azurerm 5.10.0"). The id
// table must be generated from that version and no other; moving the pin
// is a ruling, and this constant is where it shows up in a diff.
const azurermIDsPinnedVersion = "5.10.0"

// TestAzurermIDsArtifactIsWhole reads live/azurerm-ids.json, the ARM id
// table tools/azurerm-id-gen generates from azurerm's id parsers (#1122,
// epic #1120), and checks what can be checked without the provider source:
// it is pinned to the lane's version and a commit, its rows are sorted and
// unique, every row has one of the four classes, a mapped row has
// components and an unmapped one a reason, and the headline counts are the
// rows' own. The regeneration check that needs the source is
// TestRegeneratingFromThePinReproducesTheArtifact in the generator.
func TestAzurermIDsArtifactIsWhole(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "live", "azurerm-ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Pin struct {
			Version      string `json:"version"`
			Commit       string `json:"commit"`
			SchemaSHA256 string `json:"schema_sha256"`
		} `json:"pin"`
		Counts map[string]int `json:"counts"`
		Types  []struct {
			Type          string            `json:"type"`
			Class         string            `json:"class"`
			TemplateClass string            `json:"template_class"`
			Components    []json.RawMessage `json:"components"`
			Reason        string            `json:"reason"`
		} `json:"types"`
	}
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	if a.Pin.Version != azurermIDsPinnedVersion {
		t.Errorf("live/azurerm-ids.json is generated from azurerm %q; the lane is pinned at %s", a.Pin.Version, azurermIDsPinnedVersion)
	}
	if len(a.Pin.Commit) != 40 || len(a.Pin.SchemaSHA256) != 64 {
		t.Errorf("pin %+v does not name a commit and a schema digest", a.Pin)
	}

	names := make([]string, 0, len(a.Types))
	counts := map[string]int{}
	for _, r := range a.Types {
		names = append(names, r.Type)
		counts[r.Class]++
		class := r.Class
		if class == "identity-schema" {
			counts["identity-schema/"+r.TemplateClass]++
			class = r.TemplateClass
		}
		switch class {
		case "template", "parent-id":
			if len(r.Components) == 0 || r.Reason != "" {
				t.Errorf("%s is %s but has %d components and reason %q", r.Type, class, len(r.Components), r.Reason)
			}
		case "unmapped":
			if r.Reason == "" || len(r.Components) != 0 {
				t.Errorf("%s is unmapped without a reason, or with components", r.Type)
			}
		default:
			t.Errorf("%s has class %q / template class %q", r.Type, r.Class, r.TemplateClass)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Error("live/azurerm-ids.json rows are not sorted by type")
	}
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			t.Errorf("%s appears twice", names[i])
		}
	}
	if len(counts) != len(a.Counts) {
		t.Errorf("counts %v, rows say %v", a.Counts, counts)
	}
	for k, n := range counts {
		if a.Counts[k] != n {
			t.Errorf("counts[%s] = %d, rows say %d", k, a.Counts[k], n)
		}
	}
}
