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
// live/rowgen-mismatches.json has TestMismatchLedgerMatchesCommitted,
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
			// 37 -> 34 on 2026-09-15 (issue #1133): the classifier now
			// consults discovery.TaggingAPIUnservedType before crediting
			// taggability with a tag-filtered-list route, and three
			// aws_iam_ types the survey previously called "marker" moved
			// out - aws_iam_instance_profile, aws_iam_policy and
			// aws_iam_role. No other type moved; aws_iam_ is the only
			// service taggingAPIUnservedServices names today.
			"marker":       34,
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
			//
			// 9 -> 12 on 2026-09-15 (issue #1133): all three "marker"
			// movers above land here rather than on "moves to Ops" -
			// aws_iam_instance_profile through Cloud Control's unscoped
			// AWS::IAM::InstanceProfile list, aws_iam_policy and
			// aws_iam_role each through their own native list resource.
			// "moves to Ops" is unchanged at 1 in this artifact; the one
			// aws_iam_ type that lands there instead
			// (aws_iam_service_linked_role) is not in SURVEY.md's curated
			// 68-type roster, so it shows up only in survey-full.json's
			// expectation below.
			"enumerable, unbindable": 12,
			// 2 -> 1 and 4 -> 5 on 2026-08-17, one classifier change:
			// aws_acm_certificate_validation's hand exclusion in
			// opsExcluded was withdrawn by ruling. Classified from its
			// own schema it is parent-derived - the provider's identity
			// schema requires exactly certificate_arn, which is a
			// required argument of the type and refers to
			// aws_acm_certificate. Its resolution did not move: the type
			// was never in identity.MarkerlessTypes and
			// identity.Derivable already admitted it, so the resolver was
			// already producing PARENT_DERIVED for it while the artifact
			// said Ops.
			"moves to Ops":    1,
			"parent-derived":  5,
			"account-derived": 2,
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
			// 786 -> 778 on 2026-09-15 (issue #1133): the classifier now
			// consults discovery.TaggingAPIUnservedType before crediting
			// taggability with a tag-filtered-list route, and eight
			// aws_iam_ types the survey previously called "marker" moved
			// out - aws_iam_instance_profile, aws_iam_openid_connect_provider,
			// aws_iam_policy, aws_iam_role, aws_iam_saml_provider,
			// aws_iam_server_certificate, aws_iam_service_linked_role and
			// aws_iam_virtual_mfa_device. No other type moved; aws_iam_ is
			// the only service taggingAPIUnservedServices names today.
			"marker": 778,
			// 702 -> 583. 118 rows moved to "enumerable, unbindable"
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
			// 580 -> 579 and 47 -> 48 on 2026-08-17, the same single
			// classifier change the survey.json block above records:
			// aws_acm_certificate_validation off opsExcluded and onto
			// parent-derived from its own schema.
			//
			// 579 -> 561 on 2026-08-18, with no classifier change: the same
			// at-pin staleness aws_ecs_capacity_provider records below, this
			// time from three row-gen ratification commits that each gave
			// the identity table a Component naming a cloud value before
			// this artifact was next regenerated - a09e033a78
			// (aws_s3_account_public_access_block), 5d00709589/#245
			// (aws_lakeformation_lf_tag_expression here, plus three more
			// counted under enumerable, unbindable below) and
			// 028fca304d/#245, ratifying 16 more region- or
			// account-id-only singletons: aws_apprunner_default_auto_scaling_configuration_version,
			// aws_auditmanager_account_registration,
			// aws_devopsguru_event_sources_config,
			// aws_devopsguru_service_integration,
			// aws_ec2_allowed_images_settings, aws_glue_resource_policy,
			// aws_iot_event_configurations, aws_kinesis_account_settings,
			// aws_macie2_classification_export_configuration,
			// aws_observabilityadmin_telemetry_evaluation,
			// aws_observabilityadmin_telemetry_evaluation_for_organization,
			// aws_sagemaker_servicecatalog_portfolio_status,
			// aws_servicequotas_auto_management, aws_xray_encryption_config
			// and aws_xray_trace_segment_destination. All 18 rows landed on
			// account-derived directly; none passed through any other path
			// first.
			//
			// 561 -> 562 on 2026-09-15, part of the same #1133 regeneration
			// above: aws_iam_service_linked_role, one of the eight "marker"
			// movers, has no native list resource and no Cloud Control list
			// handler at all, so it lands here rather than on "enumerable,
			// unbindable" with the other seven.
			"moves to Ops":   562,
			"client-named":   117,
			"parent-derived": 48,
			// 143 -> 142: aws_cloudwatch_otel_enrichment, the fifth mover.
			// 142 -> 138 on 2026-08-17: the unique-name discovery leg
			// (internal/live/discovery/uniquename.go) landed, and the
			// classifier learned to read internal/live/identity's
			// UniqueName field instead of asserting nothing can bind a
			// listing. Four rows moved to the new "unique-name" token -
			// aws_cloudfront_cache_policy, aws_cloudfront_origin_request_policy,
			// aws_cloudfront_response_headers_policy and
			// aws_route53_cidr_collection, the same four the identity table
			// already carried a UniqueName entry for. No other row moved:
			// aws_cloudfront_origin_access_control has no UniqueName entry
			// (no source documents its name as account-and-region unique)
			// and stays here.
			//
			// 138 -> 135 on 2026-08-18, part of the same 579 -> 561
			// regeneration above: 5d00709589/#245 gave three more types a
			// Component naming a cloud value, which classify.go's
			// account-derived branch (read before the discovery fallback
			// that produced this token) now wins for -
			// aws_lakeformation_lf_tag, aws_observabilityadmin_telemetry_enrichment
			// and aws_s3control_object_lambda_access_point.
			//
			// 135 -> 142 on 2026-09-15 (issue #1133): seven of the eight
			// "marker" movers above land here - aws_iam_policy and
			// aws_iam_role through their own native list resource,
			// aws_iam_instance_profile, aws_iam_openid_connect_provider,
			// aws_iam_saml_provider, aws_iam_server_certificate and
			// aws_iam_virtual_mfa_device each through Cloud Control's
			// unscoped list for their mapped CFN type
			// (AWS::IAM::InstanceProfile, AWS::IAM::OIDCProvider,
			// AWS::IAM::SAMLProvider, AWS::IAM::ServerCertificate,
			// AWS::IAM::VirtualMFADevice). The eighth mover,
			// aws_iam_service_linked_role, has neither and lands on "moves
			// to Ops" above instead.
			"enumerable, unbindable": 142,
			// The four movers above, the whole membership of the new token.
			"unique-name": 4,
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
			//
			// 27 -> 48 on 2026-08-18: the 21 rows named in the moves to Ops
			// and enumerable, unbindable comments above (18 + 3), all
			// already-ratified account/region singletons the identity table
			// carried before this artifact was next regenerated.
			"account-derived": 48,
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
