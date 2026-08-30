// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/check"
)

// resourceBlockRe matches a literal `resource "type" "name"` declaration at
// the start of a line (optional leading whitespace only, so an indented
// "# resource ..." comment never matches - the same shape
// tools/estate-gen's retire_measure_test.go and drift_test.go already use
// for the same job over a different corpus). It is used only as the
// ScanScript fallback for a crossing script's own heredoc'd root wiring;
// every directory in [estateSpec.ConfigDirs] is read through
// internal/live/check.Load instead, which is the real HCL/module-graph
// parser this project already ships rather than a second one built for this
// tool.
var resourceBlockRe = regexp.MustCompile(`(?m)^\s*resource\s+"([A-Za-z_][A-Za-z0-9_]*)"\s+"`)

// loadDirTypes runs check.Load against one absolute directory and folds its
// managed-resource types into types. label is what gets recorded in Notes
// and ConfigDirs on success or failure.
func loadDirTypes(ctx context.Context, dir, label string, types map[string]bool) (used bool, unresolved int, note string) {
	if _, err := os.Stat(dir); err != nil {
		return false, 0, fmt.Sprintf("%s: %v (run \"just corpus-fetch\"?)", label, err)
	}
	result := check.Load(ctx, dir)
	if result.Config == nil {
		return false, 0, fmt.Sprintf("%s: %s", label, result.Diags.Error())
	}
	result.Config.DeepEach(func(c *configs.Config) {
		for _, r := range c.Module.ManagedResources {
			types[r.Type] = true
		}
	})
	return true, len(result.UnresolvedModules), ""
}

// scanEstate returns the distinct resource types one estate exercises, plus
// enough provenance to judge how complete the answer is.
func scanEstate(ctx context.Context, root string, spec estateSpec) (estateTypes, error) {
	types := map[string]bool{}
	var sources []string
	var configDirsUsed []string
	unresolved := 0
	var loadErrs []string

	for _, rel := range spec.ConfigDirs {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		used, unres, note := loadDirTypes(ctx, dir, rel, types)
		unresolved += unres
		if used {
			configDirsUsed = append(configDirsUsed, rel)
		}
		if note != "" {
			loadErrs = append(loadErrs, note)
		}
	}

	// corpus-sumaform-aws: see sumaform.go for why its two module
	// directories cannot be read straight out of .corpus.
	if spec.Name == "corpus-sumaform-aws" {
		base, server, cleanup, err := prepareSumaformModules(root)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Sprintf("preparing sumaform modules: %v (run \"just corpus-fetch\"?)", err))
		} else {
			defer cleanup()
			materialized := []struct{ label, dir string }{
				{"modules/base (materialized)", base},
				{"modules/server (materialized)", server},
			}
			for _, m := range materialized {
				used, unres, note := loadDirTypes(ctx, m.dir, m.label, types)
				unresolved += unres
				if used {
					configDirsUsed = append(configDirsUsed, m.label)
				}
				if note != "" {
					loadErrs = append(loadErrs, note)
				}
			}
		}
	}

	// terralith-scale: see terralith.go for why this estate's configuration
	// has to be generated before it can be read at all.
	if spec.Name == "terralith-scale" {
		dir, cleanup, err := prepareTerralith(ctx, root)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Sprintf("generating the terralith estate: %v", err))
		} else {
			defer cleanup()
			label := "tools/terralith-gen -scale " + terralithScaleGenScale + " (generated)"
			used, unres, note := loadDirTypes(ctx, dir, label, types)
			unresolved += unres
			if used {
				configDirsUsed = append(configDirsUsed, label)
			}
			if note != "" {
				loadErrs = append(loadErrs, note)
			}
		}
	}

	if len(configDirsUsed) > 0 {
		sources = append(sources, "config")
	}

	if spec.ScanScript {
		scriptPath := filepath.Join(root, "live", "e2e", spec.Name, "run.sh")
		text, err := os.ReadFile(scriptPath) //nolint:gosec // fixed repo-relative path built from the spec table
		if err != nil {
			loadErrs = append(loadErrs, fmt.Sprintf("run.sh: %v", err))
		} else {
			before := len(types)
			for _, m := range resourceBlockRe.FindAllStringSubmatch(string(text), -1) {
				types[m[1]] = true
			}
			if len(types) != before || len(configDirsUsed) == 0 {
				sources = append(sources, "script")
			}
		}
	}

	out := make([]string, 0, len(types))
	for t := range types {
		out = append(out, t)
	}
	sort.Strings(out)
	sort.Strings(sources)

	return estateTypes{
		Name:              spec.Name,
		Types:             out,
		Count:             len(out),
		Sources:           sources,
		ConfigDirs:        configDirsUsed,
		UnresolvedModules: unresolved,
		Notes:             loadErrs,
	}, nil
}
