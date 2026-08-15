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
)

// mappingRow is one row of live/mapping.json (issue #43's committed
// artifact): tools/mapping-gen/mapping.go's Row, read back rather than
// imported, since that package is main and unexported.
type mappingRow struct {
	TFType     string  `json:"tf_type"`
	CFNType    *string `json:"cfn_type"`
	Via        string  `json:"via"`
	FoldParent *string `json:"fold_parent"`

	// Note is mapping-gen's own prose for a row it declined to map -
	// aws_security_group_rule's "both AWS::EC2::SecurityGroup{Ingress,Egress}
	// are already claimed by the split per-direction TF types". Carried into
	// the unmapped proposal's evidence so a reader of row-gen's report sees
	// why no CFN model backs the row, instead of an unexplained blank.
	Note *string `json:"note"`
}

type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

// cfnModelledVia is the set of live/mapping.json via values that promise a
// cfn_type (or, for fold, a fold_parent): the rows classifyAll may read
// live/registry.json evidence for. Every other via - cfn-unmodeled, tf-only,
// deprecated-service, none - is a TF type CloudFormation does not model, and
// classifyUnmapped handles it.
var cfnModelledVia = map[string]bool{
	"name": true, "alias": true, "service-alias": true, "former2": true, "fold": true,
}

// loadMapping reads live/mapping.json and returns EVERY row, sorted by
// tf_type for deterministic output.
//
// It used to return only the cfnModelledVia rows - the "mapped set" - and
// silently drop the rest. That made the CloudFormation registry the gate on
// whether row-gen considered a type at all, which it should never have been:
// the registry is one evidence source among four (it, the provider's own
// identity schema via live/survey-full.json, the provider's own import
// documentation via live/import-grammar.json, and the carve seed), and it is
// not the one that decides whether a TF type EXISTS. 439 of the provider's
// 1699 types - every via:cfn-unmodeled, via:tf-only, via:deprecated-service
// and via:none row - were invisible to every mode of this tool because
// CloudFormation happens not to model them, including types whose Import
// documentation states their identity outright. classifyUnmapped classifies
// those from the documentation alone; see its doc comment.
func loadMapping(path string) ([]mappingRow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art mappingArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := append([]mappingRow(nil), art.Rows...)
	sort.Slice(out, func(i, j int) bool { return out[i].TFType < out[j].TFType })
	return out, nil
}
