// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// generatedByLine is written to every regenerated manifest's own
// generated_by field - live/flocicap.go's embed reads this artifact back
// without depending on this field's exact text, so it is free to describe
// the tool.
const generatedByLine = "tools/floci-capability-gen (go run ./tools/floci-capability-gen); see its package doc for the probe/merge workflow"

// These row shapes mirror live/flocicap.go's own unexported
// flociServiceRow/flociTypeRow/flociImageArtifact/flociCapabilitiesArtifact
// exactly, field for field - re-declared here rather than imported, the
// same duplication every other tools/*-gen artifact writer in this repo
// carries for its own committed JSON shape (e.g. tools/registry-gen's
// RegistryArtifact vs. internal/live/registry's registryArtifact): the
// reading side's copy is unexported by design, so nothing outside
// live/flocicap.go can write a manifest row that skips its own status
// vocabulary check.
type serviceRow struct {
	Service  string `json:"service"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
	Source   string `json:"source"`
}

type typeRow struct {
	Type      string `json:"type"`
	Mechanism string `json:"mechanism,omitempty"`
	Status    string `json:"status"`
	Evidence  string `json:"evidence"`
	Source    string `json:"source"`
}

type imageArtifact struct {
	Digest   string       `json:"digest"`
	Ref      string       `json:"ref"`
	Services []serviceRow `json:"services"`
	Types    []typeRow    `json:"types"`
}

type manifestArtifact struct {
	GeneratedBy string          `json:"generated_by"`
	Images      []imageArtifact `json:"images"`
}

// loadManifest reads the existing artifact at path, or returns an empty one
// when the file does not exist yet - a first-ever run has nothing to merge
// into.
func loadManifest(path string) (*manifestArtifact, error) {
	data, err := os.ReadFile(path) //nolint:gosec // fixed path inside the checkout, or a caller-chosen -out
	if err != nil {
		if os.IsNotExist(err) {
			return &manifestArtifact{}, nil
		}
		return nil, err
	}
	var art manifestArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &art, nil
}

// imageEntry returns digest's existing entry, or a zero one carrying just
// the digest if the manifest has none yet - never written back until
// setImageEntry is called, so a probe that errors out before that leaves
// the loaded manifest (and therefore the file, since run() only writes
// after every requested probe succeeds) untouched.
func (a *manifestArtifact) imageEntry(digest string) imageArtifact {
	for _, img := range a.Images {
		if img.Digest == digest {
			return img
		}
	}
	return imageArtifact{Digest: digest}
}

// setImageEntry replaces digest's entry (or appends one), then sorts the
// image list by digest so a re-run's diff is never just reordering.
func (a *manifestArtifact) setImageEntry(digest string, img imageArtifact) {
	img.Digest = digest
	for i, existing := range a.Images {
		if existing.Digest == digest {
			a.Images[i] = img
			a.sortImages()
			return
		}
	}
	a.Images = append(a.Images, img)
	a.sortImages()
}

func (a *manifestArtifact) sortImages() {
	sort.Slice(a.Images, func(i, j int) bool { return a.Images[i].Digest < a.Images[j].Digest })
}

// allKnownServices is every service id this manifest already carries a row
// for, at any digest - -mode=services' default watchlist, so a service a
// past investigation found (and recorded, by hand or by an earlier probe)
// is automatically re-checked against every image bumped after, with no
// separate roster to keep in sync.
func (a *manifestArtifact) allKnownServices() map[string]bool {
	out := map[string]bool{}
	for _, img := range a.Images {
		for _, row := range img.Services {
			out[row.Service] = true
		}
	}
	return out
}

// replaceMechanism drops every existing row under this image whose
// Mechanism equals mechanism and appends rows in their place - the merge
// rule that keeps -mode=cloudcontrol's own regenerated rows from
// duplicating or stranding a stale row for a type this run no longer
// checked (e.g. a type the admission table dropped since the last probe).
// Rows under every other mechanism (mechanism="" and "tagging-sweep" today,
// both hand-curated) are left exactly as they were.
func (img *imageArtifact) replaceMechanism(mechanism string, rows []typeRow) {
	var kept []typeRow
	for _, row := range img.Types {
		if row.Mechanism != mechanism {
			kept = append(kept, row)
		}
	}
	kept = append(kept, rows...)
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Type != kept[j].Type {
			return kept[i].Type < kept[j].Type
		}
		return kept[i].Mechanism < kept[j].Mechanism
	})
	img.Types = kept
}

func writeManifest(path string, art *manifestArtifact) error {
	art.GeneratedBy = generatedByLine
	art.sortImages()
	for i := range art.Images {
		sort.Slice(art.Images[i].Services, func(a, b int) bool {
			return art.Images[i].Services[a].Service < art.Images[i].Services[b].Service
		})
	}

	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
