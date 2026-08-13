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

	"github.com/intentius/choudoufu/internal/live/identity"
)

// mappingJSONRel is live/mapping.json (issue #43's committed artifact):
// tools/row-gen's own copy of this shape, read back here for the same
// reason - the CFN service each admitted type maps to is what batches
// admittedTypesV0's "Registry-ratified" comment sections by cohort (see
// internal/live/lint/admission.go, e.g. the "first batch, Lambda" banner
// above the five aws_lambda_* rows), and mapping.json is where that
// service name lives as data rather than as a comment string.
const mappingJSONRel = "live/mapping.json"

// mappingRow is one row of live/mapping.json.
type mappingRow struct {
	TFType     string  `json:"tf_type"`
	CFNType    *string `json:"cfn_type"`
	Via        string  `json:"via"`
	FoldParent *string `json:"fold_parent"`
}

type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

func loadMapping(path string) ([]mappingRow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art mappingArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return art.Rows, nil
}

// serviceOf is tools/row-gen/classify.go's serviceOf, verbatim: the CFN
// namespace's middle segment ("AWS::Lambda::Function" -> "Lambda").
func serviceOf(cfnType string) string {
	parts := strings.Split(cfnType, "::")
	if len(parts) >= 2 {
		return parts[1]
	}
	return cfnType
}

// defaultCohortTypes derives a cohort's admitted TF types with no -types
// flag: every type the identity table already admits
// (identity.AdmittedTypes, which internal/live/lint's admittedTypesV0 is
// pinned to exactly match - TestAdmissionTableCoversEstate,
// TestTableCoversFixtureTypes) whose mapping.json CFN service - or, for a
// via:fold row, its fold parent's CFN service - lowercases to the cohort
// name.
//
// This is what issue #56 calls "the admission table's registry-ratified
// sections": internal/live/lint/admission.go groups admittedTypesV0 into
// per-batch comment banners named after the CFN service each batch's row
// generator run classified ("Registry-ratified ... first batch, Lambda"),
// and live/mapping.json is the same service name as data. A type ratified
// by hand before the registry pipeline existed (aws_s3_bucket, aws_iam_role,
// ...) still carries a real mapping.json row - the mapping predates and is
// independent of which rows row-gen proposed - so this default is not
// scoped to "types row-gen specifically proposed", only to "types whose CFN
// service matches the cohort name and which the table already admits". A
// caller naming an exact -types list is unaffected either way.
func defaultCohortTypes(root, cohort string) ([]string, error) {
	rows, err := loadMapping(root + "/" + mappingJSONRel)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", mappingJSONRel, err)
	}

	admitted := map[string]bool{}
	for _, t := range identity.AdmittedTypes() {
		admitted[t] = true
	}

	byTFType := map[string]mappingRow{}
	for _, r := range rows {
		byTFType[r.TFType] = r
	}

	want := strings.ToLower(cohort)
	var out []string
	for _, r := range rows {
		if !admitted[r.TFType] {
			continue
		}
		cfnType := r.CFNType
		if cfnType == nil && r.FoldParent != nil {
			cfnType = r.FoldParent
		}
		if cfnType == nil {
			continue
		}
		if strings.ToLower(serviceOf(*cfnType)) != want {
			continue
		}
		out = append(out, r.TFType)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no admitted type maps to a %q CFN service in %s; pass -types explicitly", cohort, mappingJSONRel)
	}
	return out, nil
}
