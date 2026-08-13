// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Overlay is the curated file at tools/mapping-gen/overlay.json: the hand
// asserted facts the name heuristic in heuristic.go cannot derive on its
// own. Three disjoint tables, each keyed by TF type:
//
//   - Aliases pairs a TF type straight to its CFN type when the two names
//     just do not correspond - a legacy TF name (aws_cloudwatch_event_rule),
//     an abbreviated service (RDS/db), a dropped EC2 prefix (aws_vpc, not
//     aws_ec2_vpc), and so on.
//   - Folds names a TF type that is a property-child of a CFN parent:
//     Terraform decomposes some resources finer than CloudFormation does (an
//     S3 bucket's versioning, encryption and lifecycle sub-resources are all
//     just properties on CloudFormation's one AWS::S3::Bucket), so the child
//     carries no CFN type of its own - the map's value is the parent's CFN
//     type, which becomes the row's fold_parent.
//   - Nones names a TF type with no CFN counterpart at all, and says why
//     (a waiter, a credential, an account-wide setting CloudFormation has no
//     resource for).
//
// A TF type appearing in more than one table is almost certainly a curation
// mistake (the loader below refuses it), and every entry in every table
// must name a type that still exists in the current TF and CFN rosters -
// see the two-way staleness test in mapping_gen_test.go, which also refuses
// an alias the name heuristic has since grown to cover on its own.
type Overlay struct {
	// Aliases maps tf_type -> cfn_type.
	Aliases map[string]string `json:"aliases"`

	// Folds maps tf_type -> the parent's cfn_type (the row's fold_parent).
	Folds map[string]string `json:"folds"`

	// Nones maps tf_type -> the reason it has no CFN counterpart.
	Nones map[string]string `json:"nones"`
}

// loadOverlay reads and validates tools/mapping-gen/overlay.json.
func loadOverlay(path string) (Overlay, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return Overlay{}, err
	}
	var ov Overlay
	if err := json.Unmarshal(data, &ov); err != nil {
		return Overlay{}, fmt.Errorf("decoding %s: %w", path, err)
	}

	seen := map[string]string{}
	for _, table := range []struct {
		name string
		keys map[string]string
	}{
		{"aliases", ov.Aliases},
		{"folds", ov.Folds},
		{"nones", ov.Nones},
	} {
		for tfType, value := range table.keys {
			if value == "" {
				return Overlay{}, fmt.Errorf("%s: overlay %s entry for %s has an empty value", path, table.name, tfType)
			}
			if prior, ok := seen[tfType]; ok {
				return Overlay{}, fmt.Errorf("%s: %s appears in both %s and %s - a TF type belongs in exactly one overlay table",
					path, tfType, prior, table.name)
			}
			seen[tfType] = table.name
		}
	}
	return ov, nil
}
