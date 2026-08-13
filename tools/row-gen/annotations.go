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

	"github.com/intentius/choudoufu/internal/live/identity"
)

// annotations.json (this package's own tools/row-gen/annotations.json, not
// a live/ pinned-evidence artifact - it is this tool's own ruling ledger,
// the same relationship tools/mapping-gen/carve-seed.json has to that
// package) is rowgen-convergence's Phase 3 ratchet mechanism: a mismatch
// between row-gen's fresh proposal and a ratified internal/live/identity
// table.go entry is either something a future rule could derive
// mechanically (row-gen's job to eventually close) or a genuine human
// ruling recorded with its own evidence (aws_elasticsearch_domain staying
// separate from aws_opensearch_domain is the kind of call this is for -
// nothing in the CFN registry or the provider's own docs says two
// resources naming the same underlying service should or should not share
// a table row; that is a maintainer's own scoping decision). This file is
// reserved for the second kind only. A type whose mismatch is instead
// caused by live/import-grammar.json's own scrape falling short of the
// provider's full doc page (see convergence.go's ScrapeGap) does not
// belong here - see convergence_test.go's own doc comment for why.
//
// Every entry needs a reason and the evidence it rests on, both prose:
// machine-checked only in that TestAnnotationsAgreeWithMismatches
// (convergence_test.go) fails the moment an annotated type's mismatch
// disappears (annotation gone stale - the type should be dropped from this
// file) or a type here is not admitted at all (a typo, or the ratified
// entry moved on since).

// annotation is one reasoned ruling: why this type's mismatch against
// row-gen's fresh proposal is a human call, not a gap in the tool.
type annotation struct {
	Reason   string `json:"reason"`
	Evidence string `json:"evidence"`
}

type annotationsArtifact struct {
	Rulings map[string]annotation `json:"rulings"`
}

// loadAnnotations reads tools/row-gen/annotations.json. A missing file is
// not an error - convergence still runs, just with a wider unannotated
// count until the file exists.
func loadAnnotations(path string) (map[string]annotation, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]annotation{}, nil
		}
		return nil, err
	}
	var art annotationsArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	if art.Rulings == nil {
		return map[string]annotation{}, nil
	}
	for tf, a := range art.Rulings {
		if a.Reason == "" || a.Evidence == "" {
			return nil, fmt.Errorf("%s: %s has an empty reason or evidence field; an annotation without both is a shortcut, not a ruling", path, tf)
		}
	}
	return art.Rulings, nil
}

// validateAnnotations is TestAnnotationsAgreeWithMismatches's engine
// (convergence_test.go): every annotated type must (a) actually be admitted
// and (b) actually carry a genuine mismatch in art - an annotation for a
// type that no longer disagrees with row-gen's fresh proposal is stale and
// must be deleted, not left to quietly exempt some future, unrelated
// mismatch.
func validateAnnotations(art convergenceArtifact, annotations map[string]annotation) []string {
	mismatched := make(map[string]bool, len(art.Types))
	for _, row := range art.Types {
		if !row.Matched {
			mismatched[row.TFType] = true
		}
	}

	var problems []string
	tfTypes := make([]string, 0, len(annotations))
	for tf := range annotations {
		tfTypes = append(tfTypes, tf)
	}
	sort.Strings(tfTypes)
	for _, tf := range tfTypes {
		if _, ok := identity.DefaultTable[tf]; !ok {
			problems = append(problems, fmt.Sprintf("%s: annotated but not in identity.DefaultTable at all", tf))
			continue
		}
		if !mismatched[tf] {
			problems = append(problems, fmt.Sprintf("%s: annotated but row-gen's fresh proposal now matches the ratified entry; the annotation is stale, delete it", tf))
		}
	}
	return problems
}
