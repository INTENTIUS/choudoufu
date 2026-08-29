// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
)

// cohortResourceTypes returns every resource type declared anywhere under
// live/e2e/estates/<cohort>/*.tf - the estate-gen fixture cohorts
// live/cohort-acceptance.json reports pass/fail against. It exists so
// Totals.TypesInNoCohort can answer issue #435's second question: how many
// of the gauntlet board's exercised types no cohort covers at all.
//
// Those fixtures are committed (not gitignored, unlike .corpus - see
// tools/estate-gen), so this needs no corpus-fetch and no network, and a
// plain text scan is enough: cohort fixtures are estate-gen's own generated
// output, flat per-cohort directories of *.tf files with no module
// indirection to resolve (tools/estate-gen/drift_test.go and
// retire_measure_test.go already read them the same way, with the same
// regex this package uses in scan.go). live/cohort-acceptance.json itself
// carries only failed_resources (instance addresses, not a full per-cohort
// type roster), so the fixtures are the source of truth for "does any
// cohort declare this type" - not that artifact.
func cohortResourceTypes(root string) (map[string]bool, error) {
	types := map[string]bool{}
	estatesDir := filepath.Join(root, "live", "e2e", "estates")

	entries, err := os.ReadDir(estatesDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tfs, err := filepath.Glob(filepath.Join(estatesDir, e.Name(), "*.tf"))
		if err != nil {
			return nil, err
		}
		for _, tf := range tfs {
			content, err := os.ReadFile(tf) //nolint:gosec // fixture paths inside the checkout
			if err != nil {
				return nil, err
			}
			for _, m := range resourceBlockRe.FindAllStringSubmatch(string(content), -1) {
				types[m[1]] = true
			}
		}
	}
	return types, nil
}
