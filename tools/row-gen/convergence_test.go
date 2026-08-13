// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// unannotatedMismatchRatchetMax is rowgen-convergence's own downward
// ratchet, the "delete-me ratchet idiom" tools/mapping-gen's own
// unclassifiedRatchetMax already uses for live/mapping.json's unclassified
// count (mapping_gen_test.go): the highest live/rowgen-convergence.json's
// summary.unannotated_mismatches may be. This pass originally measured 170
// genuine mismatches over 571 admitted types, after merging
// ratify-sagemaker/ratify-governance/ratify-media. Merging this branch
// itself landed after a further eight ratification batches
// (ratify-data-movement, ratify-networking-advanced, ratify-databases,
// ratify-security, ratify-ec2-networking, ratify-iot,
// ratify-ai-location/stragglers/connect-euc among them) had already raised
// the admitted count to 652 - none of those batches ever passed through
// this convergence check, since it did not exist yet when they were
// ratified, so their own row-gen/DefaultTable disagreements show up here
// for the first time as 207 unannotated mismatches (125 scrape-gap, the
// rest the same still-mechanical gaps the original comment named: fold-child
// Components derivation, order-unrecoverable composites). This is a reviewed
// bump reflecting that backlog, not a quality regression introduced by this
// merge - see tools/row-gen/annotations.json's own doc comment for why none
// of the 207 is annotated yet. Lower this constant to match
// live/rowgen-convergence.json's own committed count whenever a future
// change (a wider importdocs-gen scrape, a fold-child Components rule,
// annotations.json gaining real rulings) closes some of the gap. Raising
// it back up again needs its own reviewed reason, not a silent increase -
// TestUnannotatedMismatchRatchet below fails the build the moment a
// regeneration would do that.
const unannotatedMismatchRatchetMax = 207

// TestUnannotatedMismatchRatchet reads the committed live/rowgen-
// convergence.json directly (not a fresh regeneration - see
// TestConvergenceArtifactMatchesCommitted for that check) and fails if its
// summary.unannotated_mismatches is above unannotatedMismatchRatchetMax -
// never below: a genuine drop means the constant above is stale and should
// be lowered to match.
func TestUnannotatedMismatchRatchet(t *testing.T) {
	art := loadCommittedConvergence(t)
	if art.Summary.UnannotatedMismatches > unannotatedMismatchRatchetMax {
		t.Errorf("%s's unannotated mismatch count is %d, above the ratchet ceiling of %d (unannotatedMismatchRatchetMax); a regeneration must never grow the unannotated bucket - annotate a genuine ruling in tools/row-gen/annotations.json, implement a mechanical rule, or do a reviewed bump of this constant, not a silent increase",
			convergenceJSONRel, art.Summary.UnannotatedMismatches, unannotatedMismatchRatchetMax)
	}
}

// TestConvergenceArtifactMatchesCommitted is the drift half the ratchet
// test above deliberately does not do itself (mapping_gen_test.go's own
// TestUnclassifiedCountRatchet keeps that same separation, for the same
// reason its own doc comment gives: the ratchet stays a working, cheap
// check on its own even if this drift test's shape changes later). This
// test regenerates the comparison from the checked-out sources - the same
// classifyAll pipeline -convergence uses - and requires it to match the
// committed artifact byte-for-byte modulo JSON formatting, so a change to
// classify.go, importprecedence.go or annotations.json without a
// regeneration of live/rowgen-convergence.json fails the build instead of
// silently drifting.
func TestConvergenceArtifactMatchesCommitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatalf("loadProposals: %v", err)
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		t.Fatalf("loadAnnotations: %v", err)
	}
	fresh := buildConvergence(proposals, annotations)

	committed := loadCommittedConvergence(t)

	freshJSON, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the fresh comparison: %v", err)
	}
	committedJSON, err := json.MarshalIndent(committed, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the committed comparison: %v", err)
	}
	if string(freshJSON) != string(committedJSON) {
		t.Errorf("%s has drifted from a fresh regeneration - regenerate with:\n  go run ./tools/row-gen -convergence\n\nSummary drift: committed=%+v fresh=%+v",
			convergenceJSONRel, committed.Summary, fresh.Summary)
	}
}

// TestAnnotationsAgreeWithMismatches is Phase 3's machine check:
// annotations.json's rulings apply to real, current, admitted types with a
// real, current mismatch - see validateAnnotations (annotations.go) and
// annotations.json's own doc comment. Reads the committed artifact rather
// than a fresh regeneration, the same choice TestUnannotatedMismatchRatchet
// makes, so this stays a fast, independent check.
func TestAnnotationsAgreeWithMismatches(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		t.Fatalf("loadAnnotations: %v", err)
	}
	art := loadCommittedConvergence(t)

	if problems := validateAnnotations(art, annotations); len(problems) > 0 {
		for _, p := range problems {
			t.Error(p)
		}
	}
}

// loadCommittedConvergence reads live/rowgen-convergence.json as committed,
// the shared helper TestUnannotatedMismatchRatchet, TestAnnotationsAgree-
// WithMismatches and TestConvergenceArtifactMatchesCommitted all need.
func loadCommittedConvergence(t *testing.T) convergenceArtifact {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, convergenceJSONRel))
	if err != nil {
		t.Fatalf("reading %s: %v (run: go run ./tools/row-gen -convergence)", convergenceJSONRel, err)
	}
	var art convergenceArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatalf("decoding %s: %v", convergenceJSONRel, err)
	}
	return art
}

// TestDeriveCompositeWithSeparator_OrderFromString pins
// importprecedence.go's central safety property: argument order is always
// recovered from the documented example string's own left-to-right shape,
// never trusted from the candidate list's own order - the property that
// keeps aws_api_gateway_method (registry/grammar argument order
// alphabetical, string order reversed) from being proposed with the wrong
// order, and resolves aws_networkmanager_link_association's separator and
// order correctly even though the registry's own primaryIdentifier order
// (GlobalNetworkId, DeviceId, LinkId) disagrees with the documented string
// (global_network_id, link_id, device_id).
func TestDeriveCompositeWithSeparator_OrderFromString(t *testing.T) {
	tests := []struct {
		name       string
		example    string
		sep        string
		candidates []string
		wantOK     bool
		wantOrder  []string
	}{
		{
			name:       "order recovered from the string, not the candidate list",
			example:    "global-network-0d47f6t230mz46dy4,link-444555aaabbb11223,device-07f6fd08867abc123",
			sep:        ",",
			candidates: []string{"global_network_id", "device_id", "link_id"}, // registry order: device before link
			wantOK:     true,
			wantOrder:  []string{"global_network_id", "link_id", "device_id"}, // string order: link before device
		},
		{
			name:       "arity mismatch fails closed (the aws_route trap)",
			example:    "rtb-656C65616E6F72_10.42.0.0/16",
			sep:        "_",
			candidates: []string{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id", "route_table_id"},
			wantOK:     false,
		},
		{
			name:       "opaque placeholder values carry no name token: fails closed rather than guessing",
			example:    "12345abcde/67890fghij/GET",
			sep:        "/",
			candidates: []string{"http_method", "resource_id", "rest_api_id"},
			wantOK:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc, ok := deriveCompositeWithSeparator(tt.example, tt.sep, tt.candidates)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (dc=%+v)", ok, tt.wantOK, dc)
			}
			if !ok {
				return
			}
			if len(dc.ArgsInOrder) != len(tt.wantOrder) {
				t.Fatalf("ArgsInOrder = %v, want %v", dc.ArgsInOrder, tt.wantOrder)
			}
			for i := range dc.ArgsInOrder {
				if dc.ArgsInOrder[i] != tt.wantOrder[i] {
					t.Errorf("ArgsInOrder[%d] = %q, want %q (full: %v)", i, dc.ArgsInOrder[i], tt.wantOrder[i], dc.ArgsInOrder)
				}
			}
		})
	}
}

// TestLabelForOpaqueValue pins the ARN-vs-short-id label rule
// tryArnVsIDOverride and tryOpaqueOverride both share.
func TestLabelForOpaqueValue(t *testing.T) {
	tests := []struct {
		example      string
		wantSyntax   string
		wantIdentity string
	}{
		{"arn:aws:networkmanager::123456789012:device/global-network-x/device-y", "ARN", "arn"},
		{"svc-06728e2357ea55f8a", "ID", "id"},
		{"s-12345678", "ID", "id"},
	}
	for _, tt := range tests {
		syntax, attr := labelForOpaqueValue(tt.example)
		if syntax != tt.wantSyntax || attr != tt.wantIdentity {
			t.Errorf("labelForOpaqueValue(%q) = (%q, %q), want (%q, %q)", tt.example, syntax, attr, tt.wantSyntax, tt.wantIdentity)
		}
	}
}
