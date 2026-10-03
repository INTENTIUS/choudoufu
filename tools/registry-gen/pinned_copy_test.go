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
	"testing"
)

// TestPinnedCopiesMatchRegistryJSON is issue #1222's first guard.
//
// pinned-types.json and PinnedSpec are hand-pasted projections of
// live/registry.json: the roster is its sorted types[].type_name list and
// the spec is its pin object. The two tests that read them
// (TestRealBundleMatchesPin, TestRegistryJSONMatchesRealBundle) compare them
// against the downloaded CloudFormation bundle and skip in CI, where the
// bundle is never cached. This one compares them against the committed file
// they are a projection of, so it needs no network and never skips.
//
// tools/admission-pipeline/detect.go reads pinned-types.json as the baseline
// for which CFN types a provider bump added or removed, so a stale roster
// fabricates that set with nothing else failing.
func TestPinnedCopiesMatchRegistryJSON(t *testing.T) {
	path := filepath.Join("..", "..", "live", "registry.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var reg struct {
		Pin struct {
			Digest    string `json:"digest"`
			Resources int    `json:"resources"`
			Accepted  string `json:"accepted"`
		} `json:"pin"`
		Types []struct {
			TypeName string `json:"type_name"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	if reg.Pin.Digest != PinnedSpec.Digest || reg.Pin.Resources != PinnedSpec.Resources || reg.Pin.Accepted != PinnedSpec.Accepted {
		t.Errorf("pinned_spec.go's PinnedSpec = {%s %d %s}, live/registry.json's pin = {%s %d %s}; "+
			"paste the registry's pin block into pinned_spec.go (or regenerate the registry against the pinned bundle)",
			PinnedSpec.Digest, PinnedSpec.Resources, PinnedSpec.Accepted,
			reg.Pin.Digest, reg.Pin.Resources, reg.Pin.Accepted)
	}

	want := make([]string, 0, len(reg.Types))
	for _, ty := range reg.Types {
		want = append(want, ty.TypeName)
	}
	sort.Strings(want)

	var got []string
	if err := json.Unmarshal(pinnedTypesJSON, &got); err != nil {
		t.Fatalf("parsing pinned-types.json: %v", err)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("pinned-types.json is not sorted; it is the sorted type_name list of live/registry.json")
	}

	inReg := map[string]bool{}
	for _, n := range want {
		inReg[n] = true
	}
	inPin := map[string]bool{}
	for _, n := range got {
		if inPin[n] {
			t.Errorf("pinned-types.json lists %s twice", n)
		}
		inPin[n] = true
		if !inReg[n] {
			t.Errorf("pinned-types.json lists %s, which live/registry.json does not have", n)
		}
	}
	for _, n := range want {
		if !inPin[n] {
			t.Errorf("live/registry.json has %s, which pinned-types.json does not list", n)
		}
	}
	if len(got) != len(want) {
		t.Errorf("pinned-types.json has %d names, live/registry.json has %d types", len(got), len(want))
	}
	if PinnedSpec.Resources != len(got) {
		t.Errorf("PinnedSpec.Resources = %d but pinned-types.json has %d names", PinnedSpec.Resources, len(got))
	}
}
