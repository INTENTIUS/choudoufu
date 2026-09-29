// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command substrates-gen writes live/substrates.json, GitHub issue #1588's
// capability matrix: one row per provider family registered in
// internal/live/substrate.All, built by asking each family's Substrate
// value its own questions rather than typing per-provider facts into this
// generator's control flow. The two columns Substrate has no method for
// (the fence, and anything about the harness beyond its pin file) are
// hand-kept lookups in this package, held to a completeness guard against
// substrate.All rather than left to drift.
//
//	go run ./tools/substrates-gen
//
// A second mode copies the committed live/substrates.json byte-for-byte to
// site/data/substrates.json, the same split live/smoke/claims.json's site
// copy uses:
//
//	go run ./tools/substrates-gen -render
//
// It reads only committed artifacts (live/readiness.json, live/floci-image,
// live/kind-node-image) and the in-process substrate package - no provider,
// no network, no other generator's process. Run twice with nothing else
// changed, it writes byte-identical output.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick tools/readiness-gen's repoRoot uses, so the tool runs from any
// directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/substrates-gen/build.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Build reads every committed input under root and asks internal/live/substrate.All
// its questions. It touches no network and no provider plugin.
func Build(root string) (Artifact, error) {
	rf, err := loadReadinessFacts(root)
	if err != nil {
		return Artifact{}, err
	}

	rows := make([]Row, 0, len(substrate.All))
	for _, s := range substrate.All {
		row, err := buildRow(root, s, rf)
		if err != nil {
			return Artifact{}, err
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Family < rows[j].Family })

	var flociPin string
	for _, r := range rows {
		if r.Family == "aws" {
			flociPin = r.Harness.Pin
		}
	}

	return Artifact{
		GeneratedBy: "go run ./tools/substrates-gen",
		FlociPin:    flociPin,
		Rows:        rows,
	}, nil
}

func buildRow(root string, s substrate.Substrate, rf readinessFacts) (Row, error) {
	family := s.Name()

	row := Row{
		Family:   family,
		Identity: identityFacts(s),
		Sweep:    sweepFacts(s.Sweep()),
	}

	for _, surf := range s.Surfaces() {
		w := s.Writes(surf)
		row.Surfaces = append(row.Surfaces, SurfaceRow{
			Surface:          string(surf),
			MarkerNoun:       s.MarkerNoun(surf),
			CarrierPhrase:    s.CarrierPhrase(surf),
			CarrierPaths:     pathStrings(s.CarrierPaths(surf)),
			CollidesOnKey:    s.CreateCollidesOnKey(surf),
			Writes:           WritesFacts{Create: string(w.Create), Adopt: string(w.Adopt), PostCreate: string(w.PostCreate)},
			PostCreateWriter: string(s.MarkerWriter(addrs.AbsProviderConfig{})),
		})
	}

	row.ControllerHeld = controllerHeldFacts(s)

	row.Untaggable = untaggableFor(family, rf)

	fence, err := fenceFor(family)
	if err != nil {
		return Row{}, err
	}
	row.Fence = fence

	harness, err := harnessFor(root, family)
	if err != nil {
		return Row{}, err
	}
	row.Harness = harness

	return row, nil
}

// identityFacts derives the identity axis from the two address answers
// Substrate already gives, so a third family reads correctly with no new
// wording added here.
func identityFacts(s substrate.Substrate) IdentityFacts {
	ca := s.CarriesAddress()
	aim := s.AddressInMarkers()
	desc := "no block address is carried on the object"
	switch {
	case ca && aim:
		desc = "the block address is a key of the marker map itself, beside tofu-estate"
	case ca && !aim:
		desc = "the block address is carried outside the marker map, in its own annotation"
	}
	return IdentityFacts{CarriesAddress: ca, AddressInMarkers: aim, Description: desc}
}

// sweepFacts names what a [substrate.Sweep] value means, keyed on the sweep
// kind rather than on which family holds it today.
func sweepFacts(kind substrate.Sweep) SweepFacts {
	desc := "no description recorded for this sweep kind; add one in tools/substrates-gen/build.go's sweepFacts"
	switch kind {
	case substrate.SweepTaggingIndex:
		desc = "runs through the configured provider itself; no separate client is built from the provider block"
	case substrate.SweepLabelList:
		desc = "a cluster client built from the provider block's own connection arguments (Substrate.NewSweeper)"
	}
	return SweepFacts{Kind: string(kind), Description: desc}
}

// pathStrings renders each cty.Path as a dotted/indexed string
// ("metadata[0].labels"), for the few shapes this repository's carrier
// paths actually take: attribute steps and integer index steps.
func pathStrings(paths []cty.Path) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, pathString(p))
	}
	return out
}

func pathString(p cty.Path) string {
	var b strings.Builder
	for _, step := range p {
		switch s := step.(type) {
		case cty.GetAttrStep:
			if b.Len() > 0 {
				b.WriteString(".")
			}
			b.WriteString(s.Name)
		case cty.IndexStep:
			if s.Key.Type() == cty.Number {
				f, _ := s.Key.AsBigFloat().Int64()
				fmt.Fprintf(&b, "[%d]", f)
			} else if s.Key.Type() == cty.String {
				fmt.Fprintf(&b, "[%q]", s.Key.AsString())
			} else {
				b.WriteString("[?]")
			}
		default:
			b.WriteString("?")
		}
	}
	return b.String()
}

// ---- Controller-held (GitHub issues #1604, #1606, #1706) ----

// controllerHeldFacts is the family's own [substrate.Substrate.HoldRecognition],
// read off the same facts its ControllerHeld answer reads, so a third
// family's column needs no entry here (GitHub issue #1706 retired the hand
// map keyed by family name).
func controllerHeldFacts(s substrate.Substrate) ControllerHeldFacts {
	rec := s.HoldRecognition()
	return ControllerHeldFacts{
		Recognized: len(rec.Keys) > 0,
		Mechanism:  rec.Mechanism,
		Keys:       append([]string(nil), rec.Keys...),
	}
}

// ---- Fence (hand-kept: the fence is never code here, #1118) ----

var fenceByFamily = map[string]string{
	"aws":        "IAM conditions on aws:ResourceTag, reads and writes, per principal (live/MARKERS.md).",
	"kubernetes": "One ValidatingAdmissionPolicy, writes only, enforced per principal through the cluster's authorizer.",
}

func fenceFor(family string) (string, error) {
	f, ok := fenceByFamily[family]
	if !ok {
		return "", fmt.Errorf("fence: no entry for family %q in fenceByFamily", family)
	}
	return f, nil
}

// ---- Harness ----

// harnessByFamily names each family's emulator and the single pin file the
// rest of the tree already reads for it (live/flociimage_test.go's
// pinnedDigest, live/kind_pin_test.go's live/kind-node-image); Notes is the
// one hand-kept sentence about the harness that the pin file's own content
// does not say.
var harnessByFamily = map[string]struct {
	Name, PinFile, Notes string
}{
	"aws": {
		Name:    "floci",
		PinFile: "live/floci-image",
		Notes:   "GHCR image pinned by digest; live/floci-capabilities.json probes per-AWS-service coverage against it.",
	},
	"kubernetes": {
		Name:    "kind",
		PinFile: "live/kind-node-image",
		Notes:   "a kind cluster on the pinned node image; no per-service capability probe like floci's exists for it.",
	},
}

func harnessFor(root, family string) (HarnessFacts, error) {
	h, ok := harnessByFamily[family]
	if !ok {
		return HarnessFacts{}, fmt.Errorf("harness: no entry for family %q in harnessByFamily", family)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(h.PinFile)))
	if err != nil {
		return HarnessFacts{}, fmt.Errorf("reading %s: %w", h.PinFile, err)
	}
	return HarnessFacts{
		Name:    h.Name,
		PinFile: h.PinFile,
		Pin:     strings.TrimSpace(string(data)),
		Notes:   h.Notes,
	}, nil
}

// ---- Untaggable types' tier (from the committed live/readiness.json) ----

type readinessType struct {
	Type  string `json:"type"`
	Tier  string `json:"tier"`
	Facts struct {
		Taggable     bool `json:"taggable"`
		LabelSurface bool `json:"label_surface"`
	} `json:"facts"`
}

type readinessArtifact struct {
	Types      []readinessType `json:"types"`
	Kubernetes struct {
		Types []readinessType `json:"types"`
	} `json:"kubernetes"`
}

type readinessFacts struct {
	awsTypes []readinessType
	k8sTypes []readinessType
}

func loadReadinessFacts(root string) (readinessFacts, error) {
	var art readinessArtifact
	if err := decodeJSON(root, ReadinessJSONRel, &art); err != nil {
		return readinessFacts{}, err
	}
	return readinessFacts{awsTypes: art.Types, k8sTypes: art.Kubernetes.Types}, nil
}

func decodeJSON(root, rel string, v any) error {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(root, filepath.FromSlash(rel))))
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decoding %s: %w", rel, err)
	}
	return nil
}

// untaggableFor tallies, per family, the admitted types whose schema
// carries none of the family's own marker surfaces. live/readiness.json
// already keeps this fact per family under a different name for each
// (facts.taggable for AWS, facts.label_surface for the four admitted
// Kubernetes types, per GitHub issue #1600's ruling that LabelSurface is
// exactly Kubernetes's tier-A test) - this reads that fact rather than
// asking a schema directly, so it stays a data join instead of a second
// classifier.
func untaggableFor(family string, rf readinessFacts) UntaggableFacts {
	switch family {
	case "aws":
		return tallyUntaggable(rf.awsTypes, func(t readinessType) bool { return !t.Facts.Taggable },
			"an admitted AWS type whose schema carries no settable tags map (live/readiness.json facts.taggable=false)")
	case "kubernetes":
		return tallyUntaggable(rf.k8sTypes, func(t readinessType) bool { return !t.Facts.LabelSurface },
			"an admitted Kubernetes type whose schema carries no metadata.labels block (live/readiness.json kubernetes.types facts.label_surface=false)")
	}
	return UntaggableFacts{Definition: "no definition recorded for family " + family, ByTier: map[string]int{}}
}

func tallyUntaggable(types []readinessType, untaggable func(readinessType) bool, definition string) UntaggableFacts {
	byTier := map[string]int{}
	count := 0
	for _, t := range types {
		if !untaggable(t) {
			continue
		}
		count++
		byTier[t.Tier]++
	}
	return UntaggableFacts{Definition: definition, Count: count, ByTier: byTier}
}
