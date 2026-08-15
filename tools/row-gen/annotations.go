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
// package) is rowgen-convergence's Phase 3 ratchet mechanism, and since
// issue #132's gate it is load-bearing: -emit refuses to run while any
// admitted type is neither reproduced by the classifier nor ruled here
// (buildEmitFiles). A ruling records one of three things, each with the
// evidence it rests on:
//
//   - A ratification judgment: nothing a fuller extraction could reach,
//     because the human call went against the evidence's face value
//     (aws_elasticsearch_domain staying separate from aws_opensearch_domain
//     is the canonical shape; a ratified-empty IdentityAttrs where the
//     schema names a required attribute is another).
//   - A stated extraction gap: the evidence exists on the provider's doc
//     page but no current proposal shape or parse reaches it. Pre-gate
//     these were kept OUT of this file so the scrape-gap count stayed pure
//     extractor debt; under the gate they are ruled here too, and the
//     ScrapeGap flag in live/rowgen-convergence.json remains the measure
//     of which rulings are of this kind.
//   - No evidence path at all: the type has no fresh proposal to disagree
//     with (rowgen-convergence's not_in_mapped_set - a cfn-unmodeled or
//     tf-only mapping row, or a record-backed effects type outside the AWS
//     evidence sources entirely).
//
// Every entry needs a reason, the evidence inspected, and an exit: what a
// fuller extraction would have to capture to retire the ruling (or the
// issue that states it). The exit is what keeps this ledger a list of
// named extractor gaps rather than a list of accepted losses. Machine
// checks: TestAnnotationsAgreeWithMismatches (convergence_test.go) fails
// the moment an annotated type's mismatch disappears (annotation gone
// stale - the type should be dropped from this file) or a type here is
// not admitted at all; TestAnnotationCountRatchet fails when the ledger
// grows, so the count only travels downward as extractors improve.

// annotation is one reasoned ruling: why this type's row is not derived,
// what was inspected to decide that, and what would retire the ruling.
type annotation struct {
	Reason   string `json:"reason"`
	Evidence string `json:"evidence"`
	// Exit names what a fuller extraction would have to capture for this
	// ruling to be deleted - a new proposal vocabulary, a parse the doc
	// page supports but the scrape lacks, or the issue tracking it. Never
	// empty: a ruling with no exit is an accepted loss, which this ledger
	// exists to refuse.
	Exit string `json:"exit"`
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
		if a.Reason == "" || a.Evidence == "" || a.Exit == "" {
			return nil, fmt.Errorf("%s: %s has an empty reason, evidence or exit field; an annotation without all three is a shortcut, not a ruling", path, tf)
		}
	}
	return art.Rulings, nil
}

// validateAnnotations is TestAnnotationsAgreeWithMismatches's engine
// (convergence_test.go): every annotated type must (a) actually be admitted
// and (b) actually carry a genuine mismatch in art, OR have no fresh
// proposal at all (rowgen-convergence's not_in_mapped_set: absent from
// art.Types entirely, which is the gate's other unreproduced shape) - an
// annotation for a type row-gen's fresh proposal now matches is stale and
// must be deleted, not left to quietly exempt some future, unrelated
// mismatch.
func validateAnnotations(art convergenceArtifact, annotations map[string]annotation) []string {
	compared := make(map[string]bool, len(art.Types))
	mismatched := make(map[string]bool, len(art.Types))
	for _, row := range art.Types {
		compared[row.TFType] = true
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
		if compared[tf] && !mismatched[tf] {
			problems = append(problems, fmt.Sprintf("%s: annotated but row-gen's fresh proposal now matches the ratified entry; the annotation is stale, delete it", tf))
		}
	}
	return problems
}
