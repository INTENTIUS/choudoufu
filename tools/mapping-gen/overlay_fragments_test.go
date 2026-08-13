// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOverlayFragmentsMergeAndRefuseDuplicates pins overlay.d's contract:
// fragments add entries, and a key defined twice anywhere is refused, so
// parallel family sweeps cannot silently shadow each other or the base.
func TestOverlayFragmentsMergeAndRefuseDuplicates(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(base, []byte(`{"aliases":{"aws_x_thing":"AWS::X::Thing"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fragDir := filepath.Join(dir, "overlay.d")
	if err := os.Mkdir(fragDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fragDir, "sweep-a.json"), []byte(`{"tf_only":{"aws_y_waiter":"a waiter"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ov, err := loadOverlayUnchecked(base)
	if err != nil {
		t.Fatalf("merging a disjoint fragment: %v", err)
	}
	if ov.Aliases["aws_x_thing"] == "" || ov.TFOnly["aws_y_waiter"] == "" {
		t.Fatalf("merged overlay is missing base or fragment entries: %+v", ov)
	}

	if err := os.WriteFile(filepath.Join(fragDir, "sweep-b.json"), []byte(`{"aliases":{"aws_x_thing":"AWS::X::Other"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOverlayUnchecked(base); err == nil || !strings.Contains(err.Error(), "already defined") {
		t.Fatalf("duplicate key across base and fragment was not refused: %v", err)
	}
}

// TestHeuristicOverridesMergeAcrossFragments pins the same fragment-merge
// contract as TestOverlayFragmentsMergeAndRefuseDuplicates above, for issue
// #58's heuristic_overrides table specifically: one fragment can add an
// entry the base does not have, and the same key defined twice - whether in
// two fragments or base-and-fragment - is refused exactly like every other
// table.
func TestHeuristicOverridesMergeAcrossFragments(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(base, []byte(`{"heuristic_overrides":{"aws_x_thing":"the heuristic hit is wrong"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fragDir := filepath.Join(dir, "overlay.d")
	if err := os.Mkdir(fragDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fragDir, "sweep-a.json"), []byte(`{"heuristic_overrides":{"aws_y_thing":"also wrong"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ov, err := loadOverlayUnchecked(base)
	if err != nil {
		t.Fatalf("merging a disjoint fragment: %v", err)
	}
	if ov.HeuristicOverrides["aws_x_thing"] == "" || ov.HeuristicOverrides["aws_y_thing"] == "" {
		t.Fatalf("merged overlay is missing base or fragment heuristic_overrides entries: %+v", ov)
	}

	if err := os.WriteFile(filepath.Join(fragDir, "sweep-b.json"), []byte(`{"heuristic_overrides":{"aws_x_thing":"a second, conflicting reason"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOverlayUnchecked(base); err == nil || !strings.Contains(err.Error(), "already defined") {
		t.Fatalf("duplicate heuristic_overrides key across base and fragment was not refused: %v", err)
	}
}

// TestHeuristicOverridesDisjointness pins overlay.go's HeuristicOverrides
// disjointness rule (issue #58): the table may not share a key with
// Aliases or Folds (both already assert the correct answer outright, so an
// override alongside either is a self-contradiction), but sharing a key
// with TFOnly, CFNUnmodeled or Nones is expected and must validate cleanly
// - those three assert there is no CFN counterpart at all, exactly what an
// overridden heuristic hit usually resolves to. validateOverlay needs no
// TF/CFN roster (that check is TestOverlayTwoWayStaleness's job against the
// real committed files), so this test calls it directly against synthetic
// overlays.
func TestHeuristicOverridesDisjointness(t *testing.T) {
	t.Run("overlap with aliases is refused", func(t *testing.T) {
		ov := Overlay{
			Aliases:            map[string]string{"aws_x_thing": "AWS::X::Thing"},
			HeuristicOverrides: map[string]string{"aws_x_thing": "the heuristic hit is wrong"},
		}
		if _, err := validateOverlay(ov, "overlay.json"); err == nil || !strings.Contains(err.Error(), "heuristic_overrides and aliases") {
			t.Fatalf("expected a heuristic_overrides/aliases contradiction error, got: %v", err)
		}
	})

	t.Run("overlap with folds is refused", func(t *testing.T) {
		ov := Overlay{
			Folds:              map[string]string{"aws_x_thing": "AWS::X::Parent"},
			HeuristicOverrides: map[string]string{"aws_x_thing": "the heuristic hit is wrong"},
		}
		if _, err := validateOverlay(ov, "overlay.json"); err == nil || !strings.Contains(err.Error(), "heuristic_overrides and folds") {
			t.Fatalf("expected a heuristic_overrides/folds contradiction error, got: %v", err)
		}
	})

	t.Run("overlap with cfn_unmodeled validates cleanly", func(t *testing.T) {
		ov := Overlay{
			CFNUnmodeled:       map[string]string{"aws_x_thing": "no CFN model at all"},
			HeuristicOverrides: map[string]string{"aws_x_thing": "the heuristic hit is wrong"},
		}
		if _, err := validateOverlay(ov, "overlay.json"); err != nil {
			t.Fatalf("heuristic_overrides coexisting with cfn_unmodeled should validate cleanly, got: %v", err)
		}
	})

	t.Run("overlap with tf_only validates cleanly", func(t *testing.T) {
		ov := Overlay{
			TFOnly:             map[string]string{"aws_x_thing": "a provider-side action, not a resource"},
			HeuristicOverrides: map[string]string{"aws_x_thing": "the heuristic hit is wrong"},
		}
		if _, err := validateOverlay(ov, "overlay.json"); err != nil {
			t.Fatalf("heuristic_overrides coexisting with tf_only should validate cleanly, got: %v", err)
		}
	})

	t.Run("overlap with nones validates cleanly", func(t *testing.T) {
		ov := Overlay{
			Nones:              map[string]string{"aws_x_thing": "no counterpart, no further taxonomy yet"},
			HeuristicOverrides: map[string]string{"aws_x_thing": "the heuristic hit is wrong"},
		}
		if _, err := validateOverlay(ov, "overlay.json"); err != nil {
			t.Fatalf("heuristic_overrides coexisting with nones should validate cleanly, got: %v", err)
		}
	})

	t.Run("empty reason is refused", func(t *testing.T) {
		ov := Overlay{HeuristicOverrides: map[string]string{"aws_x_thing": ""}}
		if _, err := validateOverlay(ov, "overlay.json"); err == nil || !strings.Contains(err.Error(), "empty value") {
			t.Fatalf("expected an empty-value error, got: %v", err)
		}
	})
}

// loadOverlayUnchecked is loadOverlay minus the roster-dependent
// validation, which the synthetic fixture types here would fail.
func loadOverlayUnchecked(path string) (Overlay, error) {
	ov, err := decodeOverlay(path)
	if err != nil {
		return Overlay{}, err
	}
	fragDir := filepath.Join(filepath.Dir(path), "overlay.d")
	entries, err := os.ReadDir(fragDir)
	if err != nil {
		return Overlay{}, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		frag, err := decodeOverlay(filepath.Join(fragDir, e.Name()))
		if err != nil {
			return Overlay{}, err
		}
		if err := mergeOverlay(&ov, frag, filepath.Join(fragDir, e.Name())); err != nil {
			return Overlay{}, err
		}
	}
	return ov, nil
}
