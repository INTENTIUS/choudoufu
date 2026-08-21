// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestPath is the estates manifest, relative to the repository root.
const ManifestPath = "live/gauntlet/estates.json"

// Sets. Core is the pinned population that can reach 100%; every estate,
// core or not, is in the "all" set the second bar reads.
const (
	SetCore    = "core"
	SetGrowing = "growing"
)

// Lanes say where an estate came from. The value is informational except
// that a core estate must come from one of CoreLanes.
var KnownLanes = []string{
	"terraform-popular",    // terraform-aws-modules examples and similar, pinned by tag
	"opentofu-native",      // projects describing themselves as built for OpenTofu
	"reference",            // hand-written reference shapes kept in this repository
	"published-deployment", // an organisation's own published root module
}

// CoreLanes are the lanes a core estate may come from. The selection rule
// (live/GAUNTLET.md, "The core set") is: the most-downloaded
// terraform-aws-modules examples, the OpenTofu-native projects already
// crossed, and the reference shapes; anything else is growing-set only.
var CoreLanes = []string{"terraform-popular", "opentofu-native", "reference"}

// Estate is one manifest entry. Everything a contributor has to write to add
// an estate is here and nowhere else.
type Estate struct {
	// Name is the directory name under live/e2e/ and the estate's page slug.
	Name string `json:"name"`
	// Source is a one-line human description: repository, path, version.
	Source string `json:"source"`
	// URL is the repository the estate is fetched from. Empty only for
	// in-repo reference estates.
	URL string `json:"url,omitempty"`
	// Pin is the tag or commit the estate is fetched at. Empty only for
	// in-repo reference estates.
	Pin string `json:"pin,omitempty"`
	// Lane is one of KnownLanes.
	Lane string `json:"lane"`
	// Set is SetCore or SetGrowing. A core estate needs a Reason.
	Set string `json:"set"`
	// Reason says why this estate is in the core set. Required for core.
	Reason string `json:"reason,omitempty"`
	// Script is the crossing script, relative to the repository root.
	// Defaults to live/e2e/<name>/run.sh when empty.
	Script string `json:"script,omitempty"`
}

// ScriptPath returns the crossing script path relative to the repo root.
func (e Estate) ScriptPath() string {
	if e.Script != "" {
		return e.Script
	}
	return filepath.Join("live", "e2e", e.Name, "run.sh")
}

// Manifest is live/gauntlet/estates.json.
type Manifest struct {
	Comment []string `json:"_comment"`
	Estates []Estate `json:"estates"`
}

// ManifestComment is written at the top of the manifest so a reader who
// opens the file cold knows what it is and how to add to it.
var ManifestComment = []string{
	"The gauntlet's estates: the real OpenTofu and Terraform configurations",
	"choudoufu is measured against, each run through every active stage in",
	"tools/gauntlet/stages.go side by side with stock OpenTofu.",
	"",
	"To add one: `go run ./tools/gauntlet add <name> <url> <ref> -lane <lane>`",
	"writes an entry here and a script stub at live/e2e/<name>/run.sh; fill",
	"in the stub, run `go run ./tools/gauntlet run <name>`, and commit the",
	"entry, the script, and the regenerated artifact and docs. live/GAUNTLET.md",
	"is the full contract and is rendered from the same tool.",
	"",
	"set is core or growing. Core is the pinned population the first headline",
	"bar reads and can reach 100%; it needs a reason. Every estate is in the",
	"second bar. Keep the list sorted by name; `go run ./tools/gauntlet render`",
	"rewrites this file canonically and TestManifestIsCanonical holds it.",
}

// LoadManifest reads and validates the manifest.
func LoadManifest(root string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(root, ManifestPath))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate returns the first thing wrong with the manifest.
func (m *Manifest) Validate() error {
	seen := map[string]bool{}
	for _, e := range m.Estates {
		if e.Name == "" {
			return fmt.Errorf("%s: an estate has no name", ManifestPath)
		}
		if seen[e.Name] {
			return fmt.Errorf("%s: estate %q appears twice", ManifestPath, e.Name)
		}
		seen[e.Name] = true
		if strings.ContainsAny(e.Name, "/ \t") {
			return fmt.Errorf("%s: estate %q: name must be a bare directory name", ManifestPath, e.Name)
		}
		if e.Source == "" {
			return fmt.Errorf("%s: estate %q: source is required", ManifestPath, e.Name)
		}
		if !contains(KnownLanes, e.Lane) {
			return fmt.Errorf("%s: estate %q: lane %q is not one of %v", ManifestPath, e.Name, e.Lane, KnownLanes)
		}
		switch e.Set {
		case SetCore:
			if !contains(CoreLanes, e.Lane) {
				return fmt.Errorf("%s: estate %q: a core estate must come from one of %v, not %q", ManifestPath, e.Name, CoreLanes, e.Lane)
			}
			if strings.TrimSpace(e.Reason) == "" {
				return fmt.Errorf("%s: estate %q: a core estate needs a reason", ManifestPath, e.Name)
			}
		case SetGrowing:
		default:
			return fmt.Errorf("%s: estate %q: set must be %q or %q", ManifestPath, e.Name, SetCore, SetGrowing)
		}
		if e.Lane != "reference" && (e.URL == "" || e.Pin == "") {
			return fmt.Errorf("%s: estate %q: url and pin are required unless lane is reference", ManifestPath, e.Name)
		}
	}
	return nil
}

// Canonical returns the manifest encoded the one way the tool writes it:
// sorted by name, two-space indent, trailing newline.
func (m *Manifest) Canonical() ([]byte, error) {
	cp := *m
	cp.Comment = ManifestComment
	cp.Estates = append([]Estate(nil), m.Estates...)
	sort.Slice(cp.Estates, func(i, j int) bool { return cp.Estates[i].Name < cp.Estates[j].Name })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&cp); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SaveManifest writes the manifest canonically.
func SaveManifest(root string, m *Manifest) error {
	b, err := m.Canonical()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ManifestPath), b, 0o644)
}

// ByName returns the estate with that name.
func (m *Manifest) ByName(name string) (Estate, bool) {
	for _, e := range m.Estates {
		if e.Name == name {
			return e, true
		}
	}
	return Estate{}, false
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
