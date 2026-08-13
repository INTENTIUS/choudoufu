// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The untaggable-admitted render (issue #54): live/LIMITATIONS.md's
// "Untaggable types cannot be removed by the sweep" entry used to be
// derived only against live/survey.json's curated 68, which meant a
// registry-ratified type outside that roster (aws_lambda_layer_version, the
// three ECR registry singletons, and more) was genuinely untaggable but
// could not be added to the doc without the derivation test failing on a
// type it never rostered - the split internal/live/stamp/stamp_test.go
// carried as untaggableOutsideCuratedSurvey. live/survey-full.json (issue
// #41) carries taggability for the provider's entire resource-type roster,
// so the derivation now reads that artifact instead: every admitted type
// (identity.AdmittedTypes) whose row there has no top-level tags argument,
// full stop, with no roster boundary left to carve around.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// spanUntaggableAdmitted is the "Untaggable types cannot be removed by the
// sweep" entry's type enumeration, in live/LIMITATIONS.md.
const spanUntaggableAdmitted = "untaggable-admitted"

// untaggableAdmittedTypes reads live/survey-full.json and returns the
// admitted types with no top-level tags argument at all, sorted -
// identity.AdmittedTypes is already sorted, and this preserves that order
// rather than asserting it a second time.
func untaggableAdmittedTypes(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, surveyFullJSONRel)) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return nil, fmt.Errorf("reading %s (regenerate with `go run ./tools/survey-gen -all`): %w", surveyFullJSONRel, err)
	}
	var full Survey
	if err := json.Unmarshal(data, &full); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", surveyFullJSONRel, err)
	}
	taggable := make(map[string]bool, len(full.Types))
	known := make(map[string]bool, len(full.Types))
	for _, row := range full.Types {
		taggable[row.Type] = row.Signals.Taggable
		known[row.Type] = true
	}

	var out []string
	for _, typeName := range identity.AdmittedTypes() {
		// GitHub issue #73's RECORD_ADMITTED logical types (null_resource,
		// terraform_data, the time_* and random_* rows
		// internal/live/identity/table_recordbacked.go admits) are not AWS
		// provider resource types at all - they belong to the null, time and
		// random providers - so live/survey-full.json, which carries only
		// the AWS provider's own roster, has no row for any of them and
		// never will. "Taggable" is not even a meaningful question for a
		// type whose identity is the persisted micro-state record itself,
		// not a live AWS object with a tags argument to have or lack, so
		// this derivation skips them rather than treating their absence
		// from the AWS survey as the drift it is for every other admitted
		// type.
		if entry, ok := identity.LookupType(typeName); ok && entry.RecordBacked {
			continue
		}
		if !known[typeName] {
			return nil, fmt.Errorf("%s has no row for admitted type %s; regenerate with `go run ./tools/survey-gen -all`", surveyFullJSONRel, typeName)
		}
		if !taggable[typeName] {
			out = append(out, typeName)
		}
	}
	return out, nil
}

// renderUntaggableAdmitted is the entry's prose enumeration: backtick-quoted
// type names, comma-separated with a non-Oxford "and" before the last -
// the convention the hand-written entry already used.
func renderUntaggableAdmitted(types []string) string {
	return wrapIndented(joinWithAnd(backtickTypes(types), false), 76, "")
}
