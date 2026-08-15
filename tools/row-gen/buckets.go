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
)

// live/rowgen-buckets.json is the classifier's bucket counts as a committed
// artifact, so a document generator can render them without reaching into
// this tool's classifier (issue #139). survey-gen reads it for
// live/COVERAGE.md's layers table; -emit writes it, which keeps it inside
// `just tables` with no extra stage.
const bucketsJSONRel = "live/rowgen-buckets.json"

// bucketsArtifact mirrors [summary], plus the mapped total, with JSON names.
type bucketsArtifact struct {
	GeneratedBy        string `json:"generated_by"`
	Mapped             int    `json:"mapped"`
	ServerAssigned     int    `json:"server_assigned"`
	ClientNamed        int    `json:"client_named"`
	Composite          int    `json:"composite"`
	NeedsHandSeparator int    `json:"needs_hand_separator"`
	FoldChild          int    `json:"fold_child"`
	EvidenceOnly       int    `json:"evidence_only"`
}

// bucketsFromProposals is the pure computation, shared by the writer and the
// drift test so neither can disagree with the other.
func bucketsFromProposals(proposals []proposal) bucketsArtifact {
	counts := tally(proposals)
	return bucketsArtifact{
		GeneratedBy:        "go run ./tools/row-gen -emit",
		Mapped:             len(proposals),
		ServerAssigned:     counts.ServerAssigned,
		ClientNamed:        counts.ClientNamed,
		Composite:          counts.Composite,
		NeedsHandSeparator: counts.NeedsHandSeparator,
		FoldChild:          counts.FoldChild,
		EvidenceOnly:       counts.EvidenceOnly,
	}
}

func writeBucketsArtifact(root string, proposals []proposal) error {
	data, err := json.MarshalIndent(bucketsFromProposals(proposals), "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, bucketsJSONRel)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return fmt.Errorf("writing %s: %w", bucketsJSONRel, err)
	}
	return nil
}
