// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// scopedService is one service this artifact describes: the CFN service
// segment, the IAM names it might go by, and its own tagging verb.
type scopedService struct {
	CFNService string
	Candidates []string
	TagAction  string
}

// servicesInScope derives the roster - which services to fetch - rather than
// taking a list. It is the CFN services the admission table's own types span,
// joined to live/tag-verbs.json for each one's tagging verb and IAM-prefix
// candidates.
//
// Deriving it matters for the same reason everything else here is derived: a
// hand-listed roster silently stops covering a service the moment a
// ratification batch admits one, and nothing would say so.
func servicesInScope(root string) ([]scopedService, error) {
	admitted, err := admittedTypes(root)
	if err != nil {
		return nil, err
	}
	cfnOf, err := cfnTypeByTFType(root)
	if err != nil {
		return nil, err
	}

	wanted := map[string]bool{}
	for _, tf := range admitted {
		cfn, ok := cfnOf[tf]
		if !ok {
			continue
		}
		if parts := strings.Split(cfn, "::"); len(parts) >= 2 && parts[1] != "" {
			wanted[parts[1]] = true
		}
	}

	verbs, err := tagVerbRows(root)
	if err != nil {
		return nil, err
	}

	var out []scopedService
	for svc := range wanted {
		row, ok := verbs[svc]
		if !ok {
			// A service the admission table reaches and tag-verbs does not
			// cover is a gap in that artifact, not something to invent a
			// roster entry for. It shows up as an unresolved row.
			out = append(out, scopedService{CFNService: svc})
			continue
		}
		out = append(out, scopedService{
			CFNService: svc,
			Candidates: row.Candidates,
			TagAction:  row.Operation,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CFNService < out[j].CFNService })
	return out, nil
}

// admittedTypeRe reads the generated identity table's own keys. Parsing the
// generated source rather than importing the package keeps this tool's
// dependency surface to the artifacts, the way every other generator here
// works.
var admittedTypeRe = regexp.MustCompile(`(?m)^\t"([a-z0-9_]+)":`)

func admittedTypes(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, admissionRel)) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", admissionRel, err)
	}
	m := admittedTypeRe.FindAllStringSubmatch(string(data), -1)
	if len(m) == 0 {
		return nil, fmt.Errorf("%s yielded no admitted types; the table's shape changed and this parser did not", admissionRel)
	}
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	return out, nil
}

func cfnTypeByTFType(root string) (map[string]string, error) {
	var art struct {
		Rows []struct {
			TFType  string  `json:"tf_type"`
			CFNType *string `json:"cfn_type"`
		} `json:"rows"`
	}
	if err := decodeArtifact(filepath.Join(root, mappingRel), &art); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(art.Rows))
	for _, r := range art.Rows {
		if r.CFNType != nil && *r.CFNType != "" {
			out[r.TFType] = *r.CFNType
		}
	}
	return out, nil
}

type tagVerbRow struct {
	Operation  string
	Candidates []string
}

func tagVerbRows(root string) (map[string]tagVerbRow, error) {
	var art struct {
		Rows []struct {
			Service             string   `json:"service"`
			Operation           string   `json:"operation"`
			IAMPrefixCandidates []string `json:"iam_prefix_candidates"`
		} `json:"rows"`
	}
	if err := decodeArtifact(filepath.Join(root, tagVerbsRel), &art); err != nil {
		return nil, err
	}
	out := make(map[string]tagVerbRow, len(art.Rows))
	for _, r := range art.Rows {
		out[r.Service] = tagVerbRow{Operation: r.Operation, Candidates: r.IAMPrefixCandidates}
	}
	return out, nil
}

func decodeArtifact(path string, into any) error {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}
