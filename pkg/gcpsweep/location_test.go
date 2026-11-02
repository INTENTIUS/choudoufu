// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"strings"
	"testing"
)

// TestBlockWithNoLocationListsTheProviderRegion is GitHub issue #2273's
// ruling (2026-10-26): a block that names no location is listed in the
// provider block's region, as plunge already lists it, so both engines
// list one scope. A block that names its own location keeps it.
func TestBlockWithNoLocationListsTheProviderRegion(t *testing.T) {
	provider := ProviderScope{Project: "p1", Region: "europe-west4", Zone: "europe-west4-b"}
	for name, want := range map[string]string{
		"project":  "p1",
		"region":   "europe-west4",
		"location": "europe-west4",
		"zone":     "europe-west4-b",
		"key_ring": "",
	} {
		if got := provider.Default(name); got != want {
			t.Errorf("Default(%q) = %q, want %q", name, got, want)
		}
	}

	var tl TypeList
	for _, name := range CoveredTypes() {
		c, _ := Covered(name)
		if c.Derived && strings.Contains(c.Path, "{{location}}") && len(c.Placeholders()) == 2 {
			tl = c
			break
		}
	}
	if tl.Type == "" {
		t.Fatal("no derived covered type is listed under {{project}} and {{location}} alone")
	}
	scope := func(block map[string]string) ListScope {
		t.Helper()
		s, err := tl.Scope(func(name string) (string, bool) {
			if v, ok := block[name]; ok {
				return v, true
			}
			v := provider.Default(name)
			return v, v != ""
		})
		if err != nil {
			t.Fatalf("%s: %v", tl.Type, err)
		}
		return s
	}

	if got := scope(map[string]string{}); got.Location != "europe-west4" || !strings.Contains(got.Collection, "/locations/europe-west4/") {
		t.Errorf("%s with no location: scope %+v, want the provider region europe-west4", tl.Type, got)
	}
	if got := scope(map[string]string{"location": "us-east1"}); got.Location != "us-east1" || !strings.Contains(got.Collection, "/locations/us-east1/") {
		t.Errorf("%s with location us-east1: scope %+v, want its own location", tl.Type, got)
	}
}
