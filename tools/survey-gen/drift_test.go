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

// This file is issue #169's gate. Until it existed the two survey artifacts
// had none, alone among this repository's generated artifacts -
// live/registry.json has TestRegistryCounts_MatchIssue42ReferenceValues,
// live/rowgen-convergence.json has TestConvergenceArtifactMatchesCommitted,
// the generated tables have TestEmitFilesMatchCommitted, and
// live/LIMITATIONS.md's spans have their own render check.
//
// live/pins_drift_test.go looks like it covers this and does not: it reads
// provider_version out of the artifact's OWN header, so it catches a pin
// bump nobody regenerated for and cannot catch an artifact that is stale at
// the same pin. That is exactly what had happened. Regenerating with no code
// change at all moved four of six path counts, account-derived 2 -> 19 among
// them, and nothing failed.
//
// These artifacts need a provider to regenerate, so the gate compares the
// committed file against a committed expectation rather than rebuilding it.
// That is the same shape registry-gen's reference counts use.

// surveyExpectation is what one artifact's headline numbers must be.
//
// A number here moves for one of two reasons, and they want different
// responses:
//
//   - The pinned provider release changed what it publishes. Check
//     internal/live/pins.AWSProviderVersion first; if it moved, this is an
//     ordinary regeneration and the new values go in with the artifact.
//   - The classifier changed. Then the question is whether the new
//     distribution is the intended consequence, and the commit should say
//     which types moved and why - #167 moved parent-derived 65 -> 47 and
//     said so.
//
// Either way it is an edit, never a silent adjustment.
type surveyExpectation struct {
	Rel    string
	Types  int
	Counts struct {
		Taggable       int
		ListResource   int
		IdentitySchema int
	}
	Paths map[string]int
}

var surveyExpectations = []surveyExpectation{
	{
		Rel:   "live/survey.json",
		Types: 68,
		Counts: struct {
			Taggable       int
			ListResource   int
			IdentitySchema int
		}{Taggable: 47, ListResource: 58, IdentitySchema: 61},
		Paths: map[string]int{
			"marker":       37,
			"client-named": 14,
			// 6 -> 9 and 5 -> 2 on 2026-08-16, from two classifier
			// changes in one commit. Two of the three
			// (aws_cloudfront_origin_access_control, aws_iam_group)
			// moved because the enumeration question widened from the
			// provider's native list resource alone to the two signals
			// internal/live/discovery has read since #47, the second
			// being the mapped CFN type's Cloud Control list handler.
			// The third (aws_secretsmanager_secret_version) moved
			// because its hand exclusion in opsExcluded was withdrawn by
			// ruling; it has a native list resource and had always
			// classified underneath the veto.
			"list + content match": 9,
			"moves to Ops":         2,
			"parent-derived":       4,
			"account-derived":      2,
		},
	},
	{
		Rel:   "live/survey-full.json",
		Types: 1699,
		Counts: struct {
			Taggable       int
			ListResource   int
			IdentitySchema int
		}{Taggable: 847, ListResource: 195, IdentitySchema: 479},
		Paths: map[string]int{
			// 789 -> 787: aws_comprehend_entity_recognizer and
			// aws_kinesisanalyticsv2_application moved marker ->
			// account-derived. Neither is this commit's doing - both
			// gained identity-table components naming a cloud value in
			// an earlier merge that regenerated the tables and not this
			// artifact, the same at-pin staleness the #150 note below
			// records. A regeneration was what surfaced them.
			//
			// 787 -> 786 on 2026-08-17, for the same reason a third time:
			// aws_s3control_storage_lens_configuration gained an
			// identity-table entry composing the run's account-id, so
			// cloudValuesOf now answers for it and the account-derived
			// branch wins over taggability. Four more rows moved into
			// account-derived in the same regeneration -
			// aws_bedrock_model_invocation_logging_configuration,
			// aws_cloudwatch_otel_enrichment, aws_glue_user_defined_function
			// and aws_vpc_block_public_access_options - none of them this
			// commit's doing either.
			"marker": 786,
			// 702 -> 583. 118 rows moved to "list + content match"
			// because the classifier's enumeration question now reads
			// the mapped CFN type's Cloud Control list handler as well
			// as the provider's native list resource, which is what
			// internal/live/discovery/discovery.go's scanType has done
			// since #47; the 119th mover,
			// aws_secretsmanager_secret_version, came off opsExcluded by
			// ruling and classifies on its native list resource. The 85
			// rows whose CFN list handler needs scoping input stay here,
			// with evidence that now names the input rather than
			// claiming no list exists.
			//
			// 583 -> 580 on 2026-08-17: three of the five movers above
			// (bedrock model invocation logging, glue user-defined function,
			// vpc block-public-access options) were sitting here because no
			// enumeration leg reached them, and an identity-table entry that
			// composes a cloud value outranks the enumeration question.
			"moves to Ops":   580,
			"client-named":   117,
			"parent-derived": 47,
			// 143 -> 142: aws_cloudwatch_otel_enrichment, the fifth mover.
			"list + content match": 142,
			// aws_ecs_capacity_provider moved marker -> account-derived
			// here: #150 (commit 0ca3115721) gave it IdentityAttrs whose
			// ARN folds in the run's region and account-id, which
			// classify.go reads as account-derived rather than a bare
			// server-assigned identifier. That commit regenerated the
			// identity table but not this artifact, so the two sat out of
			// step at the same provider pin - exactly the #169 shape this
			// test exists to catch, except this file's own expectations
			// are hand-synced to the committed artifact rather than
			// derived from a fresh run, so nothing caught it until a live
			// regeneration was actually run and diffed against it.
			// 22 -> 27 on 2026-08-17, the five movers named above.
			"account-derived": 27,
		},
	},
}

// TestSurveyArtifactsMatchTheirExpectations is the gate.
func TestSurveyArtifactsMatchTheirExpectations(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range surveyExpectations {
		t.Run(want.Rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, want.Rel)) //nolint:gosec // a fixed path in the checkout
			if err != nil {
				t.Fatalf("reading %s: %v", want.Rel, err)
			}
			var got Survey
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("decoding %s: %v", want.Rel, err)
			}

			if len(got.Types) != want.Types {
				t.Errorf("%s carries %d rows, want %d", want.Rel, len(got.Types), want.Types)
			}
			if got.Counts.Taggable != want.Counts.Taggable ||
				got.Counts.ListResource != want.Counts.ListResource ||
				got.Counts.IdentitySchema != want.Counts.IdentitySchema {
				t.Errorf("%s raw signals are (taggable %d, list %d, identity %d), want (%d, %d, %d)",
					want.Rel, got.Counts.Taggable, got.Counts.ListResource, got.Counts.IdentitySchema,
					want.Counts.Taggable, want.Counts.ListResource, want.Counts.IdentitySchema)
			}

			paths := map[string]int{}
			for _, row := range got.Types {
				paths[row.Path]++
			}
			for path, n := range want.Paths {
				if paths[path] != n {
					t.Errorf("%s has %d rows on path %q, want %d - regenerate (`just survey`) and, if the move is intended, edit surveyExpectations saying which of the two causes it was",
						want.Rel, paths[path], path, n)
				}
			}
			for path, n := range paths {
				if _, expected := want.Paths[path]; !expected {
					t.Errorf("%s has %d rows on unexpected path %q; add it to surveyExpectations", want.Rel, n, path)
				}
			}
		})
	}
}
