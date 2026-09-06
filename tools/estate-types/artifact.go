// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ArtifactPath is where the generated index is committed, a sibling to
// live/gauntlet.json rather than a new field on it: gauntlet.json's schema
// is being extended by other work landing the same day this was written
// (issue #435's own brief), and a second top-level artifact carries zero
// risk of colliding with that.
const ArtifactPath = "live/estate-types.json"

const artifactSchema = 1

// Artifact is live/estate-types.json's shape.
type Artifact struct {
	Schema      int           `json:"schema"`
	GeneratedBy string        `json:"generated_by"`
	Method      string        `json:"method"`
	Totals      Totals        `json:"totals"`
	Estates     []estateTypes `json:"estates"`
}

// Totals is the board-wide roll-up the PR description for issue #435 quotes.
type Totals struct {
	// Estates is len(Estates); a sibling GAUNTLET.md-drift guard test) can
	// hold it equal to live/gauntlet.json's own estate count.
	Estates int `json:"estates"`

	// DistinctTypes is the size of the union of every estate's Types.
	DistinctTypes int `json:"distinct_types"`

	// TypesInNoCohort is DistinctTypes' subset that no estate-gen cohort
	// declares, read off the roster in internal/live/cohorts - see
	// cohort.go. -1 means the cross-reference was not attempted this run.
	TypesInNoCohort int `json:"types_in_no_cohort"`
}

// estateTypes is one estate's row.
type estateTypes struct {
	Name string `json:"name"`

	// Types is the distinct, sorted resource-type set this estate's
	// crossing exercises.
	Types []string `json:"types"`
	Count int      `json:"count"`

	// Sources says how Types was produced: "config" (internal/live/check.Load
	// over ConfigDirs), "script" (a text scan of the crossing script's own
	// heredoc'd root wiring - see estateSpec.ScanScript) or both.
	Sources []string `json:"sources"`

	// ConfigDirs are the repository-root-relative directories that actually
	// loaded (a subset of estateSpec.ConfigDirs when one was missing - see
	// Notes).
	ConfigDirs []string `json:"config_dirs,omitempty"`

	// UnresolvedModules is the sum of check.LoadResult.UnresolvedModules
	// across ConfigDirs: module calls check.Load could not read without
	// installing them. Non-zero bounds how complete Types can be trusted to
	// be - see live/GAUNTLET.md and internal/live/check/load.go.
	UnresolvedModules int `json:"unresolved_modules"`

	// Notes are non-fatal problems this run hit while scanning (a missing
	// .corpus directory, a load error, a missing run.sh) - never silent,
	// always visible in the artifact.
	Notes []string `json:"notes,omitempty"`
}

// Generate runs [scanEstate] over every entry in estateSpecs and returns the
// artifact. ctx bounds check.Load's own parsing pass; there is no network or
// docker anywhere in this path.
func Generate(ctx context.Context, root string) (Artifact, error) {
	art := Artifact{
		Schema:      artifactSchema,
		GeneratedBy: "go run ./tools/estate-types",
		Method: "static config parsing: internal/live/check.Load (the module-graph " +
			"loader tools/corpus-fetch and tools/corpus-gen already use) reads each " +
			"estate's .corpus configuration directory/directories - see spec.go for " +
			"the directory list and the run.sh line each was traced against - and " +
			"every instance's managed-resource type is collected across the whole " +
			"resolved module tree, local and registry alike. A handful of estates " +
			"whose crossing script writes its own root wiring with a heredoc " +
			"(rather than deploying a .corpus directory verbatim) add a text scan of " +
			"that script for literal resource blocks - see estateSpec.ScanScript. No " +
			"gauntlet run, no docker, no cloud calls: every estate's types come from " +
			"its committed or fetched configuration, not from a live plan or state file.",
	}

	distinct := map[string]bool{}
	for _, spec := range estateSpecs {
		row, err := scanEstate(ctx, root, spec)
		if err != nil {
			return Artifact{}, fmt.Errorf("estate %q: %w", spec.Name, err)
		}
		for _, t := range row.Types {
			distinct[t] = true
		}
		art.Estates = append(art.Estates, row)
	}
	sort.Slice(art.Estates, func(i, j int) bool { return art.Estates[i].Name < art.Estates[j].Name })

	art.Totals.Estates = len(art.Estates)
	art.Totals.DistinctTypes = len(distinct)

	cohortTypes, err := cohortResourceTypes(root)
	if err != nil {
		art.Totals.TypesInNoCohort = -1
	} else {
		n := 0
		for t := range distinct {
			if !cohortTypes[t] {
				n++
			}
		}
		art.Totals.TypesInNoCohort = n
	}

	return art, nil
}

// Write renders art as canonical (gofmt-stable, deterministic key order via
// the struct field order above) indented JSON and writes it to
// filepath.Join(root, ArtifactPath).
func Write(root string, art Artifact) error {
	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(root, ArtifactPath), data, 0o644)
}

// Read loads the committed artifact.
func Read(root string) (Artifact, error) {
	data, err := os.ReadFile(filepath.Join(root, ArtifactPath))
	if err != nil {
		return Artifact{}, err
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		return Artifact{}, err
	}
	return art, nil
}
