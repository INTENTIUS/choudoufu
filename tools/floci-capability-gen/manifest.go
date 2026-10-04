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
	"strings"
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
	GeneratedBy string `json:"generated_by"`
	// Image is the floci ref the manifest describes: live/floci-image's
	// content at the last -mode=prune. live/flociimage_test.go's
	// flociImageFields registers it, so a manifest that has not caught up
	// with a moved pin fails the same guard every other measured artifact
	// under live/ answers to (#697).
	Image  string          `json:"image,omitempty"`
	Images []imageArtifact `json:"images"`
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

// replaceMechanism drops every existing row under this image whose
// Mechanism equals mechanism and appends rows in their place - the merge
// rule that keeps -mode=cloudcontrol's (or -mode=tagging's) own regenerated
// rows from duplicating or stranding a stale row for a type this run no
// longer checked (e.g. a type the admission table dropped since the last
// probe, or a tagging recipe removed from tagging.go). Rows under every
// other mechanism (mechanism="" today - the create/read path, still
// hand-curated) are left exactly as they were.
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

// pruneToPin drops every image entry except the one for pinRef's digest and
// records pinRef as the manifest's Image (#697).
//
// The manifest used to keep every digest ever probed - 58 of them, 23MB,
// embedded into package residue - while every reader in the tree looks up
// exactly one: the digest live/floci-image pins (FLOCI_IMAGE can name
// another, and an absent digest already reads as "not yet investigated").
// The history is emulator description, which is lex00/floci's to keep; the
// committed manifest is the pinned image's entry and nothing else.
//
// It refuses rather than writes when the pinned digest has no entry: an
// empty manifest would turn every capability gate into "not yet
// investigated" without anything saying so. Probe the new digest first,
// then prune.
func (a *manifestArtifact) pruneToPin(pinRef string) error {
	_, digest, ok := strings.Cut(pinRef, "@")
	if !ok || !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("live/floci-image is %q, which pins no @sha256 digest; the manifest is keyed by digest", pinRef)
	}
	var kept []imageArtifact
	for _, img := range a.Images {
		if img.Digest == digest {
			kept = append(kept, img)
		}
	}
	if len(kept) == 0 {
		return fmt.Errorf("the manifest has no entry for the pinned digest %s; probe it first "+
			"(go run ./tools/floci-capability-gen -endpoint ... -image %s), then prune", digest, pinRef)
	}
	a.Images = kept
	a.Image = pinRef
	return nil
}
