// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifestMissingFileIsEmpty(t *testing.T) {
	art, err := loadManifest(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("loadManifest on a missing file: %v", err)
	}
	if len(art.Images) != 0 {
		t.Errorf("expected an empty artifact, got %d images", len(art.Images))
	}
}

func TestSetImageEntryUpdatesInPlaceAndSorts(t *testing.T) {
	art := &manifestArtifact{}
	art.setImageEntry("sha256:bbb", imageArtifact{Ref: "repo@sha256:bbb"})
	art.setImageEntry("sha256:aaa", imageArtifact{Ref: "repo@sha256:aaa"})
	if len(art.Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(art.Images))
	}
	if art.Images[0].Digest != "sha256:aaa" || art.Images[1].Digest != "sha256:bbb" {
		t.Fatalf("images not sorted by digest: %+v", art.Images)
	}

	// Updating an existing digest replaces it in place rather than
	// appending a duplicate.
	art.setImageEntry("sha256:aaa", imageArtifact{Ref: "repo@sha256:aaa-updated"})
	if len(art.Images) != 2 {
		t.Fatalf("expected update to keep 2 images, got %d", len(art.Images))
	}
	if art.Images[0].Ref != "repo@sha256:aaa-updated" {
		t.Errorf("Ref = %q, want the updated value", art.Images[0].Ref)
	}
}

func TestReplaceMechanismKeepsOtherMechanismsUntouched(t *testing.T) {
	img := imageArtifact{
		Types: []typeRow{
			{Type: "aws_redshift_cluster", Mechanism: "", Status: "unimplemented"},
			{Type: "aws_iam_role", Mechanism: "tagging-sweep", Status: "unimplemented"},
			{Type: "aws_glue_registry", Mechanism: "cloudcontrol-list", Status: "unimplemented"},
			{Type: "aws_stale_type", Mechanism: "cloudcontrol-list", Status: "implemented"},
		},
	}

	img.replaceMechanism("cloudcontrol-list", []typeRow{
		{Type: "aws_glue_registry", Mechanism: "cloudcontrol-list", Status: "implemented"},
	})

	if len(img.Types) != 3 {
		t.Fatalf("expected 3 rows after replace (2 untouched + 1 fresh), got %d: %+v", len(img.Types), img.Types)
	}

	byKey := map[string]typeRow{}
	for _, row := range img.Types {
		byKey[row.Type+"/"+row.Mechanism] = row
	}

	if _, ok := byKey["aws_redshift_cluster/"]; !ok {
		t.Error("mechanism=\"\" row for aws_redshift_cluster was dropped, want it untouched")
	}
	if _, ok := byKey["aws_iam_role/tagging-sweep"]; !ok {
		t.Error("tagging-sweep row for aws_iam_role was dropped, want it untouched")
	}
	if row, ok := byKey["aws_glue_registry/cloudcontrol-list"]; !ok || row.Status != "implemented" {
		t.Errorf("aws_glue_registry/cloudcontrol-list = %+v, ok=%v, want status implemented (freshly probed)", row, ok)
	}
	if _, ok := byKey["aws_stale_type/cloudcontrol-list"]; ok {
		t.Error("aws_stale_type/cloudcontrol-list survived replaceMechanism; a type this sweep no longer checked must not linger")
	}
}

// TestWriteManifestRoundTrips writes an artifact and reads it back through
// loadManifest, checking the on-disk shape is exactly what live/flocicap.go
// expects to embed and parse (same field names, same nesting).
func TestWriteManifestRoundTrips(t *testing.T) {
	art := &manifestArtifact{Images: []imageArtifact{
		{
			Digest: "sha256:aaa",
			Ref:    "ghcr.io/lex00/floci@sha256:aaa",
			Services: []serviceRow{
				{Service: "networkmanager", Status: "unimplemented", Evidence: "absent", Source: "live probe"},
			},
			Types: []typeRow{
				{Type: "aws_redshift_cluster", Status: "unimplemented", Evidence: "routed to SQS", Source: "README"},
			},
		},
	}}

	path := filepath.Join(t.TempDir(), "floci-capabilities.json")
	if err := writeManifest(path, art); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // test-only temp file
	if err != nil {
		t.Fatalf("reading back %s: %v", path, err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("the written file is not valid JSON: %v", err)
	}
	if generic["generated_by"] == "" {
		t.Error("written manifest has no generated_by field")
	}
	images, ok := generic["images"].([]any)
	if !ok || len(images) != 1 {
		t.Fatalf("images = %v, want a one-element array", generic["images"])
	}
	first, _ := images[0].(map[string]any)
	if first["digest"] != "sha256:aaa" {
		t.Errorf("images[0].digest = %v, want sha256:aaa", first["digest"])
	}

	reloaded, err := loadManifest(path)
	if err != nil {
		t.Fatalf("loadManifest on the round-tripped file: %v", err)
	}
	if len(reloaded.Images) != 1 || reloaded.Images[0].Digest != "sha256:aaa" {
		t.Fatalf("reloaded artifact = %+v, want the one image back", reloaded)
	}
}
