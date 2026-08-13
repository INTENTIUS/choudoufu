// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// tfRosterArtifact is the slice of live/survey-full.json's shape (Survey in
// tools/survey-gen/classify.go) this loader needs - just the type names.
type tfRosterArtifact struct {
	Types []struct {
		Type string `json:"type"`
	} `json:"types"`
}

// loadTFRoster reads the TF-side roster: live/survey-full.json's whole
// provider resource-type list (issue #41), the roster mapping-gen's
// headline counts are measured over.
func loadTFRoster(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art tfRosterArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make([]string, 0, len(art.Types))
	for _, t := range art.Types {
		out = append(out, t.Type)
	}
	return out, nil
}

// surveyFullArtifact is the slice of live/survey-full.json's shape (Survey
// in tools/survey-gen/classify.go) loadIdentitySchemaSignals needs: each
// type's identity_schema signal, issue #53's own corroboration input for
// the tf-only mechanical classifier (taxonomy.go) - whether the provider
// ships a resource identity schema for the type at all, not just its name.
type surveyFullArtifact struct {
	Types []struct {
		Type    string `json:"type"`
		Signals struct {
			IdentitySchema bool `json:"identity_schema"`
		} `json:"signals"`
	} `json:"types"`
}

// loadIdentitySchemaSignals reads live/survey-full.json (the same file
// loadTFRoster reads for its type roster) for its per-type identity_schema
// signal alone.
func loadIdentitySchemaSignals(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art surveyFullArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make(map[string]bool, len(art.Types))
	for _, t := range art.Types {
		out[t.Type] = t.Signals.IdentitySchema
	}
	return out, nil
}

// loadCuratedRoster reads just the type-name column of live/SURVEY.md's
// hand-written per-type table: the curated 68 the pin test measures
// against. A minimal parser on purpose - tools/survey-gen's own readRoster
// lives in a different main package (Go commands cannot import one
// another) and carries columns (Path, Status, Source tier) this tool has no
// use for.
func loadCuratedRoster(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "| aws_") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) == 0 {
			continue
		}
		typeName := strings.TrimSpace(cells[0])
		if seen[typeName] {
			return nil, fmt.Errorf("%s appears twice in %s's per-type table", typeName, path)
		}
		seen[typeName] = true
		out = append(out, typeName)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no per-type rows found in %s", path)
	}
	return out, nil
}
