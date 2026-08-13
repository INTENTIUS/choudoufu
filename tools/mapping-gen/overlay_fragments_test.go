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
