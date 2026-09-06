// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"github.com/intentius/choudoufu/internal/live/cohorts"
)

// cohortResourceTypes returns every resource type the estate-gen cohorts
// declare - the fixtures live/cohort-acceptance.json reports pass/fail
// against. It exists so Totals.TypesInNoCohort can answer issue #435's second
// question: how many of the gauntlet board's exercised types no cohort covers
// at all.
//
// It used to text-scan live/e2e/estates/<cohort>/*.tf. Issue #699 deleted
// those committed trees - they were generator output, rendered at run time
// now - so the answer comes from the roster instead: each cohort's pinned
// -types roster plus the supporting resources the generator adds on top of
// it, which is what the deleted .tf files declared and nothing more (measured
// entry for entry against a full `estate-gen -all` render before the
// deletion). Reading the roster keeps this tool network-free and
// terraform-free, which the text scan was chosen for in the first place.
//
// live/cohort-acceptance.json is still not the source: it carries only
// failed_resources (instance addresses), never a full per-cohort type roster.
func cohortResourceTypes(root string) (map[string]bool, error) {
	_ = root // the roster is compiled in; no tree to read
	types := map[string]bool{}
	for _, t := range cohorts.FixtureTypes() {
		types[t] = true
	}
	return types, nil
}
