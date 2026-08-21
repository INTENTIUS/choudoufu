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

// nonAWSAdmittedUntaggable is a named ledger, in the sense
// contributing/LIVE-TABLES.md and tools/row-gen/annotations.json's own doc
// comment describe: a ruling that genuinely cannot be derived from
// live/survey-full.json, recorded with its evidence rather than folded into
// the generated artifact. live/survey-full.json is CloudFormation-registry-
// backed and, by construction, carries only the AWS provider's roster - so
// it has no row, and never will, for a type belonging to a different
// provider entirely. Issue #326 is the first time the admission table
// crossed that line: kubernetes_cluster_role_binding, kubernetes_config_map,
// kubernetes_namespace and kubernetes_storage_class, hand-ratified in
// tools/row-gen/ratified.json from the real, current hashicorp/kubernetes
// provider docs (fetched live; the offline cache has no Kubernetes provider
// data). This is unlike the RecordBacked exemption a few lines below:
// "taggable" IS a meaningful question for these four - they are live
// cloud-adjacent objects with a real schema, not a persisted micro-state
// record - so each entry here is a ratified answer, not a skip. The answer
// was verified by reading the real provider schema with
// markers.Taggable/TagSurface: none of the four's metadata block carries a
// top-level tags attribute at all (0 of 0), the same finding
// tools/row-gen/annotations.json's own rulings for these four types record.
// Retire an entry once there is a non-AWS analogue of live/survey-full.json
// this derivation can read instead of a hand ledger - the same missing
// evidence source those four rulings' own Exit fields name.
var nonAWSAdmittedUntaggable = map[string]bool{
	"kubernetes_cluster_role_binding": true,
	"kubernetes_config_map":           true,
	"kubernetes_namespace":            true,
	"kubernetes_storage_class":        true,
}

// untaggableAdmittedTypes reads live/survey-full.json and returns two
// partitions of the admitted table, sorted - identity.AdmittedTypes is
// already sorted, and this preserves that order rather than asserting it a
// second time.
//
// The first is the admitted types with no top-level tags argument at all.
// The second is the ones the survey positively records AS taggable, which
// is a different set from "everything else": the RECORD_ADMITTED logical
// types skipped below are in neither, because they are not AWS resource
// types and "taggable" is not a question about them.
//
// Returning both is issue #130. parentReadableRoster used to derive
// eligibility subtractively, as every admitted type minus the untaggable
// ones, which made a type the survey has no row for silently eligible - so
// null_resource was published as the swept parent of seven types, while
// internal/live/discovery's own markerCapable check (default-deny, over the
// provider's live schema) would never have allowed it. Two independent
// readings of "can this type carry an ownership marker" only agree if both
// are positive tests.
func untaggableAdmittedTypes(root string) (untaggable, taggable []string, err error) {
	data, readErr := os.ReadFile(filepath.Join(root, surveyFullJSONRel)) //nolint:gosec // a fixed path in the checkout
	if readErr != nil {
		return nil, nil, fmt.Errorf("reading %s (regenerate with `go run ./tools/survey-gen -all`): %w", surveyFullJSONRel, readErr)
	}
	var full Survey
	if uErr := json.Unmarshal(data, &full); uErr != nil {
		return nil, nil, fmt.Errorf("decoding %s: %w", surveyFullJSONRel, uErr)
	}
	isTaggable := make(map[string]bool, len(full.Types))
	known := make(map[string]bool, len(full.Types))
	for _, row := range full.Types {
		isTaggable[row.Type] = row.Signals.Taggable
		known[row.Type] = true
	}

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
			// nonAWSAdmittedUntaggable is the one other case a missing row
			// is not drift: a type belonging to a provider
			// live/survey-full.json was never going to carry at all. Every
			// other unknown type is a real gap - the survey should have a
			// row for every AWS type this table admits - so it still fails
			// closed.
			if nonAWSAdmittedUntaggable[typeName] {
				untaggable = append(untaggable, typeName)
				continue
			}
			return nil, nil, fmt.Errorf("%s has no row for admitted type %s; regenerate with `go run ./tools/survey-gen -all`", surveyFullJSONRel, typeName)
		}
		if isTaggable[typeName] {
			taggable = append(taggable, typeName)
			continue
		}
		untaggable = append(untaggable, typeName)
	}
	return untaggable, taggable, nil
}

// renderUntaggableAdmitted is the entry's prose enumeration: backtick-quoted
// type names, comma-separated with a non-Oxford "and" before the last -
// the convention the hand-written entry already used.
func renderUntaggableAdmitted(types []string) string {
	return wrapIndented(joinWithAnd(backtickTypes(types), false), 76, "")
}
