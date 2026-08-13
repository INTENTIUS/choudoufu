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
)

// mappingRow is the slice of live/mapping.json's row shape this tool reads
// (tools/mapping-gen/mapping.go's Row, read back rather than imported, since
// that package is main and unexported - the same duplication
// tools/row-gen/mapping.go and live/residue.go each carry their own copy of
// for the same reason).
type mappingRow struct {
	TFType  string  `json:"tf_type"`
	CFNType *string `json:"cfn_type"`
	Via     string  `json:"via"`
}

type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

// loadMappedServices reads live/mapping.json and returns the distinct CFN
// service segments named by its mapped set - every row carrying a non-null
// cfn_type (via name, alias, service-alias, or former2; a via:fold row's own
// cfn_type is null by construction and via:none carries no CFN evidence at
// all) - sorted. This is the "mapped set" issue #52 names as the fetch
// scope: the services a botocore sweep actually needs, not botocore's
// entire ~460-service catalog.
func loadMappedServices(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art mappingArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}

	seen := map[string]bool{}
	for _, r := range art.Rows {
		if r.CFNType == nil || *r.CFNType == "" {
			continue
		}
		if svc, ok := serviceOf(*r.CFNType); ok {
			seen[svc] = true
		}
	}

	out := make([]string, 0, len(seen))
	for svc := range seen {
		out = append(out, svc)
	}
	sort.Strings(out)
	return out, nil
}

// serviceOf pulls the namespace segment out of a CFN type name
// (AWS::Lambda::Function -> "Lambda", true), the same extraction
// tools/row-gen/classify.go's serviceOf performs (duplicated rather than
// imported for the same package-main reason as mappingRow above).
func serviceOf(cfnType string) (string, bool) {
	parts := strings.Split(cfnType, "::")
	if len(parts) < 2 || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
